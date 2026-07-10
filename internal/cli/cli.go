// Package cli implements the orch command surface (PRD §22). Commands
// without an implementation yet fail closed with an explicit error.
package cli

import (
	"fmt"
	"io"
	"os/exec"
)

// Exit codes returned by Run.
const (
	ExitOK    = 0
	ExitError = 1
	ExitUsage = 2
)

// Env carries the invocation context so commands stay testable.
type Env struct {
	// RepoRoot is the directory containing .orchestrator/.
	RepoRoot string
	Stdout   io.Writer
	Stderr   io.Writer
	// LookPath resolves an executable name; defaults to exec.LookPath.
	LookPath func(string) (string, error)
}

type command struct {
	name    string
	summary string
	run     func(Env) error
}

// commands lists the full PRD §22 surface in documentation order, so
// `orch help` always shows every logical command even before it works.
func commands() []command {
	return []command{
		{"init", "Interview and bootstrap this repository (not implemented)", notImplemented("init")},
		{"status", "Show mode and configuration summary", runStatus},
		{"doctor", "Check environment and configuration health", runDoctor},
		{"configure", "Change committed configuration (not implemented)", notImplemented("configure")},
		{"configure-local", "Change machine-local overrides (not implemented)", notImplemented("configure-local")},
		{"resume", "Resume an interrupted Delivery run (not implemented)", notImplemented("resume")},
		{"abort", "Stop dispatch and return to Assist (not implemented)", notImplemented("abort")},
		{"metrics", "Show local metrics (not implemented)", notImplemented("metrics")},
	}
}

func notImplemented(name string) func(Env) error {
	return func(Env) error {
		return fmt.Errorf("%s is not implemented yet", name)
	}
}

// Run dispatches args (without the program name) and returns a process
// exit code.
func Run(args []string, env Env) int {
	if env.LookPath == nil {
		env.LookPath = exec.LookPath
	}
	if len(args) == 0 {
		usage(env.Stderr)
		return ExitUsage
	}
	if args[0] == "help" || args[0] == "-h" || args[0] == "--help" {
		usage(env.Stdout)
		return ExitOK
	}
	for _, c := range commands() {
		if c.name != args[0] {
			continue
		}
		if len(args) > 1 {
			fmt.Fprintf(env.Stderr, "orch %s: unexpected argument %q\n", c.name, args[1])
			return ExitUsage
		}
		if err := c.run(env); err != nil {
			fmt.Fprintf(env.Stderr, "orch %s: %v\n", c.name, err)
			return ExitError
		}
		return ExitOK
	}
	fmt.Fprintf(env.Stderr, "orch: unknown command %q\n\n", args[0])
	usage(env.Stderr)
	return ExitUsage
}

func usage(w io.Writer) {
	fmt.Fprintln(w, "usage: orch <command>")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "commands:")
	for _, c := range commands() {
		fmt.Fprintf(w, "  %-16s %s\n", c.name, c.summary)
	}
}
