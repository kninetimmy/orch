package run

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/kninetimmy/orch/internal/ghops"
	"github.com/kninetimmy/orch/internal/manifest"
	"github.com/kninetimmy/orch/internal/state"
)

const AcceptRiskSchemaVersion = 1
const riskStatement = "orch accept-risk "

// No caller identity, time, reason, CI assertion, or human merge approval
// belongs on this wire. The review ID is only a locator for GitHub evidence.
type AcceptRiskRequest struct {
	SchemaVersion    int    `json:"schema_version"`
	RunID            string `json:"run_id"`
	IssueNumber      int    `json:"issue_number"`
	BlockID          int    `json:"block_id"`
	DecisionReviewID string `json:"decision_review_id"`
}

type AcceptRiskResult struct {
	SchemaVersion int                   `json:"schema_version"`
	IssueNumber   int                   `json:"issue_number"`
	BlockID       int                   `json:"block_id"`
	Phase         state.Phase           `json:"phase"`
	Receipt       manifest.Verification `json:"receipt"`
}

type riskDecisionEvidence struct {
	ReviewID    string    `json:"review_id"`
	ReviewURL   string    `json:"review_url"`
	ActorID     string    `json:"actor_id"`
	ActorLogin  string    `json:"actor_login"`
	SubmittedAt time.Time `json:"submitted_at"`
	Reason      string    `json:"reason"`
}

type riskAcceptanceRecord struct {
	Binding     riskBinding          `json:"binding"`
	BlockID     int                  `json:"block_id"`
	ReviewCycle int                  `json:"review_cycle"`
	HoldDigest  string               `json:"hold_digest"`
	Decision    riskDecisionEvidence `json:"decision"`
	Policy      ghops.RiskPolicy     `json:"required_policy"`
	Checks      []ghops.RiskCheck    `json:"successful_checks"`
}

