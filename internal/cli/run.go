package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/kninetimmy/orch/internal/lockfile"
	"github.com/kninetimmy/orch/internal/run"
)

// runUsage is the one-line usage for the adapter plumbing surface.
const runUsage = "orch run: usage: orch run plan|activate|dispatch|pr-open|review-worktree|review|escalate|ci|merge-report|merge|block|abandon|cleanup|complete (JSON document on stdin) | orch run status --json"

// runVerbs maps each document-taking verb to its run-engine entry point.
// Every one reads a JSON request on stdin and writes a JSON result on
// stdout; status is handled separately because it must never read stdin.
var runVerbs = map[string]func(context.Context, run.Env, []byte) (any, error){
	"plan":            adaptRunVerb(run.Plan),
	"activate":        adaptRunVerb(run.Activate),
	"dispatch":        adaptRunVerb(run.Dispatch),
	"pr-open":         adaptRunVerb(run.PROpen),
	"review-worktree": adaptRunVerb(run.ReviewWorktree),
	"review":          adaptRunVerb(run.Review),
	"escalate":        adaptRunVerb(run.Escalate),
	"ci":              adaptRunVerb(run.CI),
	"merge-report":    adaptRunVerb(run.MergeReport),
	"merge":           adaptRunVerb(run.Merge),
	"block":           adaptRunVerb(run.Block),
	"abandon":         adaptRunVerb(run.Abandon),
	"cleanup":         adaptRunVerb(run.Cleanup),
	"complete":        adaptRunVerb(run.Complete),
}

// adaptRunVerb erases a verb's concrete result type so every document
// verb shares one dispatch shape; a nil result on error keeps the JSON
// encoder off a typed-nil pointer.
func adaptRunVerb[T any](fn func(context.Context, run.Env, []byte) (T, error)) func(context.Context, run.Env, []byte) (any, error) {
	return func(ctx context.Context, env run.Env, input []byte) (any, error) {
		out, err := fn(ctx, env, input)
		if err != nil {
			return nil, err
		}
		return out, nil
	}
}

// runRunVerb dispatches the adapter-facing plumbing verbs (PRD §22):
// `orch run <verb>` (JSON stdin/stdout) plus `orch run status --json`.
// Host adapters shell out to it after their own native dialogs; it is
// never invoked by a human directly.
func runRunVerb(env Env, args []string) error {
	// status is the one verb that must not block on a console stdin that
	// never reaches EOF, so it is matched first and never reads input.
	if len(args) == 2 && args[0] == "status" && args[1] == "--json" {
		return emitRunResult(env, "status", func(ctx context.Context, runEnv run.Env) (any, error) {
			return run.Status(ctx, runEnv)
		})
	}
	if len(args) != 1 {
		return usageError(runUsage)
	}
	handler, ok := runVerbs[args[0]]
	if !ok {
		return usageError(runUsage)
	}

	stdin := env.Stdin
	if stdin == nil {
		stdin = strings.NewReader("")
	}
	input, err := io.ReadAll(stdin)
	if err != nil {
		return fmt.Errorf("read stdin: %w", err)
	}

	invoke := func() error {
		return emitRunResult(env, args[0], func(ctx context.Context, runEnv run.Env) (any, error) {
			return handler(ctx, runEnv, input)
		})
	}
	// Plan is the only document-taking read-only run verb. Status was
	// handled above; every other runVerbs entry is a mutating Delivery
	// command and holds one repository serializer through its final state
	// and metrics write. This is a command-class rule, not a Dispatch-only
	// restriction.
	if args[0] == "plan" {
		return invoke()
	}
	return withDeliveryMutation(env.RepoRoot, invoke)
}

// withDeliveryMutation holds the process-scoped, cross-process serializer for
// one complete mutating command invocation. Besides runRunVerb's mutators,
// resume and abort use this same boundary.
func withDeliveryMutation(repoRoot string, fn func() error) (err error) {
	lock, err := lockfile.AcquireMutation(repoRoot)
	if err != nil {
		return err
	}
	defer func() {
		if releaseErr := lock.Release(); err == nil && releaseErr != nil {
			err = releaseErr
		}
	}()
	return fn()
}

// emitRunResult runs one verb and writes its JSON result to stdout.
func emitRunResult(env Env, verb string, fn func(context.Context, run.Env) (any, error)) error {
	runEnv := run.Env{RepoRoot: env.RepoRoot, Runner: env.Runner, Now: time.Now}
	out, err := fn(context.Background(), runEnv)
	if err != nil {
		return err
	}
	data, err := json.MarshalIndent(out, "", "  ")
	if err != nil {
		return fmt.Errorf("encode %s result: %w", verb, err)
	}
	_, err = fmt.Fprintf(env.Stdout, "%s\n", data)
	return err
}
