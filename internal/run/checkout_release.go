package run

import (
	"context"
	"errors"
	"fmt"
	"io"
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
func (c *verbCtx) releaseCheckouts(ctx context.Context, git *gitops.Git, reviews ...bool) error {
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
		if err := inspectCheckout(ctx, target); err != nil {
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
	if err := inspectCheckout(ctx, target); err != nil {
		return "", err
	}
	return canon, nil
}

// inspectCheckout reads metadata, including ignored entries, never link
// destinations or file contents. Go's Lstat marks Windows junctions and unknown
// name-surrogate reparse points as symlink/irregular, not ordinary directories.
// Root confines child opens even if a directory is replaced during inspection;
// handle/path identity checks precede enumeration. This is not a tree snapshot
// or a link-safe replacement for generic Git teardown.
func inspectCheckout(ctx context.Context, target string) (resultErr error) {
	original, err := os.Lstat(target)
	if err != nil || original.Mode().Type() != os.ModeDir {
		return fmt.Errorf("checkout release: linked or unverifiable checkout entry; preserve and inspect it")
	}
	root, err := os.OpenRoot(target)
	if err != nil {
		return fmt.Errorf("checkout release: cannot inspect checkout interior: %w", err)
	}
	defer func() { resultErr = errors.Join(resultErr, root.Close()) }()
	opened, err := root.Lstat(".")
	if err != nil || !os.SameFile(original, opened) {
		return fmt.Errorf("checkout release: checkout identity changed during inspection")
	}
	// ponytail: bound metadata work, refuse larger/deeper trees rather than
	// silently skipping ignored paths or treating incomplete evidence as safe.
	remaining := 100_000
	var walk func(*os.Root, int) error
	walk = func(dir *os.Root, depth int) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		if depth > 64 {
			return fmt.Errorf("checkout release: checkout exceeds interior inspection bounds")
		}
		f, err := dir.Open(".")
		if err != nil {
			return fmt.Errorf("checkout release: cannot inspect checkout interior: %w", err)
		}
		entries, readErr := f.ReadDir(remaining + 1)
		closeErr := f.Close()
		if readErr != nil && !errors.Is(readErr, io.EOF) {
			return errors.Join(readErr, closeErr)
		}
		if closeErr != nil {
			return closeErr
		}
		if len(entries) > remaining {
			return fmt.Errorf("checkout release: checkout exceeds interior inspection bounds")
		}
		remaining -= len(entries)
		for _, entry := range entries {
			if err := ctx.Err(); err != nil {
				return err
			}
			info, err := dir.Lstat(entry.Name())
			if err != nil || (info.Mode().Type() != 0 && info.Mode().Type() != os.ModeDir) {
				return fmt.Errorf("checkout release: linked or unverifiable checkout entry; preserve and inspect it")
			}
			if !info.IsDir() {
				continue
			}
			child, err := dir.OpenRoot(entry.Name())
			if err != nil {
				return fmt.Errorf("checkout release: cannot inspect checkout interior: %w", err)
			}
			opened, openErr := child.Lstat(".")
			current, currentErr := dir.Lstat(entry.Name())
			if openErr != nil || currentErr != nil || current.Mode().Type() != os.ModeDir ||
				!os.SameFile(info, current) || !os.SameFile(info, opened) {
				return errors.Join(fmt.Errorf("checkout release: checkout interior identity changed"), child.Close())
			}
			if err := errors.Join(walk(child, depth+1), child.Close()); err != nil {
				return err
			}
		}
		return nil
	}
	return walk(root, 0)
}
