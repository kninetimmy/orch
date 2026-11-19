package cli

import (
	"context"
	"errors"
	"fmt"

	"github.com/kninetimmy/orch/internal/gitops"
	"github.com/kninetimmy/orch/internal/paths"
)

// runStatusRoot resolves only CLI run status's state/lock observation root.
// Git, not an enclosing Orch marker or the common directory's parent, identifies
// the clone. Non-Git locations retain the existing current-directory inspection.
func runStatusRoot(ctx context.Context, env Env) (string, error) {
	location, err := paths.Canonical(env.RepoRoot)
	if err != nil {
		return "", err
	}
	if err := lookupSameDirectory(location, location); err != nil {
		return "", err
	}
	hasGit, err := lookupHasGitAncestor(location)
	if err != nil {
		return "", err
	}
	if !hasGit {
		return location, nil
	}
	top, err := lookupGit(ctx, env, "rev-parse", "--show-toplevel")
	if err != nil {
		return "", err
	}
	checkout, err := gitops.Open(ctx, env.Runner, top)
	if err != nil {
		return "", err
	}
	// A copied Orch marker below Git's top level is not proof that we are
	// still in a linked checkout (its .git pointer may have been removed).
	// An enclosing marker outside this clone, however, has no authority here.
	localRoot, err := paths.FindRoot(location)
	if err != nil && !errors.Is(err, paths.ErrNotFound) {
		return "", err
	}
	if err == nil {
		inside, err := paths.Inside(checkout.Root(), localRoot)
		if err != nil {
			return "", err
		}
		if inside && lookupSameDirectory(localRoot, checkout.Root()) != nil {
			return "", fmt.Errorf("Orch root %s is not the selected Git checkout %s", localRoot, checkout.Root())
		}
	}
	worktrees, err := checkout.ListWorktrees(ctx)
	if err != nil {
		return "", err
	}
	if len(worktrees) == 0 || !worktrees[0].Primary {
		return "", fmt.Errorf("Git did not report a primary checkout")
	}
	root := worktrees[0].Path
	if _, _, err := verifyPrimaryCheckout(ctx, env.Runner, checkout, worktrees, root); err != nil {
		return "", err
	}
	return root, nil
}
