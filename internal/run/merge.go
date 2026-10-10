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
}

// MergeRequest asks to merge an approved, awaiting-merge issue.
type MergeRequest struct {
	SchemaVersion int           `json:"schema_version"`
	IssueNumber   int           `json:"issue_number"`
	Approval      MergeApproval `json:"approval"`
}

// MergeResult reports the completed merge.
type MergeResult struct {
	SchemaVersion int  `json:"schema_version"`
	IssueNumber   int  `json:"issue_number"`
	PRNumber      int  `json:"pr_number"`
	Merged        bool `json:"merged"`
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
func Merge(ctx context.Context, env Env, reqJSON []byte) (*MergeResult, error) {
	var req MergeRequest
	if err := decodeRequest(reqJSON, &req); err != nil {
		return nil, err
	}
	if req.SchemaVersion != MergeSchemaVersion {
		return nil, fmt.Errorf("%w: schema_version %d is unsupported (this build supports %d)", ErrBadRequest, req.SchemaVersion, MergeSchemaVersion)
	}
	byGrant := req.Approval.Statement == GrantMergeApprovalStatement
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

	if pr.State != "MERGED" {
		if req.Approval.HeadOID != pr.HeadRefOid {
			return nil, fmt.Errorf("%w: the PR moved to head %q after approval of %q; re-run merge-report", ErrMergeApproval, pr.HeadRefOid, req.Approval.HeadOID)
		}
		summary, err := gh.RequiredCI(ctx, issue.PRNumber)
		if err != nil {
			return nil, err
		}
		if byGrant && summary.State != ghops.CIPassing {
			return nil, grantStop("required CI is %s, not passing", summary.State)
		}
		if err := requireMergeableCI(summary.State); err != nil {
			return nil, err
		}
		if byGrant {
			files, err := gh.PRFiles(ctx, issue.PRNumber, issue.ApprovedHeadOID)
			if err != nil {
				return nil, grantStop("the PR's complete file list could not be confirmed: %v", err)
			}
			if p := protectedFiles(files); len(p) > 0 {
				return nil, grantStop("PR #%d changes protected paths: %s", issue.PRNumber, strings.Join(p, ", "))
			}
			if _, err := grants.RecordApproval(g.Terms.ID, grant.RecordedApproval{
				Gate: "merge", RunID: c.st.Run.ID, Issue: issue.Number, PR: issue.PRNumber, Head: issue.ApprovedHeadOID,
			}, env.now()); err != nil {
				return nil, grantStop("%v", err)
			}
		}
		if err := gh.MergePR(ctx, issue.PRNumber, c.cfg.Merge.Strategy, issue.ApprovedHeadOID, ghops.ExplicitConfirmation()); err != nil {
			return nil, err
		}
		merged, err := gh.PR(ctx, issue.PRNumber)
		if err != nil {
			return nil, wrapAfterMutation(err)
		}
		if merged.State != "MERGED" {
			return nil, wrapAfterMutation(fmt.Errorf("PR #%d did not reach MERGED (state %s) after merge", merged.Number, merged.State))
		}
		pr = merged
	} else if byGrant && !grantMerged(g, c) {
		return nil, grantStop("PR #%d already reads MERGED but grant %s spent no merge on it; record the merge with a human approval", pr.Number, g.Terms.ID)
	}

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
	setVerification(&m, manifest.Verification{
		Name:      "merge",
		Result:    "merged",
		Detail:    truncateDetail(fmt.Sprintf("%s merge at %s", c.cfg.Merge.Strategy, pr.HeadRefOid)),
		CommitOID: pr.HeadRefOid,
		At:        env.nowStamp(),
	})
	if byGrant {
		setVerification(&m, manifest.Verification{
			Name:      mergeApprovalName,
			Result:    mergeApprovedLine(g.Terms.ID),
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

	issue.Phase = state.PhaseMerged
	if err := c.save(); err != nil {
		return nil, wrapAfterMutation(err)
	}

	if err := c.recordMetric(metrics.Event{
		Verb:           "merge",
		IssueNumber:    issue.Number,
		ApprovalSource: approvalSource(req.Approval.ApprovedBy, byGrant),
	}); err != nil {
		return nil, err
	}

	return &MergeResult{
		SchemaVersion: MergeSchemaVersion,
		IssueNumber:   issue.Number,
		PRNumber:      issue.PRNumber,
		Merged:        true,
	}, nil
}
