package run

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/kninetimmy/orch/internal/gitops"
	"github.com/kninetimmy/orch/internal/manifest"
	"github.com/kninetimmy/orch/internal/metrics"
	"github.com/kninetimmy/orch/internal/paths"
	"github.com/kninetimmy/orch/internal/state"
)

const RepairSchemaVersion = 1
const RepairStatement = "authorize-integration-repair-without-scope-change"

// RepairRequest is an explicit human/Architect assertion, like resolve-block.
// RepairNumber must be the next issue-local repair number, so a request cannot be
// replayed after a later review/merge report even if phase and head recur.
type RepairRequest struct {
	SchemaVersion   int         `json:"schema_version"`
	RunID           string      `json:"run_id"`
	IssueNumber     int         `json:"issue_number"`
	RepairNumber    int         `json:"repair_number"`
	ExpectedPhase   state.Phase `json:"expected_phase"`
	ExpectedHeadOID string      `json:"expected_head_oid"`
	AuthorizedBy    string      `json:"authorized_by"`
	Reason          string      `json:"reason"`
	Statement       string      `json:"statement"`
}

type RepairResult struct {
	SchemaVersion int                     `json:"schema_version"`
	RunID         string                  `json:"run_id"`
	IssueNumber   int                     `json:"issue_number"`
	Phase         state.Phase             `json:"phase"`
	Repair        state.IntegrationRepair `json:"repair"`
	Execution     *DispatchResult         `json:"execution"`
}

