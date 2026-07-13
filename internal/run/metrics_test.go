package run

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/kninetimmy/orch/internal/execx"
	"github.com/kninetimmy/orch/internal/execx/execxtest"
	"github.com/kninetimmy/orch/internal/metrics"
	"github.com/kninetimmy/orch/internal/paths"
	"github.com/kninetimmy/orch/internal/state"
)

// metricsEnabledConfigTOML mirrors testConfigTOML with metrics enabled,
// for the tests in this file that need Metrics.Enabled true on disk.
const metricsEnabledConfigTOML = testConfigTOML + "\n[metrics]\nenabled = true\n"

// setupDeliveryRepoWithConfig mirrors verb_unit_test.go's
// setupDeliveryRepo but writes tomlContent instead of the fixed
// testConfigTOML, so a test can flip Metrics.Enabled without touching
// the shared helper every other verb test relies on.
func setupDeliveryRepoWithConfig(t *testing.T, tomlContent, planRev string, issues []state.Issue) string {
	t.Helper()
	root := setupRepo(t, tomlContent)
	planned := make([]state.Issue, len(issues))
	for i := range issues {
		planned[i] = state.Issue{PlanID: issues[i].PlanID, Phase: state.PhasePlanned}
	}
	st, err := state.EnterDelivery(root, "claude", state.PlanRef{Title: "t", Digest: "sha256:x", ConfigRevision: planRev}, planned)
	if err != nil {
		t.Fatal(err)
	}
	st.Run.Issues = issues
	if err := state.Save(root, st); err != nil {
		t.Fatal(err)
	}
	return root
}

