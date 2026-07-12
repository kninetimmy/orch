// Package bootstrap executes PRD §18 steps 10-15: given the answers an
// interview (internal/interview, internal/question) collected, it
// re-derives the terminal question.Complete document itself and
// composes internal/config, internal/instructions, internal/gitops,
// and internal/ghops into the one mechanical act that turns an
// approved interview into an open bootstrap pull request — a
// mechanical in-binary executor, no model subagent (decision 30).
//
// Execute is the package's only exported entry point and the whole
// policy home for `orch init --bootstrap`; cli/init.go stays thin
// dispatch (decode stdin into an AnswerSet, call Execute, encode the
// result). Two design choices Stage 0 exists to enforce:
//
//   - Anti-forgery (spec pass 2026-07-11, contract call 4): Execute
//     never trusts a caller-supplied Complete document — there is no
//     such parameter. It re-runs interview.Detect and interview.Next
//     itself from the raw answers in Deps.Answers and requires the
//     result to be a DocComplete with BootstrapReady, so every byte it
//     goes on to write is recomputed from validated answers, never
//     injected by an adapter. Approval is itself an answer Next
//     validates, and Blockers make approval an error, so a caller
//     cannot skip either check by hand-crafting a document.
//   - No Delivery lock (contract call 1): bootstrap never creates
//     .orchestrator/ in the primary checkout and never writes to
//     internal/state or internal/lockfile (ExecuteConfigure, PR B's
//     `orch configure` counterpart, does read both — its own Stage 0
//     refuses to start while a Delivery run is active — but neither
//     entry point ever creates or mutates either). Mutual exclusion
//     against a concurrent or crashed bootstrap comes entirely from
//     fail-closed preflights against a fixed branch name, orch/bootstrap
//     — not the per-issue orch/issue-<n>- scheme internal/run uses,
//     because the issue number this branch will eventually close does
//     not exist until Stage 1 creates it. A fixed name is what makes a
//     crashed re-run collide with its own leftovers instead of
//     sidestepping them under a fresh number (contract call 3).
//
// Stage 0 (preflights, see preflight.go) performs zero mutations: any
// failure there changes nothing on disk or on GitHub. Stage 1
// (mutations, see below) advances in a fixed order — label taxonomy,
// issue, worktree, files, §18.13 validation, commit, push, PR, status
// — and every failure after the issue is created is reported with the
// exact artifacts it left behind and the shell commands that undo them
// (contract call 3: re-running bootstrap from the same answers is the
// recovery path, not `orch abort`, which this package never calls —
// it never touches Delivery state to begin with).
package bootstrap

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/kninetimmy/orch/internal/config"
	"github.com/kninetimmy/orch/internal/execx"
	"github.com/kninetimmy/orch/internal/ghops"
	"github.com/kninetimmy/orch/internal/gitops"
	"github.com/kninetimmy/orch/internal/interview"
	"github.com/kninetimmy/orch/internal/question"
)

// BootstrapBranch is the fixed branch name Stage 0's orphan preflights
// guard and Stage 1 creates (see the package doc's "no Delivery lock"
// note): deliberately not the Delivery orch/issue-<n>- scheme.
const BootstrapBranch = "orch/bootstrap"

// bootstrapTitle is the fixed issue and PR title (PRD §18 step 10).
const bootstrapTitle = "Bootstrap orch configuration"

// ReportSchemaVersion is the Report schema this build emits.
const ReportSchemaVersion = 1

// job parameterizes stage1/stage1AfterIssue/failClosed/requireNoOrphans
// across Execute's own init-bootstrap flow and ExecuteConfigure's PR B
// counterpart (configure.go): each names its fixed branch, its
// issue/PR title, the exact re-run command failClosed's and
// requireNoOrphans' remediation should name, and the worktree
// validation hook stage1AfterIssue runs before ever committing
// (validateInstallation for init, validateConfigure for `orch
// configure`).
type job struct {
	branch   string
	title    string
	rerunCmd string
	validate func(dir string, complete *question.Complete, now time.Time) ([]ValidationEntry, error)
}

// initJob is Execute's own job: the package's original, byte-stable
// bootstrap flow.
var initJob = job{
	branch:   BootstrapBranch,
	title:    bootstrapTitle,
	rerunCmd: "orch init --bootstrap",
	validate: validateInstallation,
}

// Deps carries Execute's injectable surface.
type Deps struct {
	// RepoRoot is the directory `orch init --bootstrap` was invoked
	// from — the primary checkout. Detect probes git from here; the
	// resulting Facts.GitRoot (not RepoRoot) is what every subsequent
	// gitops/ghops/config/instructions call uses once Stage 0 confirms
	// git is present (BootstrapReady already requires it).
	RepoRoot string
	// Answers is the adapter's raw AnswerSet.Answers — never a
	// pre-built Complete document (see the package doc's anti-forgery
	// note). A nil map is treated as empty, mirroring
	// question.DecodeAnswers' own normalization.
	Answers map[string]string
	// Runner executes git and gh.
	Runner execx.Runner
	// LookPath resolves an executable name; a nil LookPath makes every
	// host/git/gh/memhub fact false (interview.Deps precedent).
	LookPath func(string) (string, error)
	// Now returns the current time; nil defaults to time.Now
	// (run.Env precedent). Stamps every §18.13 validation entry.
	Now func() time.Time
}

