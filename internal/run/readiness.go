package run

import (
	"context"
	"fmt"
	"reflect"
	"strconv"
	"strings"

	"github.com/kninetimmy/orch/internal/lockfile"
	"github.com/kninetimmy/orch/internal/manifest"
	"github.com/kninetimmy/orch/internal/state"
)

// ReadinessSchemaVersion versions inspection, independently of recorded gates.
const ReadinessSchemaVersion = 1

// ReadinessDoc is an unlocked evidence snapshot, never merge authorization.
type ReadinessDoc struct {
	SchemaVersion int                  `json:"schema_version"`
	RunID         string               `json:"run_id"`
	IssueNumber   int                  `json:"issue_number"`
	Phase         state.Phase          `json:"phase"`
	PRNumber      int                  `json:"pr_number"`
	PRURL         string               `json:"pr_url"`
	PRState       string               `json:"pr_state,omitempty"`
	HeadOID       string               `json:"head_oid,omitempty"`
	HeadDrift     bool                 `json:"head_drift"`
	Review        ReadinessReview      `json:"review"`
	CI            *MergeReportCI       `json:"ci,omitempty"`
	MergeReport   ReadinessMergeReport `json:"merge_report"`
	Merge         ReadinessMerge       `json:"merge"`
	Requirements  []string             `json:"requirements"`
	Limitations   []string             `json:"limitations"`
}

// ReadinessReview identifies the latest cycle in the issue's durable audit home.
// Current means consistent evidence at the live head, not an approving verdict.
type ReadinessReview struct {
	Cycles       int                    `json:"cycles"`
	Verdict      string                 `json:"verdict"`
	Evidence     *manifest.Verification `json:"evidence,omitempty"`
	Current      bool                   `json:"current"`
	Requirements []string               `json:"requirements"`
}

// ReadinessMergeReport distinguishes a preview from a previously pinned gate.
// Recorded means a pin exists in state, not that its phase/head is still valid.
// PrerequisitesMet is inspection only, including review freshness; it neither
// calls MergeReport nor promises its later invocation will succeed.
type ReadinessMergeReport struct {
	Recorded         bool               `json:"recorded"`
	PinnedHeadOID    string             `json:"pinned_head_oid,omitempty"`
	HeadCurrent      bool               `json:"head_current"`
	PrerequisitesMet bool               `json:"prerequisites_met"`
	Requirements     []string           `json:"requirements"`
	Preview          *MergeReportResult `json:"preview,omitempty"`
}

// ReadinessMerge deliberately cannot hold an approval or evaluate a grant.
type ReadinessMerge struct {
	Authorized       bool     `json:"authorized"`
	ApprovalRequired string   `json:"approval_required"`
	Requirements     []string `json:"requirements"`
}

