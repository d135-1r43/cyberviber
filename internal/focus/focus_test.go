package focus

import (
	"bufio"
	"encoding/json"
	"errors"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/d135-1r43/cyberviber/internal/mux"
	"github.com/d135-1r43/cyberviber/internal/proc"
	"github.com/d135-1r43/cyberviber/internal/session"
)

func table(procs ...proc.Proc) func(int) (proc.Proc, error) {
	m := map[int]proc.Proc{}
	for _, p := range procs {
		m[p.PID] = p
	}
	return func(pid int) (proc.Proc, error) {
		if p, ok := m[pid]; ok {
			return p, nil
		}
		return proc.Proc{}, errors.New("no such process")
	}
}

const tabbyExe = "/Applications/Tabby.app/Contents/MacOS/Tabby"

func TestShouldResolveTabbyTab(t *testing.T) {
	// given zellij client(90) <- zsh(80, tab root) <- Tabby(70)
	get := table(
		proc.Proc{PID: 90, PPID: 80, Name: "zellij", TTY: "ttys015"},
		proc.Proc{PID: 80, PPID: 70, Name: "zsh", TTY: "ttys015"},
		proc.Proc{PID: 70, PPID: 1, Name: "Tabby", Exe: tabbyExe},
	)

	// when
	term, ok := ResolveTerminal(90, get)

	// then
	if !ok || term.App != "tabby" || term.Bundle != "/Applications/Tabby.app" || term.TTY != "ttys015" ||
		len(term.Chain) != 2 || term.Chain[0] != 90 || term.Chain[1] != 80 {
		t.Fatalf("ok=%v %+v", ok, term)
	}
}

func TestShouldResolveGhosttyThroughLogin(t *testing.T) {
	get := table(
		proc.Proc{PID: 90, PPID: 85, Name: "claude", TTY: "ttys003"},
		proc.Proc{PID: 85, PPID: 80, Name: "-zsh"},
		proc.Proc{PID: 80, PPID: 70, Name: "login", Exe: "/usr/bin/login"},
		proc.Proc{PID: 70, PPID: 1, Name: "ghostty", Exe: "/Applications/Ghostty.app/Contents/MacOS/ghostty"},
	)
	term, ok := ResolveTerminal(90, get)
	if !ok || term.App != "ghostty" || len(term.Chain) != 3 {
		t.Fatalf("ok=%v %+v", ok, term)
	}
}

func TestShouldNotResolveWithoutApp(t *testing.T) {
	get := table(proc.Proc{PID: 90, PPID: 1, Name: "zellij"})
	if _, ok := ResolveTerminal(90, get); ok {
		t.Fatal("expected no terminal")
	}
}

const lsofOut = `p61652
f5
d0xcfb2
n->0x5385
f7
d0xecd0
n->0x8bc2
p98824
f5
d0xd8b2
n/var/folders/T/zellij-501/contract_version_1/sincere-drum
f6
d0x5385
n/var/folders/T/zellij-501/contract_version_1/sincere-drum
f11
d0x3816
n->0x6140
p77777
f5
d0x1111
n/var/folders/T/zellij-501/contract_version_1/other
`

func TestShouldMapSocketPathsToClients(t *testing.T) {
	got := ParseLsof(lsofOut)
	drum := got["/var/folders/T/zellij-501/contract_version_1/sincere-drum"]
	if len(drum.Clients) != 1 || drum.Clients[0] != 61652 || drum.Server != 98824 ||
		len(got["/var/folders/T/zellij-501/contract_version_1/other"].Clients) != 0 {
		t.Fatalf("%v", got)
	}
}

func TestShouldListEachClientOncePerSocketPath(t *testing.T) {
	// given client 5 holds two connections to server 9's socket, client 6 one to another socket
	out := "p9\nf3\nd0xa\nn/s/one\nf4\nd0xb\nn/s/one\nf5\nd0xc\nn/s/two\n" +
		"p5\nf1\nd0x1\nn->0xa\nf2\nd0x2\nn->0xb\n" +
		"p6\nf1\nd0x3\nn->0xc\n"

	// when
	got := ParseLsof(out)

	// then
	if len(got) != 2 || len(got["/s/one"].Clients) != 1 || got["/s/one"].Clients[0] != 5 || len(got["/s/two"].Clients) != 1 || got["/s/two"].Clients[0] != 6 {
		t.Fatalf("%v", got)
	}
}

