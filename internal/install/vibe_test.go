package install

import (
	"strings"
	"testing"

	"github.com/BurntSushi/toml"
)

const vibeExisting = `# Herdr support for Vibe
[[hooks]]
name = "herdr-working"
type = "pre_tool"
command = "cat >/dev/null"
timeout = 5.0
`

type vibeHook struct {
	Name, Type, Command, Match string
	Timeout                    float64
}

func vibeHooksOf(t *testing.T, b []byte) []vibeHook {
	t.Helper()
	var f struct{ Hooks []vibeHook }
	if _, err := toml.Decode(string(b), &f); err != nil {
		t.Fatalf("invalid TOML: %v\n%s", err, b)
	}
	return f.Hooks
}

func TestShouldAddVibeHooksAndKeepForeignOnes(t *testing.T) {
	// when
	out := MergeVibeHooks([]byte(vibeExisting), `/home/o'neil/go/bin/cyberviber`)

	// then
	hooks := vibeHooksOf(t, out)
	if len(hooks) != 4 || hooks[0].Name != "herdr-working" {
		t.Fatalf("got %+v", hooks)
	}
	types := map[string]bool{}
	for _, h := range hooks[1:] {
		if !strings.HasPrefix(h.Name, "cyberviber-") || h.Command != `'/home/o'\''neil/go/bin/cyberviber' hook # cyberviber` || h.Timeout != 5 {
			t.Errorf("unexpected %+v", h)
		}
		types[h.Type] = true
	}
	if !types["pre_tool"] || !types["post_tool"] || !types["post_agent"] {
		t.Fatalf("types = %v", types)
	}
	if !strings.HasPrefix(string(out), vibeExisting) {
		t.Fatal("foreign content must stay untouched")
	}
}

func TestShouldReplaceVibeHooksOnReinstall(t *testing.T) {
	once := MergeVibeHooks([]byte(vibeExisting), "/old/cyberviber")
	twice := MergeVibeHooks(once, "/new/cyberviber")
	hooks := vibeHooksOf(t, twice)
	if len(hooks) != 4 || strings.Contains(string(twice), "/old/") {
		t.Fatalf("got %+v", hooks)
	}
}

func TestShouldRemoveOnlyVibeHooksOfCyberviber(t *testing.T) {
	merged := MergeVibeHooks([]byte(vibeExisting), "/usr/local/bin/cyberviber")
	if got := string(RemoveVibeHooks(merged)); got != vibeExisting {
		t.Fatalf("got\n%s", got)
	}
	if got := RemoveVibeHooks(MergeVibeHooks(nil, "/x/cyberviber")); len(got) != 0 {
		t.Fatalf("want empty, got %q", got)
	}
}
