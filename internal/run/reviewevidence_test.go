package run

import (
	"encoding/json"
	"errors"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/kninetimmy/orch/internal/execx/execxtest"
	"github.com/kninetimmy/orch/internal/ghops"
	"github.com/kninetimmy/orch/internal/manifest"
	"github.com/kninetimmy/orch/internal/state"
)

func evidenceReview(verdict, second string) ReviewRequest {
	return ReviewRequest{
		SchemaVersion: ReviewSchemaVersion, IssueNumber: 1, ReviewedHeadOID: readinessHead,
		Verdict: verdict, Summary: "Independent criterion-by-criterion review",
		Reviewer: baseManifest().Reviewer,
		Judgments: []CriterionJudgment{
			{1, JudgmentSatisfied, "The implementation and deterministic checks cover the race."},
			{2, second, "Need an identifiable execution log for the owner display."},
		},
	}
}

func executionArtifact(head string) ExecutionEvidence {
	return ExecutionEvidence{Criterion: 2, HeadOID: head,
		Artifact: "file:///evidence/owner-output.log#sha256=0123456789abcdef",
		Command:  "existing-owner-check --show-owner", Result: "pass: owner=worker-1",
		CollectedBy: "executor-session-1", CollectedAt: fixedNow().Format("2006-01-02T15:04:05Z07:00")}
}

// Only scripted GitHub calls are available: no shell command, model, provider,
// artifact fetch or inferred execution permission can slip into this path.
func recordEvidenceReview(t *testing.T, root, body string, req ReviewRequest) string {
	t.Helper()
	calls := []execxtest.Call{
		ghAuth(), ghPRViewCall(10, "OPEN", req.ReviewedHeadOID),
		ghIssueViewCall(t, 1, "OPEN", body), ghSetIssueBodyCall(1), ghSetPRBodyCall(10),
	}
	if req.Verdict == VerdictNeedsEvidence {
		calls = append(calls, ghSetStatusCall(1, ghops.StatusAwaitingReview))
	} else if len(wrongCriteria(req.Judgments)) > 0 {
		calls = append(calls, ghSetStatusCall(1, ghops.StatusNeedsHuman))
	} else if req.Verdict == VerdictRequestChanges {
		calls = append(calls, ghSetStatusCall(1, ghops.StatusInProgress))
	}
	res, script := runVerbScript(t, root, Review, string(requestBytes(t, req)), calls...)
	if res.Verdict != req.Verdict {
		t.Fatalf("result = %+v", res)
	}
	return script.StdinAt(3)
}

func refuseEvidenceReview(t *testing.T, root string, req ReviewRequest, want error, calls ...execxtest.Call) {
	t.Helper()
	before := stateBytes(t, root)
	script := &execxtest.Script{T: t, Calls: calls}
	_, err := Review(t.Context(), ghEnv(root, script), requestBytes(t, req))
	if !errors.Is(err, want) {
		t.Fatalf("error = %v, want %v", err, want)
	}
	script.AssertExhausted()
	if string(before) != string(stateBytes(t, root)) {
		t.Fatal("refused review changed run state")
	}
}