// Repair reopens only recovered pre-PR work and pinned open PRs. All reads
// precede one atomic state write: the receipt, invalidated approvals and writable
// phase become visible together. It performs no git/GitHub mutation, dispatch,
// review, merge, grant operation or scope amendment.
func Repair(ctx context.Context, env Env, reqJSON []byte) (*RepairResult, error) {
	var req RepairRequest
	if err := decodeRequest(reqJSON, &req); err != nil {
		return nil, err
	}
	if req.SchemaVersion != RepairSchemaVersion {
		return nil, fmt.Errorf("%w: schema_version %d is unsupported (this build supports %d)", ErrBadRequest, req.SchemaVersion, RepairSchemaVersion)
	}
	if req.RunID == "" || req.IssueNumber <= 0 || req.RepairNumber <= 0 ||
		!phaseAllowed(req.ExpectedPhase, []state.Phase{state.PhaseWorktreeReady, state.PhaseAwaitingMerge}) ||
		!headOIDPattern.MatchString(req.ExpectedHeadOID) || strings.TrimSpace(req.AuthorizedBy) == "" ||
		strings.TrimSpace(req.Reason) == "" || req.Statement != RepairStatement {
		return nil, fmt.Errorf("%w: repair requires current run/issue, next repair_number, expected_phase, full expected_head_oid, authorized_by, reason and statement %q; changed scope requires fresh plan approval", ErrBadApproval, RepairStatement)
	}
	c, err := loadVerb(env, req.IssueNumber, []state.Phase{state.PhaseWorktreeReady, state.PhaseAwaitingMerge}, false)
	if err != nil {
		return nil, err
	}
	issue := c.issue()
	if req.RunID != c.st.Run.ID || req.ExpectedPhase != issue.Phase || req.RepairNumber != len(issue.Repairs)+1 {
		return nil, fmt.Errorf("%w: stale repair request; re-inspect the current run, issue, phase, head and repair history", ErrBadApproval)
	}
	for i := range c.st.Run.Issues {
		other := &c.st.Run.Issues[i]
		if i != c.idx && (strings.EqualFold(other.PlanID, issue.PlanID) || other.Branch == issue.Branch || other.Worktree == issue.Worktree) {
			return nil, fmt.Errorf("%w: ambiguous registered issue/branch/worktree identity; preserve the run and investigate", ErrBadApproval)
		}
	}
	if err := checkApprovedWork(issue); err != nil {
		return nil, err
	}
	if issue.Decision == nil {
		return nil, fmt.Errorf("issue #%d has no routing decision; preserve the run and re-plan", issue.Number)
	}
	// Initial dispatch is not an integration-repair shortcut. A pre-PR issue
	// must first have recovered from a known operational/resolved block.
	if issue.Phase == state.PhaseWorktreeReady && (issue.PRNumber != 0 || len(issue.Blocks) == 0) {
		return nil, fmt.Errorf("%w: pre-PR repair requires recovered block history and no recorded PR; use `orch run dispatch` for initial work", ErrBadRequest)
	}
	for _, b := range issue.Blocks {
		if b.Cause != state.BlockOperational && b.Resolution == nil {
			return nil, fmt.Errorf("%w: unresolved block %d; resolve the original decision and run `orch resume` first", ErrBadApproval, b.ID)
		}
	}
	if err := checkDependencies(c.st.Run, issue); err != nil {
		return nil, err
	}
	gh, err := openGitHub(ctx, env)
	if err != nil {
		return nil, err
	}
	repo, err := gh.Repo(ctx)
	if err != nil {
		return nil, err
	}
	if repo.URL == "" || repo.DefaultBranch == "" || issue.Branch == repo.DefaultBranch ||
		issue.Branch == "main" || issue.Branch == "master" {
		return nil, fmt.Errorf("cannot verify repair repository/default/feature branch; preserve artifacts and investigate")
	}
	gi, m, err := readIssueManifest(ctx, gh, issue.Number)
	if err != nil {
		return nil, err
	}
	if gi.State != "OPEN" || gi.URL != issue.URL || gi.URL != repo.URL+"/issues/"+strconv.Itoa(issue.Number) {
		return nil, fmt.Errorf("issue #%d is not the registered OPEN issue in %s; preserve artifacts and investigate", issue.Number, repo.URL)
	}
	if err := checkRepairManifest(issue, m, c.st.Run.Plan.ConfigRevision); err != nil {
		return nil, err
	}
	if issue.PRNumber == 0 {
		pr, err := gh.PRForBranch(ctx, issue.Branch)
		if err != nil {
			return nil, err
		}
		if pr != nil {
			return nil, fmt.Errorf("%w: open PR #%d already exists; use `orch resume` to reconcile it before requesting repair", ErrPRExists, pr.Number)
		}
	} else {
		pr, err := gh.PR(ctx, issue.PRNumber)
		if err != nil {
			return nil, err
		}
		if pr.State != "OPEN" || pr.URL != issue.PRURL || pr.URL != repo.URL+"/pull/"+strconv.Itoa(issue.PRNumber) ||
			pr.HeadRefName != issue.Branch || pr.BaseRefName != repo.DefaultBranch || pr.HeadRefOid != req.ExpectedHeadOID ||
			issue.ApprovedHeadOID != req.ExpectedHeadOID {
			return nil, fmt.Errorf("%w: PR identity, OPEN state, base, branch or pinned/live head does not match repair; use `orch resume --dry-run` and investigate", ErrBadApproval)
		}
		pm, err := manifest.Parse(pr.Body)
		if err != nil {
			return nil, fmt.Errorf("read PR #%d audit record: %w", pr.Number, err)
		}
		if err := checkRepairManifest(issue, pm, c.st.Run.Plan.ConfigRevision); err != nil {
			return nil, err
		}
	}
	git, err := gitops.Open(ctx, env.Runner, env.RepoRoot)
	if err != nil {
		return nil, err
	}
	if err := checkRepairWorktree(ctx, c, git, req.ExpectedHeadOID); err != nil {
		return nil, err
	}
	if issue.PRNumber > 0 {
		exists, err := git.RemoteBranchExists(ctx, "origin", issue.Branch)
		if err != nil {
			return nil, err
		}
		if !exists {
			return nil, fmt.Errorf("registered remote branch %s is missing; preserve artifacts and investigate", issue.Branch)
		}
	}
	if issue.PRNumber == 0 {
		ahead, err := git.CommitsAhead(ctx, c.worktreeAbs(), "origin/"+repo.DefaultBranch, "refs/heads/"+issue.Branch)
		if err != nil {
			return nil, err
		}
		if ahead == 0 {
			return nil, fmt.Errorf("%w: no preserved implementation commits; use ordinary `orch run dispatch`", ErrBadRequest)
		}
	}
	repair := state.IntegrationRepair{
		Number: req.RepairNumber, PhaseBefore: issue.Phase, HeadOID: req.ExpectedHeadOID,
		AuthorizedBy: req.AuthorizedBy, Reason: req.Reason, At: env.nowStamp(), ReviewCycles: issue.ReviewCycles,
	}
	issue.Repairs = append(issue.Repairs, repair)
	issue.ApprovedHeadOID, issue.LastReviewVerdict = "", ""
	issue.Phase = state.PhaseDispatched
	if issue.PRNumber > 0 {
		issue.Phase = state.PhaseInReview
	}
	if err := c.save(); err != nil {
		return nil, err
	}
	if err := c.recordMetric(metrics.Event{Verb: "repair", IssueNumber: issue.Number, Reason: req.Reason,
		Role: string(issue.Decision.Role), Executor: &issue.Decision.Executor, Reviewer: &issue.Decision.Reviewer}); err != nil {
		return nil, err
	}
	return &RepairResult{SchemaVersion: RepairSchemaVersion, RunID: c.st.Run.ID, IssueNumber: issue.Number,
		Phase: issue.Phase, Repair: repair, Execution: dispatchResult(issue)}, nil
}

