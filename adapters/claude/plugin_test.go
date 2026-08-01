// Package claude_test validates the non-Go Claude Code plugin artifacts
// under this directory: the manifest, the hooks manifest, the agent and
// skill markdown, and their consistency with the internal/guard
// PreToolUse contract and internal/run wire contracts they mirror. These
// are ordinary Go tests so `go test ./...` catches drift without a
// separate host-specific test runner. Cross-host invariants shared with
// the Codex adapter live in internal/adaptertest (PRD §23's shared
// parity layer); this file only holds Claude-specific fixtures and
// checks.
package claude_test

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/kninetimmy/orch/internal/adaptertest"
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
	adaptertest.CheckMatcherEqualsGuardTools(t, m.Hooks.PreToolUse[0].Matcher, guard.ClaudeTools())
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
// role's Claude-specific fields not already covered by
// adaptertest.Profile: its exact tool set. Profile("claude") pins the
// model directly; Claude frontmatter has no effort field to assert.
type agentSpec struct {
	tools []string
}

// agentRoster is the full five-agent Claude profile this plugin ships.
// Profile("claude") also carries a "reviewer-safe" entry, matched here by
// orch-reviewer-safe: Claude Code's Task tool model parameter only takes
// coarse tier aliases (sonnet/opus/haiku/fable), so a per-spawn override
// cannot express a routed selection. A separate installed agent is shipped
// instead because only an installed definition carries its own system
// prompt (the safe-downgrade instructions) and a distinct name this
// frontmatter-match rule can verify — the effort difference from the full
// reviewer rides the spawn-time prompt cue, not a frontmatter field (see
// the comment above on agentSpec: Claude frontmatter has no effort field
// to assert).
var agentRoster = map[string]agentSpec{
	"orch-scout":         {tools: []string{"Read", "Grep", "Glob", "WebFetch", "WebSearch"}},
	"orch-implementer":   {tools: []string{"Read", "Grep", "Glob", "Edit", "Write", "NotebookEdit", "Bash"}},
	"orch-specialist":    {tools: []string{"Read", "Grep", "Glob", "Edit", "Write", "NotebookEdit", "Bash"}},
	"orch-reviewer":      {tools: []string{"Read", "Grep", "Glob", "Bash"}},
	"orch-reviewer-safe": {tools: []string{"Read", "Grep", "Glob", "Bash"}},
}

// writeExcludedAgents are the roles that must never carry a write tool
// (PRD-adjacent read-only-by-construction requirement): scout because it
// only ever investigates, reviewer and reviewer-safe because "you did
// not write this change" is enforced by their tool list, not just their
// prompt.
var writeExcludedAgents = map[string]bool{"orch-scout": true, "orch-reviewer": true, "orch-reviewer-safe": true}

// writeTools are the four tools the PreToolUse guard hook mediates; a
// read-only agent must list none of them.
var writeTools = []string{"Write", "Edit", "MultiEdit", "NotebookEdit"}

