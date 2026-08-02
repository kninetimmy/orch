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
	if m.Version != "0.6.0" {
		t.Errorf("version = %q, want 0.6.0", m.Version)
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

// TestBlastRadiusCriterionGuidance holds this host's delivery skill and
// both shipped reviewer definitions to the criterion the engine
// contributes to a risk-domain issue. The pin is two-sided (see
// adaptertest.CheckBlastRadiusCriterionGuidance): rewording
// run.BlastRadiusCriterion fails it just as deleting the instruction
// here does, so the standard the binary imposes and the prose telling
// the executor and reviewer about it cannot drift apart. The TOML files
// are read raw, so the pin covers the prose wherever in the definition
// it sits.
func TestBlastRadiusCriterionGuidance(t *testing.T) {
	adaptertest.CheckBlastRadiusCriterionGuidance(t,
		deliverySkillPath,
		filepath.Join("agents", "orch-reviewer.toml"),
		filepath.Join("agents", "orch-reviewer-safe.toml"),
	)
}

// These statements keep a wrong acceptance criterion reachable, keep the
// plan gate from writing one no evidence can settle, and route a rejected
// criterion to the human inside the review call rather than through a
// second verb or the ordinary executor fix cycle. Each phrase is pinned
// without its surrounding punctuation or em dashes, so a prose reflow
// stays possible while deleting the statement fails.
//
// reviewerVerdictGroundsInstruction is the widened request-changes bar:
// both earlier forms — "applies only when a finding blocks an acceptance
// criterion" (issue #117) and "applies on exactly two grounds" (PR #147)
// — left a severe defect touching no criterion reportable but
// non-blocking, so a reviewer that found one filed it and approved. The
// pin names a failing required test and a weakened security boundary
// explicitly, since those are the two the enumeration used to exclude.
//
// A pin catches deletion or rewording of the words, and nothing more: no
// check here can observe whether a reviewer or Architect actually obeys
// the statement it pins.
const observableOutcomeCriterionGuidance = "An acceptance criterion describes an observable outcome, and never names a function, a control-flow step, or a validity notion the change is expected to introduce"
const unproducibleEvidenceCriterionGuidance = "A criterion requiring evidence the repository cannot produce is not a valid criterion"
const reviewerWrongCriterionGuidance = "You may return `request-changes` on the ground that an acceptance criterion is itself wrong"
const reviewerAddedCodeNecessityGuidance = "Before you request a change that adds code, establish that the added code needs to exist at all"
const reviewerWrongCriterionHumanGuidance = "Mark it `wrong acceptance criterion` and state the rejected criterion and reason in the consolidated report so the Architect can surface the rejected criterion and reason to the human"
const reviewerProxyEvidenceGuidance = "A criterion is also wrong when the only evidence available for it is a proxy for what it asked. A criterion requiring a check on how an agent routes, what it decides, or how it behaves is the standing instance"
const reviewerPerCriterionObservationGuidance = "Judge each acceptance criterion by number, one at a time. For each, state the specific observation that satisfies it"
const reviewerVerdictGroundsInstruction = "is not confined to findings that block an acceptance criterion: a required test that fails, a security boundary the change weakens, and any defect of comparable severity are each grounds on their own, even when no acceptance criterion names the area they sit in"
const reviewJudgmentsGuidance = "`judgments` is required and carries exactly one entry per acceptance criterion the issue holds"
const reviewApproveRequiresAllSatisfied = "A `verdict` of `approve` is accepted only when every criterion is judged `satisfied`"
const wrongCriterionInReviewCallGuidance = "A criterion judged `wrong` is the needs-human outcome, and this one `orch run review` call makes it"
const wrongCriterionNoSecondVerbGuidance = "do not call `orch run escalate` for it and do not resume the executor"
const wrongCriterionHumanGuidance = "surface the rejected criterion and reason to the human"
const codeFindingFixCycleGuidance = "A `request-changes` based on a code finding resumes the same executor with `followup_task` in the **same worktree** on the same branch: it fixes and pushes, then a **fresh** reviewer is dispatched"

// removedWrongCriterionEscalationGuidance is the instruction the v2
// review verb made false: it told the Architect to record the review and
// then call `orch run escalate` by hand, when `orch run review` now
// blocks the issue and flags it needs-human inside the same call. It is
// asserted absent, not present, so restoring the by-hand escalation step
// fails this test.
const removedWrongCriterionEscalationGuidance = "A `request-changes` report with a `wrong acceptance criterion` is not an executor fix cycle"

// TestDeliverySkillAndReviewersHaveWrongCriterionGuidance checks the
// Delivery per-criterion judgment contract, the wrong-criterion/fix-cycle
// split, and both reviewer definitions. The agent files are decoded as
// TOML rather than scanned raw, so a statement only counts when it is
// inside developer_instructions — the field the agent actually receives.
// Comparison ignores hard wraps. It fails when a pinned statement is
// deleted or reworded; it cannot tell whether any agent that reads the
// statement follows it.
func TestDeliverySkillAndReviewersHaveWrongCriterionGuidance(t *testing.T) {
	data, err := os.ReadFile(deliverySkillPath)
	if err != nil {
		t.Fatalf("read %s: %v", deliverySkillPath, err)
	}
	skill := adaptertest.NormalizeWhitespace(string(data))
	for _, phrase := range []string{
		observableOutcomeCriterionGuidance,
		unproducibleEvidenceCriterionGuidance,
		reviewJudgmentsGuidance,
		reviewApproveRequiresAllSatisfied,
		wrongCriterionInReviewCallGuidance,
		wrongCriterionNoSecondVerbGuidance,
		wrongCriterionHumanGuidance,
		codeFindingFixCycleGuidance,
	} {
		if !strings.Contains(skill, adaptertest.NormalizeWhitespace(phrase)) {
			t.Errorf("%s does not contain wrong-criterion guidance %q", deliverySkillPath, phrase)
		}
	}
	if strings.Contains(skill, adaptertest.NormalizeWhitespace(removedWrongCriterionEscalationGuidance)) {
		t.Errorf("%s still instructs a by-hand escalation after a wrong-criterion review: %q", deliverySkillPath, removedWrongCriterionEscalationGuidance)
	}
	for _, stem := range []string{"orch-reviewer", "orch-reviewer-safe"} {
		t.Run(stem, func(t *testing.T) {
			path := filepath.Join("agents", stem+".toml")
			var a agentTOML
			if _, err := toml.DecodeFile(path, &a); err != nil {
				t.Fatalf("decode %s: %v", path, err)
			}
			content := adaptertest.NormalizeWhitespace(a.DeveloperInstructions)
			for _, phrase := range []string{
				reviewerWrongCriterionGuidance,
				reviewerAddedCodeNecessityGuidance,
				reviewerWrongCriterionHumanGuidance,
				reviewerProxyEvidenceGuidance,
				reviewerPerCriterionObservationGuidance,
				reviewerVerdictGroundsInstruction,
			} {
				if !strings.Contains(content, adaptertest.NormalizeWhitespace(phrase)) {
					t.Errorf("%s: missing reviewer guidance %q", path, phrase)
				}
			}
		})
	}
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
