package install

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const (
	vibeBegin = "# >>> cyberviber: managed by `cyberviber install`, removed by `cyberviber uninstall`\n"
	vibeEnd   = "# <<< cyberviber\n"
)

var vibeEvents = []string{"pre_tool", "post_tool", "post_agent"}

// VibeDir is Mistral Vibe's home ($VIBE_HOME, default ~/.vibe).
func VibeDir() string {
	if d := os.Getenv("VIBE_HOME"); d != "" {
		return d
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".vibe")
}

// VibeInstalled reports whether Mistral Vibe has been set up on this machine.
func VibeInstalled() bool {
	fi, err := os.Stat(VibeDir())
	return err == nil && fi.IsDir()
}

// MergeVibeHooks returns a Vibe hooks.toml with cyberviber's hooks appended in a marked block.
// An earlier block is replaced; everything else, comments included, stays byte for byte.
func MergeVibeHooks(hooksToml []byte, bin string) []byte {
	out := RemoveVibeHooks(hooksToml)
	if len(out) > 0 && !bytes.HasSuffix(out, []byte("\n")) {
		out = append(out, '\n')
	}
	var b strings.Builder
	b.WriteString(vibeBegin)
	for _, ev := range vibeEvents {
		fmt.Fprintf(&b, "[[hooks]]\nname = \"cyberviber-%s\"\ntype = \"%s\"\ncommand = %s\ntimeout = 5.0\n",
			strings.ReplaceAll(ev, "_", "-"), ev, tomlString(command(bin, "hook")))
	}
	b.WriteString(vibeEnd)
	return append(out, b.String()...)
}

// RemoveVibeHooks drops cyberviber's block from a Vibe hooks.toml.
func RemoveVibeHooks(hooksToml []byte) []byte {
	s := string(hooksToml)
	start := strings.Index(s, vibeBegin)
	if start < 0 {
		return hooksToml
	}
	end := strings.Index(s[start:], vibeEnd)
	if end < 0 {
		return hooksToml
	}
	return []byte(s[:start] + s[start+end+len(vibeEnd):])
}

func tomlString(s string) string {
	return `"` + strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(s) + `"`
}
