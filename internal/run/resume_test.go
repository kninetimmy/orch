package run

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/kninetimmy/orch/internal/execx"
	"github.com/kninetimmy/orch/internal/execx/execxtest"
	"github.com/kninetimmy/orch/internal/ghops"
	"github.com/kninetimmy/orch/internal/lockfile"
	"github.com/kninetimmy/orch/internal/manifest"
	"github.com/kninetimmy/orch/internal/state"
)

func pbool(b bool) *bool { return &b }

// blockedFrom turns an in-flight fixture into a blocked issue that keeps
// its populated fields, so re-derivation (row 30) has something to work
// from.
func blockedFrom(base state.Issue) state.Issue {
	base.Phase = state.PhaseBlocked
	base.BlockedReason = "seed"
	return base
}

// okManifest is the observation of a healthy audit record with n
// verifications.
func okManifest(n int) issueObservations {
	return issueObservations{read: true, issueState: "OPEN", manifestOK: pbool(true), verifications: n, localBranch: pbool(true), worktree: pbool(true)}
}

// --- Pure decision table (rows 1-30) ---

func TestReconcileIssue(t *testing.T) {
	ic := fixtureIssue("a", 1, state.PhaseIssueCreated)
	wr := fixtureIssue("a", 1, state.PhaseWorktreeReady)
	di := fixtureIssue("a", 1, state.PhaseDispatched)
	po := fixtureIssue("a", 1, state.PhasePROpen)
	ir := fixtureIssue("a", 1, state.PhaseInReview)
	ir.LastReviewVerdict = "approve"
	am := fixtureIssue("a", 1, state.PhaseAwaitingMerge) // ApprovedHeadOID = "head-oid"

	openPR := func(head string) *ghops.PR { return &ghops.PR{Number: 10, State: "OPEN", HeadRefOid: head} }

	cases := []struct {
		name       string
		iss        state.Issue
		obs        issueObservations
		wantAction ResumeAction
		wantPhase  state.Phase
		wantReason string
		wantClear  bool
		wantAdopt  int
	}{
		// Row 1.
		{"planned", state.Issue{PlanID: "a", Phase: state.PhasePlanned}, issueObservations{}, ActionKept, state.PhasePlanned, "still planned", false, 0},
		// Row 2.
		{"issue-created advance", ic, issueObservations{read: true, issueState: "OPEN", manifestOK: pbool(true), localBranch: pbool(true), worktree: pbool(true)}, ActionAdvanced, state.PhaseWorktreeReady, "adopted the branch", false, 0},
		// Row 3.
		{"issue-created half", ic, issueObservations{read: true, issueState: "OPEN", manifestOK: pbool(true), localBranch: pbool(true), worktree: pbool(false)}, ActionKept, state.PhaseIssueCreated, "incomplete", false, 0},
		// Row 4.
		{"issue-created closed", ic, issueObservations{read: true, issueState: "CLOSED", manifestOK: pbool(true)}, ActionKept, state.PhaseIssueCreated, "closed", false, 0},
		{"issue-created drift", ic, issueObservations{read: true, issueState: "OPEN", manifestOK: pbool(false), manifestErr: manifest.ErrDrift}, ActionKept, state.PhaseIssueCreated, "unusable", false, 0},
		// Row 5.
		{"worktree-ready keep", wr, okManifest(0), ActionKept, state.PhaseWorktreeReady, "no PR yet", false, 0},
		// Row 6.
		{"worktree-ready drift", wr, issueObservations{read: true, issueState: "OPEN", manifestOK: pbool(false), manifestErr: manifest.ErrDrift, localBranch: pbool(true), worktree: pbool(true)}, ActionBlocked, state.PhaseBlocked, "drifted", false, 0},
		{"worktree-ready malformed", wr, issueObservations{read: true, issueState: "OPEN", manifestOK: pbool(false), manifestErr: manifest.ErrBadManifest, localBranch: pbool(true), worktree: pbool(true)}, ActionBlocked, state.PhaseBlocked, "malformed", false, 0},
		// Row 7.
		{"worktree-ready closed", wr, issueObservations{read: true, issueState: "CLOSED", manifestOK: pbool(true), localBranch: pbool(true), worktree: pbool(true)}, ActionBlocked, state.PhaseBlocked, "closed outside orch", false, 0},
		// Row 8.
		{"worktree-ready branch missing", wr, issueObservations{read: true, issueState: "OPEN", manifestOK: pbool(true), localBranch: pbool(false), worktree: pbool(true)}, ActionBlocked, state.PhaseBlocked, "missing", false, 0},
		// Row 9.
		{"worktree-ready orphan PR", wr, issueObservations{read: true, issueState: "OPEN", manifestOK: pbool(true), localBranch: pbool(true), worktree: pbool(true), prForBranch: &ghops.PR{Number: 7}}, ActionBlocked, state.PhaseBlocked, "never dispatched", false, 0},
		// Row 10.
		{"dispatched keep", di, okManifest(0), ActionKept, state.PhaseDispatched, "awaiting pr-open", false, 0},
		// Row 11.
		{"dispatched drift", di, issueObservations{read: true, issueState: "OPEN", manifestOK: pbool(false), manifestErr: manifest.ErrDrift, localBranch: pbool(true), worktree: pbool(true)}, ActionBlocked, state.PhaseBlocked, "drifted", false, 0},
		// Row 12.
		{"dispatched adopt", di, issueObservations{read: true, issueState: "OPEN", manifestOK: pbool(true), verifications: 1, localBranch: pbool(true), worktree: pbool(true), prForBranch: &ghops.PR{Number: 34, URL: "u34"}}, ActionAdoptedPR, state.PhasePROpen, "adopted PR #34", false, 34},
		// Row 13.
		{"dispatched no evidence", di, issueObservations{read: true, issueState: "OPEN", manifestOK: pbool(true), verifications: 0, localBranch: pbool(true), worktree: pbool(true), prForBranch: &ghops.PR{Number: 34}}, ActionBlocked, state.PhaseBlocked, "no verification evidence", false, 0},
		// Row 14.
		{"pr-open keep", po, issueObservations{read: true, issueState: "OPEN", manifestOK: pbool(true), localBranch: pbool(true), worktree: pbool(true), pr: openPR("h"), ciRead: true, ci: ghops.CIPassing}, ActionKept, state.PhasePROpen, "PR #10 open", false, 0},
		// Row 15.
		{"pr-open merged oob", po, issueObservations{read: true, issueState: "OPEN", manifestOK: pbool(true), localBranch: pbool(true), worktree: pbool(true), pr: &ghops.PR{Number: 10, State: "MERGED", HeadRefOid: "h"}}, ActionBlocked, state.PhaseBlocked, "merged out of band", false, 0},
		// Row 16.
		{"pr-open closed", po, issueObservations{read: true, issueState: "OPEN", manifestOK: pbool(true), localBranch: pbool(true), worktree: pbool(true), pr: &ghops.PR{Number: 10, State: "CLOSED"}}, ActionBlocked, state.PhaseBlocked, "closed without merge", false, 0},
		// Row 17.
		{"pr-open issue closed", po, issueObservations{read: true, issueState: "CLOSED", manifestOK: pbool(true), localBranch: pbool(true), worktree: pbool(true), pr: openPR("h")}, ActionBlocked, state.PhaseBlocked, "closed outside orch while", false, 0},
		// Row 18.
		{"pr-open drift", po, issueObservations{read: true, issueState: "OPEN", manifestOK: pbool(false), manifestErr: manifest.ErrDrift, localBranch: pbool(true), worktree: pbool(true), pr: openPR("h")}, ActionBlocked, state.PhaseBlocked, "drifted", false, 0},
		// Row 19.
		{"in-review approve keep", ir, issueObservations{read: true, issueState: "OPEN", manifestOK: pbool(true), localBranch: pbool(true), worktree: pbool(true), pr: openPR("h"), ciRead: true, ci: ghops.CIPassing}, ActionKept, state.PhaseInReview, "PR #10 open", false, 0},
		// Row 20.
		{"awaiting merged match", am, issueObservations{read: true, issueState: "CLOSED", pr: &ghops.PR{Number: 10, State: "MERGED", HeadRefOid: "head-oid"}}, ActionKept, state.PhaseAwaitingMerge, "merged", false, 0},
		// Row 21.
		{"awaiting merged mismatch", am, issueObservations{read: true, issueState: "CLOSED", pr: &ghops.PR{Number: 10, State: "MERGED", HeadRefOid: "other"}}, ActionBlocked, state.PhaseBlocked, "never approved", false, 0},
		// Row 22.
		{"awaiting open match", am, issueObservations{read: true, issueState: "OPEN", pr: openPR("head-oid")}, ActionKept, state.PhaseAwaitingMerge, "approved head", false, 0},
		// Row 23.
		{"awaiting head moved", am, issueObservations{read: true, issueState: "OPEN", pr: openPR("moved")}, ActionDemoted, state.PhaseInReview, "head moved", true, 0},
		// Row 24.
		{"awaiting closed", am, issueObservations{read: true, issueState: "OPEN", pr: &ghops.PR{Number: 10, State: "CLOSED"}}, ActionBlocked, state.PhaseBlocked, "closed without merge", false, 0},
		// Row 25.
		{"awaiting issue closed", am, issueObservations{read: true, issueState: "CLOSED", pr: openPR("head-oid")}, ActionBlocked, state.PhaseBlocked, "before its PR", false, 0},
		// Row 26 (missing artifacts ignored at awaiting-merge).
		{"awaiting missing artifacts", am, issueObservations{read: true, issueState: "OPEN", pr: openPR("head-oid"), localBranch: pbool(false), worktree: pbool(false)}, ActionKept, state.PhaseAwaitingMerge, "approved head", false, 0},
		// Row 27.
		{"merged keep", fixtureIssue("a", 1, state.PhaseMerged), issueObservations{read: true, issueState: "CLOSED", pr: &ghops.PR{Number: 10, State: "MERGED"}}, ActionKept, state.PhaseMerged, "merged", false, 0},
		// Row 28.
		{"merged disagree", fixtureIssue("a", 1, state.PhaseMerged), issueObservations{read: true, issueState: "OPEN", pr: &ghops.PR{Number: 10, State: "OPEN"}}, ActionBlocked, state.PhaseBlocked, "disagree", false, 0},
		// Row 29.
		{"cleaned leave", fixtureIssue("a", 1, state.PhaseCleaned), issueObservations{}, ActionKept, state.PhaseCleaned, "terminal", false, 0},
		{"abandoned leave", fixtureIssue("a", 1, state.PhaseAbandoned), issueObservations{}, ActionKept, state.PhaseAbandoned, "terminal", false, 0},

		// Row 30 re-derivation.
		{"blocked → awaiting keep", blockedFrom(am), issueObservations{read: true, issueState: "OPEN", pr: openPR("head-oid")}, ActionUnblocked, state.PhaseAwaitingMerge, "approved head", false, 0},
		{"blocked → demote", blockedFrom(am), issueObservations{read: true, issueState: "OPEN", pr: openPR("moved")}, ActionUnblocked, state.PhaseInReview, "head moved", true, 0},
		{"blocked → pr-open", blockedFrom(po), issueObservations{read: true, issueState: "OPEN", manifestOK: pbool(true), localBranch: pbool(true), worktree: pbool(true), pr: openPR("h")}, ActionUnblocked, state.PhasePROpen, "PR #10 open", false, 0},
		{"blocked → in-review", blockedFrom(ir), issueObservations{read: true, issueState: "OPEN", manifestOK: pbool(true), localBranch: pbool(true), worktree: pbool(true), pr: openPR("h")}, ActionUnblocked, state.PhaseInReview, "PR #10 open", false, 0},
		{"blocked → worktree-ready", blockedFrom(wr), okManifest(0), ActionUnblocked, state.PhaseWorktreeReady, "re-run `orch run dispatch`", false, 0},
		{"blocked → adopt", blockedFrom(wr), issueObservations{read: true, issueState: "OPEN", manifestOK: pbool(true), verifications: 1, localBranch: pbool(true), worktree: pbool(true), prForBranch: &ghops.PR{Number: 34, URL: "u34"}}, ActionAdoptedPR, state.PhasePROpen, "adopted PR #34", false, 34},
		{"blocked stays blocked", blockedFrom(po), issueObservations{read: true, issueState: "OPEN", manifestOK: pbool(true), localBranch: pbool(true), worktree: pbool(true), pr: &ghops.PR{Number: 10, State: "MERGED", HeadRefOid: "h"}}, ActionBlocked, state.PhaseBlocked, "merged out of band", false, 0},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			o := reconcileIssue(tc.iss, tc.obs)
			if o.action != tc.wantAction {
				t.Errorf("action = %q, want %q", o.action, tc.wantAction)
			}
			if o.phase != tc.wantPhase {
				t.Errorf("phase = %q, want %q", o.phase, tc.wantPhase)
			}
			if tc.wantReason != "" && !strings.Contains(o.reason, tc.wantReason) {
				t.Errorf("reason = %q, want substring %q", o.reason, tc.wantReason)
			}
			if o.clearApproval != tc.wantClear {
				t.Errorf("clearApproval = %v, want %v", o.clearApproval, tc.wantClear)
			}
			if tc.wantAdopt != 0 && o.adoptPRNumber != tc.wantAdopt {
				t.Errorf("adoptPRNumber = %d, want %d", o.adoptPRNumber, tc.wantAdopt)
			}
		})
	}
}

