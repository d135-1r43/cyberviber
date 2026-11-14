package serial

import (
	"bufio"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"image"
	"image/color"
	"io"
	"sync"
	"testing"
	"time"
)

// fakePort is the Mac side of a pipe pair; the test plays the device on the other side.
type fakePort struct {
	r      *io.PipeReader // daemon reads what the device writes
	w      *io.PipeWriter // daemon writes to the device
	closed chan struct{}
	once   sync.Once
}

func (p *fakePort) Read(b []byte) (int, error)  { return p.r.Read(b) }
func (p *fakePort) Write(b []byte) (int, error) { return p.w.Write(b) }
func (p *fakePort) Close() error {
	p.once.Do(func() { close(p.closed); p.r.Close(); p.w.Close() })
	return nil
}

type device struct {
	in  *bufio.Reader  // lines from the daemon
	out *io.PipeWriter // lines to the daemon
}

func (d *device) send(t *testing.T, s string) {
	t.Helper()
	if _, err := d.out.Write([]byte(s + "\n")); err != nil {
		t.Fatal(err)
	}
}

// next returns the next message from the daemon with the given type.
func (d *device) next(t *testing.T, typ string) map[string]any {
	t.Helper()
	got := make(chan map[string]any, 1)
	go func() {
		for {
			line, err := d.in.ReadBytes('\n')
			if err != nil {
				close(got)
				return
			}
			var m map[string]any
			if json.Unmarshal(line, &m) == nil && m["type"] == typ {
				got <- m
				return
			}
		}
	}()
	select {
	case m, ok := <-got:
		if !ok {
			t.Fatalf("port closed while waiting for %q", typ)
		}
		return m
	case <-time.After(3 * time.Second):
		t.Fatalf("timeout waiting for %q", typ)
	}
	return nil
}

func newFake() (*fakePort, *device) {
	devR, macW := io.Pipe()
	macR, devW := io.Pipe()
	return &fakePort{r: macR, w: macW, closed: make(chan struct{})}, &device{in: bufio.NewReader(devR), out: devW}
}

func startLink(t *testing.T, ports map[string]*fakePort) (*Link, context.CancelFunc) {
	t.Helper()
	l := New("auto", 115200)
	var mu sync.Mutex
	gen := 0
	l.Snapshot = func() []byte {
		mu.Lock()
		defer mu.Unlock()
		gen++
		b, _ := json.Marshal(map[string]any{"type": "sessions", "now": gen, "sessions": []any{}})
		return b
	}
	l.Focus = func(id string) (bool, string) {
		if id == "a1" {
			return true, ""
		}
		return false, "not-found"
	}
	l.List = func() []string { return []string{"/dev/cu.other", "/dev/cu.device"} }
	l.Open = func(path string, _ int) (Port, error) {
		p, ok := ports[path]
		if !ok {
			return nil, errors.New("busy")
		}
		return p, nil
	}
	l.Logf = t.Logf
	ctx, cancel := context.WithCancel(context.Background())
	go l.Run(ctx)
	return l, cancel
}

func TestShouldHandshakeAndPushSessions(t *testing.T) {
	port, dev := newFake()
	l, cancel := startLink(t, map[string]*fakePort{"/dev/cu.device": port})
	defer cancel()

	dev.next(t, "hello")
	dev.send(t, "ets Jul 29 2019 boot garbage {")
	dev.send(t, `{"type":"hello","device":"cyberviber","fw":"0.1.0","layout":"waybar","theme":"tokyo-night"}`)
	dev.next(t, "sessions")

	st := l.Status()
	if !st.Connected || st.Port != "/dev/cu.device" || st.Firmware != "0.1.0" {
		t.Fatalf("status = %+v", st)
	}
	l.Notify()
	dev.next(t, "sessions")
}

