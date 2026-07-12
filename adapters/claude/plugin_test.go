// Package claude_test validates the non-Go Claude Code plugin artifacts
// under this directory: the manifest, the hooks manifest, the agent and
// skill markdown, and their consistency with the internal/guard
// PreToolUse contract and internal/run wire contracts they mirror.
// These are ordinary Go tests so `go test ./...` catches drift without a
// separate host-specific test runner.
package claude_test

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/kninetimmy/orch/internal/guard"
	"github.com/kninetimmy/orch/internal/run"
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

// parseFrontmatter extracts the "key: value" lines between the first
// two "---" delimiter lines of path with a simple line scan — no YAML
// dependency. Every agent frontmatter value in this plugin is a
// single-line scalar, so a naive split on the first ":" per line is
// exact; a multi-line YAML block scalar would not decode correctly
// here, which is why the agent bodies below are written to avoid one.
func parseFrontmatter(t *testing.T, path string) map[string]string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	lines := strings.Split(string(data), "\n")
	if len(lines) < 2 || strings.TrimSpace(lines[0]) != "---" {
		t.Fatalf("%s: does not start with a --- frontmatter delimiter", path)
	}
	fields := map[string]string{}
	for _, line := range lines[1:] {
		if strings.TrimSpace(line) == "---" {
			return fields
		}
		idx := strings.Index(line, ":")
		if idx < 0 {
			t.Fatalf("%s: frontmatter line %q has no key:value separator", path, line)
		}
		fields[strings.TrimSpace(line[:idx])] = strings.TrimSpace(line[idx+1:])
	}
	t.Fatalf("%s: frontmatter is never closed with a second --- delimiter", path)
	return nil
}

// splitCSV splits a comma-separated frontmatter value (e.g. the tools
// list) into trimmed, non-empty entries.
func splitCSV(s string) []string {
	var out []string
	for _, part := range strings.Split(s, ",") {
		if p := strings.TrimSpace(part); p != "" {
			out = append(out, p)
		}
	}
	return out
}

// agentSpec is the committed §10 Claude profile for one agents/*.md
// role (task 17 plan item 10): its exact model and exact tool set.
type agentSpec struct {
	model string
	tools []string
}

// agentRoster is the full four-agent Claude profile this plugin ships.
var agentRoster = map[string]agentSpec{
	"orch-scout":       {model: "claude-sonnet-5", tools: []string{"Read", "Grep", "Glob", "WebFetch", "WebSearch"}},
	"orch-implementer": {model: "claude-sonnet-5", tools: []string{"Read", "Grep", "Glob", "Edit", "Write", "NotebookEdit", "Bash"}},
	"orch-specialist":  {model: "claude-opus-4-8", tools: []string{"Read", "Grep", "Glob", "Edit", "Write", "NotebookEdit", "Bash"}},
	"orch-reviewer":    {model: "claude-opus-4-8", tools: []string{"Read", "Grep", "Glob", "Bash"}},
}

// writeExcludedAgents are the roles that must never carry a write tool
// (PRD-adjacent read-only-by-construction requirement): scout because it
// only ever investigates, reviewer because "you did not write this
// change" is enforced by its tool list, not just its prompt.
var writeExcludedAgents = map[string]bool{"orch-scout": true, "orch-reviewer": true}

// writeTools are the four tools the PreToolUse guard hook mediates; a
// read-only agent must list none of them.
var writeTools = []string{"Write", "Edit", "MultiEdit", "NotebookEdit"}

// TestAgentFrontmatter validates every agents/*.md file against the
// committed §10 Claude profile: all four agents exist with a complete
// frontmatter, scout and reviewer exclude every write tool, no agent
// lists Task or an mcp__ tool (subagents have no memhub write surface),
// and models match their role exactly.
func TestAgentFrontmatter(t *testing.T) {
	for name, want := range agentRoster {
		t.Run(name, func(t *testing.T) {
			path := filepath.Join("agents", name+".md")
			fm := parseFrontmatter(t, path)

			if fm["name"] != name {
				t.Errorf("name = %q, want %q", fm["name"], name)
			}
			if fm["description"] == "" {
				t.Error("description is empty")
			}
			if fm["tools"] == "" {
				t.Fatal("tools is empty")
			}
			if fm["model"] != want.model {
				t.Errorf("model = %q, want %q", fm["model"], want.model)
			}
			if fm["model"] != "claude-sonnet-5" && fm["model"] != "claude-opus-4-8" {
				t.Errorf("model %q is not one of the two committed Claude profile models", fm["model"])
			}

			tools := splitCSV(fm["tools"])
			toolSet := make(map[string]bool, len(tools))
			for _, tool := range tools {
				toolSet[tool] = true
				if tool == "Task" {
					t.Error("tools list includes Task; no agent may spawn its own subagents")
				}
				if strings.HasPrefix(tool, "mcp__") {
					t.Errorf("tools list includes mcp__ tool %q; subagents have no memhub write surface", tool)
				}
			}
			wantTools := append([]string(nil), want.tools...)
			sort.Strings(wantTools)
			gotTools := append([]string(nil), tools...)
			sort.Strings(gotTools)
			if len(gotTools) != len(wantTools) {
				t.Errorf("tools = %v, want exactly %v", tools, want.tools)
			} else {
				for i := range gotTools {
					if gotTools[i] != wantTools[i] {
						t.Errorf("tools = %v, want exactly %v", tools, want.tools)
						break
					}
				}
			}

			if writeExcludedAgents[name] {
				for _, forbidden := range writeTools {
					if toolSet[forbidden] {
						t.Errorf("%s tools include %q, a write tool it must never have", name, forbidden)
					}
				}
			}
		})
	}
}