// TestReconcileR1Fallback proves a blocked outcome an issue cannot satisfy
// (no routing decision) falls back to keep plus a run-level warning.
func TestReconcileR1Fallback(t *testing.T) {
	iss := fixtureIssue("a", 1, state.PhaseWorktreeReady)
	iss.Decision = nil // a blocked phase requires a decision; this issue lacks one.
	obs := issueObservations{read: true, issueState: "OPEN", manifestOK: pbool(false), manifestErr: manifest.ErrDrift, localBranch: pbool(true), worktree: pbool(true)}
	o := reconcileIssue(iss, obs)
	if o.action != ActionKept || o.phase != state.PhaseWorktreeReady {
		t.Fatalf("outcome = %+v, want kept worktree-ready", o)
	}
	if len(o.warnings) == 0 || !strings.Contains(o.warnings[0], "could not be blocked") {
		t.Errorf("warnings = %v, want a could-not-be-blocked notice", o.warnings)
	}
}

// --- Observation scope ---

// TestResumeZeroCallsForTerminalPhases proves a run made only of planned,
// cleaned, and abandoned issues opens neither GitHub nor git.
func TestResumeZeroCallsForTerminalPhases(t *testing.T) {
	issues := []state.Issue{
		{PlanID: "a", Phase: state.PhasePlanned},
		fixtureIssue("b", 2, state.PhaseCleaned),
		fixtureIssue("c", 3, state.PhaseAbandoned),
	}
	root := setupDeliveryRepo(t, "r1", issues)
	script := &execxtest.Script{T: t} // empty: any gh or git call fails the test
	env := Env{RepoRoot: root, Runner: script, Now: fixedNow}
	doc, err := Resume(context.Background(), env, ResumeRequest{})
	if err != nil {
		t.Fatalf("Resume: %v", err)
	}
	script.AssertExhausted()
	for _, iss := range doc.Issues {
		if iss.Action != ActionKept {
			t.Errorf("issue %s action = %q, want kept", iss.PlanID, iss.Action)
		}
	}
}

