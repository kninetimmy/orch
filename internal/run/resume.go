// resume.go reconciles an interrupted Delivery run against GitHub and
// git, then advances, adopts, blocks, or unblocks each issue by state
// alone (PRD §23). It is the recovery path a crashed lifecycle verb, an
// out-of-band GitHub action, or a secret-stopped run returns through.
//
// Three strictly separated stages keep the decision auditable and the
// failure semantics pure:
//
//   - observe: every gh/git read is batched up front (one ListWorktrees
//     call total; per-issue reads scoped by phase). Any transport error
//     aborts before a single write.
//   - classify: reconcileIssue is a pure function over the observations —
//     the whole decision table lives there, with no I/O.
//   - apply: outcomes mutate a deep working copy, persisted with ONE
//     state.Save at the end (skipped entirely when nothing changed, so a
//     converged resume is a byte-level no-op).
//
// Resume performs zero external mutations: it never pushes, merges,
// closes, or edits labels or bodies. It only rewrites state.json to match
// what GitHub already proves. Labels self-heal when the next lifecycle
// verb runs.
package run

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/kninetimmy/orch/internal/config"
	"github.com/kninetimmy/orch/internal/ghops"
	"github.com/kninetimmy/orch/internal/gitops"
	"github.com/kninetimmy/orch/internal/lockfile"
	"github.com/kninetimmy/orch/internal/manifest"
	"github.com/kninetimmy/orch/internal/state"
)

// ResumeSchemaVersion is the resume-document schema this build emits.
const ResumeSchemaVersion = 1

// ResumeStoppedRunStatement is the exact statement that authorizes resume
// to clear a secret-stopped run's StoppedReason. A stopped run is fully
// observed, classified, and reported without it, but nothing is written.
const ResumeStoppedRunStatement = "resume-stopped-run"

// ResumeRequest is built by the CLI from flags; it never crosses a
// process boundary, so it carries no schema_version and is never JSON
// decoded.
type ResumeRequest struct {
	// Statement must be "" or ResumeStoppedRunStatement.
	Statement string
	// DryRun observes, classifies, and reports without writing.
	DryRun bool
}

// ResumeAction names what resume did to one issue.
type ResumeAction string

const (
	// ActionKept leaves the issue's phase unchanged.
	ActionKept ResumeAction = "kept"
	// ActionAdvanced moves an issue forward across a crash window GitHub
	// proves complete (activation's AddWorktree→Save gap).
	ActionAdvanced ResumeAction = "advanced"
	// ActionAdoptedPR adopts an orphan PR a crashed pr-open left, moving
	// the issue to pr-open.
	ActionAdoptedPR ResumeAction = "adopted-pr"
	// ActionDemoted returns an awaiting-merge issue to in-review after its
	// PR head moved past the recorded approval.
	ActionDemoted ResumeAction = "demoted"
	// ActionBlocked routes an issue to the blocked phase for human action.
	ActionBlocked ResumeAction = "blocked"
	// ActionUnblocked recovers a blocked issue to a re-derived phase.
	ActionUnblocked ResumeAction = "unblocked"
)

// ResumeObserved records the GitHub/git facts resume read for one issue.
// Pointer fields are nil when the fact was not checked for that phase, so
// the report never conflates "absent" with "not observed".
type ResumeObserved struct {
	IssueState   string `json:"issue_state,omitempty"` // OPEN/CLOSED
	PRNumber     int    `json:"pr_number,omitempty"`
	PRState      string `json:"pr_state,omitempty"` // OPEN/CLOSED/MERGED
	PRHeadOID    string `json:"pr_head_oid,omitempty"`
	CIState      string `json:"ci_state,omitempty"`     // no-checks|pending|failing|passing
	LocalBranch  *bool  `json:"local_branch,omitempty"` // nil = not checked
	Worktree     *bool  `json:"worktree,omitempty"`
	RemoteBranch *bool  `json:"remote_branch,omitempty"`
	ManifestOK   *bool  `json:"manifest_ok,omitempty"`
}

// IssueResume is the per-issue line of the resume report.
type IssueResume struct {
	PlanID      string         `json:"plan_id"`
	Number      int            `json:"number,omitempty"`
	Title       string         `json:"title"`
	PhaseBefore state.Phase    `json:"phase_before"`
	PhaseAfter  state.Phase    `json:"phase_after"`
	Action      ResumeAction   `json:"action"`
	Reason      string         `json:"reason,omitempty"`
	Observed    ResumeObserved `json:"observed"`
}

