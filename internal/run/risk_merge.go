package run

import (
	"context"
	"fmt"
	"strings"

	"github.com/kninetimmy/orch/internal/ghops"
)

func verifyRiskMerge(ctx context.Context, c *verbCtx, gh *ghops.GH, head string) (riskAudit, ghops.PR, ghops.CISummary, error) {
	var pr ghops.PR
	var summary ghops.CISummary
	a, err := openRiskAudit(ctx, c, gh, head, true)
	if err != nil {
		return a, pr, summary, err
	}
	binding, err := a.binding(c)
	if err != nil {
		return a, pr, summary, err
	}
	is := c.issue()
	if err := checkApprovingReview(is); err != nil {
		return a, pr, summary, err
	}
	_, record, err := currentRiskReview(c, a, binding)
	if err != nil {
		return a, pr, summary, err
	}
	if err := verifyRiskFollowUp(ctx, c, a, binding, is.LatestRiskBlock(), record.FollowUp, is.ReviewCycles); err != nil {
		return a, pr, summary, err
	}
	summary, err = strictRiskCI(c, a)
	if err != nil {
		return a, pr, summary, err
	}
	p := a.snapshot.Repository.PullRequest
	pr = ghops.PR{Number: p.Number, URL: p.URL, HeadRefOid: p.HeadRefOid, State: p.State}
	return a, pr, summary, nil
}

func strictRiskCI(c *verbCtx, a riskAudit) (ghops.CISummary, error) {
	checks, err := a.snapshot.RiskCI(a.policy, c.env.now())
	if err != nil {
		return ghops.CISummary{}, fmt.Errorf("%w: %v", ErrCINotReady, err)
	}
	summary := ghops.CISummary{State: ghops.CIPassing, Total: len(checks)}
	for _, check := range checks {
		summary.Required = append(summary.Required, ghops.Check{Name: check.Context, State: check.Conclusion, Bucket: "pass"})
	}
	return summary, nil
}

func mergeResidualRisk(ctx context.Context, c *verbCtx, req MergeRequest) (*MergeResult, error) {
	is := c.issue()
	approval := req.Approval
	actor := strings.TrimSpace(approval.ApprovedBy)
	if req.CIException != nil || approval.Statement != MergeApprovalStatement || runGrantID(c.st) != "" {
		return nil, fmt.Errorf("%w: residual-risk merge cannot combine with manual CI or a grant", ErrMergeApproval)
	}
	if is.RiskMergeReportAt == nil || approval.PRNumber != is.PRNumber || approval.HeadOID != is.ApprovedHeadOID ||
		approval.RepairNumber != len(is.Repairs) || actor == "" || strings.HasPrefix(actor, grantApproverPrefix) ||
		!approval.ApprovedAt.After(*is.RiskMergeReportAt) || approval.ApprovedAt.After(c.env.now()) {
		return nil, fmt.Errorf("%w: obtain separate fresh human approve-merge after this residual-risk merge report for the pinned PR/head and current repair", ErrMergeApproval)
	}
	gh, err := openGitHub(ctx, c.env)
	if err != nil {
		return nil, err
	}
	// This accepts only OPEN. In particular it cannot claim an out-of-band
	// merge, even if its head matches the old report.
	a, pr, _, err := verifyRiskMerge(ctx, c, gh, approval.HeadOID)
	if err != nil {
		return nil, err
	}
	if err := a.target.MergeRiskPR(ctx, a.snapshot, c.cfg.Merge.Strategy, approval.HeadOID, ghops.ExplicitConfirmation()); err != nil {
		return nil, err
	}
	pr.State = "MERGED"
	return finishMerge(ctx, c, gh, pr, actor, "", false)
}
