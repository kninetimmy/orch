package cli

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/kninetimmy/orch/internal/evalplan"
)

func runEval(env Env, args []string) error {
	const usage = "usage: orch eval preview --plan FILE [--json]"
	if len(args) == 0 {
		return usageError(usage)
	}
	if args[0] != "preview" {
		return usageError(fmt.Sprintf("orch eval: unsupported verb %q; only preview is implemented; %s", args[0], usage))
	}
	var file string
	jsonOutput := false
	for i := 1; i < len(args); i++ {
		switch args[i] {
		case "--plan":
			if file != "" || i+1 == len(args) || args[i+1] == "" || args[i+1][0] == '-' {
				return usageError(usage)
			}
			i++
			file = args[i]
		case "--json":
			if jsonOutput {
				return usageError(usage)
			}
			jsonOutput = true
		default:
			return usageError(fmt.Sprintf("orch eval preview: unexpected argument %q; %s", args[i], usage))
		}
	}
	if file == "" {
		return usageError(usage)
	}
	record, err := evalplan.Preview(context.Background(), env.RepoRoot, file, env.Runner)
	if err != nil {
		return err
	}
	if jsonOutput {
		encoder := json.NewEncoder(env.Stdout)
		encoder.SetIndent("", "  ")
		return encoder.Encode(record)
	}
	return evalplan.WriteText(env.Stdout, record)
}
