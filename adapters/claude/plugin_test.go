// Package claude_test validates the non-Go Claude Code plugin artifacts
// under this directory: the manifest, the hooks manifest, and their
// consistency with the internal/guard PreToolUse contract they mirror.
// These are ordinary Go tests so `go test ./...` catches drift without a
// separate host-specific test runner.
package claude_test

import (
	"bytes"
	"encoding/json"
	"os"
	"sort"
	"strings"
	"testing"

	"github.com/kninetimmy/orch/internal/guard"
)

// pluginManifest is the strict shape of .claude-plugin/plugin.json.
type pluginManifest struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Version     string `json:"version"`
	Author      struct {
		Name string `json:"name"`
	} `json:"author"`
}

// hookEntry is one `hooks` array element under a matcher.
type hookEntry struct {
	Type    string `json:"type"`
	Command string `json:"command"`
	Timeout int    `json:"timeout,omitempty"`
}

// hookMatcher is one PreToolUse/SessionStart array element.
type hookMatcher struct {
	Matcher string      `json:"matcher"`
	Hooks   []hookEntry `json:"hooks"`
}

// hooksManifest is the strict shape of hooks/hooks.json.
type hooksManifest struct {
	Hooks struct {
		PreToolUse   []hookMatcher `json:"PreToolUse"`
		SessionStart []hookMatcher `json:"SessionStart"`
	} `json:"hooks"`
}

// decodeStrict parses path into v with DisallowUnknownFields, so an
// unexpected key in either manifest fails the test instead of silently
// passing through.
func decodeStrict(t *testing.T, path string, v any) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		t.Fatalf("decode %s: %v", path, err)
	}
}

func TestPluginManifestStrict(t *testing.T) {
	var m pluginManifest
	decodeStrict(t, ".claude-plugin/plugin.json", &m)
	if m.Name != "orch" {
		t.Errorf("name = %q, want orch", m.Name)
	}
	if m.Description == "" {
		t.Error("description is empty")
	}
	if m.Version != "0.1.0" {
		t.Errorf("version = %q, want 0.1.0", m.Version)
	}
	if m.Author.Name == "" {
		t.Error("author.name is empty")
	}
}

func loadHooksManifest(t *testing.T) hooksManifest {
	t.Helper()
	var m hooksManifest
	decodeStrict(t, "hooks/hooks.json", &m)
	return m
}

func TestHooksManifestStrict(t *testing.T) {
	m := loadHooksManifest(t)
	if len(m.Hooks.PreToolUse) == 0 {
		t.Fatal("hooks.json has no PreToolUse entries")
	}
	if len(m.Hooks.SessionStart) == 0 {
		t.Fatal("hooks.json has no SessionStart entries")
	}
	for _, event := range [][]hookMatcher{m.Hooks.PreToolUse, m.Hooks.SessionStart} {
		for _, matcher := range event {
			if len(matcher.Hooks) == 0 {
				t.Errorf("matcher %q has no hooks", matcher.Matcher)
			}
			for _, h := range matcher.Hooks {
				if h.Type != "command" {
					t.Errorf("matcher %q: hook type = %q, want command", matcher.Matcher, h.Type)
				}
				if strings.TrimSpace(h.Command) == "" {
					t.Errorf("matcher %q: empty command", matcher.Matcher)
				}
			}
		}
	}
}

// TestMatcherGuardParity pins the PreToolUse matcher's tool_name set
// against the exact set internal/guard's Claude PreToolUse handling
// accepts, in both directions: the matcher must name every guard-handled
// tool, and name nothing else (guard denies any other tool_name, so
// broadening the matcher would hard-deny it at the hook, never reaching
// guard's own decision).
func TestMatcherGuardParity(t *testing.T) {
	m := loadHooksManifest(t)
	if len(m.Hooks.PreToolUse) != 1 {
		t.Fatalf("PreToolUse has %d entries, want exactly 1", len(m.Hooks.PreToolUse))
	}
	matcher := m.Hooks.PreToolUse[0].Matcher
	got := strings.Split(matcher, "|")
	sort.Strings(got)

	want := guard.ClaudeTools()
	sort.Strings(want)

	if len(got) != len(want) {
		t.Fatalf("matcher tools = %v, want %v", got, want)
	}
	for i := range got {
		if got[i] != want[i] {
			t.Errorf("matcher tools = %v, want %v", got, want)
			break
		}
	}
}

// portabilityForbidden are shell metacharacters a bare-argv hook command
// must never contain: hook commands run directly as argv on every OS, no
// shell interposed, so a shell-syntax command would work nowhere.
const portabilityForbidden = "|><&;$%\"'`()"

func TestHookCommandsPortable(t *testing.T) {
	m := loadHooksManifest(t)
	check := func(matchers []hookMatcher) {
		for _, matcher := range matchers {
			for _, h := range matcher.Hooks {
				if strings.ContainsAny(h.Command, portabilityForbidden) {
					t.Errorf("command %q contains a shell metacharacter", h.Command)
				}
			}
		}
	}
	check(m.Hooks.PreToolUse)
	check(m.Hooks.SessionStart)
}

// TestHookCommandsPinnedToBinaryVerbs drift-pins the hook commands to the
// exact orch verbs they invoke, so a rename of either verb breaks this
// test instead of silently divorcing the plugin from the binary.
func TestHookCommandsPinnedToBinaryVerbs(t *testing.T) {
	m := loadHooksManifest(t)
	if got := m.Hooks.PreToolUse[0].Hooks[0].Command; got != "orch guard claude" {
		t.Errorf("PreToolUse command = %q, want %q", got, "orch guard claude")
	}
	if got := m.Hooks.SessionStart[0].Hooks[0].Command; got != "orch hook claude session-start" {
		t.Errorf("SessionStart command = %q, want %q", got, "orch hook claude session-start")
	}
}