func TestShouldHaveMultiplexerForEveryKind(t *testing.T) {
	for _, k := range mux.Kinds() {
		if multiplexers[k] == nil {
			t.Errorf("mux kind %q has no Multiplexer in package focus", k)
		}
	}
}

func TestShouldNormalizePaneID(t *testing.T) {
	for in, want := range map[string]string{"23": "terminal_23", "terminal_23": "terminal_23", "plugin_2": "plugin_2"} {
		if got := paneID(in); got != want {
			t.Errorf("%s → %s, want %s", in, got, want)
		}
	}
}

// fakePlugin answers like the Tabby plugin on a unix socket in dir.
func fakePlugin(t *testing.T, dir string, matchPID int) *[]focusRequest {
	t.Helper()
	ln, err := net.Listen("unix", filepath.Join(dir, "4242.sock"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	var seen []focusRequest
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			var req focusRequest
			json.NewDecoder(bufio.NewReader(c)).Decode(&req)
			seen = append(seen, req)
			ok := false
			for _, p := range req.PIDs {
				ok = ok || p == matchPID
			}
			json.NewEncoder(c).Encode(map[string]any{"ok": ok, "index": 3})
			c.Close()
		}
	}()
	return &seen
}

func shortTempDir(t *testing.T) string {
	t.Helper()
	d, err := os.MkdirTemp("/tmp", "cv") // unix socket paths are limited to 104 bytes
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(d) })
	return d
}

func TestShouldAskTabbyPluginAndRemoveStaleSockets(t *testing.T) {
	// given
	dir := shortTempDir(t)
	seen := fakePlugin(t, dir, 80)
	os.WriteFile(filepath.Join(dir, "1.sock"), nil, 0o600) // stale: not a socket

	// when
	tab, err := FocusTabby(dir, []int{90, 80})

	// then
	if err != nil || tab != 3 || len(*seen) != 1 || (*seen)[0].Cmd != "focus" {
		t.Fatalf("tab=%d err=%v seen=%+v", tab, err, *seen)
	}
	if _, err := os.Stat(filepath.Join(dir, "1.sock")); !os.IsNotExist(err) {
		t.Fatal("stale socket must be removed")
	}
}

func TestShouldReportNoPluginAndNotFound(t *testing.T) {
	if _, err := FocusTabby(shortTempDir(t), []int{1}); !errors.Is(err, ErrNoPlugin) {
		t.Fatalf("err=%v", err)
	}
	dir := shortTempDir(t)
	fakePlugin(t, dir, 80)
	if _, err := FocusTabby(dir, []int{1}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("err=%v", err)
	}
}

type recorder struct {
	calls   []string
	answers map[string]string // substring of the call → output
}

func (r *recorder) run(name string, args ...string) ([]byte, error) {
	call := name + " " + strings.Join(args, " ")
	r.calls = append(r.calls, call)
	for k, v := range r.answers {
		if strings.Contains(call, k) {
			return []byte(v), nil
		}
	}
	return nil, nil
}

func newFocuser(s session.Session, get func(int) (proc.Proc, error), env map[string]string, r *recorder, tabbyDir string) *Focuser {
	return &Focuser{
		Lookup:   func(id string) (session.Session, bool) { return s, id == s.ID },
		Get:      get,
		Env:      func(int) map[string]string { return env },
		Run:      r.run,
		TabbyDir: tabbyDir,
		GOOS:     "darwin",
	}
}