// ResumeDoc is the report resume returns and the CLI renders. Applied is
// false whenever resume declined to write: on a dry run, or on a stopped
// run reached without the statement.
type ResumeDoc struct {
	SchemaVersion       int           `json:"schema_version"`
	Mode                state.Mode    `json:"mode"`
	RunID               string        `json:"run_id,omitempty"`
	DryRun              bool          `json:"dry_run"`
	Applied             bool          `json:"applied"`
	StoppedReasonBefore string        `json:"stopped_reason_before,omitempty"`
	StoppedReasonAfter  string        `json:"stopped_reason_after,omitempty"`
	Issues              []IssueResume `json:"issues,omitempty"`
	Warnings            []string      `json:"warnings,omitempty"`
}

// issueObservations is one issue's observed GitHub/git facts, the pure
// input reconcileIssue classifies. read is false for the phases that make
// zero API calls (planned, cleaned, abandoned).
type issueObservations struct {
	read          bool
	issueState    string    // "" when the issue was not read
	manifestOK    *bool     // nil when the manifest was not parsed
	manifestErr   error     // set when manifestOK points to false
	verifications int       // verification count in the audit record
	pr            *ghops.PR // issue.PRNumber read (pr-open onward)
	prForBranch   *ghops.PR // open PR for the branch (worktree-ready/dispatched)
	ciRead        bool
	ci            ghops.CIState
	localBranch   *bool
	worktree      *bool
	remoteBranch  *bool
}

// outcome is reconcileIssue's verdict for one issue: the resulting action
// and phase, a human reason, and the field mutations apply performs.
type outcome struct {
	action        ResumeAction
	phase         state.Phase
	reason        string
	adoptPRNumber int
	adoptPRURL    string
	adoptBranch   string
	adoptWorktree string
	// clearApproval drops ApprovedHeadOID and LastReviewVerdict (demotion).
	clearApproval bool
	// warnings are run-level notices appended to ResumeDoc.Warnings.
	warnings []string
}

// resumeCtx is the loaded, validated context resume reconciles from.
type resumeCtx struct {
	env   Env
	cfg   *config.Config
	st    *state.State
	owner *lockfile.Owner
}

// Resume reconciles the active Delivery run against GitHub and continues
// it. It never mutates GitHub or git; the only write is state.json, and
// only when reality and state actually disagree.
func Resume(ctx context.Context, env Env, req ResumeRequest) (*ResumeDoc, error) {
	if req.Statement != "" && req.Statement != ResumeStoppedRunStatement {
		return nil, fmt.Errorf("%w: statement %q is not recognized; the only statement resume accepts is %q", ErrBadRequest, req.Statement, ResumeStoppedRunStatement)
	}

	rc, err := resumeLoad(env)
	if err != nil {
		return nil, err
	}
	if rc == nil {
		// Assist with no lock: nothing to resume, mirroring abort's
		// idempotence (PRD §14).
		return &ResumeDoc{SchemaVersion: ResumeSchemaVersion, Mode: state.ModeAssist, DryRun: req.DryRun}, nil
	}

	issues := rc.st.Run.Issues

	// observe: open gh/git once (only when at least one issue needs a
	// read), then batch every read before classifying.
	observations := make([]issueObservations, len(issues))
	if anyObserves(issues) {
		gh, err := openGitHub(ctx, env)
		if err != nil {
			return nil, err
		}
		git, err := gitops.Open(ctx, env.Runner, env.RepoRoot)
		if err != nil {
			return nil, err
		}
		worktrees, err := git.ListWorktrees(ctx)
		if err != nil {
			return nil, err
		}
		for i := range issues {
			if !phaseObserves(effectiveObsPhase(issues[i])) {
				continue
			}
			obs, err := observeIssue(ctx, gh, git, worktrees, issues[i])
			if err != nil {
				return nil, err
			}
			observations[i] = obs
		}
	}

	// classify: pure, no I/O.
	outcomes := make([]outcome, len(issues))
	for i := range issues {
		outcomes[i] = reconcileIssue(issues[i], observations[i])
	}

	// Build the report from the pre-resume issues and the outcomes; the
	// working copy is only needed to persist.
	doc := &ResumeDoc{
		SchemaVersion:       ResumeSchemaVersion,
		Mode:                rc.st.Mode,
		RunID:               rc.st.Run.ID,
		DryRun:              req.DryRun,
		StoppedReasonBefore: rc.st.Run.StoppedReason,
		StoppedReasonAfter:  rc.st.Run.StoppedReason,
	}
	for i := range issues {
		doc.Issues = append(doc.Issues, IssueResume{
			PlanID:      issues[i].PlanID,
			Number:      issues[i].Number,
			Title:       issues[i].Title,
			PhaseBefore: issues[i].Phase,
			PhaseAfter:  outcomes[i].phase,
			Action:      outcomes[i].action,
			Reason:      outcomes[i].reason,
			Observed:    buildObserved(observations[i]),
		})
		doc.Warnings = append(doc.Warnings, outcomes[i].warnings...)
	}
	if allCleaned(issues) {
		doc.Warnings = append(doc.Warnings, "every issue is cleaned; run `orch run complete` to return to assist")
	}

	// apply: a stopped run without the statement, or any dry run, is
	// observed and reported but never written.
	authorized := !req.DryRun && (rc.st.Run.StoppedReason == "" || req.Statement == ResumeStoppedRunStatement)
	doc.Applied = authorized
	if !authorized {
		return doc, nil
	}

	work, err := cloneState(rc.st)
	if err != nil {
		return nil, err
	}
	changed := false
	for i := range outcomes {
		if applyOutcome(&work.Run.Issues[i], outcomes[i]) {
			changed = true
		}
	}
	if work.Run.StoppedReason != "" && req.Statement == ResumeStoppedRunStatement {
		work.Run.StoppedReason = ""
		doc.StoppedReasonAfter = ""
		changed = true
	}
	if changed {
		if err := state.Save(env.RepoRoot, work); err != nil {
			return nil, err
		}
	}
	return doc, nil
}

