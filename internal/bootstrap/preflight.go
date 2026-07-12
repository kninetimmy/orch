package bootstrap

import (
	"context"
	"fmt"

	"github.com/kninetimmy/orch/internal/ghops"
	"github.com/kninetimmy/orch/internal/gitops"
)

// requireNoOrphans runs Stage 0's three orphan preflights against the
// fixed BootstrapBranch, each failing closed with its exact
// remediation command (contract call 3): a crashed or duplicate
// bootstrap run leaves a leftover local branch, remote branch, or open
// PR, and re-running must collide with it rather than silently
// sidestep it.
func requireNoOrphans(ctx context.Context, git *gitops.Git, gh *ghops.GH) error {
	// RevParse's error is treated as "does not exist" rather than
	// distinguished from a transport failure, mirroring
	// run.Cleanup's own local-branch presence check.
	if _, err := git.RevParse(ctx, BootstrapBranch); err == nil {
		return fmt.Errorf("%w: local branch %s already exists; remove it with `git branch -D %s` and re-run `orch init --bootstrap`", ErrBranchExists, BootstrapBranch, BootstrapBranch)
	}

	remoteExists, err := git.RemoteBranchExists(ctx, "origin", BootstrapBranch)
	if err != nil {
		return err
	}
	if remoteExists {
		return fmt.Errorf("%w: remote branch origin/%s already exists; remove it with `git push origin --delete %s` and re-run `orch init --bootstrap`", ErrRemoteBranchExists, BootstrapBranch, BootstrapBranch)
	}

	pr, err := gh.PRForBranch(ctx, BootstrapBranch)
	if err != nil {
		return err
	}
	if pr != nil {
		return fmt.Errorf("%w: PR #%d (%s) is already open for branch %s; close or merge it and re-run `orch init --bootstrap`", ErrOpenPRExists, pr.Number, pr.URL, BootstrapBranch)
	}
	return nil
}
