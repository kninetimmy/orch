package gitops

import (
	"context"
	"fmt"
	"slices"
	"strings"
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
