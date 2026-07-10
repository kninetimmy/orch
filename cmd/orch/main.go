// Command orch is the shared-core CLI for the Orch development
// orchestrator (see ORCH-PRD.md). Host adapters invoke this binary;
// it never runs interactive dialogs itself.
package main

import (
	"fmt"
	"os"

	"github.com/kninetimmy/orch/internal/cli"
)

func main() {
	root, err := os.Getwd()
	if err != nil {
		fmt.Fprintf(os.Stderr, "orch: %v\n", err)
		os.Exit(cli.ExitError)
	}
	os.Exit(cli.Run(os.Args[1:], cli.Env{
		RepoRoot: root,
		Stdout:   os.Stdout,
		Stderr:   os.Stderr,
	}))
}
