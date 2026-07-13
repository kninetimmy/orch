package interview

import (
	"context"
	"strings"

	"github.com/kninetimmy/orch/internal/execx"
	"github.com/kninetimmy/orch/internal/memhub"
)

// Deps carries Detect's injectable surface: LookPath and Runner are
// the same shapes cli.Env and execx.Local already use, so a real
// caller wires them identically and a test substitutes fakes.
type Deps struct {
	// RepoRoot is the directory Detect probes git and memhub from. It
	// need not carry .orchestrator/ yet — Detect runs pre-init.
	RepoRoot string
	// LookPath resolves an executable name; a nil LookPath makes every
	// host/git/gh/memhub fact false.
	LookPath func(string) (string, error)
	// Runner executes git and memhub. Required whenever LookPath finds
	// git or memhub — Detect never calls a nil Runner.
	Runner execx.Runner
}

// Facts is Detect's pure snapshot of the local environment (PRD §18
// steps 1-2): which host CLIs are on PATH, whether RepoRoot sits
// inside a git repository, whether gh is on PATH, and whether memhub
// is both on PATH and healthy. Facts feeds question defaults and
// Complete's audit record; it never travels inside an AnswerSet — an
// adapter cannot spoof detection by editing the answers map, since
// Next never reads facts back out of it.
type Facts struct {
	ClaudeCLI bool
	CodexCLI  bool

	Git     bool
	GitRoot string

	Gh bool

	MemhubCLI     bool
	MemhubHealthy bool
	MemhubDetail  string
}

// Detect probes deps for every Facts field. It never returns an
// error: an unresolvable tool or an unreachable memhub is data (a
// false/empty Facts field), not a Detect failure — the interview
// reports what it found and lets the human decide, rather than fail
// closed on missing tooling before any question has even been asked.
//
// Git is probed with a raw `git rev-parse --show-toplevel` through
// Runner rather than gitops.Open, which asserts .orchestrator/ sits at
// the git root and so cannot hold before initialization exists.
// memhub is probed through internal/memhub's Client, the shared PRD
// §20 client that runs `memhub status` with the repo root as an
// explicit working directory.
func Detect(ctx context.Context, deps Deps) Facts {
	var f Facts
	f.ClaudeCLI = lookPathOK(deps.LookPath, "claude")
	f.CodexCLI = lookPathOK(deps.LookPath, "codex")
	f.Gh = lookPathOK(deps.LookPath, "gh")

	if lookPathOK(deps.LookPath, "git") {
		root, ok := detectGitRoot(ctx, deps)
		f.Git = ok
		f.GitRoot = root
	}

	if lookPathOK(deps.LookPath, "memhub") {
		f.MemhubCLI = true
		f.MemhubHealthy, f.MemhubDetail = probeMemhub(ctx, deps)
	}

	return f
}

// lookPathOK reports whether lookPath resolves name; a nil lookPath
// fails closed to false rather than panic.
func lookPathOK(lookPath func(string) (string, error), name string) bool {
	if lookPath == nil {
		return false
	}
	_, err := lookPath(name)
	return err == nil
}

// detectGitRoot runs `git rev-parse --show-toplevel` in deps.RepoRoot
// and reports the trimmed toplevel path, or false if the command could
// not run or exited non-zero (RepoRoot is not inside a git
// repository).
func detectGitRoot(ctx context.Context, deps Deps) (string, bool) {
	res, err := deps.Runner.Run(ctx, execx.Cmd{Name: "git", Args: []string{"rev-parse", "--show-toplevel"}, Dir: deps.RepoRoot})
	if err != nil || res.ExitCode != 0 {
		return "", false
	}
	return strings.TrimSpace(res.Stdout), true
}

// probeMemhub runs internal/memhub's Client.Probe against deps.RepoRoot
// and reports whether it succeeded, plus a detail string for the
// failure case — Probe's error text verbatim.
func probeMemhub(ctx context.Context, deps Deps) (healthy bool, detail string) {
	if err := memhub.New(deps.Runner, deps.RepoRoot).Probe(ctx); err != nil {
		return false, err.Error()
	}
	return true, ""
}
