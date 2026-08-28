package opencode_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kninetimmy/orch/adapters/opencode"
	"github.com/kninetimmy/orch/internal/adaptertest"
)

func TestPackagePinsOpenCodeV2Contract(t *testing.T) {
	var manifest struct {
		Name         string            `json:"name"`
		Version      string            `json:"version"`
		Dependencies map[string]string `json:"dependencies"`
	}
	if err := json.Unmarshal([]byte(opencode.PackageJSON), &manifest); err != nil {
		t.Fatal(err)
	}
	if manifest.Name != "@kninetimmy/orch-opencode" || manifest.Version == "" {
		t.Fatalf("manifest = %#v", manifest)
	}
	if got := manifest.Dependencies["@opencode-ai/plugin"]; got != "0.0.0-beta-18314" {
		t.Fatalf("@opencode-ai/plugin = %q, want pinned beta-18314", got)
	}
	if len(manifest.Dependencies) != 1 {
		t.Fatalf("dependencies = %v, want only the V2 plugin API", manifest.Dependencies)
	}
}

func TestInstallGuideAddsCheckoutPluginAndSkillSources(t *testing.T) {
	data, err := os.ReadFile("README.md")
	if err != nil {
		t.Fatal(err)
	}
	content := string(data)
	for _, want := range []string{
		`"plugins": ["/absolute/path/to/orch/adapters/opencode/src/index.js"]`,
		`"skills": ["/absolute/path/to/orch/adapters/opencode/skills"]`,
		"entries to add, not replacement arrays",
	} {
		if !strings.Contains(content, want) {
			t.Errorf("README.md missing additive installation guidance %q", want)
		}
	}
}

func TestNativeAgents(t *testing.T) {
	profile := adaptertest.Profile("opencode")
	for role, spec := range profile {
		name := "orch-" + role
		model := spec.Model
		if spec.Variant != "" {
			model += "#" + spec.Variant
		}
		data, err := opencode.AgentDefinitions.ReadFile("agents/" + name + ".md")
		if err != nil {
			t.Fatal(err)
		}
		text := string(data)
		for _, want := range []string{"mode: subagent", "model: " + model, `action: subagent, resource: "*", effect: deny`} {
			if !strings.Contains(text, want) {
				t.Errorf("%s missing %q", name, want)
			}
		}
		readOnly := name == "orch-scout" || strings.HasPrefix(name, "orch-reviewer")
		for _, action := range []string{"edit", "shell"} {
			hasDeny := strings.Contains(text, "action: "+action+`, resource: "*", effect: deny`)
			if hasDeny != readOnly {
				t.Errorf("%s %s deny = %v, want %v", name, action, hasDeny, readOnly)
			}
		}
	}
}

func TestSkillsUseOnlyOpenCodeV2Surfaces(t *testing.T) {
	forbidden := []string{
		"request_user_input", "spawn_agent", "wait_agent", "followup_task",
		"CODEX_THREAD_ID", "Codex rollout", "Codex marketplace", "Codex hook",
		"agent TOML", "agent-TOML",
	}
	for _, name := range []string{"orch-architect", "orch-delivery", "orch-setup"} {
		data, err := opencode.Skills.ReadFile("skills/" + name + "/SKILL.md")
		if err != nil {
			t.Fatal(err)
		}
		text := string(data)
		for _, word := range forbidden {
			if strings.Contains(text, word) {
				t.Errorf("%s refers to Codex-only surface %q", name, word)
			}
		}
	}
}

const skillGlob = "skills/*/SKILL.md"
const architectSkillPath = "skills/orch-architect/SKILL.md"
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