// resumeLoad runs resume's preconditions, mirroring loadVerb but
// resume-specific: it fails closed on any inconsistency (that is abort's
// territory), returns a nil ctx with a nil error for the assist +
// no-lock idempotent no-op, and — unlike loadVerb — applies NO stopped-run
// gate and NO per-issue phase gate, because resume must run on stopped and
// blocked runs.
func resumeLoad(env Env) (*resumeCtx, error) {
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
		// CheckConsistent passed, so assist implies no lock: nothing to
		// resume.
		return nil, nil
	}
	cfg, err := config.Load(env.RepoRoot)
	if err != nil {
		return nil, err
	}
	if cfg.ConfigRevision != st.Run.Plan.ConfigRevision {
		return nil, fmt.Errorf("%w: config revision %q does not match the run's %q; run `orch abort`, ship the config change on its own Delivery run, then re-plan", ErrConfigDrift, cfg.ConfigRevision, st.Run.Plan.ConfigRevision)
	}
	return &resumeCtx{env: env, cfg: cfg, st: st, owner: owner}, nil
}

// cloneState returns a deep copy of st via its own persistence encoding,
// so apply mutates a working copy and the loaded state stays intact until
// the single terminal Save.
func cloneState(st *state.State) (*state.State, error) {
	data, err := json.Marshal(st)
	if err != nil {
		return nil, fmt.Errorf("clone state: %w", err)
	}
	var cp state.State
	if err := json.Unmarshal(data, &cp); err != nil {
		return nil, fmt.Errorf("clone state: %w", err)
	}
	return &cp, nil
}

// applyOutcome mutates one issue with o and reports whether anything
// changed, so a converged resume can skip Save entirely. Only the fields
// resume ever rewrites are compared.
func applyOutcome(iss *state.Issue, o outcome) bool {
	changed := false
	if iss.Phase != o.phase {
		iss.Phase = o.phase
		changed = true
	}
	if o.adoptBranch != "" && iss.Branch != o.adoptBranch {
		iss.Branch = o.adoptBranch
		changed = true
	}
	if o.adoptWorktree != "" && iss.Worktree != o.adoptWorktree {
		iss.Worktree = o.adoptWorktree
		changed = true
	}
	if o.adoptPRNumber != 0 && iss.PRNumber != o.adoptPRNumber {
		iss.PRNumber = o.adoptPRNumber
		iss.PRURL = o.adoptPRURL
		changed = true
	}
	if o.clearApproval && (iss.ApprovedHeadOID != "" || iss.LastReviewVerdict != "") {
		iss.ApprovedHeadOID = ""
		iss.LastReviewVerdict = ""
		changed = true
	}
	// A blocked outcome records its reason; every other outcome clears any
	// stale BlockedReason so an unblocked issue leaves the phase cleanly.
	reason := ""
	if o.action == ActionBlocked {
		reason = o.reason
	}
	if iss.BlockedReason != reason {
		iss.BlockedReason = reason
		changed = true
	}
	return changed
}

// --- classify (pure) ---

