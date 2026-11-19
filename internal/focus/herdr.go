package focus

import (
	"bufio"
	"encoding/json"
	"fmt"
	"log"
	"net"
	"path/filepath"
	"time"

	"github.com/d135-1r43/cyberviber/internal/mux"
)

type herdr struct{}

// Focus asks the herdr server, over its socket API, to focus the pane. Clients attach through
// herdr-client.sock next to the API socket.
func (herdr) Focus(f *Focuser, loc mux.Location) []int {
	clientSock := filepath.Join(filepath.Dir(loc.Session), "herdr-client.sock")
	clients := f.socketClients("herdr", func(path string, _ int) bool { return path == clientSock })
	if loc.Pane != "" {
		if err := herdrCall(loc.Session, "pane.focus", map[string]string{"pane_id": loc.Pane}); err != nil {
			log.Printf("focus: herdr pane.focus %s: %v", loc.Pane, err)
		}
	}
	return clients
}

type herdrResponse struct {
	Error *struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
}

func herdrCall(sock, method string, params any) error {
	c, err := net.DialTimeout("unix", sock, 500*time.Millisecond)
	if err != nil {
		return err
	}
	defer c.Close()
	c.SetDeadline(time.Now().Add(2 * time.Second))
	req := map[string]any{"id": "cyberviber", "method": method, "params": params}
	if err := json.NewEncoder(c).Encode(req); err != nil {
		return err
	}
	var res herdrResponse
	if err := json.NewDecoder(bufio.NewReader(c)).Decode(&res); err != nil {
		return err
	}
	if res.Error != nil {
		return fmt.Errorf("%s: %s", res.Error.Code, res.Error.Message)
	}
	return nil
}