// TestResumeObservesPROpenScope pins the exact gh transcript resume reads
// for a pr-open issue.
func TestResumeObservesPROpenScope(t *testing.T) {
	root := setupDeliveryGitRepo(t, "r1", []state.Issue{fixtureIssue("a", 1, state.PhasePROpen)})
	createBranchWorktree(t, root, "orch/issue-1")
	body := baseManifestBody(t)
	doc, script := resumeMux(t, root, ResumeRequest{},
		ghAuth(),
		ghIssueViewCall(t, 1, "OPEN", body),
		ghPRViewCall(10, "OPEN", "head"),
		ghRollupEmptyCall(10),
	)
	script.AssertExhausted()
	if doc.Issues[0].Action != ActionKept {
		t.Errorf("action = %q, want kept", doc.Issues[0].Action)
	}
	if doc.Issues[0].Observed.CIState != string(ghops.CINoChecks) {
		t.Errorf("ci = %q, want no-checks", doc.Issues[0].Observed.CIState)
	}
}

// --- Purity ---

// TestResumeDryRunIsPure proves a dry run classifies a change but writes
// nothing.
func TestResumeDryRunIsPure(t *testing.T) {
	root := setupDeliveryGitRepo(t, "r1", []state.Issue{fixtureIssue("a", 1, state.PhaseAwaitingMerge)})
	before := stateBytes(t, root)
	body := baseManifestBody(t)
	doc, script := resumeMux(t, root, ResumeRequest{DryRun: true},
		ghAuth(),
		ghIssueViewCall(t, 1, "OPEN", body),
		ghPRViewCall(10, "OPEN", "moved-head"),
	)
	script.AssertExhausted()
	if doc.Applied {
		t.Error("dry run reported applied")
	}
	if doc.Issues[0].PhaseAfter != state.PhaseInReview {
		t.Errorf("classified phase = %q, want in-review (demotion)", doc.Issues[0].PhaseAfter)
	}
	if string(stateBytes(t, root)) != string(before) {
		t.Error("dry run changed state")
	}
}

