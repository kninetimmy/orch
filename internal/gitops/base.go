package gitops

import (
	"context"
	"errors"
	"fmt"
	"os"

	"github.com/kninetimmy/orch/internal/paths"
)

// WithBaseWorktree checks ref out into a disposable detached worktree
// and calls fn with its directory, then always removes it. It is the
// substrate for PRD §16's "a failure is pre-existing only when
// reproduced on the base branch": the run engine runs the failing
// verification command inside fn and interprets the exit code; gitops
// only provides the checkout.
//
// Cleanup is the one place gitops uses `worktree remove --force`:
// reproduction commands legitimately leave untracked build artifacts,
// and the checkout is disposable by construction — detached, holding
// no branch and no user work, at a path this function generated — so
// forced removal cannot destroy work (PRD §15 still holds). A cleanup
// failure is joined with fn's error, never swallowed (PRD §16:
// cleanup failures keep the run incomplete).
func (g *Git) WithBaseWorktree(ctx context.Context, ref string, fn func(dir string) error) (err error) {
	tmp, err := os.MkdirTemp("", "orch-base-*")
	if err != nil {
		return fmt.Errorf("create base worktree directory: %w", err)
	}
	dir, err := paths.Canonical(tmp)
	if err != nil {
		return errors.Join(err, os.Remove(tmp))
	}
	if _, err := g.git(ctx, g.root, "worktree", "add", "--detach", dir, ref); err != nil {
		return errors.Join(err, os.Remove(tmp))
	}
	defer func() {
		// context.WithoutCancel: a canceled ctx must not leave the
		// disposable worktree behind.
		if _, cleanupErr := g.git(context.WithoutCancel(ctx), g.root, "worktree", "remove", "--force", dir); cleanupErr != nil {
			err = errors.Join(err, fmt.Errorf("clean up base worktree: %w", cleanupErr))
		}
	}()
	return fn(dir)
}
