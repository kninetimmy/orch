package run

import (
	"context"
	"fmt"
	"slices"
	"strconv"
	"strings"

	"github.com/kninetimmy/orch/internal/ghops"
	"github.com/kninetimmy/orch/internal/manifest"
	"github.com/kninetimmy/orch/internal/metrics"
	"github.com/kninetimmy/orch/internal/state"
)

// ReviewSchemaVersion is the review request/result schema this build
// accepts and emits.
//
// v2 requires one judgment per acceptance criterion (the Judgments
// field below). It is a version bump rather than an optional field
// because a v1 review says nothing about any individual criterion, and
// accepting one would keep recording approvals that assert nothing —
// the hole this schema closes. Raising the constant is what makes the
// version check fire first, so a build-behind adapter is told which
// version this build supports instead of being handed a confusing
// complaint about a missing field.
const ReviewSchemaVersion = 2

// The two review verdicts (PRD §12 step 11).
const (
	VerdictApprove        = "approve"
	VerdictRequestChanges = "request-changes"
)

// The three judgments a reviewer records for one acceptance criterion.
// satisfied and unsatisfied are calls about the work, so an unsatisfied
// criterion routes back to the executor exactly as a request-changes
// review always has. wrong is a call about the criterion itself, and it
// is the human's to answer: the engine has no verb that amends a gated
// acceptance criterion, so returning the issue to the executor would
// only ask it to re-satisfy text that cannot be satisfied.
const (
	JudgmentSatisfied   = "satisfied"
	JudgmentUnsatisfied = "unsatisfied"
	JudgmentWrong       = "wrong"
)

// CriterionJudgment is the reviewer's call on one acceptance criterion,
// with the reason that call rests on. Criterion is the criterion's
// 1-based position in the issue's approved acceptance criteria — the
// same order the audit record lists them in — and the engine counts
// those from its own state, so a request can neither invent a criterion
// nor quietly omit one.
type CriterionJudgment struct {
	Criterion int    `json:"criterion"`
	Judgment  string `json:"judgment"`
	Reason    string `json:"reason"`
}

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
	// Judgments carries exactly one judgment, with its reason, for each
	// acceptance criterion the issue holds (checkJudgments). It is
	// mandatory: a consolidated summary can approve while saying nothing
	// about any individual criterion, and the judgments are what make an
	// approval an assertion about each one.
	Judgments []CriterionJudgment `json:"judgments"`
	// Verifications is optional evidence gathered on this review cycle
	// (for example, tests re-run on a fix commit), using the same
	// {name, command, result, detail} input shape pr-open accepts. Each
	// entry is replace-by-name upserted into the manifest alongside the
	// review-cycle-N entry this verb already writes, so resubmitting the
	// same name updates that entry rather than duplicating it. A name
	// colliding with an engine-owned entry (required-ci, merge,
	// abandoned, review-cycle-<n>, or acceptance-criterion-<n>) is
	// rejected — those are never caller-suppliable, so the engine's own
	// record can never be quietly overwritten or duplicated.
	Verifications []VerificationInput `json:"verifications,omitempty"`
	// Usage is adapter-reported model usage for this review cycle
	// (PRD §21 "where available"); optional and best-effort. This is
	// the reviewer's own usage — the cost of producing this review.
	Usage *metrics.Usage `json:"usage,omitempty"`
	// ExecutorUsage is adapter-reported model usage for the executor's
	// fix-and-push work on this cycle (PRD §21 "where available");
	// optional and best-effort. A request-changes verdict loops the
	// same executor in the same worktree to fix and push, and this
	// verb is the only call that happens on each such cycle — without
	// this field the executor's fix-cycle cost has nowhere to go.
	ExecutorUsage *metrics.Usage `json:"executor_usage,omitempty"`
}

// ReviewResult reports the recorded review cycle. Phase is the phase
// the recording left the issue in, and WrongCriteria/BlockedReason are
// populated only when a criterion was judged wrong: that outcome blocks
// the issue for the human inside this call, so the result has to say so
// — there is no second verb call left to report it.
type ReviewResult struct {
	SchemaVersion int         `json:"schema_version"`
	IssueNumber   int         `json:"issue_number"`
	ReviewCycles  int         `json:"review_cycles"`
	Verdict       string      `json:"verdict"`
	Phase         state.Phase `json:"phase"`
	WrongCriteria []int       `json:"wrong_criteria,omitempty"`
	BlockedReason string      `json:"blocked_reason,omitempty"`
}