// reconcileIssue is the pure classifier: it maps one issue and its
// observations onto an outcome via the reconciliation table. A blocked
// issue re-derives an effective phase first (row 30); every emitted
// blocked outcome is guarded so it can only land on an issue that
// satisfies validateIssues (R1).
func reconcileIssue(iss state.Issue, obs issueObservations) outcome {
	var o outcome
	if iss.Phase == state.PhaseBlocked {
		o = rederive(iss, obs)
	} else {
		o = reconcileCore(iss, obs)
	}
	if o.action == ActionBlocked && !canBlock(iss) {
		// R1: planned and issue-created issues (and any issue missing the
		// fields a blocked phase requires) can never be blocked — fall back
		// to keep plus a run-level warning.
		return outcome{
			action: ActionKept, phase: iss.Phase,
			reason:   "kept; classification wanted to block but the issue lacks the number, branch, worktree, or routing decision a blocked phase requires",
			warnings: []string{fmt.Sprintf("issue %s (%s) could not be blocked (missing number, branch, worktree, or routing decision); run `orch abort`", iss.PlanID, iss.Phase)},
		}
	}
	return o
}

// rederive recovers a blocked issue: it hypothesizes an effective phase
// from the populated fields (row 30), reconciles against that phase, and
// — when the result is not itself blocked — applies it with BlockedReason
// cleared. A dispatched-keep hypothesis drops to worktree-ready, the
// deliberate lower bound (re-running dispatch is proven safe and restores
// the staleness-closing fast-forward before pr-open).
func rederive(iss state.Issue, obs issueObservations) outcome {
	hyp := derivedPhase(iss)
	syn := iss
	syn.Phase = hyp
	core := reconcileCore(syn, obs)
	if hyp == state.PhaseDispatched && core.action == ActionKept {
		core.phase = state.PhaseWorktreeReady
		core.reason = "worktree-ready; no PR found — re-run `orch run dispatch` to continue"
	}
	if core.action == ActionBlocked {
		// Stays blocked, but with a freshly derived reason.
		return outcome{action: ActionBlocked, phase: state.PhaseBlocked, reason: core.reason}
	}
	act := ActionUnblocked
	if core.action == ActionAdoptedPR {
		act = ActionAdoptedPR
	}
	return outcome{
		action: act, phase: core.phase, reason: core.reason,
		adoptPRNumber: core.adoptPRNumber, adoptPRURL: core.adoptPRURL,
		adoptBranch: core.adoptBranch, adoptWorktree: core.adoptWorktree,
		clearApproval: core.clearApproval, warnings: core.warnings,
	}
}

// derivedPhase is the effective phase a blocked issue is observed and
// reconciled as, from its populated fields (row 30 re-derivation). The
// no-PR case maps to dispatched so observation reads PRForBranch and the
// row 12 adopt path stays reachable; rederive then lowers a plain
// dispatched keep to worktree-ready.
func derivedPhase(iss state.Issue) state.Phase {
	switch {
	case iss.ApprovedHeadOID != "":
		return state.PhaseAwaitingMerge
	case iss.PRNumber > 0:
		if iss.LastReviewVerdict != "" || iss.ReviewCycles > 0 {
			return state.PhaseInReview
		}
		return state.PhasePROpen
	default:
		return state.PhaseDispatched
	}
}

// reconcileCore runs the reconciliation table for a non-blocked phase.
func reconcileCore(iss state.Issue, obs issueObservations) outcome {
	switch iss.Phase {
	case state.PhasePlanned:
		// Row 1: activation never created the issue; resume cannot.
		return outcome{
			action: ActionKept, phase: state.PhasePlanned,
			reason:   "kept; still planned",
			warnings: []string{fmt.Sprintf("activation did not complete for plan issue %s; resume cannot create issues — run `orch abort` and re-activate", iss.PlanID)},
		}
	case state.PhaseIssueCreated:
		return reconcileIssueCreated(iss, obs)
	case state.PhaseWorktreeReady:
		return reconcileWorktreeReady(iss, obs)
	case state.PhaseDispatched:
		return reconcileDispatched(iss, obs)
	case state.PhasePROpen, state.PhaseInReview:
		return reconcilePROpen(iss, obs)
	case state.PhaseAwaitingMerge:
		return reconcileAwaitingMerge(iss, obs)
	case state.PhaseMerged:
		return reconcileMerged(iss, obs)
	case state.PhaseCleaned, state.PhaseAbandoned:
		// Row 29: terminal, nothing read, nothing to do.
		return outcome{action: ActionKept, phase: iss.Phase, reason: "kept; terminal phase"}
	default:
		return outcome{action: ActionKept, phase: iss.Phase, reason: "kept"}
	}
}

