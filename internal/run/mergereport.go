package run

import (
	"context"
	"fmt"
	"time"

	"github.com/kninetimmy/orch/internal/ghops"
	"github.com/kninetimmy/orch/internal/manifest"
	"github.com/kninetimmy/orch/internal/metrics"
	"github.com/kninetimmy/orch/internal/state"
)

// MergeReportSchemaVersion is the merge-report request/result schema this
// build accepts and emits.
const MergeReportSchemaVersion = 1

// MergeReportRequest asks to pin an approved PR at the merge gate.
type MergeReportRequest struct {
	SchemaVersion int               `json:"schema_version"`
	IssueNumber   int               `json:"issue_number"`
	CIException   *HumanCIException `json:"ci_exception,omitempty"`
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
	SchemaVersion     int                    `json:"schema_version"`
	IssueNumber       int                    `json:"issue_number"`
	PRNumber          int                    `json:"pr_number"`
	PRURL             string                 `json:"pr_url"`
	HeadOID           string                 `json:"head_oid"`
	MergeStrategy     string                 `json:"merge_strategy"`
	CI                MergeReportCI          `json:"ci"`
	ReviewCycles      int                    `json:"review_cycles"`
	ConfigRevision    string                 `json:"config_revision"`
	NoCIStatement     string                 `json:"no_ci_statement,omitempty"`
	RepairNumber      int                    `json:"repair_number,omitempty"`
	CIException       *manifest.Verification `json:"ci_exception,omitempty"`
	RiskMergeReportAt *time.Time             `json:"risk_merge_report_at,omitempty"`
}

// MergeReport moves an approved, mergeable issue to awaiting-merge and
// pins the PR's live head OID as the approved head (PRD §8: approval pins
// one PR state). It requires the last review to have approved, the PR to
// be open, and required CI to be passing or absent — a pending or failing
// state fails closed. No-checks is allowed but stated explicitly in the
// report (PRD §16).
// Before #517 that was the only path. The default still holds; an explicit
// request-local human CI exception additionally accepts pending/failing data,
// never a probe error, after validating the independently reviewed live head.
// Before #528 the ordinary path could pin a newer unreviewed live head. Now
// both paths require the durable, successfully recorded review at that head.
func MergeReport(ctx context.Context, env Env, reqJSON []byte) (*MergeReportResult, error) {
	var req MergeReportRequest
	if err := decodeRequest(reqJSON, &req); err != nil {
		return nil, err
	}
	if req.SchemaVersion != MergeReportSchemaVersion {
		return nil, fmt.Errorf("%w: schema_version %d is unsupported (this build supports %d)", ErrBadRequest, req.SchemaVersion, MergeReportSchemaVersion)
	}
	if req.IssueNumber <= 0 {
		return nil, fmt.Errorf("%w: issue_number must be positive", ErrBadRequest)
	}

	c, err := loadVerb(env, req.IssueNumber, []state.Phase{state.PhaseInReview}, false)
	if err != nil {
		return nil, err
	}
	issue := c.issue()
	if err := checkApprovingReview(issue); err != nil {
		return nil, err
	}
	risk := issue.LatestRiskBlock() != nil
	if risk && req.CIException != nil {
		return nil, fmt.Errorf("%w: residual-risk merge report cannot use a manual CI exception", ErrBadApproval)
	}
	if req.CIException != nil {
		if err := checkHumanCIException(c, req.CIException, CIExceptionStatement); err != nil {
			return nil, err
		}
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
	if issue.ReviewedHeadOID != pr.HeadRefOid {
		return nil, fmt.Errorf("%w: recorded reviewed head %q differs from live PR head %q; obtain fresh routed review", ErrReviewStale, issue.ReviewedHeadOID, pr.HeadRefOid)
	}
	if err := checkRepairReview(issue, pr.HeadRefOid); err != nil {
		return nil, err
	}
	var summary ghops.CISummary
	var receipt *manifest.Verification
	if risk {
		_, _, summary, err = verifyRiskMerge(ctx, c, gh, pr.HeadRefOid)
		if err != nil {
			return nil, err
		}
		at := env.now()
		issue.RiskMergeReportAt = &at
	} else if req.CIException == nil {
		summary, err = gh.RequiredCI(ctx, issue.PRNumber)
		if err != nil {
			return nil, err
		}
		if err := requireMergeableCI(summary.State); err != nil {
			return nil, err
		}
	} else {
		var iss ghops.Issue
		var m manifest.Manifest
		iss, m, summary, err = observeManualCI(ctx, c, gh, pr, req.CIException)
		if err != nil {
			return nil, err
		}
		receipt, err = recordManualCI(ctx, c, gh, iss, pr, m, summary, req.CIException)
		if err != nil {
			return nil, err
		}
	}

	issue.ApprovedHeadOID = pr.HeadRefOid
	issue.Phase = state.PhaseAwaitingMerge
	if err := c.save(); err != nil {
		if req.CIException != nil {
			return nil, wrapAfterMutation(err) // The CI acceptance audit was already written.
		}
		return nil, err
	}
	if err := gh.SetStatus(ctx, issue.Number, ghops.StatusNeedsHuman); err != nil {
		return nil, wrapAfterMutation(err)
	}

	if err := c.recordMetric(metrics.Event{
		Verb:        "merge-report",
		IssueNumber: issue.Number,
	}); err != nil {
		return nil, err
	}

	result := mergeReportResult(issue, pr, summary, c.cfg.Merge.Strategy, c.cfg.ConfigRevision)
	result.CIException = receipt
	return result, nil
}

// mergeReportResult formats evidence only. Calling it does not record a gate;
// MergeReport retains all of its preconditions and mutations above.
func mergeReportResult(issue *state.Issue, pr ghops.PR, summary ghops.CISummary, strategy, revision string) *MergeReportResult {
	result := &MergeReportResult{
		SchemaVersion:     MergeReportSchemaVersion,
		IssueNumber:       issue.Number,
		PRNumber:          pr.Number,
		PRURL:             pr.URL,
		HeadOID:           pr.HeadRefOid,
		MergeStrategy:     strategy,
		CI:                MergeReportCI{State: string(summary.State), Required: ciChecks(summary), Total: summary.Total},
		ReviewCycles:      issue.ReviewCycles,
		ConfigRevision:    revision,
		RiskMergeReportAt: issue.RiskMergeReportAt,
	}
	if summary.State == ghops.CINoChecks {
		result.NoCIStatement = "no required CI checks gate this merge (PRD §16)"
	}
	if repair := latestRepair(issue); repair != nil {
		result.RepairNumber = repair.Number
	}
	return result
}

// checkApprovingReview is the local prerequisite for merge-report. Next uses
// it without claiming the live PR/CI checks below have been performed.
func checkApprovingReview(issue *state.Issue) error {
	if issue.LastReviewVerdict != VerdictApprove {
		return fmt.Errorf("issue #%d has no approving review (last verdict %q); cannot report it ready to merge", issue.Number, issue.LastReviewVerdict)
	}
	if issue.ReviewedHeadOID == "" || len(issue.PendingEvidence) > 0 || issue.RiskReviewPending != nil {
		return fmt.Errorf("%w: issue #%d requires a fully recorded review with no pending evidence", ErrReviewStale, issue.Number)
	}
	if repair := latestRepair(issue); repair != nil {
		return checkRepairReview(issue, repair.VerifiedHeadOID)
	}
	return nil
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
