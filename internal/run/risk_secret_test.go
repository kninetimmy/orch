package run

import (
	"bytes"
	"errors"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/kninetimmy/orch/internal/execx"
	"github.com/kninetimmy/orch/internal/state"
)

func TestSecretDuringRiskHoldRequiresBothDecisions(t *testing.T) {
	root := newLifecycleRepoWithConfig(t, riskConfigTOML)
	env, g, req, accept := enterRiskFixture(t, root)
	branch := loadRun(t, root).Run.Issues[0].Branch
	createBranchWorktree(t, root, branch)
	env.Runner = muxRunner{git: execx.Local{}, gh: g}
	g.head = strings.TrimSpace(rawGit(t, root, "rev-parse", "HEAD"))
	req.ReviewedHeadOID = g.head
	g.green()

	held := holdRisk(t, env, g, req)
	original := loadRun(t, root).Run.Issues[0].Blocks[0]
	const secretDetail = "Credential exposure reported; rotate the affected credential."
	secret, err := Block(t.Context(), env, requestBytes(t, BlockRequest{BlockSchemaVersion, 1, "secret", secretDetail}))
	if err != nil || !secret.RunStopped {
		t.Fatalf("secret block = %+v, %v", secret, err)
	}
	resolve := ResolveBlockRequest{ResolveBlockSchemaVersion, accept.RunID, 1, held.RiskBlockID + 1, "human", "Credential rotated; approved work is unchanged.", ResolveBlockStatement}
	if _, err := ResolveBlock(t.Context(), env, requestBytes(t, resolve)); !errors.Is(err, ErrRunStopped) {
		t.Fatalf("resolve before releasing the global stop: %v", err)
	}

	wt := filepath.Join(root, filepath.FromSlash(worktreeRel(1)))
	writeWork(t, wt)
	rawGit(t, wt, "push", "origin", branch)
	g.head = strings.TrimSpace(rawGit(t, wt, "rev-parse", "HEAD"))
	checkout := []byte(reviewWorktreeJSON(1, g.head))
	approve := req
	approve.ReviewedHeadOID, approve.Verdict, approve.RiskHold = g.head, VerdictApprove, nil
	approve.RiskFollowUp = &RiskFollowUp{held.RiskBlockID, riskAddressed, "The changed refresh path no longer has the original uncertainty."}
	resumed, err := Resume(t.Context(), env, ResumeRequest{Statement: ResumeStoppedRunStatement})
	if err != nil || !resumed.Applied || resumed.StoppedReasonBefore != "secret: "+secretDetail ||
		resumed.StoppedReasonAfter != "" || resumed.Issues[0].PhaseAfter != state.PhaseBlocked {
		t.Fatalf("release of global stop = %+v, %v", resumed, err)
	}

	is := loadRun(t, root).Run.Issues[0]
	if len(is.Blocks) != 2 || is.Blocks[0].ID != original.ID || is.Blocks[0].Reason != original.Reason ||
		is.Blocks[0].RiskHold == nil || !is.Blocks[0].RiskHold.Invalidated ||
		is.Blocks[0].RiskHold.AuditDigest != original.RiskHold.AuditDigest ||
		is.Blocks[1].ID != resolve.BlockID || is.Blocks[1].Cause != state.BlockSecret ||
		is.Blocks[1].Reason != "secret: "+secretDetail || is.Blocks[1].Resolution != nil ||
		is.BlockedReason != is.Blocks[1].Reason {
		t.Errorf("global release did not preserve both issue decisions: %+v", is.Blocks)
	}
	if _, err := Block(t.Context(), env, requestBytes(t, BlockRequest{BlockSchemaVersion, 1, "github", "Later transport failure."})); err != nil {
		t.Fatal(err)
	}
	if got := loadRun(t, root).Run.Issues[0]; !reflect.DeepEqual(got, is) {
		t.Errorf("later generic block replaced a pending decision: %+v", got)
	}
	before := stateBytes(t, root)
	issueBody, prBody := g.issueBody, g.prBody
	if got, err := ReviewWorktree(t.Context(), env, checkout); !errors.Is(err, ErrWrongPhase) {
		t.Errorf("review checkout while secret pending = %+v, %v", got, err)
	}
	if got, err := Review(t.Context(), env, requestBytes(t, approve)); !errors.Is(err, ErrWrongPhase) {
		t.Errorf("changed-head approval while secret pending = %+v, %v", got, err)
	}
	if !bytes.Equal(before, stateBytes(t, root)) || issueBody != g.issueBody || prBody != g.prBody {
		t.Error("refused review must leave state and audit mirrors untouched")
	}
	if t.Failed() {
		return
	}
	if doc, err := Next(t.Context(), env); err != nil || doc.Actions[0].Action != "resolve-block" ||
		!strings.Contains(doc.Actions[0].Prerequisites[0], "secret block 2") {
		t.Fatalf("next while secret pending = %+v, %v", doc, err)
	}
	if doc, err := Readiness(t.Context(), env, 1); err != nil || doc.MergeReport.PrerequisitesMet ||
		!strings.Contains(strings.Join(doc.Requirements, "\n"), "After resolution, hold 1 still needs its changed-head review path") {
		t.Fatalf("readiness while secret pending = %+v, %v", doc, err)
	}

	if _, err := ResolveBlock(t.Context(), env, requestBytes(t, resolve)); err != nil {
		t.Fatal(err)
	}
	is = loadRun(t, root).Run.Issues[0]
	if is.Phase != state.PhaseBlocked || is.BlockedReason != original.Reason || is.Blocks[0].Resolution != nil ||
		is.Blocks[1].Resolution == nil || is.Blocks[1].Resolution.Detail != resolve.Detail ||
		is.Blocks[1].Reason != "secret: "+secretDetail {
		t.Fatalf("secret resolution did not return to the original hold: %+v", is)
	}
	if resumed, err := Resume(t.Context(), env, ResumeRequest{}); err != nil || resumed.Issues[0].PhaseAfter != state.PhaseBlocked {
		t.Fatalf("resume after secret resolution = %+v, %v", resumed, err)
	}
	if doc, err := Next(t.Context(), env); err != nil || doc.Actions[0].Action != "review-changed-head-or-replan" {
		t.Fatalf("next after secret resolution = %+v, %v", doc, err)
	}
	wrongResolution := resolve
	wrongResolution.BlockID = held.RiskBlockID
	if _, err := ResolveBlock(t.Context(), env, requestBytes(t, wrongResolution)); !errors.Is(err, ErrBadApproval) {
		t.Fatalf("generic resolution of the original hold: %v", err)
	}
	if _, err := AcceptRisk(t.Context(), env, requestBytes(t, accept)); !errors.Is(err, ErrBadApproval) {
		t.Fatalf("acceptance of the invalidated hold: %v", err)
	}
	if got, err := ReviewWorktree(t.Context(), env, checkout); err != nil || got.HeadOID != g.head {
		t.Fatalf("review checkout after secret resolution = %+v, %v", got, err)
	}
	detachedHeadAt(t, reviewWorktreeAbs(root, 1), g.head)
	missingFollowUp := approve
	missingFollowUp.RiskFollowUp = nil
	if _, err := Review(t.Context(), env, requestBytes(t, missingFollowUp)); !errors.Is(err, ErrBadRequest) {
		t.Fatalf("approval did not require the original hold to be addressed: %v", err)
	}
	if got, err := Review(t.Context(), env, requestBytes(t, approve)); err != nil ||
		got.Phase != state.PhaseInReview || got.ReviewCycles != held.ReviewCycles+1 {
		t.Fatalf("scoped approval on the different head = %+v, %v", got, err)
	}
	if got := loadRun(t, root).Run.Issues[0].Blocks; !reflect.DeepEqual(got, is.Blocks) {
		t.Fatalf("scoped approval changed the decision records: %+v", got)
	}
}

