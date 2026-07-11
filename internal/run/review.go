package run

import (
	"context"
	"fmt"

	"github.com/kninetimmy/orch/internal/ghops"
	"github.com/kninetimmy/orch/internal/manifest"
	"github.com/kninetimmy/orch/internal/state"
)

// ReviewSchemaVersion is the review request/result schema this build
// accepts and emits.
const ReviewSchemaVersion = 1

// The two review verdicts (PRD §12 step 11).
const (
	VerdictApprove        = "approve"
	VerdictRequestChanges = "request-changes"
)

// ReviewRequest records one consolidated review cycle for an open PR.
// Reviewer is the selection that actually performed the review; it must
// equal the issue's routed reviewer, so a review run on the wrong model
// fails closed instead of polluting the audit record.
type ReviewRequest struct {
	SchemaVersion   int                `json:"schema_version"`
	IssueNumber     int                `json:"issue_number"`
	ReviewedHeadOID string             `json:"reviewed_head_oid"`
	Verdict         string             `json:"verdict"`
	Summary         string             `json:"summary"`
	Reviewer        manifest.Selection `json:"reviewer"`
}

// ReviewResult reports the recorded review cycle.
type ReviewResult struct {
	SchemaVersion int    `json:"schema_version"`
	IssueNumber   int    `json:"issue_number"`
	ReviewCycles  int    `json:"review_cycles"`
	Verdict       string `json:"verdict"`
}

// Review records one consolidated review cycle (PRD §12 step 11). It
// requires the reporting reviewer to be the issue's routed reviewer
// (PRD §13: the audit record's reviewer is the one that reviewed), and
// enforces PRD §14's "review begins only after the PR stops changing" by
// requiring the reviewed head OID to equal the PR's live head — a stale
// review is rejected so the reviewer re-reads. It advances the issue to
// in-review, appends the cycle to the issue and PR audit records, and,
// on request-changes, sets the status back to in-progress.
func Review(ctx context.Context, env Env, reqJSON []byte) (*ReviewResult, error) {
	var req ReviewRequest
	if err := decodeRequest(reqJSON, &req); err != nil {
		return nil, err
	}
	if req.SchemaVersion != ReviewSchemaVersion {
		return nil, fmt.Errorf("%w: schema_version %d is unsupported (this build supports %d)", ErrBadRequest, req.SchemaVersion, ReviewSchemaVersion)
	}
	if req.Verdict != VerdictApprove && req.Verdict != VerdictRequestChanges {
		return nil, fmt.Errorf("%w: verdict %q is not one of %s, %s", ErrBadRequest, req.Verdict, VerdictApprove, VerdictRequestChanges)
	}
	if req.Summary == "" {
		return nil, fmt.Errorf("%w: review summary must not be empty (PRD §12.11: one consolidated report per cycle)", ErrBadRequest)
	}

	c, err := loadVerb(env, req.IssueNumber, []state.Phase{state.PhasePROpen, state.PhaseInReview}, false)
	if err != nil {
		return nil, err
	}
	issue := c.issue()
	if issue.Decision == nil {
		return nil, fmt.Errorf("issue #%d has no routing decision; run `orch abort`", issue.Number)
	}
	if req.Reviewer != issue.Decision.Reviewer {
		return nil, fmt.Errorf("%w: reviewer %s@%s is not the routed reviewer %s@%s; review with the routed selection, or run `orch run escalate` to reroute first", ErrReviewerMismatch, req.Reviewer.Model, req.Reviewer.Effort, issue.Decision.Reviewer.Model, issue.Decision.Reviewer.Effort)
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
		return nil, fmt.Errorf("PR #%d is %s, not OPEN; cannot review it", pr.Number, pr.State)
	}
	if req.ReviewedHeadOID != pr.HeadRefOid {
		return nil, fmt.Errorf("%w: reviewed head %q is not the PR's live head %q; the PR changed under the reviewer, re-review", ErrReviewStale, req.ReviewedHeadOID, pr.HeadRefOid)
	}

	issue.ReviewCycles++
	issue.LastReviewVerdict = req.Verdict
	issue.Phase = state.PhaseInReview
	if err := c.save(); err != nil {
		return nil, err
	}

	iss, m, err := readIssueManifest(ctx, gh, issue.Number)
	if err != nil {
		return nil, wrapAfterMutation(err)
	}
	m.Verifications = append(m.Verifications, manifest.Verification{
		Name:   fmt.Sprintf("review-cycle-%d", issue.ReviewCycles),
		Result: req.Verdict,
		Detail: truncateDetail(req.Summary),
		At:     env.nowStamp(),
	})
	if err := writeManifest(ctx, gh, iss, &pr, m); err != nil {
		return nil, wrapAfterMutation(err)
	}

	if req.Verdict == VerdictRequestChanges {
		if err := gh.SetStatus(ctx, issue.Number, ghops.StatusInProgress); err != nil {
			return nil, wrapAfterMutation(err)
		}
	}

	return &ReviewResult{
		SchemaVersion: ReviewSchemaVersion,
		IssueNumber:   issue.Number,
		ReviewCycles:  issue.ReviewCycles,
		Verdict:       req.Verdict,
	}, nil
}
