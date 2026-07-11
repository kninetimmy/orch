// Package ghops executes the GitHub mechanics of the Delivery
// workflow (PRD §12 steps 6, 9, 13-17) through the authenticated gh
// CLI (PRD §5), via an injected execx.Runner: repository resolution,
// the PRD §13 label taxonomy, issue lifecycle, and PR open, status,
// required-CI state, and merge.
//
// The package is policy-free like gitops: callers supply issue
// titles, bodies, branch names, and label values — naming policy
// belongs to the run engine, and issue and PR bodies (the PRD §13
// audit record) are opaque strings here. Every operation fails
// closed: any gh error propagates, destructive operations require an
// explicit Confirmation (PRD §15), and merging is only ever invoked
// after the human merge gate (PRD §8) — ghops itself never decides to
// merge.
//
// Operations that pre-check and then act (for example
// EnsureLabelTaxonomy) are not atomic; the run engine serializes
// potentially conflicting writes (PRD §14), so the gap is not raced
// in practice.
package ghops

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"github.com/kninetimmy/orch/internal/execx"
	"github.com/kninetimmy/orch/internal/paths"
)

// ghEnv is applied to every gh invocation. GH_PROMPT_DISABLED makes
// gh error instead of prompting (fail closed, never hang); the update
// notifier and spinner are silenced so captured output holds only
// command results; NO_COLOR and CLICOLOR keep parsed output free of
// ANSI escapes.
var ghEnv = []string{
	"GH_PROMPT_DISABLED=1",
	"GH_NO_UPDATE_NOTIFIER=1",
	"GH_SPINNER_DISABLED=1",
	"NO_COLOR=1",
	"CLICOLOR=0",
}

// GH executes GitHub operations against one repository through an
// injected execx.Runner.
type GH struct {
	r    execx.Runner
	root string
}

// Open binds to the repository whose primary checkout root is
// repoRoot and verifies gh authentication with a `gh auth status`
// probe: a non-zero exit means no usable credentials and Open fails
// closed with ErrNotAuthenticated (PRD §5). The exit code is the
// structured signal — gh's human-readable status text is never
// parsed. Repository association is checked separately by Repo,
// because Assist may operate without a GitHub remote (PRD §5); the
// Delivery preflight composes Open and Repo.
func Open(ctx context.Context, r execx.Runner, repoRoot string) (*GH, error) {
	root, err := paths.Canonical(repoRoot)
	if err != nil {
		return nil, err
	}
	g := &GH{r: r, root: root}
	res, err := g.run(ctx, nil, "auth", "status")
	if err != nil {
		return nil, err
	}
	if res.ExitCode != 0 {
		return nil, fmt.Errorf("%w (gh auth status exited %d)", ErrNotAuthenticated, res.ExitCode)
	}
	return g, nil
}

// Root returns the canonical primary-checkout root.
func (g *GH) Root() string { return g.root }

// run executes gh with the fail-closed environment and returns the
// raw result; callers that treat non-zero exit as data use this.
func (g *GH) run(ctx context.Context, stdin io.Reader, args ...string) (execx.Result, error) {
	return g.r.Run(ctx, execx.Cmd{Name: "gh", Args: args, Dir: g.root, Env: ghEnv, Stdin: stdin})
}

// gh is run plus the common interpretation: a non-zero exit becomes
// an error naming the subcommand, directory, and gh's stderr; success
// returns trimmed stdout.
func (g *GH) gh(ctx context.Context, args ...string) (string, error) {
	return g.ghStdin(ctx, nil, args...)
}

// ghStdin is gh with standard input attached; issue and PR bodies
// travel this way (--body-file -) because they can exceed
// command-line length limits and must never be shell-quoted.
func (g *GH) ghStdin(ctx context.Context, stdin io.Reader, args ...string) (string, error) {
	res, err := g.run(ctx, stdin, args...)
	if err != nil {
		return "", err
	}
	if res.ExitCode != 0 {
		return "", fmt.Errorf("gh %s in %s exited %d: %s", args[0], g.root, res.ExitCode, strings.TrimSpace(res.Stderr))
	}
	return strings.TrimSpace(res.Stdout), nil
}

// ghJSON runs gh and unmarshals its stdout into out; the explicit
// --json field list in args is what pins the response shape.
func (g *GH) ghJSON(ctx context.Context, out any, args ...string) error {
	stdout, err := g.gh(ctx, args...)
	if err != nil {
		return err
	}
	if err := json.Unmarshal([]byte(stdout), out); err != nil {
		return fmt.Errorf("gh %s in %s returned unparsable JSON: %w", args[0], g.root, err)
	}
	return nil
}

// Confirmation is a typed approval token for destructive operations.
// The zero value fails closed; only ExplicitConfirmation produces a
// confirming token. ghops never prompts — collecting the human's
// approval is the caller's job (PRD §8, §15).
type Confirmation struct{ ok bool }

// ExplicitConfirmation returns a token stating the caller obtained
// explicit approval for a destructive operation.
func ExplicitConfirmation() Confirmation { return Confirmation{ok: true} }