func TestEvidenceOnlyReviewAndSameHeadRecovery(t *testing.T) {
	root := setupDeliveryRepo(t, "r1", []state.Issue{twoCriteriaIssue(state.PhaseInReview)})
	// A deliberately nonexistent grant proves this outcome does not even open
	// the grant store or evaluate/spend a code-fix cycle.
	st := loadRun(t, root)
	st.Run.Plan.ApprovedBy = "grant:nonexistent"
	if err := state.Save(root, st); err != nil {
		t.Fatal(err)
	}
	req := evidenceReview(VerdictNeedsEvidence, JudgmentUnverified)
	body := recordEvidenceReview(t, root, twoCriteriaBody(t), req)
	issue := loadRun(t, root).Run.Issues[0]
	if issue.Phase != state.PhaseInReview || issue.LastReviewVerdict != VerdictNeedsEvidence ||
		issue.ReviewedHeadOID != readinessHead || !slices.Equal(issue.PendingEvidence, []int{2}) || len(issue.Blocks) != 0 {
		t.Fatalf("reopened state = %+v", issue)
	}
	doc, err := Next(t.Context(), Env{RepoRoot: root})
	if err != nil || doc.Actions[0].Action != "collect-evidence-and-review" ||
		!strings.Contains(doc.Actions[0].Prerequisites[0], readinessHead) {
		t.Fatalf("next = %+v, %v", doc, err)
	}
	m, err := manifest.Parse(body)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := readinessReviewEvidence(&issue, "r1", m); err != nil {
		t.Fatal(err)
	}
	v := findVerification(t, body, "review-evidence-1")
	var receipt reviewEvidenceRecord
	if err := json.Unmarshal([]byte(v.Detail), &receipt); err != nil ||
		v.CommitOID != readinessHead || v.Result != VerdictNeedsEvidence ||
		!reflect.DeepEqual(receipt.Judgments, req.Judgments) || !slices.Equal(receipt.PendingCriteria, []int{2}) {
		t.Fatalf("durable receipt = %+v / %+v, %v", v, receipt, err)
	}
	// Neither merge path may get as far as opening GitHub or accepting CI.
	for _, ci := range []*HumanCIException{nil, {Statement: CIExceptionStatement}} {
		script := &execxtest.Script{T: t}
		_, err := MergeReport(t.Context(), ghEnv(root, script), requestBytes(t, MergeReportRequest{SchemaVersion: 1, IssueNumber: 1, CIException: ci}))
		if err == nil || !strings.Contains(err.Error(), "no approving review") {
			t.Fatalf("merge-report = %v", err)
		}
		script.AssertExhausted()
	}
	// A new review cannot simply relabel an unverified criterion as satisfied.
	req = evidenceReview(VerdictApprove, JudgmentSatisfied)
	refuseEvidenceReview(t, root, req, ErrBadRequest, ghAuth(), ghPRViewCall(10, "OPEN", readinessHead))
	req.ExecutionEvidence = []ExecutionEvidence{executionArtifact(readinessHead)}
	req.Reviewer.Model = "not-the-routed-reviewer"
	refuseEvidenceReview(t, root, req, ErrReviewerMismatch)
	req.Reviewer = baseManifest().Reviewer
	body = recordEvidenceReview(t, root, body, req) // no fix commit / head change
	issue = loadRun(t, root).Run.Issues[0]
	if issue.LastReviewVerdict != VerdictApprove || issue.ReviewCycles != 2 || len(issue.PendingEvidence) != 0 || checkApprovingReview(&issue) != nil {
		t.Fatalf("same-head recovery = %+v", issue)
	}
	m, err = manifest.Parse(body)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := readinessReviewEvidence(&issue, "r1", m); err != nil {
		t.Fatal(err)
	}
	// Evidence recovery does not waive the existing required-CI gate.
	script := &execxtest.Script{T: t, Calls: joinCalls(
		[]execxtest.Call{ghAuth(), ghPRViewCall(10, "OPEN", readinessHead)}, ciCalls("pending"))}
	_, err = MergeReport(t.Context(), ghEnv(root, script), []byte(`{"schema_version":1,"issue_number":1}`))
	if !errors.Is(err, ErrCINotReady) {
		t.Fatalf("pending CI = %v", err)
	}
	script.AssertExhausted()
	// The ordinary merge gate must not pin an unreviewed newer head.
	for _, ci := range []*HumanCIException{nil, {
		RunID: st.Run.ID, IssueNumber: 1, PRNumber: 10, HeadOID: readinessOldHead,
		ApprovedBy: "alice", ApprovedAt: fixedNow(), Reason: "explicit human decision", Statement: CIExceptionStatement,
	}} {
		script = &execxtest.Script{T: t, Calls: []execxtest.Call{ghAuth(), ghPRViewCall(10, "OPEN", readinessOldHead)}}
		_, err = MergeReport(t.Context(), ghEnv(root, script), requestBytes(t, MergeReportRequest{SchemaVersion: 1, IssueNumber: 1, CIException: ci}))
		if !errors.Is(err, ErrReviewStale) {
			t.Fatalf("stale merge-report = %v", err)
		}
		script.AssertExhausted()
	}
	// Even a fresh review on that newer head needs new execution evidence.
	req.ReviewedHeadOID, req.ExecutionEvidence = readinessOldHead, nil
	refuseEvidenceReview(t, root, req, ErrBadRequest, ghAuth(), ghPRViewCall(10, "OPEN", readinessOldHead))
	req.ExecutionEvidence = []ExecutionEvidence{executionArtifact(readinessHead)}
	refuseEvidenceReview(t, root, req, ErrReviewStale, ghAuth(), ghPRViewCall(10, "OPEN", readinessOldHead))
	req.ExecutionEvidence[0].HeadOID = readinessOldHead
	recordEvidenceReview(t, root, body, req)
	report := runVerb(t, root, MergeReport, `{"schema_version":1,"issue_number":1}`,
		ghAuth(), ghPRViewCall(10, "OPEN", readinessOldHead), ghRollupEmptyCall(10),
		ghSetStatusCall(1, ghops.StatusNeedsHuman))
	issue = loadRun(t, root).Run.Issues[0]
	a := nextIssue(loadRun(t, root).Run, &issue)
	if report.HeadOID != readinessOldHead || a.Action != "merge" || !strings.Contains(a.ApprovalRequired, MergeApprovalStatement) {
		t.Fatalf("new report bypassed human gate: %+v / %+v", report, a)
	}
}

