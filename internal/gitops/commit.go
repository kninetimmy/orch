package gitops

import (
	"context"
	"fmt"
	"strings"
)

// CommitAll stages every change in dir (`git add -A`) and commits it
// with message. It is the mechanical-commit primitive `orch init
// --bootstrap` (internal/bootstrap) uses to capture its generated
// files in the disposable worktree it just populated: a caller that
// reaches CommitAll always expects a real change, so an empty commit
// — nothing staged after `git add -A` — is a bug, not a benign no-op,
// and CommitAll fails closed with ErrNothingToCommit rather than
// create one (`git commit --allow-empty` is never used here).
func (g *Git) CommitAll(ctx context.Context, dir, message string) error {
	if _, err := g.git(ctx, dir, "add", "-A"); err != nil {
		return err
	}
	res, err := g.run(ctx, dir, "diff", "--cached", "--quiet")
	if err != nil {
		return err
	}
	switch res.ExitCode {
	case 0:
		return fmt.Errorf("%w in %s", ErrNothingToCommit, dir)
	case 1:
		// Staged changes exist; proceed to commit.
	default:
		return fmt.Errorf("git diff --cached --quiet in %s exited %d: %s", dir, res.ExitCode, strings.TrimSpace(res.Stderr))
	}
	_, err = g.git(ctx, dir, "commit", "-m", message)
	return err
}
