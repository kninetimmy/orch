package run

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/kninetimmy/orch/internal/gitops"
	"github.com/kninetimmy/orch/internal/opencode"
	"github.com/kninetimmy/orch/internal/paths"
)

// releaseCheckouts is the shared-engine/host handoff, not generic Git policy.
// Only explicit review replacement and confirmed cleanup call it. Other hosts,
// unknown/unregistered paths and truly absent paths keep their Git-only path;
// gitops still refuses residue and verifies absence independently.
// Before #510 other hosts bypassed all interior preflight. Now Windows checks
// every requested checkout before host release or the first branch deletion.
func (c *verbCtx) releaseCheckouts(ctx context.Context, git *gitops.Git, reviews ...bool) error {
	for _, review := range reviews {
		target := c.worktreeAbs()
		if review {
			target = reviewWorktreeAbs(c.env.RepoRoot, c.issue().Number)
		}
		if err := gitops.CheckWorktreeRemoval(ctx, target); err != nil {
			return err
		}
	}
	if c.st.Run.Host != "opencode" {
		return nil
	}
	// Validate every target before releasing any services: a dangerous review
	// interior must also prevent executor disposal during cleanup.
	var targets []string
	for _, review := range reviews {
		target, err := c.checkoutReleaseTarget(ctx, git, review)
		if err != nil {
			return err
		}
		if target != "" {
			targets = append(targets, target)
		}
	}
	for _, target := range targets {
		if err := opencode.ReleaseLocation(ctx, c.env.Runner, c.env.RepoRoot, target); err != nil {
			return err
		}
	}
	// Releasing locks makes Git traversal more reachable. Refuse newly observed
	// hazards too; this is still not atomic exclusion of filesystem re-entry.
	for _, target := range targets {
		if err := gitops.InspectWorktree(ctx, target); err != nil {
			return err
		}
	}
	return nil
}

func (c *verbCtx) checkoutReleaseTarget(ctx context.Context, git *gitops.Git, review bool) (string, error) {
	issue := c.issue()
	rel := worktreeRel(issue.Number)
	if !review && issue.Worktree != rel {
		return "", fmt.Errorf("checkout release: issue worktree does not match its engine-owned path; preserve it and inspect ownership")
	}
	if review {
		rel = reviewWorktreeRel(issue.Number)
	}
	target := filepath.Join(c.env.RepoRoot, filepath.FromSlash(rel))
	list, err := git.ListWorktrees(ctx)
	if err != nil {
		return "", err
	}
	// Canonical comparison locates registration, but filesystem identity is a
	// separate requirement before any host mutation (including on Windows).
	canon, err := paths.Canonical(target)
	if err != nil {
		return "", err
	}
	var owned *gitops.Worktree
	for i := range list {
		inside, err := paths.Inside(canon, list[i].Path)
		if err != nil {
			return "", err
		}
		reverse, err := paths.Inside(list[i].Path, canon)
		if err != nil {
			return "", err
		}
		if inside && reverse {
			if owned != nil {
				return "", fmt.Errorf("checkout release: duplicate Git registration")
			}
			owned = &list[i]
		}
	}
	if owned == nil {
		return "", nil // No host disposal of unregistered residue, ever.
	}
	info, err := os.Lstat(target)
	if err != nil || !info.IsDir() || owned.Primary {
		return "", fmt.Errorf("checkout release: refusing primary, redirected or unverifiable checkout")
	}
	registered, err := os.Stat(owned.Path)
	if err != nil || !os.SameFile(info, registered) {
		return "", fmt.Errorf("checkout release: registered checkout identity differs")
	}
	container := filepath.Join(c.env.RepoRoot, filepath.FromSlash(WorktreeContainer))
	parent, err := os.Stat(filepath.Dir(canon))
	if err != nil {
		return "", err
	}
	expectedParent, err := os.Stat(container)
	if err != nil || !os.SameFile(parent, expectedParent) {
		return "", fmt.Errorf("checkout release: checkout escaped its disposable container")
	}
	inside, err := paths.Inside(c.env.RepoRoot, canon)
	if err != nil || !inside {
		return "", fmt.Errorf("checkout release: disposable checkout is outside the primary root")
	}
	checkout, err := gitops.Open(ctx, c.env.Runner, target)
	if err != nil {
		return "", err
	}
	common, err := git.CommonDir(ctx)
	if err != nil {
		return "", err
	}
	selectedCommon, err := checkout.CommonDir(ctx)
	if err != nil {
		return "", err
	}
	left, err := os.Stat(common)
	if err != nil {
		return "", err
	}
	right, err := os.Stat(selectedCommon)
	if err != nil || !left.IsDir() || !right.IsDir() || !os.SameFile(left, right) {
		return "", fmt.Errorf("checkout release: Git common-directory identity differs")
	}
	if review {
		if !owned.Detached {
			return "", fmt.Errorf("checkout release: review checkout holds a branch; refusing disposal")
		}
	} else {
		if owned.Detached || owned.Branch != issue.Branch {
			return "", fmt.Errorf("checkout release: executor branch differs from run ownership")
		}
		if err := git.RequireClean(ctx, target); err != nil {
			return "", err
		}
	}
	if err := gitops.InspectWorktree(ctx, target); err != nil {
		return "", err
	}
	return canon, nil
}