// TestResumeMidTranscriptFailureIsPure proves a gh transport error mid-
// observation aborts before any write.
func TestResumeMidTranscriptFailureIsPure(t *testing.T) {
	root := setupDeliveryGitRepo(t, "r1", []state.Issue{fixtureIssue("a", 1, state.PhasePROpen)})
	createBranchWorktree(t, root, "orch/issue-1")
	before := stateBytes(t, root)
	body := baseManifestBody(t)
	script := &execxtest.Script{T: t, Calls: []execxtest.Call{
		ghAuth(),
		ghIssueViewCall(t, 1, "OPEN", body),
		{Name: "gh", Args: []string{"pr", "view", "10", "--json", prFieldsRun}, Exit: 1, Stderr: "boom"},
	}}
	env := Env{RepoRoot: root, Runner: muxRunner{git: execx.Local{}, gh: script}, Now: fixedNow}
	if _, err := Resume(context.Background(), env, ResumeRequest{}); err == nil {
		t.Fatal("Resume accepted a gh failure")
	}
	script.AssertExhausted()
	if string(stateBytes(t, root)) != string(before) {
		t.Error("state changed on an observation failure")
	}
}

// TestResumeConvergedSkipsSave proves a converged resume is a byte-level
// no-op (the skip-save path).
func TestResumeConvergedSkipsSave(t *testing.T) {
	root := setupDeliveryGitRepo(t, "r1", []state.Issue{fixtureIssue("a", 1, state.PhaseMerged)})
	before := stateBytes(t, root)
	body := baseManifestBody(t)
	doc, script := resumeMux(t, root, ResumeRequest{},
		ghAuth(),
		ghIssueViewCall(t, 1, "CLOSED", body),
		ghPRViewCall(10, "MERGED", "head"),
	)
	script.AssertExhausted()
	if !doc.Applied {
		t.Error("converged resume reported not applied")
	}
	if doc.Issues[0].Action != ActionKept {
		t.Errorf("action = %q, want kept", doc.Issues[0].Action)
	}
	if string(stateBytes(t, root)) != string(before) {
		t.Error("converged resume rewrote state")
	}
}

