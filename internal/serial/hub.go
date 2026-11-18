package serial

import (
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"time"
)

// HubSocket is where cyd's hub (cyd-launcher) listens; keep it in sync with hub.SocketPath there.
func HubSocket(getenv func(string) string) string {
	return hubSocket(runtime.GOOS, getenv, os.Getuid())
}

func hubSocket(goos string, getenv func(string) string, uid int) string {
	if dir := getenv("XDG_RUNTIME_DIR"); dir != "" {
		return filepath.Join(dir, "cyd-hub.sock")
	}
	dir := os.TempDir()
	if goos == "darwin" {
		dir = "/tmp"
	}
	return filepath.Join(dir, fmt.Sprintf("cyd-hub-%d.sock", uid))
}

func isSocket(path string) bool {
	if path == "" {
		return false
	}
	fi, err := os.Stat(path)
	return err == nil && fi.Mode()&os.ModeSocket != 0
}

func hubRelease(sock string, d time.Duration) error {
	return hubCommand(sock, map[string]any{"type": "hub", "cmd": "release", "seconds": int(d / time.Second)})
}

// HubLauncher asks the hub to reset the board into cyd-launcher's menu.
func HubLauncher(sock string) error {
	return hubCommand(sock, map[string]any{"type": "hub", "cmd": "launcher"})
}

// HubRunning reports whether a hub answers on sock; a killed hub can leave the socket file behind.
func HubRunning(sock string) bool {
	if !isSocket(sock) {
		return false
	}
	c, err := net.DialTimeout("unix", sock, 500*time.Millisecond)
	if err != nil {
		return false
	}
	c.Close()
	return true
}

func hubCommand(sock string, req map[string]any) error {
	c, err := net.DialTimeout("unix", sock, time.Second)
	if err != nil {
		return err
	}
	defer c.Close()
	_ = c.SetDeadline(time.Now().Add(3 * time.Second))
	b, _ := json.Marshal(req)
	if _, err := c.Write(append(b, '\n')); err != nil {
		return err
	}
	var reply struct {
		OK    bool   `json:"ok"`
		Error string `json:"error"`
	}
	if err := json.NewDecoder(c).Decode(&reply); err != nil {
		return err
	}
	if !reply.OK {
		return fmt.Errorf("hub: %s", reply.Error)
	}
	return nil
}