// Readiness reads local state/configuration and GitHub evidence only. In
// particular it never uses loadVerb (session attribution), CI (audit writes),
// MergeReport (head pinning/status/metrics), or any grant-store operation.
func Readiness(ctx context.Context, env Env, issueNumber int) (*ReadinessDoc, error) {
	if issueNumber <= 0 {
		return nil, fmt.Errorf("%w: issue_number must be a positive integer", ErrBadRequest)
	}
	st, err := state.Load(env.RepoRoot)
	if err != nil {
		return nil, err
	}
	owner, err := lockfile.Inspect(env.RepoRoot)
	if err != nil {
		return nil, err
	}
	if err := state.CheckConsistent(st, owner); err != nil {
		return nil, err
	}
	if st.Mode != state.ModeDelivery {
		return nil, ErrNoDeliveryRun
	}
	var issue *state.Issue
	for i := range st.Run.Issues {
		if st.Run.Issues[i].Number == issueNumber {
			if issue != nil {
				return nil, fmt.Errorf("%w: issue_number %d matches more than one run issue", ErrUnknownIssue, issueNumber)
			}
			issue = &st.Run.Issues[i]
		}
	}
	if issue == nil {
		return nil, fmt.Errorf("%w: issue_number %d", ErrUnknownIssue, issueNumber)
	}
	doc := &ReadinessDoc{
		SchemaVersion: ReadinessSchemaVersion, RunID: st.Run.ID, IssueNumber: issue.Number,
		Phase: issue.Phase, PRNumber: issue.PRNumber, PRURL: issue.PRURL,
		Review:       ReadinessReview{Cycles: issue.ReviewCycles, Verdict: issue.LastReviewVerdict, Requirements: []string{}},
		MergeReport:  ReadinessMergeReport{Recorded: issue.ApprovedHeadOID != "", PinnedHeadOID: issue.ApprovedHeadOID, Requirements: []string{}},
		Requirements: []string{},
		Limitations: []string{
			"Read-only unlocked snapshot; GitHub probes are not atomic. Re-inspect after changes and let lifecycle verbs recheck their preconditions.",
			"Review evidence is from the issue audit record, checked against run state and the open PR mirror. Manifest consistency is not cryptographic provenance; consistently hand-edited records cannot be detected.",
			"Preview is not a recorded merge gate. No git/worktree/test/host preflights or grant authorization checks were performed; no approval is recorded or supplied by this call.",
		},
	}
	cfg, err := loadExecutionConfig(env, st)
	if err != nil {
		doc.Requirements = append(doc.Requirements, err.Error())
	}
	if st.Run.StoppedReason != "" {
		doc.Requirements = append(doc.Requirements, fmt.Sprintf("%s: %s; explicit recovery or abort is required", ErrRunStopped, st.Run.StoppedReason))
	}
	if err := checkApprovedWork(issue); err != nil {
		doc.Requirements = append(doc.Requirements, err.Error())
	}
	if issue.Decision == nil {
		doc.Requirements = append(doc.Requirements, "The issue needs its approved routing decision; abort and re-plan rather than inventing routing.")
	}
	if issue.Phase == state.PhaseBlocked {
		doc.Requirements = append(doc.Requirements, "The issue is blocked: "+issue.BlockedReason+"; inspection cannot resolve decisions or perform recovery.")
	}
	if !phaseAllowed(issue.Phase, []state.Phase{state.PhaseInReview, state.PhaseAwaitingMerge}) {
		doc.MergeReport.Requirements = append(doc.MergeReport.Requirements, "A recorded approving review in phase in-review is required before merge-report; awaiting-merge identifies an already recorded gate.")
	}
	if issue.PRNumber == 0 {
		doc.Requirements = append(doc.Requirements, "No PR is recorded; open and record the issue's PR before review or merge inspection.")
		doc.Review.Requirements = append(doc.Review.Requirements, "Review evidence and live head are unverified without a PR.")
		finishReadiness(doc)
		return doc, nil
	}

	gh, err := openGitHub(ctx, env)
	if err != nil {
		return nil, err
	}
	pr, err := gh.PR(ctx, issue.PRNumber)
	if err != nil {
		return nil, err
	}
	doc.PRURL, doc.PRState, doc.HeadOID = pr.URL, pr.State, pr.HeadRefOid
	if !headOIDPattern.MatchString(pr.HeadRefOid) || pr.URL == "" || pr.HeadRefName != issue.Branch || (issue.PRURL != "" && issue.PRURL != pr.URL) {
		doc.Requirements = append(doc.Requirements, "Live PR identity/head is missing or contradicts the recorded issue PR/branch.")
	}
	if pr.State != "OPEN" {
		doc.Requirements = append(doc.Requirements, fmt.Sprintf("PR #%d is %s, not OPEN.", pr.Number, pr.State))
	}
	_, m, err := readIssueManifest(ctx, gh, issue.Number)
	if err != nil {
		doc.Review.Requirements = append(doc.Review.Requirements, "Issue review evidence is unverified: "+err.Error())
	} else {
		evidence, err := readinessReviewEvidence(issue, st.Run.Plan.ConfigRevision, m)
		doc.Review.Evidence = evidence
		if err != nil {
			doc.Review.Requirements = append(doc.Review.Requirements, err.Error())
		} else if pr.State == "OPEN" {
			mirror, err := manifest.Parse(pr.Body)
			if err != nil {
				doc.Review.Requirements = append(doc.Review.Requirements, "PR audit mirror is unverified: "+err.Error())
			} else if !sameReadinessAudit(m, mirror) {
				doc.Review.Requirements = append(doc.Review.Requirements, "Issue and PR audit records contradict each other.")
			}
		}
		if evidence != nil && evidence.CommitOID != "" && evidence.CommitOID != pr.HeadRefOid {
			doc.HeadDrift = true
			doc.Review.Requirements = append(doc.Review.Requirements, "The reviewed commit differs from the live PR head; re-review the stable live head.")
		}
		// A request-changes cycle can be current evidence without approval.
		doc.Review.Current = evidence != nil && evidence.CommitOID == pr.HeadRefOid && len(doc.Review.Requirements) == 0
	}
	if err := checkApprovingReview(issue); err != nil {
		doc.Review.Requirements = append(doc.Review.Requirements, err.Error())
	}
	summary, err := gh.RequiredCI(ctx, issue.PRNumber)
	if err != nil {
		return nil, fmt.Errorf("readiness required CI: %w", err)
	}
	doc.CI = &MergeReportCI{State: string(summary.State), Required: ciChecks(summary), Total: summary.Total}
	if err := requireMergeableCI(summary.State); err != nil {
		doc.MergeReport.Requirements = append(doc.MergeReport.Requirements, err.Error())
	}
	// Bracket CI/audit probes with PR reads; never attach their evidence to a
	// different live head or a PR that closed while inspection was running.
	after, err := gh.PR(ctx, issue.PRNumber)
	if err != nil {
		return nil, err
	}
	if after.HeadRefOid != pr.HeadRefOid || after.State != pr.State || after.HeadRefName != pr.HeadRefName || after.URL != pr.URL || after.Body != pr.Body {
		return nil, fmt.Errorf("PR #%d changed during readiness inspection; re-inspect its stable live head", pr.Number)
	}
	if cfg != nil {
		doc.MergeReport.Preview = mergeReportResult(issue, pr, summary, cfg.Merge.Strategy, cfg.ConfigRevision)
	}
	doc.MergeReport.HeadCurrent = issue.ApprovedHeadOID != "" && issue.ApprovedHeadOID == pr.HeadRefOid
	if issue.ApprovedHeadOID != "" && !doc.MergeReport.HeadCurrent {
		doc.HeadDrift = true
		doc.MergeReport.Requirements = append(doc.MergeReport.Requirements, "The pinned merge-report head differs from the live PR head; the recorded gate is stale and must be refreshed through the normal workflow.")
	}
	finishReadiness(doc)
	return doc, nil
}

