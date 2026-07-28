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

// prepareWorktreePath canonicalizes path and applies the two placement
// pre-checks every worktree creation shares, in order: path must not
// already exist (never clobber), and a path inside the primary
// checkout must be git-ignored (F1: worktrees must be isolated from
// the primary checkout's tracked tree, PRD §5 — an ignored
// inside-primary path is safe because it never appears in
// `status --porcelain`, so RequireClean and isolation guarantees still
// hold). It touches nothing; the caller creates the worktree.
func (g *Git) prepareWorktreePath(ctx context.Context, path string) (string, error) {
	canon, err := paths.Canonical(path)
	if err != nil {
		return "", err
	}
	switch _, err := os.Lstat(canon); {
	case err == nil:
		return "", fmt.Errorf("worktree path %s already exists; refusing to clobber it", canon)
	case !errors.Is(err, fs.ErrNotExist):
		return "", fmt.Errorf("check worktree path %s: %w", canon, err)
	}
	inside, err := paths.Inside(g.root, canon)
	if err != nil {
		return "", err
	}
	if inside {
		if err := g.RequireIgnored(ctx, canon); err != nil {
			return "", fmt.Errorf("worktree path %s is inside the primary checkout and not git-ignored: %w", canon, err)
		}
	}
	return canon, nil
}

// AddWorktree creates branch at startPoint together with a new
// worktree at path (PRD §12 step 7: one branch/worktree per issue).
// It fails closed before touching git when path already exists (never
// clobber), when the branch already exists (ErrBranchExists), or when
// path lies inside the primary checkout and is not git-ignored
// (prepareWorktreePath). Never uses --force.
func (g *Git) AddWorktree(ctx context.Context, path, branch, startPoint string) (*Worktree, error) {
	canon, err := g.prepareWorktreePath(ctx, path)
	if err != nil {
		return nil, err
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

// AddDetachedWorktree checks commit out into a new worktree at path
// with a detached HEAD: it creates no branch and checks none out, so
// the checkout holds no ref that another worktree could contend for or
// move under it. It runs prepareWorktreePath's clobber and
// inside-primary checks first and never uses --force, exactly as
// AddWorktree does; only the branch handling differs.
//
// The returned Worktree carries the commit HEAD actually resolved to,
// read back from the new checkout rather than echoed from commit, so
// an abbreviated commit-ish is reported in full and the caller can
// report what it got rather than what it asked for.
func (g *Git) AddDetachedWorktree(ctx context.Context, path, commit string) (*Worktree, error) {
	canon, err := g.prepareWorktreePath(ctx, path)
	if err != nil {
		return nil, err
	}
	if _, err := g.git(ctx, g.root, "worktree", "add", "--detach", canon, commit); err != nil {
		return nil, err
	}
	head, err := g.git(ctx, canon, "rev-parse", "--verify", "HEAD^{commit}")
	if err != nil {
		return nil, err
	}
	return &Worktree{Path: canon, Head: head, Detached: true}, nil
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

// findWorktree returns the registered worktree whose path is the
// canonical path canon, or ErrUnknownWorktree when git knows no
// worktree there — the check that keeps every removal path from
// deleting an arbitrary directory. The primary checkout matches like
// any other entry; refusing it belongs to the caller, and every
// removal path does refuse it.
func (g *Git) findWorktree(ctx context.Context, canon string) (*Worktree, error) {
	worktrees, err := g.ListWorktrees(ctx)
	if err != nil {
		return nil, err
	}
	for i := range worktrees {
		same, err := samePath(worktrees[i].Path, canon)
		if err != nil {
			return nil, err
		}
		if same {
			return &worktrees[i], nil
		}
	}
	return nil, fmt.Errorf("%w: %s", ErrUnknownWorktree, canon)
}

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
	wt, err := g.findWorktree(ctx, canon)
	if err != nil {
		return err
	}
	if wt.Primary {
		return fmt.Errorf("%s is the primary checkout; refusing to remove it", canon)
	}
	if err := g.RequireClean(ctx, canon); err != nil {
		return err
	}
	if _, err := g.git(ctx, g.root, "worktree", "remove", canon); err != nil {
		return err
	}
	return g.PruneWorktrees(ctx)
}

// RemoveDetachedWorktree removes the registered detached worktree at
// path, discarding whatever modified or untracked files it holds, and
// prunes stale metadata. Fail-closed gates, in order: the canonical
// path must match a registered worktree (ErrUnknownWorktree — the same
// "never delete an arbitrary directory" rule RemoveWorktree obeys); it
// must not be the primary checkout, which is refused even when its own
// HEAD happens to be detached; and it must be detached, so a worktree
// holding a branch is refused by name.
//
// It takes no Confirmation, for the reason WithBaseWorktree's forced
// removal takes none: what PRD §15 protects from a forced removal is
// committed work, which lives on a branch, and the detached gate above
// makes "holds no branch" a checked precondition rather than a promise
// — a branch worktree can never reach the removal below. What is lost
// is uncommitted scratch inside a disposable checkout, which is what
// disposable means. A caller removing a worktree that may hold work
// must use RemoveWorktree, which is confirmed and refuses a dirty tree.
func (g *Git) RemoveDetachedWorktree(ctx context.Context, path string) error {
	canon, err := paths.Canonical(path)
	if err != nil {
		return err
	}
	wt, err := g.findWorktree(ctx, canon)
	if err != nil {
		return err
	}
	if wt.Primary {
		return fmt.Errorf("%s is the primary checkout; refusing to remove it", canon)
	}
	if !wt.Detached {
		return fmt.Errorf("%s is checked out on branch %s, not detached; refusing to force-remove a worktree holding a branch", canon, wt.Branch)
	}
	if _, err := g.git(ctx, g.root, "worktree", "remove", "--force", canon); err != nil {
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