func TestSetupSkillShowsCatalogOptionsInOnePicker(t *testing.T) {
	data, err := opencode.Skills.ReadFile(setupSkillPath)
	if err != nil {
		t.Fatal(err)
	}
	raw := string(data)
	parts := strings.SplitN(raw, "---", 3)
	if len(parts) != 3 || !strings.Contains(adaptertest.NormalizeWhitespace(parts[1]), "one native picker") {
		t.Errorf("%s metadata does not describe one native picker", setupSkillPath)
	}
	content := adaptertest.NormalizeWhitespace(raw)
	for _, phrase := range []string{
		"When `pagination` is present, ignore its presentation pages on OpenCode",
		"present every parent `options[]` entry together, in emitted order, in one `question` call",
		"Never present or follow the emitted `next`, `previous`, or `cancel` actions",
		"record `answers[question.id] = option.value`",
		"Do not accept OpenCode's custom-answer choice for these closed catalog-backed selects",
	} {
		if !strings.Contains(content, adaptertest.NormalizeWhitespace(phrase)) {
			t.Errorf("%s does not contain one-picker guidance %q", setupSkillPath, phrase)
		}
	}
	for _, stale := range []string{"pagination.pages[0]", "move one page", "page's `index`/`total`"} {
		if strings.Contains(content, stale) {
			t.Errorf("%s retains paginated OpenCode navigation guidance %q", setupSkillPath, stale)
		}
	}
}

func TestDeliverySkillHasRoutedSelectionCue(t *testing.T) {
	adaptertest.CheckOpenCodeRoutedSelectionCue(t, deliverySkillPath)
}

func TestSelectionWireVersionsMatchEngine(t *testing.T) {
	adaptertest.CheckSelectionWireVersions(t, deliverySkillPath, architectSkillPath)
}

func TestDeliverySkillHasBranchScopeVerificationGuidance(t *testing.T) {
	adaptertest.CheckBranchScopeVerificationGuidance(t, deliverySkillPath)
}

func TestBlastRadiusCriterionGuidance(t *testing.T) {
	adaptertest.CheckBlastRadiusCriterionGuidance(t,
		deliverySkillPath,
		filepath.Join("agents", "orch-reviewer.md"),
		filepath.Join("agents", "orch-reviewer-safe.md"),
	)
}

func TestDeliverySkillHasReviewJudgmentContract(t *testing.T) {
	data, err := opencode.Skills.ReadFile(deliverySkillPath)
	if err != nil {
		t.Fatal(err)
	}
	content := adaptertest.NormalizeWhitespace(string(data))
	for _, phrase := range []string{
		"An acceptance criterion describes an observable outcome, and never names a function, a control-flow step, or a validity notion the change is expected to introduce",
		"A criterion requiring evidence the repository cannot produce is not a valid criterion",
		"`judgments` is required and carries exactly one entry per acceptance criterion the issue holds",
		"A `verdict` of `approve` is accepted only when every criterion is judged `satisfied`",
		"A criterion judged `wrong` is the needs-human outcome, and this one `orch run review` call makes it",
		"do not call `orch run escalate` for it and do not resume the executor",
		"surface the rejected criterion and reason to the human",
		"dispatches a fresh implementation subagent through OpenCode's `subagent` tool into the **same worktree** on the same branch",
	} {
		if !strings.Contains(content, adaptertest.NormalizeWhitespace(phrase)) {
			t.Errorf("%s does not contain review-contract guidance %q", deliverySkillPath, phrase)
		}
	}
}

func TestReviewersHaveMatureReviewContract(t *testing.T) {
	for _, stem := range []string{"orch-reviewer", "orch-reviewer-safe"} {
		data, err := opencode.AgentDefinitions.ReadFile("agents/" + stem + ".md")
		if err != nil {
			t.Fatal(err)
		}
		content := adaptertest.NormalizeWhitespace(string(data))
		for _, phrase := range []string{
			"You may return `request-changes` on the ground that an acceptance criterion is itself wrong",
			"Before you request a change that adds code, establish that the added code needs to exist at all",
			"Mark it `wrong acceptance criterion` and state the rejected criterion and reason in the consolidated report so the Architect can surface the rejected criterion and reason to the human",
			"A criterion is also wrong when the only evidence available for it is a proxy for what it asked. A criterion requiring a check on how an LLM agent routes, what it decides, or how it behaves is the standing instance",
			"Judge each acceptance criterion by number, one at a time. For each, state the specific observation that satisfies it",
			"this stage is about coverage, not filtering",
			"is not confined to findings that block an acceptance criterion: a required test that fails, a security boundary the change weakens, and any defect of comparable severity are each grounds on their own, even when no acceptance criterion names the area they sit in",
		} {
			if !strings.Contains(content, adaptertest.NormalizeWhitespace(phrase)) {
				t.Errorf("%s: missing reviewer guidance %q", stem, phrase)
			}
		}
	}
}
