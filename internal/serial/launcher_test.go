package serial

import (
	"io"
	"strings"
	"sync"
	"testing"
	"time"
)

// launcherBoard plays the board behind a CH340: RTS drives EN, and after the reset the
// launcher answers "stay" with "ok stay" once it has booted.
type launcherBoard struct {
	mu        sync.Mutex
	rts       []bool
	resetDone bool
	answer    bool
	toHost    *io.PipeReader
	board     *io.PipeWriter
	lines     []string
}

func newLauncherBoard(answer bool) *launcherBoard {
	r, w := io.Pipe()
	return &launcherBoard{answer: answer, toHost: r, board: w}
}

func (b *launcherBoard) SetRTS(on bool) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.rts = append(b.rts, on)
	if !on && len(b.rts) >= 2 && b.rts[len(b.rts)-2] {
		b.resetDone = true
		go b.board.Write([]byte("ets Jun  8 2016 00:22:57\r\nrst:0x1 (POWERON_RESET)\r\n"))
	}
	return nil
}

func (b *launcherBoard) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	for _, l := range strings.Split(strings.TrimRight(string(p), "\n"), "\n") {
		b.lines = append(b.lines, l)
		if l == "stay" && b.resetDone && b.answer {
			go b.board.Write([]byte("ok stay\n"))
		}
	}
	return len(p), nil
}

func (b *launcherBoard) Read(p []byte) (int, error) { return b.toHost.Read(p) }

func (b *launcherBoard) state() ([]bool, []string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return append([]bool(nil), b.rts...), append([]string(nil), b.lines...)
}

var fastReset = ResetTiming{Pulse: time.Millisecond, Retry: 5 * time.Millisecond, Timeout: 500 * time.Millisecond}

func TestReturnToLauncherPulsesENAndKeepsTheMenu(t *testing.T) {
	b := newLauncherBoard(true)

	if err := ReturnToLauncher(b, fastReset); err != nil {
		t.Fatal(err)
	}

	rts, lines := b.state()
	if len(rts) != 2 || !rts[0] || rts[1] {
		t.Fatalf("RTS sequence = %v, want [true false]", rts)
	}
	if len(lines) == 0 || lines[0] != "stay" {
		t.Fatalf("sent %q, want stay", lines)
	}
}

func TestReturnToLauncherFailsWithoutConfirmation(t *testing.T) {
	b := newLauncherBoard(false)

	start := time.Now()
	err := ReturnToLauncher(b, fastReset)

	if err == nil || !strings.Contains(err.Error(), "launcher") {
		t.Fatalf("err = %v, want a missing launcher error", err)
	}
	if time.Since(start) > 2*time.Second {
		t.Fatalf("took %s, the timeout is %s", time.Since(start), fastReset.Timeout)
	}
}