// Report is Execute's success output, emitted by `orch init
// --bootstrap` as schema-versioned JSON on stdout.
type Report struct {
	SchemaVersion int               `json:"schema_version"`
	Issue         ReportRef         `json:"issue"`
	PR            ReportRef         `json:"pr"`
	Branch        string            `json:"branch"`
	Validations   []ValidationEntry `json:"validations"`
	NextSteps     []string          `json:"next_steps"`
}

// ReportRef names a created issue or PR.
type ReportRef struct {
	Number int    `json:"number"`
	URL    string `json:"url"`
}

// ValidationEntry is one §18.13 mechanical check's outcome, in the
// shape manifest.Verification/run.VerificationInput already establish
// for audit-record evidence: Name identifies the check, Result is a
// short free-text verdict ("pass"/"fail"), Detail explains a failure,
// and At is the RFC3339 UTC stamp from Deps.Now.
type ValidationEntry struct {
	Name   string `json:"name"`
	Result string `json:"result"`
	Detail string `json:"detail,omitempty"`
	At     string `json:"at,omitempty"`
}

// now returns deps's timestamp source as UTC (run.Env.now precedent).
func (d Deps) now() time.Time {
	if d.Now != nil {
		return d.Now().UTC()
	}
	return time.Now().UTC()
}

// Execute runs the full bootstrap: Stage 0 re-derives and validates
// the interview's terminal document from deps.Answers (zero
// mutations; see preflight.go), then Stage 1 performs the ordered
// mutation sequence PRD §18 steps 10-15 describe.
func Execute(ctx context.Context, deps Deps) (Report, error) {
	complete, gitRoot, err := stage0(ctx, deps)
	if err != nil {
		return Report{}, err
	}

	git, err := gitops.Open(ctx, deps.Runner, gitRoot)
	if err != nil {
		return Report{}, err
	}
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
	if err := requireNoOrphans(ctx, git, gh, initJob); err != nil {
		return Report{}, err
	}

	cfg, err := config.Parse([]byte(complete.Summary.ConfigTOML))
	if err != nil {
		// Unreachable in practice: materialize already round-trips this
		// exact TOML through Parse before Next ever emits it.
		return Report{}, fmt.Errorf("parse re-derived configuration: %w", err)
	}

	return stage1(ctx, deps, git, gh, repo, complete, cfg, initJob)
}

// stage0 re-derives Complete from deps.Answers and runs every
// zero-mutation preflight up through "not already initialized" (the
// gitops/ghops preflights that need an open handle live in Execute,
// which shares that handle with Stage 1). It returns the validated
// Complete document and the detected git root.
func stage0(ctx context.Context, deps Deps) (*question.Complete, string, error) {
	facts := interview.Detect(ctx, interview.Deps{RepoRoot: deps.RepoRoot, LookPath: deps.LookPath, Runner: deps.Runner})
	root := facts.GitRoot
	if root == "" {
		root = deps.RepoRoot
	}
	doc, err := interview.Next(facts, deps.Answers, root)
	if err != nil {
		return nil, "", err
	}
	if doc.Kind != question.DocComplete {
		return nil, "", fmt.Errorf("%w: re-derived document is %q, not %q; resubmit the same answers through `orch init --step` until the interview completes", ErrNotComplete, doc.Kind, question.DocComplete)
	}
	complete := doc.Complete
	if !complete.BootstrapReady {
		return nil, "", fmt.Errorf("%w: %s", ErrNotBootstrapReady, joinReasons(complete.NotReadyReasons))
	}

	gitRoot := facts.GitRoot
	cfgPath := filepath.Join(gitRoot, filepath.FromSlash(config.Path))
	switch _, err := os.Stat(cfgPath); {
	case err == nil:
		return nil, "", fmt.Errorf("%w; run `orch configure` to change it", ErrAlreadyInitialized)
	case !errors.Is(err, os.ErrNotExist):
		return nil, "", fmt.Errorf("check %s: %w", config.Path, err)
	}

	return complete, gitRoot, nil
}

// joinReasons renders NotReadyReasons for ErrNotBootstrapReady's
// message; empty is unreachable (BootstrapReady false always carries
// at least one reason — see interview.buildComplete) but handled
// rather than assumed.
func joinReasons(reasons []string) string {
	if len(reasons) == 0 {
		return "not ready"
	}
	out := reasons[0]
	for _, r := range reasons[1:] {
		out += "; " + r
	}
	return out
}
