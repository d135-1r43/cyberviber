package focus

import (
	"encoding/json"
	"path/filepath"
	"strings"

	"github.com/d135-1r43/cyberviber/internal/session"
)

// Orca runs its shells under a terminal host ("Orca Agents.app" in Orca's support directory), not
// under Orca.app, so the window to bring forward is Orca's own. Orca exports the terminal's
// runtime handle and its CLI directory to the processes in each terminal; `orca terminal switch`
// shows that terminal in the UI.
type orca struct{}

const orcaDefaultApp = "/Applications/Orca.app"

func (orca) Focus(f *Focuser, _ session.Session, t Terminal, _ bool) (int, string) {
	handle, binDir := "", ""
	for _, pid := range t.Chain {
		env := f.Env(pid)
		if handle = env["ORCA_TERMINAL_HANDLE"]; handle != "" {
			binDir = env["ORCA_CLI_BIN_DIR"]
			break
		}
	}
	app := orcaDefaultApp
	if i := strings.Index(binDir, ".app/"); i >= 0 {
		app = binDir[:i+len(".app")]
	}
	defer f.Run("open", "-a", app)
	if handle == "" {
		return 0, "tab-not-found"
	}
	cli := "orca"
	if binDir != "" {
		cli = filepath.Join(binDir, "orca") // launchd's PATH does not include /usr/local/bin
	}
	out, err := f.Run(cli, "terminal", "switch", "--terminal", handle, "--json")
	var reply struct {
		OK bool `json:"ok"`
	}
	if err != nil || json.Unmarshal(out, &reply) != nil || !reply.OK {
		return 0, "tab-not-found"
	}
	return 0, ""
}