func TestEvidenceRecoveryPartialAuditWriteCannotDischargeRequest(t *testing.T) {
	for _, target := range []string{"issue", "PR"} {
		t.Run(target, func(t *testing.T) {
			root := setupDeliveryRepo(t, "r1", []state.Issue{twoCriteriaIssue(state.PhaseInReview)})
			body := recordEvidenceReview(t, root, twoCriteriaBody(t), evidenceReview(VerdictNeedsEvidence, JudgmentUnverified))
			req := evidenceReview(VerdictApprove, JudgmentSatisfied)
			req.ExecutionEvidence = []ExecutionEvidence{executionArtifact(readinessHead)}
			calls := []execxtest.Call{ghAuth(), ghPRViewCall(10, "OPEN", readinessHead), ghIssueViewCall(t, 1, "OPEN", body)}
			failure := ghSetIssueBodyCall(1)
			if target == "PR" {
				calls = append(calls, failure)
				failure = ghSetPRBodyCall(10)
			}
			failure.Exit, failure.Stderr = 1, "audit write failed"
			calls = append(calls, failure)
			script := &execxtest.Script{T: t, Calls: calls}
			if _, err := Review(t.Context(), ghEnv(root, script), requestBytes(t, req)); err == nil {
				t.Fatal("failed audit write accepted")
			}
			script.AssertExhausted()
			issue := loadRun(t, root).Run.Issues[0]
			if issue.ReviewedHeadOID != "" || !slices.Equal(issue.PendingEvidence, []int{2}) || checkApprovingReview(&issue) == nil {
				t.Fatalf("partial audit write authorized a merge or erased pending evidence: %+v", issue)
			}
			req.ExecutionEvidence = nil
			refuseEvidenceReview(t, root, req, ErrBadRequest, ghAuth(), ghPRViewCall(10, "OPEN", readinessHead))
		})
	}
}

func TestEvidenceReviewPreservesCollectionTimestampPrecision(t *testing.T) {
	issue := twoCriteriaIssue(state.PhaseInReview)
	issue.PendingEvidence, issue.EvidenceCriteria = []int{2}, []int{2}
	root := setupDeliveryRepo(t, "r1", []state.Issue{issue})
	req := evidenceReview(VerdictApprove, JudgmentSatisfied)
	req.ExecutionEvidence = []ExecutionEvidence{executionArtifact(readinessHead)}
	req.ExecutionEvidence[0].CollectedAt = fixedNow().Add(250 * time.Millisecond).Format(time.RFC3339Nano)
	script := &execxtest.Script{T: t, Calls: []execxtest.Call{
		ghAuth(), ghPRViewCall(10, "OPEN", readinessHead),
		ghIssueViewCall(t, 1, "OPEN", twoCriteriaBody(t)), ghSetIssueBodyCall(1), ghSetPRBodyCall(10),
	}}
	env := ghEnv(root, script)
	env.Now = func() time.Time { return fixedNow().Add(500 * time.Millisecond) }
	if _, err := Review(t.Context(), env, requestBytes(t, req)); err != nil {
		t.Fatal(err)
	}
	script.AssertExhausted()
	m, err := manifest.Parse(script.StdinAt(3))
	if err != nil {
		t.Fatal(err)
	}
	issue = loadRun(t, root).Run.Issues[0]
	cycle := findVerification(t, script.StdinAt(3), "review-cycle-1")
	receipt := findVerification(t, script.StdinAt(3), "review-evidence-1")
	if cycle.At != fixedNow().Format(time.RFC3339) || receipt.At != env.Now().Format(time.RFC3339Nano) {
		t.Fatalf("legacy review / precise evidence timestamps = %q / %q", cycle.At, receipt.At)
	}
	if _, err := readinessReviewEvidence(&issue, "r1", m); err != nil {
		t.Fatalf("valid sub-second collection became future evidence after reload: %v", err)
	}
	var record reviewEvidenceRecord
	if err := json.Unmarshal([]byte(receipt.Detail), &record); err != nil {
		t.Fatal(err)
	}
	record.ExecutionEvidence[0].CollectedAt = fixedNow().Add(750 * time.Millisecond).Format(time.RFC3339Nano)
	m.Verifications[len(m.Verifications)-1].Detail = string(requestBytes(t, record))
	if _, err := readinessReviewEvidence(&issue, "r1", m); err == nil {
		t.Fatal("evidence collected after the precise review instant was accepted")
	}
}

