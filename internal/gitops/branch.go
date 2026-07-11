package gitops

import (
	"context"
	"fmt"
	"strings"
)

// Fetch updates refs from remote; callers use it before FastForward
// or before reproducing a failure against a remote base ref.
func (g *Git) Fetch(ctx context.Context, remote string, refspecs ...string) error {
	args := append([]string{"fetch", remote}, refspecs...)
	_, err := g.git(ctx, g.root, args...)
	return err
}

// DeleteBranch deletes a local branch that is fully merged into the
// current HEAD; an unmerged branch wraps ErrBranchNotMerged and is
// preserved (PRD §15). After a squash merge the local branch is never
// "merged" in git's ancestry terms — use ForceDeleteBranch once the
// merge has been verified through the GitHub API.
func (g *Git) DeleteBranch(ctx context.Context, branch string, c Confirmation) error {
	if !c.ok {
		return fmt.Errorf("%w: delete branch %s", ErrNotConfirmed, branch)
	}
	if err := g.requireBranchExists(ctx, branch); err != nil {
		return err
	}
	res, err := g.run(ctx, g.root, "merge-base", "--is-ancestor", "refs/heads/"+branch, "HEAD")
	if err != nil {
		return err
	}
	switch res.ExitCode {
	case 0:
		// Merged; fall through to the deletion.
	case 1:
		return fmt.Errorf("%w: %s", ErrBranchNotMerged, branch)
	default:
		return fmt.Errorf("git merge-base in %s exited %d: %s", g.root, res.ExitCode, strings.TrimSpace(res.Stderr))
	}
	_, err = g.git(ctx, g.root, "branch", "-d", branch)
	return err
}

// ForceDeleteBranch deletes a local branch with -D. It exists solely
// for the squash-merge default (PRD §16), where the branch never
// becomes an ancestor of the base: the caller MUST have verified the
// PR merged before calling. It refuses to delete a branch checked out
// in any worktree, including the primary checkout.
func (g *Git) ForceDeleteBranch(ctx context.Context, branch string, c Confirmation) error {
	if !c.ok {
		return fmt.Errorf("%w: force-delete branch %s", ErrNotConfirmed, branch)
	}
	if err := g.requireBranchExists(ctx, branch); err != nil {
		return err
	}
	worktrees, err := g.ListWorktrees(ctx)
	if err != nil {
		return err
	}
	for _, wt := range worktrees {
		if wt.Branch == branch {
			return fmt.Errorf("branch %s is checked out in worktree %s; remove the worktree first", branch, wt.Path)
		}
	}
	_, err = g.git(ctx, g.root, "branch", "-D", branch)
	return err
}

// DeleteRemoteBranch deletes the merged remote branch (PRD §12 step
// 18) with `git push <remote> --delete <branch>` — the same
// credential path that pushed the branch in the first place.
func (g *Git) DeleteRemoteBranch(ctx context.Context, remote, branch string, c Confirmation) error {
	if !c.ok {
		return fmt.Errorf("%w: delete remote branch %s/%s", ErrNotConfirmed, remote, branch)
	}
	_, err := g.git(ctx, g.root, "push", remote, "--delete", branch)
	return err
}

// FastForward advances the primary checkout to remote's tip of branch
// (PRD §12 step 20). Fail-closed gates: branch must be the branch
// checked out in the primary checkout, and the tree must be clean.
// Diverged history wraps ErrNotFastForward and changes nothing.
func (g *Git) FastForward(ctx context.Context, remote, branch string) error {
	current, err := g.CurrentBranch(ctx)
	if err != nil {
		return err
	}
	if current != branch {
		return fmt.Errorf("primary checkout is on %s, not %s; refusing to fast-forward", current, branch)
	}
	if err := g.RequireClean(ctx, g.root); err != nil {
		return err
	}
	if err := g.Fetch(ctx, remote, branch); err != nil {
		return err
	}
	res, err := g.run(ctx, g.root, "merge-base", "--is-ancestor", "HEAD", "FETCH_HEAD")
	if err != nil {
		return err
	}
	switch res.ExitCode {
	case 0:
		// HEAD is an ancestor of (or equal to) the fetched tip.
	case 1:
		// Local commits missing from the remote tip mean either a
		// diverged history or a locally-ahead branch; both are
		// suspicious in Delivery, so both fail closed.
		return fmt.Errorf("%w: local %s has commits not on %s/%s", ErrNotFastForward, branch, remote, branch)
	default:
		return fmt.Errorf("git merge-base in %s exited %d: %s", g.root, res.ExitCode, strings.TrimSpace(res.Stderr))
	}
	_, err = g.git(ctx, g.root, "merge", "--ff-only", "FETCH_HEAD")
	return err
}

// requireBranchExists fails closed when branch is not a local branch,
// so deletion errors name the real problem instead of git's generic
// refusal text.
func (g *Git) requireBranchExists(ctx context.Context, branch string) error {
	res, err := g.run(ctx, g.root, "rev-parse", "--verify", "--quiet", "refs/heads/"+branch)
	if err != nil {
		return err
	}
	if res.ExitCode != 0 {
		return fmt.Errorf("branch %s does not exist in %s", branch, g.root)
	}
	return nil
}
