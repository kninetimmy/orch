// Package gitops executes the git mechanics of the Delivery workflow
// (PRD §12): per-issue feature branches and worktrees, post-merge
// cleanup, cleanliness and protected-branch preconditions (PRD §5,
// §15), and base-branch checkouts for reproducing failures (PRD §16).
//
// The package is policy-free: callers supply branch names, worktree
// paths, remotes, and protected-branch lists — naming and placement
// policy belongs to the run engine. Every operation fails closed: any
// git error propagates, destructive operations require an explicit
// Confirmation (PRD §15: blocked worktrees and branches are
// preserved), and --force never touches user work.
//
// Operations that pre-check and then act (for example AddWorktree)
// are not atomic; the run engine serializes potentially conflicting
// writes (PRD §14), so the gap is not raced in practice.
package gitops

import (
	"context"
	"fmt"
	"strings"

	"github.com/kninetimmy/orch/internal/execx"
	"github.com/kninetimmy/orch/internal/paths"
)

// gitEnv is applied to every git invocation. GIT_TERMINAL_PROMPT=0
// makes git error instead of prompting for credentials (fail closed,
// never hang); LC_ALL=C keeps any output the package must read stable
// across locales.
var gitEnv = []string{"GIT_TERMINAL_PROMPT=0", "LC_ALL=C"}

// Git executes git operations against one repository through an
// injected execx.Runner.
type Git struct {
	r    execx.Runner
	root string
}

// Open binds to the repository whose primary checkout root is
// repoRoot — the directory holding .orchestrator/. It verifies that
// repoRoot is inside a git work tree and that git's top level equals
// repoRoot: .orchestrator/ must live at the git root, otherwise the
// containment guarantees of PRD §15 would not hold.
func Open(ctx context.Context, r execx.Runner, repoRoot string) (*Git, error) {
	root, err := paths.Canonical(repoRoot)
	if err != nil {
		return nil, err
	}
	g := &Git{r: r, root: root}
	res, err := g.run(ctx, root, "rev-parse", "--show-toplevel")
	if err != nil {
		return nil, err
	}
	if res.ExitCode != 0 {
		return nil, fmt.Errorf("%w: %s (%s)", ErrNotARepo, root, strings.TrimSpace(res.Stderr))
	}
	top := strings.TrimSpace(res.Stdout)
	same, err := samePath(root, top)
	if err != nil {
		return nil, err
	}
	if !same {
		return nil, fmt.Errorf("%s is not the git top level (%s is); %s must sit at the repository root", root, top, paths.OrchestratorDir)
	}
	return g, nil
}

// Root returns the canonical primary-checkout root.
func (g *Git) Root() string { return g.root }

// run executes git with the fail-closed environment and returns the
// raw result; callers that treat non-zero exit as data use this.
func (g *Git) run(ctx context.Context, dir string, args ...string) (execx.Result, error) {
	return g.r.Run(ctx, execx.Cmd{Name: "git", Args: args, Dir: dir, Env: gitEnv})
}

// git is run plus the common interpretation: a non-zero exit becomes
// an error naming the subcommand, directory, and git's stderr;
// success returns trimmed stdout.
func (g *Git) git(ctx context.Context, dir string, args ...string) (string, error) {
	res, err := g.run(ctx, dir, args...)
	if err != nil {
		return "", err
	}
	if res.ExitCode != 0 {
		return "", fmt.Errorf("git %s in %s exited %d: %s", args[0], dir, res.ExitCode, strings.TrimSpace(res.Stderr))
	}
	return strings.TrimSpace(res.Stdout), nil
}

// samePath reports whether two paths name the same canonical
// location, reusing the segment-aware, case-folding containment
// semantics of paths.Inside in both directions.
func samePath(a, b string) (bool, error) {
	ab, err := paths.Inside(a, b)
	if err != nil {
		return false, err
	}
	ba, err := paths.Inside(b, a)
	if err != nil {
		return false, err
	}
	return ab && ba, nil
}
