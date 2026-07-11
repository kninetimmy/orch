package run

import (
	"github.com/kninetimmy/orch/internal/routing"
	"github.com/kninetimmy/orch/internal/state"
)

// state mirrors routing's Decision and Attempt (state imports manifest,
// never routing), so the escalate verb converts between the two forms
// here. The field sets are identical; these helpers are the single seam.

// fromRoutingDecision converts a routing.Decision into the persistable
// state.Decision the run records on an issue.
func fromRoutingDecision(d routing.Decision) *state.Decision {
	return &state.Decision{
		Role:               d.Role,
		Executor:           d.Executor,
		Reviewer:           d.Reviewer,
		ReviewerDowngraded: d.ReviewerDowngraded,
		Rationale:          d.Rationale,
	}
}

// toRoutingDecision converts a persisted state.Decision back into the
// routing.Decision the routing package consumes.
func toRoutingDecision(d state.Decision) routing.Decision {
	return routing.Decision{
		Role:               d.Role,
		Executor:           d.Executor,
		Reviewer:           d.Reviewer,
		ReviewerDowngraded: d.ReviewerDowngraded,
		Rationale:          d.Rationale,
	}
}

// fromRoutingHistory converts routing.History into the persistable
// []state.Attempt an issue records.
func fromRoutingHistory(h routing.History) []state.Attempt {
	if len(h) == 0 {
		return nil
	}
	out := make([]state.Attempt, len(h))
	for i, a := range h {
		out[i] = state.Attempt{Role: a.Role, Selection: a.Selection, Failed: a.Failed, Reason: a.Reason}
	}
	return out
}

// toRoutingHistory converts persisted attempts back into routing.History.
func toRoutingHistory(as []state.Attempt) routing.History {
	if len(as) == 0 {
		return nil
	}
	out := make(routing.History, len(as))
	for i, a := range as {
		out[i] = routing.Attempt{Role: a.Role, Selection: a.Selection, Failed: a.Failed, Reason: a.Reason}
	}
	return out
}