func finishReadiness(doc *ReadinessDoc) {
	doc.MergeReport.PrerequisitesMet = len(doc.Requirements) == 0 && len(doc.Review.Requirements) == 0 && doc.Review.Current && len(doc.MergeReport.Requirements) == 0
	doc.Merge.Requirements = append([]string{}, doc.Requirements...)
	doc.Merge.Requirements = append(doc.Merge.Requirements, doc.Review.Requirements...)
	doc.Merge.Requirements = append(doc.Merge.Requirements, doc.MergeReport.Requirements...)
	if doc.Phase != state.PhaseAwaitingMerge || !doc.MergeReport.Recorded {
		doc.Merge.Requirements = append(doc.Merge.Requirements, "The normal merge-report verb must record an awaiting-merge gate; this preview does not pin a head or advance phase.")
	}
	if !doc.MergeReport.HeadCurrent {
		doc.Merge.Requirements = append(doc.Merge.Requirements, "A recorded merge-report head matching the live PR head is required.")
	}
	doc.Merge.ApprovalRequired = fmt.Sprintf("Fresh human approval with statement %q must separately cover this issue's PR and the exact, still-current head recorded by merge-report. Existing separately authorized grant contracts are unchanged, but are not inspected, validated or spent here.", MergeApprovalStatement)
	doc.Merge.Requirements = append(doc.Merge.Requirements, doc.Merge.ApprovalRequired)
}

