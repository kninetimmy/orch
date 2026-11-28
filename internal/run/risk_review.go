package run

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"strings"
	"time"

	"github.com/kninetimmy/orch/internal/ghops"
	"github.com/kninetimmy/orch/internal/manifest"
	"github.com/kninetimmy/orch/internal/metrics"
	"github.com/kninetimmy/orch/internal/state"
)

// EligibleOnly is the routed reviewer's attestation, not an engine judgment
// about the risk's merit. No known defect, serious risk, or other blocker fits.
type RiskHoldInput struct {
	Trigger      string `json:"trigger"`
	Impact       string `json:"impact"`
	Recovery     string `json:"recovery"`
	EligibleOnly bool   `json:"eligible_only"`
}

const (
	riskAcknowledged = "acknowledge-accepted"
	riskAddressed    = "no-longer-present"
)

type RiskFollowUp struct {
	BlockID     int    `json:"block_id"`
	Disposition string `json:"disposition"`
	Reason      string `json:"reason"`
}

type riskReviewRecord struct {
	Binding        riskBinding   `json:"binding"`
	EvidenceDigest string        `json:"execution_and_judgments_digest"`
	TestsDigest    string        `json:"test_evidence_digest,omitempty"`
	FollowUp       *RiskFollowUp `json:"follow_up,omitempty"`
}

type riskHoldRecord struct {
	Binding      riskBinding        `json:"binding"`
	BlockID      int                `json:"block_id"`
	ReviewCycle  int                `json:"review_cycle"`
	ReviewDigest string             `json:"review_digest"`
	Reviewer     manifest.Selection `json:"reviewer"`
	Challenge    string             `json:"challenge"`
	Risk         RiskHoldInput      `json:"risk"`
}

func boundedRiskText(s string) bool { return strings.TrimSpace(s) != "" && len(s) <= 2000 }

// Only Secret decisions may intervene between staging the first hold and this retry.
// The last one may still need explicit resolution.
func stagedFirstHoldSecrets(is *state.Issue) []state.Block {
	p := is.RiskReviewPending
	if is.Phase != state.PhaseBlocked || p == nil || p.FirstHoldBlockID <= 0 || p.FirstHoldBlockID > len(is.Blocks) || is.LatestRiskBlock() != nil {
		return nil
	}
	secrets := is.Blocks[p.FirstHoldBlockID-1:]
	for i := range secrets {
		if secrets[i].Cause != state.BlockSecret || (i < len(secrets)-1 && secrets[i].Resolution == nil) {
			return nil
		}
	}
	return secrets
}

func canReviewWhileBlocked(is *state.Issue) bool {
	return is.ActiveRiskBlock() != nil || (len(stagedFirstHoldSecrets(is)) != 0 && !is.DecisionPending())
}

