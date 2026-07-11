// This file holds the shared infrastructure for the per-issue Delivery
// lifecycle verbs (PRD §12 steps 8-22), each in its own file:
//
//	dispatch      worktree-ready → dispatched
//	pr-open       dispatched     → pr-open
//	review        pr-open/in-review → in-review
//	escalate      dispatched/pr-open/in-review → reroute (same) or blocked
//	ci            pr-open/in-review/awaiting-merge → same (manifest only)
//	merge-report  in-review      → awaiting-merge
//	merge         awaiting-merge → merged
//	block         any non-terminal → blocked
//	abandon       any non-terminal → abandoned
//	cleanup       merged/abandoned → cleaned
//	complete      run-level; requires every issue cleaned
//
// Failure semantics are pure (PRD §15, resolved 2026-07-11): no verb
// mutates phase, state, GitHub, or git as a side effect of failing.
// State advances only on the success path, persisted after each
// sub-step; a post-mutation error is wrapped (wrapAfterMutation) with
// the `orch abort`/resume remediation, exactly as activation wraps
// errors after EnterDelivery. Every mutating verb runs the shared
// preconditions in loadVerb before touching anything.
package run

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/kninetimmy/orch/internal/config"
	"github.com/kninetimmy/orch/internal/ghops"
	"github.com/kninetimmy/orch/internal/lockfile"
	"github.com/kninetimmy/orch/internal/state"
)

// now returns the verb's timestamp source as UTC (PRD §23: every
// engine-stamped At is RFC3339 UTC; tests inject a fixed clock). Verb
// logic never reads the wall clock directly — it always goes through
// here so a fixed Env.Now makes every stamped body deterministic.
func (e Env) now() time.Time {
	if e.Now != nil {
		return e.Now().UTC()
	}
	return time.Now().UTC()
}

// nowStamp is now() formatted as the RFC3339 UTC string the manifest
// records carry.
func (e Env) nowStamp() string {
	return e.now().Format(time.RFC3339)
}

// verbCtx is the loaded, validated context every mutating lifecycle verb
// shares: the config, the delivery state, the lock owner, and the index
// of the target issue (-1 for run-level verbs like complete). The verb
// mutates c.st.Run and persists with state.Save(c.env.RepoRoot, c.st).
type verbCtx struct {
	env   Env
	cfg   *config.Config
	st    *state.State
	owner *lockfile.Owner
	idx   int
}

// issue returns a pointer to the target issue within c.st.Run.Issues, so
// verb mutations land in the value state.Save persists.
func (c *verbCtx) issue() *state.Issue {
	return &c.st.Run.Issues[c.idx]
}

// save persists c.st after a verb sub-step (PRD §23 incremental
// persistence).
func (c *verbCtx) save() error {
	return state.Save(c.env.RepoRoot, c.st)
}

// worktreeAbs is the target issue's worktree as an absolute filesystem
// path, from its repo-relative slash form.
func (c *verbCtx) worktreeAbs() string {
	return filepath.Join(c.env.RepoRoot, filepath.FromSlash(c.issue().Worktree))
}

