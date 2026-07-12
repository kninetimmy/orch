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
	"strings"
	"time"

	"github.com/kninetimmy/orch/internal/bootstrap"
	"github.com/kninetimmy/orch/internal/config"
	"github.com/kninetimmy/orch/internal/interview"
	"github.com/kninetimmy/orch/internal/question"
)

// configureUsage is the one-line usage for `orch configure`, which
// parses its own flags (init.go's --step/--bootstrap precedent).
const configureUsage = "orch configure: usage: orch configure | orch configure --step | orch configure --deliver"

// runConfigure dispatches the three `orch configure` forms (PRD §17/
// §22). --step and --deliver are mutually exclusive and any other
// argument is a usage mistake, both exit 2. The flag is named
// --deliver, not --bootstrap: PRD §17 describes configuration changes
// as delivered through an issue/PR, and this repository is already
// past its first bootstrap by the time `orch configure` applies.
func runConfigure(env Env, args []string) error {
	var step, deliver bool
	for _, a := range args {
		switch a {
		case "--step":
			step = true
		case "--deliver":
			deliver = true
		default:
			return usageError(configureUsage)
		}
	}
	if step && deliver {
		return usageError(configureUsage)
	}

	switch {
	case step:
		return runConfigureStep(env)
	case deliver:
		return runConfigureDeliver(env)
	default:
		return runConfigureReport(env)
	}
}

// loadCommittedForReport reads and parses env.RepoRoot's committed
// configuration directly (config.Parse on the raw bytes, never
// config.Load): the bare report shows what is committed, not what a
// machine-local override currently makes effective — the same
// committed-vs-effective distinction `orch configure`'s own interview
// is built on (interview.NextConfigure seeds from committed alone).
func loadCommittedForReport(repoRoot string) (*config.Config, error) {
	data, err := os.ReadFile(filepath.Join(repoRoot, filepath.FromSlash(config.Path)))
	if errors.Is(err, fs.ErrNotExist) {
		return nil, config.ErrNotInitialized
	}
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", config.Path, err)
	}
	return config.Parse(data)
}

// runConfigureReport prints the bare `orch configure` human report:
// the committed revision, enabled hosts, the four settings, and a
// delivery-active warning when applicable, plus guidance pointing at
// the plumbing invocation. It never reads stdin and always exits 0
// (the `orch init`/`orch configure-local` bare-report precedent), even
// when the repository is not yet initialized or its configuration
// cannot be parsed.
func runConfigureReport(env Env) error {
	fmt.Fprintln(env.Stdout, "orch configure: committed configuration report")

	cfg, err := loadCommittedForReport(env.RepoRoot)
	if errors.Is(err, config.ErrNotInitialized) {
		fmt.Fprintln(env.Stdout, "\nnot initialized; run `orch init` first.")
		return nil
	}
	if err != nil {
		fmt.Fprintf(env.Stdout, "\nconfiguration error: %v\n", err)
		return nil
	}

	fmt.Fprintf(env.Stdout, "  committed revision: %s\n", cfg.ConfigRevision)
	hosts := cfg.EnabledHosts()
	if len(hosts) == 0 {
		fmt.Fprintln(env.Stdout, "  enabled hosts:      none")
	} else {
		fmt.Fprintf(env.Stdout, "  enabled hosts:      %s\n", strings.Join(hosts, ", "))
	}
	fmt.Fprintf(env.Stdout, "  concurrency:        %d\n", cfg.Concurrency.MaxSubagents)
	fmt.Fprintf(env.Stdout, "  merge strategy:     %s\n", cfg.Merge.Strategy)
	fmt.Fprintf(env.Stdout, "  memhub mode:        %s\n", cfg.Memhub.Mode)
	fmt.Fprintf(env.Stdout, "  metrics enabled:    %t\n", cfg.Metrics.Enabled)

	if err := requireNoActiveDelivery(env.RepoRoot); err != nil {
		fmt.Fprintf(env.Stdout, "\nwarning: %v\n", err)
	}

	fmt.Fprintln(env.Stdout, "\nthis is a report only; an adapter drives the interactive interview through `orch configure --step`.")
	return nil
}

// runConfigureStep is the adapter plumbing step-loop endpoint: decode
// one AnswerSet from stdin and write the resulting question.Document
// to stdout (the `orch init --step`/`orch configure-local --step`
// precedent). It refuses while a Delivery run is active before ever
// consulting NextConfigure — a mid-run committed configuration change
// would alter routing inputs the active run already computed against.
func runConfigureStep(env Env) error {
	if err := requireNoActiveDelivery(env.RepoRoot); err != nil {
		return err
	}

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
	doc, err := interview.NextConfigure(facts, answers.Answers, root)
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

// runConfigureDeliver is `orch configure --deliver`'s adapter plumbing
// endpoint: decode one AnswerSet from stdin (never a Complete document
// — bootstrap.ExecuteConfigure re-derives and validates it itself, the
// same anti-forgery property `orch init --bootstrap` relies on) and
// hand it to bootstrap.ExecuteConfigure, the whole policy home for the
// delivery flow. This function stays thin dispatch (runInitBootstrap
// precedent).
func runConfigureDeliver(env Env) error {
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
	report, err := bootstrap.ExecuteConfigure(context.Background(), deps)
	if err != nil {
		return err
	}

	out, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		return fmt.Errorf("encode configure report: %w", err)
	}
	_, err = fmt.Fprintf(env.Stdout, "%s\n", out)
	return err
}
