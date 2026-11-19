package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/kninetimmy/orch/internal/grant"
	"github.com/kninetimmy/orch/internal/lockfile"
	"github.com/kninetimmy/orch/internal/run"
)

// runUsage names the adapter plumbing surface and explicit recovery requests.
const runUsage = `orch run: usage: orch run plan|activate|dispatch|repair|pr-open|review-worktree|review|escalate|ci|merge-report|merge|block|resolve-block|abandon|cleanup|complete (JSON document on stdin) | orch run status|next --json | orch run readiness --issue <number> --json | orch run help
repair: explicit scoped integration authorization for recovered pre-PR work or a pinned open PR; see docs/decision-block-recovery.md for schema 1 and the renewed verification/review/merge gates
review: schema 4 supports non-approving needs-evidence / unverified criteria and identifiable same-head execution_evidence; collect outside the OpenCode reviewer, then obtain fresh routed review (docs/review-evidence.md). This grants no execution or merge authority.
manual CI: after fresh human confirmation, merge-report schema 1 accepts:
{"schema_version":1,"issue_number":123,"ci_exception":{"run_id":"<current run>","issue_number":123,"pr_number":124,"head_oid":"<full independently reviewed head>","approved_by":"<human>","approved_at":"<actual RFC3339 decision time>","reason":"<why incomplete CI is accepted>","statement":"accept-incomplete-ci","repair_number":0}}
Then obtain separate fresh approve-merge confirmation for that PR/head and send merge schema 1 with its ordinary approval plus the explicit ci_exception again. Receipts are audit, not reusable permission; grants and probe failures cannot use this exception.
already merged: merge schema 1 instead accepts merged_acknowledgment with the same decision fields and statement "acknowledge-already-merged-pr", dated AFTER the merge; omit approval and ci_exception. This only acknowledges an independently approved reviewed head already proven MERGED, including its specific out-of-band-merge operational block; it never merges or supplies prior approval.
Both paths retain actual CI evidence and require separate cleanup and complete. Exact requests, retry/repair rules and limitations: docs/manual-ci-exceptions.md`

// runVerbs maps each document-taking verb to its run-engine entry point.
// Every one reads a JSON request on stdin and writes a JSON result on
// stdout; status, next and readiness are separate because none may read stdin.
var runVerbs = map[string]func(context.Context, run.Env, []byte) (any, error){
	"plan":            adaptRunVerb(run.Plan),
	"activate":        adaptRunVerb(run.Activate),
	"dispatch":        adaptRunVerb(run.Dispatch),
	"repair":          adaptRunVerb(run.Repair),
	"pr-open":         adaptRunVerb(run.PROpen),
	"review-worktree": adaptRunVerb(run.ReviewWorktree),
	"review":          adaptRunVerb(run.Review),
	"escalate":        adaptRunVerb(run.Escalate),
	"ci":              adaptRunVerb(run.CI),
	"merge-report":    adaptRunVerb(run.MergeReport),
	"merge":           adaptRunVerb(run.Merge),
	"block":           adaptRunVerb(run.Block),
	"resolve-block":   adaptRunVerb(run.ResolveBlock),
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
// `orch run <verb>` (JSON stdin/stdout) plus the argv-only inspections.
// Host adapters invoke mutating verbs after their native dialogs. The
// read-only inspections are also available directly to people and clients.
func runRunVerb(env Env, args []string) error {
	if len(args) == 1 && (args[0] == "help" || args[0] == "--help" || args[0] == "-h") {
		_, err := fmt.Fprintln(env.Stdout, runUsage)
		return err
	}
	// Before next, status alone bypassed stdin; item 2 added next. All three
	// inspections now bypass stdin and mutation serialization, even on a console.
	if len(args) == 2 && (args[0] == "status" || args[0] == "next") && args[1] == "--json" {
		return emitRunResult(env, args[0], func(ctx context.Context, runEnv run.Env) (any, error) {
			if args[0] == "next" {
				return run.Next(ctx, runEnv)
			}
			root, err := runStatusRoot(ctx, env)
			if err != nil {
				return nil, fmt.Errorf("status checkout: %w", err)
			}
			// This root is observation-only; all other verbs keep env.RepoRoot.
			runEnv.RepoRoot = root
			return run.Status(ctx, runEnv)
		})
	}
	if len(args) == 4 && args[0] == "readiness" && args[1] == "--issue" && args[3] == "--json" {
		number, err := strconv.Atoi(args[2])
		if err != nil || number <= 0 {
			return usageError("orch run: usage: orch run readiness --issue <positive integer> --json")
		}
		return emitRunResult(env, "readiness", func(ctx context.Context, runEnv run.Env) (any, error) {
			return run.Readiness(ctx, runEnv, number)
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
	// Plan is the only document-taking read-only run verb. The inspections
	// were handled above; every other runVerbs entry is a mutating Delivery
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
// resume, abort, and metrics record use this same boundary.
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
	runEnv := run.Env{RepoRoot: env.RepoRoot, Runner: env.Runner, Now: time.Now, ClaudeSession: os.Getenv(grant.SessionEnv)}
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