func TestEvidenceReviewContradictions(t *testing.T) {
	for _, tc := range []struct{ verdict, judgment string }{
		{VerdictApprove, JudgmentUnverified},
		{VerdictNeedsEvidence, JudgmentUnsatisfied},
		{VerdictNeedsEvidence, JudgmentWrong},
		{VerdictNeedsEvidence, JudgmentSatisfied},
	} {
		t.Run(tc.verdict+"/"+tc.judgment, func(t *testing.T) {
			root := setupDeliveryRepo(t, "r1", []state.Issue{twoCriteriaIssue(state.PhaseInReview)})
			refuseEvidenceReview(t, root, evidenceReview(tc.verdict, tc.judgment), ErrBadRequest)
		})
	}
}

func TestEvidenceReadinessValidatesRetainedReceipts(t *testing.T) {
	for _, name := range []string{"current", "missing receipt", "stale head", "stale artifact", "mirror artifact mismatch"} {
		t.Run(name, func(t *testing.T) {
			root := setupDeliveryRepo(t, "r1", []state.Issue{twoCriteriaIssue(state.PhaseInReview)})
			body := recordEvidenceReview(t, root, twoCriteriaBody(t), evidenceReview(VerdictNeedsEvidence, JudgmentUnverified))
			issue := loadRun(t, root).Run.Issues[0]
			m, err := manifest.Parse(body)
			if err != nil {
				t.Fatal(err)
			}
			head, mirror := readinessHead, body
			switch name {
			case "missing receipt":
				m.Verifications = m.Verifications[:len(m.Verifications)-1]
				body = readinessBody(t, m)
				mirror = body
			case "stale head":
				head = readinessOldHead
			case "stale artifact", "mirror artifact mismatch":
				record := reviewEvidenceRecord{
					Judgments:         evidenceReview(VerdictNeedsEvidence, JudgmentUnverified).Judgments,
					ExecutionEvidence: []ExecutionEvidence{executionArtifact(readinessOldHead)},
					PendingCriteria:   []int{2},
				}
				m.Verifications[len(m.Verifications)-1].Detail = string(requestBytes(t, record))
				mirror = readinessBody(t, m)
				if name == "stale artifact" {
					body = mirror
				}
			}
			doc, err := inspectReadiness(t, root, 1, readinessCalls(t, issue, head, "OPEN", body, mirror, "no-checks")...)
			if err != nil || doc.Review.Current != (name == "current") || doc.MergeReport.PrerequisitesMet || doc.Merge.Authorized {
				t.Fatalf("readiness = %+v, %v", doc, err)
			}
		})
	}
}

