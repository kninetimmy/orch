package interview

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/kninetimmy/orch/internal/execx"
	"github.com/kninetimmy/orch/internal/instructions"
	"github.com/kninetimmy/orch/internal/memhub"
	"github.com/kninetimmy/orch/internal/opencode"
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
	// Runner executes git, memhub, and the OpenCode catalog query. It is
	// required whenever LookPath finds one of those commands.
	Runner execx.Runner
}

// Facts is Detect's pure snapshot of the local environment (PRD §18
// steps 1-2): which host CLIs are on PATH, whether RepoRoot sits
// inside a git repository, whether gh is on PATH, whether memhub is
// both on PATH and healthy, and how each host's root instruction file
// already stands (Instructions, keyed by host name). Facts feeds
// question defaults and Complete's audit record; it never travels
// inside an AnswerSet — an adapter cannot spoof detection by editing
// the answers map, since Next never reads facts back out of it.
type Facts struct {
	ClaudeCLI   bool
	CodexCLI    bool
	OpenCodeCLI bool
	// OpenCodeCatalog and OpenCodeCatalogError distinguish a successful
	// (possibly empty) catalog read from a failed one without making
	// Detect return an error or dropping the failure.
	OpenCodeCatalog      opencode.Catalog
	OpenCodeCatalogError string
	OpenCodeWarning      string

	Git     bool
	GitRoot string

	Gh bool

	MemhubCLI     bool
	MemhubHealthy bool
	MemhubDetail  string

	Instructions map[string]InstructionFileState
}

// InstructionFileState is Detect's classification of one host's root
// instruction file (InstructionFile names it): whether the file exists
// at all, whether it carries any content outside the Orch managed
// block, and whether it is nothing but that block. It is deliberately
// part of Facts rather than something the summary step reads for
// itself, so the seed question (seed.go) can be derived while
// buildSequence stays a pure function of facts and answers alone.
//
// BlockOnly is strictly narrower than "Exists && !Conventions", and the
// difference is load-bearing: the seed question's repair case proposes
// replacing this file's bytes, so it keys on BlockOnly alone. Every
// classification this build cannot fully vouch for — an unreadable
// file, a structurally broken marker pair, a drifted or newer-versioned
// block, and a file carrying no managed block at all (an empty one
// included) — reports Exists without Conventions and without BlockOnly.
type InstructionFileState struct {
	Exists      bool
	Conventions bool
	BlockOnly   bool
}

// Detect probes deps for every Facts field. It never returns an
// error: an unresolvable tool, unreachable memhub, or unreadable
// OpenCode catalog is data (a
// false/empty Facts field), not a Detect failure — the interview
// reports what it found and lets the human decide, rather than fail
// closed on missing tooling before any question has even been asked.
//
// Git is probed with a raw `git rev-parse --show-toplevel` through
// Runner rather than gitops.Open, which asserts .orchestrator/ sits at
// the git root and so cannot hold before initialization exists.
// memhub is probed through internal/memhub's Client, the shared PRD
// §20 client that runs `memhub status` with the repo root as an
// explicit working directory. The root instruction files are read
// directly (classifyInstructionFile), the one probe that touches the
// repository's own content rather than its tooling.
func Detect(ctx context.Context, deps Deps) Facts {
	// Only bound detection has an error path; ordinary discovery stays infallible.
	f, _ := detect(ctx, deps, false)
	return f
}

// DetectBound uses the same probes as Detect, but never follows a different
// detected Git root. MCP's launch binding must also cover the catalog and
// instruction reads inside detection, not just the later interview call.
// Missing Git remains a fact; a reported root with an unverifiable or different
// directory identity is an error before any subsequent probes or file reads.
// Ordinary Detect callers (including first-time init) retain root discovery.
func DetectBound(ctx context.Context, deps Deps) (Facts, error) {
	return detect(ctx, deps, true)
}

