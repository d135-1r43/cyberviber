package focus

import (
	"runtime"
	"strconv"
	"sync"

	"github.com/d135-1r43/cyberviber/internal/mux"
	"github.com/d135-1r43/cyberviber/internal/proc"
	"github.com/d135-1r43/cyberviber/internal/session"
)

// Result of a focus request. Reason is set when OK is false.
type Result struct {
	OK       bool
	Reason   string // not-found, no-process, detached, unknown-terminal, no-plugin, no-remote-control, no-accessibility, no-automation, tab-not-found
	Terminal string
	Mux      string
	Tab      int
}

// Multiplexer focuses a pane inside a terminal multiplexer. Multiplexer servers run detached from
// the terminal, so it returns the PIDs of the attached clients: they lead to the terminal tab.
// No clients means the session is detached.
type Multiplexer interface {
	Focus(f *Focuser, loc mux.Location) (clients []int)
}

// App focuses the window or tab of a terminal app. It returns the tab index when the app reports
// one, and a Reason when the tab was not found.
type App interface {
	Focus(f *Focuser, s session.Session, t Terminal, inMux bool) (tab int, reason string)
}

var (
	multiplexers = map[string]Multiplexer{"zellij": zellij{}, "herdr": herdr{}}
	apps         = map[string]App{"tabby": tabby{}, "ghostty": ghostty{}, "kitty": kitty{}, "warp": warp{}}
)

type Focuser struct {
	Lookup   func(id string) (session.Session, bool)
	Get      func(pid int) (proc.Proc, error)
	Env      func(pid int) map[string]string
	Run      func(name string, args ...string) ([]byte, error)
	TabbyDir string
	GOOS     string // defaults to runtime.GOOS

	mu         sync.Mutex
	ghosttyIDs map[string]string // session id → Ghostty terminal id
}

// Focus brings the session's terminal to the front: multiplexer pane first, then the terminal tab.
func (f *Focuser) Focus(id string) Result {
	s, ok := f.Lookup(id)
	if !ok {
		return Result{Reason: "not-found"}
	}
	if s.PID == 0 {
		return Result{Reason: "no-process"}
	}

	loc := s.Mux
	if loc.IsZero() {
		env := f.Env(s.PID)
		loc, _ = mux.Detect(func(k string) string { return env[k] })
	}
	res := Result{Mux: loc.Kind}

	anchor := s.PID
	if m, ok := multiplexers[loc.Kind]; ok {
		clients := m.Focus(f, loc)
		if len(clients) == 0 {
			res.Reason = "detached"
			return res
		}
		anchor = clients[0]
	}

	term, ok := ResolveTerminal(anchor, f.Get)
	if !ok {
		res.Reason = "unknown-terminal"
		return res
	}
	res.Terminal = term.App

	app, ok := apps[term.App]
	if !ok && f.goos() == "linux" {
		app, ok = hyprWindow{}, true
	}
	if !ok {
		f.activate(term)
		res.Reason = "unknown-terminal"
		return res
	}
	res.Tab, res.Reason = app.Focus(f, s, term, !loc.IsZero())
	res.OK = res.Reason == ""
	return res
}

func (f *Focuser) goos() string {
	if f.GOOS != "" {
		return f.GOOS
	}
	return runtime.GOOS
}

// activate brings the terminal app to the front. On Linux only Hyprland is supported: Wayland
// does not let an app raise itself, the compositor has to.
func (f *Focuser) activate(t Terminal) {
	if f.goos() == "darwin" {
		f.Run("open", "-a", t.Bundle)
		return
	}
	f.hyprFocus("pid:" + strconv.Itoa(t.AppPID))
}
