package event

import (
	"encoding/json"
	"testing"
)

func TestShouldSummarizeVibeToolCalls(t *testing.T) {
	cases := []struct {
		name, tool, input, wantActivity, wantFile string
	}{
		{"bash", "file_system.bash", `{"command":"go test ./...","timeout_seconds":60}`, "Bash: go test ./...", ""},
		{"edit", "file_system.edit", `{"file_path":"/x/main.go","old_string":"a","new_string":"b"}`, "Edit main.go", "/x/main.go"},
		{"search replace", "file_system.search_replace", `{"file_path":"/x/a.ts"}`, "Edit a.ts", "/x/a.ts"},
		{"write takes path", "file_system.write_file", `{"path":"/x/README.md","content":"..."}`, "Write README.md", "/x/README.md"},
		{"read takes path", "file_system.read_file", `{"path":"/x/pom.xml","offset":0}`, "Read pom.xml", ""},
		{"question", "ui.ask_user_question", `{"questions":[{"question":"Which port?"}]}`, "Which port?", ""},
		{"subagent", "agents.spawn", `{"config_name":"reviewer"}`, "Agent", ""},
		{"mcp tool", "mcp_linear.create_issue", `{}`, "linear: create_issue", ""},
		{"other builtin", "process.start", `{}`, "process_start", ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			// when
			act, file := Summarize(VibeTool(c.tool), json.RawMessage(c.input))

			// then
			if act != c.wantActivity {
				t.Errorf("activity = %q, want %q", act, c.wantActivity)
			}
			if file != c.wantFile {
				t.Errorf("file = %q, want %q", file, c.wantFile)
			}
		})
	}
}