// Review records one consolidated review cycle (PRD §12 step 11). It
// requires the reporting reviewer to be the issue's routed reviewer
// (PRD §13: the audit record's reviewer is the one that reviewed), one
// judgment with a stated reason for every acceptance criterion the
// issue holds (checkJudgments, before any mutation), and — enforcing
// PRD §14's "review begins only after the PR stops changing" — a
// reviewed head OID equal to the PR's live head, so a stale review is
// rejected and the reviewer re-reads.
//
// It appends the cycle to the issue and PR audit records alongside the
// per-criterion judgments and any caller-supplied verification evidence
// (replace-by-name, so a fix-commit re-run reaches the record even
// though the issue is no longer in phase dispatched). Where it leaves
// the issue follows the judgments:
//
//   - a criterion judged wrong blocks the issue and flags it for the
//     human, the same transition escalate's return-to-architect makes,
//     but taken here so no second verb call stands between the reviewer
//     saying an approved criterion is false and the human hearing it;
//   - otherwise a request-changes verdict sets the status back to
//     in-progress and the issue stays in-review, unchanged;
//   - an approving verdict (which checkJudgments allows only when every
//     criterion is satisfied) leaves the issue in-review, ready for the
//     merge report.
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
	if err := req.Usage.Validate(); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrBadRequest, err)
	}
	if err := req.ExecutorUsage.Validate(); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrBadRequest, err)
	}
	verifications, err := convertVerifications(req.Verifications, env.nowStamp())
	if err != nil {
		return nil, err
	}
	if err := rejectEngineOwnedNames(verifications); err != nil {
		return nil, err
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
		return nil, fmt.Errorf("%w: reviewer %s is not the routed reviewer %s; review with the routed selection, or run `orch run escalate` to reroute first", ErrReviewerMismatch, req.Reviewer, issue.Decision.Reviewer)
	}
	// The criteria the judgments must cover are read from state, never
	// counted from the request, so a review cannot decide for itself how
	// many criteria it is answering. checkApprovedWork guards the one
	// shape that would make that count vacuously zero.
	if err := checkApprovedWork(issue); err != nil {
		return nil, err
	}
	if err := checkJudgments(req.Judgments, req.Verdict, issue.AcceptanceCriteria); err != nil {
		return nil, err
	}
	slices.SortFunc(req.Judgments, func(a, b CriterionJudgment) int { return a.Criterion - b.Criterion })
	wrong := wrongCriteria(req.Judgments)

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
	if len(wrong) > 0 {
		issue.Phase = state.PhaseBlocked
		issue.BlockedReason = wrongCriteriaReason(wrong, issue.ReviewCycles)
	}
	if err := c.save(); err != nil {
		return nil, err
	}

	iss, m, err := readIssueManifest(ctx, gh, issue.Number)
	if err != nil {
		return nil, wrapAfterMutation(err)
	}
	// Every entry this cycle writes is stamped with the PR's live head as
	// the engine read it, not with the request's ReviewedHeadOID. The two
	// are equal — the staleness check above rejects the cycle otherwise —
	// but the record's provenance should not rest on a request field even
	// when that field was just validated.
	stampCommitOID(verifications, pr.HeadRefOid)
	for _, v := range verifications {
		setVerification(&m, v)
	}
	judged := criterionVerifications(req.Judgments, env.nowStamp())
	stampCommitOID(judged, pr.HeadRefOid)
	for _, v := range judged {
		setVerification(&m, v)
	}
	m.Verifications = append(m.Verifications, manifest.Verification{
		Name:      fmt.Sprintf("review-cycle-%d", issue.ReviewCycles),
		Result:    req.Verdict,
		Detail:    truncateReviewDetail(req.Summary),
		CommitOID: pr.HeadRefOid,
		At:        env.nowStamp(),
	})
	if err := writeManifest(ctx, gh, iss, &pr, m); err != nil {
		return nil, wrapAfterMutation(err)
	}

	switch {
	case len(wrong) > 0:
		if err := gh.SetStatus(ctx, issue.Number, ghops.StatusNeedsHuman); err != nil {
			return nil, wrapAfterMutation(err)
		}
	case req.Verdict == VerdictRequestChanges:
		if err := gh.SetStatus(ctx, issue.Number, ghops.StatusInProgress); err != nil {
			return nil, wrapAfterMutation(err)
		}
	}

	if err := c.recordMetric(metrics.Event{
		Verb:         "review",
		IssueNumber:  issue.Number,
		Verdict:      req.Verdict,
		ReviewCycles: issue.ReviewCycles,
		Reviewer:     &req.Reviewer,
		Usage:        req.Usage,
	}); err != nil {
		return nil, err
	}
	// The executor's fix-cycle usage, when supplied, is recorded as its
	// own event rather than folded into the reviewer's event above: the
	// two carry different roles' cost, and Role is what keeps them
	// distinguishable after the fact (this event carries no Verdict, so
	// it is never mistaken for a review decision when a report counts
	// review cycles).
	if req.ExecutorUsage != nil {
		if err := c.recordMetric(metrics.Event{
			Verb:         "review",
			IssueNumber:  issue.Number,
			ReviewCycles: issue.ReviewCycles,
			Role:         string(issue.Decision.Role),
			Usage:        req.ExecutorUsage,
		}); err != nil {
			return nil, err
		}
	}

	return &ReviewResult{
		SchemaVersion: ReviewSchemaVersion,
		IssueNumber:   issue.Number,
		ReviewCycles:  issue.ReviewCycles,
		Verdict:       req.Verdict,
		Phase:         issue.Phase,
		WrongCriteria: wrong,
		BlockedReason: issue.BlockedReason,
	}, nil
}

