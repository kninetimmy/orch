package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/kninetimmy/orch/internal/run"
)

// runRunVerb dispatches the adapter-facing plumbing verbs (PRD §22):
// `orch run plan|activate|status --json`. Host adapters shell out to
// it after their own native dialogs, exchanging JSON documents on
// stdin/stdout; it is never invoked by a human directly.
func runRunVerb(env Env, args []string) error {
	var verb string
	switch {
	case len(args) == 1 && args[0] == "plan":
		verb = "plan"
	case len(args) == 1 && args[0] == "activate":
		verb = "activate"
	case len(args) == 2 && args[0] == "status" && args[1] == "--json":
		verb = "status"
	default:
		return usageError("orch run: usage: orch run plan|activate|status --json")
	}

	// Only the document-taking verbs read stdin: status must not block
	// on a console stdin that never reaches EOF.
	var input []byte
	if verb == "plan" || verb == "activate" {
		stdin := env.Stdin
		if stdin == nil {
			stdin = strings.NewReader("")
		}
		var err error
		input, err = io.ReadAll(stdin)
		if err != nil {
			return fmt.Errorf("read stdin: %w", err)
		}
	}

	runEnv := run.Env{RepoRoot: env.RepoRoot, Runner: env.Runner, Now: time.Now}
	ctx := context.Background()

	var out any
	var err error
	switch verb {
	case "plan":
		out, err = run.Plan(ctx, runEnv, input)
	case "activate":
		out, err = run.Activate(ctx, runEnv, input)
	case "status":
		out, err = run.Status(ctx, runEnv)
	}
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