func TestShouldTrackBacklightSettingsFromHello(t *testing.T) {
	port, dev := newFake()
	l, cancel := startLink(t, map[string]*fakePort{"/dev/cu.device": port})
	defer cancel()

	dev.next(t, "hello")
	dev.send(t, `{"type":"hello","device":"cyberviber","fw":"0.3.0","brightness":75,"dim_after":0}`)
	dev.next(t, "sessions")
	if st := l.Status(); st.Brightness != 75 || st.DimAfter == nil || *st.DimAfter != 0 {
		t.Fatalf("status = %+v", st)
	}

	dev.send(t, `{"type":"hello","device":"cyberviber","fw":"0.3.0","brightness":100,"dim_after":15}`)
	dev.next(t, "sessions")
	if st := l.Status(); st.Brightness != 100 || st.DimAfter == nil || *st.DimAfter != 15 {
		t.Fatalf("status after settings change = %+v", st)
	}
}

func TestShouldAnswerFocus(t *testing.T) {
	port, dev := newFake()
	_, cancel := startLink(t, map[string]*fakePort{"/dev/cu.device": port})
	defer cancel()

	dev.next(t, "hello")
	dev.send(t, `{"type":"hello","device":"cyberviber","fw":"0.1.0"}`)
	dev.next(t, "sessions")

	dev.send(t, `{"type":"focus","id":"a1"}`)
	if m := dev.next(t, "focused"); m["id"] != "a1" || m["ok"] != true {
		t.Fatalf("focused = %v", m)
	}
	dev.send(t, `{"type":"focus","id":"zz"}`)
	if m := dev.next(t, "focused"); m["ok"] != false || m["reason"] != "not-found" {
		t.Fatalf("focused = %v", m)
	}
}

func TestShouldIgnoreDevicesThatDontSayHello(t *testing.T) {
	other, otherDev := newFake()
	go io.Copy(io.Discard, otherDev.in) // a silent device that swallows everything
	_, cancel := startLink(t, map[string]*fakePort{"/dev/cu.other": other})
	defer cancel()

	select {
	case <-other.closed:
	case <-time.After(helloTimeout + 2*time.Second):
		t.Fatal("silent port was not closed")
	}
}

func TestShouldReleaseAndPause(t *testing.T) {
	port, dev := newFake()
	l, cancel := startLink(t, map[string]*fakePort{"/dev/cu.device": port})
	defer cancel()

	dev.next(t, "hello")
	dev.send(t, `{"type":"hello","device":"cyberviber","fw":"0.1.0"}`)
	dev.next(t, "sessions")

	l.Release(time.Minute)
	select {
	case <-port.closed:
	case <-time.After(2 * time.Second):
		t.Fatal("port not closed after release")
	}
	deadline := time.Now().Add(2 * time.Second)
	for l.Status().Released == 0 {
		if time.Now().After(deadline) {
			t.Fatalf("status = %+v", l.Status())
		}
		time.Sleep(10 * time.Millisecond)
	}
	if l.Status().Connected {
		t.Fatal("still connected")
	}
}

func TestShouldReconnectAfterDisconnect(t *testing.T) {
	port, dev := newFake()
	ports := map[string]*fakePort{"/dev/cu.device": port}
	var mu sync.Mutex
	l := New("auto", 115200)
	l.Snapshot = func() []byte { return []byte(`{"type":"sessions","now":1,"sessions":[]}`) }
	l.List = func() []string { return []string{"/dev/cu.device"} }
	l.Open = func(path string, _ int) (Port, error) {
		mu.Lock()
		defer mu.Unlock()
		return ports[path], nil
	}
	l.Logf = t.Logf
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go l.Run(ctx)

	dev.next(t, "hello")
	dev.send(t, `{"type":"hello","device":"cyberviber","fw":"0.1.0"}`)
	dev.next(t, "sessions")

	port2, dev2 := newFake()
	mu.Lock()
	ports["/dev/cu.device"] = port2
	mu.Unlock()
	dev.out.CloseWithError(errors.New("unplugged"))

	dev2.next(t, "hello")
	dev2.send(t, `{"type":"hello","device":"cyberviber","fw":"0.1.1"}`)
	dev2.next(t, "sessions")
	if fw := l.Status().Firmware; fw != "0.1.1" {
		t.Fatalf("fw = %q", fw)
	}
}