func AcceptRisk(ctx context.Context, env Env, input []byte) (*AcceptRiskResult, error) {
	var req AcceptRiskRequest
	if err := decodeRequest(input, &req); err != nil {
		return nil, err
	}
	if req.SchemaVersion != AcceptRiskSchemaVersion || req.RunID == "" || req.IssueNumber <= 0 || req.BlockID <= 0 || req.DecisionReviewID == "" {
		return nil, fmt.Errorf("%w: accept-risk requires schema 1, current run_id, issue_number, block_id, and a GitHub decision_review_id", ErrBadRequest)
	}
	c, err := loadVerb(env, req.IssueNumber, []state.Phase{state.PhaseBlocked}, false)
	if err != nil {
		return nil, err
	}
	is := c.issue()
	block := is.ActiveRiskBlock()
	if req.RunID != c.st.Run.ID || block == nil || req.BlockID != block.ID || block.RiskHold.Invalidated || block.RiskHold.Acceptance != nil {
		return nil, fmt.Errorf("%w: accept-risk requires the current eligible residual-risk hold", ErrBadApproval)
	}
	hold := block.RiskHold
	if is.RiskReviewPending != nil || is.ReviewCycles != hold.ReviewCycle || is.LastReviewVerdict != VerdictRiskHold ||
		is.ReviewedHeadOID != hold.HeadOID || is.ApprovedHeadOID != "" || hold.RepairNumber != len(is.Repairs) {
		return nil, fmt.Errorf("%w: the held review is incomplete or superseded", ErrReviewStale)
	}
	gh, err := openGitHub(ctx, env)
	if err != nil {
		return nil, err
	}
	a, err := openRiskAudit(ctx, c, gh, hold.HeadOID, true)
	if err != nil {
		return nil, err
	}
	binding, err := a.binding(c)
	if err != nil {
		return nil, err
	}
	name := riskAcceptanceName(block.ID)
	base := a
	base.issue = withoutRiskEntry(a.issue, name)
	base.pr = withoutRiskEntry(a.pr, name)
	if _, _, err := currentRiskReview(c, base, binding); err != nil {
		return nil, err
	}
	decision, err := readRiskDecision(ctx, c, a, binding, block, req.DecisionReviewID)
	if err != nil {
		return nil, err
	}
	now := env.now()
	checks, err := a.snapshot.RiskCI(a.policy, now)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrCINotReady, err)
	}
	at := now
	for _, m := range []manifest.Manifest{a.issue, a.pr} {
		// An interrupted mirror write is not authority. Re-observe the same
		// authenticated decision and strict CI, then converge on the same receipt.
		if existing, found, err := uniqueRiskEntry(m, name); err != nil {
			return nil, err
		} else if found {
			at, err = time.Parse(time.RFC3339Nano, existing.At)
			if err != nil || at.Before(decision.SubmittedAt) || at.After(now) {
				return nil, fmt.Errorf("%w: invalid prior residual-risk receipt timestamp", ErrReviewStale)
			}
		}
	}
	for _, check := range checks {
		if check.CompletedAt.After(at) {
			return nil, fmt.Errorf("%w: CI changed during an unfinished residual-risk acceptance; preserve the audit and investigate", ErrReviewStale)
		}
	}
	v, err := riskVerification(name, "accepted", hold.HeadOID, at.Format(time.RFC3339Nano),
		riskAcceptanceRecord{binding, block.ID, hold.ReviewCycle, hold.AuditDigest, decision, a.policy, checks})
	if err != nil {
		return nil, err
	}
	if err := addRiskEntry(&a.issue, v); err != nil {
		return nil, err
	}
	if err := addRiskEntry(&a.pr, v); err != nil {
		return nil, err
	}
	digest, err := contentDigest(v)
	if err != nil {
		return nil, err
	}
	if err := a.write(ctx); err != nil {
		return nil, fmt.Errorf("residual-risk decision audit incomplete; retry the same accept-risk request: %w", err)
	}
	if err := gh.SetStatus(ctx, is.Number, ghops.StatusAwaitingReview); err != nil {
		return nil, fmt.Errorf("residual-risk acceptance not finalized; retry the same accept-risk request: %w", err)
	}
	hold.Acceptance = &state.RiskAcceptance{ReviewID: req.DecisionReviewID, AuditDigest: digest}
	is.Phase, is.BlockedReason = state.PhaseInReview, ""
	if err := c.save(); err != nil {
		return nil, fmt.Errorf("residual-risk acceptance not finalized; retry the same accept-risk request: %w", err)
	}
	// Leave the non-approving held verdict and head untouched. The next cycle
	// must independently approve after acknowledging the exact receipt.
	return &AcceptRiskResult{AcceptRiskSchemaVersion, is.Number, block.ID, is.Phase, v}, nil
}

func withoutRiskEntry(m manifest.Manifest, name string) manifest.Manifest {
	kept := make([]manifest.Verification, 0, len(m.Verifications))
	for _, v := range m.Verifications {
		if v.Name != name {
			kept = append(kept, v)
		}
	}
	m.Verifications = kept
	return m
}

