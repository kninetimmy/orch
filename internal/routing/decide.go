package routing

import (
	"fmt"

	"github.com/kninetimmy/orch/internal/manifest"
)

// Decision is the routing chosen for a task: the role and the exact
// executor and reviewer selections, plus whether the reviewer was
// downgraded and a human-readable rationale (PRD §13). The rationale is
// always non-empty and feeds manifest.RoutingRationale verbatim.
type Decision struct {
	Role               manifest.Role
	Executor           manifest.Selection
	Reviewer           manifest.Selection
	ReviewerDowngraded bool
	Rationale          string
}

// Decide chooses the role, executor selection, and reviewer for a task
// against a profile and the history of prior attempts (PRD §9–§11). It
// applies the first-match-wins policy table, resolving conflicts toward
// the stronger route (PRD §11: uncertainty favors the stronger route)
// and recording any refused downgrade in the rationale rather than
// erroring. A nil history is a fresh task.
//
// It fails closed: an incomplete profile is ErrBadProfile and an
// unknown risk domain is ErrBadTask. When the only stronger executor a
// row calls for has already failed, initial routing is exhausted and it
// returns ErrNoStrongerRoute (a plan-gate problem, distinct from an
// escalation returning to the Architect).
func Decide(p Profile, t Task, h History) (Decision, error) {
	if err := p.validate(); err != nil {
		return Decision{}, err
	}
	for _, d := range t.RiskDomains {
		if !d.Valid() {
			return Decision{}, fmt.Errorf("%w: unknown risk domain %q", ErrBadTask, d)
		}
	}

	risk := len(t.RiskDomains) > 0
	difficult := t.UnusuallyDifficult

	switch {
	// Row 1: read-only work that is risky, difficult, or whose scout
	// model already failed runs on the specialist model in read-only
	// mode (role stays scout).
	case t.ReadOnly && (risk || difficult || h.FailedModel(p.Scout.Model)):
		if h.FailedModel(p.Specialist.Model) {
			return Decision{}, fmt.Errorf("%w: read-only work needs the specialist model but it has already failed", ErrNoStrongerRoute)
		}
		return Decision{
			Role:      manifest.RoleScout,
			Executor:  p.Specialist,
			Reviewer:  p.Reviewer,
			Rationale: rationaleScoutEscalated(t, h, p),
		}, nil

	// Row 2: plain read-only work goes to a scout.
	case t.ReadOnly:
		return Decision{
			Role:      manifest.RoleScout,
			Executor:  p.Scout,
			Reviewer:  p.Reviewer,
			Rationale: rationaleScoutPlain(),
		}, nil

	// Row 3: risky, difficult, or implementer-model-failed work goes to a
	// specialist with a strong reviewer. Review is never downgraded here,
	// even if all four downgrade facts hold — the refusal is recorded in
	// the rationale.
	case risk || difficult || h.FailedModel(p.Implementer.Model):
		if h.FailedModel(p.Specialist.Model) {
			return Decision{}, fmt.Errorf("%w: work needs the specialist model but it has already failed", ErrNoStrongerRoute)
		}
		return Decision{
			Role:      manifest.RoleSpecialist,
			Executor:  p.Specialist,
			Reviewer:  p.Reviewer,
			Rationale: rationaleSpecialist(t, h, p),
		}, nil

	// Row 4: a change that is affirmatively mechanical, low-risk, fully
	// specified, and unsurprising earns a downgraded reviewer.
	case t.Downgrade.Eligible():
		return Decision{
			Role:               manifest.RoleImplementer,
			Executor:           p.Implementer,
			Reviewer:           p.ReviewDowngrade,
			ReviewerDowngraded: true,
			Rationale:          rationaleDowngrade(),
		}, nil

	// Row 5: default — a normal change goes to an implementer with a
	// strong reviewer.
	default:
		return Decision{
			Role:      manifest.RoleImplementer,
			Executor:  p.Implementer,
			Reviewer:  p.Reviewer,
			Rationale: rationaleImplementer(t),
		}, nil
	}
}