// newActivateRepoWithConfig mirrors activate_test.go's
// newActivateRepoWithIgnore but writes tomlContent instead of the fixed
// testConfigTOML, so the activation metrics test can enable metrics on
// disk.
func newActivateRepoWithConfig(t *testing.T, tomlContent string) string {
	t.Helper()
	setupGitEnv(t)
	root, err := paths.Canonical(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	rawGit(t, root, "init", "-b", "main")
	if err := os.WriteFile(filepath.Join(root, "README.md"), []byte("hi\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, ".gitignore"), []byte(fullGitignore), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, ".orchestrator"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, ".orchestrator", "config.toml"), []byte(tomlContent), 0o644); err != nil {
		t.Fatal(err)
	}
	rawGit(t, root, "add", "-A")
	rawGit(t, root, "commit", "-m", "initial")
	return root
}

func loadMetricsDocs(t *testing.T, root string) []metrics.Document {
	t.Helper()
	docs, err := metrics.LoadAll(root)
	if err != nil {
		t.Fatalf("metrics.LoadAll: %v", err)
	}
	return docs
}

// assertNoMetricsDir pins the PRD §23 acceptance criterion: disabled
// metrics create no storage at all.
func assertNoMetricsDir(t *testing.T, root string) {
	t.Helper()
	if _, err := os.Stat(filepath.Join(root, filepath.FromSlash(metrics.Dir))); !os.IsNotExist(err) {
		t.Errorf("metrics dir exists (stat err = %v), want absent", err)
	}
}

// reviewApproveJSON builds a valid Review request for issue 1, echoing
// the routed reviewer implementerDecision() carries.
func reviewApproveJSON(usage string) string {
	extra := ""
	if usage != "" {
		extra = `,"usage":` + usage
	}
	return `{"schema_version":1,"issue_number":1,"reviewed_head_oid":"head-oid-1","verdict":"approve","summary":"looks good","reviewer":{"model":"claude-opus-4-8","effort":"high"}` + extra + `}`
}

// TestReviewRecordsMetricWhenEnabled pins internal/run/metrics.go's
// wiring for one verb (review, per the spec's dispatch-or-review
// choice — review needs no git sandbox, only scripted gh): with
// metrics enabled, a successful Review call records exactly one event
// carrying the verb-specific fields the spec's table names.
func TestReviewRecordsMetricWhenEnabled(t *testing.T) {
	root := setupDeliveryRepoWithConfig(t, metricsEnabledConfigTOML, "r1", []state.Issue{fixtureIssue("a", 1, state.PhasePROpen)})
	body := baseManifestBody(t)
	script := &execxtest.Script{T: t, Calls: []execxtest.Call{
		ghAuth(), ghPRViewCall(10, "OPEN", "head-oid-1"),
		ghIssueViewCall(t, 1, "OPEN", body), ghSetIssueBodyCall(1), ghSetPRBodyCall(10),
	}}
	usageJSON := `{"input_tokens":100,"output_tokens":50,"cache_read_tokens":10,"cache_creation_tokens":5,"duration_ms":1200}`
	_, err := Review(context.Background(), ghEnv(root, script), []byte(reviewApproveJSON(usageJSON)))
	if err != nil {
		t.Fatalf("Review: %v", err)
	}
	script.AssertExhausted()

	docs := loadMetricsDocs(t, root)
	if len(docs) != 1 {
		t.Fatalf("docs = %+v, want 1", docs)
	}
	st := loadRun(t, root)
	if docs[0].RunID != st.Run.ID {
		t.Errorf("doc run id = %q, want %q", docs[0].RunID, st.Run.ID)
	}
	if len(docs[0].Events) != 1 {
		t.Fatalf("events = %+v, want 1", docs[0].Events)
	}
	ev := docs[0].Events[0]
	if ev.Verb != "review" || ev.IssueNumber != 1 {
		t.Errorf("event verb/issue = %q/%d", ev.Verb, ev.IssueNumber)
	}
	if ev.Verdict != "approve" || ev.ReviewCycles != 1 {
		t.Errorf("event verdict/cycles = %q/%d, want approve/1", ev.Verdict, ev.ReviewCycles)
	}
	if ev.Reviewer == nil || ev.Reviewer.Model != "claude-opus-4-8" || ev.Reviewer.Effort != "high" {
		t.Errorf("event reviewer = %+v, want the routed reviewer", ev.Reviewer)
	}
	if ev.Usage == nil || ev.Usage.InputTokens != 100 || ev.Usage.OutputTokens != 50 || ev.Usage.DurationMS != 1200 {
		t.Errorf("event usage = %+v, want the request's usage", ev.Usage)
	}
	if ev.At != fixedNow().Format(time.RFC3339) {
		t.Errorf("event at = %q, want the fixed clock stamp", ev.At)
	}
}

// TestReviewMetricsDisabledCreatesNoStorage pins PRD §23: the same
// successful verb call against the ordinary (metrics-disabled)
// testConfigTOML never creates .orchestrator/metrics at all.
func TestReviewMetricsDisabledCreatesNoStorage(t *testing.T) {
	root := setupDeliveryRepo(t, "r1", []state.Issue{fixtureIssue("a", 1, state.PhasePROpen)})
	body := baseManifestBody(t)
	script := &execxtest.Script{T: t, Calls: []execxtest.Call{
		ghAuth(), ghPRViewCall(10, "OPEN", "head-oid-1"),
		ghIssueViewCall(t, 1, "OPEN", body), ghSetIssueBodyCall(1), ghSetPRBodyCall(10),
	}}
	_, err := Review(context.Background(), ghEnv(root, script), []byte(reviewApproveJSON("")))
	if err != nil {
		t.Fatalf("Review: %v", err)
	}
	script.AssertExhausted()
	assertNoMetricsDir(t, root)
}

// TestReviewNegativeUsageIsBadRequestBeforeMutation pins the wire
// validation the spec adds to Review's request-validation block: a
// negative usage field is rejected as ErrBadRequest before loadVerb
// ever runs, so no gh call happens and state is untouched.
func TestReviewNegativeUsageIsBadRequestBeforeMutation(t *testing.T) {
	root := setupDeliveryRepo(t, "r1", []state.Issue{fixtureIssue("a", 1, state.PhasePROpen)})
	before := stateBytes(t, root)
	script := &execxtest.Script{T: t}
	req := reviewApproveJSON(`{"input_tokens":-1}`)
	_, err := Review(context.Background(), ghEnv(root, script), []byte(req))
	if !errors.Is(err, ErrBadRequest) {
		t.Fatalf("err = %v, want ErrBadRequest", err)
	}
	script.AssertExhausted()
	if got := stateBytes(t, root); string(got) != string(before) {
		t.Error("state changed on a negative-usage review request")
	}
	assertNoMetricsDir(t, root)
}

// TestPROpenNegativeUsageIsBadRequestBeforeMutation is the pr-open
// half of the same wire validation.
func TestPROpenNegativeUsageIsBadRequestBeforeMutation(t *testing.T) {
	root := setupDeliveryRepo(t, "r1", []state.Issue{fixtureIssue("a", 1, state.PhaseDispatched)})
	before := stateBytes(t, root)
	script := &execxtest.Script{T: t}
	req := `{"schema_version":1,"issue_number":1,"verifications":[{"name":"go test","result":"pass"}],"usage":{"duration_ms":-5}}`
	_, err := PROpen(context.Background(), ghEnv(root, script), []byte(req))
	if !errors.Is(err, ErrBadRequest) {
		t.Fatalf("err = %v, want ErrBadRequest", err)
	}
	script.AssertExhausted()
	if got := stateBytes(t, root); string(got) != string(before) {
		t.Error("state changed on a negative-usage pr-open request")
	}
	assertNoMetricsDir(t, root)
}

// TestActivateRecordsOneMetricPerIssueWhenEnabled pins activate.go's
// gated per-issue metrics loop (the verb has no verbCtx, so it appends
// directly rather than through recordMetric).
func TestActivateRecordsOneMetricPerIssueWhenEnabled(t *testing.T) {
	root := newActivateRepoWithConfig(t, metricsEnabledConfigTOML)
	calls := fullTaxonomyScript()
	calls = append(calls,
		ghIssueCreateCall("Issue A", []string{"ready", "feature", "implementer", "standard"}, 1),
		ghIssueCreateCall("Issue B", []string{"ready", "feature", "specialist", "critical"}, 2),
	)
	script := &execxtest.Script{T: t, Calls: calls}
	env := Env{RepoRoot: root, Runner: muxRunner{git: execx.Local{}, gh: script}, Now: fixedNow}

	result, err := Activate(context.Background(), env, activationJSON(t, twoIssuePlanJSON()))
	if err != nil {
		t.Fatalf("Activate: %v", err)
	}
	script.AssertExhausted()

	docs := loadMetricsDocs(t, root)
	if len(docs) != 1 {
		t.Fatalf("docs = %+v, want 1", docs)
	}
	if docs[0].RunID != result.RunID {
		t.Errorf("doc run id = %q, want %q", docs[0].RunID, result.RunID)
	}
	if len(docs[0].Events) != 2 {
		t.Fatalf("events = %+v, want one activate event per issue", docs[0].Events)
	}
	for i, want := range []struct {
		issueNumber int
		role        string
	}{
		{1, "implementer"},
		{2, "specialist"},
	} {
		ev := docs[0].Events[i]
		if ev.Verb != "activate" || ev.IssueNumber != want.issueNumber || ev.Role != want.role {
			t.Errorf("event[%d] = %+v, want verb=activate issue=%d role=%s", i, ev, want.issueNumber, want.role)
		}
		if ev.Executor == nil || ev.Rationale == "" {
			t.Errorf("event[%d] missing executor/rationale: %+v", i, ev)
		}
	}
}

// TestActivateMetricsDisabledCreatesNoStorage is activation's §23 pin:
// the ordinary metrics-disabled testConfigTOML never creates
// .orchestrator/metrics.
func TestActivateMetricsDisabledCreatesNoStorage(t *testing.T) {
	root := newActivateRepo(t)
	calls := fullTaxonomyScript()
	calls = append(calls, ghIssueCreateCall("Fix the status lock race", []string{"ready", "bug", "implementer", "standard"}, 1))
	script := &execxtest.Script{T: t, Calls: calls}
	env := Env{RepoRoot: root, Runner: muxRunner{git: execx.Local{}, gh: script}, Now: fixedNow}

	if _, err := Activate(context.Background(), env, activationJSON(t, validPlanJSON())); err != nil {
		t.Fatalf("Activate: %v", err)
	}
	script.AssertExhausted()
	assertNoMetricsDir(t, root)
}
