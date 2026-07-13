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
// accepts and emits.
const DispatchSchemaVersion = 1

// DispatchRequest asks to hand one worktree-ready issue to its executor.
type DispatchRequest struct {
	SchemaVersion int `json:"schema_version"`
	IssueNumber   int `json:"issue_number"`
}

// DispatchResult carries what the adapter needs to spawn the executor
// into the issue's worktree (PRD §12 step 8): the branch, the
// repo-relative worktree, and the routing selection.
type DispatchResult struct {
	SchemaVersion int                `json:"schema_version"`
	IssueNumber   int                `json:"issue_number"`
	Branch        string             `json:"branch"`
	Worktree      string             `json:"worktree"`
	Executor      manifest.Selection `json:"executor"`
	Reviewer      manifest.Selection `json:"reviewer"`
	Rationale     string             `json:"rationale"`
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
		SchemaVersion: DispatchSchemaVersion,
		IssueNumber:   issue.Number,
		Branch:        issue.Branch,
		Worktree:      issue.Worktree,
		Executor:      issue.Decision.Executor,
		Reviewer:      issue.Decision.Reviewer,
		Rationale:     issue.Decision.Rationale,
	}, nil
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