func detect(ctx context.Context, deps Deps, bound bool) (Facts, error) {
	var f Facts
	f.ClaudeCLI = lookPathOK(deps.LookPath, "claude")
	f.CodexCLI = lookPathOK(deps.LookPath, "codex")
	openCodePresent := lookPathOK(deps.LookPath, "opencode2") || lookPathOK(deps.LookPath, "opencode")
	f.Gh = lookPathOK(deps.LookPath, "gh")

	if lookPathOK(deps.LookPath, "git") {
		root, ok := detectGitRoot(ctx, deps)
		f.Git = ok
		f.GitRoot = root
	}

	if bound && f.Git {
		if !filepath.IsAbs(f.GitRoot) {
			return Facts{}, fmt.Errorf("interview repository binding: detected Git root %q is not absolute", f.GitRoot)
		}
		rootInfo, err := os.Stat(deps.RepoRoot)
		if err != nil {
			return Facts{}, fmt.Errorf("interview repository binding: %w", err)
		}
		gitInfo, err := os.Stat(f.GitRoot)
		if err != nil {
			return Facts{}, fmt.Errorf("interview repository binding: detected Git root: %w", err)
		}
		if !gitInfo.IsDir() || !os.SameFile(rootInfo, gitInfo) {
			return Facts{}, fmt.Errorf("interview repository binding: detected Git root %q does not match bound repository %q", f.GitRoot, deps.RepoRoot)
		}
	}

	if lookPathOK(deps.LookPath, "memhub") {
		f.MemhubCLI = true
		f.MemhubHealthy, f.MemhubDetail = probeMemhub(ctx, deps)
	}

	root := f.GitRoot
	if bound || root == "" {
		root = deps.RepoRoot
	}
	if openCodePresent {
		runtime, err := opencode.Resolve(ctx, deps.Runner, root)
		if err != nil {
			f.OpenCodeCatalogError = err.Error()
		} else {
			f.OpenCodeCLI = true
			f.OpenCodeWarning = runtime.Warning
			catalog, err := runtime.ReadCatalog(ctx, deps.Runner, root)
			if err != nil {
				f.OpenCodeCatalogError = err.Error()
			} else {
				f.OpenCodeCatalog = catalog
			}
		}
	}
	f.Instructions = map[string]InstructionFileState{
		"claude":   classifyInstructionFile(root, InstructionFile("claude")),
		"codex":    classifyInstructionFile(root, InstructionFile("codex")),
		"opencode": classifyInstructionFile(root, InstructionFile("opencode")),
	}

	return f, nil
}

// classifyInstructionFile reports whether repoRoot/name exists, whether
// it holds anything outside the Orch managed block, and whether it is
// nothing but that block (isBlockOnly, seed.go). It reuses
// instructions.PlanRemove because what is left once the managed region
// is stripped is exactly the question being asked (the same reading
// `orch doctor` reports this state from). Like every other Detect
// probe it reports trouble as data, not failure: an unreadable file or
// structurally broken markers classify as "exists, no conventions, not
// block-only", so nothing is ever seeded from — or, in the repair case,
// over — a file this build cannot read or parse, and `orch init`/
// `configure` still block on those files themselves.
//
// Before bound detection, the root probed was always the detected git root,
// falling back to Deps.RepoRoot. Ordinary Detect retains that resolution;
// DetectBound verifies identity first and uses Deps.RepoRoot instead, so both
// paths classify the same files their caller's summary will later plan against.
func classifyInstructionFile(repoRoot, name string) InstructionFileState {
	data, err := os.ReadFile(filepath.Join(repoRoot, name))
	if errors.Is(err, fs.ErrNotExist) {
		return InstructionFileState{}
	}
	if err != nil {
		return InstructionFileState{Exists: true}
	}
	ch, err := instructions.PlanRemove(string(data))
	if err != nil {
		return InstructionFileState{Exists: true}
	}
	return InstructionFileState{
		Exists:      true,
		Conventions: !instructions.IsOtherwiseEmpty(ch.New),
		BlockOnly:   isBlockOnly(string(data)),
	}
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