// The state verdict alone is not commit evidence. Review writes state first,
// then the issue record, then the PR mirror, so partial writes must fail closed.
func readinessReviewEvidence(issue *state.Issue, revision string, m manifest.Manifest) (*manifest.Verification, error) {
	name := fmt.Sprintf("review-cycle-%d", issue.ReviewCycles)
	var evidence *manifest.Verification
	seen := map[string]bool{}
	cycles := 0
	for i := range m.Verifications {
		v := &m.Verifications[i]
		if reviewCycleNamePattern.MatchString(v.Name) || acceptanceCriterionNamePattern.MatchString(v.Name) {
			if seen[v.Name] {
				return nil, fmt.Errorf("audit evidence is contradictory: duplicate %s", v.Name)
			}
			seen[v.Name] = true
		}
		if reviewCycleNamePattern.MatchString(v.Name) {
			n, err := strconv.Atoi(strings.TrimPrefix(v.Name, "review-cycle-"))
			if err != nil || n <= 0 || n > issue.ReviewCycles || v.Name != fmt.Sprintf("review-cycle-%d", n) {
				return nil, fmt.Errorf("audit review cycle %s contradicts the recorded cycle count %d", v.Name, issue.ReviewCycles)
			}
			cycles++
		}
		if v.Name == name {
			evidence = v
		}
	}
	if evidence == nil {
		return nil, fmt.Errorf("review evidence %s is missing from the issue audit record; no reviewed commit is verified", name)
	}
	if cycles != issue.ReviewCycles {
		return evidence, fmt.Errorf("audit review history is incomplete: found %d of %d recorded cycles", cycles, issue.ReviewCycles)
	}
	if !sameWork(issue, approvedWork{objective: m.Objective, acceptanceCriteria: m.AcceptanceCriteria, requiredTests: m.RequiredTests, testsCIDoesNotRun: m.TestsCIDoesNotRun}) ||
		m.ConfigRevision != revision || issue.Decision == nil || m.Role != issue.Decision.Role || m.Executor != issue.Decision.Executor || m.Reviewer != issue.Decision.Reviewer {
		return evidence, fmt.Errorf("audit work, routing or configuration contradicts the approved run")
	}
	if evidence.Result != issue.LastReviewVerdict || (evidence.Result != VerdictApprove && evidence.Result != VerdictRequestChanges) || !headOIDPattern.MatchString(evidence.CommitOID) {
		return evidence, fmt.Errorf("audit review verdict/commit is missing, invalid or contradicts run state")
	}
	// Judgment detail can legitimately rotate out under the body cap; its
	// result and commit cannot. Require every current criterion's evidence.
	for n := range issue.AcceptanceCriteria {
		name := fmt.Sprintf("acceptance-criterion-%d", n+1)
		if !seen[name] {
			return evidence, fmt.Errorf("audit review judgment %s is missing", name)
		}
	}
	for _, v := range m.Verifications {
		if !acceptanceCriterionNamePattern.MatchString(v.Name) {
			continue
		}
		n, err := strconv.Atoi(strings.TrimPrefix(v.Name, "acceptance-criterion-"))
		if err != nil || n < 1 || n > len(issue.AcceptanceCriteria) || v.CommitOID != evidence.CommitOID ||
			(v.Result != JudgmentSatisfied && v.Result != JudgmentUnsatisfied && v.Result != JudgmentWrong) ||
			(evidence.Result == VerdictApprove && v.Result != JudgmentSatisfied) {
			return evidence, fmt.Errorf("audit review judgment %s contradicts the latest review verdict/commit", v.Name)
		}
	}
	return evidence, nil
}

// Body-cap rotation can remove different Detail text from the two bodies.
// Compare canonical non-detail evidence, not prose or independently capped text.
func sameReadinessAudit(a, b manifest.Manifest) bool {
	a.Verifications = append([]manifest.Verification(nil), a.Verifications...)
	b.Verifications = append([]manifest.Verification(nil), b.Verifications...)
	for i := range a.Verifications {
		a.Verifications[i].Detail = ""
	}
	for i := range b.Verifications {
		b.Verifications[i].Detail = ""
	}
	return reflect.DeepEqual(a, b)
}
