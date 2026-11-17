package run

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/kninetimmy/orch/internal/ghops"
	"github.com/kninetimmy/orch/internal/manifest"
	"github.com/kninetimmy/orch/internal/state"
)

const CIExceptionStatement = "accept-incomplete-ci"
const MergedAcknowledgmentStatement = "acknowledge-already-merged-pr"

// HumanCIException is request-local authority, never a persisted permission.
// The adapter must obtain an explicit human decision; the engine cannot
// authenticate a person. A grant cannot make either assertion.
type HumanCIException struct {
	RunID        string    `json:"run_id"`
	IssueNumber  int       `json:"issue_number"`
	PRNumber     int       `json:"pr_number"`
	HeadOID      string    `json:"head_oid"`
	ApprovedBy   string    `json:"approved_by"`
	ApprovedAt   time.Time `json:"approved_at"`
	Reason       string    `json:"reason"`
	Statement    string    `json:"statement"`
	RepairNumber int       `json:"repair_number,omitempty"`
}

// manualCIAuditPattern reserves every numbered receipt, not just the first.
// Receipts preserve evidence but are never read as merge authorization.
var manualCIAuditPattern = regexp.MustCompile(`^(ci-exception|merged-acknowledgment)-[0-9]+$`)

type manualCIRecord struct {
	HumanCIException
	PRURL    string        `json:"pr_url"`
	CI       MergeReportCI `json:"ci"`
	MergedAt string        `json:"merged_at,omitempty"`
}

func checkHumanCIException(c *verbCtx, approval *HumanCIException, statement string) error {
	issue := c.issue()
	if approval.Statement != statement || approval.RunID != c.st.Run.ID || approval.IssueNumber != issue.Number ||
		approval.PRNumber <= 0 || approval.PRNumber != issue.PRNumber || !headOIDPattern.MatchString(approval.HeadOID) ||
		strings.TrimSpace(approval.ApprovedBy) == "" || strings.HasPrefix(strings.TrimSpace(approval.ApprovedBy), grantApproverPrefix) ||
		strings.TrimSpace(approval.Reason) == "" || approval.ApprovedAt.IsZero() || approval.ApprovedAt.After(c.env.now()) {
		return fmt.Errorf("%w: manual CI decision requires matching current run_id, issue_number, pr_number, full reviewed head_oid, human approved_by, approved_at, reason and statement %q", ErrBadApproval, statement)
	}
	repairNumber := 0
	if repair := latestRepair(issue); repair != nil {
		repairNumber = repair.Number
		at, err := time.Parse(time.RFC3339, repair.At)
		if err != nil || !approval.ApprovedAt.After(at) {
			return fmt.Errorf("%w: integration repair requires a new manual CI decision after %s", ErrBadApproval, repair.At)
		}
	}
	if approval.RepairNumber != repairNumber {
		return fmt.Errorf("%w: manual CI decision must acknowledge current repair_number %d", ErrBadApproval, repairNumber)
	}
	if issue.DecisionPending() {
		return fmt.Errorf("%w: a manual CI decision cannot resolve an issue decision", ErrBadApproval)
	}
	for _, b := range issue.Blocks {
		if b.Cause != state.BlockOperational && b.Resolution == nil {
			return fmt.Errorf("%w: unresolved decision block %d requires its own resolution", ErrBadApproval, b.ID)
		}
	}
	if err := checkApprovedWork(issue); err != nil {
		return err
	}
	if err := checkApprovingReview(issue); err != nil {
		return err
	}
	return checkDependencies(c.st.Run, issue)
}

