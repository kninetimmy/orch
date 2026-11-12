package run

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/kninetimmy/orch/internal/config"
	"github.com/kninetimmy/orch/internal/state"
)

// NextSchemaVersion versions guidance independently of StatusDoc and state.
const NextSchemaVersion = 1

// NextDoc is local-state advice, not a preflight result or authorization.
// Actions are in recorded issue order, not a new scheduling/routing policy.
type NextDoc struct {
	SchemaVersion int          `json:"schema_version"`
	Mode          state.Mode   `json:"mode"`
	RunID         string       `json:"run_id,omitempty"`
	Actions       []NextAction `json:"actions"`
	Limitations   []string     `json:"limitations"`
}

// NextAction names the work to consider and its outstanding conditions.
// ApprovalRequired describes a separate gate; inspection never satisfies it.
type NextAction struct {
	Action           string      `json:"action"`
	PlanID           string      `json:"plan_id,omitempty"`
	IssueNumber      int         `json:"issue_number,omitempty"`
	Phase            state.Phase `json:"phase,omitempty"`
	Reason           string      `json:"reason"`
	Prerequisites    []string    `json:"prerequisites"`
	Blockers         []string    `json:"blockers,omitempty"`
	ApprovalRequired string      `json:"approval_required,omitempty"`
}

// Next inspects fresh state, lock and effective configuration without calling
// loadVerb (which records sessions), Plan (which can probe memhub), or any
// lifecycle verb. Corrupt/unrecognized state or locks are errors, as in Status.
func Next(_ context.Context, env Env) (*NextDoc, error) {
	st, status, err := inspectStatus(env)
	if err != nil {
		return nil, err
	}
	return next(env, st, status)
}

// next uses the same loaded snapshot as the status representation. In
// particular, issue context must not combine independently loaded work/advice.
func next(env Env, st *state.State, status *StatusDoc) (*NextDoc, error) {
	doc := &NextDoc{SchemaVersion: NextSchemaVersion, Mode: st.Mode, Limitations: []string{
		"Local state and configuration only; no live git, GitHub, CI, worktree, host availability or test checks were performed. Every action still requires its normal verb preflights.",
		"Guidance grants no approval or confirmation and performs no action. Human plan/merge gates and explicit decision resolutions remain separate; existing grant contracts are unchanged and are not evaluated here.",
		"This is an unlocked snapshot, not a reservation; re-inspect after state changes.",
	}}
	if st.Run != nil {
		doc.RunID = st.Run.ID
	}
	if !status.Consistent {
		doc.Actions = []NextAction{{
			Action: "recover", Reason: "State and Delivery lock disagree.",
			Blockers:      []string{status.Warning},
			Prerequisites: []string{"Investigate and preserve the run before explicit `orch abort` recovery; resume cannot repair an inconsistent state/lock pair."},
		}}
		return doc, nil
	}
	if st.Mode == state.ModeAssist {
		if _, err := config.Load(env.RepoRoot); err != nil {
			doc.Actions = []NextAction{{
				Action: "recover", Reason: "Planning configuration cannot be loaded.",
				Blockers:      []string{err.Error()},
				Prerequisites: []string{"Restore valid configuration before gating a plan; inspection does not change configuration."},
			}}
		} else {
			doc.Actions = []NextAction{{
				Action: "plan", Reason: "Assist permits read-only investigation and planning, not tracked-file implementation.",
				Prerequisites:    []string{"Prepare scope, routing inputs, acceptance criteria and required tests; present the `orch run plan` gate before activation."},
				ApprovalRequired: "Explicit plan approval of the effective contract is required before Delivery; this call is not approval.",
			}}
		}
		return doc, nil
	}
	if _, err := loadExecutionConfig(env, st); err != nil {
		doc.Actions = []NextAction{{
			Action: "restore-or-replan", Reason: "The current engine/configuration cannot execute the approved run.",
			Blockers:         []string{err.Error()},
			Prerequisites:    []string{"Preserve the run and follow the blocker remediation; do not migrate active state or treat resume as new authorization."},
			ApprovalRequired: "Changed scope or execution contract requires abort, re-plan and fresh human plan approval.",
		}}
		return doc, nil
	}
	if st.Run.StoppedReason != "" {
		doc.Actions = []NextAction{{
			Action: "recover-stopped-run", Reason: "The whole run is stopped.",
			Blockers:         []string{st.Run.StoppedReason},
			Prerequisites:    []string{"Remediate the stop before explicit `orch resume` recovery or `orch abort`; resume must still reconcile live artifacts and does not resolve pending decision blocks."},
			ApprovalRequired: fmt.Sprintf("Explicit stopped-run recovery requires statement %q; this call neither authorizes recovery nor clears the stop.", ResumeStoppedRunStatement),
		}}
		return doc, nil
	}
	if len(st.Run.Issues) == 0 {
		doc.Actions = []NextAction{{
			Action: "recover", Reason: "An activated run must contain issues.",
			Blockers:      []string{"The recorded run has no issues; do not infer completion."},
			Prerequisites: []string{"Preserve and investigate the run; use explicit abort and a newly approved plan to restart."},
		}}
		return doc, nil
	}
	// Inspection cannot safely address ambiguous identities. State.Load's
	// phase validation does not check these, and dispatch resolves dependencies
	// by case-insensitive plan id while lifecycle verbs address issue numbers.
	ids, numbers := map[string]bool{}, map[int]bool{}
	for _, issue := range st.Run.Issues {
		id := strings.ToLower(issue.PlanID)
		if id == "" || ids[id] || (issue.Number > 0 && numbers[issue.Number]) {
			return nil, fmt.Errorf("cannot determine next actions: missing or duplicate run issue identity (%q, #%d); preserve the run and investigate before explicit `orch abort` recovery", issue.PlanID, issue.Number)
		}
		ids[id], numbers[issue.Number] = true, true
	}
	for _, issue := range st.Run.Issues {
		for _, dep := range issue.DependsOn {
			if !ids[strings.ToLower(dep)] {
				one := issue
				one.DependsOn = []string{dep}
				return nil, checkDependencies(st.Run, &one)
			}
		}
	}
	if allCleaned(st.Run.Issues) {
		doc.Actions = []NextAction{{
			Action: "complete", Reason: "Every recorded issue is cleaned; the run has not yet returned to Assist.",
			Prerequisites: []string{"`orch run complete` must verify the primary checkout is clean and on the default branch, fast-forward it, and release Delivery state/lock; follow its memhub wrap-up cue if due."},
		}}
		return doc, nil
	}
	for i := range st.Run.Issues {
		doc.Actions = append(doc.Actions, nextIssue(st.Run, &st.Run.Issues[i]))
	}
	return doc, nil
}