func TestShouldFocusZellijPaneThenTabbyTab(t *testing.T) {
	// given claude(100) runs in zellij pane 23 of sincere-drum; client 90 runs in a Tabby tab
	get := table(
		proc.Proc{PID: 100, PPID: 99, Name: "claude"},
		proc.Proc{PID: 99, PPID: 98, Name: "zsh"},
		proc.Proc{PID: 98, PPID: 1, Name: "zellij"},
		proc.Proc{PID: 90, PPID: 80, Name: "zellij", TTY: "ttys015", Exe: "/opt/homebrew/bin/zellij"},
		proc.Proc{PID: 80, PPID: 70, Name: "zsh"},
		proc.Proc{PID: 70, PPID: 1, Name: "Tabby", Exe: tabbyExe},
	)
	r := &recorder{answers: map[string]string{"lsof": strings.ReplaceAll(strings.ReplaceAll(lsofOut, "61652", "90"), "98824", "98")}}
	dir := shortTempDir(t)
	seen := fakePlugin(t, dir, 80)
	f := newFocuser(session.Session{ID: "s1", PID: 100}, get,
		map[string]string{"ZELLIJ_SESSION_NAME": "sincere-drum", "ZELLIJ_PANE_ID": "23"}, r, dir)

	// when
	res := f.Focus("s1")

	// then
	if !res.OK || res.Terminal != "tabby" || res.Mux != "zellij" {
		t.Fatalf("%+v calls=%v", res, r.calls)
	}
	joined := strings.Join(r.calls, "\n")
	if !strings.Contains(joined, "/opt/homebrew/bin/zellij --session sincere-drum action focus-pane-id terminal_23") {
		t.Fatalf("zellij not focused: %v", r.calls)
	}
	if !strings.Contains(joined, "open -a /Applications/Tabby.app") {
		t.Fatalf("tabby not activated: %v", r.calls)
	}
	if len(*seen) != 1 || (*seen)[0].PIDs[0] != 90 {
		t.Fatalf("plugin must get the client chain: %+v", *seen)
	}
}

func TestShouldFindZellijClientBehindHashedSocketName(t *testing.T) {
	// given zellij 0.46 names its socket by a hash; only server 98's env names the session
	get := table(
		proc.Proc{PID: 100, PPID: 1, Name: "claude"},
		proc.Proc{PID: 90, PPID: 80, Name: "zellij", TTY: "ttys015", Exe: "/opt/homebrew/bin/zellij"},
		proc.Proc{PID: 80, PPID: 70, Name: "zsh"},
		proc.Proc{PID: 70, PPID: 1, Name: "Tabby", Exe: tabbyExe},
	)
	lsof := "p98\nf5\nd0xa\nn/T/zellij-501/contract_version_1/cdfa29d86a1c\n" +
		"p97\nf5\nd0xb\nn/T/zellij-501/contract_version_1/0123456789ab\n" +
		"p90\nf5\nd0x1\nn->0xa\n" +
		"p91\nf5\nd0x2\nn->0xb\n"
	r := &recorder{answers: map[string]string{"lsof": lsof}}
	dir := shortTempDir(t)
	fakePlugin(t, dir, 80)
	f := newFocuser(session.Session{ID: "s1", PID: 100, Mux: mux.Location{Kind: "zellij", Session: "marvellous-accordion", Pane: "2"}}, get, nil, r, dir)
	f.Env = func(pid int) map[string]string {
		switch pid {
		case 98:
			return map[string]string{"ZELLIJ_SESSION_NAME": "marvellous-accordion"}
		case 97:
			return map[string]string{"ZELLIJ_SESSION_NAME": "other"}
		}
		return nil
	}

	// when
	res := f.Focus("s1")

	// then
	if !res.OK || res.Terminal != "tabby" {
		t.Fatalf("%+v calls=%v", res, r.calls)
	}
}

func TestShouldPreferZellijInfoFromHooks(t *testing.T) {
	get := table(proc.Proc{PID: 100, PPID: 1, Name: "claude"})
	r := &recorder{}
	f := newFocuser(session.Session{ID: "s1", PID: 100, Mux: mux.Location{Kind: "zellij", Session: "work", Pane: "7"}}, get, nil, r, shortTempDir(t))
	res := f.Focus("s1")
	if res.Reason != "detached" || !strings.Contains(strings.Join(r.calls, "\n"), "zellij --session work action focus-pane-id terminal_7") {
		t.Fatalf("%+v %v", res, r.calls)
	}
}