// observeManualCI brackets review/audit/CI evidence with live PR reads. Probe
// errors cannot be waived. A partial receipt mirror write may be retried, but
// the independent review and all non-receipt audit evidence must still agree.
func observeManualCI(ctx context.Context, c *verbCtx, gh *ghops.GH, pr ghops.PR, approval *HumanCIException) (ghops.Issue, manifest.Manifest, ghops.CISummary, error) {
	var empty manifest.Manifest
	issue := c.issue()
	repo, err := gh.Repo(ctx)
	if err != nil {
		return ghops.Issue{}, empty, ghops.CISummary{}, err
	}
	if repo.URL == "" || repo.DefaultBranch == "" || pr.URL != issue.PRURL ||
		pr.URL != repo.URL+"/pull/"+strconv.Itoa(issue.PRNumber) || pr.HeadRefName != issue.Branch ||
		pr.BaseRefName != repo.DefaultBranch || pr.HeadRefOid != approval.HeadOID ||
		(pr.State != "OPEN" && pr.State != "MERGED") {
		return ghops.Issue{}, empty, ghops.CISummary{}, fmt.Errorf("%w: registered PR identity, state, base, branch or live head differs from the manual CI decision", ErrBadApproval)
	}
	iss, m, err := readIssueManifest(ctx, gh, issue.Number)
	if err != nil {
		return iss, m, ghops.CISummary{}, err
	}
	if iss.URL != issue.URL || iss.URL != repo.URL+"/issues/"+strconv.Itoa(issue.Number) ||
		(iss.State != "OPEN" && (iss.State != "CLOSED" || pr.State != "MERGED")) {
		return iss, m, ghops.CISummary{}, fmt.Errorf("%w: manual CI decision does not identify the registered issue", ErrBadApproval)
	}
	evidence, err := readinessReviewEvidence(issue, c.st.Run.Plan.ConfigRevision, m)
	if err != nil {
		return iss, m, ghops.CISummary{}, err
	}
	at, err := time.Parse(time.RFC3339, evidence.At)
	if err != nil || evidence.CommitOID != pr.HeadRefOid || !approval.ApprovedAt.After(at) {
		return iss, m, ghops.CISummary{}, fmt.Errorf("%w: manual CI decision requires the independently approved live head and a decision after its review", ErrReviewStale)
	}
	if err := checkRepairReview(issue, pr.HeadRefOid); err != nil {
		return iss, m, ghops.CISummary{}, err
	}
	if pr.State == "OPEN" {
		mirror, err := manifest.Parse(pr.Body)
		if err != nil {
			return iss, m, ghops.CISummary{}, err
		}
		if !sameReadinessAudit(withoutManualCIAudit(m), withoutManualCIAudit(mirror)) {
			return iss, m, ghops.CISummary{}, fmt.Errorf("%w: issue and open PR audit records contradict each other", ErrReviewStale)
		}
	}
	if approval.Statement == MergedAcknowledgmentStatement {
		mergedAt, err := time.Parse(time.RFC3339, pr.MergedAt)
		if pr.State != "MERGED" || err != nil || !approval.ApprovedAt.After(mergedAt) || at.After(mergedAt) {
			return iss, m, ghops.CISummary{}, fmt.Errorf("%w: acknowledgment must follow the proven merge of the independently reviewed head; it is not prior merge approval", ErrBadApproval)
		}
	}
	summary, err := gh.RequiredCI(ctx, issue.PRNumber)
	if err != nil {
		return iss, m, summary, err
	}
	after, err := gh.PR(ctx, issue.PRNumber)
	if err != nil {
		return iss, m, summary, err
	}
	if after != pr {
		return iss, m, summary, fmt.Errorf("%w: PR changed during manual CI inspection; re-inspect and obtain a decision for its stable reviewed head", ErrBadApproval)
	}
	return iss, m, summary, nil
}

func withoutManualCIAudit(m manifest.Manifest) manifest.Manifest {
	m.Verifications = append([]manifest.Verification(nil), m.Verifications...)
	kept := m.Verifications[:0]
	for _, v := range m.Verifications {
		if !manualCIAuditPattern.MatchString(v.Name) {
			kept = append(kept, v)
		}
	}
	m.Verifications = kept
	return m
}