func TestEvidenceOnlyAfterRepairDoesNotMarkRepairVerified(t *testing.T) {
	root := setupDeliveryRepo(t, "r1", []state.Issue{twoCriteriaIssue(state.PhaseInReview)})
	st := loadRun(t, root)
	st.Run.Issues[0].Repairs = []state.IntegrationRepair{{
		Number: 1, PhaseBefore: state.PhaseAwaitingMerge, HeadOID: readinessHead,
		AuthorizedBy: "alice", Reason: "integrate approved work", At: fixedNow().Format("2006-01-02T15:04:05Z07:00"),
	}}
	if err := state.Save(root, st); err != nil {
		t.Fatal(err)
	}
	req := evidenceReview(VerdictNeedsEvidence, JudgmentUnverified)
	req.RepairNumber = 1
	body := recordEvidenceReview(t, root, twoCriteriaBody(t), req)
	issue := loadRun(t, root).Run.Issues[0]
	if issue.Repairs[0].VerifiedHeadOID != "" || checkApprovingReview(&issue) == nil {
		t.Fatal("non-approving missing repair evidence became verified")
	}
	req = evidenceReview(VerdictApprove, JudgmentSatisfied)
	req.RepairNumber = 1
	req.ExecutionEvidence = []ExecutionEvidence{executionArtifact(readinessHead)}
	refuseEvidenceReview(t, root, req, ErrBadRequest, ghAuth(), ghPRViewCall(10, "OPEN", readinessHead))
	req.Verifications = []VerificationInput{{Name: "repair-tests", Command: "go test ./...", Result: "pass"}}
	recordEvidenceReview(t, root, body, req)
	issue = loadRun(t, root).Run.Issues[0]
	if issue.Repairs[0].VerifiedHeadOID != readinessHead || checkApprovingReview(&issue) != nil {
		t.Fatal("fresh repair verification and independent approval did not recover")
	}
}

func TestEvidencePersistsAcrossGenuineFindingAndResume(t *testing.T) {
	for _, judgment := range []string{JudgmentUnsatisfied, JudgmentWrong} {
		t.Run(judgment, func(t *testing.T) {
			root := setupDeliveryRepo(t, "r1", []state.Issue{twoCriteriaIssue(state.PhaseInReview)})
			req := evidenceReview(VerdictRequestChanges, JudgmentUnverified)
			req.Judgments[0].Judgment = judgment
			recordEvidenceReview(t, root, twoCriteriaBody(t), req)
			issue := loadRun(t, root).Run.Issues[0]
			if !slices.Equal(issue.PendingEvidence, []int{2}) {
				t.Fatalf("pending evidence lost: %+v", issue)
			}
			a := nextIssue(loadRun(t, root).Run, &issue)
			want := "fix-and-review"
			if judgment == JudgmentWrong {
				want = "resolve-block"
			}
			if a.Action != want {
				t.Fatalf("next = %+v, want %s", a, want)
			}
			if judgment == JudgmentUnsatisfied {
				obs := okManifest(1)
				obs.work = &approvedWork{issue.Objective, issue.AcceptanceCriteria, issue.RequiredTests, issue.TestsCIDoesNotRun}
				obs.pr = &ghops.PR{Number: 10, State: "OPEN", HeadRefOid: readinessOldHead}
				applyOutcome(&issue, reconcileIssue(issue, obs))
				if !slices.Equal(issue.PendingEvidence, []int{2}) || issue.Phase != state.PhaseInReview {
					t.Fatal("resume erased pending evidence")
				}
				req = evidenceReview(VerdictApprove, JudgmentSatisfied)
				req.ReviewedHeadOID = readinessOldHead
				refuseEvidenceReview(t, root, req, ErrBadRequest, ghAuth(), ghPRViewCall(10, "OPEN", readinessOldHead))
			}
		})
	}
}

func TestExecutionEvidenceValidation(t *testing.T) {
	for _, name := range []string{"stale head", "short head", "missing artifact", "missing command", "missing result", "missing attribution", "missing time", "future time", "duplicate", "out of range", "before repair"} {
		t.Run(name, func(t *testing.T) {
			issue := twoCriteriaIssue(state.PhaseInReview)
			issue.PendingEvidence = []int{2}
			req := evidenceReview(VerdictApprove, JudgmentSatisfied)
			e := executionArtifact(readinessHead)
			switch name {
			case "stale head":
				e.HeadOID = readinessOldHead
			case "short head":
				req.ReviewedHeadOID, e.HeadOID = "short", "short"
			case "missing artifact":
				e.Artifact = " "
			case "missing command":
				e.Command = ""
			case "missing result":
				e.Result = ""
			case "missing attribution":
				e.CollectedBy = ""
			case "missing time":
				e.CollectedAt = ""
			case "future time":
				e.CollectedAt = fixedNow().AddDate(1, 0, 0).Format("2006-01-02T15:04:05Z07:00")
			case "out of range":
				e.Criterion = 3
			case "before repair":
				issue.Repairs = []state.IntegrationRepair{{At: fixedNow().AddDate(0, 0, 1).Format("2006-01-02T15:04:05Z07:00")}}
			}
			req.ExecutionEvidence = []ExecutionEvidence{e}
			if name == "duplicate" {
				req.ExecutionEvidence = append(req.ExecutionEvidence, e)
			}
			if _, err := checkExecutionEvidence(&issue, req, req.ReviewedHeadOID, fixedNow()); err == nil {
				t.Fatal("invalid evidence accepted")
			}
		})
	}
}

