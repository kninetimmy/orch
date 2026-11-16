package gitops

import (
	"context"
	"errors"
	"fmt"
	"os"

	"github.com/kninetimmy/orch/internal/paths"
)

// WithBaseWorktree checks ref out into a disposable detached worktree
// and calls fn with its directory, then attempts removal. It is the
// substrate for PRD §16's "a failure is pre-existing only when
// reproduced on the base branch": the run engine runs the failing
// verification command inside fn and interprets the exit code; gitops
// only provides the checkout.
//
// Originally documented as the one place gitops uses `worktree remove --force`;
// RemoveDetachedWorktree also uses it for registered disposable review trees.
// Reproduction commands legitimately leave untracked build artifacts,
// and the checkout is disposable by construction — detached, holding
// no branch and no user work, at a path this function generated — so
// before #510 this was treated as sufficient for safe deletion. Now Windows
// teardown additionally refuses observed root/parent/interior hazards: generated
// ownership alone does not prevent an ignored junction from reaching other work.
// A cleanup failure is joined with fn's error, never swallowed (PRD §16:
// cleanup failures keep the run incomplete).
func (g *Git) WithBaseWorktree(ctx context.Context, ref string, fn func(dir string) error) (err error) {
	if err := ctx.Err(); err != nil {
		return err
	}
	tmp, err := os.MkdirTemp("", "orch-base-*")
	if err != nil {
		return fmt.Errorf("create base worktree directory: %w", err)
	}
	if err := CheckWorktreeRemoval(ctx, tmp); err != nil {
		return fmt.Errorf("preserve base worktree directory %s for inspection: %w", tmp, err)
	}
	removeEmpty := func(cause error) error {
		cleanupErr := CheckWorktreeRemoval(context.WithoutCancel(ctx), tmp)
		if cleanupErr == nil {
			cleanupErr = os.Remove(tmp) // Only the freshly generated empty path.
		}
		if cleanupErr != nil {
			cleanupErr = fmt.Errorf("clean up base directory %s; preserve and inspect it: %w", tmp, cleanupErr)
		}
		return errors.Join(cause, cleanupErr)
	}
	dir, err := paths.Canonical(tmp)
	if err != nil {
		return removeEmpty(err)
	}
	if _, err := g.git(ctx, g.root, "worktree", "add", "--detach", dir, ref); err != nil {
		return removeEmpty(err)
	}
	defer func() {
		// Cancellation must not skip the cleanup attempt or its safety check.
		cleanupCtx := context.WithoutCancel(ctx)
		cleanupErr := CheckWorktreeRemoval(cleanupCtx, tmp)
		if cleanupErr == nil {
			_, cleanupErr = g.git(cleanupCtx, g.root, "worktree", "remove", "--force", dir)
			if cleanupErr == nil {
				cleanupErr = requireWorktreeAbsent(tmp)
			}
		}
		if cleanupErr != nil {
			err = errors.Join(err, fmt.Errorf("clean up base worktree %s; preserve and inspect it before retrying: %w", dir, cleanupErr))
		}
	}()
	return fn(dir)
}
