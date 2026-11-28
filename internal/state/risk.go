package state

import "time"

// RiskHold ties a numbered block to the audit that created it. An acceptance
// attaches one GitHub decision; it is never a generic BlockResolution.
type RiskHold struct {
	HeadOID      string          `json:"head_oid"`
	ReviewCycle  int             `json:"review_cycle"`
	RepairNumber int             `json:"repair_number"`
	Challenge    string          `json:"challenge"`
	AuditDigest  string          `json:"audit_digest"`
	Invalidated  bool            `json:"invalidated,omitempty"`
	Acceptance   *RiskAcceptance `json:"acceptance,omitempty"`
}

type RiskAcceptance struct {
	ReviewID    string `json:"review_id"`
	AuditDigest string `json:"audit_digest"`
}

// RiskReviewPending reserves an exact retry before writing either audit mirror.
// The new verdict, head and optional hold land together after both writes.
type RiskReviewPending struct {
	HeadOID       string    `json:"head_oid"`
	RequestDigest string    `json:"request_digest"`
	At            time.Time `json:"at"`
	Challenge     string    `json:"challenge,omitempty"`
	// FirstHoldBlockID is the provisional receipt number before either mirror is written.
	FirstHoldBlockID int `json:"first_hold_block_id,omitempty"`
}

func (h RiskHold) valid(issue Issue) bool {
	return fullOID.MatchString(h.HeadOID) && h.ReviewCycle > 0 && h.ReviewCycle <= issue.ReviewCycles &&
		h.RepairNumber >= 0 && h.RepairNumber <= len(issue.Repairs) && h.Challenge != "" && h.AuditDigest != "" &&
		(h.Acceptance == nil || (h.Acceptance.ReviewID != "" && h.Acceptance.AuditDigest != ""))
}

func (iss *Issue) LatestRiskBlock() *Block {
	for i := len(iss.Blocks) - 1; i >= 0; i-- {
		if iss.Blocks[i].Cause == BlockResidualRisk {
			return &iss.Blocks[i]
		}
	}
	return nil
}

func (iss *Issue) ActiveRiskBlock() *Block {
	if b := iss.CurrentBlock(); b != nil && b.Cause == BlockResidualRisk {
		return b
	}
	return nil
}
