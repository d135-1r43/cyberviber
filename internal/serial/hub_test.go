package serial

import (
	"bufio"
	"context"
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestHubSocketMatchesCydOnLinux(t *testing.T) {
	env := map[string]string{"XDG_RUNTIME_DIR": "/run/user/1000"}

	if got := hubSocket("linux", func(k string) string { return env[k] }, 1000); got != "/run/user/1000/cyd-hub.sock" {
		t.Fatalf("socket = %q", got)
	}
}

func TestHubSocketMatchesCydOnMacOS(t *testing.T) {
	env := map[string]string{"TMPDIR": "/var/folders/xy/abc/T/"}

	if got := hubSocket("darwin", func(k string) string { return env[k] }, 501); got != "/tmp/cyd-hub-501.sock" {
		t.Fatalf("socket = %q", got)
	}
}

func fakeHub(t *testing.T) (string, chan net.Conn) {
	t.Helper()
	dir, err := os.MkdirTemp("/tmp", "cvhub")
	if err != nil {
		t.Fatal(err)
	}
	sock := filepath.Join(dir, "hub.sock")
	ln, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close(); os.RemoveAll(dir) })
	conns := make(chan net.Conn, 4)
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			conns <- c
		}
	}()
	return sock, conns
}

func accepted(t *testing.T, conns chan net.Conn) net.Conn {
	t.Helper()
	select {
	case c := <-conns:
		t.Cleanup(func() { c.Close() })
		return c
	case <-time.After(5 * time.Second):
		t.Fatal("daemon did not connect to the hub")
	}
	return nil
}

func nextOfType(t *testing.T, r *bufio.Reader, typ string) map[string]any {
	t.Helper()
	for {
		line, err := r.ReadBytes('\n')
		if err != nil {
			t.Fatalf("waiting for %q: %v", typ, err)
		}
		var m map[string]any
		if json.Unmarshal(line, &m) == nil && m["type"] == typ {
			return m
		}
	}
}

func TestShouldListOnlyTheHubWhileItRuns(t *testing.T) {
	sock, _ := fakeHub(t)

	if got := listPorts("auto", sock); len(got) != 1 || got[0] != sock {
		t.Fatalf("ports = %v", got)
	}
}

func TestShouldIgnoreAHubSocketThatIsNotThere(t *testing.T) {
	for _, p := range listPorts("auto", "/tmp/no-such-cyd-hub.sock") {
		if p == "/tmp/no-such-cyd-hub.sock" {
			t.Fatalf("ports = %v", p)
		}
	}
}

func TestShouldTalkToTheBoardThroughTheHub(t *testing.T) {
	sock, conns := fakeHub(t)
	l := New("auto", 115200)
	l.Hub = sock
	l.Snapshot = func() []byte { return []byte(`{"type":"sessions","sessions":[]}`) }
	l.Logf = t.Logf
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go l.Run(ctx)

	c := accepted(t, conns)
	r := bufio.NewReader(c)
	if h := nextOfType(t, r, "hello"); h["daemon"] != "cyberviber" {
		t.Fatalf("hello = %v", h)
	}
	c.Write([]byte(`{"type":"hello","device":"cyberviber","fw":"0.4.3"}` + "\n"))
	nextOfType(t, r, "sessions")

	if st := l.Status(); !st.Connected || st.Port != sock || st.Firmware != "0.4.3" {
		t.Fatalf("status = %+v", st)
	}
}

func TestShouldMoveToTheHubOnceItAppears(t *testing.T) {
	dir, err := os.MkdirTemp("/tmp", "cvhub")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)
	sock := filepath.Join(dir, "hub.sock")
	port, dev := newFake()
	l := New("auto", 115200)
	l.Hub = sock
	l.HubPoll = 20 * time.Millisecond
	l.Snapshot = func() []byte { return []byte(`{"type":"sessions","sessions":[]}`) }
	l.Logf = t.Logf
	l.List = func() []string { return []string{"/dev/cu.device"} }
	l.Open = func(string, int) (Port, error) { return port, nil }
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go l.Run(ctx)
	dev.next(t, "hello")
	dev.send(t, `{"type":"hello","device":"cyberviber","fw":"0.4.3"}`)
	dev.next(t, "sessions")

	ln, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()

	select {
	case <-port.closed:
	case <-time.After(3 * time.Second):
		t.Fatal("daemon kept the serial port although the hub came up")
	}
}

func TestShouldAskTheHubToReleaseThePort(t *testing.T) {
	sock, conns := fakeHub(t)
	l := New("auto", 115200)
	l.Hub = sock

	go l.Release(90 * time.Second)

	c := accepted(t, conns)
	line, err := bufio.NewReader(c).ReadBytes('\n')
	if err != nil {
		t.Fatal(err)
	}
	var req map[string]any
	if json.Unmarshal(line, &req) != nil || req["type"] != "hub" || req["cmd"] != "release" || req["seconds"] != 90.0 {
		t.Fatalf("request = %s", line)
	}
}

func TestShouldKeepAnExplicitPortAlthoughTheHubRuns(t *testing.T) {
	sock, _ := fakeHub(t)
	port, dev := newFake()
	l := New("/dev/cu.device", 115200)
	l.Hub = sock
	l.HubPoll = 20 * time.Millisecond
	l.Snapshot = func() []byte { return []byte(`{"type":"sessions","sessions":[]}`) }
	l.Logf = t.Logf
	l.Open = func(string, int) (Port, error) { return port, nil }
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go l.Run(ctx)
	dev.next(t, "hello")
	dev.send(t, `{"type":"hello","device":"cyberviber","fw":"0.4.3"}`)
	dev.next(t, "sessions")

	select {
	case <-port.closed:
		t.Fatal("daemon dropped the configured port for the hub")
	case <-time.After(200 * time.Millisecond):
	}
}

func TestShouldAskTheHubToOpenTheLauncherMenu(t *testing.T) {
	sock, conns := fakeHub(t)
	got := make(chan []byte, 1)
	go func() {
		c := <-conns
		defer c.Close()
		line, _ := bufio.NewReader(c).ReadBytes('\n')
		got <- line
		c.Write([]byte(`{"ok":true}` + "\n"))
	}()

	if err := HubLauncher(sock); err != nil {
		t.Fatal(err)
	}
	if line := <-got; string(line) != `{"cmd":"launcher","type":"hub"}`+"\n" {
		t.Fatalf("request = %s", line)
	}
}

func TestShouldReportTheHubsError(t *testing.T) {
	sock, conns := fakeHub(t)
	go func() {
		c := <-conns
		defer c.Close()
		bufio.NewReader(c).ReadBytes('\n')
		c.Write([]byte(`{"ok":false,"error":"no board"}` + "\n"))
	}()

	if err := HubLauncher(sock); err == nil || err.Error() != "hub: no board" {
		t.Fatalf("err = %v", err)
	}
}

func TestShouldNotSeeAHubBehindAStaleSocket(t *testing.T) {
	sock, _ := fakeHub(t)
	if !HubRunning(sock) {
		t.Fatal("hub runs")
	}
	if HubRunning(sock + ".gone") {
		t.Fatal("no hub there")
	}
}