// reconcileIssueCreated handles rows 2-4.
func reconcileIssueCreated(iss state.Issue, obs issueObservations) outcome {
	if obs.issueState == "CLOSED" || manifestBad(obs) {
		// Row 4: a closed issue or an unusable audit record — keep (R1
		// forbids blocking an issue-created issue) with a warning.
		return outcome{
			action: ActionKept, phase: state.PhaseIssueCreated,
			reason:   "kept; issue closed or audit record unusable",
			warnings: []string{fmt.Sprintf("issue #%d is closed outside orch or its audit record is unusable; activation did not finish — run `orch abort` and re-activate", iss.Number)},
		}
	}
	br := present(obs.localBranch)
	wt := present(obs.worktree)
	if br && wt {
		// Row 2: activation crashed between AddWorktree and Save; adopt the
		// derived branch and worktree it already created.
		return outcome{
			action: ActionAdvanced, phase: state.PhaseWorktreeReady,
			adoptBranch:   branchName(iss.Number, iss.Title),
			adoptWorktree: worktreeRel(iss.Number),
			reason:        "advanced to worktree-ready; adopted the branch and worktree activation created",
		}
	}
	// Row 3: keep, naming which half exists.
	return outcome{
		action: ActionKept, phase: state.PhaseIssueCreated,
		reason:   "kept; branch/worktree creation incomplete",
		warnings: []string{fmt.Sprintf("issue #%d has %s; activation did not finish creating its branch and worktree — run `orch abort` and re-activate", iss.Number, halfExists(br, wt))},
	}
}

// reconcileWorktreeReady handles rows 5-9.
func reconcileWorktreeReady(iss state.Issue, obs issueObservations) outcome {
	if o, ok := manifestBlock(iss, obs); ok {
		return o // row 6
	}
	if obs.issueState == "CLOSED" {
		return closedBlock(iss) // row 7
	}
	if o, ok := artifactBlock(iss, obs); ok {
		return o // row 8
	}
	if obs.prForBranch != nil {
		// Row 9: an open PR for a branch the issue never dispatched.
		return blockOut(fmt.Sprintf("open PR #%d exists for branch %s but the issue was never dispatched (out-of-band PR)", obs.prForBranch.Number, iss.Branch))
	}
	// Row 5.
	return outcome{action: ActionKept, phase: state.PhaseWorktreeReady, reason: "kept; branch and worktree present, no PR yet"}
}

// reconcileDispatched handles rows 10-13.
func reconcileDispatched(iss state.Issue, obs issueObservations) outcome {
	if o, ok := manifestBlock(iss, obs); ok {
		return o // row 11 (drift)
	}
	if obs.issueState == "CLOSED" {
		return closedBlock(iss) // row 11 (closed)
	}
	if o, ok := artifactBlock(iss, obs); ok {
		return o // row 11 (missing)
	}
	if obs.prForBranch != nil {
		if obs.verifications >= 1 {
			// Row 12: adopt the ErrPRExists orphan. pr-open writes
			// verifications to the issue body BEFORE CreatePR, so a genuine
			// orphan always carries evidence.
			return outcome{
				action: ActionAdoptedPR, phase: state.PhasePROpen,
				adoptPRNumber: obs.prForBranch.Number, adoptPRURL: obs.prForBranch.URL,
				reason: fmt.Sprintf("adopted PR #%d (open PR for %s with recorded verification evidence)", obs.prForBranch.Number, iss.Branch),
			}
		}
		// Row 13: an open PR with no verification evidence was not orch's.
		return blockOut(fmt.Sprintf("open PR #%d for branch was not opened by orch (audit record carries no verification evidence)", obs.prForBranch.Number))
	}
	// Row 10: a pushed remote branch is a benign pr-open crash window.
	return outcome{action: ActionKept, phase: state.PhaseDispatched, reason: "kept; awaiting pr-open (any pushed remote branch is a benign crash window)"}
}

