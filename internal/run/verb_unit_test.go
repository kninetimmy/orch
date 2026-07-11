package run

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kninetimmy/orch/internal/execx"
	"github.com/kninetimmy/orch/internal/execx/execxtest"
	"github.com/kninetimmy/orch/internal/ghops"
	"github.com/kninetimmy/orch/internal/lockfile"
	"github.com/kninetimmy/orch/internal/manifest"
	"github.com/kninetimmy/orch/internal/state"
)

func implementerDecision() *state.Decision {
	return &state.Decision{
		Role:      manifest.RoleImplementer,
		Executor:  manifest.Selection{Model: "claude-sonnet-5", Effort: "xhigh"},
		Reviewer:  manifest.Selection{Model: "claude-opus-4-8", Effort: "high"},
		Rationale: "impl",
	}
}

func specialistDecision() *state.Decision {
	return &state.Decision{
		Role:      manifest.RoleSpecialist,
		Executor:  manifest.Selection{Model: "claude-opus-4-8", Effort: "high"},
		Reviewer:  manifest.Selection{Model: "claude-opus-4-8", Effort: "high"},
		Rationale: "spec",
	}
}

func downgradedDecision() *state.Decision {
	return &state.Decision{
		Role:               manifest.RoleImplementer,
		Executor:           manifest.Selection{Model: "claude-sonnet-5", Effort: "xhigh"},
		Reviewer:           manifest.Selection{Model: "claude-sonnet-5", Effort: "high"},
		ReviewerDowngraded: true,
		Rationale:          "downgrade",
	}
}

// fixtureIssue builds a state.Issue satisfying validateIssues for phase,
// with the routing fields populated for the dispatched-onward phases.
func fixtureIssue(planID string, number int, phase state.Phase) state.Issue {
	iss := state.Issue{
		PlanID:   planID,
		Title:    "T",
		Phase:    phase,
		Number:   number,
		Branch:   fmt.Sprintf("orch/issue-%d", number),
		Worktree: fmt.Sprintf(".orchestrator/worktrees/issue-%d", number),
		Decision: implementerDecision(),
	}
	switch phase {
	case state.PhasePROpen, state.PhaseInReview:
		iss.PRNumber = 10 * number
		iss.PRURL = fmt.Sprintf("https://github.com/o/r/pull/%d", 10*number)
	case state.PhaseAwaitingMerge, state.PhaseMerged:
		iss.PRNumber = 10 * number
		iss.PRURL = fmt.Sprintf("https://github.com/o/r/pull/%d", 10*number)
		iss.ApprovedHeadOID = "head-oid"
	case state.PhaseBlocked:
		iss.BlockedReason = "seed"
	}
	return iss
}

