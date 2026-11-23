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
// Events needs Accessibility permission for the daemon (System Settings → Privacy & Security).
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
	for i := 0; i < 6; i++ {
		time.Sleep(50 * time.Millisecond)
		out, err := f.Run("osascript", "-e", warpRaiseByName(t.AppPID, marker))
		if err != nil {
			if strings.Contains(string(out), "assistive access") || strings.Contains(string(out), "-1719") {
				return 0, "no-accessibility"
			}
			continue
		}
		if strings.TrimSpace(string(out)) == "ok" {
			return 0, ""
		}
	}
	return 0, "tab-not-found"
}
