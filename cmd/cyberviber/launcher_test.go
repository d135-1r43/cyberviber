package main

import (
	"errors"
	"testing"
	"time"

	"github.com/d135-1r43/cyberviber/internal/serial"
)

func TestLauncherReleaseKeepsALongerRelease(t *testing.T) {
	now := time.Unix(1000, 0)

	sec := launcherReleaseSeconds(serial.Status{Released: now.Unix() + 300}, now)

	if sec != 0 {
		t.Fatalf("release for %ds, want 0: the port is already free for 300 s", sec)
	}
}

func TestLauncherReleaseFreesAConnectedPort(t *testing.T) {
	now := time.Unix(1000, 0)

	if sec := launcherReleaseSeconds(serial.Status{Connected: true}, now); sec != 10 {
		t.Fatalf("release for %ds, want 10", sec)
	}
	if sec := launcherReleaseSeconds(serial.Status{Released: now.Unix() + 3}, now); sec != 10 {
		t.Fatalf("release for %ds, want 10: 3 s left are too short", sec)
	}
}

func TestRetryWaitsUntilTheDaemonLetsGoOfThePort(t *testing.T) {
	calls := 0

	got, err := retry(func() (int, error) {
		calls++
		if calls < 3 {
			return 0, errors.New("busy")
		}
		return 42, nil
	}, time.Millisecond, time.Second)

	if err != nil || got != 42 || calls != 3 {
		t.Fatalf("got %d, %v after %d calls", got, err, calls)
	}
}

func TestRetryGivesUpWithTheLastError(t *testing.T) {
	_, err := retry(func() (int, error) { return 0, errors.New("busy") }, time.Millisecond, 20*time.Millisecond)

	if err == nil || err.Error() != "busy" {
		t.Fatalf("err = %v, want busy", err)
	}
}
