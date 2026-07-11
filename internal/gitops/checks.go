package gitops

import (
	"context"
	"fmt"
	"path/filepath"
	"slices"
	"strings"

	"github.com/kninetimmy/orch/internal/paths"
)

// RequireClean returns nil only when dir's working tree has no
// modified, staged, or untracked files (PRD §5 Delivery
// prerequisites; §15 preservation checks). dir "" means the primary
// checkout. The error names the offending paths so the operator knows
// what to commit or stash.
func (g *Git) RequireClean(ctx context.Context, dir string) error {
	if dir == "" {
		dir = g.root
	}
	out, err := g.git(ctx, dir, "status", "--porcelain=v1", "--untracked-files=all")
	if err != nil {
		return err
	}
	if out == "" {
		return nil
	}
	lines := strings.Split(out, "\n")
	shown := lines
	const maxShown = 5
	if len(shown) > maxShown {
		shown = shown[:maxShown]
	}
	suffix := ""
	if len(lines) > maxShown {
		suffix = fmt.Sprintf(" (and %d more)", len(lines)-maxShown)
	}
	return fmt.Errorf("%w in %s: %s%s; commit or stash before continuing", ErrNotClean, dir, strings.Join(shown, ", "), suffix)
}

// CurrentBranch returns the branch checked out in the primary
// checkout. A detached HEAD wraps ErrDetachedHead: Delivery never
// operates detached, so callers fail closed.
func (g *Git) CurrentBranch(ctx context.Context) (string, error) {
	res, err := g.run(ctx, g.root, "symbolic-ref", "--short", "HEAD")
	if err != nil {
		return "", err
	}
	if res.ExitCode != 0 {
		return "", fmt.Errorf("%w in %s: %s", ErrDetachedHead, g.root, strings.TrimSpace(res.Stderr))
	}
	return strings.TrimSpace(res.Stdout), nil
}

// RequireNotOn enforces PRD §15's "the active branch must not be
// main": the caller names the protected branches (that list is
// policy, owned by the run engine), gitops enforces it mechanically.
func (g *Git) RequireNotOn(ctx context.Context, protected ...string) error {
	branch, err := g.CurrentBranch(ctx)
	if err != nil {
		return err
	}
	if slices.Contains(protected, branch) {
		return fmt.Errorf("%w: %s", ErrProtectedBranch, branch)
	}
	return nil
}

// RevParse resolves ref to a full commit hash, for audit manifests
// (PRD §13) and verification.
func (g *Git) RevParse(ctx context.Context, ref string) (string, error) {
	return g.git(ctx, g.root, "rev-parse", "--verify", ref+"^{commit}")
}

// RequireIgnored returns nil only when path is git-ignored relative to
// the primary checkout (F1: an inside-primary worktree relaxation is
// only safe because an ignored path never appears in
// `status --porcelain`, so RequireClean and isolation guarantees still
// hold). It works for paths that do not yet exist — `git check-ignore`
// matches by pattern, not by filesystem presence. path may be absolute
// or relative; either way it is canonicalized and expressed relative
// to root before the check. A path outside root is an error: this
// check only makes sense for a candidate inside-primary location.
//
// The query is always sent to git with a trailing slash: path is
// always a directory in every caller of this method (a worktree or
// its container), and a directory-only .gitignore pattern (the common
// case, e.g. "foo/") only matches a bare, non-existent path when git
// is told it is a directory this way — without it, `check-ignore`
// silently refuses to match.
func (g *Git) RequireIgnored(ctx context.Context, path string) error {
	canon, err := paths.Canonical(path)
	if err != nil {
		return err
	}
	rel, err := filepath.Rel(g.root, canon)
	if err != nil {
		return fmt.Errorf("compute path for %s relative to %s: %w", canon, g.root, err)
	}
	query := filepath.ToSlash(rel) + "/"
	res, err := g.run(ctx, g.root, "check-ignore", "-q", "--", query)
	if err != nil {
		return err
	}
	switch res.ExitCode {
	case 0:
		return nil
	case 1:
		return fmt.Errorf("%w: %s; add a line like \"%s/\" to .gitignore", ErrNotIgnored, canon, filepath.ToSlash(rel))
	default:
		return fmt.Errorf("git check-ignore in %s exited %d: %s", g.root, res.ExitCode, strings.TrimSpace(res.Stderr))
	}
}
