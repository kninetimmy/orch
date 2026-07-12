package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"time"

	"github.com/kninetimmy/orch/internal/bootstrap"
	"github.com/kninetimmy/orch/internal/config"
	"github.com/kninetimmy/orch/internal/interview"
	"github.com/kninetimmy/orch/internal/question"
)

// initUsage is the one-line usage for `orch init`, which parses its
// own flags (resume.go precedent) rather than using noArgs.
const initUsage = "orch init: usage: orch init | orch init --step | orch init --bootstrap"

// runInit dispatches the three `orch init` forms (PR B: PRD §18/§22).
// --step and --bootstrap are mutually exclusive and any other
// argument is a usage mistake, both exit 2.
func runInit(env Env, args []string) error {
	var step, doBootstrap bool
	for _, a := range args {
		switch a {
		case "--step":
			step = true
		case "--bootstrap":
			doBootstrap = true
		default:
			return usageError(initUsage)
		}
	}
	if step && doBootstrap {
		return usageError(initUsage)
	}

	switch {
	case step:
		return runInitStep(env)
	case doBootstrap:
		return runInitBootstrap(env)
	default:
		return runInitReport(env)
	}
}

// initDetect runs interview.Detect against env, the shared probe every
// `orch init` form starts from.
func initDetect(env Env) interview.Facts {
	return interview.Detect(context.Background(), interview.Deps{RepoRoot: env.RepoRoot, LookPath: env.LookPath, Runner: env.Runner})
}

// initRoot is the root Next consults once the question sequence is
// answered (internal/interview's package doc): facts.GitRoot, falling
// back to env.RepoRoot when git is absent.
func initRoot(env Env, facts interview.Facts) string {
	if facts.GitRoot != "" {
		return facts.GitRoot
	}
	return env.RepoRoot
}

// configExists reports whether the committed configuration is already
// present at root. Display-only: runInitReport uses it to pick a
// report line, and a bare report always exits 0. The plumbing forms
// fail closed through requireNotInitialized instead.
func configExists(root string) bool {
	_, err := os.Stat(filepath.Join(root, filepath.FromSlash(config.Path)))
	return err == nil
}

// requireNotInitialized fails closed when the committed configuration
// already exists at root — or when its presence cannot be determined
// (bootstrap's stage0 discipline, mirrored: an unreadable check is a
// denial, not a pass).
func requireNotInitialized(root string) error {
	switch _, err := os.Stat(filepath.Join(root, filepath.FromSlash(config.Path))); {
	case err == nil:
		return fmt.Errorf("already initialized (%s exists); run `orch configure` to change it", config.Path)
	case !errors.Is(err, fs.ErrNotExist):
		return fmt.Errorf("check %s: %w", config.Path, err)
	}
	return nil
}

// runInitReport prints the bare `orch init` human detection report
// (PRD §22): host CLIs, git + root, gh, and memhub + health detail,
// plus guidance pointing at the plumbing invocation. It never reads
// stdin (the `orch run status` precedent: a command a human might run
// directly must never block on a console that never reaches EOF) and
// always exits 0 — it is a report, even when git is missing or the
// repository is already initialized.
func runInitReport(env Env) error {
	facts := initDetect(env)

	fmt.Fprintln(env.Stdout, "orch init: detection report")
	fmt.Fprintf(env.Stdout, "  claude CLI:  %s\n", yesNo(facts.ClaudeCLI))
	fmt.Fprintf(env.Stdout, "  codex CLI:   %s\n", yesNo(facts.CodexCLI))
	fmt.Fprintf(env.Stdout, "  git:         %s\n", yesNo(facts.Git))
	if facts.Git {
		fmt.Fprintf(env.Stdout, "  git root:    %s\n", facts.GitRoot)
	}
	fmt.Fprintf(env.Stdout, "  gh:          %s\n", yesNo(facts.Gh))
	fmt.Fprintf(env.Stdout, "  memhub CLI:  %s\n", yesNo(facts.MemhubCLI))
	if facts.MemhubCLI {
		fmt.Fprintf(env.Stdout, "  memhub:      %s\n", memhubStatus(facts))
	}

	root := initRoot(env, facts)
	if configExists(root) {
		fmt.Fprintf(env.Stdout, "\nalready initialized (%s exists); run `orch configure` to change it.\n", config.Path)
		return nil
	}

	fmt.Fprintln(env.Stdout, "\nthis is a report only; an adapter drives the interactive interview through `orch init --step`.")
	return nil
}

// yesNo renders a Go bool as the detection report's "yes"/"no".
func yesNo(b bool) string {
	if b {
		return "yes"
	}
	return "no"
}

// memhubStatus renders the detection report's memhub health line.
func memhubStatus(f interview.Facts) string {
	if f.MemhubHealthy {
		return "healthy"
	}
	if f.MemhubDetail != "" {
		return fmt.Sprintf("unhealthy (%s)", f.MemhubDetail)
	}
	return "unhealthy"
}

// runInitStep is the adapter plumbing step-loop endpoint
// (internal/interview's package doc): decode one AnswerSet from
// stdin, re-run Detect, call Next, and write the resulting Document
// to stdout. Interview errors and stdin decode errors exit 1 with the
// message on stderr so the adapter can re-ask.
func runInitStep(env Env) error {
	data, err := io.ReadAll(env.Stdin)
	if err != nil {
		return fmt.Errorf("read stdin: %w", err)
	}
	answers, err := question.DecodeAnswers(data)
	if err != nil {
		return err
	}

	facts := initDetect(env)
	root := initRoot(env, facts)
	if err := requireNotInitialized(root); err != nil {
		return err
	}

	doc, err := interview.Next(facts, answers.Answers, root)
	if err != nil {
		return err
	}

	out, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return fmt.Errorf("encode step document: %w", err)
	}
	_, err = fmt.Fprintf(env.Stdout, "%s\n", out)
	return err
}

// runInitBootstrap is `orch init --bootstrap`'s adapter plumbing
// endpoint: decode one AnswerSet from stdin (never a Complete
// document — internal/bootstrap re-derives and validates it itself,
// the anti-forgery property its package doc describes) and hand it to
// bootstrap.Execute, the whole policy home for the bootstrap. This
// function stays thin dispatch.
func runInitBootstrap(env Env) error {
	data, err := io.ReadAll(env.Stdin)
	if err != nil {
		return fmt.Errorf("read stdin: %w", err)
	}
	answers, err := question.DecodeAnswers(data)
	if err != nil {
		return err
	}

	deps := bootstrap.Deps{
		RepoRoot: env.RepoRoot,
		Answers:  answers.Answers,
		Runner:   env.Runner,
		LookPath: env.LookPath,
		Now:      time.Now,
	}
	report, err := bootstrap.Execute(context.Background(), deps)
	if err != nil {
		return err
	}

	out, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		return fmt.Errorf("encode bootstrap report: %w", err)
	}
	_, err = fmt.Fprintf(env.Stdout, "%s\n", out)
	return err
}