// skillFiles returns every skills/*/SKILL.md path.
func skillFiles(t *testing.T) []string {
	t.Helper()
	matches, err := filepath.Glob(filepath.Join("skills", "*", "SKILL.md"))
	if err != nil {
		t.Fatalf("glob skills/*/SKILL.md: %v", err)
	}
	if len(matches) == 0 {
		t.Fatal("no skills/*/SKILL.md files found")
	}
	return matches
}

// runVerbTokens is the closed set every `orch run <word>` token found in
// a skill must belong to: the 13 document-taking verbs internal/cli/run.go
// dispatches, plus "status" (orch run status --json, dispatched
// separately but still spelled "orch run status").
var runVerbTokens = map[string]bool{
	"plan": true, "activate": true, "dispatch": true, "pr-open": true,
	"review": true, "escalate": true, "ci": true, "merge-report": true,
	"merge": true, "block": true, "abandon": true, "cleanup": true,
	"complete": true, "status": true,
}

var orchRunTokenPattern = regexp.MustCompile(`orch run ([a-z-]+)`)

// TestSkillOrchRunVerbsAreReal drift-pins every `orch run <verb>` token
// mentioned in a skill against the real verb set: a renamed or removed
// verb that a skill still mentions fails this test instead of silently
// documenting a dead command.
func TestSkillOrchRunVerbsAreReal(t *testing.T) {
	for _, path := range skillFiles(t) {
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		for _, m := range orchRunTokenPattern.FindAllStringSubmatch(string(data), -1) {
			verb := m[1]
			if !runVerbTokens[verb] {
				t.Errorf("%s: mentions `orch run %s`, which is not one of the 13 verbs or status", path, verb)
			}
		}
	}
}

// statementConstants maps every internal/run approval/statement literal
// this plugin's skills may quote to the exported constant it must equal.
// Importing internal/run here means a rename of any of these constants
// breaks the build, and a value change makes the map key (read live from
// the constant, not hardcoded) no longer match the skill's hardcoded
// prose — either way, drift breaks this test instead of silently
// documenting a wrong statement.
var statementConstants = map[string]string{
	run.ApprovalStatement:      "run.ApprovalStatement",
	run.MergeApprovalStatement: "run.MergeApprovalStatement",
	run.AbandonStatement:       "run.AbandonStatement",
	run.CleanupStatement:       "run.CleanupStatement",
}

var statementLiteralPattern = regexp.MustCompile(`"statement":\s*"([a-z-]+)"`)

// TestSkillStatementLiteralsPinnedToRunConstants asserts every
// `"statement": "..."` literal quoted in a skill equals one of the
// internal/run approval/statement constants, and that every constant
// appears at least once (so a skill can never silently drop or misquote
// one of the anti-forgery statements the engine requires verbatim).
func TestSkillStatementLiteralsPinnedToRunConstants(t *testing.T) {
	seen := map[string]bool{}
	for _, path := range skillFiles(t) {
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		for _, m := range statementLiteralPattern.FindAllStringSubmatch(string(data), -1) {
			literal := m[1]
			constName, ok := statementConstants[literal]
			if !ok {
				t.Errorf("%s: statement literal %q does not equal any internal/run approval/statement constant", path, literal)
				continue
			}
			seen[constName] = true
		}
	}
	for _, constName := range statementConstants {
		if !seen[constName] {
			t.Errorf("no skill quotes the statement literal for %s", constName)
		}
	}
}

// planGateOptions are the exact §8 four options the orch-delivery skill
// must present at the plan gate, in order.
var planGateOptions = []string{
	"Approve and enter Delivery",
	"Adjust agent routing",
	"Revise scope",
	"Cancel and remain read-only",
}

// TestDeliverySkillHasPlanGateOptions pins the orch-delivery skill's
// documented plan-gate AskUserQuestion options against the exact PRD §8
// four-option set.
func TestDeliverySkillHasPlanGateOptions(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("skills", "orch-delivery", "SKILL.md"))
	if err != nil {
		t.Fatalf("read orch-delivery/SKILL.md: %v", err)
	}
	content := string(data)
	for _, opt := range planGateOptions {
		if !strings.Contains(content, opt) {
			t.Errorf("orch-delivery/SKILL.md does not contain plan-gate option %q", opt)
		}
	}
}

// setupTerminalForms are the exact three terminal-form command strings
// the orch-setup skill must document, one per interview.
var setupTerminalForms = []string{
	"orch init --bootstrap",
	"orch configure --deliver",
	"orch configure-local --apply",
}

// TestSetupSkillHasTerminalForms pins the orch-setup skill's documented
// terminal forms against the exact three commands each interview ends
// with.
func TestSetupSkillHasTerminalForms(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("skills", "orch-setup", "SKILL.md"))
	if err != nil {
		t.Fatalf("read orch-setup/SKILL.md: %v", err)
	}
	content := string(data)
	for _, form := range setupTerminalForms {
		if !strings.Contains(content, form) {
			t.Errorf("orch-setup/SKILL.md does not contain terminal form %q", form)
		}
	}
}