func TestShouldFocusTabbyWithoutZellij(t *testing.T) {
	get := table(
		proc.Proc{PID: 100, PPID: 80, Name: "claude", TTY: "ttys003"},
		proc.Proc{PID: 80, PPID: 70, Name: "zsh"},
		proc.Proc{PID: 70, PPID: 1, Name: "Tabby", Exe: tabbyExe},
	)
	r := &recorder{}
	dir := shortTempDir(t)
	fakePlugin(t, dir, 80)
	res := newFocuser(session.Session{ID: "s1", PID: 100}, get, map[string]string{}, r, dir).Focus("s1")
	if !res.OK || strings.Contains(strings.Join(r.calls, "\n"), "zellij") {
		t.Fatalf("%+v %v", res, r.calls)
	}
}

func TestShouldActivateTabbyAndReportMissingPlugin(t *testing.T) {
	get := table(
		proc.Proc{PID: 100, PPID: 80, Name: "claude"},
		proc.Proc{PID: 80, PPID: 70, Name: "zsh"},
		proc.Proc{PID: 70, PPID: 1, Name: "Tabby", Exe: tabbyExe},
	)
	r := &recorder{}
	res := newFocuser(session.Session{ID: "s1", PID: 100}, get, nil, r, shortTempDir(t)).Focus("s1")
	if res.OK || res.Reason != "no-plugin" || !strings.Contains(strings.Join(r.calls, "\n"), "open -a /Applications/Tabby.app") {
		t.Fatalf("%+v %v", res, r.calls)
	}
}

func TestShouldReportUnknownSessionsAndProcesses(t *testing.T) {
	r := &recorder{}
	f := newFocuser(session.Session{ID: "s1"}, table(), nil, r, shortTempDir(t))
	if res := f.Focus("nope"); res.Reason != "not-found" {
		t.Fatalf("%+v", res)
	}
	if res := f.Focus("s1"); res.Reason != "no-process" {
		t.Fatalf("%+v", res)
	}
}

func TestShouldFocusGhosttyByCachedIDThenCwd(t *testing.T) {
	// given
	get := table(
		proc.Proc{PID: 100, PPID: 80, Name: "claude", TTY: "ttys003"},
		proc.Proc{PID: 80, PPID: 70, Name: "zsh"},
		proc.Proc{PID: 70, PPID: 1, Name: "ghostty", Exe: "/Applications/Ghostty.app/Contents/MacOS/ghostty"},
	)
	r := &recorder{answers: map[string]string{"working directory is": "ABC-123\n"}}
	f := newFocuser(session.Session{ID: "s1", PID: 100, Cwd: "/Users/m/git/x"}, get, nil, r, shortTempDir(t))

	// when: first focus resolves by cwd and caches the id
	res := f.Focus("s1")

	// then
	if !res.OK || res.Terminal != "ghostty" {
		t.Fatalf("%+v %v", res, r.calls)
	}
	r.calls = nil
	r.answers = map[string]string{`id is "ABC-123"`: "ok\n"}
	if res := f.Focus("s1"); !res.OK || !strings.Contains(strings.Join(r.calls, "\n"), `id is "ABC-123"`) {
		t.Fatalf("cached id not used: %+v %v", res, r.calls)
	}
}

func TestShouldEscapeAppleScriptStrings(t *testing.T) {
	if got := asString(`a"b\c`); got != `"a\"b\\c"` {
		t.Fatalf("got %s", got)
	}
}

// fakeHerdr answers herdr socket API requests like the herdr server.
func fakeHerdr(t *testing.T, sock string) *[]map[string]any {
	t.Helper()
	ln, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	var seen []map[string]any
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			var req map[string]any
			json.NewDecoder(bufio.NewReader(c)).Decode(&req)
			seen = append(seen, req)
			json.NewEncoder(c).Encode(map[string]any{"id": req["id"], "result": map[string]any{"type": "ok"}})
			c.Close()
		}
	}()
	return &seen
}