// --- Stopped runs ---

func stoppedAwaitingRepo(t *testing.T) string {
	t.Helper()
	root := setupDeliveryGitRepo(t, "r1", []state.Issue{fixtureIssue("a", 1, state.PhaseAwaitingMerge)})
	st := loadRun(t, root)
	st.Run.StoppedReason = "secret: token in diff"
	if err := state.Save(root, st); err != nil {
		t.Fatal(err)
	}
	return root
}

// TestResumeStoppedWithoutStatement proves a stopped run is fully reported
// but never written without the statement.
func TestResumeStoppedWithoutStatement(t *testing.T) {
	root := stoppedAwaitingRepo(t)
	before := stateBytes(t, root)
	body := baseManifestBody(t)
	doc, script := resumeMux(t, root, ResumeRequest{},
		ghAuth(),
		ghIssueViewCall(t, 1, "OPEN", body),
		ghPRViewCall(10, "OPEN", "head-oid"),
	)
	script.AssertExhausted()
	if doc.Applied {
		t.Error("stopped run without the statement reported applied")
	}
	if doc.StoppedReasonAfter == "" {
		t.Error("stopped reason cleared without the statement")
	}
	if string(stateBytes(t, root)) != string(before) {
		t.Error("stopped run without the statement wrote state")
	}
}

// TestResumeStoppedWithStatement proves the statement clears the stop and
// persists.
func TestResumeStoppedWithStatement(t *testing.T) {
	root := stoppedAwaitingRepo(t)
	body := baseManifestBody(t)
	doc, script := resumeMux(t, root, ResumeRequest{Statement: ResumeStoppedRunStatement},
		ghAuth(),
		ghIssueViewCall(t, 1, "OPEN", body),
		ghPRViewCall(10, "OPEN", "head-oid"),
	)
	script.AssertExhausted()
	if !doc.Applied || doc.StoppedReasonAfter != "" {
		t.Fatalf("doc = %+v, want applied with the stop cleared", doc)
	}
	if st := loadRun(t, root); st.Run.StoppedReason != "" {
		t.Error("stop not cleared on disk")
	}
}

