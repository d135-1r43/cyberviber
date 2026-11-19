package focus

import (
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestShouldFindSocketClientsWithRealSS(t *testing.T) {
	// given this process connected to its own socket
	if _, err := exec.LookPath("ss"); err != nil {
		t.Skip("ss (iproute2) not installed")
	}
	sock := filepath.Join(shortTempDir(t), "herdr-client.sock")
	ln, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	c, err := net.Dial("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	s, err := ln.Accept()
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	comm, _ := os.ReadFile("/proc/self/comm")
	f := &Focuser{GOOS: "linux", Run: func(name string, args ...string) ([]byte, error) {
		return exec.Command(name, args...).Output()
	}}

	// when
	got := f.socketClients(string(comm[:len(comm)-1]), func(p string, _ int) bool { return p == sock })

	// then
	if len(got) != 1 || got[0] != os.Getpid() {
		t.Fatalf("got %v, want [%d]", got, os.Getpid())
	}
}