func TestSecretDuringRiskHoldPreservesInterruptedReview(t *testing.T) {
	env, g, req, accept := newRiskFixture(t, true)
	old := holdRisk(t, env, g, req)
	original := findVerification(t, g.issueBody, riskHoldName(old.RiskBlockID))
	g.head, req.ReviewedHeadOID = riskNewHead, riskNewHead
	g.failMirror = true
	if _, err := Review(t.Context(), env, requestBytes(t, req)); err == nil {
		t.Fatal("interrupted changed-head hold was finalized")
	}
	provisional := riskHoldName(old.RiskBlockID + 1)
	findVerification(t, g.issueBody, provisional)

	if _, err := Block(t.Context(), env, requestBytes(t, BlockRequest{BlockSchemaVersion, 1, "secret", "Credential exposure reported."})); err != nil {
		t.Fatal(err)
	}
	if _, err := Resume(t.Context(), env, ResumeRequest{Statement: ResumeStoppedRunStatement}); err != nil {
		t.Fatal(err)
	}
	if _, err := Review(t.Context(), env, requestBytes(t, req)); !errors.Is(err, ErrWrongPhase) {
		t.Fatalf("even the exact review retry must wait for the secret decision: %v", err)
	}
	resolve := ResolveBlockRequest{ResolveBlockSchemaVersion, accept.RunID, 1, old.RiskBlockID + 1, "human", "Credential rotated.", ResolveBlockStatement}
	if _, err := ResolveBlock(t.Context(), env, requestBytes(t, resolve)); err != nil {
		t.Fatal(err)
	}
	different := req
	different.Summary = "Different review, not the audit retry."
	if _, err := Review(t.Context(), env, requestBytes(t, different)); !errors.Is(err, ErrReviewStale) {
		t.Fatalf("secret resolution did not preserve the exact audit retry: %v", err)
	}
	retried, err := Review(t.Context(), env, requestBytes(t, req))
	if err != nil || retried.Phase != state.PhaseBlocked || retried.RiskBlockID != resolve.BlockID+1 ||
		retried.ReviewCycles != old.ReviewCycles+1 || retried.RiskChallenge == old.RiskChallenge {
		t.Fatalf("exact retry after secret resolution = %+v, %v", retried, err)
	}
	name := riskHoldName(retried.RiskBlockID)
	if findVerification(t, g.issueBody, name) != findVerification(t, g.prBody, name) ||
		strings.Contains(g.issueBody, provisional) || strings.Contains(g.prBody, provisional) {
		t.Fatal("audit retry left a hold receipt under the secret block ID")
	}
	if findVerification(t, g.issueBody, original.Name) != original || findVerification(t, g.prBody, original.Name) != original {
		t.Fatal("audit retry changed the original hold receipt")
	}
}