// TestResumeBadStatement proves any statement but the exact one is
// ErrBadRequest, before any read.
func TestResumeBadStatement(t *testing.T) {
	root := setupDeliveryRepo(t, "r1", []state.Issue{fixtureIssue("a", 1, state.PhaseAwaitingMerge)})
	script := &execxtest.Script{T: t}
	env := Env{RepoRoot: root, Runner: script, Now: fixedNow}
	_, err := Resume(context.Background(), env, ResumeRequest{Statement: "nope"})
	if !errors.Is(err, ErrBadRequest) {
		t.Fatalf("err = %v, want ErrBadRequest", err)
	}
	script.AssertExhausted()
}

// TestResumeStatementOnNonStoppedRun proves the statement is a reported
// no-op on a run that was never stopped.
func TestResumeStatementOnNonStoppedRun(t *testing.T) {
	root := setupDeliveryGitRepo(t, "r1", []state.Issue{fixtureIssue("a", 1, state.PhaseAwaitingMerge)})
	body := baseManifestBody(t)
	doc, script := resumeMux(t, root, ResumeRequest{Statement: ResumeStoppedRunStatement},
		ghAuth(),
		ghIssueViewCall(t, 1, "OPEN", body),
		ghPRViewCall(10, "OPEN", "head-oid"),
	)
	script.AssertExhausted()
	if !doc.Applied || doc.StoppedReasonBefore != "" || doc.StoppedReasonAfter != "" {
		t.Errorf("doc = %+v, want applied no-op with no stop", doc)
	}
}

// --- Fail-closed preconditions ---

func TestResumeConfigDrift(t *testing.T) {
	root := setupDeliveryRepo(t, "r-different", []state.Issue{fixtureIssue("a", 1, state.PhaseDispatched)})
	script := &execxtest.Script{T: t}
	env := Env{RepoRoot: root, Runner: script, Now: fixedNow}
	_, err := Resume(context.Background(), env, ResumeRequest{})
	if !errors.Is(err, ErrConfigDrift) {
		t.Fatalf("err = %v, want ErrConfigDrift", err)
	}
	script.AssertExhausted() // fail closed before opening GitHub
}

func TestResumeOrphanedLockFailsClosed(t *testing.T) {
	root := setupDeliveryRepo(t, "r1", []state.Issue{fixtureIssue("a", 1, state.PhaseDispatched)})
	if err := lockfile.Release(root); err != nil {
		t.Fatal(err)
	}
	script := &execxtest.Script{T: t}
	env := Env{RepoRoot: root, Runner: script, Now: fixedNow}
	if _, err := Resume(context.Background(), env, ResumeRequest{}); err == nil {
		t.Fatal("Resume accepted a delivery state with no lock")
	}
	script.AssertExhausted()
}

func TestResumeAssistNoLock(t *testing.T) {
	root := setupRepo(t, testConfigTOML)
	script := &execxtest.Script{T: t}
	env := Env{RepoRoot: root, Runner: script, Now: fixedNow}
	doc, err := Resume(context.Background(), env, ResumeRequest{})
	if err != nil {
		t.Fatalf("Resume: %v", err)
	}
	script.AssertExhausted()
	if doc.Mode != state.ModeAssist || doc.Applied {
		t.Errorf("doc = %+v, want an assist no-op", doc)
	}
}

// --- Safe at every phase ---

