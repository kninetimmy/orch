package run

import (
	"context"
	"fmt"

	"github.com/kninetimmy/orch/internal/ghops"
	"github.com/kninetimmy/orch/internal/manifest"
	"github.com/kninetimmy/orch/internal/metrics"
	"github.com/kninetimmy/orch/internal/routing"
	"github.com/kninetimmy/orch/internal/state"
)

// EscalateSchemaVersion is the escalate request/result schema this build
// accepts and emits.
const EscalateSchemaVersion = 1

// EscalateRequest raises a PRD §11 escalation on an in-flight issue.
// Trigger is the closed routing.Trigger set; the engine passes it
// through and lets routing reject a mismatch.
type EscalateRequest struct {
	SchemaVersion int    `json:"schema_version"`
	IssueNumber   int    `json:"issue_number"`
	Trigger       string `json:"trigger"`
	Detail        string `json:"detail"`
}

// EscalateResult reports the escalation outcome: a reroute (with the new
// executor/reviewer for the adapter to spawn into the same worktree) or a
// return-to-architect (the issue is blocked for human design work).
type EscalateResult struct {
	SchemaVersion int                 `json:"schema_version"`
	IssueNumber   int                 `json:"issue_number"`
	Kind          string              `json:"kind"`
	Executor      *manifest.Selection `json:"executor,omitempty"`
	Reviewer      *manifest.Selection `json:"reviewer,omitempty"`
	Rationale     string              `json:"rationale,omitempty"`
	Reason        string              `json:"reason"`
}

// Escalate applies a PRD §11 escalation to an in-flight issue. It builds
// the routing profile and history from config and the issue's persisted
// state and runs routing.Escalate (an unknown or mismatched trigger is
// an error). A reroute updates the routing decision, attempts, role
// label, and audit records, transferring the work to a stronger executor
// in place (PRD §11). A return-to-architect blocks the issue for human
// design work.
func Escalate(ctx context.Context, env Env, reqJSON []byte) (*EscalateResult, error) {
	var req EscalateRequest
	if err := decodeRequest(reqJSON, &req); err != nil {
		return nil, err
	}
	if req.SchemaVersion != EscalateSchemaVersion {
		return nil, fmt.Errorf("%w: schema_version %d is unsupported (this build supports %d)", ErrBadRequest, req.SchemaVersion, EscalateSchemaVersion)
	}

	c, err := loadVerb(env, req.IssueNumber, []state.Phase{state.PhaseDispatched, state.PhasePROpen, state.PhaseInReview}, false)
	if err != nil {
		return nil, err
	}
	issue := c.issue()
	if issue.Decision == nil {
		return nil, fmt.Errorf("issue #%d has no routing decision; run `orch abort`", issue.Number)
	}

	profile, err := hostProfile(c.cfg, c.st.Run.Host)
	if err != nil {
		return nil, err
	}
	outcome, err := routing.Escalate(
		profile,
		toRoutingDecision(*issue.Decision),
		toRoutingHistory(issue.Attempts),
		routing.Trigger(req.Trigger),
		req.Detail,
	)
	if err != nil {
		return nil, err
	}

	gh, err := openGitHub(ctx, env)
	if err != nil {
		return nil, err
	}

	switch outcome.Kind {
	case routing.OutcomeReturnToArchitect:
		return escalateReturnToArchitect(ctx, c, gh, outcome)
	case routing.OutcomeReroute:
		return escalateReroute(ctx, env, c, gh, outcome)
	default:
		return nil, fmt.Errorf("routing returned an unknown escalation outcome %q", outcome.Kind)
	}
}

// escalateReroute persists the new decision and attempts, then updates
// the role label and audit records to reflect the transfer (PRD §11).
func escalateReroute(ctx context.Context, env Env, c *verbCtx, gh *ghops.GH, outcome routing.Outcome) (*EscalateResult, error) {
	issue := c.issue()
	stamp := env.nowStamp()
	for i := range outcome.Escalations {
		outcome.Escalations[i].At = stamp
	}
	oldRole := ghRoleLabel(issue.Decision.Role)

	issue.Decision = fromRoutingDecision(outcome.Decision)
	issue.Attempts = fromRoutingHistory(outcome.History)
	if err := c.save(); err != nil {
		return nil, err
	}

	newRole := ghRoleLabel(outcome.Decision.Role)
	if newRole != oldRole {
		if err := gh.SetRole(ctx, issue.Number, newRole); err != nil {
			return nil, wrapAfterMutation(err)
		}
	}

	iss, m, err := readIssueManifest(ctx, gh, issue.Number)
	if err != nil {
		return nil, wrapAfterMutation(err)
	}
	applyDecision(&m, *issue.Decision)
	m.Escalations = append(m.Escalations, outcome.Escalations...)
	pr, err := prForIssue(ctx, gh, issue)
	if err != nil {
		return nil, wrapAfterMutation(err)
	}
	if err := writeManifest(ctx, gh, iss, pr, m); err != nil {
		return nil, wrapAfterMutation(err)
	}

	executor := outcome.Decision.Executor
	reviewer := outcome.Decision.Reviewer

	if err := c.recordMetric(metrics.Event{
		Verb:         "escalate",
		IssueNumber:  issue.Number,
		EscalateKind: string(routing.OutcomeReroute),
		Executor:     &executor,
		Reviewer:     &reviewer,
		Reason:       outcome.Reason,
	}); err != nil {
		return nil, err
	}

	return &EscalateResult{
		SchemaVersion: EscalateSchemaVersion,
		IssueNumber:   issue.Number,
		Kind:          string(routing.OutcomeReroute),
		Executor:      &executor,
		Reviewer:      &reviewer,
		Rationale:     outcome.Decision.Rationale,
		Reason:        outcome.Reason,
	}, nil
}

// escalateReturnToArchitect records the exhausted history, blocks the
// issue, and flags it for human design work (PRD §11).
func escalateReturnToArchitect(ctx context.Context, c *verbCtx, gh *ghops.GH, outcome routing.Outcome) (*EscalateResult, error) {
	issue := c.issue()
	issue.Attempts = fromRoutingHistory(outcome.History)
	issue.Phase = state.PhaseBlocked
	issue.BlockedReason = outcome.Reason
	if err := c.save(); err != nil {
		return nil, err
	}
	if err := gh.SetStatus(ctx, issue.Number, ghops.StatusNeedsHuman); err != nil {
		return nil, wrapAfterMutation(err)
	}

	if err := c.recordMetric(metrics.Event{
		Verb:         "escalate",
		IssueNumber:  issue.Number,
		EscalateKind: string(routing.OutcomeReturnToArchitect),
		Reason:       outcome.Reason,
	}); err != nil {
		return nil, err
	}

	return &EscalateResult{
		SchemaVersion: EscalateSchemaVersion,
		IssueNumber:   issue.Number,
		Kind:          string(routing.OutcomeReturnToArchitect),
		Reason:        outcome.Reason,
	}, nil
}

// ghRoleLabel maps a routed role to its GitHub role label: specialist
// for the specialist role, implementer for every other executor role
// (matching issueLabels).
func ghRoleLabel(r manifest.Role) ghops.Role {
	if r == manifest.RoleSpecialist {
		return ghops.RoleSpecialist
	}
	return ghops.RoleImplementer
}
