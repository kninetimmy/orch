package run

import (
	"context"
	"fmt"
	"strings"

	"github.com/kninetimmy/orch/internal/ghops"
	"github.com/kninetimmy/orch/internal/gitops"
	"github.com/kninetimmy/orch/internal/manifest"
	"github.com/kninetimmy/orch/internal/metrics"
	"github.com/kninetimmy/orch/internal/state"
)

// DispatchSchemaVersion is the dispatch request/result schema this build
// accepts and emits. v2 added the approved objective, acceptance
// criteria, and required tests to the result, so an adapter's spawn
// prompt is a transcription of what a human approved rather than the
// Architect's recollection of it. v3 adds which of those required tests
// the plan declared the repository's CI does not run.
//
// The version rises for a result-only field because the request's
// version is the one thing that can refuse an adapter predating it. An
// adapter written against v2 transcribes exactly the three fields v2
// named; handed a v3 result it would drop the declaration silently, and
// the executor would never learn that a test the human approved as
// local-only is local-only — the same nobody-runs-it failure the
// declaration exists to surface. Before v3, a request carrying
// schema_version 2 was accepted; this build rejects it with the message
// below, and the remedy is to update the host adapter.
const DispatchSchemaVersion = 3

// DispatchRequest asks to hand one worktree-ready issue to its executor.
type DispatchRequest struct {
	SchemaVersion int `json:"schema_version"`
	IssueNumber   int `json:"issue_number"`
}

// DispatchResult carries what the adapter needs to spawn the executor
// into the issue's worktree (PRD §12 step 8): the branch, the
// repo-relative worktree, the routing selection, and the approved work
// the executor is being handed.
type DispatchResult struct {
	SchemaVersion int                `json:"schema_version"`
	IssueNumber   int                `json:"issue_number"`
	Branch        string             `json:"branch"`
	Worktree      string             `json:"worktree"`
	Executor      manifest.Selection `json:"executor"`
	Reviewer      manifest.Selection `json:"reviewer"`
	Rationale     string             `json:"rationale"`
	// Objective, AcceptanceCriteria, RequiredTests, and TestsCIDoesNotRun
	// are the approved plan text verbatim. The adapter transcribes them
	// into the spawn prompt; it never paraphrases or re-derives them,
	// because what the executor is told to build is what the human
	// approved.
	//
	// TestsCIDoesNotRun names entries of RequiredTests, so the adapter
	// transcribes it against the test it qualifies: the executor is being
	// told this required test is the only thing that will ever run it. It
	// is absent when the plan declared nothing, which is not a claim that
	// CI runs every required test.
	Objective          string   `json:"objective"`
	AcceptanceCriteria []string `json:"acceptance_criteria"`
	RequiredTests      []string `json:"required_tests"`
	TestsCIDoesNotRun  []string `json:"tests_ci_does_not_run,omitempty"`
}

