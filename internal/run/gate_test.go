package run

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kninetimmy/orch/internal/state"
)

// testConfigTOML is the on-disk committed configuration matching
// testConfig(): one host (claude), memhub off.
const testConfigTOML = `
schema_version  = 1
config_revision = "r1"

[memhub]
mode = "off"

[hosts.claude.roles.architect]
model  = "claude-opus-4-8"
effort = "xhigh"

[hosts.claude.roles.scout]
model  = "claude-sonnet-5"
effort = "low"

[hosts.claude.roles.implementer]
model  = "claude-sonnet-5"
effort = "xhigh"

[hosts.claude.roles.specialist]
model  = "claude-opus-4-8"
effort = "high"

[hosts.claude.roles.reviewer]
model  = "claude-opus-4-8"
effort = "high"

[hosts.claude.roles.review_downgrade]
model  = "claude-sonnet-5"
effort = "high"
`

// setupRepo returns a temp repo root with .orchestrator/config.toml
// written from tomlContent. No state.json is written, so Load reports
// Assist.
func setupRepo(t *testing.T, tomlContent string) string {
	t.Helper()
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, ".orchestrator"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, ".orchestrator", "config.toml"), []byte(tomlContent), 0o644); err != nil {
		t.Fatal(err)
	}
	return root
}

func fixedNow() time.Time { return time.Date(2026, 7, 11, 12, 0, 0, 0, time.UTC) }

func TestPlanGoldenTwoIssue(t *testing.T) {
	root := setupRepo(t, testConfigTOML)
	env := Env{RepoRoot: root, Now: fixedNow}

	doc, err := Plan(context.Background(), env, []byte(twoIssuePlanJSON()))
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}

	if doc.SchemaVersion != GateSchemaVersion {
		t.Errorf("SchemaVersion = %d, want %d", doc.SchemaVersion, GateSchemaVersion)
	}
	if doc.PlanTitle != "Two issue plan" || doc.Host != "claude" {
		t.Errorf("PlanTitle/Host = %q/%q", doc.PlanTitle, doc.Host)
	}
	if doc.ConfigRevision != "r1" || doc.MergeStrategy != "squash" {
		t.Errorf("ConfigRevision/MergeStrategy = %q/%q", doc.ConfigRevision, doc.MergeStrategy)
	}
	if len(doc.ConfigOverrides) != 0 {
		t.Errorf("ConfigOverrides = %v, want none", doc.ConfigOverrides)
	}
	if doc.Memhub.Mode != "off" || doc.Memhub.Probe != "skipped" {
		t.Errorf("Memhub = %+v, want off/skipped", doc.Memhub)
	}
	if doc.CI.WorkflowsPresent {
		t.Error("CI.WorkflowsPresent = true, want false (no workflow fixture)")
	}
	if !strings.Contains(doc.CI.Statement, "no .github/workflows files found") {
		t.Errorf("CI.Statement = %q", doc.CI.Statement)
	}

	p, err := DecodePlan([]byte(twoIssuePlanJSON()))
	if err != nil {
		t.Fatal(err)
	}
	wantDigest, err := p.Digest()
	if err != nil {
		t.Fatal(err)
	}
	if doc.PlanDigest != wantDigest {
		t.Errorf("PlanDigest = %q, want %q", doc.PlanDigest, wantDigest)
	}

	if len(doc.Issues) != 2 {
		t.Fatalf("Issues = %+v, want 2", doc.Issues)
	}
	a, b := doc.Issues[0], doc.Issues[1]

	if a.ID != "a" || a.Role != "implementer" || a.ReviewerDowngraded {
		t.Errorf("issue a = %+v", a)
	}
	if a.Executor.Model != "claude-sonnet-5" || a.Executor.Effort != "xhigh" {
		t.Errorf("issue a executor = %+v", a.Executor)
	}
	if a.Reviewer.Model != "claude-opus-4-8" || a.Reviewer.Effort != "high" {
		t.Errorf("issue a reviewer = %+v", a.Reviewer)
	}
	wantRationaleA := "Routed to an implementer with a strong reviewer with no reviewer downgrade requested."
	if a.RoutingRationale != wantRationaleA {
		t.Errorf("issue a rationale = %q, want %q", a.RoutingRationale, wantRationaleA)
	}
	if a.Risk != "standard" {
		t.Errorf("issue a risk = %q, want standard", a.Risk)
	}
	wantLabelsA := []string{"ready", "feature", "implementer", "standard"}
	if strings.Join(a.Labels, ",") != strings.Join(wantLabelsA, ",") {
		t.Errorf("issue a labels = %v, want %v", a.Labels, wantLabelsA)
	}

	if b.ID != "b" || b.Role != "specialist" || b.ReviewerDowngraded {
		t.Errorf("issue b = %+v", b)
	}
	if b.Executor.Model != "claude-opus-4-8" || b.Executor.Effort != "high" {
		t.Errorf("issue b executor = %+v", b.Executor)
	}
	wantRationaleB := "Routed to a specialist with a strong reviewer because risk domains (concurrency)."
	if b.RoutingRationale != wantRationaleB {
		t.Errorf("issue b rationale = %q, want %q", b.RoutingRationale, wantRationaleB)
	}
	if b.Risk != "critical" {
		t.Errorf("issue b risk = %q, want critical", b.Risk)
	}
	wantLabelsB := []string{"ready", "feature", "specialist", "critical"}
	if strings.Join(b.Labels, ",") != strings.Join(wantLabelsB, ",") {
		t.Errorf("issue b labels = %v, want %v", b.Labels, wantLabelsB)
	}
	if len(b.DependsOn) != 1 || b.DependsOn[0] != "a" || b.Wave != 2 {
		t.Errorf("issue b deps/wave = %v/%d", b.DependsOn, b.Wave)
	}
}

func TestPlanCIStatementWithWorkflowFixture(t *testing.T) {
	root := setupRepo(t, testConfigTOML)
	wfDir := filepath.Join(root, ".github", "workflows")
	if err := os.MkdirAll(wfDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(wfDir, "ci.yml"), []byte("name: ci\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	env := Env{RepoRoot: root, Now: fixedNow}

	doc, err := Plan(context.Background(), env, []byte(validPlanJSON()))
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	if !doc.CI.WorkflowsPresent {
		t.Error("CI.WorkflowsPresent = false, want true")
	}
	if !strings.Contains(doc.CI.Statement, "are present") {
		t.Errorf("CI.Statement = %q", doc.CI.Statement)
	}
}

func TestPlanErrDeliveryActive(t *testing.T) {
	root := setupRepo(t, testConfigTOML)
	if _, err := state.EnterDelivery(root, "claude", state.PlanRef{Digest: "sha256:x"}, []state.Issue{{PlanID: "a", Phase: state.PhasePlanned}}); err != nil {
		t.Fatal(err)
	}
	env := Env{RepoRoot: root, Now: fixedNow}

	_, err := Plan(context.Background(), env, []byte(validPlanJSON()))
	if !errors.Is(err, ErrDeliveryActive) {
		t.Fatalf("err = %v, want ErrDeliveryActive", err)
	}
}
