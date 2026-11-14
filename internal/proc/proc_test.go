package proc

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestShouldReadOwnProcess(t *testing.T) {
	// when
	p, err := Get(os.Getpid())

	// then
	if err != nil {
		t.Fatal(err)
	}
	if p.PID != os.Getpid() || p.PPID != os.Getppid() || p.Comm == "" {
		t.Fatalf("unexpected %+v", p)
	}
}

func TestShouldReadArgv0Name(t *testing.T) {
	// the kernel comm of Claude Code is its version ("2.1.316"); argv[0] is "claude"
	p, err := Get(os.Getpid())
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Base(os.Args[0]); p.Name != want {
		t.Fatalf("name = %q, want %q", p.Name, want)
	}
}

func TestShouldRecognizeClaude(t *testing.T) {
	cases := []struct {
		p    Proc
		want bool
	}{
		{Proc{Name: "claude", Comm: "2.1.316"}, true},
		{Proc{Name: "2.1.316", Exe: "/Users/m/.local/share/claude/versions/2.1.316"}, true},
		{Proc{Name: "zsh", Comm: "zsh"}, false},
		{Proc{Name: "node", Comm: "node"}, false},
	}
	for _, c := range cases {
		if got := c.p.IsClaude(); got != c.want {
			t.Errorf("%+v: got %v", c.p, got)
		}
	}
}

func TestShouldDetectAliveProcesses(t *testing.T) {
	if !Alive(os.Getpid()) {
		t.Fatal("own process must be alive")
	}
	if Alive(999999) {
		t.Fatal("pid 999999 must not be alive")
	}
}

func fakeTable(procs ...Proc) func(int) (Proc, error) {
	m := map[int]Proc{}
	for _, p := range procs {
		m[p.PID] = p
	}
	return func(pid int) (Proc, error) {
		if p, ok := m[pid]; ok {
			return p, nil
		}
		return Proc{}, errors.New("no such process")
	}
}

func TestShouldFindClaudeAboveHookShell(t *testing.T) {
	// given hook(100) <- sh(90) <- claude(80) <- zsh(70) <- zellij(1)
	get := fakeTable(
		Proc{PID: 90, PPID: 80, Comm: "sh", Name: "sh"},
		Proc{PID: 80, PPID: 70, Comm: "2.1.318", Name: "claude", TTY: "ttys022"},
		Proc{PID: 70, PPID: 1, Comm: "zsh", Name: "zsh"},
	)

	// when
	p, ok := findAgent(90, get)

	// then
	if !ok || p.PID != 80 || p.TTY != "ttys022" {
		t.Fatalf("got %+v ok=%v", p, ok)
	}
}

func TestShouldFallBackToFirstNonShellAncestor(t *testing.T) {
	// given claude installed via npm runs as node
	get := fakeTable(
		Proc{PID: 90, PPID: 85, Comm: "bash", Name: "bash"},
		Proc{PID: 85, PPID: 80, Comm: "zsh", Name: "-zsh"},
		Proc{PID: 80, PPID: 70, Comm: "node", Name: "node"},
		Proc{PID: 70, PPID: 1, Comm: "zsh", Name: "zsh"},
	)

	// when
	p, ok := findAgent(90, get)

	// then
	if !ok || p.PID != 80 {
		t.Fatalf("got %+v ok=%v", p, ok)
	}
}

func TestShouldGiveUpAtLaunchd(t *testing.T) {
	get := fakeTable(Proc{PID: 90, PPID: 1, Comm: "sh"})
	if _, ok := findAgent(90, get); ok {
		t.Fatal("expected no agent")
	}
}

func TestShouldParseProcargs(t *testing.T) {
	// given argc=2, exec path, padding, argv, env
	b := []byte{2, 0, 0, 0}
	b = append(b, "/x/versions/2.1.318\x00\x00\x00claude\x00--resume\x00ZELLIJ_PANE_ID=23\x00HOME=/Users/m\x00\x00"...)

	// when
	exe, argv, env := parseProcargs(b)

	// then
	if exe != "/x/versions/2.1.318" || len(argv) != 2 || argv[0] != "claude" || argv[1] != "--resume" {
		t.Fatalf("exe=%q argv=%q", exe, argv)
	}
	if len(env) != 2 || env[0] != "ZELLIJ_PANE_ID=23" {
		t.Fatalf("env=%q", env)
	}
}

func TestShouldReadOwnEnvironment(t *testing.T) {
	if got := Env(os.Getpid())["HOME"]; got != os.Getenv("HOME") {
		t.Fatalf("HOME = %q", got)
	}
}

func TestShouldListAncestors(t *testing.T) {
	get := fakeTable(
		Proc{PID: 90, PPID: 80, Name: "zellij"},
		Proc{PID: 80, PPID: 70, Name: "zsh"},
		Proc{PID: 70, PPID: 1, Name: "Tabby"},
	)
	chain := chain(90, get)
	if len(chain) != 3 || chain[0].PID != 90 || chain[2].PID != 70 {
		t.Fatalf("%+v", chain)
	}
}

func TestShouldFindVibeAboveItsAppServer(t *testing.T) {
	// given hook(100) <- sh(90) <- python app-server(85) <- vibe-rs(80) <- zsh(70)
	get := fakeTable(
		Proc{PID: 90, PPID: 85, Comm: "sh", Name: "sh"},
		Proc{PID: 85, PPID: 80, Comm: "python", Name: "python"},
		Proc{PID: 80, PPID: 70, Comm: "vibe-rs", Name: "vibe-rs", TTY: "pts/4"},
		Proc{PID: 70, PPID: 1, Comm: "zsh", Name: "zsh"},
	)

	// when
	p, ok := findAgent(90, get)

	// then
	if !ok || p.PID != 80 || p.TTY != "pts/4" {
		t.Fatalf("got %+v ok=%v", p, ok)
	}
}