// checkJudgments fails closed unless js carries exactly one judgment,
// from the closed set and with a stated reason, for each of the issue's
// acceptance criteria, and unless an approving verdict finds every one
// of them satisfied. criteria is the issue's own approved list, so the
// coverage the request must meet is the engine's count, not its own.
//
// An approving verdict over a criterion judged anything but satisfied
// is refused rather than silently re-routed: it is the reviewer
// contradicting itself, and recording it either way would put the
// contradiction into the audit record — an approval on the merge path
// over a criterion the same review says is not met.
func checkJudgments(js []CriterionJudgment, verdict string, criteria []string) error {
	seen := make(map[int]bool, len(criteria))
	for i, j := range js {
		switch j.Judgment {
		case JudgmentSatisfied, JudgmentUnsatisfied, JudgmentWrong:
		default:
			return fmt.Errorf("%w: judgments[%d] judgment %q is not one of %s, %s, %s", ErrBadRequest, i, j.Judgment, JudgmentSatisfied, JudgmentUnsatisfied, JudgmentWrong)
		}
		if j.Reason == "" {
			return fmt.Errorf("%w: judgments[%d] states no reason; the audit record carries the reviewer's reason for every judgment", ErrBadRequest, i)
		}
		if j.Criterion < 1 || j.Criterion > len(criteria) {
			return fmt.Errorf("%w: judgments[%d] judges criterion %d, but the issue holds %d acceptance criteria, numbered 1 to %d", ErrBadRequest, i, j.Criterion, len(criteria), len(criteria))
		}
		if seen[j.Criterion] {
			return fmt.Errorf("%w: criterion %d is judged more than once; each acceptance criterion takes exactly one judgment", ErrBadRequest, j.Criterion)
		}
		seen[j.Criterion] = true
		if verdict == VerdictApprove && j.Judgment != JudgmentSatisfied {
			return fmt.Errorf("%w: criterion %d is judged %s, so the verdict cannot be %s; record %s", ErrBadRequest, j.Criterion, j.Judgment, VerdictApprove, VerdictRequestChanges)
		}
	}
	for i := range criteria {
		if !seen[i+1] {
			return fmt.Errorf("%w: acceptance criterion %d carries no judgment; a review judges each of the issue's %d acceptance criteria", ErrBadRequest, i+1, len(criteria))
		}
	}
	return nil
}

// wrongCriteria are the criteria js judges wrong, in the ascending
// order js is sorted into. It is empty for every review that judges no
// criterion wrong, which is what the ordinary approve and
// request-changes paths test.
func wrongCriteria(js []CriterionJudgment) []int {
	var out []int
	for _, j := range js {
		if j.Judgment == JudgmentWrong {
			out = append(out, j.Criterion)
		}
	}
	return out
}

// wrongCriteriaReason is the blocked reason a review that judges a
// criterion wrong leaves on the issue. It names the criteria and the
// cycle, and states plainly that the correction belongs at the plan
// gate: there is no engine verb that amends a gated acceptance
// criterion, so a reason implying the run can fix one itself would send
// the human looking for a command that does not exist.
func wrongCriteriaReason(nums []int, cycle int) string {
	labels := make([]string, len(nums))
	for i, n := range nums {
		labels[i] = strconv.Itoa(n)
	}
	return fmt.Sprintf("Acceptance criteria judged wrong on review cycle %d: %s; returning to the human — a gated criterion is corrected at the plan gate, not by the executor, and no verb amends one in place", cycle, strings.Join(labels, ", "))
}

// criterionVerifications renders the judgments into audit-record
// entries: one per acceptance criterion, named for its 1-based position
// in the record's own acceptance-criteria list, carrying the judgment
// as its result and the reviewer's stated reason as its detail.
//
// They are written replace-by-name rather than once per cycle, so the
// record's answer to "how does this criterion stand" is the current one
// and the entry count stays bounded by the criteria count however many
// cycles run. Each cycle's narrative still accumulates in its own
// review-cycle-<n> entry. The name shape is engine-owned and refused
// from callers (acceptanceCriterionNamePattern, manifestbody.go).
func criterionVerifications(js []CriterionJudgment, stamp string) []manifest.Verification {
	out := make([]manifest.Verification, len(js))
	for i, j := range js {
		out[i] = manifest.Verification{
			Name:   fmt.Sprintf("acceptance-criterion-%d", j.Criterion),
			Result: j.Judgment,
			Detail: truncateDetail(j.Reason),
			At:     stamp,
		}
	}
	return out
}