// TestResumeSafeAtEveryPhase drives a reality-consistent resume for each
// phase and proves the saved state loads back cleanly.
func TestResumeSafeAtEveryPhase(t *testing.T) {
	body := baseManifestBody(t)
	phases := []state.Phase{
		state.PhasePlanned, state.PhaseIssueCreated, state.PhaseWorktreeReady,
		state.PhaseDispatched, state.PhasePROpen, state.PhaseInReview,
		state.PhaseAwaitingMerge, state.PhaseMerged, state.PhaseAbandoned,
		state.PhaseCleaned, state.PhaseBlocked,
	}
	for _, ph := range phases {
		t.Run(string(ph), func(t *testing.T) {
			iss := fixtureIssue("a", 1, ph)
			if ph == state.PhasePlanned {
				iss = state.Issue{PlanID: "a", Phase: state.PhasePlanned}
			}
			root := setupDeliveryGitRepo(t, "r1", []state.Issue{iss})

			var calls []execxtest.Call
			switch ph {
			case state.PhasePlanned, state.PhaseAbandoned, state.PhaseCleaned:
				// No reads.
			case state.PhaseIssueCreated:
				calls = []execxtest.Call{ghAuth(), ghIssueViewCall(t, 1, "OPEN", body)}
			case state.PhaseWorktreeReady:
				createBranchWorktree(t, root, "orch/issue-1")
				calls = []execxtest.Call{ghAuth(), ghIssueViewCall(t, 1, "OPEN", body), ghPRListEmptyCall("orch/issue-1")}
			case state.PhaseDispatched, state.PhaseBlocked:
				createBranchWorktree(t, root, "orch/issue-1")
				calls = []execxtest.Call{ghAuth(), ghIssueViewCall(t, 1, "OPEN", body), ghPRListEmptyCall("orch/issue-1")}
			case state.PhasePROpen, state.PhaseInReview:
				createBranchWorktree(t, root, "orch/issue-1")
				calls = []execxtest.Call{ghAuth(), ghIssueViewCall(t, 1, "OPEN", body), ghPRViewCall(10, "OPEN", "head"), ghRollupEmptyCall(10)}
			case state.PhaseAwaitingMerge:
				calls = []execxtest.Call{ghAuth(), ghIssueViewCall(t, 1, "OPEN", body), ghPRViewCall(10, "OPEN", "head-oid")}
			case state.PhaseMerged:
				calls = []execxtest.Call{ghAuth(), ghIssueViewCall(t, 1, "CLOSED", body), ghPRViewCall(10, "MERGED", "head-oid")}
			}

			doc, script := resumeMux(t, root, ResumeRequest{}, calls...)
			script.AssertExhausted()
			if !doc.Applied {
				t.Error("resume did not apply")
			}
			// The saved state must load (and therefore validate) cleanly.
			if _, err := state.Load(root); err != nil {
				t.Fatalf("state does not load after resume at %s: %v", ph, err)
			}
			if ph == state.PhaseBlocked && doc.Issues[0].PhaseAfter != state.PhaseWorktreeReady {
				t.Errorf("blocked re-derived to %s, want worktree-ready", doc.Issues[0].PhaseAfter)
			}
		})
	}
}

// --- Integration: adopt an orphan PR left by a crashed pr-open ---

// TestResumeAdoptsOrphanPR simulates a pr-open that created the PR but
// crashed before Save, then proves resume adopts the orphan and advances
// the issue to pr-open.
func TestResumeAdoptsOrphanPR(t *testing.T) {
	root := newLifecycleRepo(t)
	body := baseManifestBody(t)
	const branch = "orch/issue-1-fix-the-status-lock-race"
	const title = "Fix the status lock race"

	activateCalls := append(fullTaxonomyScript(), ghIssueCreateCall(title, []string{"ready", "bug", "implementer", "standard"}, 1))
	script := &execxtest.Script{T: t, Calls: activateCalls}
	env := Env{RepoRoot: root, Runner: muxRunner{git: execx.Local{}, gh: script}, Now: fixedNow}
	if _, err := Activate(context.Background(), env, activationJSON(t, validPlanJSON())); err != nil {
		t.Fatalf("Activate: %v", err)
	}
	script.AssertExhausted()

	runVerb(t, root, Dispatch, `{"schema_version":1,"issue_number":1}`,
		ghAuth(), ghRepoViewCall("main"), ghSetStatusCall(1, ghops.StatusInProgress))

	wtDir := filepath.Join(root, ".orchestrator", "worktrees", "issue-1")
	writeWork(t, wtDir)

	// A real pr-open opens PR #10 and pushes the branch.
	runVerb(t, root, PROpen, `{"schema_version":1,"issue_number":1,"verifications":[{"name":"go test","result":"pass"}]}`,
		ghAuth(), ghRepoViewCall("main"), ghPRListEmptyCall(branch),
		ghIssueViewCall(t, 1, "OPEN", body), ghSetIssueBodyCall(1),
		ghCreatePRCall(branch, title, 10), ghSetStatusCall(1, ghops.StatusAwaitingReview))
	wantPhase(t, root, 1, state.PhasePROpen)

	// Simulate the crash window: the PR exists on GitHub, but state was
	// never advanced past dispatched.
	st := loadRun(t, root)
	st.Run.Issues[0].Phase = state.PhaseDispatched
	st.Run.Issues[0].PRNumber = 0
	st.Run.Issues[0].PRURL = ""
	if err := state.Save(root, st); err != nil {
		t.Fatal(err)
	}

	// Resume observes the orphan PR (with recorded verification evidence)
	// and adopts it.
	orphanBody := manifestBodyWithVerification(t)
	doc, resumeScript := resumeMux(t, root, ResumeRequest{},
		ghAuth(),
		ghIssueViewCall(t, 1, "OPEN", orphanBody),
		prListForBranchCall(branch, 10, "head"),
	)
	resumeScript.AssertExhausted()
	if doc.Issues[0].Action != ActionAdoptedPR {
		t.Fatalf("action = %q, want adopted-pr", doc.Issues[0].Action)
	}
	wantPhase(t, root, 1, state.PhasePROpen)
	if st := loadRun(t, root); st.Run.Issues[0].PRNumber != 10 {
		t.Errorf("adopted PR number = %d, want 10", st.Run.Issues[0].PRNumber)
	}
}

