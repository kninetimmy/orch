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

// requireIgnoredProbe is the basename RequireIgnored appends to the
// directory it is checking before asking git about it — see the
// function doc for why a bare directory query is unsafe. It is
// deliberately not dot-prefixed: a leading dot risks being swallowed
// by an unrelated ".*" convention some repositories use to ignore all
// hidden files, which would report the probe "ignored" for a reason
// that says nothing about whether the directory itself is covered.
const requireIgnoredProbe = "orch-check-ignore-probe"

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
// The query sent to git is not path itself but a synthetic child of
// it — path plus "/" plus the fixed probe basename requireIgnoredProbe
// — rather than path alone with a bare trailing slash. Two properties
// are both needed, and a trailing slash on path alone provides only
// the first while silently breaking the second:
//
//   - Every caller passes a directory (a worktree or its container).
//     A directory-only .gitignore pattern (the common case, e.g.
//     "foo/") does not match a path that does not yet exist on disk
//     unless git is told the path is a directory. Querying a child
//     path inside the directory accomplishes that without needing a
//     trailing slash on path itself, and the match still holds when
//     the directory is nested several levels deep under the pattern
//     (verified against real git).
//   - A bare trailing slash on path (e.g. "foo/") produces a query
//     whose final path component is empty. Git does not always skip a
//     blank .gitignore line before turning it into a pattern: a line
//     that is empty except for a trailing CRLF, and a line containing
//     only whitespace under a plain LF file, both trim down to an
//     empty pattern rather than being dropped — on any OS, since
//     neither case depends on how the file itself is line-ended. An
//     empty pattern matches an empty basename, so a trailing-slash-only
//     query comes back "ignored" against such a .gitignore regardless
//     of whether path is covered by any real pattern (verified against
//     real git 2.53). Appending a non-empty probe basename closes this
//     off: an empty pattern can never match a non-empty basename.
func (g *Git) RequireIgnored(ctx context.Context, path string) error {
	canon, err := paths.Canonical(path)
	if err != nil {
		return err
	}
	rel, err := filepath.Rel(g.root, canon)
	if err != nil {
		return fmt.Errorf("compute path for %s relative to %s: %w", canon, g.root, err)
	}
	query := filepath.ToSlash(rel) + "/" + requireIgnoredProbe
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
