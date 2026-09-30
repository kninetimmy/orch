package run

import (
	"context"
	"fmt"
	"strings"

	"github.com/kninetimmy/orch/internal/metrics"
	"github.com/kninetimmy/orch/internal/state"
)

const ResolveBlockSchemaVersion = 1
const ResolveBlockStatement = "resolve-block-without-scope-change"

// ResolveBlockRequest records a human/Architect decision, not a new plan.
// RunID, IssueNumber and BlockID together identify the original block.
type ResolveBlockRequest struct {
	SchemaVersion int    `json:"schema_version"`
	RunID         string `json:"run_id"`
	IssueNumber   int    `json:"issue_number"`
	BlockID       int    `json:"block_id"`
	ResolvedBy    string `json:"resolved_by"`
	Detail        string `json:"detail"`
	Statement     string `json:"statement"`
}

type ResolveBlockResult struct {
	SchemaVersion int         `json:"schema_version"`
	IssueNumber   int         `json:"issue_number"`
	BlockID       int         `json:"block_id"`
	Phase         state.Phase `json:"phase"`
}

// ResolveBlock persists the resolution before recovery can proceed. It does
// not clear the phase or approvals, reset attempts, amend work, or bypass the
// stopped-run gate. Resume must still reconcile artifacts after this call.
func ResolveBlock(_ context.Context, env Env, reqJSON []byte) (*ResolveBlockResult, error) {
	var req ResolveBlockRequest
	if err := decodeRequest(reqJSON, &req); err != nil {
		return nil, err
	}
	if req.SchemaVersion != ResolveBlockSchemaVersion {
		return nil, fmt.Errorf("%w: schema_version %d is unsupported (this build supports %d)", ErrBadRequest, req.SchemaVersion, ResolveBlockSchemaVersion)
	}
	if req.IssueNumber <= 0 || req.BlockID <= 0 || req.RunID == "" || req.Statement != ResolveBlockStatement || strings.TrimSpace(req.ResolvedBy) == "" || strings.TrimSpace(req.Detail) == "" {
		return nil, fmt.Errorf("%w: resolution requires resolved_by, detail and statement %q; changed scope or criteria require fresh plan approval", ErrBadApproval, ResolveBlockStatement)
	}
	c, err := loadVerb(env, req.IssueNumber, []state.Phase{state.PhaseBlocked}, false)
	if err != nil {
		return nil, err
	}
	issue := c.issue()
	if req.RunID != c.st.Run.ID || req.BlockID != len(issue.Blocks) {
		return nil, fmt.Errorf("%w: resolution must identify the current run, issue and original block; unknown legacy blocks require fresh plan approval", ErrBadApproval)
	}
	b := &issue.Blocks[len(issue.Blocks)-1]
	if b.Cause == state.BlockOperational || b.Resolution != nil {
		return nil, fmt.Errorf("%w: block %d has no pending decision; use `orch resume`", ErrBadRequest, b.ID)
	}
	b.Resolution = &state.BlockResolution{ResolvedBy: req.ResolvedBy, Detail: req.Detail, At: env.nowStamp(), Statement: req.Statement}
	if err := c.save(); err != nil {
		return nil, err
	}
	if err := c.recordMetric(metrics.Event{Verb: "resolve-block", IssueNumber: issue.Number, Reason: req.Detail}); err != nil {
		return nil, err
	}
	return &ResolveBlockResult{SchemaVersion: ResolveBlockSchemaVersion, IssueNumber: issue.Number, BlockID: b.ID, Phase: issue.Phase}, nil
}