func nextIssue(run *state.Run, issue *state.Issue) NextAction {
	a := NextAction{PlanID: issue.PlanID, IssueNumber: issue.Number, Phase: issue.Phase, Prerequisites: []string{}}
	active := phaseAllowed(issue.Phase, []state.Phase{state.PhaseWorktreeReady, state.PhaseDispatched, state.PhasePROpen, state.PhaseInReview, state.PhaseAwaitingMerge})
	if active {
		if issue.Decision == nil {
			a.Action, a.Reason = "replan", "Approved work requires a routing decision."
			a.Blockers = []string{fmt.Sprintf("issue #%d has no routing decision; run `orch abort`", issue.Number)}
			a.Prerequisites = []string{"Abort and re-plan; do not invent routing for an approved run."}
			a.ApprovalRequired = "Fresh human plan approval is required."
			return a
		}
		if err := checkApprovedWork(issue); err != nil {
			a.Action, a.Reason = "replan", "Missing approved work cannot be recovered in place."
			a.Blockers, a.Prerequisites = []string{err.Error()}, []string{"Abort, re-plan and re-activate; resume cannot supply new approved work."}
			a.ApprovalRequired = "Fresh human plan approval is required."
			return a
		}
	}
	switch issue.Phase {
	case state.PhasePlanned, state.PhaseIssueCreated:
		a.Action, a.Reason = "recover", "Activation did not finish for this issue."
		a.Blockers = []string{"No dispatchable recorded worktree exists yet."}
		a.Prerequisites = []string{"Investigate with `orch resume --dry-run`; resume can adopt already-created artifacts but cannot create missing issues/branches/worktrees. Incomplete activation may require abort and re-activation with fresh approval."}
	case state.PhaseWorktreeReady:
		a.Action, a.Reason = "dispatch", "The issue is recorded worktree-ready."
		a.Prerequisites = []string{"`orch run dispatch` must fetch origin, fast-forward the issue branch to the live default branch and set in-progress before handing approved work to the routed executor."}
	case state.PhaseDispatched:
		a.Action, a.Reason = "implement", "The routed executor has been dispatched; no PR is recorded yet."
		a.Prerequisites = []string{"Implement only the approved work in the issue worktree, run required tests, commit and push. `orch run pr-open` requires verification evidence, a clean worktree, commits ahead of origin/default and no existing open PR."}
	case state.PhasePROpen, state.PhaseInReview:
		if issue.Phase == state.PhaseInReview && checkApprovingReview(issue) == nil {
			a.Action, a.Reason = "merge-report", "An approving review is recorded, but no merge report has pinned the live head yet."
			a.Prerequisites = []string{"`orch run merge-report` must verify the PR is open and required CI is passing or explicitly absent, then pin its live head and present the report to the human. This guidance has not performed those checks."}
		} else if issue.LastReviewVerdict == VerdictRequestChanges {
			a.Action, a.Reason = "fix-and-review", "The last recorded review requests changes."
			a.Prerequisites = []string{"Have the routed executor fix and push, then the routed reviewer re-review the stable live head and record one reasoned judgment per approved acceptance criterion."}
		} else {
			a.Action, a.Reason = "review", "The PR needs a consolidated review before a merge report."
			a.Prerequisites = []string{"The PR must stop changing; the routed reviewer must review its live head and record a verdict plus one reasoned judgment per approved acceptance criterion with `orch run review`."}
		}
	case state.PhaseAwaitingMerge:
		a.Action, a.Reason = "merge", "A merge report pinned this PR at the merge gate; it is not permission to merge."
		a.Prerequisites = []string{"Present the report and obtain a separate approval before `orch run merge`; the engine must still verify the live PR head/required CI and confirm merge and issue closure."}
		a.ApprovalRequired = fmt.Sprintf("Fresh human approval for PR #%d at head %s with statement %q (or a separately valid existing grant contract, not evaluated here).", issue.PRNumber, issue.ApprovedHeadOID, MergeApprovalStatement)
	case state.PhaseMerged, state.PhaseAbandoned:
		a.Action, a.Reason = "cleanup", "The issue is terminal but its git artifacts are not recorded cleaned."
		a.Prerequisites = []string{"`orch run cleanup` must inspect/remove the remote branch, issue and disposable review worktrees, and local branch; failures leave cleanup incomplete."}
		a.ApprovalRequired = fmt.Sprintf("Explicit deletion confirmation with statement %q is required; guidance supplies no confirmation.", CleanupStatement)
	case state.PhaseBlocked:
		a.Blockers = []string{issue.BlockedReason}
		if issue.DecisionPending() {
			if len(issue.Blocks) == 0 {
				a.Action, a.Reason = "replan", "The block cause is unknown; healthy artifacts cannot establish authorization."
				a.Prerequisites = []string{"Preserve the run, then abort and re-plan; a legacy block cannot be resolved in place."}
				a.ApprovalRequired = "Fresh human plan approval is required."
			} else {
				a.Action, a.Reason = "resolve-block", "The original block requires an explicit human/Architect decision, not operational recovery."
				b := issue.Blocks[len(issue.Blocks)-1]
				a.Prerequisites = []string{fmt.Sprintf("Resolve original block %d (%s) with `orch run resolve-block` only if approved scope and criteria remain unchanged, then use resume to reconcile artifacts; changed scope or criteria requires abort and fresh plan approval.", b.ID, b.Cause)}
				a.ApprovalRequired = fmt.Sprintf("Record the human/Architect resolution and detail with statement %q; this inspection is not that decision.", ResolveBlockStatement)
			}
		} else {
			a.Action, a.Reason = "resume", "The block is operational or its explicit decision resolution is already recorded."
			a.Prerequisites = []string{"Remediate the blocker and use `orch resume` to reconcile live git/GitHub artifacts; inspection cannot establish that recovery succeeded or unblock the issue."}
		}
	case state.PhaseCleaned:
		a.Action, a.Reason = "none", "This issue is cleaned; other issues still prevent run completion."
	default:
		// state.Load rejects unknown phases; keep the classifier fail-closed too.
		a.Action, a.Reason = "recover", "Unsupported lifecycle phase."
		a.Blockers = []string{string(issue.Phase)}
		a.Prerequisites = []string{"Preserve the run and use its original engine, or abort and seek fresh plan approval."}
	}
	if active {
		// Use dispatch's recorded prerequisite rules conservatively for all
		// active work: later phases do not authorize ignoring an abandoned
		// dependency. This changes advice, not any lifecycle verb's gates.
		for _, dep := range issue.DependsOn {
			one := *issue
			one.DependsOn = []string{dep}
			if err := checkDependencies(run, &one); err != nil {
				a.Blockers = append(a.Blockers, err.Error())
				if a.Action != "replan" {
					a.Action, a.Reason = "wait", "Active work is waiting on recorded dependencies."
				}
				if errors.Is(err, ErrDependencyAbandoned) {
					a.Action, a.Reason = "replan", "An abandoned dependency materially changed the plan's scope."
					a.ApprovalRequired = "Return through the human plan gate; do not silently drop or replace the dependency."
				}
			}
		}
	}
	return a
}
