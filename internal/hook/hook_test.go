package hook

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/d135-1r43/cyberviber/internal/event"
	"github.com/d135-1r43/cyberviber/internal/mux"
	"github.com/d135-1r43/cyberviber/internal/proc"
)

func TestShouldEnrichPreToolUse(t *testing.T) {
	// given
	in := `{"session_id":"s1","cwd":"/x","hook_event_name":"PreToolUse","tool_name":"Edit",
	        "tool_input":{"file_path":"/x/main.go","old_string":"a","new_string":"b"}}`
	env := map[string]string{"ZELLIJ_SESSION_NAME": "sincere-drum", "ZELLIJ_PANE_ID": "23"}
	agent := func() (proc.Proc, bool) { return proc.Proc{PID: 80, TTY: "ttys022"}, true }
	now := time.Date(2026, 11, 4, 10, 0, 0, 0, time.UTC)

	// when
	e, err := Build(strings.NewReader(in), func(k string) string { return env[k] }, agent, now)

	// then
	if err != nil {
		t.Fatal(err)
	}
	if e.Name != event.PreToolUse || e.Activity != "Edit main.go" || e.File != "/x/main.go" ||
		e.PID != 80 || e.TTY != "ttys022" || e.Mux != (mux.Location{Kind: "zellij", Session: "sincere-drum", Pane: "23"}) || !e.Time.Equal(now) {
		t.Fatalf("unexpected %+v", e)
	}
	b, _ := json.Marshal(e)
	if strings.Contains(string(b), "old_string") {
		t.Fatal("raw tool input must not be forwarded")
	}
}

func TestShouldPhrasePermissionRequestAsQuestion(t *testing.T) {
	in := `{"session_id":"s1","hook_event_name":"PermissionRequest","tool_name":"Bash","tool_input":{"command":"rm -rf build"}}`
	e, err := Build(strings.NewReader(in), func(string) string { return "" }, func() (proc.Proc, bool) { return proc.Proc{}, false }, time.Now())
	if err != nil || e.Activity != "Allow Bash: rm -rf build?" || e.PID != 0 {
		t.Fatalf("err=%v %+v", err, e)
	}
}

func TestShouldFailOnGarbage(t *testing.T) {
	_, err := Build(strings.NewReader("nope"), func(string) string { return "" }, func() (proc.Proc, bool) { return proc.Proc{}, false }, time.Now())
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestShouldReadContextUsageFromStatusLineInput(t *testing.T) {
	in := `{"session_id":"abc","model":{"id":"claude-opus-5-5"},"context_window":{"context_window_size":200000,"used_percentage":46.6}}`
	id, pct, ok := ParseStatus(strings.NewReader(in))
	if !ok || id != "abc" || pct != 47 {
		t.Fatalf("got %q %d %v", id, pct, ok)
	}
	for _, bad := range []string{
		`{"session_id":"abc","context_window":{"used_percentage":null}}`,
		`{"session_id":"abc"}`,
		`{"context_window":{"used_percentage":12}}`,
		`not json`,
	} {
		if _, _, ok := ParseStatus(strings.NewReader(bad)); ok {
			t.Errorf("%s: want no value", bad)
		}
	}
}

func noAgent() (proc.Proc, bool) { return proc.Proc{}, false }

func noEnv(string) string { return "" }

func TestShouldTranslateVibePreTool(t *testing.T) {
	// given
	in := `{"hook_event_name":"pre_tool","session_id":"v1","parent_session_id":null,"cwd":"/x",
	        "transcript_path":"/t","tool_name":"file_system.edit","tool_call_id":"call_1",
	        "tool_input":{"file_path":"/x/main.go","old_string":"a","new_string":"b"}}`
	env := map[string]string{"HERDR_SOCKET_PATH": "/h/herdr/herdr.sock", "HERDR_PANE_ID": "p3"}
	agent := func() (proc.Proc, bool) { return proc.Proc{PID: 80, TTY: "pts/4"}, true }

	// when
	e, err := Build(strings.NewReader(in), func(k string) string { return env[k] }, agent, time.Now())

	// then
	if err != nil {
		t.Fatal(err)
	}
	if e.Name != event.PreToolUse || e.SessionID != "v1" || e.Cwd != "/x" || e.ToolName != "Edit" ||
		e.Activity != "Edit main.go" || e.File != "/x/main.go" || e.PID != 80 || e.Mux.Kind != "herdr" {
		t.Fatalf("unexpected %+v", e)
	}
}

func TestShouldTranslateVibeQuestionToAskUserQuestion(t *testing.T) {
	in := `{"hook_event_name":"pre_tool","session_id":"v1","tool_name":"ui.ask_user_question",
	        "tool_input":{"questions":[{"question":"Which port?"}]}}`
	e, err := Build(strings.NewReader(in), noEnv, noAgent, time.Now())
	if err != nil || e.Name != event.PreToolUse || e.ToolName != "AskUserQuestion" || e.Activity != "Which port?" {
		t.Fatalf("err=%v %+v", err, e)
	}
}

func TestShouldTranslateVibePostToolAndPostAgent(t *testing.T) {
	cases := map[string]string{
		`{"hook_event_name":"post_tool","session_id":"v1","tool_name":"file_system.bash","tool_status":"success"}`: event.PostToolUse,
		`{"hook_event_name":"post_agent","session_id":"v1","parent_session_id":null,"agent_name":"default"}`:       event.Stop,
	}
	for in, want := range cases {
		e, err := Build(strings.NewReader(in), noEnv, noAgent, time.Now())
		if err != nil || e.Name != want || e.SessionID != "v1" {
			t.Errorf("%s: err=%v %+v", in, err, e)
		}
	}
}

func TestShouldAttributeVibeSubagentEventsToTheParentSession(t *testing.T) {
	// a subagent's turn ending is not the end of the parent's turn
	cases := map[string]string{
		`{"hook_event_name":"pre_tool","session_id":"sub","parent_session_id":"v1","tool_name":"file_system.read_file","tool_input":{"path":"/x/a.go"}}`: event.PreToolUse,
		`{"hook_event_name":"post_agent","session_id":"sub","parent_session_id":"v1","agent_name":"reviewer"}`:                                           event.PostToolUse,
	}
	for in, want := range cases {
		e, err := Build(strings.NewReader(in), noEnv, noAgent, time.Now())
		if err != nil || e.Name != want || e.SessionID != "v1" {
			t.Errorf("%s: err=%v %+v", in, err, e)
		}
	}
}

func TestShouldIgnoreUnknownVibeEvents(t *testing.T) {
	in := `{"hook_event_name":"pre_agent","session_id":"v1"}`
	if _, err := Build(strings.NewReader(in), noEnv, noAgent, time.Now()); err == nil {
		t.Fatal("expected error for an unknown lower-case event")
	}
}