// reconcilePROpen handles rows 14-19 for pr-open and in-review.
func reconcilePROpen(iss state.Issue, obs issueObservations) outcome {
	if o, ok := manifestBlock(iss, obs); ok {
		return o // row 18
	}
	if o, ok := artifactBlock(iss, obs); ok {
		return o // row 18
	}
	pr := obs.pr
	if pr == nil {
		return blockOut(fmt.Sprintf("PR #%d could not be read", iss.PRNumber))
	}
	switch pr.State {
	case "MERGED":
		// Row 15: never adopt HeadRefOid as ApprovedHeadOID (R2/R3).
		return blockOut(fmt.Sprintf("PR #%d merged out of band; the human merge gate (merge-report + approve-merge) was bypassed", pr.Number))
	case "CLOSED":
		// Row 16.
		return blockOut(fmt.Sprintf("PR #%d closed without merge outside orch; run `orch run abandon` or reopen the PR, then run `orch resume`", pr.Number))
	case "OPEN":
		if obs.issueState == "CLOSED" {
			// Row 17.
			return blockOut(fmt.Sprintf("issue #%d was closed outside orch while its PR #%d is still open", iss.Number, pr.Number))
		}
		// Rows 14/19: keep. CI is reported as observation only — resume
		// never gates on CI.
		return outcome{action: ActionKept, phase: iss.Phase, reason: fmt.Sprintf("kept; PR #%d open (CI %s)", pr.Number, obs.ci)}
	default:
		return blockOut(fmt.Sprintf("PR #%d reads unexpected state %q", pr.Number, pr.State))
	}
}

// reconcileAwaitingMerge handles rows 20-26. A missing branch or worktree
// is observation only here (row 26): cleanup already tolerates absent
// artifacts.
func reconcileAwaitingMerge(iss state.Issue, obs issueObservations) outcome {
	pr := obs.pr
	if pr == nil {
		return blockOut(fmt.Sprintf("PR #%d could not be read", iss.PRNumber))
	}
	switch pr.State {
	case "MERGED":
		if pr.HeadRefOid == iss.ApprovedHeadOID {
			// Row 20: merged at the approved head. A closed issue here is
			// normal (the Closes link), not a contradiction.
			return outcome{action: ActionKept, phase: state.PhaseAwaitingMerge, reason: fmt.Sprintf("kept; PR #%d merged — re-run `orch run merge` with the recorded approval to finish issue closure", pr.Number)}
		}
		// Row 21.
		return blockOut(fmt.Sprintf("PR #%d merged at %s but approval pinned %s; the merged commit was never approved", pr.Number, pr.HeadRefOid, iss.ApprovedHeadOID))
	case "CLOSED":
		// Row 24.
		return blockOut(fmt.Sprintf("PR #%d closed without merge outside orch; run `orch run abandon` or reopen the PR, then run `orch resume`", pr.Number))
	case "OPEN":
		if obs.issueState == "CLOSED" {
			// Row 25.
			return blockOut(fmt.Sprintf("issue #%d was closed outside orch before its PR #%d merged", iss.Number, pr.Number))
		}
		if pr.HeadRefOid == iss.ApprovedHeadOID {
			// Row 22.
			return outcome{action: ActionKept, phase: state.PhaseAwaitingMerge, reason: fmt.Sprintf("kept; PR #%d open at the approved head", pr.Number)}
		}
		// Row 23: the head moved after approval. Demote to in-review,
		// clearing the approval and the stale verdict — the one deliberate
		// backwards move (blocked would be a permanent trap, and a stale
		// approve verdict is the dangerous option: merge-report pins the
		// live head from it).
		return outcome{
			action: ActionDemoted, phase: state.PhaseInReview, clearApproval: true,
			reason: fmt.Sprintf("PR #%d head moved after approval (%s → %s); cleared the approval and returned to review", pr.Number, iss.ApprovedHeadOID, pr.HeadRefOid),
		}
	default:
		return blockOut(fmt.Sprintf("PR #%d reads unexpected state %q", pr.Number, pr.State))
	}
}

// reconcileMerged handles rows 27-28. Branch/worktree presence is
// observation only (row 26); cleanup is next and tolerates their absence.
func reconcileMerged(iss state.Issue, obs issueObservations) outcome {
	pr := obs.pr
	if pr == nil {
		return blockOut(fmt.Sprintf("PR #%d could not be read", iss.PRNumber))
	}
	if pr.State == "MERGED" {
		// Row 27.
		o := outcome{action: ActionKept, phase: state.PhaseMerged, reason: fmt.Sprintf("kept; PR #%d merged", pr.Number)}
		if obs.issueState == "OPEN" {
			o.warnings = []string{fmt.Sprintf("issue #%d reads merged but its GitHub issue is still open; re-run `orch run merge` to finish closure", iss.Number)}
		}
		return o
	}
	// Row 28: GitHub cannot un-merge, so state and GitHub disagree.
	return blockOut(fmt.Sprintf("state records merged but PR #%d reads %s; state and GitHub disagree — investigate before cleanup", pr.Number, pr.State))
}