// setupDeliveryRepo writes a delivery state with the given issues on disk
// (no git), with the plan config revision planRev.
func setupDeliveryRepo(t *testing.T, planRev string, issues []state.Issue) string {
	t.Helper()
	root := setupRepo(t, testConfigTOML)
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

func ghEnv(root string, script *execxtest.Script) Env {
	return Env{RepoRoot: root, Runner: script, Now: fixedNow}
}

func stateBytes(t *testing.T, root string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(root, ".orchestrator", "state.json"))
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// --- Wave / dependency enforcement ---

func TestDispatchDependencyEnforcement(t *testing.T) {
	a := fixtureIssue("a", 1, state.PhaseWorktreeReady)
	b := fixtureIssue("b", 2, state.PhaseWorktreeReady)
	b.DependsOn = []string{"a"}
	root := setupDeliveryRepo(t, "r1", []state.Issue{a, b})

	// Unmet: a is not merged/cleaned.
	script := &execxtest.Script{T: t}
	_, err := Dispatch(context.Background(), ghEnv(root, script), []byte(`{"schema_version":1,"issue_number":2}`))
	if !errors.Is(err, ErrDependencyUnmet) {
		t.Fatalf("err = %v, want ErrDependencyUnmet", err)
	}
	script.AssertExhausted() // failed before any git/gh call

	// Abandoned dependency: distinct message.
	st := loadRun(t, root)
	st.Run.Issues[0].Phase = state.PhaseAbandoned
	if err := state.Save(root, st); err != nil {
		t.Fatal(err)
	}
	script = &execxtest.Script{T: t}
	_, err = Dispatch(context.Background(), ghEnv(root, script), []byte(`{"schema_version":1,"issue_number":2}`))
	if !errors.Is(err, ErrDependencyAbandoned) {
		t.Fatalf("err = %v, want ErrDependencyAbandoned", err)
	}
	script.AssertExhausted()
}

// --- Config drift ---

func TestConfigDriftFailsClosed(t *testing.T) {
	root := setupDeliveryRepo(t, "r2", []state.Issue{fixtureIssue("a", 1, state.PhaseWorktreeReady)})
	script := &execxtest.Script{T: t}
	_, err := Dispatch(context.Background(), ghEnv(root, script), []byte(`{"schema_version":1,"issue_number":1}`))
	if !errors.Is(err, ErrConfigDrift) {
		t.Fatalf("err = %v, want ErrConfigDrift", err)
	}
	script.AssertExhausted()
}

// --- Secret stop ---

func TestSecretBlockStopsRun(t *testing.T) {
	root := setupDeliveryRepo(t, "r1", []state.Issue{
		fixtureIssue("a", 1, state.PhaseDispatched),
		fixtureIssue("b", 2, state.PhaseDispatched),
	})

	// A secret block stops the whole run.
	script := &execxtest.Script{T: t, Calls: []execxtest.Call{ghAuth(), ghSetStatusCall(1, ghops.StatusBlocked)}}
	res, err := Block(context.Background(), ghEnv(root, script), []byte(`{"schema_version":1,"issue_number":1,"class":"secret","detail":"found a key"}`))
	if err != nil {
		t.Fatalf("Block: %v", err)
	}
	script.AssertExhausted()
	if !res.RunStopped {
		t.Error("secret block did not stop the run")
	}
	if st := loadRun(t, root); st.Run.StoppedReason == "" {
		t.Error("StoppedReason not set after secret block")
	}

	// Every other mutating verb now fails closed.
	empty := &execxtest.Script{T: t}
	_, err = CI(context.Background(), ghEnv(root, empty), []byte(`{"schema_version":1,"issue_number":2}`))
	if !errors.Is(err, ErrRunStopped) {
		t.Fatalf("CI on stopped run = %v, want ErrRunStopped", err)
	}
	empty.AssertExhausted()

	// block itself is exempt: it can still record another block.
	script2 := &execxtest.Script{T: t, Calls: []execxtest.Call{ghAuth(), ghSetStatusCall(2, ghops.StatusBlocked)}}
	res2, err := Block(context.Background(), ghEnv(root, script2), []byte(`{"schema_version":1,"issue_number":2,"class":"hook","detail":"pre-commit failed"}`))
	if err != nil {
		t.Fatalf("Block (exempt) on stopped run: %v", err)
	}
	script2.AssertExhausted()
	if res2.RunStopped { // a non-secret block does not itself stop
		t.Error("non-secret block reported run_stopped")
	}
}

// --- Escalation ---

func TestEscalateReroute(t *testing.T) {
	root := setupDeliveryRepo(t, "r1", []state.Issue{fixtureIssue("a", 1, state.PhaseDispatched)})
	body := baseManifestBody(t)
	script := &execxtest.Script{T: t, Calls: []execxtest.Call{
		ghAuth(),
		ghSetRoleCall(1, ghops.RoleSpecialist),
		ghIssueViewCall(t, 1, "OPEN", body),
		ghSetIssueBodyCall(1),
	}}
	res, err := Escalate(context.Background(), ghEnv(root, script), []byte(`{"schema_version":1,"issue_number":1,"trigger":"implementer-hard-execution","detail":"stuck"}`))
	if err != nil {
		t.Fatalf("Escalate: %v", err)
	}
	script.AssertExhausted()
	if res.Kind != "reroute" || res.Executor == nil || res.Executor.Model != "claude-opus-4-8" {
		t.Fatalf("result = %+v, want reroute to specialist", res)
	}
	st := loadRun(t, root)
	iss := st.Run.Issues[0]
	if iss.Decision.Role != manifest.RoleSpecialist || iss.Decision.Executor.Model != "claude-opus-4-8" {
		t.Errorf("decision = %+v, want specialist", iss.Decision)
	}
	if len(iss.Attempts) != 1 || !iss.Attempts[0].Failed || iss.Attempts[0].Role != manifest.RoleImplementer {
		t.Errorf("attempts = %+v, want one failed implementer", iss.Attempts)
	}
	if iss.Phase != state.PhaseDispatched {
		t.Errorf("phase = %s, want dispatched (reroute keeps the phase)", iss.Phase)
	}
}

func TestEscalateReturnToArchitect(t *testing.T) {
	iss := fixtureIssue("a", 1, state.PhaseDispatched)
	iss.Decision = specialistDecision()
	root := setupDeliveryRepo(t, "r1", []state.Issue{iss})
	script := &execxtest.Script{T: t, Calls: []execxtest.Call{ghAuth(), ghSetStatusCall(1, ghops.StatusNeedsHuman)}}
	res, err := Escalate(context.Background(), ghEnv(root, script), []byte(`{"schema_version":1,"issue_number":1,"trigger":"weak-model-failure","detail":"exhausted"}`))
	if err != nil {
		t.Fatalf("Escalate: %v", err)
	}
	script.AssertExhausted()
	if res.Kind != "return-to-architect" || res.Reason == "" {
		t.Fatalf("result = %+v, want return-to-architect", res)
	}
	st := loadRun(t, root)
	if st.Run.Issues[0].Phase != state.PhaseBlocked || st.Run.Issues[0].BlockedReason == "" {
		t.Errorf("issue = %+v, want blocked with a reason", st.Run.Issues[0])
	}
}

func TestEscalateRestoresDowngradedReviewer(t *testing.T) {
	iss := fixtureIssue("a", 1, state.PhaseDispatched)
	iss.Decision = downgradedDecision()
	root := setupDeliveryRepo(t, "r1", []state.Issue{iss})
	body := baseManifestBody(t)
	script := &execxtest.Script{T: t, Calls: []execxtest.Call{
		ghAuth(),
		ghSetRoleCall(1, ghops.RoleSpecialist),
		ghIssueViewCall(t, 1, "OPEN", body),
		ghSetIssueBodyCall(1),
	}}
	if _, err := Escalate(context.Background(), ghEnv(root, script), []byte(`{"schema_version":1,"issue_number":1,"trigger":"implementer-hard-execution","detail":"stuck"}`)); err != nil {
		t.Fatalf("Escalate: %v", err)
	}
	script.AssertExhausted()
	d := loadRun(t, root).Run.Issues[0].Decision
	if d.ReviewerDowngraded || d.Reviewer.Model != "claude-opus-4-8" {
		t.Errorf("decision = %+v, want the strong reviewer restored", d)
	}
}

func TestEscalateBadTrigger(t *testing.T) {
	root := setupDeliveryRepo(t, "r1", []state.Issue{fixtureIssue("a", 1, state.PhaseDispatched)})
	before := stateBytes(t, root)
	script := &execxtest.Script{T: t, Calls: []execxtest.Call{ghAuth()}}
	_, err := Escalate(context.Background(), ghEnv(root, script), []byte(`{"schema_version":1,"issue_number":1,"trigger":"reviewer-uncertainty","detail":"x"}`))
	if err == nil {
		t.Fatal("Escalate accepted a mismatched trigger")
	}
	// A non-downgraded implementer decision cannot take reviewer-uncertainty.
	if !strings.Contains(err.Error(), "trigger") {
		t.Errorf("err = %v, want a trigger mismatch", err)
	}
	if got := stateBytes(t, root); string(got) != string(before) {
		t.Error("state changed on a rejected escalation")
	}
}

// --- Abandon ---

func TestAbandonClosesPRAndIssue(t *testing.T) {
	root := setupDeliveryRepo(t, "r1", []state.Issue{fixtureIssue("a", 1, state.PhasePROpen)})
	body := baseManifestBody(t)
	script := &execxtest.Script{T: t, Calls: []execxtest.Call{
		ghAuth(),
		ghPRViewCall(10, "OPEN", "head"),
		{Name: "gh", Args: []string{"pr", "close", "10"}},
		ghIssueViewCall(t, 1, "OPEN", body),
		ghSetIssueBodyCall(1),
		ghCloseIssueCall(1),
	}}
	res, err := Abandon(context.Background(), ghEnv(root, script), []byte(`{"schema_version":1,"issue_number":1,"reason":"scope dropped","statement":"abandon-issue"}`))
	if err != nil {
		t.Fatalf("Abandon: %v", err)
	}
	script.AssertExhausted()
	if res.Phase != state.PhaseAbandoned {
		t.Errorf("phase = %s, want abandoned", res.Phase)
	}
	wantPhase(t, root, 1, state.PhaseAbandoned)
}

func TestAbandonWrongStatement(t *testing.T) {
	root := setupDeliveryRepo(t, "r1", []state.Issue{fixtureIssue("a", 1, state.PhaseDispatched)})
	script := &execxtest.Script{T: t}
	_, err := Abandon(context.Background(), ghEnv(root, script), []byte(`{"schema_version":1,"issue_number":1,"reason":"x","statement":"nope"}`))
	if !errors.Is(err, ErrBadRequest) {
		t.Fatalf("err = %v, want ErrBadRequest", err)
	}
	script.AssertExhausted()
}

// --- Purity: failure paths mutate nothing before a persisted step ---

func TestReviewStaleHeadIsPure(t *testing.T) {
	root := setupDeliveryRepo(t, "r1", []state.Issue{fixtureIssue("a", 1, state.PhasePROpen)})
	before := stateBytes(t, root)
	script := &execxtest.Script{T: t, Calls: []execxtest.Call{ghAuth(), ghPRViewCall(10, "OPEN", "real-head")}}
	_, err := Review(context.Background(), ghEnv(root, script), []byte(`{"schema_version":1,"issue_number":1,"reviewed_head_oid":"stale-head","verdict":"approve","summary":"s","reviewer":{"model":"claude-opus-4-8","effort":"high"}}`))
	if !errors.Is(err, ErrReviewStale) {
		t.Fatalf("err = %v, want ErrReviewStale", err)
	}
	script.AssertExhausted()
	if got := stateBytes(t, root); string(got) != string(before) {
		t.Error("state changed on a stale review")
	}
}

func TestReviewWrongReviewerIsPure(t *testing.T) {
	root := setupDeliveryRepo(t, "r1", []state.Issue{fixtureIssue("a", 1, state.PhasePROpen)})
	before := stateBytes(t, root)
	script := &execxtest.Script{T: t} // fails before any gh call
	_, err := Review(context.Background(), ghEnv(root, script), []byte(`{"schema_version":1,"issue_number":1,"reviewed_head_oid":"head","verdict":"approve","summary":"s","reviewer":{"model":"claude-haiku-4-5","effort":"low"}}`))
	if !errors.Is(err, ErrReviewerMismatch) {
		t.Fatalf("err = %v, want ErrReviewerMismatch", err)
	}
	script.AssertExhausted()
	if got := stateBytes(t, root); string(got) != string(before) {
		t.Error("state changed on a reviewer mismatch")
	}
}

func TestBlockGitHubUnavailableIsPure(t *testing.T) {
	root := setupDeliveryRepo(t, "r1", []state.Issue{fixtureIssue("a", 1, state.PhaseDispatched)})
	before := stateBytes(t, root)
	script := &execxtest.Script{T: t, Calls: []execxtest.Call{{Name: "gh", Args: []string{"auth", "status"}, Exit: 1}}}
	_, err := Block(context.Background(), ghEnv(root, script), []byte(`{"schema_version":1,"issue_number":1,"class":"hook","detail":"x"}`))
	if !errors.Is(err, ghops.ErrNotAuthenticated) {
		t.Fatalf("err = %v, want ErrNotAuthenticated", err)
	}
	script.AssertExhausted()
	if got := stateBytes(t, root); string(got) != string(before) {
		t.Error("state changed though GitHub was unavailable before any mutation")
	}
}

func TestMergeCIFailingIsPure(t *testing.T) {
	iss := fixtureIssue("a", 1, state.PhaseAwaitingMerge)
	iss.ApprovedHeadOID = "head-oid-2"
	root := setupDeliveryRepo(t, "r1", []state.Issue{iss})
	before := stateBytes(t, root)
	script := &execxtest.Script{T: t, Calls: []execxtest.Call{
		ghAuth(),
		ghPRViewCall(10, "OPEN", "head-oid-2"),
		{Name: "gh", Args: []string{"pr", "view", "10", "--json", "statusCheckRollup"}, Stdout: `{"statusCheckRollup":[{}]}`},
		{Name: "gh", Args: []string{"pr", "checks", "10", "--required", "--json", "name,state,bucket,link"}, Exit: 1,
			Stdout: `[{"name":"build","state":"FAILURE","bucket":"fail","link":""}]`},
	}}
	req := `{"schema_version":1,"issue_number":1,"approval":{"pr_number":10,"head_oid":"head-oid-2","approved_by":"a","approved_at":"2026-07-11T12:00:00Z","statement":"approve-merge"}}`
	_, err := Merge(context.Background(), ghEnv(root, script), []byte(req))
	if !errors.Is(err, ErrCINotReady) {
		t.Fatalf("err = %v, want ErrCINotReady", err)
	}
	script.AssertExhausted() // stopped before pr merge
	if got := stateBytes(t, root); string(got) != string(before) {
		t.Error("state changed though CI blocked the merge")
	}
}

func TestMergeWrongStatementIsPure(t *testing.T) {
	root := setupDeliveryRepo(t, "r1", []state.Issue{func() state.Issue {
		iss := fixtureIssue("a", 1, state.PhaseAwaitingMerge)
		iss.ApprovedHeadOID = "head-oid-2"
		return iss
	}()})
	before := stateBytes(t, root)
	script := &execxtest.Script{T: t}
	req := `{"schema_version":1,"issue_number":1,"approval":{"pr_number":10,"head_oid":"head-oid-2","approved_by":"a","approved_at":"2026-07-11T12:00:00Z","statement":"wrong"}}`
	_, err := Merge(context.Background(), ghEnv(root, script), []byte(req))
	if !errors.Is(err, ErrMergeApproval) {
		t.Fatalf("err = %v, want ErrMergeApproval", err)
	}
	script.AssertExhausted() // rejected before opening gh
	if got := stateBytes(t, root); string(got) != string(before) {
		t.Error("state changed on a wrong merge statement")
	}
}

// --- Body-cap policy ---

func TestTruncateDetail(t *testing.T) {
	if got := truncateDetail("short"); got != "short" {
		t.Errorf("truncateDetail(short) = %q, want unchanged", got)
	}
	long := strings.Repeat("x", verificationDetailCap+500)
	got := truncateDetail(long)
	if !strings.HasSuffix(got, verificationTruncationMarker) {
		t.Errorf("truncated detail missing marker: %q", got[len(got)-40:])
	}
	if len([]rune(got)) != verificationDetailCap+len([]rune(verificationTruncationMarker)) {
		t.Errorf("truncated length = %d, want cap + marker", len([]rune(got)))
	}
}

func TestUpsertCappedDropsOldestDetails(t *testing.T) {
	big := strings.Repeat("x", 25000)
	m := manifest.Manifest{
		SchemaVersion:    manifest.SchemaVersion,
		Role:             manifest.RoleImplementer,
		Executor:         manifest.Selection{Model: "m", Effort: "e"},
		RoutingRationale: "r",
		Reviewer:         manifest.Selection{Model: "m", Effort: "e"},
		ConfigRevision:   "r1",
		Verifications: []manifest.Verification{
			{Name: "v1", Result: "pass", Detail: big, At: "t1"},
			{Name: "v2", Result: "pass", Detail: big, At: "t2"},
			{Name: "v3", Result: "pass", Detail: big, At: "t3"},
		},
	}
	body, err := upsertCapped("prose", m)
	if err != nil {
		t.Fatalf("upsertCapped: %v", err)
	}
	if len(body) > bodyCapHeadroom {
		t.Fatalf("body length %d over headroom %d", len(body), bodyCapHeadroom)
	}
	parsed, err := manifest.Parse(body)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	// The oldest verification lost its detail; the newest kept theirs; all
	// names survive (the canonical record stays intact).
	if parsed.Verifications[0].Detail != "" {
		t.Error("oldest detail not dropped")
	}
	if parsed.Verifications[2].Detail == "" {
		t.Error("newest detail dropped unnecessarily")
	}
	for i, v := range parsed.Verifications {
		if v.Name == "" || v.Result == "" {
			t.Errorf("verification %d lost its name/result: %+v", i, v)
		}
	}
}

func TestUpsertCappedHardFails(t *testing.T) {
	prose := strings.Repeat("y", githubBodyLimit)
	m := manifest.Manifest{
		SchemaVersion:    manifest.SchemaVersion,
		Role:             manifest.RoleImplementer,
		Executor:         manifest.Selection{Model: "m", Effort: "e"},
		RoutingRationale: "r",
		Reviewer:         manifest.Selection{Model: "m", Effort: "e"},
		ConfigRevision:   "r1",
		Verifications:    []manifest.Verification{{Name: "v", Result: "pass", Detail: "d", At: "t"}},
	}
	_, err := upsertCapped(prose, m)
	if !errors.Is(err, ErrBodyTooLarge) {
		t.Fatalf("err = %v, want ErrBodyTooLarge", err)
	}
	if !strings.Contains(err.Error(), fmt.Sprint(githubBodyLimit)) {
		t.Errorf("err %v does not name GitHub's limit %d", err, githubBodyLimit)
	}
}

// --- Schema v3 persistence ---

func TestActivatePersistsV3Fields(t *testing.T) {
	root := newActivateRepo(t)
	calls := append(fullTaxonomyScript(),
		ghIssueCreateCall("Issue A", []string{"ready", "feature", "implementer", "standard"}, 1),
		ghIssueCreateCall("Issue B", []string{"ready", "feature", "specialist", "critical"}, 2),
	)
	script := &execxtest.Script{T: t, Calls: calls}
	env := Env{RepoRoot: root, Runner: muxRunner{git: execx.Local{}, gh: script}, Now: fixedNow}
	if _, err := Activate(context.Background(), env, activationJSON(t, twoIssuePlanJSON())); err != nil {
		t.Fatalf("Activate: %v", err)
	}
	script.AssertExhausted()

	st := loadRun(t, root)
	b := st.Run.Issues[1]
	if b.PlanID != "b" || b.Wave != 2 || len(b.DependsOn) != 1 || b.DependsOn[0] != "a" {
		t.Errorf("issue b = %+v, want wave 2 depends_on a", b)
	}
	if b.Decision == nil || b.Decision.Role != manifest.RoleSpecialist {
		t.Errorf("issue b decision = %+v, want a persisted specialist decision", b.Decision)
	}
}

// --- Abort safe at every new phase ---

func TestAbortSafeAtEveryPhase(t *testing.T) {
	phases := []state.Phase{
		state.PhaseDispatched, state.PhasePROpen, state.PhaseInReview,
		state.PhaseAwaitingMerge, state.PhaseMerged, state.PhaseAbandoned,
		state.PhaseCleaned, state.PhaseBlocked,
	}
	for _, ph := range phases {
		t.Run(string(ph), func(t *testing.T) {
			root := setupDeliveryRepo(t, "r1", []state.Issue{fixtureIssue("a", 1, ph)})
			res, err := state.Abort(root)
			if err != nil {
				t.Fatalf("Abort at %s: %v", ph, err)
			}
			if res.PriorRun == nil {
				t.Errorf("Abort at %s did not report the prior run", ph)
			}
			after := loadRun(t, root)
			if after.Mode != state.ModeAssist {
				t.Errorf("mode after abort at %s = %s, want assist", ph, after.Mode)
			}
			if o, _ := lockfile.Inspect(root); o != nil {
				t.Errorf("lock survived abort at %s", ph)
			}
		})
	}
}
