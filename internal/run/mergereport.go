package run

import (
	"context"
	"fmt"

	"github.com/kninetimmy/orch/internal/ghops"
	"github.com/kninetimmy/orch/internal/state"
)

// MergeReportSchemaVersion is the merge-report request/result schema this
// build accepts and emits.
const MergeReportSchemaVersion = 1

// MergeReportRequest asks to pin an approved PR at the merge gate.
type MergeReportRequest struct {
	SchemaVersion int `json:"schema_version"`
	IssueNumber   int `json:"issue_number"`
}

// MergeReportCI is the CI evidence carried in the ready-to-merge report.
type MergeReportCI struct {
	State    string    `json:"state"`
	Required []CICheck `json:"required"`
	Total    int       `json:"total"`
}

// MergeReportResult is the ready-to-merge report the adapter shows at the
// human merge gate (PRD §8, §16).
type MergeReportResult struct {
	SchemaVersion  int           `json:"schema_version"`
	IssueNumber    int           `json:"issue_number"`
	PRNumber       int           `json:"pr_number"`
	PRURL          string        `json:"pr_url"`
	HeadOID        string        `json:"head_oid"`
	MergeStrategy  string        `json:"merge_strategy"`
	CI             MergeReportCI `json:"ci"`
	ReviewCycles   int           `json:"review_cycles"`
	ConfigRevision string        `json:"config_revision"`
	NoCIStatement  string        `json:"no_ci_statement,omitempty"`
}

// MergeReport moves an approved, mergeable issue to awaiting-merge and
// pins the PR's live head OID as the approved head (PRD §8: approval pins
// one PR state). It requires the last review to have approved, the PR to
// be open, and required CI to be passing or absent — a pending or failing
// state fails closed. No-checks is allowed but stated explicitly in the
// report (PRD §16).
func MergeReport(ctx context.Context, env Env, reqJSON []byte) (*MergeReportResult, error) {
	var req MergeReportRequest
	if err := decodeRequest(reqJSON, &req); err != nil {
		return nil, err
	}
	if req.SchemaVersion != MergeReportSchemaVersion {
		return nil, fmt.Errorf("%w: schema_version %d is unsupported (this build supports %d)", ErrBadRequest, req.SchemaVersion, MergeReportSchemaVersion)
	}

	c, err := loadVerb(env, req.IssueNumber, []state.Phase{state.PhaseInReview}, false)
	if err != nil {
		return nil, err
	}
	issue := c.issue()
	if issue.LastReviewVerdict != VerdictApprove {
		return nil, fmt.Errorf("issue #%d has no approving review (last verdict %q); cannot report it ready to merge", issue.Number, issue.LastReviewVerdict)
	}

	gh, err := openGitHub(ctx, env)
	if err != nil {
		return nil, err
	}
	pr, err := gh.PR(ctx, issue.PRNumber)
	if err != nil {
		return nil, err
	}
	if pr.State != "OPEN" {
		return nil, fmt.Errorf("PR #%d is %s, not OPEN; cannot report it ready to merge", pr.Number, pr.State)
	}
	summary, err := gh.RequiredCI(ctx, issue.PRNumber)
	if err != nil {
		return nil, err
	}
	if err := requireMergeableCI(summary.State); err != nil {
		return nil, err
	}

	issue.ApprovedHeadOID = pr.HeadRefOid
	issue.Phase = state.PhaseAwaitingMerge
	if err := c.save(); err != nil {
		return nil, err
	}
	if err := gh.SetStatus(ctx, issue.Number, ghops.StatusNeedsHuman); err != nil {
		return nil, wrapAfterMutation(err)
	}

	result := &MergeReportResult{
		SchemaVersion:  MergeReportSchemaVersion,
		IssueNumber:    issue.Number,
		PRNumber:       pr.Number,
		PRURL:          pr.URL,
		HeadOID:        pr.HeadRefOid,
		MergeStrategy:  c.cfg.Merge.Strategy,
		CI:             MergeReportCI{State: string(summary.State), Required: ciChecks(summary), Total: summary.Total},
		ReviewCycles:   issue.ReviewCycles,
		ConfigRevision: c.cfg.ConfigRevision,
	}
	if summary.State == ghops.CINoChecks {
		result.NoCIStatement = "no required CI checks gate this merge (PRD §16)"
	}
	return result, nil
}

// requireMergeableCI fails closed unless state is passing or no-checks
// (PRD §16): a pending or failing required-CI state blocks the merge
// report, naming the state.
func requireMergeableCI(state ghops.CIState) error {
	switch state {
	case ghops.CIPassing, ghops.CINoChecks:
		return nil
	default:
		return fmt.Errorf("%w: required CI is %s", ErrCINotReady, state)
	}
}
