package serial

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	bugst "go.bug.st/serial"
)

// ResettablePort is a port whose RTS line drives the board's EN pin (ESP32 auto-reset circuit).
type ResettablePort interface {
	io.ReadWriter
	SetRTS(on bool) error
}

type ResetTiming struct {
	Pulse   time.Duration // EN held low
	Retry   time.Duration // between "stay" attempts while the launcher boots
	Timeout time.Duration // until the launcher must have confirmed; its autostart fires after 3 s
}

var DefaultResetTiming = ResetTiming{Pulse: 100 * time.Millisecond, Retry: 200 * time.Millisecond, Timeout: 2500 * time.Millisecond}

// ReturnToLauncher resets the board like its RST button (the ESP32 reports it as power-on, so
// cyd-launcher's bootloader starts the launcher) and tells the launcher to skip its autostart.
func ReturnToLauncher(p ResettablePort, t ResetTiming) error {
	if err := p.SetRTS(true); err != nil {
		return fmt.Errorf("reset: %w", err)
	}
	time.Sleep(t.Pulse)
	if err := p.SetRTS(false); err != nil {
		return fmt.Errorf("reset: %w", err)
	}

	confirmed := make(chan struct{})
	go func() {
		sc := bufio.NewScanner(p)
		for sc.Scan() {
			if strings.TrimSpace(sc.Text()) == "ok stay" {
				close(confirmed)
				return
			}
		}
	}()

	deadline := time.After(t.Timeout)
	retry := time.NewTicker(t.Retry)
	defer retry.Stop()
	for {
		if _, err := p.Write([]byte("stay\n")); err != nil {
			return fmt.Errorf("write: %w", err)
		}
		select {
		case <-confirmed:
			return nil
		case <-deadline:
			return errors.New("the board reset, but no launcher answered (is cyd-launcher installed?)")
		case <-retry.C:
		}
	}
}

// OpenResettable opens the port without touching DTR and RTS, like the daemon does.
func OpenResettable(path string, baud int) (bugst.Port, error) {
	p, err := bugst.Open(path, quietMode(baud))
	if err != nil {
		return nil, fmt.Errorf("open %s: %w", path, err)
	}
	if err := p.SetReadTimeout(200 * time.Millisecond); err != nil {
		p.Close()
		return nil, err
	}
	return p, nil
}

// Ports lists candidate device paths: path itself unless it is "auto".
func Ports(path string) []string { return listPorts(path, "") }
