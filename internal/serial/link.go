// Package serial connects the daemon to the device over USB serial: port discovery,
// newline-delimited JSON framing, reconnect after unplug, and releasing the port for flashing.
package serial

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"path/filepath"
	"sort"
	"sync"
	"time"

	bugst "go.bug.st/serial"

	"github.com/d135-1r43/cyberviber/internal/protocol"
)

// Port is an open serial connection. Read must return (0, nil) or an error within a short
// timeout so the reader can notice Close.
type Port interface {
	io.ReadWriteCloser
}

// Patterns are the device names of the USB-UART bridges used on ESP32 boards (CH340, CP210x).
var Patterns = []string{"/dev/cu.usbserial-*", "/dev/cu.wchusbserial*", "/dev/cu.SLAB_USBtoUART*", "/dev/ttyUSB*", "/dev/ttyACM*"}

const (
	helloTimeout = 6 * time.Second // the board may reset on open; boot takes ~1 s
	keepalive    = 5 * time.Second
	rescan       = 2 * time.Second
	maxLine      = 16 << 10
)

// Status describes the device connection.
type Status struct {
	Connected  bool   `json:"connected"`
	Port       string `json:"port,omitempty"`
	Firmware   string `json:"fw,omitempty"`
	Layout     string `json:"layout,omitempty"`
	Theme      string `json:"theme,omitempty"`
	Brightness int    `json:"brightness,omitempty"`     // percent
	DimAfter   *int   `json:"dim_after,omitempty"`      // minutes, 0 = never; nil with firmware before 0.3.0
	USB        string `json:"usb,omitempty"`            // "top" or "bottom"; empty with firmware before 0.4.0
	Released   int64  `json:"released_until,omitempty"` // unix seconds, while the port is released
}

type Link struct {
	Path     string        // "auto" or a device path
	Baud     int           // 115200
	Snapshot func() []byte // current sessions message (one JSON object)
	Focus    func(id string) (ok bool, reason string)
	Version  string // daemon version, sent in hello

	// Open and List are replaceable for tests.
	Open func(path string, baud int) (Port, error)
	List func() []string
	Logf func(format string, args ...any)

	notify  chan struct{}
	release chan time.Duration
	out     chan []byte

	mu     sync.Mutex
	status Status
	shot   *shot // screenshot in progress
}

// ErrNotConnected is returned when a message needs the device and none is connected.
var ErrNotConnected = errors.New("device not connected")

func (l *Link) init() {
	if l.notify == nil {
		l.notify = make(chan struct{}, 1)
		l.release = make(chan time.Duration, 1)
		l.out = make(chan []byte, 8)
	}
	if l.Open == nil {
		l.Open = openPort
	}
	if l.List == nil {
		l.List = func() []string { return listPorts(l.Path) }
	}
	if l.Logf == nil {
		l.Logf = log.Printf
	}
	if l.Baud == 0 {
		l.Baud = 115200
	}
}

// New returns a Link ready for Notify/Release before Run starts.
func New(path string, baud int) *Link {
	l := &Link{Path: path, Baud: baud}
	l.init()
	return l
}

// Notify sends the current session list to the device (coalesced, never blocks).
func (l *Link) Notify() {
	select {
	case l.notify <- struct{}{}:
	default:
	}
}

// Release closes the port and keeps it closed for d, so a flasher can use it.
func (l *Link) Release(d time.Duration) {
	select {
	case <-l.release:
	default:
	}
	l.release <- d
}

// Send queues one message (a JSON object) for the connected device.
func (l *Link) Send(msg []byte) error {
	if !l.Status().Connected {
		return ErrNotConnected
	}
	select {
	case l.out <- msg:
		return nil
	default:
		return errors.New("device busy")
	}
}

func (l *Link) Status() Status {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.status
}

func (l *Link) setStatus(s Status) {
	l.mu.Lock()
	l.status = s
	l.mu.Unlock()
}

