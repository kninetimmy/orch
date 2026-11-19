package run

import (
	"encoding/json"
	"fmt"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/kninetimmy/orch/internal/manifest"
	"github.com/kninetimmy/orch/internal/state"
)

// ExecutionEvidence is caller-attributed execution output evaluated by the
// reviewer. Artifact is an identifiable log/path/URL with a digest or other
// stable identity, not its contents. The engine never opens it or runs Command;
// the independent reviewer judges its authenticity, result and criterion fit.
type ExecutionEvidence struct {
	Criterion   int    `json:"criterion"`
	HeadOID     string `json:"head_oid"`
	Artifact    string `json:"artifact"`
	Command     string `json:"command"`
	Result      string `json:"result"`
	CollectedBy string `json:"collected_by"`
	CollectedAt string `json:"collected_at"`
}

// Like manual CI receipts, every numbered evidence receipt is engine-owned and
// retained whole under body-cap rotation. This is review history, not a second
// workflow or a permission. Manifest's existing generic verification shape stays
// unchanged; approval contract v3 fences out receipt-unaware lifecycle engines.
var reviewEvidenceNamePattern = regexp.MustCompile(`^review-evidence-[0-9]+$`)

type reviewEvidenceRecord struct {
	Judgments         []CriterionJudgment `json:"judgments"`
	ExecutionEvidence []ExecutionEvidence `json:"execution_evidence,omitempty"`
	PendingCriteria   []int               `json:"pending_criteria,omitempty"`
}

func reviewVerdict(v string) bool {
	return v == VerdictApprove || v == VerdictRequestChanges || v == VerdictNeedsEvidence
}

func checkExecutionEvidence(issue *state.Issue, req ReviewRequest, head string, now time.Time) ([]int, error) {
	pending := append([]int(nil), issue.PendingEvidence...)
	if head != issue.ReviewedHeadOID {
		pending = unionCriteria(pending, issue.EvidenceCriteria)
	}
	unverified := criteriaWithJudgment(req.Judgments, JudgmentUnverified)
	if (len(pending) > 0 || len(unverified) > 0 || len(req.ExecutionEvidence) > 0) && !headOIDPattern.MatchString(head) {
		return nil, fmt.Errorf("%w: evidence review requires a full reviewed PR head OID", ErrBadRequest)
	}
	seen := map[int]bool{}
	for _, e := range req.ExecutionEvidence {
		if e.Criterion < 1 || e.Criterion > len(issue.AcceptanceCriteria) || seen[e.Criterion] {
			return nil, fmt.Errorf("%w: execution_evidence requires one entry per supplied criterion, in the approved range", ErrBadRequest)
		}
		seen[e.Criterion] = true
		if e.HeadOID != head {
			return nil, fmt.Errorf("%w: execution evidence for criterion %d is from head %q, not live reviewed head %q; collect new evidence and obtain fresh review", ErrReviewStale, e.Criterion, e.HeadOID, head)
		}
		for _, value := range []string{e.Artifact, e.Command, e.Result, e.CollectedBy} {
			if strings.TrimSpace(value) == "" || len([]rune(value)) > verificationDetailCap {
				return nil, fmt.Errorf("%w: execution evidence requires bounded nonempty artifact, command, result and collected_by", ErrBadRequest)
			}
		}
		at, err := time.Parse(time.RFC3339, e.CollectedAt)
		if err != nil || at.IsZero() || at.After(now) {
			return nil, fmt.Errorf("%w: execution evidence requires a non-future RFC3339 collected_at", ErrBadRequest)
		}
		if repair := latestRepair(issue); repair != nil {
			repairedAt, err := time.Parse(time.RFC3339, repair.At)
			if err != nil || at.Before(repairedAt) {
				return nil, fmt.Errorf("%w: execution evidence predates integration repair; collect fresh evidence", ErrReviewStale)
			}
		}
	}
	for _, j := range req.Judgments {
		if j.Judgment == JudgmentUnverified && !slices.Contains(pending, j.Criterion) {
			pending = append(pending, j.Criterion)
		}
		if j.Judgment == JudgmentSatisfied && slices.Contains(pending, j.Criterion) {
			if !seen[j.Criterion] {
				return nil, fmt.Errorf("%w: pending criterion %d needs identifiable execution_evidence for the live reviewed head; verifications alone do not discharge it", ErrBadRequest, j.Criterion)
			}
			pending = slices.DeleteFunc(pending, func(n int) bool { return n == j.Criterion })
		}
	}
	slices.Sort(pending)
	return pending, nil
}

func unionCriteria(a, b []int) []int {
	out := append([]int(nil), a...)
	for _, n := range b {
		if !slices.Contains(out, n) {
			out = append(out, n)
		}
	}
	slices.Sort(out)
	return out
}

func checkReviewEvidenceReceipt(issue *state.Issue, m manifest.Manifest, cycle manifest.Verification) error {
	name := fmt.Sprintf("review-evidence-%d", issue.ReviewCycles)
	var receipt *manifest.Verification
	for i := range m.Verifications {
		v := &m.Verifications[i]
		if v.Name == name {
			if receipt != nil {
				return fmt.Errorf("duplicate audit evidence %s", name)
			}
			receipt = v
		}
	}
	if receipt == nil {
		if cycle.Result == VerdictNeedsEvidence || len(issue.PendingEvidence) > 0 {
			return fmt.Errorf("audit evidence %s is missing", name)
		}
		return nil
	}
	var record reviewEvidenceRecord
	if err := decodeRequest([]byte(receipt.Detail), &record); err != nil {
		return fmt.Errorf("audit evidence %s is invalid: %w", name, err)
	}
	if receipt.Result != cycle.Result || receipt.CommitOID != cycle.CommitOID || receipt.At != cycle.At ||
		!slices.Equal(record.PendingCriteria, issue.PendingEvidence) {
		return fmt.Errorf("audit evidence %s contradicts the latest review/state", name)
	}
	if err := checkJudgments(record.Judgments, cycle.Result, issue.AcceptanceCriteria); err != nil {
		return err
	}
	at, err := time.Parse(time.RFC3339, receipt.At)
	if err != nil {
		return fmt.Errorf("audit evidence %s has invalid timestamp", name)
	}
	req := ReviewRequest{Judgments: record.Judgments, ExecutionEvidence: record.ExecutionEvidence}
	pending, err := checkExecutionEvidence(issue, req, receipt.CommitOID, at)
	if err != nil || !slices.Equal(pending, issue.PendingEvidence) {
		return fmt.Errorf("audit evidence %s has invalid execution evidence or pending criteria: %v", name, err)
	}
	for _, j := range record.Judgments {
		for _, v := range m.Verifications {
			if v.Name == fmt.Sprintf("acceptance-criterion-%d", j.Criterion) && v.Result != j.Judgment {
				return fmt.Errorf("audit evidence %s contradicts criterion %d", name, j.Criterion)
			}
		}
	}
	return nil
}

func reviewEvidenceReceipt(req ReviewRequest, pending []int, cycle int, head, stamp string) (manifest.Verification, error) {
	detail, err := json.Marshal(reviewEvidenceRecord{req.Judgments, req.ExecutionEvidence, pending})
	return manifest.Verification{
		Name: fmt.Sprintf("review-evidence-%d", cycle), Result: req.Verdict,
		Detail: string(detail), CommitOID: head, At: stamp,
	}, err
}
