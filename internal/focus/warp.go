package focus

import (
	"fmt"
	"strings"
	"time"

	"github.com/d135-1r43/cyberviber/internal/session"
)

// Warp has no AppleScript dictionary and its tabs are invisible to the accessibility API; only its
// windows are, titled after their active tab. So a title marker written to the session's TTY finds
// the window only while the session's tab is the active one there. Raising a window through System
// Events needs Accessibility permission and Automation access to System Events for the daemon.
type warp struct{}

func warpRaiseByName(appPID int, marker string) string {
	return fmt.Sprintf(`tell application "System Events"
	set ps to every process whose unix id is %d
	if (count of ps) is 0 then return ""
	set ws to every window of item 1 of ps whose name contains %s
	if (count of ws) is 0 then return ""
	perform action "AXRaise" of item 1 of ws
	return "ok"
end tell`, appPID, asString(marker))
}

func (warp) Focus(f *Focuser, _ session.Session, t Terminal, inMux bool) (int, string) {
	if f.goos() != "darwin" {
		return hyprWindow{}.Focus(f, session.Session{}, t, inMux)
	}
	defer f.activate(t)
	if t.TTY == "" {
		return 0, "tab-not-found"
	}
	marker := "cv-" + randomHex(6)
	if writeTitle(t.TTY, marker) != nil {
		return 0, "tab-not-found"
	}
	gui := warpGUI(f, t)
	for i := 0; i < 6; i++ {
		time.Sleep(50 * time.Millisecond)
		out, err := f.Run("osascript", "-e", warpRaiseByName(gui, marker))
		if err != nil {
			if strings.Contains(string(out), "assistive access") { // -1719 alone also means "invalid index"
				return 0, "no-accessibility"
			}
			if strings.Contains(string(out), "Not authorized to send Apple events") || strings.Contains(string(out), "-1743") {
				return 0, "no-automation"
			}
			continue
		}
		if strings.TrimSpace(string(out)) == "ok" {
			return 0, ""
		}
	}
	return 0, "tab-not-found"
}

// warpGUI returns the Warp process that owns the windows. The shells run under Warp's
// terminal-server, a child process from the same bundle, which is where ResolveTerminal stops.
func warpGUI(f *Focuser, t Terminal) int {
	pid := t.AppPID
	for i := 0; i < 8; i++ {
		p, err := f.Get(pid)
		if err != nil {
			break
		}
		parent, err := f.Get(p.PPID)
		if err != nil || !strings.HasPrefix(parent.Exe, t.Bundle+"/") {
			break
		}
		pid = parent.PID
	}
	return pid
}