// recordManualCI writes the non-authorizing receipt before any merge or phase
// transition. Identical retries keep the original receipt and engine timestamp;
// changed observations append history rather than erasing the earlier evidence.
func recordManualCI(ctx context.Context, c *verbCtx, gh *ghops.GH, iss ghops.Issue, pr ghops.PR, m manifest.Manifest, summary ghops.CISummary, approval *HumanCIException) (*manifest.Verification, error) {
	record := manualCIRecord{HumanCIException: *approval, PRURL: pr.URL,
		CI: MergeReportCI{State: string(summary.State), Required: ciChecks(summary), Total: summary.Total}}
	prefix := "ci-exception-"
	if approval.Statement == MergedAcknowledgmentStatement {
		prefix, record.MergedAt = "merged-acknowledgment-", pr.MergedAt
	}
	detail, err := json.Marshal(record)
	if err != nil {
		return nil, err
	}
	number := 1
	var receipt *manifest.Verification
	for _, v := range m.Verifications {
		if !strings.HasPrefix(v.Name, prefix) || !manualCIAuditPattern.MatchString(v.Name) {
			continue
		}
		n, err := strconv.Atoi(strings.TrimPrefix(v.Name, prefix))
		if err != nil || n < 1 || n == int(^uint(0)>>1) {
			return nil, fmt.Errorf("%w: invalid manual CI audit identity %q", ErrBadRequest, v.Name)
		}
		if n >= number {
			number = n + 1
		}
		if v.Detail == string(detail) && v.CommitOID == pr.HeadRefOid && v.Result == approval.Statement {
			copy := v
			receipt = &copy
		}
	}
	if receipt == nil {
		receipt = &manifest.Verification{Name: prefix + strconv.Itoa(number), Result: approval.Statement,
			Detail: string(detail), CommitOID: pr.HeadRefOid, At: c.env.nowStamp()}
		m.Verifications = append(m.Verifications, *receipt)
	}
	// Protect full decisions/evidence from truncation or body-cap rotation;
	// oversized history fails before any write, merge or head pin.
	if _, err := upsertCapped(iss.Body, m); err != nil {
		return nil, err
	}
	if pr.State == "OPEN" {
		if _, err := upsertCapped(pr.Body, m); err != nil {
			return nil, err
		}
	}
	if err := writeManifest(ctx, gh, iss, &pr, m); err != nil {
		return nil, wrapAfterMutation(err)
	}
	return receipt, nil
}

// acknowledgeMerged finishes only an already-merged, independently approved
// PR. It does not invent a pre-merge gate or merge approval, and only the
// specific out-of-band-merge reconciliation block is eligible for recovery.
func acknowledgeMerged(ctx context.Context, env Env, req MergeRequest) (*MergeResult, error) {
	if req.Approval != (MergeApproval{}) || req.CIException != nil {
		return nil, fmt.Errorf("%w: merged_acknowledgment is separate from approval and ci_exception", ErrBadApproval)
	}
	c, err := loadVerb(env, req.IssueNumber, []state.Phase{state.PhaseInReview, state.PhaseAwaitingMerge, state.PhaseBlocked, state.PhaseMerged}, false)
	if err != nil {
		return nil, err
	}
	issue := c.issue()
	approval := req.MergedAcknowledgment
	if err := checkHumanCIException(c, approval, MergedAcknowledgmentStatement); err != nil {
		return nil, err
	}
	if issue.Phase == state.PhaseBlocked && (len(issue.Blocks) == 0 ||
		issue.Blocks[len(issue.Blocks)-1].Cause != state.BlockOperational ||
		issue.BlockedReason != outOfBandMergeReason(issue.PRNumber)) {
		return nil, fmt.Errorf("%w: acknowledgment can recover only the corresponding out-of-band-merge operational block", ErrBadApproval)
	}
	if issue.ApprovedHeadOID != "" && issue.ApprovedHeadOID != approval.HeadOID {
		return nil, fmt.Errorf("%w: acknowledgment cannot replace a different pinned head", ErrBadApproval)
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
		return nil, fmt.Errorf("%w: PR #%d is %s; acknowledgment never merges an open PR", ErrBadApproval, pr.Number, pr.State)
	}
	iss, m, summary, err := observeManualCI(ctx, c, gh, pr, approval)
	if err != nil {
		return nil, err
	}
	if _, err := recordManualCI(ctx, c, gh, iss, pr, m, summary, approval); err != nil {
		return nil, err
	}
	return finishMerge(ctx, c, gh, pr, approval.ApprovedBy, "", true)
}