const kittyExe = "/Applications/kitty.app/Contents/MacOS/kitty"

func TestShouldFocusHerdrPaneThenKittyWindow(t *testing.T) {
	// given claude(100) runs in herdr pane w1M:p1; herdr client 90 runs in a kitty window via login(80)
	dir := shortTempDir(t)
	sock := filepath.Join(dir, "herdr.sock")
	seen := fakeHerdr(t, sock)
	get := table(
		proc.Proc{PID: 100, PPID: 99, Name: "claude"},
		proc.Proc{PID: 99, PPID: 1, Name: "herdr"},
		proc.Proc{PID: 90, PPID: 85, Name: "herdr", TTY: "ttys000"},
		proc.Proc{PID: 85, PPID: 80, Name: "-zsh"},
		proc.Proc{PID: 80, PPID: 70, Name: "login", Exe: "/usr/bin/login"},
		proc.Proc{PID: 70, PPID: 1, Name: "kitty", Exe: kittyExe},
	)
	herdrLsof := "p99\nf30\nd0xaa\nn" + filepath.Join(dir, "herdr-client.sock") + "\nf4\nd0xbb\nn" + sock + "\n" +
		"p90\nf4\nd0x11\nn->0xaa\n"
	r := &recorder{answers: map[string]string{
		"-c herdr":     herdrLsof,
		"focus-window": "",
	}}
	f := newFocuser(session.Session{ID: "s1", PID: 100}, get,
		map[string]string{"HERDR_SOCKET_PATH": sock, "HERDR_PANE_ID": "w1M:p1", "KITTY_LISTEN_ON": "unix:/tmp/kitty-70"}, r, dir)

	// when
	res := f.Focus("s1")

	// then
	if !res.OK || res.Terminal != "kitty" || res.Mux != "herdr" {
		t.Fatalf("%+v calls=%v", res, r.calls)
	}
	if len(*seen) != 1 || (*seen)[0]["method"] != "pane.focus" ||
		(*seen)[0]["params"].(map[string]any)["pane_id"] != "w1M:p1" {
		t.Fatalf("herdr not focused: %+v", *seen)
	}
	joined := strings.Join(r.calls, "\n")
	if !strings.Contains(joined, "/Applications/kitty.app/Contents/MacOS/kitten @ --to unix:/tmp/kitty-70 focus-window --match pid:90 or pid:85 or pid:80") {
		t.Fatalf("kitty window not focused: %v", r.calls)
	}
	if !strings.Contains(joined, "open -a /Applications/kitty.app") {
		t.Fatalf("kitty not activated: %v", r.calls)
	}
}

func TestShouldReportDetachedHerdr(t *testing.T) {
	dir := shortTempDir(t)
	sock := filepath.Join(dir, "herdr.sock")
	fakeHerdr(t, sock)
	r := &recorder{}
	f := newFocuser(session.Session{ID: "s1", PID: 100, Mux: mux.Location{Kind: "herdr", Session: sock, Pane: "w1:p2"}},
		table(proc.Proc{PID: 100, PPID: 1, Name: "claude"}), nil, r, dir)
	if res := f.Focus("s1"); res.Reason != "detached" || res.Mux != "herdr" {
		t.Fatalf("%+v %v", res, r.calls)
	}
}

func TestShouldActivateKittyWithoutRemoteControl(t *testing.T) {
	get := table(
		proc.Proc{PID: 100, PPID: 80, Name: "claude"},
		proc.Proc{PID: 80, PPID: 70, Name: "login"},
		proc.Proc{PID: 70, PPID: 1, Name: "kitty", Exe: kittyExe},
	)
	r := &recorder{}
	res := newFocuser(session.Session{ID: "s1", PID: 100}, get, nil, r, shortTempDir(t)).Focus("s1")
	joined := strings.Join(r.calls, "\n")
	if res.OK || res.Reason != "no-remote-control" || strings.Contains(joined, "kitten") || !strings.Contains(joined, "open -a /Applications/kitty.app") {
		t.Fatalf("%+v %v", res, r.calls)
	}
}

