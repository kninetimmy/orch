package run

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/kninetimmy/orch/internal/gitops"
	"github.com/kninetimmy/orch/internal/metrics"
	"github.com/kninetimmy/orch/internal/state"
)

// CleanupSchemaVersion is the cleanup request/result schema this build
// accepts and emits.
const CleanupSchemaVersion = 1

// CleanupStatement is the exact confirmation cleanup requires: one
// statement covers the verb's three confirmed deletions — remote
// branch, worktree, and local branch — because they are one act
// (PRD §15). Cleanup also removes the disposable review checkout; that
// removal needs no confirmation of its own: it is detached, so it holds
// no branch and none of the committed work a confirmation protects
// (gitops.RemoveDetachedWorktree).
const CleanupStatement = "cleanup-issue"

// CleanupRequest removes a merged or abandoned issue's git artifacts.
type CleanupRequest struct {
	SchemaVersion int    `json:"schema_version"`
	IssueNumber   int    `json:"issue_number"`
	Statement     string `json:"statement"`
	// RunID is required by the direct OpenCode tool. Omit for the ordinary
	// CLI request; it keeps its existing confirmation and preflight.
	RunID *string `json:"run_id,omitempty"`
}

// CleanupResult reports the completed cleanup.
type CleanupResult struct {
	SchemaVersion int         `json:"schema_version"`
	IssueNumber   int         `json:"issue_number"`
	Phase         state.Phase `json:"phase"`
}