func TestShouldAssembleScreenshot(t *testing.T) {
	port, dev := newFake()
	l, cancel := startLink(t, map[string]*fakePort{"/dev/cu.device": port})
	defer cancel()
	dev.next(t, "hello")
	dev.send(t, `{"type":"hello","device":"cyberviber","fw":"0.2.0"}`)
	dev.next(t, "sessions")

	type result struct {
		img image.Image
		err error
	}
	res := make(chan result, 1)
	go func() {
		img, err := l.Screenshot(context.Background())
		res <- result{img, err}
	}()
	dev.next(t, "screenshot")
	// 2×4 image in two strips; pure red is 0xF800 (little-endian 00 F8)
	strip := base64.StdEncoding.EncodeToString([]byte{0x00, 0xF8, 0x00, 0xF8, 0x00, 0xF8, 0x00, 0xF8})
	dev.send(t, `{"type":"shot","w":2,"h":4,"y":0,"n":2,"data":"`+strip+`"}`)
	dev.send(t, `{"type":"shot","w":2,"h":4,"y":2,"n":2,"data":"AAB#garbled"}`) // damaged in transit
	dev.send(t, `{"type":"shot_end"}`)
	if m := dev.next(t, "screenshot"); m["y"] != 2.0 || m["rows"] != 2.0 {
		t.Fatalf("re-request = %v", m)
	}
	dev.send(t, `{"type":"shot","w":2,"h":4,"y":2,"n":2,"data":"`+strip+`"}`)
	dev.send(t, `{"type":"shot_end"}`)
	select {
	case r := <-res:
		if r.err != nil {
			t.Fatal(r.err)
		}
		if c := color.RGBAModel.Convert(r.img.At(1, 3)).(color.RGBA); c != (color.RGBA{255, 0, 0, 255}) {
			t.Fatalf("pixel = %v", c)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("screenshot did not complete")
	}
}

func TestShouldSendConfigOnlyWhenConnected(t *testing.T) {
	l := New("auto", 115200)
	if err := l.Send([]byte(`{"type":"config"}`)); !errors.Is(err, ErrNotConnected) {
		t.Fatalf("err = %v", err)
	}
	port, dev := newFake()
	l2, cancel := startLink(t, map[string]*fakePort{"/dev/cu.device": port})
	defer cancel()
	dev.next(t, "hello")
	dev.send(t, `{"type":"hello","device":"cyberviber","fw":"0.2.0"}`)
	dev.next(t, "sessions")
	if err := l2.Send([]byte(`{"type":"config","layout":"tiling"}`)); err != nil {
		t.Fatal(err)
	}
	if m := dev.next(t, "config"); m["layout"] != "tiling" {
		t.Fatalf("config = %v", m)
	}
}

func TestShouldReleaseThePortWhenAnotherAppBoots(t *testing.T) {
	// given cyberviber is connected
	port, dev := newFake()
	l, cancel := startLink(t, map[string]*fakePort{"/dev/cu.device": port})
	defer cancel()
	dev.next(t, "hello")
	dev.send(t, `{"type":"hello","device":"cyberviber","fw":"0.4.2"}`)
	dev.next(t, "sessions")

	// when the launcher restarts the board into another app, which says hello as itself
	dev.send(t, `{"type":"hello","device":"pcstats","fw":"0.1.0"}`)

	// then the daemon lets go of the port, so that app's host program can open it
	select {
	case <-port.closed:
	case <-time.After(3 * time.Second):
		t.Fatal("port kept open for another app")
	}
	if l.Status().Connected {
		t.Fatal("must not count as connected")
	}
}

func TestShouldGiveUpProbingAtOnceWhenAnotherAppAnswers(t *testing.T) {
	// given another app runs on the board
	port, dev := newFake()
	_, cancel := startLink(t, map[string]*fakePort{"/dev/cu.device": port})
	defer cancel()
	dev.next(t, "hello")
	start := time.Now()

	// when
	dev.send(t, `{"type":"hello","device":"pcstats","fw":"0.1.0"}`)

	// then
	select {
	case <-port.closed:
		if d := time.Since(start); d >= helloTimeout {
			t.Fatalf("took %v", d)
		}
	case <-time.After(helloTimeout):
		t.Fatal("port held until the hello timeout")
	}
}
