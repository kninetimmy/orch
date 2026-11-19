package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/kninetimmy/orch/internal/config"
	"github.com/kninetimmy/orch/internal/execx"
	"github.com/kninetimmy/orch/internal/gitops"
	"github.com/kninetimmy/orch/internal/paths"
)

// lookupContext is an observation, not lookup authorization or a Memhub probe.
// It deliberately reads neither project memory nor Delivery state, and runs
// only read-only Git commands. Mode has exactly one resolver: config.Load.
type lookupContext struct {
	SchemaVersion int    `json:"schema_version"`
	Managed       bool   `json:"managed"`
	MemhubMode    string `json:"memhub_mode,omitempty"`
	MemoryRoot    string `json:"memory_root,omitempty"`
	CodeRoot      string `json:"code_root,omitempty"`
	GitCommonDir  string `json:"git_common_dir,omitempty"`
	Head          string `json:"head,omitempty"`
}

func hookLookupContext(env Env) error {
	location, err := paths.Canonical(env.RepoRoot)
	if err != nil {
		return err
	}
	if err := lookupSameDirectory(location, location); err != nil {
		return err
	}
	localRoot, err := paths.FindOutermostRoot(location)
	if err != nil && !errors.Is(err, paths.ErrNotFound) {
		return err
	}
	if errors.Is(err, paths.ErrNotFound) {
		// A linked checkout can have a managed primary outside its ancestry,
		// even when its copied Orch marker is absent. Only skip Git discovery
		// when there is no Git marker either; broken markers are not absence.
		hasGit, err := lookupHasGitAncestor(location)
		if err != nil {
			return err
		}
		if !hasGit {
			return json.NewEncoder(env.Stdout).Encode(lookupContext{SchemaVersion: 2})
		}
	}
	ctx := context.Background()
	top, err := lookupGit(ctx, env, "rev-parse", "--show-toplevel")
	if err != nil {
		return err
	}
	checkout, err := gitops.Open(ctx, env.Runner, top)
	if err != nil {
		return err
	}
	worktrees, err := checkout.ListWorktrees(ctx)
	if err != nil {
		return err
	}
	if len(worktrees) == 0 || !worktrees[0].Primary {
		return fmt.Errorf("lookup context: Git did not report a primary checkout")
	}
	primary := worktrees[0].Path
	// Do not let a nested or unrelated Orch configuration replace the primary.
	root, err := paths.FindOutermostRoot(primary)
	unmanaged := errors.Is(err, paths.ErrNotFound)
	if err != nil && !unmanaged {
		return err
	}
	if unmanaged {
		if localRoot != "" {
			return fmt.Errorf("lookup context: selected location has an Orch root but its Git primary does not")
		}
		root = primary
	}
	if err := lookupSameDirectory(root, primary); err != nil {
		return err
	}
	common, head, err := verifyPrimaryCheckout(ctx, env.Runner, checkout, worktrees, root)
	if err != nil {
		return err
	}
	if unmanaged {
		return json.NewEncoder(env.Stdout).Encode(lookupContext{SchemaVersion: 2})
	}
	cfg, err := config.Load(root)
	if err != nil {
		return err
	}
	return json.NewEncoder(env.Stdout).Encode(lookupContext{
		SchemaVersion: 2, Managed: true, MemhubMode: cfg.Memhub.Mode, MemoryRoot: root,
		CodeRoot: checkout.Root(), GitCommonDir: common, Head: head,
	})
}

// verifyPrimaryCheckout shares only Git registration and filesystem identity
// checks with CLI run status, not lookup's Orch-root or Memhub configuration.
func verifyPrimaryCheckout(ctx context.Context, runner execx.Runner, checkout *gitops.Git, worktrees []gitops.Worktree, root string) (common, head string, err error) {
	found := false
	for _, wt := range worktrees {
		if lookupSameDirectory(wt.Path, checkout.Root()) == nil {
			found, head = true, wt.Head
			break
		}
	}
	if !found {
		return "", "", fmt.Errorf("lookup context: selected checkout is not a registered worktree")
	}
	primaryGit, err := gitops.Open(ctx, runner, root)
	if err != nil {
		return "", "", err
	}
	common, err = primaryGit.CommonDir(ctx)
	if err != nil {
		return "", "", err
	}
	selectedCommon, err := checkout.CommonDir(ctx)
	if err != nil {
		return "", "", err
	}
	if err := lookupSameDirectory(common, selectedCommon); err != nil {
		return "", "", err
	}
	return common, head, nil
}

func lookupHasGitAncestor(dir string) (bool, error) {
	for {
		_, err := os.Lstat(filepath.Join(dir, ".git"))
		if err == nil {
			return true, nil
		}
		if !errors.Is(err, os.ErrNotExist) {
			return false, err
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return false, nil
		}
		dir = parent
	}
}

func lookupSameDirectory(a, b string) error {
	left, err := os.Stat(a)
	if err != nil {
		return err
	}
	right, err := os.Stat(b)
	if err != nil {
		return err
	}
	if !left.IsDir() || !right.IsDir() || !os.SameFile(left, right) {
		return fmt.Errorf("lookup context: directory identity mismatch: %s and %s", a, b)
	}
	return nil
}

func lookupGit(ctx context.Context, env Env, args ...string) (string, error) {
	res, err := env.Runner.Run(ctx, execx.Cmd{
		Name: "git", Args: args, Dir: env.RepoRoot,
		Env: []string{"GIT_TERMINAL_PROMPT=0", "LC_ALL=C"},
	})
	if err != nil {
		return "", err
	}
	if res.ExitCode != 0 {
		return "", fmt.Errorf("lookup context: git %s: %s", args[0], strings.TrimSpace(res.Stderr))
	}
	return strings.TrimSpace(res.Stdout), nil
}