// Cleanup deletes a merged or abandoned issue's remote branch, worktree,
// and local branch (PRD §12 steps 18-19) along with any disposable
// review checkout a review cycle left behind, then marks the issue
// cleaned (terminal). Each deletion is pre-checked; the only state write
// is the final Save. Before #505, missing worktree registration sufficed
// for an idempotent retry. Now both checkout paths must be absent on disk:
// residue or inspection failure leaves the phase unchanged (PRD §16).
// A retry can finish after residue has been inspected and safely removed
// outside the failed attempt; this is not automatic orphan recovery.
func Cleanup(ctx context.Context, env Env, reqJSON []byte) (*CleanupResult, error) {
	var req CleanupRequest
	if err := decodeRequest(reqJSON, &req); err != nil {
		return nil, err
	}
	if req.SchemaVersion != CleanupSchemaVersion {
		return nil, fmt.Errorf("%w: schema_version %d is unsupported (this build supports %d)", ErrBadRequest, req.SchemaVersion, CleanupSchemaVersion)
	}
	if req.Statement != CleanupStatement {
		return nil, fmt.Errorf("%w: cleanup statement %q does not equal %q", ErrBadRequest, req.Statement, CleanupStatement)
	}
	if req.IssueNumber <= 0 {
		return nil, fmt.Errorf("%w: cleanup requires a positive issue_number", ErrBadRequest)
	}

	c, err := loadVerb(env, req.IssueNumber, []state.Phase{state.PhaseMerged, state.PhaseAbandoned}, false)
	if err != nil {
		return nil, err
	}
	issue := c.issue()
	if req.RunID != nil && (*req.RunID == "" || *req.RunID != c.st.Run.ID || c.st.Run.Host != "opencode") {
		return nil, fmt.Errorf("%w: OpenCode cleanup run_id does not match the active run", ErrBadRequest)
	}

	git, err := gitops.Open(ctx, env.Runner, env.RepoRoot)
	if err != nil {
		return nil, err
	}
	confirm := gitops.ExplicitConfirmation()
	var bound *cleanupBinding
	var approvedHead string
	if req.RunID != nil {
		if err := git.RequirePrimary(ctx); err != nil {
			return nil, err
		}
		if issue.Phase == state.PhaseMerged {
			if !headOIDPattern.MatchString(issue.ApprovedHeadOID) {
				return nil, fmt.Errorf("OpenCode cleanup: merged issue has no verifiable approved PR head; preserve the checkouts and branches")
			}
			approvedHead = strings.ToLower(issue.ApprovedHeadOID)
		}
		bound = &cleanupBinding{approvedHead: approvedHead}
	}

	// Before #506, checkout-scoped OpenCode services could keep Windows paths
	// locked through Git removal. Explicit cleanup now releases verified idle
	// executor/review locations before any Git deletion. Both interiors must
	// pass inspection before either release. Failure preserves phase;
	// an earlier successful release may already have unloaded an idle cache.
	// Before #510 other hosts skipped this boundary; now Windows checks both
	// raw checkout paths/interiors before any branch deletion on every host.
	check, err := c.releaseCheckouts(ctx, git, bound, false, true)
	if err != nil {
		return nil, err
	}
	var remote *gitops.RemoteBranchBinding
	if bound != nil {
		remote = bound.remote
	}
	// A primary-owned shell, PTY or Code Mode job can start during Git I/O
	// without reloading a target. Recheck observable activity and then the
	// original ownership, not a replacement checkout.
	beforeRemoval := func(removed ...string) error {
		if check != nil {
			if err := check(ctx, removed...); err != nil {
				return err
			}
		}
		if bound != nil && len(removed) < len(bound.checkouts) {
			// Checkout removals are ordered executor, then review.
			return c.verifyCleanupCheckouts(ctx, git, bound.checkouts[len(removed):]...)
		}
		return nil
	}

	// 1. Remote branch (idempotent: only delete when present).
	var exists bool
	if remote != nil {
		exists, err = remote.Exists(ctx)
	} else {
		exists, err = git.RemoteBranchExists(ctx, "origin", issue.Branch)
	}
	if err != nil {
		return nil, err
	}
	if err := beforeRemoval(); err != nil {
		return nil, err
	}
	if remote != nil {
		if err := remote.VerifyDestination(ctx); err != nil {
			return nil, err
		}
	}
	if exists {
		if remote != nil {
			err = remote.Delete(ctx, confirm)
		} else {
			err = git.DeleteRemoteBranch(ctx, "origin", issue.Branch, confirm)
		}
		if err != nil {
			return nil, err
		}
		if err := beforeRemoval(); err != nil {
			return nil, err
		}
	}

	// 2. Worktree. Before #505 we tolerated ErrUnknownWorktree based on
	// registration alone; now gitops returns it only for an absent path.
	// Existing unregistered residue is preserved and fails cleanup.
	// RemoveWorktree verifies removal and prunes stale metadata itself.
	executor := c.worktreeAbs()
	if err := git.RemoveWorktree(ctx, executor, confirm); err != nil && !errors.Is(err, gitops.ErrUnknownWorktree) {
		return nil, err
	}

	// 3. Review worktree, when a review cycle provisioned one
	// (idempotent only when absent, like the executor checkout).
	// No field of the run state names it — see
	// ReviewWorktree for why that absence is deliberate — so cleanup
	// derives its path exactly as the verb that created it does rather
	// than reading state that was never written.
	if err := beforeRemoval(executor); err != nil {
		return nil, err
	}
	review := reviewWorktreeAbs(env.RepoRoot, issue.Number)
	if err := git.RemoveDetachedWorktree(ctx, review); err != nil && !errors.Is(err, gitops.ErrUnknownWorktree) {
		return nil, err
	}

	// 4. Local branch (idempotent: only force-delete when it still
	// resolves — a squash merge leaves it unmerged by design).
	if approvedHead != "" {
		// A bound merged handoff with a tracked inventory must not ignore
		// churn during review removal before mutating the last ref. Ref-only
		// retries have no inventory and retain their existing job checks.
		if bound.checkouts[0].registration != nil || bound.checkouts[1].registration != nil {
			if err := beforeRemoval(executor, review); err != nil {
				return nil, err
			}
		}
		if err := git.ForceDeleteBranchAtHead(ctx, issue.Branch, approvedHead, confirm); err != nil {
			return nil, err
		}
	} else if _, err := git.RevParse(ctx, issue.Branch); err == nil {
		if err := git.ForceDeleteBranch(ctx, issue.Branch, confirm); err != nil {
			return nil, err
		}
	}

	issue.Phase = state.PhaseCleaned
	if err := c.save(); err != nil {
		return nil, wrapAfterMutation(err)
	}

	if err := c.recordMetric(metrics.Event{
		Verb:        "cleanup",
		IssueNumber: issue.Number,
	}); err != nil {
		return nil, err
	}

	return &CleanupResult{
		SchemaVersion: CleanupSchemaVersion,
		IssueNumber:   issue.Number,
		Phase:         state.PhaseCleaned,
	}, nil
}