func TestShouldResolveLinuxTerminalByBinaryName(t *testing.T) {
	get := table(
		proc.Proc{PID: 90, PPID: 80, Name: "claude", TTY: "pts/3"},
		proc.Proc{PID: 80, PPID: 70, Name: "zsh"},
		proc.Proc{PID: 70, PPID: 1, Name: "kitty", Exe: "/usr/bin/kitty"},
	)
	term, ok := ResolveTerminal(90, get)
	if !ok || term.App != "kitty" || term.Bundle != "/usr/bin/kitty" || term.AppPID != 70 || len(term.Chain) != 2 {
		t.Fatalf("ok=%v %+v", ok, term)
	}
}

const ssOut = `u_str ESTAB 0      0                                 * 180848 * 178742 users:(("herdr",pid=266,fd=5))
u_str ESTAB 0      0      /run/user/1000/herdr/herdr-client.sock 178742 * 180848 users:(("herdr",pid=265,fd=30))
u_str ESTAB 0      0                                 * 190001 * 190002 users:(("zsh",pid=300,fd=5))
u_str ESTAB 0      0      /run/user/1000/herdr/herdr-client.sock 190002 * 190001 users:(("herdr",pid=265,fd=31))
u_str ESTAB 0      0                                 * 190003 * 0 users:(("herdr",pid=267,fd=6))
`

func TestShouldMapSsSocketsToClientsOfCommand(t *testing.T) {
	got := ParseSS(ssOut, "herdr")
	if c := got["/run/user/1000/herdr/herdr-client.sock"]; len(got) != 1 || len(c.Clients) != 1 || c.Clients[0] != 266 || c.Server != 265 {
		t.Fatalf("%v", got)
	}
}

func TestShouldFocusHerdrThenKittyOnHyprland(t *testing.T) {
	// given claude(100) in herdr pane w1:p2; herdr client 266 runs in kitty 70 on Linux
	dir := shortTempDir(t)
	sock := filepath.Join(dir, "herdr.sock")
	seen := fakeHerdr(t, sock)
	get := table(
		proc.Proc{PID: 100, PPID: 265, Name: "claude"},
		proc.Proc{PID: 265, PPID: 1, Name: "herdr"},
		proc.Proc{PID: 266, PPID: 80, Name: "herdr", TTY: "pts/1"},
		proc.Proc{PID: 80, PPID: 70, Name: "zsh"},
		proc.Proc{PID: 70, PPID: 1, Name: "kitty", Exe: "/usr/bin/kitty"},
	)
	r := &recorder{answers: map[string]string{
		"ss -xpH":      strings.ReplaceAll(ssOut, "/run/user/1000/herdr", dir),
		"hl.dsp.focus": "ok",
	}}
	f := newFocuser(session.Session{ID: "s1", PID: 100}, get,
		map[string]string{"HERDR_SOCKET_PATH": sock, "HERDR_PANE_ID": "w1:p2", "KITTY_LISTEN_ON": "unix:@kitty-70"}, r, dir)
	f.GOOS = "linux"

	// when
	res := f.Focus("s1")

	// then
	joined := strings.Join(r.calls, "\n")
	if !res.OK || res.Terminal != "kitty" || len(*seen) != 1 {
		t.Fatalf("%+v %v", res, r.calls)
	}
	if !strings.Contains(joined, "/usr/bin/kitten @ --to unix:@kitty-70 focus-window --match pid:266 or pid:80") {
		t.Fatalf("kitty window not focused: %v", r.calls)
	}
	if !strings.Contains(joined, `hyprctl -i 0 dispatch hl.dsp.focus({ window = "pid:70" })`) ||
		strings.Contains(joined, "focuswindow") || strings.Contains(joined, "open -a") || strings.Contains(joined, "lsof") {
		t.Fatalf("expected hyprctl activation, no macOS tools: %v", r.calls)
	}
}