func reviewRisk(ctx context.Context, c *verbCtx, gh *ghops.GH, req ReviewRequest, pending, unverified, wrong []int, repairVerified bool) (*ReviewResult, error) {
	is := c.issue()
	head := req.ReviewedHeadOID
	if b := is.ActiveRiskBlock(); b != nil && strings.EqualFold(head, b.RiskHold.HeadOID) {
		if b.RiskHold.Invalidated {
			return nil, fmt.Errorf("%w: hold %d was invalidated; only a different live head can take fresh review while held", ErrBadApproval, b.ID)
		}
		return nil, fmt.Errorf("%w: same-head hold %d requires GitHub-authenticated accept-risk; only a different head can take fresh review while held", ErrBadApproval, b.ID)
	}
	if req.RiskHold != nil && (!req.RiskHold.EligibleOnly || !boundedRiskText(req.RiskHold.Trigger) ||
		!boundedRiskText(req.RiskHold.Impact) || !boundedRiskText(req.RiskHold.Recovery)) {
		return nil, fmt.Errorf("%w: a single eligible residual uncertainty requires trigger, impact, recovery, and eligible_only", ErrBadRequest)
	}
	complete := req.Verdict == VerdictApprove || req.Verdict == VerdictRiskHold
	if complete && (len(pending) != 0 || (latestRepair(is) != nil && !repairVerified)) {
		return nil, fmt.Errorf("%w: residual-risk approvals and holds require complete execution and repair evidence", ErrBadRequest)
	}
	a, err := openRiskAudit(ctx, c, gh, head, false)
	if err != nil {
		return nil, err
	}
	binding, err := a.binding(c)
	if err != nil {
		return nil, err
	}
	if err := checkRiskFollowUp(ctx, c, a, binding, req, is.ReviewCycles+1); err != nil {
		return nil, err
	}
	digest, err := contentDigest(struct {
		Request ReviewRequest
		Binding riskBinding
	}{req, binding})
	if err != nil {
		return nil, err
	}
	stage := is.RiskReviewPending
	retry := stage != nil
	firstHoldSecrets := stagedFirstHoldSecrets(is)
	interveningSecrets := firstHoldSecrets
	if retry {
		if stage.HeadOID != head || stage.RequestDigest != digest {
			return nil, fmt.Errorf("%w: unfinished residual-risk audit requires the exact review retry; preserve it if its head or policy changed", ErrReviewStale)
		}
		if b := is.ActiveRiskBlock(); b != nil {
			interveningSecrets = is.Blocks[b.ID:]
		}
	} else {
		if !sameReadinessAudit(a.issue, a.pr) {
			return nil, fmt.Errorf("%w: issue and PR audit disagree before residual-risk review", ErrReviewStale)
		}
		stage = &state.RiskReviewPending{HeadOID: head, RequestDigest: digest, At: c.env.now()}
		if req.RiskHold != nil {
			var challenge [24]byte
			if _, err := rand.Read(challenge[:]); err != nil {
				return nil, fmt.Errorf("generate residual risk challenge: %w", err)
			}
			stage.Challenge = hex.EncodeToString(challenge[:])
			if is.LatestRiskBlock() == nil {
				stage.FirstHoldBlockID = len(is.Blocks) + 1
			}
		}
	}
	stamp := stage.At.Format(time.RFC3339)
	stampPrecise := stage.At.Format(time.RFC3339Nano)
	pending, err = checkExecutionEvidence(is, req, head, stage.At)
	if err != nil {
		return nil, err
	}
	tests, err := convertVerifications(req.Verifications, stamp)
	if err != nil {
		return nil, err
	}
	judgments := criterionVerifications(req.Judgments, stamp)
	stampCommitOID(tests, head)
	stampCommitOID(judgments, head)
	cycle := is.ReviewCycles + 1
	cycleV := manifest.Verification{Name: fmt.Sprintf("review-cycle-%d", cycle), Result: req.Verdict,
		Detail: truncateReviewDetail(req.Summary), CommitOID: head, At: stamp}
	evidence, err := reviewEvidenceReceipt(req, pending, cycle, head, stampPrecise)
	if err != nil {
		return nil, err
	}
	evidenceDigest, err := contentDigest(evidence)
	if err != nil {
		return nil, err
	}
	var hold *state.RiskHold
	var reviewDigest string
	for _, m := range []*manifest.Manifest{&a.issue, &a.pr} {
		for _, v := range tests {
			setVerification(m, v)
		}
		for _, v := range judgments {
			setVerification(m, v)
		}
		// Ordinary review narratives can rotate out. A retry must not
		// depend on that optional text, but commit, verdict and time must match.
		if cur, exists, err := uniqueRiskEntry(*m, cycleV.Name); err != nil {
			return nil, err
		} else if exists && (!retry || cur.Result != cycleV.Result || cur.CommitOID != head || cur.At != stamp) {
			return nil, fmt.Errorf("%w: conflicting residual-risk review cycle", ErrReviewStale)
		}
		setVerification(m, cycleV)
		if err := addRiskEntry(m, evidence); err != nil {
			return nil, err
		}
		proof := riskReviewRecord{Binding: binding, EvidenceDigest: evidenceDigest, FollowUp: req.RiskFollowUp}
		if complete {
			proof.TestsDigest, err = riskTestEvidence(is, *m, head, stage.At)
			if err != nil {
				return nil, err
			}
		}
		v, err := riskVerification(riskReviewName(cycle), req.Verdict, head, stampPrecise, proof)
		if err != nil {
			return nil, err
		}
		if err := addRiskEntry(m, v); err != nil {
			return nil, err
		}
		reviewDigest, err = contentDigest(v)
		if err != nil {
			return nil, err
		}
		if req.RiskHold != nil {
			id := len(is.Blocks) + 1
			record := riskHoldRecord{binding, id, cycle, reviewDigest, req.Reviewer, stage.Challenge, *req.RiskHold}
			if err := dropDisplacedHoldReceipt(m, interveningSecrets, record, head, stampPrecise); err != nil {
				return nil, err
			}
			v, err := riskVerification(riskHoldName(id), VerdictRiskHold, head, stampPrecise, record)
			if err != nil {
				return nil, err
			}
			if err := addRiskEntry(m, v); err != nil {
				return nil, err
			}
			hash, err := contentDigest(v)
			if err != nil {
				return nil, err
			}
			hold = &state.RiskHold{HeadOID: head, ReviewCycle: cycle, RepairNumber: req.RepairNumber,
				Challenge: stage.Challenge, AuditDigest: hash, Invalidated: len(firstHoldSecrets) != 0}
		}
	}
	candidate := *is
	candidate.ReviewCycles, candidate.LastReviewVerdict, candidate.ReviewedHeadOID = cycle, req.Verdict, head
	candidate.PendingEvidence = pending
	candidate.EvidenceCriteria = unionCriteria(candidate.EvidenceCriteria, unverified)
	if _, err := readinessReviewEvidence(&candidate, c.st.Run.Plan.ConfigRevision, a.issue); err != nil {
		return nil, err
	}
	if !retry {
		is.RiskReviewPending = stage // invalidates prior merge evidence before the first mirror write
		if err := c.save(); err != nil {
			return nil, err
		}
	}
	if err := a.write(ctx); err != nil {
		return nil, fmt.Errorf("residual-risk review audit incomplete; retry the exact review, not generic recovery: %w", err)
	}
	if hold != nil || len(wrong) > 0 {
		err = gh.SetStatus(ctx, is.Number, ghops.StatusNeedsHuman)
	} else if req.Verdict == VerdictRequestChanges {
		err = gh.SetStatus(ctx, is.Number, ghops.StatusInProgress)
	} else if req.Verdict == VerdictNeedsEvidence {
		err = gh.SetStatus(ctx, is.Number, ghops.StatusAwaitingReview)
	}
	if err != nil {
		return nil, fmt.Errorf("residual-risk review incomplete; retry the exact review: %w", err)
	}
	is.ReviewCycles, is.LastReviewVerdict, is.ReviewedHeadOID = cycle, req.Verdict, head
	is.RiskReviewDigest, is.RiskReviewPending = reviewDigest, nil
	is.ApprovedHeadOID, is.RiskMergeReportAt = "", nil
	is.PendingEvidence, is.EvidenceCriteria = candidate.PendingEvidence, candidate.EvidenceCriteria
	is.Phase, is.BlockedReason = state.PhaseInReview, ""
	if repair := latestRepair(is); repair != nil {
		repair.VerifiedHeadOID = ""
		if repairVerified {
			repair.VerifiedHeadOID = head
		}
	}
	result := &ReviewResult{
		SchemaVersion: ReviewSchemaVersion, IssueNumber: is.Number, ReviewCycles: cycle, Verdict: req.Verdict,
		WrongCriteria: wrong, UnverifiedCriteria: unverified, PendingEvidence: pending,
	}
	if hold != nil {
		is.SetBlock(state.BlockResidualRisk, fmt.Sprintf("Review cycle %d held for one residual risk; use the GitHub decision in docs/residual-risk.md", cycle))
		block := &is.Blocks[len(is.Blocks)-1]
		block.RiskHold = hold
		result.RiskBlockID, result.RiskChallenge = block.ID, hold.Challenge
	} else if len(wrong) > 0 {
		is.SetBlock(state.BlockWrong, wrongCriteriaReason(wrong, cycle))
	}
	if err := c.save(); err != nil {
		return nil, fmt.Errorf("residual-risk review not finalized; retry the exact review: %w", err)
	}
	result.Phase, result.BlockedReason = is.Phase, is.BlockedReason
	var cause string
	if hold != nil {
		cause = metrics.BlockCauseResidualRisk
	} else if len(wrong) > 0 {
		cause = metrics.BlockCauseWrongCriterion
	}
	if err := c.recordReviewMetrics(req, cause); err != nil {
		return nil, err
	}
	return result, nil
}

