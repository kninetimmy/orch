package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
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
	MemhubMode    string `json:"memhub_mode"`
	MemoryRoot    string `json:"memory_root"`
	CodeRoot      string `json:"code_root"`
	GitCommonDir  string `json:"git_common_dir"`
	Head          string `json:"head"`
}

func hookLookupContext(env Env) error {
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
	if err != nil {
		return err
	}
	if err := lookupSameDirectory(root, primary); err != nil {
		return err
	}
	found := false
	var head string
	for _, wt := range worktrees {
		if lookupSameDirectory(wt.Path, checkout.Root()) == nil {
			found, head = true, wt.Head
			break
		}
	}
	if !found {
		return fmt.Errorf("lookup context: selected checkout is not a registered worktree")
	}
	primaryGit, err := gitops.Open(ctx, env.Runner, root)
	if err != nil {
		return err
	}
	common, err := primaryGit.CommonDir(ctx)
	if err != nil {
		return err
	}
	selectedCommon, err := checkout.CommonDir(ctx)
	if err != nil {
		return err
	}
	if err := lookupSameDirectory(common, selectedCommon); err != nil {
		return err
	}
	cfg, err := config.Load(root)
	if err != nil {
		return err
	}
	return json.NewEncoder(env.Stdout).Encode(lookupContext{
		SchemaVersion: 1, MemhubMode: cfg.Memhub.Mode, MemoryRoot: root,
		CodeRoot: checkout.Root(), GitCommonDir: common, Head: head,
	})
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
