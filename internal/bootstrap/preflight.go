package bootstrap

import (
	"context"
	"fmt"

	"github.com/kninetimmy/orch/internal/ghops"
	"github.com/kninetimmy/orch/internal/gitops"
)

// requireNoOrphans runs Stage 0's three orphan preflights against j's
// fixed branch, each failing closed with its exact remediation command
// naming j.rerunCmd (contract call 3): a crashed or duplicate run
// leaves a leftover local branch, remote branch, or open PR, and
// re-running must collide with it rather than silently sidestep it.
func requireNoOrphans(ctx context.Context, git *gitops.Git, gh *ghops.GH, j job) error {
	// RevParse's error is treated as "does not exist" rather than
	// distinguished from a transport failure, mirroring
	// run.Cleanup's own local-branch presence check.
	if _, err := git.RevParse(ctx, j.branch); err == nil {
		return fmt.Errorf("%w: local branch %s already exists; remove it with `git branch -D %s` and re-run `%s`", ErrBranchExists, j.branch, j.branch, j.rerunCmd)
	}

	remoteExists, err := git.RemoteBranchExists(ctx, "origin", j.branch)
	if err != nil {
		return err
	}
	if remoteExists {
		return fmt.Errorf("%w: remote branch origin/%s already exists; remove it with `git push origin --delete %s` and re-run `%s`", ErrRemoteBranchExists, j.branch, j.branch, j.rerunCmd)
	}

	pr, err := gh.PRForBranch(ctx, j.branch)
	if err != nil {
		return err
	}
	if pr != nil {
		return fmt.Errorf("%w: PR #%d (%s) is already open for branch %s; close or merge it and re-run `%s`", ErrOpenPRExists, pr.Number, pr.URL, j.branch, j.rerunCmd)
	}
	return nil
}