// A resolved secret may have taken the next block ID after the review staged.
// Move only byte-for-byte matching provisional receipts of this exact retry.
func dropDisplacedHoldReceipt(m *manifest.Manifest, secrets []state.Block, record riskHoldRecord, head, stamp string) error {
	for _, b := range secrets {
		name := riskHoldName(b.ID)
		current, found, err := uniqueRiskEntry(*m, name)
		if err != nil {
			return err
		}
		if !found {
			continue
		}
		record.BlockID = b.ID
		want, err := riskVerification(name, VerdictRiskHold, head, stamp, record)
		if err != nil {
			return err
		}
		if current != want {
			return fmt.Errorf("%w: conflicting provisional hold receipt %s; preserve it and investigate", ErrReviewStale, name)
		}
		*m = withoutRiskEntry(*m, name)
	}
	return nil
}

func riskTestEvidence(is *state.Issue, m manifest.Manifest, head string, at time.Time) (string, error) {
	proofs := make([]manifest.Verification, 0, len(is.RequiredTests))
	for _, command := range is.RequiredTests {
		var proof manifest.Verification
		for _, v := range m.Verifications {
			if v.Command != command || v.CommitOID != head ||
				riskAuditName.MatchString(v.Name) || engineVerificationNames[v.Name] {
				continue
			}
			gathered, err := time.Parse(time.RFC3339, v.At)
			if err != nil || gathered.IsZero() || gathered.After(at) {
				continue
			}
			if repair := latestRepair(is); repair != nil {
				repaired, err := time.Parse(time.RFC3339, repair.At)
				if err != nil || gathered.Before(repaired) {
					continue
				}
			}
			if v.Result != "pass" && v.Result != "passed" {
				return "", fmt.Errorf("%w: non-passing same-head verification for required test %q", ErrBadRequest, command)
			}
			proof = v
		}
		if proof.Name == "" {
			return "", fmt.Errorf("%w: missing passing same-head verification for required test %q", ErrBadRequest, command)
		}
		proof.Detail = "" // caller prose can rotate; its evidence identity cannot
		proofs = append(proofs, proof)
	}
	return contentDigest(proofs)
}

