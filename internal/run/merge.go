package run

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/kninetimmy/orch/internal/ghops"
	"github.com/kninetimmy/orch/internal/grant"
	"github.com/kninetimmy/orch/internal/manifest"
	"github.com/kninetimmy/orch/internal/metrics"
	"github.com/kninetimmy/orch/internal/state"
)

// MergeSchemaVersion is the merge request/result schema this build
// accepts and emits.
const MergeSchemaVersion = 1

// MergeApprovalStatement is the exact assertion a human merge approval
// must carry (PRD §8): a fresh approval per PR, pinned to the head OID
// the human saw. The engine cannot verify a human, so this recorded
// string is the proof one approved this specific merge. Before #353 it
// was the only statement merge accepted; now GrantMergeApprovalStatement
// (grantgate.go) also merges, past its stops, in a run a grant activated.
const MergeApprovalStatement = "approve-merge"

// MergeApproval is the adapter's record of the human's merge approval,
// pinned to one PR at one head commit.
type MergeApproval struct {
	PRNumber   int       `json:"pr_number"`
	HeadOID    string    `json:"head_oid"`
	ApprovedBy string    `json:"approved_by"`
	ApprovedAt time.Time `json:"approved_at"`
	Statement  string    `json:"statement"`
	// Repaired issues require an explicit acknowledgement of the latest
	// authorization; a pre-repair approval cannot be replayed at the same OID.
	RepairNumber int `json:"repair_number,omitempty"`
}

// MergeRequest asks to merge an approved, awaiting-merge issue, or separately
// acknowledge an already-merged reviewed PR without asserting prior approval.
type MergeRequest struct {
	SchemaVersion        int               `json:"schema_version"`
	IssueNumber          int               `json:"issue_number"`
	Approval             MergeApproval     `json:"approval"`
	CIException          *HumanCIException `json:"ci_exception,omitempty"`
	MergedAcknowledgment *HumanCIException `json:"merged_acknowledgment,omitempty"`
}

// MergeResult reports the completed merge.
type MergeResult struct {
	SchemaVersion int                     `json:"schema_version"`
	IssueNumber   int                     `json:"issue_number"`
	PRNumber      int                     `json:"pr_number"`
	Merged        bool                    `json:"merged"`
	ManualCIAudit []manifest.Verification `json:"manual_ci_audit,omitempty"`
}

