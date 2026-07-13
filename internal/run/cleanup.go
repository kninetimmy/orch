package run

import (
	"context"
	"errors"
	"fmt"

	"github.com/kninetimmy/orch/internal/gitops"
	"github.com/kninetimmy/orch/internal/metrics"
	"github.com/kninetimmy/orch/internal/state"
)

// CleanupSchemaVersion is the cleanup request/result schema this build
// accepts and emits.
const CleanupSchemaVersion = 1

// CleanupStatement is the exact confirmation cleanup requires: one
// statement covers the verb's three deletions — remote branch, worktree,
// and local branch — because they are one act (PRD §15).
const CleanupStatement = "cleanup-issue"

// CleanupRequest removes a merged or abandoned issue's git artifacts.
type CleanupRequest struct {
	SchemaVersion int    `json:"schema_version"`
	IssueNumber   int    `json:"issue_number"`
	Statement     string `json:"statement"`
}

// CleanupResult reports the completed cleanup.
type CleanupResult struct {
	SchemaVersion int         `json:"schema_version"`
	IssueNumber   int         `json:"issue_number"`
	Phase         state.Phase `json:"phase"`
}

// Cleanup deletes a merged or abandoned issue's remote branch, worktree,
// and local branch (PRD §12 steps 18-19), then marks the issue cleaned
// (terminal). Each deletion is pre-checked so a crashed cleanup re-runs
// cleanly; the only state write is the final Save. A failure mid-cleanup
// leaves the run incomplete (PRD §16) — the phase stays put and cleanup
// re-runs.
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

	c, err := loadVerb(env, req.IssueNumber, []state.Phase{state.PhaseMerged, state.PhaseAbandoned}, false)
	if err != nil {
		return nil, err
	}
	issue := c.issue()

	git, err := gitops.Open(ctx, env.Runner, env.RepoRoot)
	if err != nil {
		return nil, err
	}
	confirm := gitops.ExplicitConfirmation()

	// 1. Remote branch (idempotent: only delete when present).
	exists, err := git.RemoteBranchExists(ctx, "origin", issue.Branch)
	if err != nil {
		return nil, err
	}
	if exists {
		if err := git.DeleteRemoteBranch(ctx, "origin", issue.Branch, confirm); err != nil {
			return nil, err
		}
	}

	// 2. Worktree (idempotent: an already-removed worktree is not
	// registered, so RemoveWorktree reports ErrUnknownWorktree, which we
	// tolerate). RemoveWorktree prunes stale metadata itself.
	if err := git.RemoveWorktree(ctx, c.worktreeAbs(), confirm); err != nil && !errors.Is(err, gitops.ErrUnknownWorktree) {
		return nil, err
	}

	// 3. Local branch (idempotent: only force-delete when it still
	// resolves — a squash merge leaves it unmerged by design).
	if _, err := git.RevParse(ctx, issue.Branch); err == nil {
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
