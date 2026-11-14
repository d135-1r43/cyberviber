package event

import "strings"

// Mistral Vibe hook event names.
const (
	VibePreTool   = "pre_tool"
	VibePostTool  = "post_tool"
	VibePostAgent = "post_agent"
)

var vibeTools = map[string]string{
	"file_system.bash":           "Bash",
	"file_system.edit":           "Edit",
	"file_system.search_replace": "Edit",
	"file_system.write_file":     "Write",
	"file_system.read_file":      "Read",
	"ui.ask_user_question":       "AskUserQuestion",
	"agents.spawn":               "Agent",
}

// VibeTool maps a Vibe tool name (always qualified in hook payloads, e.g. "file_system.bash")
// to the Claude Code tool Summarize knows; MCP tools ("mcp_<server>.<tool>") to Claude's form.
func VibeTool(name string) string {
	if t, ok := vibeTools[name]; ok {
		return t
	}
	if rest, ok := strings.CutPrefix(name, "mcp_"); ok {
		if server, tool, ok := strings.Cut(rest, "."); ok {
			return "mcp__" + server + "__" + tool
		}
	}
	return strings.ReplaceAll(name, ".", "_")
}