// Merge merges an approved PR at the human merge gate (PRD §12 steps
// 16-17). The approval must carry the exact statement and pin the PR and
// head OID the engine recorded at merge-report, which must still equal
// the PR's live head — any drift fails closed. The verb is re-runnable
// after a crash: if the PR already reads MERGED it skips the merge and
// proceeds to closure confirmation and the phase transition.
//
// A grant approval (GrantMergeApprovalStatement) additionally passes every
// grant stop (mergeGrant), requires passing required CI (no-checks is not
// enough) and a complete PR file list touching no protected path, and
// spends its merge unit before `gh pr merge`, so a failure after the merge
// never leaves the grant past its limit and a re-run spends nothing more.
// A PR that reads MERGED with no unit spent was not merged by the grant,
// so the grant refuses to record it.
// Request-local human CI exceptions never authorize a grant. Already-merged
// acknowledgment is a separate human recovery path, not a merge approval.
func Merge(ctx context.Context, env Env, reqJSON []byte) (*MergeResult, error) {
	var req MergeRequest
	if err := decodeRequest(reqJSON, &req); err != nil {
		return nil, err
	}
	if req.SchemaVersion != MergeSchemaVersion {
		return nil, fmt.Errorf("%w: schema_version %d is unsupported (this build supports %d)", ErrBadRequest, req.SchemaVersion, MergeSchemaVersion)
	}
	if req.IssueNumber <= 0 {
		return nil, fmt.Errorf("%w: issue_number must be positive", ErrBadRequest)
	}
	if req.MergedAcknowledgment != nil {
		return acknowledgeMerged(ctx, env, req)
	}
	byGrant := req.Approval.Statement == GrantMergeApprovalStatement
	if byGrant && req.CIException != nil {
		return nil, grantStop("manual CI exceptions require a separate human merge approval, never a grant")
	}
	if req.Approval.Statement != MergeApprovalStatement && !byGrant {
		return nil, fmt.Errorf("%w: approval statement %q does not equal %q", ErrMergeApproval, req.Approval.Statement, MergeApprovalStatement)
	}

	c, err := loadVerb(env, req.IssueNumber, []state.Phase{state.PhaseAwaitingMerge}, false)
	if err != nil {
		return nil, err
	}
	issue := c.issue()
	if req.Approval.PRNumber != issue.PRNumber {
		return nil, fmt.Errorf("%w: approval pr_number %d does not match issue #%d's PR %d", ErrMergeApproval, req.Approval.PRNumber, issue.Number, issue.PRNumber)
	}
	if req.Approval.HeadOID != issue.ApprovedHeadOID {
		return nil, fmt.Errorf("%w: approval head_oid %q does not match the approved head %q pinned at merge-report; re-run merge-report", ErrMergeApproval, req.Approval.HeadOID, issue.ApprovedHeadOID)
	}
	if repair := latestRepair(issue); repair != nil {
		at, err := time.Parse(time.RFC3339, repair.At)
		if err != nil || req.Approval.RepairNumber != repair.Number || !req.Approval.ApprovedAt.After(at) {
			return nil, fmt.Errorf("%w: integration repair %d invalidated earlier merge approvals; obtain a new approval with repair_number %d after %s", ErrMergeApproval, repair.Number, repair.Number, repair.At)
		}
		if err := checkRepairReview(issue, issue.ApprovedHeadOID); err != nil {
			return nil, err
		}
	}
	if req.CIException != nil {
		if err := checkHumanCIException(c, req.CIException, CIExceptionStatement); err != nil {
			return nil, err
		}
		if strings.TrimSpace(req.Approval.ApprovedBy) == "" ||
			strings.HasPrefix(strings.TrimSpace(req.Approval.ApprovedBy), grantApproverPrefix) ||
			req.Approval.ApprovedAt.Before(req.CIException.ApprovedAt) || req.Approval.ApprovedAt.After(env.now()) {
			return nil, fmt.Errorf("%w: CI acceptance is not merge approval; obtain separate fresh human approve-merge confirmation", ErrMergeApproval)
		}
	}
	var grants *grant.Store
	var g *grant.Grant
	if byGrant {
		if grants, g, err = mergeGrant(ctx, c, req.Approval.ApprovedBy); err != nil {
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
	if req.Approval.HeadOID != pr.HeadRefOid {
		return nil, fmt.Errorf("%w: the PR moved to head %q after approval of %q; re-run merge-report", ErrMergeApproval, pr.HeadRefOid, req.Approval.HeadOID)
	}
	if pr.State != "OPEN" && pr.State != "MERGED" {
		return nil, fmt.Errorf("%w: PR #%d is %s, not OPEN or MERGED", ErrMergeApproval, pr.Number, pr.State)
	}
	var summary ghops.CISummary
	if req.CIException != nil {
		iss, m, observed, err := observeManualCI(ctx, c, gh, pr, req.CIException)
		if err != nil {
			return nil, err
		}
		summary = observed
		if _, err := recordManualCI(ctx, c, gh, iss, pr, m, summary, req.CIException); err != nil {
			return nil, err
		}
	}

	if pr.State != "MERGED" {
		if req.CIException == nil {
			summary, err = gh.RequiredCI(ctx, issue.PRNumber)
			if err != nil {
				return nil, err
			}
		}
		if byGrant && summary.State != ghops.CIPassing {
			return nil, grantStop("required CI is %s, not passing", summary.State)
		}
		if err := requireMergeableCI(summary.State); err != nil && req.CIException == nil {
			return nil, err
		}
		if byGrant {
			files, err := gh.PRFiles(ctx, issue.PRNumber, issue.ApprovedHeadOID)
			if err != nil {
				return nil, grantStop("the PR's complete file list could not be confirmed: %v", err)
			}
			if p := protectedFiles(files, c.cfg); len(p) > 0 {
				return nil, grantStop("PR #%d changes protected paths: %s", issue.PRNumber, strings.Join(p, ", "))
			}
			if _, err := grants.RecordApproval(g.Terms.ID, grant.RecordedApproval{
				Gate: "merge", RunID: c.st.Run.ID, Issue: issue.Number, PR: issue.PRNumber, Head: issue.ApprovedHeadOID,
			}, env.now()); err != nil {
				return nil, grantStop("%v", err)
			}
		}
		if err := gh.MergePR(ctx, issue.PRNumber, c.cfg.Merge.Strategy, issue.ApprovedHeadOID, ghops.ExplicitConfirmation()); err != nil {
			if req.CIException != nil {
				return nil, wrapAfterMutation(err) // The CI acceptance audit was already written.
			}
			return nil, err
		}
		merged, err := gh.PR(ctx, issue.PRNumber)
		if err != nil {
			return nil, wrapAfterMutation(err)
		}
		if merged.State != "MERGED" || merged.HeadRefOid != issue.ApprovedHeadOID {
			return nil, wrapAfterMutation(fmt.Errorf("PR #%d merge confirmation returned state %s at head %q; expected MERGED at approved head %q", merged.Number, merged.State, merged.HeadRefOid, issue.ApprovedHeadOID))
		}
		pr = merged
	} else if byGrant && !grantMerged(g, c) {
		return nil, grantStop("PR #%d already reads MERGED but grant %s spent no merge on it; record the merge with a human approval", pr.Number, g.Terms.ID)
	}

	grantID := ""
	if byGrant {
		grantID = g.Terms.ID
	}
	return finishMerge(ctx, c, gh, pr, req.Approval.ApprovedBy, grantID, false)
}

// finishMerge is ordinary issue closure/audit/state completion, shared with
// explicit post-merge acknowledgment. Cleanup remains a separate confirmation.
func finishMerge(ctx context.Context, c *verbCtx, gh *ghops.GH, pr ghops.PR, approvedBy, grantID string, acknowledged bool) (*MergeResult, error) {
	env, issue := c.env, c.issue()
	// Terminal status (PRD §13): the issue reads delivered from here
	// on, not the needs-human set at merge-report.
	if err := gh.SetStatus(ctx, issue.Number, ghops.StatusDelivered); err != nil {
		return nil, wrapAfterMutation(err)
	}

	// Confirm issue closure (PRD §12 step 17): the Closes link usually
	// fires on merge, but the human merge approval covers closing it if
	// it did not.
	iss, m, err := readIssueManifest(ctx, gh, issue.Number)
	if err != nil {
		return nil, wrapAfterMutation(err)
	}
	if iss.State == "OPEN" {
		if err := gh.CloseIssue(ctx, issue.Number, ghops.ExplicitConfirmation()); err != nil {
			return nil, wrapAfterMutation(err)
		}
	}

	// The issue body is the durable audit home once the PR is merged.
	// The stamped commit is the merged head — the approval pinned it, the
	// merge matched it — which is what makes every earlier entry in the
	// record readable as "gathered before the commit that shipped" or not.
	detail := fmt.Sprintf("%s merge at %s", c.cfg.Merge.Strategy, pr.HeadRefOid)
	if acknowledged {
		detail = fmt.Sprintf("already-merged PR acknowledged at reviewed head %s; no prior Orch merge approval asserted", pr.HeadRefOid)
	}
	setVerification(&m, manifest.Verification{
		Name:      "merge",
		Result:    "merged",
		Detail:    truncateDetail(detail),
		CommitOID: pr.HeadRefOid,
		At:        env.nowStamp(),
	})
	if grantID != "" {
		setVerification(&m, manifest.Verification{
			Name:      mergeApprovalName,
			Result:    mergeApprovedLine(grantID),
			CommitOID: pr.HeadRefOid,
			At:        env.nowStamp(),
		})
	}
	issueBody, err := upsertCapped(iss.Body, m)
	if err != nil {
		return nil, wrapAfterMutation(err)
	}
	if err := gh.SetIssueBody(ctx, issue.Number, issueBody); err != nil {
		return nil, wrapAfterMutation(err)
	}

	// For acknowledgment this records the delivered reviewed head only now,
	// not a fabricated earlier merge-report or human merge approval.
	issue.ApprovedHeadOID = pr.HeadRefOid
	issue.Phase, issue.BlockedReason = state.PhaseMerged, ""
	if err := c.save(); err != nil {
		return nil, wrapAfterMutation(err)
	}

	source, reason := approvalSource(approvedBy, grantID != ""), ""
	if acknowledged {
		// Observe the delivered merge without inventing prior human approval.
		source, reason = "", MergedAcknowledgmentStatement
	}
	if err := c.recordMetric(metrics.Event{
		Verb:           "merge",
		IssueNumber:    issue.Number,
		PRNumber:       issue.PRNumber,
		ApprovalSource: source,
		Reason:         reason,
	}); err != nil {
		return nil, err
	}

	result := &MergeResult{
		SchemaVersion: MergeSchemaVersion,
		IssueNumber:   issue.Number,
		PRNumber:      issue.PRNumber,
		Merged:        true,
	}
	for _, v := range m.Verifications {
		if manualCIAuditPattern.MatchString(v.Name) {
			result.ManualCIAudit = append(result.ManualCIAudit, v)
		}
	}
	return result, nil
}