func TestEvidenceHistoryRetentionAndGrantAccounting(t *testing.T) {
	req := evidenceReview(VerdictNeedsEvidence, JudgmentUnverified)
	receipt, err := reviewEvidenceReceipt(req, []int{2}, 1, readinessHead, fixedNow().Format("2006-01-02T15:04:05Z07:00"))
	if err != nil {
		t.Fatal(err)
	}
	m := baseManifest()
	m.Verifications = []manifest.Verification{
		{Name: "old", Result: "pass", Detail: strings.Repeat("x", 2000)},
		{Name: "review-cycle-1", Result: VerdictNeedsEvidence}, receipt,
		{Name: "review-cycle-2", Result: VerdictApprove},
	}
	if n, over := fixCycleBlocked(m, 1); n != 1 || over {
		t.Fatalf("historical needs-evidence consumed a fix cycle: %d/%t", n, over)
	}
	m.Verifications = append(m.Verifications, manifest.Verification{Name: "review-cycle-3", Result: VerdictRequestChanges})
	if n, over := fixCycleBlocked(m, 1); n != 2 || !over {
		t.Fatalf("genuine finding did not count: %d/%t", n, over)
	}
	for _, n := range []string{"review-evidence-1", "review-evidence-222"} {
		if err := rejectEngineOwnedNames([]manifest.Verification{{Name: n}}); !errors.Is(err, ErrBadRequest) {
			t.Fatal("evidence receipt name is caller-writable")
		}
	}
	body, err := upsertCapped(strings.Repeat("p", 55000), m)
	if err != nil {
		t.Fatal(err)
	}
	if got := findVerification(t, body, receipt.Name); got.Detail != receipt.Detail {
		t.Fatal("body cap dropped evidence judgments, reasons or artifact identities")
	}
	if findVerification(t, body, "old").Detail != "" {
		t.Fatal("fixture did not exercise body-cap rotation")
	}
	if _, err := upsertCapped(strings.Repeat("p", bodyCapHeadroom), m); !errors.Is(err, ErrBodyTooLarge) {
		t.Fatalf("oversized retained evidence did not fail closed: %v", err)
	}
}

func TestEvidenceContractsRefuseVersionMismatch(t *testing.T) {
	root := setupDeliveryRepo(t, "r1", []state.Issue{twoCriteriaIssue(state.PhaseInReview)})
	req := evidenceReview(VerdictNeedsEvidence, JudgmentUnverified)
	req.SchemaVersion = 3
	refuseEvidenceReview(t, root, req, ErrBadRequest)
	st := loadRun(t, root)
	// Freeze the installed v2 engine's compatibility condition: permissive
	// state decoding must never reach a save that drops v3 review fields.
	if st.SchemaVersion == 6 && st.Run.Plan.ContractVersion == 2 && st.Run.Plan.ExecutionDigest != "" {
		t.Fatal("old execution policy accepts evidence-aware state")
	}
	st.Run.Plan.ContractVersion = 2
	if err := state.Save(root, st); err != nil {
		t.Fatal(err)
	}
	req.SchemaVersion = ReviewSchemaVersion
	refuseEvidenceReview(t, root, req, ErrBadApproval)
	before := string(stateBytes(t, root))
	script := &execxtest.Script{T: t}
	if _, err := Resume(t.Context(), ghEnv(root, script), ResumeRequest{}); !errors.Is(err, ErrBadApproval) || !strings.Contains(err.Error(), "original engine") {
		t.Fatalf("resume compatibility = %v", err)
	}
	doc, err := Next(t.Context(), ghEnv(root, script))
	if err != nil || doc.Actions[0].Action != "restore-or-replan" || before != string(stateBytes(t, root)) {
		t.Fatalf("next compatibility = %+v, %v", doc, err)
	}
	script.AssertExhausted()
}