// TestAgentFrontmatter validates every agents/*.md file against the
// committed §10 Claude profile: all five agents exist with a complete
// frontmatter, scout, reviewer, and reviewer-safe exclude every write
// tool, no agent lists Task or an mcp__ tool (memhub writes are
// Architect-only policy; banning mcp__ removes a direct memhub write
// surface but does not by itself make a Bash-carrying agent
// memhub-incapable, since memhub is an external CLI), and models match
// adaptertest.Profile("claude") for their role exactly.
func TestAgentFrontmatter(t *testing.T) {
	profile := adaptertest.Profile("claude")
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

			role := strings.TrimPrefix(name, "orch-")
			roleSpec, ok := profile[role]
			if !ok {
				t.Fatalf("no adaptertest.Profile(\"claude\") entry for role %q", role)
			}
			if fm["model"] != roleSpec.Model {
				t.Errorf("model = %q, want %q", fm["model"], roleSpec.Model)
			}

			tools := splitCSV(fm["tools"])
			toolSet := make(map[string]bool, len(tools))
			for _, tool := range tools {
				toolSet[tool] = true
				if tool == "Task" {
					t.Error("tools list includes Task; no agent may spawn its own subagents")
				}
				if strings.HasPrefix(tool, "mcp__") {
					t.Errorf("tools list includes mcp__ tool %q; memhub writes are Architect-only", tool)
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

// reviewerCoverageInstruction and reviewerBlockingVerdictInstruction
// are the two prose anchors the reviewer agents' "## Report" section
// must carry: the finding stage is about coverage, not a severity
// filter, and the approve/request-changes verdict is a separate
// judgment turning on a closed pair of grounds. The verdict anchor
// enumerates both grounds rather than only the blocking one (its
// original issue #117 form): a wrong-criterion finding does not block
// an acceptance criterion — it rejects one — so an exclusive blocking
// rule would foreclose the very verdict the "When a criterion is
// itself wrong" section grants, and a reviewer reading the file in
// order would take the later rule and approve. Neither anchor includes
// the backtick-quoted `request-changes` token itself, so a formatting
// change around that token doesn't make this check brittle.
const reviewerCoverageInstruction = "this stage is about coverage, not filtering"
const reviewerBlockingVerdictInstruction = "applies on exactly two grounds — a finding blocks an acceptance criterion, or an acceptance criterion is itself wrong"

// TestReviewerReportsCoverageAndBlockingVerdict checks the two reviewer
// agent files directly by stem, not via agentRoster: agentRoster is a
// name-keyed map iterated by TestAgentFrontmatter, so a file it doesn't
// list would go unchecked there, and a body-content assertion has
// nothing to do with the tools/model fields that map exists for. This
// mechanically guards the coverage-not-filtering and blocking-only-verdict
// instructions instead of leaving them as prose a future reader must
// re-verify by hand.
func TestReviewerReportsCoverageAndBlockingVerdict(t *testing.T) {
	for _, stem := range []string{"orch-reviewer", "orch-reviewer-safe"} {
		t.Run(stem, func(t *testing.T) {
			path := filepath.Join("agents", stem+".md")
			data, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("read %s: %v", path, err)
			}
			content := adaptertest.NormalizeWhitespace(string(data))
			if !strings.Contains(content, reviewerCoverageInstruction) {
				t.Errorf("%s: missing coverage instruction %q", path, reviewerCoverageInstruction)
			}
			if !strings.Contains(content, reviewerBlockingVerdictInstruction) {
				t.Errorf("%s: missing blocking-only verdict instruction %q", path, reviewerBlockingVerdictInstruction)
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

// These statements keep a wrong acceptance criterion reachable and route
// it to the human rather than through the ordinary executor fix cycle.
// Each phrase is pinned without its surrounding punctuation or em dashes,
// so a prose reflow stays possible while deleting the statement fails.
const observableOutcomeCriterionGuidance = "An acceptance criterion describes an observable outcome, and never names a function, a control-flow step, or a validity notion the change is expected to introduce"
const reviewerWrongCriterionGuidance = "You may return `request-changes` on the ground that an acceptance criterion is itself wrong"
const reviewerAddedCodeNecessityGuidance = "Before you request a change that adds code, establish that the added code needs to exist at all"
const reviewerWrongCriterionHumanGuidance = "Mark it `wrong acceptance criterion` and state the rejected criterion and reason in the consolidated report so the Architect can surface the rejected criterion and reason to the human"
const wrongCriterionEscalationGuidance = "A `request-changes` report with a `wrong acceptance criterion` is not an executor fix cycle"
const wrongCriterionEscalationTrigger = "call `orch run escalate` with `trigger` `architectural-ambiguity`"
const wrongCriterionHumanGuidance = "surface the rejected criterion and reason to the human"
const codeFindingFixCycleGuidance = "A `request-changes` based on a code finding resumes the same executor in the **same worktree** on the same branch: it fixes and pushes, then a **fresh** reviewer is spawned"

// TestDeliverySkillAndReviewersHaveWrongCriterionGuidance checks the
// Delivery escalation/fix-cycle split and both reviewer definitions. Like
// TestReviewerReportsCoverageAndBlockingVerdict above, the reviewer files
// are addressed by stem rather than through agentRoster, whose job is the
// tools/model frontmatter. Comparison ignores hard wraps.
func TestDeliverySkillAndReviewersHaveWrongCriterionGuidance(t *testing.T) {
	data, err := os.ReadFile(deliverySkillPath)
	if err != nil {
		t.Fatalf("read %s: %v", deliverySkillPath, err)
	}
	skill := adaptertest.NormalizeWhitespace(string(data))
	for _, phrase := range []string{
		observableOutcomeCriterionGuidance,
		wrongCriterionEscalationGuidance,
		wrongCriterionEscalationTrigger,
		wrongCriterionHumanGuidance,
		codeFindingFixCycleGuidance,
	} {
		if !strings.Contains(skill, adaptertest.NormalizeWhitespace(phrase)) {
			t.Errorf("%s does not contain wrong-criterion guidance %q", deliverySkillPath, phrase)
		}
	}
	for _, stem := range []string{"orch-reviewer", "orch-reviewer-safe"} {
		t.Run(stem, func(t *testing.T) {
			path := filepath.Join("agents", stem+".md")
			data, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("read %s: %v", path, err)
			}
			content := adaptertest.NormalizeWhitespace(string(data))
			for _, phrase := range []string{reviewerWrongCriterionGuidance, reviewerAddedCodeNecessityGuidance, reviewerWrongCriterionHumanGuidance} {
				if !strings.Contains(content, adaptertest.NormalizeWhitespace(phrase)) {
					t.Errorf("%s: missing reviewer guidance %q", path, phrase)
				}
			}
		})
	}
}

// frontmatterMatchRuleSentence is the exact phrase
// orch-delivery/SKILL.md must state: Claude Code's Task tool model
// parameter is a coarse tier-alias enum (sonnet/opus/haiku/fable), so
// no per-spawn override can express an exact routed version — only an
// installed agent definition's frontmatter pins one — and a routed
// selection matching no installed frontmatter must stop and tell the
// human rather than silently spawning a mismatched agent.
const frontmatterMatchRuleSentence = "stop and tell the human"

// TestDeliverySkillStatesFrontmatterMatchRule is this adapter's twin of
// the Codex adapter's TestDeliverySkillStatesTOMLMatchRule: the no-match
// escalation rule must be stated verbatim, not just implied. Both spawn
// steps carry it — the executor spawn and the reviewer spawn — so the
// phrase is required at least twice, not merely present somewhere. The
// count runs on whitespace-normalized text on both sides (via
// adaptertest.NormalizeWhitespace), so a site the markdown hard-wraps
// across a line break still counts.
func TestDeliverySkillStatesFrontmatterMatchRule(t *testing.T) {
	data, err := os.ReadFile(deliverySkillPath)
	if err != nil {
		t.Fatalf("read %s: %v", deliverySkillPath, err)
	}
	content := adaptertest.NormalizeWhitespace(string(data))
	phrase := adaptertest.NormalizeWhitespace(frontmatterMatchRuleSentence)
	if got := strings.Count(content, phrase); got < 2 {
		t.Errorf("%s contains the verbatim phrase %q %d time(s), want it at both the executor and reviewer spawn steps",
			deliverySkillPath, frontmatterMatchRuleSentence, got)
	}
}
