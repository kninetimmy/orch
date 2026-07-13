package run

import (
	"context"
	"fmt"

	"github.com/kninetimmy/orch/internal/gitops"
	"github.com/kninetimmy/orch/internal/metrics"
	"github.com/kninetimmy/orch/internal/state"
)

// CompleteSchemaVersion is the complete request/result schema this build
// accepts and emits.
const CompleteSchemaVersion = 1

// CompleteRequest finishes a Delivery run. It carries no issue — complete
// is run-level.
type CompleteRequest struct {
	SchemaVersion int `json:"schema_version"`
}

// CompleteResult reports the finished run and hands the adapter the
// memhub wrap-up cue (PRD §20: only the Architect initiates memory
// writes, so the engine never runs them itself).
type CompleteResult struct {
	SchemaVersion   int    `json:"schema_version"`
	RunID           string `json:"run_id"`
	Merged          int    `json:"merged"`
	Abandoned       int    `json:"abandoned"`
	ReturnedTo      string `json:"returned_to"`
	MemhubWrapupDue bool   `json:"memhub_wrapup_due"`
}

// Complete finishes a Delivery run once every issue is cleaned (PRD §7
// auto-return): it fast-forwards the primary checkout to origin/<default>
// (PRD §12 step 20) and returns the repository to Assist with the lock
// released. It never runs memhub itself — it only signals that a wrap-up
// is due. The git preflights run before any state change, so a failure
// leaves the run in Delivery.
func Complete(ctx context.Context, env Env, reqJSON []byte) (*CompleteResult, error) {
	var req CompleteRequest
	if err := decodeRequest(reqJSON, &req); err != nil {
		return nil, err
	}
	if req.SchemaVersion != CompleteSchemaVersion {
		return nil, fmt.Errorf("%w: schema_version %d is unsupported (this build supports %d)", ErrBadRequest, req.SchemaVersion, CompleteSchemaVersion)
	}

	c, err := loadVerb(env, 0, nil, false)
	if err != nil {
		return nil, err
	}
	for i := range c.st.Run.Issues {
		iss := &c.st.Run.Issues[i]
		if iss.Phase != state.PhaseCleaned {
			return nil, fmt.Errorf("%w: issue #%d is in phase %s, not cleaned", ErrNotAllCleaned, iss.Number, iss.Phase)
		}
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
	branch, err := git.CurrentBranch(ctx)
	if err != nil {
		return nil, err
	}
	if branch != repo.DefaultBranch {
		return nil, fmt.Errorf("primary checkout is on %s, not the default branch %s; completion requires the primary checkout on the default branch", branch, repo.DefaultBranch)
	}
	if err := git.RequireClean(ctx, ""); err != nil {
		return nil, err
	}
	if err := git.FastForward(ctx, "origin", repo.DefaultBranch); err != nil {
		return nil, err
	}

	runID := c.st.Run.ID
	merged, abandoned := terminalCounts(c.st.Run)

	if err := state.CompleteDelivery(env.RepoRoot); err != nil {
		return nil, wrapAfterMutation(err)
	}

	if err := c.recordMetric(metrics.Event{
		Verb:      "complete",
		Merged:    merged,
		Abandoned: abandoned,
	}); err != nil {
		return nil, err
	}

	return &CompleteResult{
		SchemaVersion:   CompleteSchemaVersion,
		RunID:           runID,
		Merged:          merged,
		Abandoned:       abandoned,
		ReturnedTo:      string(state.ModeAssist),
		MemhubWrapupDue: c.cfg.Memhub.Mode != "off",
	}, nil
}

// terminalCounts splits the run's cleaned issues into those that merged
// and those that were abandoned. An approved head OID (pinned only by
// merge-report) marks a merged issue; every other cleaned issue was
// abandoned.
func terminalCounts(run *state.Run) (merged, abandoned int) {
	for i := range run.Issues {
		if run.Issues[i].ApprovedHeadOID != "" {
			merged++
		} else {
			abandoned++
		}
	}
	return merged, abandoned
}
