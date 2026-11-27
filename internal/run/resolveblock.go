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
// stopped-run gate. Resume reconciles ordinary blocks; a risk hold still
// requires its own recovery path after resolving an overlapping secret.
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
	b := issue.CurrentBlock()
	if req.RunID != c.st.Run.ID || b == nil || req.BlockID != b.ID {
		return nil, fmt.Errorf("%w: resolution must identify the current run, issue and original block; unknown legacy blocks require fresh plan approval", ErrBadApproval)
	}
	if b.Cause == state.BlockResidualRisk || (issue.RiskReviewPending != nil && b.PausedRiskBlockID == 0) {
		return nil, fmt.Errorf("%w: generic resolution cannot discharge a residual-risk hold or finish its audit; see docs/residual-risk.md", ErrBadApproval)
	}
	if b.Cause == state.BlockOperational || b.Resolution != nil {
		return nil, fmt.Errorf("%w: block %d has no pending decision; use `orch resume`", ErrBadRequest, b.ID)
	}
	b.Resolution = &state.BlockResolution{ResolvedBy: req.ResolvedBy, Detail: req.Detail, At: env.nowStamp(), Statement: req.Statement}
	issue.BlockedReason = issue.CurrentBlock().Reason
	if err := c.save(); err != nil {
		return nil, err
	}
	if err := c.recordMetric(metrics.Event{Verb: "resolve-block", IssueNumber: issue.Number, Reason: req.Detail}); err != nil {
		return nil, err
	}
	return &ResolveBlockResult{SchemaVersion: ResolveBlockSchemaVersion, IssueNumber: issue.Number, BlockID: b.ID, Phase: issue.Phase}, nil
}

func secretRiskRecovery(b *state.Block) string {
	return fmt.Sprintf("Resolve issue-level secret block %d with `orch run resolve-block` before changed-head review, only if the approved scope and criteria remain unchanged. Releasing the global stop does not resolve this block. After resolution, hold %d still needs its changed-head review path; it cannot be accepted.", b.ID, b.PausedRiskBlockID)
}

func invalidatedRiskRecovery(b *state.Block) string {
	return fmt.Sprintf("Residual-risk hold %d can no longer be accepted. Obtain fresh independent review on a different live head or return through plan approval.", b.ID)
}