// manifestBlock returns a blocked outcome and true when the audit record
// is drifted or malformed (rows 6/11/18).
func manifestBlock(iss state.Issue, obs issueObservations) (outcome, bool) {
	if manifestBad(obs) {
		return blockOut(fmt.Sprintf("audit record on issue #%d is %s (%v); repair the managed region by hand, then run `orch resume`", iss.Number, manifestProblem(obs.manifestErr), obs.manifestErr)), true
	}
	return outcome{}, false
}

// artifactBlock returns a blocked outcome and true when the local branch
// or worktree is missing (rows 8/11/18). Resume never recreates git
// artifacts.
func artifactBlock(iss state.Issue, obs issueObservations) (outcome, bool) {
	if !present(obs.localBranch) || !present(obs.worktree) {
		return blockOut(fmt.Sprintf("branch/worktree %s missing; resume never recreates git artifacts — recreate manually or run `orch abort`", iss.Branch)), true
	}
	return outcome{}, false
}

// closedBlock is row 7/11's out-of-band issue-closure block.
func closedBlock(iss state.Issue) outcome {
	return blockOut(fmt.Sprintf("issue #%d was closed outside orch without an abandon statement; reopen it or run `orch run abandon`", iss.Number))
}

func blockOut(reason string) outcome {
	return outcome{action: ActionBlocked, phase: state.PhaseBlocked, reason: reason}
}

// manifestBad reports a parsed-but-unusable audit record.
func manifestBad(obs issueObservations) bool {
	return obs.manifestOK != nil && !*obs.manifestOK
}

// manifestProblem names the manifest fault for the blocked reason.
func manifestProblem(err error) string {
	if errors.Is(err, manifest.ErrDrift) {
		return "drifted"
	}
	return "malformed"
}

// present reports a checked-and-true boolean observation.
func present(p *bool) bool { return p != nil && *p }

// canBlock reports whether iss carries the fields validateIssues requires
// of a blocked issue (R1).
func canBlock(iss state.Issue) bool {
	return iss.Number > 0 && iss.Branch != "" && iss.Worktree != "" && iss.Decision != nil
}

// halfExists names which of the branch/worktree pair activation created,
// for the row 3 warning.
func halfExists(branch, worktree bool) string {
	switch {
	case branch && !worktree:
		return "its branch but no worktree"
	case !branch && worktree:
		return "its worktree but no branch"
	default:
		return "neither its branch nor its worktree"
	}
}

// --- observe (I/O) ---

// anyObserves reports whether at least one issue needs a GitHub/git read,
// so a run made only of planned/cleaned/abandoned issues opens neither.
func anyObserves(issues []state.Issue) bool {
	for i := range issues {
		if phaseObserves(effectiveObsPhase(issues[i])) {
			return true
		}
	}
	return false
}

// effectiveObsPhase is the phase an issue is observed as: a blocked issue
// observes as its re-derived hypothesis so the right facts are read.
func effectiveObsPhase(iss state.Issue) state.Phase {
	if iss.Phase == state.PhaseBlocked {
		return derivedPhase(iss)
	}
	return iss.Phase
}

// phaseObserves reports whether a phase makes any GitHub/git read. The
// planned, cleaned, and abandoned phases make zero API calls.
func phaseObserves(p state.Phase) bool {
	switch p {
	case state.PhasePlanned, state.PhaseCleaned, state.PhaseAbandoned:
		return false
	default:
		return true
	}
}