// Dispatch moves a worktree-ready issue to dispatched: it enforces the
// issue's dependencies (every DependsOn issue merged or cleaned; an
// abandoned dependency returns the plan to the gate), fetches origin and
// fast-forwards the issue branch to origin/<default> inside its worktree
// (closing the activation and inter-wave staleness windows), sets the
// issue status to in-progress, and records the phase. The only state
// write is the final Save, so any failure before it leaves the run
// unchanged.
func Dispatch(ctx context.Context, env Env, reqJSON []byte) (*DispatchResult, error) {
	var req DispatchRequest
	if err := decodeRequest(reqJSON, &req); err != nil {
		return nil, err
	}
	if req.SchemaVersion != DispatchSchemaVersion {
		return nil, fmt.Errorf("%w: schema_version %d is unsupported (this build supports %d)", ErrBadRequest, req.SchemaVersion, DispatchSchemaVersion)
	}

	c, err := loadVerb(env, req.IssueNumber, []state.Phase{state.PhaseWorktreeReady}, false)
	if err != nil {
		return nil, err
	}
	issue := c.issue()
	if issue.Decision == nil {
		return nil, fmt.Errorf("issue #%d has no routing decision; run `orch abort`", issue.Number)
	}
	if err := checkApprovedWork(issue); err != nil {
		return nil, err
	}
	if err := checkDependencies(c.st.Run, issue); err != nil {
		return nil, err
	}

	gh, err := openGitHub(ctx, env)
	if err != nil {
		return nil, err
	}
	repo, err := gh.Repo(ctx)
	if err != nil {
		return nil, err
	}
	git, err := gitops.Open(ctx, env.Runner, env.RepoRoot)
	if err != nil {
		return nil, err
	}

	if err := git.Fetch(ctx, "origin"); err != nil {
		return nil, err
	}
	if err := git.FastForwardIn(ctx, c.worktreeAbs(), "origin/"+repo.DefaultBranch); err != nil {
		return nil, err
	}
	if err := gh.SetStatus(ctx, issue.Number, ghops.StatusInProgress); err != nil {
		return nil, err
	}

	issue.Phase = state.PhaseDispatched
	if err := c.save(); err != nil {
		return nil, err
	}

	if err := c.recordMetric(metrics.Event{
		Verb:               "dispatch",
		IssueNumber:        issue.Number,
		Role:               string(issue.Decision.Role),
		Executor:           &issue.Decision.Executor,
		Reviewer:           &issue.Decision.Reviewer,
		ReviewerDowngraded: issue.Decision.ReviewerDowngraded,
		Rationale:          issue.Decision.Rationale,
	}); err != nil {
		return nil, err
	}

	return &DispatchResult{
		SchemaVersion:      DispatchSchemaVersion,
		IssueNumber:        issue.Number,
		Branch:             issue.Branch,
		Worktree:           issue.Worktree,
		Executor:           issue.Decision.Executor,
		Reviewer:           issue.Decision.Reviewer,
		Rationale:          issue.Decision.Rationale,
		Objective:          issue.Objective,
		AcceptanceCriteria: issue.AcceptanceCriteria,
		RequiredTests:      issue.RequiredTests,
		TestsCIDoesNotRun:  issue.TestsCIDoesNotRun,
	}, nil
}

// checkApprovedWork fails closed when the issue carries no approved work
// text. Activation copies it from the plan, so the only way to reach
// dispatch without it is a run activated by a build older than the
// schema-2 audit record — where dispatching anyway would hand the
// executor an empty objective and say nothing about it.
//
// The remediation is abort, never resume. State missing the approved
// work implies an issue body still carrying a schema-1 record, which
// this build's manifest.Parse rejects; resume would therefore take its
// failure path and block the issue rather than repopulate anything,
// leaving the operator worse off than before they followed the advice.
// Naming one concrete command for an unrepairable version mismatch
// follows state.Load's precedent.
func checkApprovedWork(issue *state.Issue) error {
	var missing []string
	if issue.Objective == "" {
		missing = append(missing, "objective")
	}
	if len(issue.AcceptanceCriteria) == 0 {
		missing = append(missing, "acceptance criteria")
	}
	if len(issue.RequiredTests) == 0 {
		missing = append(missing, "required tests")
	}
	if len(missing) == 0 {
		return nil
	}
	return fmt.Errorf("issue #%d carries no %s; this run predates the schema-2 audit record and cannot be repaired in place — run `orch abort` to return to assist, then re-plan and re-activate", issue.Number, strings.Join(missing, ", "))
}

// checkDependencies fails closed unless every issue in issue.DependsOn is
// merged or cleaned. An abandoned dependency is a material scope change
// that must return through the plan gate (PRD §8/§16); an in-flight
// dependency is an ordering violation naming the blocking issue (PRD
// §11 wave order).
func checkDependencies(run *state.Run, issue *state.Issue) error {
	byPlanID := make(map[string]*state.Issue, len(run.Issues))
	for i := range run.Issues {
		byPlanID[strings.ToLower(run.Issues[i].PlanID)] = &run.Issues[i]
	}
	for _, dep := range issue.DependsOn {
		d, ok := byPlanID[strings.ToLower(dep)]
		if !ok {
			return fmt.Errorf("%w: dependency %q does not resolve to a run issue; run `orch abort`", ErrDependencyUnmet, dep)
		}
		switch d.Phase {
		case state.PhaseMerged, state.PhaseCleaned:
			// Satisfied.
		case state.PhaseAbandoned:
			return fmt.Errorf("%w: dependency %s (#%d) was abandoned; the plan's scope changed materially and must return through the plan gate (PRD §8/§16)", ErrDependencyAbandoned, d.PlanID, d.Number)
		default:
			return fmt.Errorf("%w: dependency %s (#%d) is in phase %s, not yet merged or cleaned", ErrDependencyUnmet, d.PlanID, d.Number, d.Phase)
		}
	}
	return nil
}
