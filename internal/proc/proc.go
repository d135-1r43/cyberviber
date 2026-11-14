// Package proc reads the process table: sysctl on macOS, /proc on Linux (no cgo, no ps).
package proc

import (
	"errors"
	"strings"
	"syscall"
)

type Proc struct {
	PID  int
	PPID int
	Comm string // kernel name; for Claude Code this is its version, e.g. "2.1.316"
	Name string // basename of argv[0], e.g. "claude", "-zsh"; falls back to Comm
	Exe  string // executable path from the kernel, if readable
	TTY  string // e.g. "ttys022", "" when the process has no controlling terminal
}

// IsClaude reports whether the process is the Claude Code CLI.
func (p Proc) IsClaude() bool {
	return strings.EqualFold(p.Name, "claude") || strings.EqualFold(p.Comm, "claude") ||
		strings.Contains(p.Exe, "/claude/versions/")
}

// IsVibe reports whether the process is the Mistral Vibe CLI (its Rust TUI; `vibe` execs into it).
func (p Proc) IsVibe() bool {
	return strings.EqualFold(p.Name, "vibe-rs") || strings.EqualFold(p.Comm, "vibe-rs")
}

// Chain returns pid and its ancestors up to (excluding) PID 1.
func Chain(pid int) []Proc { return chain(pid, Get) }

func chain(pid int, get func(int) (Proc, error)) []Proc {
	var out []Proc
	for i := 0; i < 64 && pid > 1; i++ {
		p, err := get(pid)
		if err != nil {
			break
		}
		out = append(out, p)
		pid = p.PPID
	}
	return out
}

// Alive reports whether a process with this PID exists.
func Alive(pid int) bool {
	err := syscall.Kill(pid, 0)
	return err == nil || errors.Is(err, syscall.EPERM)
}

// FindAgent walks up from pid (usually the hook's parent) to the Claude Code or Vibe process.
func FindAgent(pid int) (Proc, bool) { return findAgent(pid, Get) }

var shells = map[string]bool{"sh": true, "bash": true, "zsh": true, "dash": true, "fish": true, "nu": true}

func findAgent(pid int, get func(int) (Proc, error)) (Proc, bool) {
	var fallback *Proc
	for i := 0; i < 32 && pid > 1; i++ {
		p, err := get(pid)
		if err != nil {
			break
		}
		if p.IsClaude() || p.IsVibe() {
			return p, true
		}
		name := p.Name
		if name == "" {
			name = p.Comm
		}
		if fallback == nil && !shells[strings.TrimPrefix(name, "-")] {
			fp := p
			fallback = &fp
		}
		pid = p.PPID
	}
	if fallback != nil {
		return *fallback, true
	}
	return Proc{}, false
}
