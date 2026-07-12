package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/kninetimmy/orch/internal/config"
	"github.com/kninetimmy/orch/internal/interview"
	"github.com/kninetimmy/orch/internal/lockfile"
	"github.com/kninetimmy/orch/internal/question"
	"github.com/kninetimmy/orch/internal/state"
)

// configureLocalUsage is the one-line usage for `orch configure-local`,
// which parses its own flags (init.go's --step/--bootstrap precedent).
const configureLocalUsage = "orch configure-local: usage: orch configure-local | orch configure-local --step | orch configure-local --apply"

// configureLocalReportSchemaVersion is the --apply JSON report schema
// this build emits.
const configureLocalReportSchemaVersion = 1

// configureLocalReport is `orch configure-local --apply`'s JSON report.
type configureLocalReport struct {
	SchemaVersion int      `json:"schema_version"`
	Path          string   `json:"path"`
	Action        string   `json:"action"` // "written" or "removed"
	Overrides     []string `json:"overrides"`
}

// runConfigureLocal dispatches the three `orch configure-local` forms
// (PRD §17/§22). --step and --apply are mutually exclusive and any
// other argument is a usage mistake, both exit 2. Unlike `orch init`,
// there is no facts-based root resolution: configure-local only ever
// runs post-init, so env.RepoRoot is already the directory holding
// .orchestrator/ (Env's own contract).
func runConfigureLocal(env Env, args []string) error {
	var step, apply bool
	for _, a := range args {
		switch a {
		case "--step":
			step = true
		case "--apply":
			apply = true
		default:
			return usageError(configureLocalUsage)
		}
	}
	if step && apply {
		return usageError(configureLocalUsage)
	}

	switch {
	case step:
		return runConfigureLocalStep(env)
	case apply:
		return runConfigureLocalApply(env)
	default:
		return runConfigureLocalReport(env)
	}
}

// requireNoActiveDelivery fails closed when a Delivery run is active —
// state.Load reports mode delivery, or lockfile.Inspect reports a held
// lock — or when either check cannot even be completed (an unreadable
// state or lock file denies, the same fail-closed treatment every other
// precondition in this codebase gives an unreadable file).
// configure-local's --step and --apply both call this before ever
// consulting NextConfigureLocal: a mid-Delivery-run local model change
// would alter routing inputs the active run already computed against.
// internal/bootstrap.ExecuteConfigure (PR B) reuses this same check for
// `orch configure`.
func requireNoActiveDelivery(repoRoot string) error {
	st, err := state.Load(repoRoot)
	if err != nil {
		return err
	}
	owner, err := lockfile.Inspect(repoRoot)
	if err != nil {
		return err
	}
	if st.Mode == state.ModeDelivery {
		return fmt.Errorf("delivery run %s is active (host %s); run `orch abort` first, or wait for it to finish", st.Run.ID, st.Run.Host)
	}
	if owner != nil {
		return fmt.Errorf("delivery lock is held by %s on %s (pid %d); run `orch abort` first", owner.Host, owner.Hostname, owner.PID)
	}
	return nil
}

// runConfigureLocalReport prints the bare `orch configure-local` human
// report: the committed revision and current machine-local overrides,
// plus guidance pointing at the plumbing invocation. It never reads
// stdin and always exits 0 (the `orch init` bare-report precedent),
// even when the repository is not yet initialized or its configuration
// cannot be loaded.
func runConfigureLocalReport(env Env) error {
	fmt.Fprintln(env.Stdout, "orch configure-local: machine-local override report")

	cfg, err := config.Load(env.RepoRoot)
	if errors.Is(err, config.ErrNotInitialized) {
		fmt.Fprintln(env.Stdout, "\nnot initialized; run `orch init` first.")
		return nil
	}
	if err != nil {
		fmt.Fprintf(env.Stdout, "\nconfiguration error: %v\n", err)
		return nil
	}

	fmt.Fprintf(env.Stdout, "  committed revision: %s\n", cfg.ConfigRevision)
	switch {
	case !config.HasLocalOverride(env.RepoRoot):
		fmt.Fprintf(env.Stdout, "  local overrides:    none (%s does not exist)\n", config.LocalOverridePath)
	case len(cfg.Overrides) == 0:
		fmt.Fprintf(env.Stdout, "  local overrides:    none (%s exists but sets nothing)\n", config.LocalOverridePath)
	default:
		fmt.Fprintf(env.Stdout, "  local overrides:    %s\n", strings.Join(cfg.Overrides, ", "))
	}

	fmt.Fprintln(env.Stdout, "\nthis is a report only; an adapter drives the interactive interview through `orch configure-local --step`.")
	return nil
}

// runConfigureLocalStep is the adapter plumbing step-loop endpoint:
// decode one AnswerSet from stdin and write the resulting
// question.Document to stdout. It is pure and idempotent — it never
// writes config.local.toml; that only ever happens under --apply, a
// separate form, so an adapter retry can never double-write.
func runConfigureLocalStep(env Env) error {
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

	doc, err := interview.NextConfigureLocal(answers.Answers, env.RepoRoot)
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

// runConfigureLocalApply is `orch configure-local --apply`'s adapter
// plumbing endpoint: decode one AnswerSet from stdin and re-derive it
// through NextConfigureLocal itself (anti-forgery — there is no
// caller-supplied Complete parameter; the wire AnswerSet never carries
// one), requiring the result to be the terminal DocComplete before any
// write happens. It then writes or removes config.local.toml per the
// summary's one FileChange, re-validates the result with a post-write
// config.Load, and reports what it did as JSON.
func runConfigureLocalApply(env Env) error {
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

	doc, err := interview.NextConfigureLocal(answers.Answers, env.RepoRoot)
	if err != nil {
		return err
	}
	if doc.Kind != question.DocComplete {
		return fmt.Errorf("orch configure-local --apply: the given answers do not re-derive to an approved configuration (got %q; re-run --step to see what is still needed)", doc.Kind)
	}

	change := doc.Complete.Summary.Files[0]
	action := "written"
	if change.Delete {
		if err := config.RemoveLocal(env.RepoRoot); err != nil {
			return err
		}
		action = "removed"
	} else {
		if err := config.WriteLocal(env.RepoRoot, []byte(change.NewContent)); err != nil {
			return err
		}
	}

	cfg, err := config.Load(env.RepoRoot)
	if err != nil {
		return fmt.Errorf("post-write validation: %w", err)
	}

	out, err := json.MarshalIndent(configureLocalReport{
		SchemaVersion: configureLocalReportSchemaVersion,
		Path:          config.LocalOverridePath,
		Action:        action,
		Overrides:     cfg.Overrides,
	}, "", "  ")
	if err != nil {
		return fmt.Errorf("encode apply report: %w", err)
	}
	_, err = fmt.Fprintf(env.Stdout, "%s\n", out)
	return err
}