func TestShouldFallBackToLegacyDispatcherOnHyprlandBefore057(t *testing.T) {
	// given a Hyprland that does not understand the Lua dispatcher
	r := &recorder{answers: map[string]string{"hl.dsp.focus": "Invalid dispatcher"}}
	f := &Focuser{Run: r.run, GOOS: "linux"}

	// when
	f.activate(Terminal{AppPID: 70})

	// then
	if len(r.calls) != 2 || r.calls[1] != "hyprctl -i 0 dispatch focuswindow pid:70" {
		t.Fatalf("%v", r.calls)
	}
}

func TestShouldFocusZellijThenFootWindowOnHyprland(t *testing.T) {
	// given claude(100) in zellij pane 0; zellij client 90 runs in a standalone foot (70), Omarchy's default
	get := table(
		proc.Proc{PID: 100, PPID: 98, Name: "claude"},
		proc.Proc{PID: 98, PPID: 1, Name: "zellij"},
		proc.Proc{PID: 90, PPID: 80, Name: "zellij", TTY: "pts/1", Exe: "/usr/bin/zellij"},
		proc.Proc{PID: 80, PPID: 70, Name: "zsh"},
		proc.Proc{PID: 70, PPID: 1, Name: "foot", Exe: "/usr/bin/foot"},
	)
	r := &recorder{answers: map[string]string{
		"ss -xpH":      `u_str ESTAB 0 0 * 5 * 6 users:(("zellij",pid=90,fd=5))` + "\n" + `u_str ESTAB 0 0 /run/user/1000/zellij/contract_version_1/judicious-accordion 6 * 5 users:(("zellij",pid=98,fd=9))`,
		"-j clients":   `[{"address":"0xaa","pid":13028,"title":"chrome"},{"address":"0xbb","pid":70,"title":"zellij"}]`,
		"hl.dsp.focus": "ok",
	}}
	f := newFocuser(session.Session{ID: "s1", PID: 100}, get,
		map[string]string{"ZELLIJ_SESSION_NAME": "judicious-accordion", "ZELLIJ_PANE_ID": "0"}, r, shortTempDir(t))
	f.GOOS = "linux"

	// when
	res := f.Focus("s1")

	// then
	joined := strings.Join(r.calls, "\n")
	if !res.OK || res.Terminal != "foot" || res.Mux != "zellij" {
		t.Fatalf("%+v %v", res, r.calls)
	}
	if !strings.Contains(joined, "/usr/bin/zellij --session judicious-accordion action focus-pane-id terminal_0") ||
		!strings.Contains(joined, `hyprctl -i 0 dispatch hl.dsp.focus({ window = "address:0xbb" })`) {
		t.Fatalf("pane or window not focused: %v", r.calls)
	}
}

func TestShouldTellFootServerWindowsApartByTitleMarker(t *testing.T) {
	// given a foot server (70) with two windows; the session runs on pts/4
	get := table(
		proc.Proc{PID: 100, PPID: 80, Name: "claude", TTY: "pts/4"},
		proc.Proc{PID: 80, PPID: 70, Name: "zsh"},
		proc.Proc{PID: 70, PPID: 1, Name: "foot", Exe: "/usr/bin/foot"},
	)
	marker := ""
	old := writeTitle
	writeTitle = func(tty, title string) error {
		if tty != "pts/4" {
			t.Errorf("title written to %s", tty)
		}
		marker = title
		return nil
	}
	t.Cleanup(func() { writeTitle = old })
	var calls []string
	f := &Focuser{
		Lookup: func(id string) (session.Session, bool) { return session.Session{ID: "s1", PID: 100}, true },
		Get:    get, Env: func(int) map[string]string { return nil }, GOOS: "linux",
		Run: func(name string, args ...string) ([]byte, error) {
			call := name + " " + strings.Join(args, " ")
			calls = append(calls, call)
			if strings.Contains(call, "-j clients") {
				return []byte(`[{"address":"0x1","pid":70,"title":"other"},{"address":"0x2","pid":70,"title":"` + marker + `"}]`), nil
			}
			return []byte("ok"), nil
		},
	}

	// when
	res := f.Focus("s1")

	// then
	if !res.OK || marker == "" || calls[len(calls)-1] != `hyprctl -i 0 dispatch hl.dsp.focus({ window = "address:0x2" })` {
		t.Fatalf("%+v %v", res, calls)
	}
}

