package event

import (
	"encoding/json"
	"net/url"
	"path/filepath"
	"strings"
)

// MaxActivity is the maximum length (in runes) of an activity text.
const MaxActivity = 80

type toolInput struct {
	Command      string `json:"command"`
	Description  string `json:"description"`
	FilePath     string `json:"file_path"`
	NotebookPath string `json:"notebook_path"`
	Path         string `json:"path"`
	Pattern      string `json:"pattern"`
	URL          string `json:"url"`
	Query        string `json:"query"`
	Questions    []struct {
		Question string `json:"question"`
	} `json:"questions"`
}

// Summarize turns a tool call into a short activity text. For tools that change a file it
// also returns the file path, so the daemon can count changed files per turn.
func Summarize(tool string, raw json.RawMessage) (activity, editedFile string) {
	var in toolInput
	if len(raw) > 0 && json.Unmarshal(raw, &in) != nil {
		return truncate(tool), ""
	}
	if in.FilePath == "" {
		in.FilePath = in.Path
	}
	switch tool {
	case "Bash":
		text := in.Description
		if text == "" {
			text, _, _ = strings.Cut(in.Command, "\n")
		}
		if text == "" {
			return "Bash", ""
		}
		return truncate("Bash: " + text), ""
	case "Edit", "MultiEdit", "Write":
		verb := "Edit"
		if tool == "Write" {
			verb = "Write"
		}
		return truncate(verb + " " + filepath.Base(in.FilePath)), in.FilePath
	case "NotebookEdit":
		return truncate("Edit " + filepath.Base(in.NotebookPath)), in.NotebookPath
	case "Read":
		return truncate("Read " + filepath.Base(in.FilePath)), ""
	case "Grep", "Glob":
		return truncate(tool + " " + in.Pattern), ""
	case "WebFetch":
		if u, err := url.Parse(in.URL); err == nil && u.Host != "" {
			return truncate("Fetch " + u.Host), ""
		}
		return "Fetch", ""
	case "WebSearch":
		return truncate("Search " + in.Query), ""
	case "Agent", "Task":
		if in.Description == "" {
			return "Agent", ""
		}
		return truncate("Agent: " + in.Description), ""
	case "AskUserQuestion":
		if len(in.Questions) > 0 && in.Questions[0].Question != "" {
			return truncate(in.Questions[0].Question), ""
		}
		return "Asking a question", ""
	}
	if rest, ok := strings.CutPrefix(tool, "mcp__"); ok {
		if server, name, ok := strings.Cut(rest, "__"); ok {
			return truncate(server + ": " + name), ""
		}
	}
	return truncate(tool), ""
}

func truncate(s string) string {
	s = strings.Join(strings.Fields(s), " ")
	r := []rune(s)
	if len(r) <= MaxActivity {
		return s
	}
	return string(r[:MaxActivity-1]) + "…"
}