// Run scans for the device and serves it until ctx is done.
func (l *Link) Run(ctx context.Context) {
	l.init()
	var pausedUntil time.Time
	for ctx.Err() == nil {
		if wait := time.Until(pausedUntil); wait > 0 {
			l.setStatus(Status{Released: pausedUntil.Unix()})
			if !l.sleep(ctx, wait, &pausedUntil) {
				return
			}
			continue
		}
		l.setStatus(Status{})
		for _, path := range l.List() {
			c, hello, err := l.probe(ctx, path)
			if err != nil {
				continue
			}
			l.Logf("serial: device on %s (fw %s)", path, hello.FW)
			l.setStatus(Status{Connected: true, Port: path, Firmware: hello.FW, Layout: hello.Layout, Theme: hello.Theme,
				Brightness: hello.Bright, DimAfter: hello.Dim, USB: hello.USB})
			rel, err := l.serve(ctx, c)
			c.close()
			l.setStatus(Status{})
			switch {
			case rel > 0:
				l.Logf("serial: port %s released for %s", path, rel)
				pausedUntil = time.Now().Add(rel)
			case ctx.Err() == nil:
				l.Logf("serial: %s disconnected: %v", path, err)
			}
			break
		}
		if time.Until(pausedUntil) <= 0 && !l.sleep(ctx, rescan, &pausedUntil) {
			return
		}
	}
}

// sleep waits for d; a Release extends the pause. Returns false when ctx is done.
func (l *Link) sleep(ctx context.Context, d time.Duration, pausedUntil *time.Time) bool {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-t.C:
	case rel := <-l.release:
		*pausedUntil = time.Now().Add(rel)
	}
	return true
}

type hello struct {
	Type   string `json:"type"`
	Device string `json:"device"`
	FW     string `json:"fw"`
	Layout string `json:"layout"`
	Theme  string `json:"theme"`
	Bright int    `json:"brightness"`
	Dim    *int   `json:"dim_after"`
	USB    string `json:"usb"`
}

type inbound struct {
	Type string `json:"type"`
	ID   string `json:"id"`
}

// conn owns an open port: a reader goroutine delivering lines and a write lock.
type conn struct {
	port  Port
	lines chan []byte
	errc  chan error
	wmu   sync.Mutex
	once  sync.Once
	done  chan struct{}
}

func newConn(p Port) *conn {
	c := &conn{port: p, lines: make(chan []byte, 16), errc: make(chan error, 1), done: make(chan struct{})}
	go c.read()
	return c
}

func (c *conn) read() {
	buf := make([]byte, 1024)
	var line []byte
	for {
		n, err := c.port.Read(buf)
		select {
		case <-c.done:
			return
		default:
		}
		if err != nil {
			c.errc <- err
			return
		}
		data := buf[:n]
		for {
			i := bytes.IndexByte(data, '\n')
			if i < 0 {
				line = append(line, data...)
				if len(line) > maxLine {
					line = line[:0] // runaway garbage
				}
				break
			}
			line = append(line, data[:i]...)
			data = data[i+1:]
			if l := bytes.TrimSpace(line); len(l) > 0 && l[0] == '{' {
				select {
				case c.lines <- append([]byte(nil), l...):
				case <-c.done:
					return
				}
			}
			line = line[:0]
		}
	}
}

func (c *conn) write(msg []byte) error {
	c.wmu.Lock()
	defer c.wmu.Unlock()
	_, err := c.port.Write(append(msg, '\n'))
	return err
}

func (c *conn) close() {
	c.once.Do(func() {
		close(c.done)
		c.port.Close()
	})
}

func (l *Link) helloMsg() []byte {
	b, _ := json.Marshal(map[string]string{"type": "hello", "daemon": "cyberviber", "version": l.Version})
	return b
}

// probe opens path and waits for the firmware's hello. The daemon sends its own hello every
// second, which the firmware answers; the firmware also says hello once after boot.
func (l *Link) probe(ctx context.Context, path string) (*conn, hello, error) {
	p, err := l.Open(path, l.Baud)
	if err != nil {
		return nil, hello{}, err
	}
	c := newConn(p)
	deadline := time.NewTimer(helloTimeout)
	defer deadline.Stop()
	tick := time.NewTicker(time.Second)
	defer tick.Stop()
	c.write(l.helloMsg())
	for {
		select {
		case <-ctx.Done():
			c.close()
			return nil, hello{}, ctx.Err()
		case <-deadline.C:
			c.close()
			return nil, hello{}, errors.New("no hello")
		case err := <-c.errc:
			c.close()
			return nil, hello{}, err
		case <-tick.C:
			if err := c.write(l.helloMsg()); err != nil {
				c.close()
				return nil, hello{}, err
			}
		case line := <-c.lines:
			var h hello
			if json.Unmarshal(line, &h) != nil || h.Type != "hello" {
				continue
			}
			if foreign(h) {
				c.close()
				return nil, hello{}, fmt.Errorf("board runs %s", h.Device)
			}
			if h.Device == "cyberviber" {
				return c, h, nil
			}
		}
	}
}