const warpExe = "/Applications/Warp.app/Contents/MacOS/stable"

func warpFocuser(t *testing.T, run func(call, marker string) ([]byte, error)) (*Focuser, *[]string, *string) {
	t.Helper()
	get := table(
		proc.Proc{PID: 100, PPID: 80, Name: "claude", TTY: "ttys005"},
		proc.Proc{PID: 80, PPID: 70, Name: "zsh"},
		proc.Proc{PID: 70, PPID: 1, Name: "stable", Exe: warpExe},
	)
	marker := ""
	old := writeTitle
	writeTitle = func(tty, title string) error {
		if tty != "ttys005" {
			t.Errorf("title written to %s", tty)
		}
		marker = title
		return nil
	}
	t.Cleanup(func() { writeTitle = old })
	var calls []string
	f := &Focuser{
		Lookup: func(id string) (session.Session, bool) { return session.Session{ID: "s1", PID: 100}, true },
		Get:    get, Env: func(int) map[string]string { return nil }, GOOS: "darwin",
		Run: func(name string, args ...string) ([]byte, error) {
			call := name + " " + strings.Join(args, " ")
			calls = append(calls, call)
			return run(call, marker)
		},
	}
	return f, &calls, &marker
}

func TestShouldRaiseWarpWindowByTitleMarker(t *testing.T) {
	// given Warp (70) shows the session's tab, so its window title carries the marker
	f, calls, marker := warpFocuser(t, func(call, marker string) ([]byte, error) {
		if marker != "" && strings.Contains(call, marker) && strings.Contains(call, "unix id is 70") {
			return []byte("ok\n"), nil
		}
		return nil, nil
	})

	// when
	res := f.Focus("s1")

	// then the window is raised, then Warp is brought to the front
	all := strings.Join(*calls, "\n")
	if !res.OK || res.Terminal != "warp" || *marker == "" || !strings.Contains(all, "AXRaise") ||
		(*calls)[len(*calls)-1] != "open -a /Applications/Warp.app" {
		t.Fatalf("%+v %v", res, *calls)
	}
}

func TestShouldActivateWarpWhenSessionTabIsNotFrontmost(t *testing.T) {
	// given no Warp window title ever shows the marker (the session's tab is in the background)
	f, calls, _ := warpFocuser(t, func(string, string) ([]byte, error) { return nil, nil })

	// when
	res := f.Focus("s1")

	// then
	if res.OK || res.Reason != "tab-not-found" || (*calls)[len(*calls)-1] != "open -a /Applications/Warp.app" {
		t.Fatalf("%+v %v", res, *calls)
	}
}

func TestShouldReportMissingAccessibilityForWarp(t *testing.T) {
	// given macOS refuses System Events access to the daemon
	f, calls, _ := warpFocuser(t, func(call, _ string) ([]byte, error) {
		if strings.HasPrefix(call, "osascript") {
			return []byte("execution error: System Events got an error: osascript is not allowed assistive access. (-1719)"), errors.New("exit status 1")
		}
		return nil, nil
	})

	// when
	res := f.Focus("s1")

	// then it asks once, does not keep polling, and still brings Warp forward
	n := 0
	for _, c := range *calls {
		if strings.HasPrefix(c, "osascript") {
			n++
		}
	}
	if res.Reason != "no-accessibility" || n != 1 || (*calls)[len(*calls)-1] != "open -a /Applications/Warp.app" {
		t.Fatalf("%+v %v", res, *calls)
	}
}
