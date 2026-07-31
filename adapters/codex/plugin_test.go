// Package codex_test validates the non-Go Codex CLI plugin artifacts
// under this directory: the manifest, the hooks manifest, the agent
// TOMLs and skill markdown, and their consistency with the
// internal/guard PreToolUse contract and internal/run wire contracts
// they mirror. These are ordinary Go tests so `go test ./...` catches
// drift without a separate host-specific test runner. Cross-host
// invariants shared with the Claude adapter live in internal/adaptertest
// (PRD §23's shared parity layer); this file only holds Codex-specific
// fixtures and checks.
package codex_test

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/BurntSushi/toml"

	"github.com/kninetimmy/orch/internal/adaptertest"
	"github.com/kninetimmy/orch/internal/guard"
)

// pluginManifest is the strict shape of .codex-plugin/plugin.json.
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
	decodeStrict(t, ".codex-plugin/plugin.json", &m)
	if m.Name != "orch" {
		t.Errorf("name = %q, want orch", m.Name)
	}
	if m.Description == "" {
		t.Error("description is empty")
	}
	if m.Version != "0.5.9" {
		t.Errorf("version = %q, want 0.5.9", m.Version)
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
// against the exact set internal/guard's Codex PreToolUse handling
// accepts, in both directions: the matcher must name every guard-handled
// tool, and name nothing else (guard denies any other tool_name, so
// broadening the matcher would hard-deny it at the hook, never reaching
// guard's own decision).
func TestMatcherGuardParity(t *testing.T) {
	m := loadHooksManifest(t)
	if len(m.Hooks.PreToolUse) != 1 {
		t.Fatalf("PreToolUse has %d entries, want exactly 1", len(m.Hooks.PreToolUse))
	}
	adaptertest.CheckMatcherEqualsGuardTools(t, m.Hooks.PreToolUse[0].Matcher, guard.CodexTools())
}

func TestHookCommandsPortable(t *testing.T) {
	m := loadHooksManifest(t)
	var commands []string
	for _, matcher := range m.Hooks.PreToolUse {
		for _, h := range matcher.Hooks {
			commands = append(commands, h.Command)
		}
	}
	for _, matcher := range m.Hooks.SessionStart {
		for _, h := range matcher.Hooks {
			commands = append(commands, h.Command)
		}
	}
	adaptertest.CheckHookCommandPortability(t, commands)
}

// TestHookCommandsPinnedToBinaryVerbs drift-pins the hook commands to the
// exact orch verbs they invoke, so a rename of either verb breaks this
// test instead of silently divorcing the plugin from the binary.
func TestHookCommandsPinnedToBinaryVerbs(t *testing.T) {
	m := loadHooksManifest(t)
	if got := m.Hooks.PreToolUse[0].Hooks[0].Command; got != "orch guard codex" {
		t.Errorf("PreToolUse command = %q, want %q", got, "orch guard codex")
	}
	if got := m.Hooks.SessionStart[0].Hooks[0].Command; got != "orch hook codex session-start" {
		t.Errorf("SessionStart command = %q, want %q", got, "orch hook codex session-start")
	}
}

// agentTOML is the strict shape of one agents/*.toml file.
type agentTOML struct {
	Name                  string `toml:"name"`
	Description           string `toml:"description"`
	DeveloperInstructions string `toml:"developer_instructions"`
	Model                 string `toml:"model"`
	ModelReasoningEffort  string `toml:"model_reasoning_effort"`
}

// agentFiles is the full five-agent Codex profile this plugin ships,
// naming each TOML by its file stem.
var agentFiles = []string{
	"orch-scout", "orch-implementer", "orch-specialist",
	"orch-reviewer", "orch-reviewer-safe",
}

// readOnlyAgents are the roles whose developer_instructions must state
// they must not modify the repository — scout because it only ever
// investigates, reviewer and reviewer-safe because "you did not write
// this change" is a contract enforced by instructions alone: this host
// has no per-agent tool whitelist at all, and the guard hook enforces
// containment only, not role read-only-ness.
var readOnlyAgents = map[string]bool{"orch-scout": true, "orch-reviewer": true, "orch-reviewer-safe": true}

// readOnlySentinel is the phrase this test standardizes across the
// read-only agents' developer_instructions to assert read-only
// discipline is actually stated, not just implied.
const readOnlySentinel = "must not modify"

// TestAgentTOMLs validates every agents/*.toml file against the
// committed §10 Codex profile: strict TOML decode (no unrecognized
// keys), name matches its filename, non-empty description and
// developer_instructions, (model, model_reasoning_effort) matches
// adaptertest.Profile("codex") for its role, no MCP or subagent-spawning
// grant, and read-only agents state read-only discipline explicitly.
func TestAgentTOMLs(t *testing.T) {
	profile := adaptertest.Profile("codex")
	for _, name := range agentFiles {
		t.Run(name, func(t *testing.T) {
			path := filepath.Join("agents", name+".toml")
			var a agentTOML
			meta, err := toml.DecodeFile(path, &a)
			if err != nil {
				t.Fatalf("decode %s: %v", path, err)
			}
			if undecoded := meta.Undecoded(); len(undecoded) != 0 {
				t.Errorf("%s: unrecognized keys %v", path, undecoded)
			}

			if a.Name != name {
				t.Errorf("name = %q, want %q", a.Name, name)
			}
			if a.Description == "" {
				t.Error("description is empty")
			}
			if a.DeveloperInstructions == "" {
				t.Fatal("developer_instructions is empty")
			}

			role := strings.TrimPrefix(name, "orch-")
			want, ok := profile[role]
			if !ok {
				t.Fatalf("no adaptertest.Profile(\"codex\") entry for role %q", role)
			}
			if a.Model != want.Model {
				t.Errorf("model = %q, want %q", a.Model, want.Model)
			}
			if a.ModelReasoningEffort != want.Effort {
				t.Errorf("model_reasoning_effort = %q, want %q", a.ModelReasoningEffort, want.Effort)
			}

			if strings.Contains(a.DeveloperInstructions, "mcp__") {
				t.Errorf("%s: developer_instructions mentions an mcp__ tool; memhub writes are Architect-only", path)
			}

			if readOnlyAgents[name] {
				normalized := adaptertest.NormalizeWhitespace(a.DeveloperInstructions)
				sentinel := adaptertest.NormalizeWhitespace(readOnlySentinel)
				if !strings.Contains(normalized, sentinel) {
					t.Errorf("%s: developer_instructions does not contain the read-only sentinel %q", path, readOnlySentinel)
				}
			}
		})
	}
}

// skillGlob is the pattern every shared skill-drift check in this
// package scans.
const skillGlob = "skills/*/SKILL.md"

const deliverySkillPath = "skills/orch-delivery/SKILL.md"
const setupSkillPath = "skills/orch-setup/SKILL.md"

func TestSkillOrchRunVerbsAreReal(t *testing.T) {
	adaptertest.CheckRunVerbTokens(t, skillGlob)
}

func TestSkillStatementLiteralsPinnedToRunConstants(t *testing.T) {
	adaptertest.CheckStatementLiterals(t, skillGlob)
}

func TestDeliverySkillHasPlanGateOptions(t *testing.T) {
	adaptertest.CheckPlanGateOptions(t, deliverySkillPath)
}

func TestDeliverySkillHasMergeGateOptions(t *testing.T) {
	adaptertest.CheckMergeGateOptions(t, deliverySkillPath)
}

func TestSetupSkillHasTerminalForms(t *testing.T) {
	adaptertest.CheckSetupTerminalForms(t, setupSkillPath)
}

func TestDeliverySkillHasRoutedSelectionCue(t *testing.T) {
	adaptertest.CheckRoutedSelectionCue(t, deliverySkillPath)
}

func TestDeliverySkillHasBranchScopeVerificationGuidance(t *testing.T) {
	adaptertest.CheckBranchScopeVerificationGuidance(t, deliverySkillPath)
}

// TestDeliverySkillPinsCodexChildUsageMapping keeps the adapter's optional
// exact-usage capture scoped to the completed task that produced it: the
// initial executor and fresh reviewer use full totals, while a resumed fix
// executor goes to the following review cycle as a delta.
func TestDeliverySkillPinsCodexChildUsageMapping(t *testing.T) {
	data, err := os.ReadFile(deliverySkillPath)
	if err != nil {
		t.Fatalf("read %s: %v", deliverySkillPath, err)
	}
	content := adaptertest.NormalizeWhitespace(string(data))
	for _, phrase := range []string{
		"`CODEX_THREAD_ID`",
		"canonical task identity returned by `spawn_agent`",
		"`orch hook codex subagent-usage`",
		"`previous_total_tokens`",
		"previous captured cumulative total",
		"initial executor full total to `pr-open`'s `usage`",
		"fresh reviewer full total to that cycle's `review` `usage`",
		"resumed fix executor delta to the following `review`'s `executor_usage`",
		"When capture returns no total, omit the corresponding optional field",
	} {
		if !strings.Contains(content, adaptertest.NormalizeWhitespace(phrase)) {
			t.Errorf("%s does not contain child-usage mapping phrase %q", deliverySkillPath, phrase)
		}
	}
}

// tomlMatchRuleSentence is the exact phrase orch-delivery/SKILL.md must
// state: on Codex there is no per-spawn model override, so a routed
// selection that matches no installed orch-* TOML must stop and tell
// the human rather than silently dispatching a mismatched agent.
const tomlMatchRuleSentence = "stop and tell the human"

// TestDeliverySkillStatesTOMLMatchRule pins the Codex half of a rule
// both hosts carry: orch-delivery/SKILL.md must state the no-match
// escalation rule verbatim, not just imply it. Claude Code's twin is
// TestDeliverySkillStatesFrontmatterMatchRule in
// adapters/claude/plugin_test.go — its Task tool's model parameter is a
// coarse tier-alias enum, so a per-spawn override cannot express an
// exact routed version there either, and only an installed agent
// definition's frontmatter pins one. The containment check runs on
// whitespace-normalized text on both sides (via
// adaptertest.NormalizeWhitespace), so a site the markdown hard-wraps
// across a line break still counts.
func TestDeliverySkillStatesTOMLMatchRule(t *testing.T) {
	data, err := os.ReadFile(deliverySkillPath)
	if err != nil {
		t.Fatalf("read %s: %v", deliverySkillPath, err)
	}
	content := adaptertest.NormalizeWhitespace(string(data))
	phrase := adaptertest.NormalizeWhitespace(tomlMatchRuleSentence)
	if !strings.Contains(content, phrase) {
		t.Errorf("%s does not contain the verbatim phrase %q", deliverySkillPath, tomlMatchRuleSentence)
	}
}

// TestNoCommandsShipped pins the recorded decision that this adapter
// ships no commands/ directory: Codex custom prompts (slash commands)
// are deprecated, so skills are invoked directly instead.
func TestNoCommandsShipped(t *testing.T) {
	if _, err := os.Stat("commands"); !os.IsNotExist(err) {
		t.Errorf("commands/ exists (err = %v); this adapter ships skills-only, no commands", err)
	}
}