// --- Shared helpers ---

// setupDeliveryGitRepo writes a delivery state onto a real git sandbox
// with an origin remote, so resume's git probes run against real git.
func setupDeliveryGitRepo(t *testing.T, planRev string, issues []state.Issue) string {
	t.Helper()
	root := newLifecycleRepo(t)
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

// createBranchWorktree materializes the fixture issue's branch and a
// worktree checked out on it, so resume's presence probes read them.
func createBranchWorktree(t *testing.T, root, branch string) {
	t.Helper()
	rawGit(t, root, "branch", branch)
	wt := filepath.Join(root, ".orchestrator", "worktrees", "issue-1")
	rawGit(t, root, "worktree", "add", wt, branch)
}

func writeWork(t *testing.T, wtDir string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(wtDir, "feature.go"), []byte("package feature\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	rawGit(t, wtDir, "add", "-A")
	rawGit(t, wtDir, "commit", "-m", "work")
}

// resumeMux runs Resume against real git and a scripted gh, returning the
// report and the script for exhaustion checks.
func resumeMux(t *testing.T, root string, req ResumeRequest, calls ...execxtest.Call) (*ResumeDoc, *execxtest.Script) {
	t.Helper()
	script := &execxtest.Script{T: t, Calls: calls}
	env := Env{RepoRoot: root, Runner: muxRunner{git: execx.Local{}, gh: script}, Now: fixedNow}
	doc, err := Resume(context.Background(), env, req)
	if err != nil {
		t.Fatalf("Resume: %v", err)
	}
	return doc, script
}

// prListForBranchCall scripts a PRForBranch probe that finds one open PR.
func prListForBranchCall(branch string, number int, headOID string) execxtest.Call {
	return execxtest.Call{
		Name: "gh", Args: []string{"pr", "list", "--head", branch, "--state", "open", "--json", prFieldsRun},
		Stdout: "[" + prJSONStdout(number, "OPEN", headOID) + "]",
	}
}

func prJSONStdout(number int, prState, headOID string) string {
	return `{"number":` + strconv.Itoa(number) + `,"state":"` + prState + `","title":"t","url":"https://github.com/o/r/pull/` + strconv.Itoa(number) + `","headRefName":"b","baseRefName":"main","headRefOid":"` + headOID + `","mergeStateStatus":"CLEAN","mergedAt":null,"body":"pr body"}`
}

// manifestBodyWithVerification renders an audit record carrying one
// verification, the evidence resume requires to adopt an orphan PR.
func manifestBodyWithVerification(t *testing.T) string {
	t.Helper()
	body, err := manifest.Upsert("**Objective**\n\ndo it\n", manifest.Manifest{
		SchemaVersion:    manifest.SchemaVersion,
		Role:             manifest.RoleImplementer,
		Executor:         manifest.Selection{Model: "claude-sonnet-5", Effort: "xhigh"},
		RoutingRationale: "impl",
		Reviewer:         manifest.Selection{Model: "claude-opus-4-8", Effort: "high"},
		ConfigRevision:   "r1",
		Verifications:    []manifest.Verification{{Name: "go test", Result: "pass", At: "2026-07-11T12:00:00Z"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	return body
}