func readRiskDecision(ctx context.Context, c *verbCtx, a riskAudit, binding riskBinding, block *state.Block, id string) (riskDecisionEvidence, error) {
	var decision riskDecisionEvidence
	h := block.RiskHold
	v, err := riskReceipt(a.issue, riskHoldName(block.ID), h.AuditDigest)
	if err != nil {
		return decision, err
	}
	var held riskHoldRecord
	if err := decodeRequest([]byte(v.Detail), &held); err != nil {
		return decision, err
	}
	if h.Invalidated || held.Binding != binding || held.BlockID != block.ID || held.ReviewCycle != h.ReviewCycle ||
		held.Challenge != h.Challenge || h.HeadOID != a.snapshot.Repository.PullRequest.HeadRefOid || v.CommitOID != h.HeadOID ||
		v.Result != VerdictRiskHold || (c.issue().ReviewCycles == h.ReviewCycle && held.ReviewDigest != c.issue().RiskReviewDigest) {
		return decision, fmt.Errorf("%w: held review, GitHub binding, approved work or required-check policy changed", ErrReviewStale)
	}
	d, err := a.target.RiskDecision(ctx, id, a.snapshot)
	if err != nil {
		return decision, err
	}
	heldAt, err := time.Parse(time.RFC3339Nano, v.At)
	if err != nil || heldAt.IsZero() || !d.CreatedAt.After(heldAt) || d.SubmittedAt.Before(d.CreatedAt) || d.SubmittedAt.After(c.env.now()) {
		return decision, fmt.Errorf("%w: GitHub decision must be created and submitted after this hold, not in the future", ErrBadApproval)
	}
	line, reason, ok := strings.Cut(strings.ReplaceAll(d.Body, "\r\n", "\n"), "\n\n")
	reason = strings.TrimSpace(reason)
	if !ok || line != riskStatement+h.Challenge || !boundedRiskText(reason) {
		return decision, fmt.Errorf("%w: GitHub COMMENT must contain the exact hold challenge followed by a blank line and a nonempty reason (max 2000 bytes)", ErrBadApproval)
	}
	return riskDecisionEvidence{d.ID, d.URL, d.Author.ID, d.Author.Login, d.SubmittedAt, reason}, nil
}

func checkRiskFollowUp(ctx context.Context, c *verbCtx, a riskAudit, binding riskBinding, req ReviewRequest, cycle int) error {
	block := c.issue().LatestRiskBlock()
	if req.Verdict != VerdictApprove || block == nil {
		if req.RiskFollowUp != nil {
			return fmt.Errorf("%w: risk_follow_up is only for an approval after a recorded hold", ErrBadRequest)
		}
		return nil
	}
	return verifyRiskFollowUp(ctx, c, a, binding, block, req.RiskFollowUp, cycle)
}

func verifyRiskFollowUp(ctx context.Context, c *verbCtx, a riskAudit, binding riskBinding, block *state.Block, follow *RiskFollowUp, cycle int) error {
	hold := block.RiskHold
	if follow == nil || follow.BlockID != block.ID || !boundedRiskText(follow.Reason) {
		return fmt.Errorf("%w: a fresh routed approval must explicitly address risk block %d and state why", ErrBadRequest, block.ID)
	}
	same := strings.EqualFold(hold.HeadOID, a.snapshot.Repository.PullRequest.HeadRefOid) && hold.RepairNumber == binding.RepairNumber
	switch follow.Disposition {
	case riskAddressed:
		if !same {
			return nil // The reviewer explains why the risk is absent at this new head/repair.
		}
	case riskAcknowledged:
		if !same || hold.Acceptance == nil || hold.Invalidated || cycle != hold.ReviewCycle+1 {
			break
		}
		v, err := riskReceipt(a.issue, riskAcceptanceName(block.ID), hold.Acceptance.AuditDigest)
		if err != nil {
			return err
		}
		var receipt riskAcceptanceRecord
		if err := decodeRequest([]byte(v.Detail), &receipt); err != nil {
			return err
		}
		d, err := readRiskDecision(ctx, c, a, binding, block, hold.Acceptance.ReviewID)
		if err != nil {
			return err
		}
		at, err := time.Parse(time.RFC3339Nano, v.At)
		if err != nil || at.Before(d.SubmittedAt) || at.After(c.env.now()) ||
			v.Result != "accepted" || v.CommitOID != hold.HeadOID || receipt.Binding != binding ||
			receipt.BlockID != block.ID || receipt.ReviewCycle != hold.ReviewCycle || receipt.HoldDigest != hold.AuditDigest ||
			receipt.Decision != d || len(receipt.Checks) == 0 {
			return fmt.Errorf("%w: residual-risk acceptance proof is missing, stale or contradictory", ErrReviewStale)
		}
		return nil
	}
	return fmt.Errorf("%w: hold %d cannot be reused at a different head, after repair or after a superseding review; if risk persists obtain a fresh hold and acceptance", ErrReviewStale, block.ID)
}
