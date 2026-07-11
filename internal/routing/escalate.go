package routing

import (
	"fmt"

	"github.com/kninetimmy/orch/internal/manifest"
)

// Trigger names a PRD §11 escalation event a running attempt can raise.
// The set is closed; any other value is ErrBadTrigger.
type Trigger string

const (
	// TriggerScoutUncertainty: a scout reports it cannot resolve the work
	// read-only; escalate to the specialist model in read-only mode.
	TriggerScoutUncertainty Trigger = "scout-uncertainty"
	// TriggerImplementerHardExecution: an implementer finds execution
	// unusually hard; transfer the worktree to a specialist.
	TriggerImplementerHardExecution Trigger = "implementer-hard-execution"
	// TriggerWeakModelFailure: one meaningful failure of an underpowered
	// model; climb one tier rather than retry.
	TriggerWeakModelFailure Trigger = "weak-model-failure"
	// TriggerReviewerUncertainty: a downgraded reviewer reports doubt;
	// escalate to a complete strong-model review. A strong reviewer's
	// doubt is architectural-ambiguity, not this.
	TriggerReviewerUncertainty Trigger = "reviewer-uncertainty"
	// TriggerArchitecturalAmbiguity: unresolved design ambiguity from any
	// role; return to the Architect.
	TriggerArchitecturalAmbiguity Trigger = "architectural-ambiguity"
)

// OutcomeKind distinguishes the two shapes an Escalate result takes.
type OutcomeKind string

const (
	// OutcomeReroute is a new executor and/or reviewer selection to run.
	OutcomeReroute OutcomeKind = "reroute"
	// OutcomeReturnToArchitect hands the task back to the Architect: the
	// escalation ladder is exhausted or the trouble is architectural.
	// Decision is the zero value — there is no executor route to run.
	OutcomeReturnToArchitect OutcomeKind = "return-to-architect"
)

// Outcome is the result of an escalation. On a reroute Decision holds
// the new routing and Escalations records the change(s) for the audit
// manifest; on a return-to-architect Decision is zero and Escalations
// is empty. History is always the input history with any retired
// attempt appended (the correct input to a follow-up Escalate), and
// Reason is a non-empty human-readable summary.
type Outcome struct {
	Kind        OutcomeKind
	Decision    Decision
	Escalations []manifest.Escalation
	History     History
	Reason      string
}

// Escalate applies a PRD §11 escalation to a decision. It appends a
// failed Attempt for every model it retires (one meaningful failure
// retires a model permanently) and emits a manifest.Escalation for each
// selection change with At left empty for the run engine to stamp. A
// trigger that does not fit the decision is ErrTriggerMismatch and an
// unknown trigger is ErrBadTrigger; a return to the Architect is a
// first-class Outcome, never an error.
//
// Cross-cutting rule: any executor reroute on a downgraded-reviewer
// decision also restores the strong reviewer and emits a second
// escalation record — a task that needed escalation was, by evidence,
// not "unsurprising".
func Escalate(p Profile, d Decision, h History, tr Trigger, detail string) (Outcome, error) {
	if err := p.validate(); err != nil {
		return Outcome{}, err
	}

	switch tr {
	case TriggerScoutUncertainty:
		if d.Role != manifest.RoleScout {
			return Outcome{}, fmt.Errorf("%w: scout-uncertainty requires a scout decision, got role %q", ErrTriggerMismatch, d.Role)
		}
		return climbExecutor(p, d, h, tr, detail, manifest.RoleScout, p.Specialist), nil

	case TriggerImplementerHardExecution:
		if d.Role != manifest.RoleImplementer {
			return Outcome{}, fmt.Errorf("%w: implementer-hard-execution requires an implementer decision, got role %q", ErrTriggerMismatch, d.Role)
		}
		return climbExecutor(p, d, h, tr, detail, manifest.RoleSpecialist, p.Specialist), nil

	case TriggerWeakModelFailure:
		switch d.Role {
		case manifest.RoleScout:
			return climbExecutor(p, d, h, tr, detail, manifest.RoleScout, p.Specialist), nil
		case manifest.RoleImplementer:
			return climbExecutor(p, d, h, tr, detail, manifest.RoleSpecialist, p.Specialist), nil
		case manifest.RoleSpecialist:
			// Nowhere stronger to climb: retire the specialist and return
			// to the Architect.
			return Outcome{
				Kind:    OutcomeReturnToArchitect,
				History: appendAttempt(h, retiredExecutor(d, detail)),
				Reason:  reasonSpecialistExhausted(detail),
			}, nil
		default:
			return Outcome{}, fmt.Errorf("%w: weak-model-failure requires an executor role, got %q", ErrTriggerMismatch, d.Role)
		}

	case TriggerReviewerUncertainty:
		if !d.ReviewerDowngraded {
			return Outcome{}, fmt.Errorf("%w: reviewer-uncertainty requires a downgraded reviewer (a strong reviewer's doubt is architectural-ambiguity)", ErrTriggerMismatch)
		}
		return escalateReviewer(p, d, h, detail), nil

	case TriggerArchitecturalAmbiguity:
		return Outcome{
			Kind:    OutcomeReturnToArchitect,
			History: h,
			Reason:  reasonArchitecturalAmbiguity(detail),
		}, nil

	default:
		return Outcome{}, fmt.Errorf("%w: unknown trigger %q", ErrBadTrigger, tr)
	}
}