func currentRiskReview(c *verbCtx, a riskAudit, binding riskBinding) (manifest.Verification, riskReviewRecord, error) {
	is := c.issue()
	var record riskReviewRecord
	if is.RiskReviewPending != nil || len(is.PendingEvidence) > 0 || !sameReadinessAudit(a.issue, a.pr) {
		return manifest.Verification{}, record, fmt.Errorf("%w: incomplete or contradictory residual-risk review audit", ErrReviewStale)
	}
	cycle, err := readinessReviewEvidence(is, c.st.Run.Plan.ConfigRevision, a.issue)
	if err != nil {
		return manifest.Verification{}, record, err
	}
	v, err := riskReceipt(a.issue, riskReviewName(is.ReviewCycles), is.RiskReviewDigest)
	if err != nil {
		return v, record, err
	}
	if err := decodeRequest([]byte(v.Detail), &record); err != nil {
		return v, record, err
	}
	if record.Binding != binding || v.Result != cycle.Result || v.CommitOID != cycle.CommitOID ||
		v.CommitOID != a.snapshot.Repository.PullRequest.HeadRefOid {
		return v, record, fmt.Errorf("%w: review, approved work, GitHub binding, or required-check policy changed", ErrReviewStale)
	}
	if _, err := riskReceipt(a.issue, fmt.Sprintf("review-evidence-%d", is.ReviewCycles), record.EvidenceDigest); err != nil {
		return v, record, err
	}
	at, err := time.Parse(time.RFC3339Nano, v.At)
	if err != nil || at.IsZero() || at.After(c.env.now()) {
		return v, record, fmt.Errorf("%w: residual-risk review has an invalid timestamp", ErrReviewStale)
	}
	tests, err := riskTestEvidence(is, a.issue, v.CommitOID, at)
	if err != nil || tests != record.TestsDigest {
		return v, record, fmt.Errorf("%w: residual-risk review's required test evidence changed: %v", ErrReviewStale, err)
	}
	return v, record, checkRepairReview(is, v.CommitOID)
}
