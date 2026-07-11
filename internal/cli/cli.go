// Package cli implements the orch command surface (PRD §22). Commands
// without an implementation yet fail closed with an explicit error.
package cli

import (
	"errors"
	"fmt"
	"io"
	"os/exec"
	"strings"

	"github.com/kninetimmy/orch/internal/execx"
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
	// Stdin is the process input; nil is treated as empty. `orch run`
	// verbs read their JSON document from it.
	Stdin io.Reader
	// LookPath resolves an executable name; defaults to exec.LookPath.
	LookPath func(string) (string, error)
	// Runner executes external commands (git, gh); defaults to
	// execx.Local resolving through LookPath.
	Runner execx.Runner
}

// usageError marks a user-facing usage mistake, as opposed to an
// operational failure: Run maps it to ExitUsage instead of ExitError.
type usageError string

func (e usageError) Error() string { return string(e) }

type command struct {
	name    string
	summary string
	run     func(Env, []string) error
}

// commands lists the full PRD §22 surface in documentation order, so
// `orch help` always shows every logical command even before it works.
// `run` is listed last and labeled plumbing (F2): it is adapter-facing
// JSON stdin/stdout, not a command a human runs directly.
func commands() []command {
	return []command{
		{"init", "Interview and bootstrap this repository (not implemented)", noArgs("init", notImplemented("init"))},
		{"status", "Show mode and configuration summary", noArgs("status", runStatus)},
		{"doctor", "Check environment and configuration health", noArgs("doctor", runDoctor)},
		{"configure", "Change committed configuration (not implemented)", noArgs("configure", notImplemented("configure"))},
		{"configure-local", "Change machine-local overrides (not implemented)", noArgs("configure-local", notImplemented("configure-local"))},
		{"resume", "Resume an interrupted Delivery run (not implemented)", noArgs("resume", notImplemented("resume"))},
		{"abort", "Stop dispatch and return to Assist", noArgs("abort", runAbort)},
		{"metrics", "Show local metrics (not implemented)", noArgs("metrics", notImplemented("metrics"))},
		{"run", "Adapter plumbing: Delivery run verbs (JSON stdin/stdout; not a human command)", runRunVerb},
	}
}

func notImplemented(name string) func(Env) error {
	return func(Env) error {
		return fmt.Errorf("%s is not implemented yet", name)
	}
}

// noArgs adapts a no-trailing-argument command function to the
// dispatcher's func(Env, []string) error shape, preserving the
// existing trailing-argument rejection for every command that isn't
// adapter plumbing.
func noArgs(name string, fn func(Env) error) func(Env, []string) error {
	return func(env Env, args []string) error {
		if len(args) > 0 {
			return usageError(fmt.Sprintf("orch %s: unexpected argument %q", name, args[0]))
		}
		return fn(env)
	}
}

// Run dispatches args (without the program name) and returns a process
// exit code.
func Run(args []string, env Env) int {
	if env.LookPath == nil {
		env.LookPath = exec.LookPath
	}
	if env.Runner == nil {
		env.Runner = execx.Local{LookPath: env.LookPath}
	}
	if env.Stdin == nil {
		env.Stdin = strings.NewReader("")
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
		err := c.run(env, args[1:])
		if err == nil {
			return ExitOK
		}
		var ue usageError
		if errors.As(err, &ue) {
			fmt.Fprintf(env.Stderr, "%s\n", err)
			return ExitUsage
		}
		fmt.Fprintf(env.Stderr, "orch %s: %v\n", c.name, err)
		return ExitError
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
