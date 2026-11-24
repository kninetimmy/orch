package run

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/kninetimmy/orch/internal/execx"
	"github.com/kninetimmy/orch/internal/ghops"
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
func (c *verbCtx) releaseCheckouts(ctx context.Context, git *gitops.Git, bound *cleanupBinding, reviews ...bool) (opencode.ReleaseCheck, error) {
	for _, review := range reviews {
		target := c.worktreeAbs()
		if review {
			target = reviewWorktreeAbs(c.env.RepoRoot, c.issue().Number)
		}
		if err := gitops.CheckWorktreeRemoval(ctx, target); err != nil {
			return nil, err
		}
	}
	if c.st.Run.Host != "opencode" {
		return nil, nil
	}
	// Validate every target before releasing any services: a dangerous review
	// interior must also prevent executor disposal during cleanup.
	var targets []string
	for _, review := range reviews {
		target, err := c.checkoutReleaseTarget(ctx, git, review)
		if err != nil {
			return nil, err
		}
		if bound != nil {
			if target.registration == nil {
				if _, err := os.Lstat(target.path); !os.IsNotExist(err) {
					return nil, fmt.Errorf("checkout release: unregistered checkout or absence cannot be verified; preserve it and inspect ownership")
				}
			}
			if review {
				bound.checkouts[1] = target
			} else {
				if bound.approvedHead != "" && target.registration != nil && target.registration.Head != bound.approvedHead {
					return nil, fmt.Errorf("OpenCode cleanup: executor HEAD differs from the approved PR head; preserve the checkout")
				}
				bound.checkouts[0] = target
			}
		}
		if target.registration != nil {
			targets = append(targets, target.canonical)
		}
	}
	var verify func(context.Context) error
	if bound != nil {
		if bound.approvedHead != "" {
			issue := c.issue()
			branch := issue.Branch
			// A retry with an absent executor still must preserve unexpected refs.
			if _, err := git.LocalBranchAtHead(ctx, branch, bound.approvedHead); err != nil {
				return nil, err
			}
			if err := git.RequireBranchIdle(ctx, branch, bound.checkouts[0].canonical); err != nil {
				return nil, err
			}
			remote, err := git.BindRemoteBranchAtHead(ctx, "origin", branch, bound.approvedHead, func(endpoint string) bool {
				return ghops.MatchesPullRequestRepository(issue.PRURL, issue.PRNumber, endpoint)
			})
			if err != nil {
				return nil, err
			}
			bound.remote = remote
		}
		verify = func(ctx context.Context) error {
			if err := c.verifyCleanupCheckouts(ctx, git, bound.checkouts[:]...); err != nil {
				return err
			}
			if bound.remote != nil {
				return bound.remote.VerifyDestination(ctx)
			}
			return nil
		}
	}
	// Before #525 a later target's host preflight could reopen an earlier one.
	// Batch every loading probe before either eviction; final host inventory and
	// Windows delete-access observations must pass for both before Git mutation.
	check, err := opencode.ReleaseLocations(ctx, c.env.Runner, c.env.RepoRoot, verify, targets...)
	if err != nil {
		return nil, err
	}
	// Releasing locks makes Git traversal more reachable. Refuse newly observed
	// hazards too; this is still not atomic exclusion of filesystem re-entry.
	for _, target := range targets {
		if err := gitops.InspectWorktree(ctx, target); err != nil {
			return nil, err
		}
	}
	return check, nil
}

func (c *verbCtx) checkoutReleaseTarget(ctx context.Context, git *gitops.Git, review bool) (cleanupCheckout, error) {
	issue := c.issue()
	rel := worktreeRel(issue.Number)
	if !review && issue.Worktree != rel {
		return cleanupCheckout{}, fmt.Errorf("checkout release: issue worktree does not match its engine-owned path; preserve it and inspect ownership")
	}
	if review {
		rel = reviewWorktreeRel(issue.Number)
	}
	target := filepath.Join(c.env.RepoRoot, filepath.FromSlash(rel))
	list, err := git.ListWorktrees(ctx)
	if err != nil {
		return cleanupCheckout{}, err
	}
	// Canonical comparison locates registration, but filesystem identity is a
	// separate requirement before any host mutation (including on Windows).
	canon, err := paths.Canonical(target)
	if err != nil {
		return cleanupCheckout{}, err
	}
	var owned *gitops.Worktree
	for i := range list {
		inside, err := paths.Inside(canon, list[i].Path)
		if err != nil {
			return cleanupCheckout{}, err
		}
		reverse, err := paths.Inside(list[i].Path, canon)
		if err != nil {
			return cleanupCheckout{}, err
		}
		if inside && reverse {
			if owned != nil {
				return cleanupCheckout{}, fmt.Errorf("checkout release: duplicate Git registration")
			}
			owned = &list[i]
		}
	}
	if owned == nil {
		return cleanupCheckout{path: target}, nil // No host disposal of unregistered residue, ever.
	}
	info, err := os.Lstat(target)
	if err != nil || !info.IsDir() || owned.Primary {
		return cleanupCheckout{}, fmt.Errorf("checkout release: refusing primary, redirected or unverifiable checkout")
	}
	registered, err := os.Stat(owned.Path)
	if err != nil || !os.SameFile(info, registered) {
		return cleanupCheckout{}, fmt.Errorf("checkout release: registered checkout identity differs")
	}
	container := filepath.Join(c.env.RepoRoot, filepath.FromSlash(WorktreeContainer))
	parent, err := os.Stat(filepath.Dir(canon))
	if err != nil {
		return cleanupCheckout{}, err
	}
	expectedParent, err := os.Stat(container)
	if err != nil || !os.SameFile(parent, expectedParent) {
		return cleanupCheckout{}, fmt.Errorf("checkout release: checkout escaped its disposable container")
	}
	inside, err := paths.Inside(c.env.RepoRoot, canon)
	if err != nil || !inside {
		return cleanupCheckout{}, fmt.Errorf("checkout release: disposable checkout is outside the primary root")
	}
	if err := verifyCheckoutCommon(ctx, c.env.Runner, git, target); err != nil {
		return cleanupCheckout{}, err
	}
	if review {
		if !owned.Detached {
			return cleanupCheckout{}, fmt.Errorf("checkout release: review checkout holds a branch; refusing disposal")
		}
	} else {
		if owned.Detached || owned.Branch != issue.Branch {
			return cleanupCheckout{}, fmt.Errorf("checkout release: executor branch differs from run ownership")
		}
		if err := git.RequireClean(ctx, target); err != nil {
			return cleanupCheckout{}, err
		}
	}
	if err := gitops.InspectWorktree(ctx, target); err != nil {
		return cleanupCheckout{}, err
	}
	return cleanupCheckout{path: target, canonical: canon, registration: owned, info: info}, nil
}

func verifyCheckoutCommon(ctx context.Context, runner execx.Runner, git *gitops.Git, target string) error {
	checkout, err := gitops.Open(ctx, runner, target)
	if err != nil {
		return err
	}
	common, err := git.CommonDir(ctx)
	if err != nil {
		return err
	}
	selectedCommon, err := checkout.CommonDir(ctx)
	if err != nil {
		return err
	}
	left, err := os.Stat(common)
	if err != nil {
		return err
	}
	right, err := os.Stat(selectedCommon)
	if err != nil || !left.IsDir() || !right.IsDir() || !os.SameFile(left, right) {
		return fmt.Errorf("checkout release: Git common-directory identity differs")
	}
	return nil
}
