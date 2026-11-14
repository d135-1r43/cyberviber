// Package hook implements `cyberviber hook`: read a Claude Code hook payload, enrich it, forward it.
package hook

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"strings"
	"time"

	"github.com/d135-1r43/cyberviber/internal/event"
	"github.com/d135-1r43/cyberviber/internal/mux"
	"github.com/d135-1r43/cyberviber/internal/proc"
)

type payload struct {
	event.Event
	ToolInput       json.RawMessage `json:"tool_input"`
	ParentSessionID string          `json:"parent_session_id"`
}

// Build parses the hook payload from r and adds activity text and process information.
func Build(r io.Reader, getenv func(string) string, agent func() (proc.Proc, bool), now time.Time) (event.Event, error) {
	var p payload
	if err := json.NewDecoder(io.LimitReader(r, 16<<20)).Decode(&p); err != nil {
		return event.Event{}, fmt.Errorf("parse hook payload: %w", err)
	}
	if err := fromVibe(&p); err != nil {
		return event.Event{}, err
	}
	e := p.Event
	e.Time = now
	switch e.Name {
	case event.PreToolUse:
		e.Activity, e.File = event.Summarize(e.ToolName, p.ToolInput)
	case event.PermissionRequest:
		act, _ := event.Summarize(e.ToolName, p.ToolInput)
		e.Activity = "Allow " + act + "?"
	}
	if a, ok := agent(); ok {
		e.PID, e.TTY = a.PID, a.TTY
	}
	e.Mux, _ = mux.Detect(getenv)
	return e, nil
}

// fromVibe rewrites a Mistral Vibe hook payload into the Claude Code events the daemon knows.
// A subagent's events belong to its parent session; its finished turn is not the parent's.
func fromVibe(p *payload) error {
	switch p.Name {
	case event.VibePreTool:
		p.Name = event.PreToolUse
	case event.VibePostTool:
		p.Name = event.PostToolUse
	case event.VibePostAgent:
		p.Name = event.Stop
		if p.ParentSessionID != "" {
			p.Name = event.PostToolUse
		}
	default:
		if p.Name != "" && strings.ToLower(p.Name) == p.Name {
			return fmt.Errorf("unsupported vibe hook event %q", p.Name)
		}
		return nil
	}
	if p.ParentSessionID != "" {
		p.SessionID = p.ParentSessionID
	}
	if p.ToolName != "" {
		p.ToolName = event.VibeTool(p.ToolName)
	}
	return nil
}

// Send posts the event to the daemon. It never blocks for long: hooks must not slow Claude Code down.
func Send(port int, e event.Event) error {
	b, err := json.Marshal(e)
	if err != nil {
		return err
	}
	c := http.Client{Timeout: 500 * time.Millisecond}
	resp, err := c.Post(fmt.Sprintf("http://127.0.0.1:%d/event", port), "application/json", bytes.NewReader(b))
	if err != nil {
		return err
	}
	resp.Body.Close()
	return nil
}

// ParseStatus reads Claude Code's status line input and returns the session id and its
// context usage in percent. ok is false when there is no usage yet (no messages).
func ParseStatus(r io.Reader) (id string, pct int, ok bool) {
	var in struct {
		SessionID string `json:"session_id"`
		Context   struct {
			Used *float64 `json:"used_percentage"`
		} `json:"context_window"`
	}
	if json.NewDecoder(io.LimitReader(r, 1<<20)).Decode(&in) != nil || in.SessionID == "" || in.Context.Used == nil {
		return "", 0, false
	}
	return in.SessionID, int(math.Round(min(max(*in.Context.Used, 0), 100))), true
}

// SendCtx posts a session's context usage to the daemon.
func SendCtx(port int, id string, pct int) error {
	b, _ := json.Marshal(map[string]any{"session_id": id, "pct": pct})
	c := http.Client{Timeout: 300 * time.Millisecond}
	resp, err := c.Post(fmt.Sprintf("http://127.0.0.1:%d/ctx", port), "application/json", bytes.NewReader(b))
	if err != nil {
		return err
	}
	resp.Body.Close()
	return nil
}