// observeIssue batches every read one issue's classification needs, scoped
// by its effective phase. A transport error aborts the whole resume (R5);
// a drifted or malformed audit record is a classification fact, not an
// error.
func observeIssue(ctx context.Context, gh *ghops.GH, git *gitops.Git, worktrees []gitops.Worktree, iss state.Issue) (issueObservations, error) {
	obs := issueObservations{read: true}
	p := effectiveObsPhase(iss)

	// At issue-created the stored Branch/Worktree are not yet written, so
	// presence is checked against the names activation derives.
	branch := iss.Branch
	if iss.Phase == state.PhaseIssueCreated {
		branch = branchName(iss.Number, iss.Title)
	}

	// Every observing phase reads the issue for its OPEN/CLOSED state; the
	// audit record is parsed only through awaiting-merge (cleanup never
	// parses bodies, so merged skips it).
	gi, err := gh.Issue(ctx, iss.Number)
	if err != nil {
		return obs, err
	}
	obs.issueState = gi.State
	if parsesManifest(p) {
		m, perr := manifest.Parse(gi.Body)
		if perr != nil {
			no := false
			obs.manifestOK = &no
			obs.manifestErr = perr
		} else {
			yes := true
			obs.manifestOK = &yes
			obs.verifications = len(m.Verifications)
		}
	}

	if readsPRByNumber(p) {
		pr, err := gh.PR(ctx, iss.PRNumber)
		if err != nil {
			return obs, err
		}
		obs.pr = &pr
	}
	if readsPRForBranch(p) {
		pr, err := gh.PRForBranch(ctx, branch)
		if err != nil {
			return obs, err
		}
		obs.prForBranch = pr
	}
	if readsCI(p) {
		ci, err := gh.RequiredCI(ctx, iss.PRNumber)
		if err != nil {
			return obs, err
		}
		obs.ciRead = true
		obs.ci = ci.State
	}

	lb := localBranchPresent(ctx, git, branch)
	obs.localBranch = &lb
	wt := worktreePresent(worktrees, branch)
	obs.worktree = &wt
	if readsRemote(p) {
		rb, err := git.RemoteBranchExists(ctx, "origin", branch)
		if err != nil {
			return obs, err
		}
		obs.remoteBranch = &rb
	}
	return obs, nil
}

// parsesManifest reports the phases whose audit record resume parses:
// issue-created through awaiting-merge.
func parsesManifest(p state.Phase) bool {
	switch p {
	case state.PhaseIssueCreated, state.PhaseWorktreeReady, state.PhaseDispatched,
		state.PhasePROpen, state.PhaseInReview, state.PhaseAwaitingMerge:
		return true
	default:
		return false
	}
}

// readsPRByNumber reports the phases that read the issue's recorded PR.
func readsPRByNumber(p state.Phase) bool {
	switch p {
	case state.PhasePROpen, state.PhaseInReview, state.PhaseAwaitingMerge, state.PhaseMerged:
		return true
	default:
		return false
	}
}

// readsPRForBranch reports the phases that probe for an orphan PR on the
// branch (no recorded PR number yet).
func readsPRForBranch(p state.Phase) bool {
	return p == state.PhaseWorktreeReady || p == state.PhaseDispatched
}

// readsCI reports the phases whose CI state is reported (observation
// only): pr-open and in-review.
func readsCI(p state.Phase) bool {
	return p == state.PhasePROpen || p == state.PhaseInReview
}

// readsRemote reports the phases whose remote branch presence is reported
// (observation only), from the first push at pr-open — plus dispatched,
// where a pushed remote is a benign pr-open crash window.
func readsRemote(p state.Phase) bool {
	switch p {
	case state.PhaseDispatched, state.PhasePROpen, state.PhaseInReview,
		state.PhaseAwaitingMerge, state.PhaseMerged:
		return true
	default:
		return false
	}
}

// localBranchPresent reports whether branch resolves locally. Conservative
// conflation: any rev-parse failure — a missing branch or a git error —
// reads as absent, because resume never recreates a branch, so the worst
// case is a keep or block the human re-runs (ListWorktrees already proved
// git works, so a real git fault would have aborted earlier).
func localBranchPresent(ctx context.Context, git *gitops.Git, branch string) bool {
	_, err := git.RevParse(ctx, "refs/heads/"+branch)
	return err == nil
}

// worktreePresent reports whether a worktree is checked out on branch. A
// worktree on a different branch reads as absent (the safe reading).
func worktreePresent(worktrees []gitops.Worktree, branch string) bool {
	for _, wt := range worktrees {
		if wt.Branch == branch {
			return true
		}
	}
	return false
}

// --- report helpers ---

// buildObserved renders the observations into the report's observed view.
func buildObserved(obs issueObservations) ResumeObserved {
	o := ResumeObserved{IssueState: obs.issueState}
	pr := obs.pr
	if pr == nil {
		pr = obs.prForBranch
	}
	if pr != nil {
		o.PRNumber = pr.Number
		o.PRState = pr.State
		o.PRHeadOID = pr.HeadRefOid
	}
	if obs.ciRead {
		o.CIState = string(obs.ci)
	}
	o.LocalBranch = obs.localBranch
	o.Worktree = obs.worktree
	o.RemoteBranch = obs.remoteBranch
	o.ManifestOK = obs.manifestOK
	return o
}

// allCleaned reports whether every issue reached the cleaned phase.
func allCleaned(issues []state.Issue) bool {
	if len(issues) == 0 {
		return false
	}
	for i := range issues {
		if issues[i].Phase != state.PhaseCleaned {
			return false
		}
	}
	return true
}