// loadVerb runs the preconditions every mutating lifecycle verb shares,
// in order (PRD §14 concurrency invariant, §16 config-drift and
// stopped-run fail-closed):
//
//  1. Consistent delivery state owned by this run's lock.
//  2. Delivery mode.
//  3. Config that has not drifted from the run's ConfigRevision.
//  4. The run is not stopped (unless exemptStop — only block is exempt).
//  5. When issueNumber > 0, exactly one matching run issue whose phase is
//     in allowed; issueNumber == 0 is a run-level verb with no issue.
//
// It returns the loaded context; the verb performs its ordered
// operations and persists on the success path.
func loadVerb(env Env, issueNumber int, allowed []state.Phase, exemptStop bool) (*verbCtx, error) {
	st, err := state.Load(env.RepoRoot)
	if err != nil {
		return nil, err
	}
	owner, err := lockfile.Inspect(env.RepoRoot)
	if err != nil {
		return nil, err
	}
	if err := state.CheckConsistent(st, owner); err != nil {
		return nil, err
	}
	if st.Mode != state.ModeDelivery {
		return nil, fmt.Errorf("%w; run `orch run activate` to enter Delivery first", ErrNoDeliveryRun)
	}
	// CheckConsistent + validate guarantee st.Run is non-nil and its ID
	// matches the lock owner here (the PR A concurrency invariant).
	cfg, err := config.Load(env.RepoRoot)
	if err != nil {
		return nil, err
	}
	if cfg.ConfigRevision != st.Run.Plan.ConfigRevision {
		return nil, fmt.Errorf("%w: config revision %q does not match the run's %q; run `orch abort`, ship the config change on its own Delivery run, then re-plan", ErrConfigDrift, cfg.ConfigRevision, st.Run.Plan.ConfigRevision)
	}
	if !exemptStop && st.Run.StoppedReason != "" {
		return nil, fmt.Errorf("%w (%s); run `orch abort` to return to assist, or a future `orch resume` to continue", ErrRunStopped, st.Run.StoppedReason)
	}

	c := &verbCtx{env: env, cfg: cfg, st: st, owner: owner, idx: -1}
	if issueNumber <= 0 {
		return c, nil
	}

	idx := -1
	for i := range st.Run.Issues {
		if st.Run.Issues[i].Number == issueNumber {
			if idx != -1 {
				return nil, fmt.Errorf("%w: issue_number %d matches more than one run issue; run `orch abort`", ErrUnknownIssue, issueNumber)
			}
			idx = i
		}
	}
	if idx == -1 {
		return nil, fmt.Errorf("%w: issue_number %d", ErrUnknownIssue, issueNumber)
	}
	c.idx = idx
	if !phaseAllowed(st.Run.Issues[idx].Phase, allowed) {
		return nil, fmt.Errorf("%w: issue #%d is in phase %s; this verb requires one of %s", ErrWrongPhase, issueNumber, st.Run.Issues[idx].Phase, phaseList(allowed))
	}
	return c, nil
}

// phaseAllowed reports whether p is in allowed.
func phaseAllowed(p state.Phase, allowed []state.Phase) bool {
	for _, a := range allowed {
		if p == a {
			return true
		}
	}
	return false
}

// phaseList renders allowed as a comma-separated list for error text.
func phaseList(allowed []state.Phase) string {
	parts := make([]string, len(allowed))
	for i, a := range allowed {
		parts[i] = string(a)
	}
	return strings.Join(parts, ", ")
}

// nonTerminalPhases is the set block and abandon accept: every phase an
// in-flight issue can hold, including blocked (re-block / abandon a
// blocked issue). The terminal phases merged, cleaned, and abandoned are
// excluded — there is nothing left to block or abandon there.
var nonTerminalPhases = []state.Phase{
	state.PhaseWorktreeReady, state.PhaseDispatched, state.PhasePROpen,
	state.PhaseInReview, state.PhaseAwaitingMerge, state.PhaseBlocked,
}

// decodeRequest decodes data into v, rejecting unknown fields at any
// level and trailing data (the DecodeActivation pattern). The caller
// checks the schema_version field after decoding.
func decodeRequest(data []byte, v any) error {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		return fmt.Errorf("%w: %v", ErrBadRequest, err)
	}
	if dec.More() {
		return fmt.Errorf("%w: trailing data after request document", ErrBadRequest)
	}
	return nil
}

// wrapAfterMutation marks an error that occurred after a lifecycle verb
// already advanced persisted state or a GitHub/git surface: the run is
// mid-step, so the remediation is `orch abort` or a future `orch resume`
// (the verb re-runs cleanly from the last completed sub-step), never a
// bare retry. It mirrors activation's wrapAfterEnter for the per-issue
// verbs.
func wrapAfterMutation(err error) error {
	if err == nil {
		return nil
	}
	return fmt.Errorf("%w (state advanced; run `orch abort` to return to assist, or a future `orch resume` to continue from the last completed step)", err)
}

// openGitHub opens the authenticated gh handle every verb that touches
// GitHub shares.
func openGitHub(ctx context.Context, env Env) (*ghops.GH, error) {
	return ghops.Open(ctx, env.Runner, env.RepoRoot)
}
