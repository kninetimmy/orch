package gitops

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"strings"

	"github.com/kninetimmy/orch/internal/paths"
)

// Worktree describes one entry of `git worktree list --porcelain`.
type Worktree struct {
	// Path is the canonical worktree directory.
	Path string
	// Branch is the checked-out branch, "" when detached.
	Branch string
	// Head is the checked-out commit hash.
	Head string
	// Detached reports a detached HEAD.
	Detached bool
	// Primary marks the main working tree, which is never removed.
	Primary bool
}

// ListWorktrees returns every worktree registered with the
// repository, primary checkout first (git guarantees that order).
func (g *Git) ListWorktrees(ctx context.Context) ([]Worktree, error) {
	out, err := g.git(ctx, g.root, "worktree", "list", "--porcelain")
	if err != nil {
		return nil, err
	}
	var list []Worktree
	var cur *Worktree
	flush := func() error {
		if cur == nil {
			return nil
		}
		canon, err := paths.Canonical(cur.Path)
		if err != nil {
			return err
		}
		cur.Path = canon
		cur.Primary = len(list) == 0
		list = append(list, *cur)
		cur = nil
		return nil
	}
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimRight(line, "\r")
		switch {
		case line == "":
			if err := flush(); err != nil {
				return nil, err
			}
		case strings.HasPrefix(line, "worktree "):
			cur = &Worktree{Path: strings.TrimPrefix(line, "worktree ")}
		case cur == nil:
			return nil, fmt.Errorf("parse worktree list: unexpected line %q", line)
		case strings.HasPrefix(line, "HEAD "):
			cur.Head = strings.TrimPrefix(line, "HEAD ")
		case strings.HasPrefix(line, "branch "):
			cur.Branch = strings.TrimPrefix(strings.TrimPrefix(line, "branch "), "refs/heads/")
		case line == "detached":
			cur.Detached = true
		}
	}
	if err := flush(); err != nil {
		return nil, err
	}
	return list, nil
}

// AddWorktree creates branch at startPoint together with a new
// worktree at path (PRD §12 step 7: one branch/worktree per issue).
// It fails closed before touching git when path already exists (never
// clobber), when the branch already exists (ErrBranchExists), or when
// path lies inside the primary checkout and is not git-ignored (F1:
// worktrees must be isolated from the primary checkout's tracked tree,
// PRD §5 — an ignored inside-primary path is safe because it never
// appears in `status --porcelain`, so RequireClean and isolation
// guarantees still hold). Never uses --force.
func (g *Git) AddWorktree(ctx context.Context, path, branch, startPoint string) (*Worktree, error) {
	canon, err := paths.Canonical(path)
	if err != nil {
		return nil, err
	}
	switch _, err := os.Lstat(canon); {
	case err == nil:
		return nil, fmt.Errorf("worktree path %s already exists; refusing to clobber it", canon)
	case !errors.Is(err, fs.ErrNotExist):
		return nil, fmt.Errorf("check worktree path %s: %w", canon, err)
	}
	inside, err := paths.Inside(g.root, canon)
	if err != nil {
		return nil, err
	}
	if inside {
		if err := g.RequireIgnored(ctx, canon); err != nil {
			return nil, fmt.Errorf("worktree path %s is inside the primary checkout and not git-ignored: %w", canon, err)
		}
	}
	res, err := g.run(ctx, g.root, "rev-parse", "--verify", "--quiet", "refs/heads/"+branch)
	if err != nil {
		return nil, err
	}
	if res.ExitCode == 0 {
		return nil, fmt.Errorf("%w: %s", ErrBranchExists, branch)
	}
	if _, err := g.git(ctx, g.root, "worktree", "add", "-b", branch, canon, startPoint); err != nil {
		return nil, err
	}
	head, err := g.RevParse(ctx, branch)
	if err != nil {
		return nil, err
	}
	return &Worktree{Path: canon, Branch: branch, Head: head}, nil
}

// Confirmation proves the caller obtained explicit human approval for
// a destructive operation (PRD §15). The zero value fails closed;
// only ExplicitConfirmation constructs a valid token. gitops never
// prompts — collecting the approval is the CLI and run engine's job.
type Confirmation struct{ ok bool }

// ExplicitConfirmation returns the token that authorizes one
// destructive gitops call. Call it only after a human approved the
// specific deletion (PRD §15: cleanup or abandonment deletion
// requires explicit confirmation).
func ExplicitConfirmation() Confirmation { return Confirmation{ok: true} }

// RemoveWorktree removes the registered worktree at path and prunes
// stale metadata (PRD §12 step 19). Fail-closed gates, in order: the
// Confirmation token must be valid (ErrNotConfirmed); the canonical
// path must match a registered non-primary worktree
// (ErrUnknownWorktree — never delete an arbitrary directory); and the
// worktree must be clean including untracked files (ErrNotClean —
// blocked work is preserved, PRD §15). Never uses --force.
func (g *Git) RemoveWorktree(ctx context.Context, path string, c Confirmation) error {
	if !c.ok {
		return fmt.Errorf("%w: remove worktree %s", ErrNotConfirmed, path)
	}
	canon, err := paths.Canonical(path)
	if err != nil {
		return err
	}
	worktrees, err := g.ListWorktrees(ctx)
	if err != nil {
		return err
	}
	found := false
	for _, wt := range worktrees {
		same, err := samePath(wt.Path, canon)
		if err != nil {
			return err
		}
		if !same {
			continue
		}
		if wt.Primary {
			return fmt.Errorf("%s is the primary checkout; refusing to remove it", canon)
		}
		found = true
		break
	}
	if !found {
		return fmt.Errorf("%w: %s", ErrUnknownWorktree, canon)
	}
	if err := g.RequireClean(ctx, canon); err != nil {
		return err
	}
	if _, err := g.git(ctx, g.root, "worktree", "remove", canon); err != nil {
		return err
	}
	return g.PruneWorktrees(ctx)
}

// PruneWorktrees removes stale worktree bookkeeping whose directories
// are already gone. It deletes no working files, so it needs no
// Confirmation.
func (g *Git) PruneWorktrees(ctx context.Context) error {
	_, err := g.git(ctx, g.root, "worktree", "prune")
	return err
}
