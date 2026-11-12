package run

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/kninetimmy/orch/internal/lockfile"
	"github.com/kninetimmy/orch/internal/state"
)

// IssueContextSchemaVersion versions the observation, not a dispatch receipt.
const IssueContextSchemaVersion = 1

// IssueContextDoc projects recorded status and engine guidance for one issue.
// Missing work stays missing; references and selections are not preflighted.
type IssueContextDoc struct {
	SchemaVersion int             `json:"schema_version"`
	Mode          state.Mode      `json:"mode"`
	Consistent    bool            `json:"consistent"`
	Warning       string          `json:"warning,omitempty"`
	Lock          *lockfile.Owner `json:"lock,omitempty"`
	Run           IssueContextRun `json:"run"`
	Issue         state.Issue     `json:"issue"`
	Dependencies  []state.Issue   `json:"dependencies"`
	Guidance      *NextDoc        `json:"guidance"`
	Limitations   []string        `json:"limitations"`
}

// IssueContextRun retains RunView's metadata without unrelated issues.
type IssueContextRun struct {
	ID            string        `json:"id"`
	Host          string        `json:"host"`
	StartedAt     time.Time     `json:"started_at"`
	Plan          state.PlanRef `json:"plan"`
	StoppedReason string        `json:"stopped_reason,omitempty"`
}

// IssueContext inspects a positive issue number without dispatching, attributing
// a session, probing external tools or saving state. Status and next share one
// loaded state/lock pair, so concurrent updates cannot splice different runs,
// phases or routing into proceed advice. The pair itself is still unlocked.
func IssueContext(_ context.Context, env Env, issueNumber int) (*IssueContextDoc, error) {
	if issueNumber <= 0 {
		return nil, fmt.Errorf("%w: issue_number must be positive", ErrBadRequest)
	}
	st, status, err := inspectStatus(env)
	if err != nil {
		return nil, err
	}
	if status.Run == nil {
		return nil, ErrNoDeliveryRun
	}
	var issue *state.Issue
	for i := range status.Run.Issues {
		if status.Run.Issues[i].Number == issueNumber {
			if issue != nil {
				return nil, fmt.Errorf("%w: issue_number %d matches more than one run issue; preserve the run and investigate", ErrUnknownIssue, issueNumber)
			}
			issue = &status.Run.Issues[i]
		}
	}
	if issue == nil {
		return nil, fmt.Errorf("%w: issue_number %d", ErrUnknownIssue, issueNumber)
	}
	guidance, err := next(env, st, status)
	if err != nil {
		return nil, err
	}
	actions := []NextAction{}
	for _, action := range guidance.Actions {
		// Run-level advice has neither an issue number nor a plan ID. An
		// unrelated planned issue can also lack a number, but is not run-level.
		if action.IssueNumber == 0 && action.PlanID == "" || action.IssueNumber == issueNumber {
			actions = append(actions, action)
		}
	}
	guidance.Actions = actions
	dependencyIDs := make(map[string]bool, len(issue.DependsOn))
	for _, dep := range issue.DependsOn {
		dependencyIDs[strings.ToLower(dep)] = true
	}
	dependencies := []state.Issue{}
	for _, candidate := range status.Run.Issues {
		if dependencyIDs[strings.ToLower(candidate.PlanID)] {
			dependencies = append(dependencies, candidate)
		}
	}
	return &IssueContextDoc{
		SchemaVersion: IssueContextSchemaVersion,
		Mode:          status.Mode, Consistent: status.Consistent, Warning: status.Warning, Lock: status.Lock,
		Run: IssueContextRun{
			ID: status.Run.ID, Host: status.Run.Host, StartedAt: status.Run.StartedAt,
			Plan: status.Run.Plan, StoppedReason: status.Run.StoppedReason,
		},
		Issue: *issue, Dependencies: dependencies, Guidance: guidance,
		Limitations: []string{
			"Recorded context is an observation, not dispatch authorization or a dispatch receipt. Missing approved work or routing is not reconstructed.",
			"Branch/worktree references, host selections and PR fields are recorded metadata, not evidence of live existence or readiness. Normal lifecycle verbs retain every preflight and approval gate.",
		},
	}, nil
}
