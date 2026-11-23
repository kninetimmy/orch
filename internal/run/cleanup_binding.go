package run

import (
	"context"
	"fmt"
	"os"
	"slices"

	"github.com/kninetimmy/orch/internal/gitops"
	"github.com/kninetimmy/orch/internal/paths"
)

type cleanupCheckout struct {
	path         string
	canonical    string
	registration *gitops.Worktree
	info         os.FileInfo
}

// Keep the original registrations (including HEAD) and filesystem identities
// across the host handoff. A later observation must not authorize a replacement.
func (c *verbCtx) verifyCleanupCheckouts(ctx context.Context, git *gitops.Git, targets ...cleanupCheckout) error {
	list, err := git.ListWorktrees(ctx)
	if err != nil {
		return err
	}
	for _, target := range targets {
		info, err := os.Lstat(target.path)
		if target.registration == nil && os.IsNotExist(err) {
			canonical, err := paths.Canonical(target.path)
			if err != nil {
				return err
			}
			if !slices.ContainsFunc(list, func(w gitops.Worktree) bool { return w.Path == canonical }) {
				continue
			}
		}
		if err != nil || target.registration == nil || !info.IsDir() || !os.SameFile(info, target.info) ||
			!slices.Contains(list, *target.registration) {
			return fmt.Errorf("OpenCode cleanup: checkout identity changed or could not be reverified; preserve the checkout")
		}
		if err := verifyCheckoutCommon(ctx, c.env.Runner, git, target.path); err != nil {
			return err
		}
		if !target.registration.Detached {
			if err := git.RequireClean(ctx, target.path); err != nil {
				return err
			}
		}
	}
	return nil
}