func checkRepairManifest(issue *state.Issue, m manifest.Manifest, revision string) error {
	if !sameWork(issue, approvedWork{m.Objective, m.AcceptanceCriteria, m.RequiredTests, m.TestsCIDoesNotRun}) ||
		m.Role != issue.Decision.Role || m.Executor != issue.Decision.Executor || m.Reviewer != issue.Decision.Reviewer ||
		m.RoutingRationale != issue.Decision.Rationale || m.ConfigRevision != revision {
		return fmt.Errorf("%w: audit record differs from approved work/routing; restore the original record or return through fresh plan approval", ErrBadApproval)
	}
	return nil
}

func checkRepairWorktree(ctx context.Context, c *verbCtx, git *gitops.Git, expectedHead string) error {
	issue := c.issue()
	if issue.Worktree != worktreeRel(issue.Number) {
		return fmt.Errorf("repair requires the existing registered issue worktree %s; preserve artifacts and investigate", worktreeRel(issue.Number))
	}
	abs := c.worktreeAbs()
	inRepo, err := paths.Inside(c.env.RepoRoot, abs)
	if err != nil {
		return err
	}
	inside, err := paths.Inside(filepath.Join(c.env.RepoRoot, ".orchestrator", "worktrees"), abs)
	if err != nil {
		return err
	}
	if !inRepo || !inside {
		return fmt.Errorf("repair worktree escapes the repository worktree container")
	}
	info, err := os.Stat(abs)
	if err != nil {
		return fmt.Errorf("inspect repair worktree: %w", err)
	}
	worktrees, err := git.ListWorktrees(ctx)
	if err != nil {
		return err
	}
	found := false
	for _, wt := range worktrees {
		if wt.Primary || wt.Detached || wt.Branch != issue.Branch || wt.Head != expectedHead {
			continue
		}
		wi, err := os.Stat(wt.Path)
		if err != nil {
			return err
		}
		if os.SameFile(info, wi) {
			found = true
		}
	}
	if !found {
		return fmt.Errorf("%w: worktree/path/branch/head mismatch; preserve artifacts and investigate", ErrBadApproval)
	}
	local, err := gitops.Open(ctx, c.env.Runner, abs)
	if err != nil {
		return err
	}
	common, err := git.CommonDir(ctx)
	if err != nil {
		return err
	}
	localCommon, err := local.CommonDir(ctx)
	if err != nil {
		return err
	}
	a, err := os.Stat(common)
	if err != nil {
		return err
	}
	b, err := os.Stat(localCommon)
	if err != nil {
		return err
	}
	branch, err := local.CurrentBranch(ctx)
	if err != nil {
		return err
	}
	head, err := local.RevParse(ctx, "HEAD")
	if err != nil {
		return err
	}
	if !os.SameFile(a, b) || branch != issue.Branch || head != expectedHead {
		return fmt.Errorf("%w: actual worktree repository/branch/head mismatch", ErrBadApproval)
	}
	return git.RequireClean(ctx, abs)
}

func latestRepair(issue *state.Issue) *state.IntegrationRepair {
	if len(issue.Repairs) == 0 {
		return nil
	}
	return &issue.Repairs[len(issue.Repairs)-1]
}

// checkRepairReview prevents re-pinning a stale approval after authorized repair,
// including a live-head change between the new review and merge report.
func checkRepairReview(issue *state.Issue, head string) error {
	if repair := latestRepair(issue); repair != nil &&
		(issue.ReviewCycles <= repair.ReviewCycles || repair.VerifiedHeadOID == "" || repair.VerifiedHeadOID != head) {
		return fmt.Errorf("%w: integration repair %d requires verification and fresh routed review on live head %s before a new merge report", ErrReviewStale, repair.Number, head)
	}
	return nil
}