func TestSecretDuringStagedFirstRiskHold(t *testing.T) {
	env, g, req, accept := newRiskFixture(t, true)
	g.failMirror = true
	exact := requestBytes(t, req)
	if _, err := Review(t.Context(), env, exact); err == nil || !strings.Contains(err.Error(), "PR mirror unavailable") {
		t.Fatalf("first hold did not stop at the partial audit write: %v", err)
	}
	wantPhase(t, env.RepoRoot, 1, state.PhasePROpen)
	provisional := findVerification(t, g.issueBody, "risk-hold-1")
	reviewProof := findVerification(t, g.issueBody, "risk-review-1")

	const secretDetail = "Credential exposure reported."
	if got, err := Block(t.Context(), env, requestBytes(t, BlockRequest{BlockSchemaVersion, 1, "secret", secretDetail})); err != nil || !got.RunStopped {
		t.Fatalf("secret block = %+v, %v", got, err)
	}
	resolve := ResolveBlockRequest{ResolveBlockSchemaVersion, accept.RunID, 1, 1, "human", "Credential rotated.", ResolveBlockStatement}
	if _, err := ResolveBlock(t.Context(), env, requestBytes(t, resolve)); !errors.Is(err, ErrRunStopped) {
		t.Fatalf("resolve before releasing the global stop: %v", err)
	}
	if _, err := Resume(t.Context(), env, ResumeRequest{Statement: ResumeStoppedRunStatement}); err != nil {
		t.Fatal(err)
	}
	before := stateBytes(t, env.RepoRoot)
	issueBody, prBody := g.issueBody, g.prBody
	if _, err := Review(t.Context(), env, exact); !errors.Is(err, ErrWrongPhase) {
		t.Fatalf("exact audit retry with an unresolved secret: %v", err)
	}
	approve := req
	g.head, approve.ReviewedHeadOID, approve.Verdict, approve.RiskHold = riskNewHead, riskNewHead, VerdictApprove, nil
	if _, err := Review(t.Context(), env, requestBytes(t, approve)); !errors.Is(err, ErrWrongPhase) {
		t.Fatalf("changed-head approval with an unresolved secret: %v", err)
	}
	if !bytes.Equal(before, stateBytes(t, env.RepoRoot)) || issueBody != g.issueBody || prBody != g.prBody {
		t.Fatal("refused reviews changed state or audit mirrors")
	}
	if doc, err := Next(t.Context(), env); err != nil || doc.Actions[0].Action != "resolve-block" ||
		!strings.Contains(doc.Actions[0].Prerequisites[0], "retry the exact staged review") {
		t.Fatalf("next with a staged hold and unresolved secret = %+v, %v", doc, err)
	}
	if doc, err := Readiness(t.Context(), env, 1); err != nil || doc.MergeReport.PrerequisitesMet ||
		!strings.Contains(strings.Join(doc.Requirements, "\n"), "Resolve issue-level secret block 1") {
		t.Fatalf("readiness with a staged hold and unresolved secret = %+v, %v", doc, err)
	}

	if _, err := ResolveBlock(t.Context(), env, requestBytes(t, resolve)); err != nil {
		t.Fatalf("explicit resolution of Secret interrupting the first staged hold: %v", err)
	}
	before = stateBytes(t, env.RepoRoot)
	if _, err := Resume(t.Context(), env, ResumeRequest{}); err != nil || !bytes.Equal(before, stateBytes(t, env.RepoRoot)) {
		t.Fatalf("generic resume must leave the staged review and Secret history alone: %v", err)
	}
	if doc, err := Next(t.Context(), env); err != nil || doc.Actions[0].Action != "retry-risk-review" {
		t.Fatalf("next after secret resolution = %+v, %v", doc, err)
	}
	if _, err := Review(t.Context(), env, exact); !errors.Is(err, ErrReviewStale) {
		t.Fatalf("retry at a stale head: %v", err)
	}
	if _, err := Review(t.Context(), env, requestBytes(t, approve)); !errors.Is(err, ErrReviewStale) {
		t.Fatalf("different live review instead of the exact audit retry: %v", err)
	}
	g.head, g.strict = riskHead, false
	if _, err := Review(t.Context(), env, exact); !errors.Is(err, ErrReviewStale) {
		t.Fatalf("exact request after required policy drift: %v", err)
	}
	g.strict = true
	if !bytes.Equal(before, stateBytes(t, env.RepoRoot)) || issueBody != g.issueBody || prBody != g.prBody {
		t.Fatal("stale and mismatched retries changed state or audit mirrors")
	}

	g.failMirror = true
	if _, err := Review(t.Context(), env, exact); err == nil || !strings.Contains(err.Error(), "PR mirror unavailable") {
		t.Fatalf("retry did not stop at the partial relocation write: %v", err)
	}
	if !bytes.Equal(before, stateBytes(t, env.RepoRoot)) {
		t.Fatal("partial relocation advanced the review")
	}
	got, err := Review(t.Context(), env, exact)
	if err != nil || got.Phase != state.PhaseBlocked || got.RiskBlockID != 2 || got.ReviewCycles != 1 {
		t.Fatalf("exact retry after explicit secret resolution = %+v, %v", got, err)
	}
	is := loadRun(t, env.RepoRoot).Run.Issues[0]
	if len(is.Blocks) != 2 || is.Blocks[0].Cause != state.BlockSecret || is.Blocks[0].Reason != "secret: "+secretDetail ||
		is.Blocks[0].Resolution == nil || is.Blocks[0].Resolution.Detail != resolve.Detail {
		t.Fatalf("retry lost the resolved Secret: %+v", is.Blocks)
	}
	want := provisional
	want.Name, want.Detail = "risk-hold-2", strings.Replace(want.Detail, `"block_id":1`, `"block_id":2`, 1)
	for _, body := range []string{g.issueBody, g.prBody} {
		if findVerification(t, body, want.Name) != want || findVerification(t, body, reviewProof.Name) != reviewProof ||
			strings.Contains(body, provisional.Name) {
			t.Fatal("audit retry did not preserve the review proof and relocate only the provisional hold")
		}
	}
	g.submit(got.RiskChallenge)
	accept.BlockID = got.RiskBlockID
	if _, err := AcceptRisk(t.Context(), env, requestBytes(t, accept)); !errors.Is(err, ErrBadApproval) {
		t.Fatalf("a hold interrupted by a Secret must not become eligible for acceptance: %v", err)
	}
}