// foreign reports a hello from another app on the same board (e.g. one started by a launcher);
// its own host program needs the port then.
func foreign(h hello) bool { return h.Device != "" && h.Device != "cyberviber" }

// serve pushes session lists and handles device messages. It returns a positive duration
// when the port was released, otherwise the error that ended the connection.
func (l *Link) serve(ctx context.Context, c *conn) (time.Duration, error) {
	for len(l.out) > 0 { // drop messages meant for an earlier connection
		<-l.out
	}
	if err := c.write(l.Snapshot()); err != nil {
		return 0, err
	}
	tick := time.NewTicker(keepalive)
	defer tick.Stop()
	werr := make(chan error, 1)
	send := func(b []byte) {
		if err := c.write(b); err != nil {
			select {
			case werr <- err:
			default:
			}
		}
	}
	for {
		select {
		case <-ctx.Done():
			return 0, ctx.Err()
		case d := <-l.release:
			return d, nil
		case err := <-c.errc:
			return 0, err
		case err := <-werr:
			return 0, err
		case b := <-l.out:
			send(b)
		case <-l.notify:
			if l.shotBusy() { // writing while the device streams a screenshot damages the stream
				continue
			}
			send(l.Snapshot())
			tick.Reset(keepalive)
		case <-tick.C:
			if !l.shotBusy() {
				send(l.Snapshot())
			}
		case line := <-c.lines:
			var m inbound
			if json.Unmarshal(line, &m) != nil {
				continue
			}
			switch m.Type {
			case "hello": // device rebooted
				var h hello
				if json.Unmarshal(line, &h) == nil {
					if foreign(h) {
						return 0, fmt.Errorf("board runs %s now", h.Device)
					}
					st := l.Status()
					st.Firmware, st.Layout, st.Theme, st.Brightness, st.DimAfter, st.USB = h.FW, h.Layout, h.Theme, h.Bright, h.Dim, h.USB
					l.setStatus(st)
				}
				send(l.Snapshot())
			case "shot":
				l.shotLine(line)
			case "shot_end":
				l.shotEnd(send)
			case "log": // debug builds of the firmware, e.g. touch calibration
				l.Logf("device: %s", line)
			case "focus":
				if m.ID == "" {
					continue
				}
				go func(id string) {
					res := protocol.Focused{Type: "focused", ID: id, Reason: "not-implemented"}
					if l.Focus != nil {
						res.OK, res.Reason = l.Focus(id)
					}
					b, _ := json.Marshal(res)
					send(b)
				}(m.ID)
			}
		}
	}
}

func listPorts(path string) []string {
	if path != "" && path != "auto" {
		return []string{path}
	}
	var out []string
	for _, p := range Patterns {
		m, _ := filepath.Glob(p)
		out = append(out, m...)
	}
	sort.Strings(out)
	return out
}

// openPort opens the port with DTR and RTS inactive, so the ESP32 auto-reset circuit
// (EN and IO0 driven from RTS/DTR) does not reset the board or put it into the bootloader.
func openPort(path string, baud int) (Port, error) {
	p, err := bugst.Open(path, &bugst.Mode{
		BaudRate:          baud,
		DataBits:          8,
		Parity:            bugst.NoParity,
		StopBits:          bugst.OneStopBit,
		InitialStatusBits: &bugst.ModemOutputBits{DTR: false, RTS: false},
	})
	if err != nil {
		return nil, fmt.Errorf("open %s: %w", path, err)
	}
	if err := p.SetReadTimeout(500 * time.Millisecond); err != nil {
		p.Close()
		return nil, err
	}
	return p, nil
}
