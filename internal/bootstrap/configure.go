package bootstrap

import (
	"context"
	"errors"
	"fmt"

	"github.com/kninetimmy/orch/internal/config"
	"github.com/kninetimmy/orch/internal/ghops"
	"github.com/kninetimmy/orch/internal/gitops"
	"github.com/kninetimmy/orch/internal/interview"
	"github.com/kninetimmy/orch/internal/lockfile"
	"github.com/kninetimmy/orch/internal/question"
	"github.com/kninetimmy/orch/internal/state"
)

// ConfigureBranch is the fixed branch name `orch configure`'s Stage 0
// orphan preflights guard and Stage 1 creates — the same
// crash-collision rationale as BootstrapBranch (package doc), and
// deliberately distinct from both it and internal/run's per-issue
// orch/issue-<n>- scheme (drift-pinned in bootstrap_test.go).
const ConfigureBranch = "orch/configure"

// configureTitle is the fixed issue and PR title `orch configure`
// opens.
const configureTitle = "Update orch configuration"

// configureJob is the job internal/bootstrap threads through
// stage1/stage1AfterIssue/failClosed/requireNoOrphans for
// ExecuteConfigure — initJob's `orch configure` counterpart.
var configureJob = job{
	branch:   ConfigureBranch,
	title:    configureTitle,
	rerunCmd: "orch configure --deliver",
	validate: validateConfigure,
}

// ErrDeliveryActive reports a running or crashed Delivery run detected
// through state.Load/lockfile.Inspect: `orch configure` refuses to
// start while one is active (contract call 1) — a mid-run committed
// configuration edit would change the routing inputs the active run
// already computed against.
var ErrDeliveryActive = errors.New("a delivery run is active")

// ExecuteConfigure runs the full `orch configure` PR flow (PRD §17):
// Stage 0 re-derives and validates interview.NextConfigure's terminal
// document from deps.Answers (zero mutations; see stage0Configure),
// then Stage 1 performs the same ordered mutation sequence Execute's
// own bootstrap flow does (EnsureLabelTaxonomy, issue, worktree,
// files, validation, commit, push, PR, status), parameterized by
// configureJob instead of initJob.
func ExecuteConfigure(ctx context.Context, deps Deps) (Report, error) {
	complete, gitRoot, err := stage0Configure(ctx, deps)
	if err != nil {
		return Report{}, err
	}

	git, err := gitops.Open(ctx, deps.Runner, gitRoot)
	if err != nil {
		return Report{}, err
	}
	// Load-bearing: complete.Summary's diffs (ConfigDiff and every
	// instruction-file Diff) were computed against the working tree as
	// it stood when NextConfigure last ran; a dirty tree could mean
	// those diffs no longer describe what Stage 1 is about to write.
	if err := git.RequireClean(ctx, ""); err != nil {
		return Report{}, err
	}
	gh, err := ghops.Open(ctx, deps.Runner, gitRoot)
	if err != nil {
		return Report{}, err
	}
	repo, err := gh.Repo(ctx)
	if err != nil {
		return Report{}, err
	}
	if err := requireNoOrphans(ctx, git, gh, configureJob); err != nil {
		return Report{}, err
	}

	cfg, err := config.Parse([]byte(complete.Summary.ConfigTOML))
	if err != nil {
		// Unreachable in practice: materializeConfigure already
		// round-trips this exact TOML through Parse before NextConfigure
		// ever emits it.
		return Report{}, fmt.Errorf("parse re-derived configuration: %w", err)
	}

	return stage1(ctx, deps, git, gh, repo, complete, cfg, configureJob)
}

// stage0Configure re-derives Complete from deps.Answers and runs every
// zero-mutation preflight up through the active-Delivery refusal
// (contract call 1): Detect, then requireNoActiveDelivery, then
// interview.NextConfigure re-derivation requiring a ready terminal
// document — the gitops/ghops preflights that need an open handle live
// in ExecuteConfigure, which shares that handle with Stage 1, mirroring
// Execute's own stage0/Execute split.
func stage0Configure(ctx context.Context, deps Deps) (*question.Complete, string, error) {
	facts := interview.Detect(ctx, interview.Deps{RepoRoot: deps.RepoRoot, LookPath: deps.LookPath, Runner: deps.Runner})
	root := facts.GitRoot
	if root == "" {
		root = deps.RepoRoot
	}

	if err := requireNoActiveDelivery(root); err != nil {
		return nil, "", err
	}

	doc, err := interview.NextConfigure(facts, deps.Answers, root)
	if err != nil {
		return nil, "", err
	}
	if doc.Kind != question.DocComplete {
		return nil, "", fmt.Errorf("%w: re-derived document is %q, not %q; resubmit the same answers through `orch configure --step` until the interview completes", ErrNotComplete, doc.Kind, question.DocComplete)
	}
	complete := doc.Complete
	if !complete.BootstrapReady {
		return nil, "", fmt.Errorf("%w: %s", ErrNotBootstrapReady, joinReasons(complete.NotReadyReasons))
	}

	return complete, root, nil
}

// requireNoActiveDelivery fails closed when a Delivery run is active —
// state.Load reports mode delivery, or lockfile.Inspect reports a held
// lock — or when either check cannot even be completed (an unreadable
// file denies, the same fail-closed treatment every other precondition
// in this codebase gives an unreadable file). This duplicates
// internal/cli's own requireNoActiveDelivery (configurelocal.go) rather
// than importing it: cli already imports bootstrap, so the reverse
// import is unavailable — the reason this package's doc comment now
// says "never writes" to state/lockfile rather than "never touches"
// them (documented deviation).
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
		return fmt.Errorf("%w: delivery run %s is active (host %s); run `orch abort` first, or wait for it to finish", ErrDeliveryActive, st.Run.ID, st.Run.Host)
	}
	if owner != nil {
		return fmt.Errorf("%w: delivery lock is held by %s on %s (pid %d); run `orch abort` first", ErrDeliveryActive, owner.Host, owner.Hostname, owner.PID)
	}
	return nil
}