// climbExecutor retires the current executor and reroutes to target
// under newRole, or returns to the Architect when target is already
// exhausted (its model has failed — including the just-retired one, so a
// degenerate profile whose stronger tier reuses a failed model closes
// here). On a downgraded-reviewer decision a successful reroute also
// restores the strong reviewer with a second escalation record.
func climbExecutor(p Profile, d Decision, h History, tr Trigger, detail string, newRole manifest.Role, target manifest.Selection) Outcome {
	newHistory := appendAttempt(h, retiredExecutor(d, detail))
	if newHistory.FailedModel(target.Model) {
		return Outcome{
			Kind:    OutcomeReturnToArchitect,
			History: newHistory,
			Reason:  reasonExecutorExhausted(d.Role, detail),
		}
	}

	newDecision := Decision{
		Role:               newRole,
		Executor:           target,
		Reviewer:           d.Reviewer,
		ReviewerDowngraded: d.ReviewerDowngraded,
		Rationale:          rationaleReroute(newRole, tr),
	}
	escalations := []manifest.Escalation{{
		Kind:   "escalation",
		Role:   newRole,
		From:   d.Executor,
		To:     target,
		Reason: reasonExecutorReroute(d.Role, tr, detail),
	}}

	if d.ReviewerDowngraded {
		newDecision.Reviewer = p.Reviewer
		newDecision.ReviewerDowngraded = false
		escalations = append(escalations, manifest.Escalation{
			Kind:   "escalation",
			Role:   manifest.RoleReviewer,
			From:   d.Reviewer,
			To:     p.Reviewer,
			Reason: reasonReviewerRestored(detail),
		})
	}

	return Outcome{
		Kind:        OutcomeReroute,
		Decision:    newDecision,
		Escalations: escalations,
		History:     newHistory,
		Reason:      escalations[0].Reason,
	}
}

// escalateReviewer retires the downgraded reviewer and reroutes to the
// strong reviewer, leaving the executor untouched. A degenerate profile
// whose strong reviewer shares the downgrade's model has no stronger
// review to offer and returns to the Architect.
func escalateReviewer(p Profile, d Decision, h History, detail string) Outcome {
	newHistory := appendAttempt(h, Attempt{
		Role:      manifest.RoleReviewer,
		Selection: d.Reviewer,
		Failed:    true,
		Reason:    detail,
	})
	if newHistory.FailedModel(p.Reviewer.Model) {
		return Outcome{
			Kind:    OutcomeReturnToArchitect,
			History: newHistory,
			Reason:  reasonReviewerDegenerate(detail),
		}
	}

	newDecision := d
	newDecision.Reviewer = p.Reviewer
	newDecision.ReviewerDowngraded = false
	newDecision.Rationale = rationaleReviewerEscalated()

	return Outcome{
		Kind:     OutcomeReroute,
		Decision: newDecision,
		Escalations: []manifest.Escalation{{
			Kind:   "escalation",
			Role:   manifest.RoleReviewer,
			From:   d.Reviewer,
			To:     p.Reviewer,
			Reason: reasonReviewerUncertainty(detail),
		}},
		History: newHistory,
		Reason:  reasonReviewerUncertainty(detail),
	}
}

// retiredExecutor is the failed Attempt for a decision's executor.
func retiredExecutor(d Decision, detail string) Attempt {
	return Attempt{
		Role:      d.Role,
		Selection: d.Executor,
		Failed:    true,
		Reason:    detail,
	}
}

// appendAttempt returns h with a appended, never aliasing the caller's
// backing array, so a caller that round-trips History keeps its own copy
// intact.
func appendAttempt(h History, a Attempt) History {
	out := make(History, len(h), len(h)+1)
	copy(out, h)
	return append(out, a)
}
