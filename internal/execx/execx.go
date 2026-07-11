// Package execx defines the injectable external-command runner the
// orchestration core uses for git and gh (PRD §5, §12). Commands are
// always argument vectors — never shell strings — so external input
// can never be shell-interpreted. A non-zero exit is data in Result,
// not an error: callers that need exit codes as information (PRD §16
// reproduce-on-base) read them directly, and callers for whom
// non-zero means failure convert it themselves.
package execx

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"time"
)

// Cmd describes one external command invocation.
type Cmd struct {
	// Name is the executable name; the runner resolves it on PATH.
	Name string
	// Args is the argument vector. It is passed verbatim to the
	// process and never shell-interpreted.
	Args []string
	// Dir is the working directory. It is required: an implicit
	// inherited cwd would make command behavior depend on ambient
	// process state, so an empty Dir is an error (fail closed).
	Dir string
	// Env holds KEY=VALUE pairs appended to os.Environ(). Later
	// entries win, so callers can override inherited variables.
	// nil adds nothing.
	Env []string
	// Stdin is the process input; nil means no input.
	Stdin io.Reader
}

// Result carries everything a caller needs to interpret an invocation
// that ran to completion.
type Result struct {
	Stdout   string
	Stderr   string
	ExitCode int
}

// Runner runs external commands. Run returns a non-nil error only
// when the command could not be executed at all (executable missing,
// context canceled, spawn failure); a command that ran and exited
// non-zero returns a nil error with the exit code in Result.
type Runner interface {
	Run(ctx context.Context, cmd Cmd) (Result, error)
}

// Local is the production Runner backed by os/exec.
type Local struct {
	// LookPath resolves an executable name; defaults to exec.LookPath.
	LookPath func(string) (string, error)
}

// waitDelay bounds how long Run waits for I/O after the context is
// canceled: a grandchild process holding the inherited pipes must not
// keep Wait blocked forever.
const waitDelay = 10 * time.Second

// Run executes c and captures its output. The executable is resolved
// first so a missing binary yields an actionable error instead of a
// spawn failure.
func (l Local) Run(ctx context.Context, c Cmd) (Result, error) {
	if c.Dir == "" {
		return Result{}, fmt.Errorf("run %s: working directory not set", c.Name)
	}
	lookPath := l.LookPath
	if lookPath == nil {
		lookPath = exec.LookPath
	}
	path, err := lookPath(c.Name)
	if err != nil {
		return Result{}, fmt.Errorf("%s not found on PATH; install it or adjust PATH: %w", c.Name, err)
	}

	cmd := exec.CommandContext(ctx, path, c.Args...)
	cmd.Dir = c.Dir
	cmd.Env = append(os.Environ(), c.Env...)
	cmd.Stdin = c.Stdin
	cmd.WaitDelay = waitDelay
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err = cmd.Run()
	res := Result{Stdout: stdout.String(), Stderr: stderr.String()}
	var exitErr *exec.ExitError
	switch {
	case err == nil:
		return res, nil
	case errors.As(err, &exitErr) && ctx.Err() == nil:
		// The command ran and exited non-zero: that is data, not an
		// error. A cancellation-induced kill is reported as an error
		// instead, because the caller did not get a real exit status.
		res.ExitCode = exitErr.ExitCode()
		return res, nil
	default:
		return Result{}, fmt.Errorf("run %s: %w", c.Name, err)
	}
}
