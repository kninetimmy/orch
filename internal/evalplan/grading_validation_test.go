//go:build corpus_validation

package evalplan

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/kninetimmy/orch/internal/evalcorpus"
)

type gradingFixture struct {
	controller *controllerFixture
	evaluation *Evaluation
	progress   *Progress
}

func newGradingFixture(t *testing.T, matched, repairs bool) *gradingFixture {
	t.Helper()
	f := newControllerFixture(t, "screen", 1)
	if matched {
		candidate := f.proposal.Baseline
		f.proposal.Candidate, f.proposal.Intervention = &candidate, "requested-profile"
		f.preview(t)
	}
	e := f.prepare(t)
	first := true
	p, err := run(t.Context(), f.root, e.ID, scriptedWorker{func(ctx context.Context, request workerRequest) (workerResult, error) {
		outcome := "native-completed"
		if repairs && first {
			outcome, first = "task-failure", false
		}
		output := privateSentinel
		return workerResult{Outcome: outcome, Output: &output}, nil
	}})
	if err != nil || p.State != "completed" {
		t.Fatalf("no-model preparation fixture: %+v %v", p, err)
	}
	return &gradingFixture{f, e, p}
}

func (f *gradingFixture) input(t *testing.T, unit, attempt int, id string) (GradeSubmission, Rubric, evalcorpus.Case) {
	t.Helper()
	a := readAttempt(t, f.controller, f.evaluation, unit, attempt)
	data, err := os.ReadFile(filepath.Join(f.controller.root, f.evaluation.ID, attemptName(unit, attempt), "case.json"))
	if err != nil {
		t.Fatal(err)
	}
	var c evalcorpus.Case
	if err := strictStored(data, &c); err != nil {
		t.Fatal(err)
	}
	r := Rubric{SchemaVersion: 1, Version: 1, ControllerOnly: true, CorpusSHA256: f.controller.record.Plan.Corpus.SHA256,
		CaseID: c.ID, CaseVersion: c.Version, CaseSHA256: a.CaseSHA256, PacketSHA256: c.PacketSHA256, KeySHA256: c.Key.SHA256, ProbeSHA256: c.Probe.SHA256,
		Role: c.Role, Classification: c.Classification, Author: Evaluator{Identity: "synthetic-author", Kind: "agent", Relationship: "author", IndependentOf: []string{}, SourceAccess: []string{"pinned-source", "key", "controls"}, Exposure: []string{"synthetic-only", "training-unknown"}},
		Requirements: []Requirement{{ID: "fact", Kind: "conclusion", Expectation: "Synthetic scoped fact", Severity: "major", EvidenceRoutes: []string{"source", "reproduction"}, Checks: []string{"TestCorpus/fixture"}}},
		Controls:     []ControlBinding{}, Blockers: []KeyedDefect{}, Alternatives: []string{"Any supported equivalent behavior/source/reproduction, without text or code-shape matching."}}
	if c.Role == "implementation" {
		r.Requirements = []Requirement{
			{ID: "behavior", Kind: "behavior", Expectation: "Required behavior", Severity: "major", EvidenceRoutes: []string{"reproduction"}, Checks: []string{"TestCorpus/fixture"}},
			{ID: "regression", Kind: "regression", Expectation: "Preserved behavior", Severity: "major", EvidenceRoutes: []string{"reproduction"}, Checks: []string{"TestCorpus/fixture"}},
			{ID: "scope", Kind: "scope", Expectation: "Component scope", Severity: "major", EvidenceRoutes: []string{"scope", "source"}, Checks: []string{}},
		}
	}
	if c.Classification == "defective" {
		r.Blockers = []KeyedDefect{{ID: "FIXTURE-BLOCKER", Severity: "major", Requirement: "fact", Description: "Synthetic defect"}}
	}
	for _, control := range c.Controls {
		r.Controls = append(r.Controls, ControlBinding{control.Name, storedDigest(control), control.ExpectedFailures})
	}
	artifact := func(name string, data []byte) Artifact {
		writeFixture(t, filepath.Join(f.controller.inputs, name), data)
		return Artifact{Path: name, SHA256: evalcorpus.Digest(data)}
	}
	rubric := artifact("rubric-"+c.ID+".json", fixtureJSON(t, r))
	source := artifact("source-evidence.txt", []byte(privateSentinel+"; synthetic source/citation evidence"))
	behavior := artifact("behavior-evidence.txt", []byte(privateSentinel+"; synthetic behavioral reproduction output"))
	s := GradeSubmission{SchemaVersion: 1, ID: id, Operation: "grade", EvaluationID: f.evaluation.ID, EvaluationSHA256: storedDigest(f.evaluation), PlanDigest: f.controller.record.PlanDigest,
		Unit: a.Unit, Attempt: a.Number, AttemptSHA256: f.progress.Slots[unit-1].Attempts[attempt-1].SHA256, CaseSHA256: a.CaseSHA256, PacketSHA256: a.PacketSHA256,
		AttemptArtifacts: attemptArtifacts(&a), Rubric: rubric, Evaluator: Evaluator{Identity: "synthetic-evaluator", Kind: "human", Relationship: "evaluator", IndependentOf: []string{}, SourceAccess: []string{"pinned-source", "key", "controls", "worker-output"}, Exposure: []string{"synthetic-only", "repair-feedback"}},
		Reason: privateSentinel + "; attributed preparation assertion", Evidence: []GradeEvidence{
			{ID: "source", Artifact: source, Route: "source", Citation: privateSentinel + " source trace"},
			{ID: "behavior", Artifact: behavior, Route: "reproduction", Citation: privateSentinel + " alternative check", Reproduction: &Reproduction{Command: []string{"never-execute-this-supplied-command"}, Phase: "behavior", Passed: []string{"Equivalent/alternative"}, Failed: []string{}}},
		}, Judgments: []Judgment{}}
	for _, req := range r.Requirements {
		evidence := "source"
		if req.Kind == "behavior" || req.Kind == "regression" {
			evidence = "behavior"
		}
		s.Judgments = append(s.Judgments, Judgment{req.ID, "satisfied", []string{evidence}, privateSentinel + " supports required behavior"})
	}
	if c.Role == "review" {
		s.Review = &ReviewJudgment{Verdict: "approve", Evidence: []string{"source"}, Reason: privateSentinel, Findings: []FindingJudgment{}}
		if c.Classification == "defective" {
			s.Review.Verdict = "request-changes"
			s.Review.Findings = append(s.Review.Findings, FindingJudgment{"finding", "FIXTURE-BLOCKER", "major", "supported", []string{"source"}, privateSentinel})
		}
	}
	return s, r, c
}

func (f *gradingFixture) submit(t *testing.T, s GradeSubmission) *GradeReceipt {
	t.Helper()
	file := filepath.Join(f.controller.inputs, s.ID+".json")
	writeFixture(t, file, fixtureJSON(t, s))
	receipt, err := SubmitGrade(f.controller.repo, f.controller.root, f.evaluation.ID, s.Unit.Ordinal, s.Attempt, file)
	if err != nil {
		t.Fatalf("submit %s: %v", s.ID, err)
	}
	return receipt
}

func validationInput(t *testing.T, f *gradingFixture, s GradeSubmission, r Rubric, c evalcorpus.Case, id string) GradeSubmission {
	t.Helper()
	s.ID, s.Operation, s.Judgments, s.Review, s.Supersedes = id, "validate-rubric", nil, nil, ""
	s.Evaluator.Identity, s.Evaluator.Relationship = "synthetic-independent-validator", "independent-validator"
	s.Evaluator.IndependentOf = []string{r.Author.Identity, "synthetic-evaluator"}
	s.Validation = &RubricValidation{Checks: []Judgment{}, Resolves: []string{}}
	for i, control := range c.Controls {
		name := fmt.Sprintf("control-%d", i)
		bytes := []byte(privateSentinel + " synthetic exact control output " + control.Name)
		writeFixture(t, filepath.Join(f.controller.inputs, name+".txt"), bytes)
		passed := []string{}
		if len(control.ExpectedFailures) == 0 {
			passed = append(passed, "TestCorpus/fixture")
		}
		s.Evidence = append(s.Evidence, GradeEvidence{ID: name, Artifact: Artifact{Path: name + ".txt", SHA256: evalcorpus.Digest(bytes)}, Route: "reproduction", Citation: privateSentinel,
			Reproduction: &Reproduction{Command: c.Command, Phase: "behavior", Passed: passed, Failed: slices.Clone(control.ExpectedFailures)}})
	}
	for _, check := range validationChecks(c, &r) {
		ids := []string{"source"}
		for i, control := range c.Controls {
			if check == "control:"+control.Name {
				ids = []string{fmt.Sprintf("control-%d", i)}
			}
		}
		s.Validation.Checks = append(s.Validation.Checks, Judgment{check, "satisfied", ids, privateSentinel + " synthetic validation assertion"})
	}
	return s
}

func gradingSnapshot(t *testing.T, f *gradingFixture) *Report {
	t.Helper()
	r, err := Inspect(f.controller.root, f.evaluation.ID)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func TestGradingRepairsAlternativesRegradesAndReports(t *testing.T) {
	if controllerTestProcess(t) {
		return
	}
	f := newGradingFixture(t, false, true)
	before, err := RetainReport(f.controller.root, f.evaluation.ID)
	if err != nil {
		t.Fatal(err)
	}
	oldReport, err := os.ReadFile(filepath.Join(before.Destination, "report.json"))
	if err != nil {
		t.Fatal(err)
	}
	s, r, c := f.input(t, 1, 1, "initial-failure")
	s.Judgments[0].State = "violated"
	s.Judgments[1].State = "violated"
	s.Evidence[1].Reproduction.Passed, s.Evidence[1].Reproduction.Failed = []string{}, []string{"Equivalent/alternative"}
	f.submit(t, s)
	if got := gradingSnapshot(t, f).Snapshot.Attempts[0].Grade; got != "unknown" {
		t.Fatal("author/evaluator agreement became independent validation")
	}
	validation := validationInput(t, f, s, r, c, "independent-assertion")
	f.submit(t, validation)
	final, _, _ := f.input(t, 1, 2, "final-success")
	f.submit(t, final)
	snapshot := gradingSnapshot(t, f)
	if snapshot.Snapshot.Grading.Units[0].Initial != "fail" || snapshot.Snapshot.Grading.Units[0].Final != "pass" || !snapshot.Snapshot.Attempts[0].Grading.AfterRepairFeedback || !snapshot.Snapshot.Attempts[1].Grading.PreparationOnly || snapshot.Snapshot.Attempts[1].Grading.AcceptedExecution {
		t.Fatal("initial/final/retrospective/preparation evidence collapsed")
	}
	for _, coverage := range snapshot.Snapshot.Grading.Coverage {
		if coverage.Role == "implementation" && coverage.Partition == "development" && (coverage.Stage == "initial" && coverage.Failed != 1 || coverage.Stage == "final" && coverage.Passed != 1) {
			t.Fatalf("coverage %+v", coverage)
		}
		if coverage.Scheduled == 0 && (coverage.Lower != nil || coverage.Upper != nil || !strings.HasPrefix(coverage.Status, "not-applicable")) {
			t.Fatal("zero opportunities treated as measured zero")
		}
		if coverage.Accepted != 0 {
			t.Fatal("preparation became model trial acceptance")
		}
	}
	// A behaviorally supported different check/algorithm is accepted, without
	// requiring TestCorpus names, reference prose, keywords or source shape.
	alternative, _, _ := f.input(t, 1, 1, "alternative-correction")
	alternative.Supersedes = s.ID
	f.submit(t, alternative)
	if gradingSnapshot(t, f).Snapshot.Grading.Units[0].Initial != "pass" {
		t.Fatal("supported alternative rejected")
	}
	r.Version++
	rubricBytes := fixtureJSON(t, r)
	writeFixture(t, filepath.Join(f.controller.inputs, "rubric-v2.json"), rubricBytes)
	validation = validationInput(t, f, alternative, r, c, "validate-replacement")
	validation.Rubric, validation.PreviousRubricSHA256 = Artifact{"rubric-v2.json", evalcorpus.Digest(rubricBytes)}, alternative.Rubric.SHA256
	f.submit(t, validation)
	snapshot = gradingSnapshot(t, f)
	if snapshot.Snapshot.Grading.Rubrics[0].AwaitingRegrade != 2 || snapshot.Snapshot.Grading.Units[0].Initial != "unknown" || snapshot.Snapshot.Grading.Units[0].Final != "unknown" {
		t.Fatal("replacement restored comparative validity without affected regrades")
	}
	regrade := alternative
	regrade.ID, regrade.Supersedes, regrade.Rubric = "regrade-initial", alternative.ID, validation.Rubric
	f.submit(t, regrade)
	leaveUnknown := final
	leaveUnknown.ID, leaveUnknown.Supersedes, leaveUnknown.Rubric = "explicit-final-unknown", final.ID, validation.Rubric
	for i := range leaveUnknown.Judgments {
		leaveUnknown.Judgments[i].State, leaveUnknown.Judgments[i].Evidence = "unknown", []string{}
	}
	f.submit(t, leaveUnknown)
	snapshot = gradingSnapshot(t, f)
	if snapshot.Snapshot.Grading.Rubrics[0].AwaitingRegrade != 0 || snapshot.Snapshot.Grading.Units[0].Final != "unknown" || len(snapshot.Snapshot.Grading.History) != 7 {
		t.Fatal("explicit unknown/regrade history missing")
	}
	for _, format := range []string{"text", "markdown", "json"} {
		var out bytes.Buffer
		if err := RenderReport(&out, snapshot, format); err != nil {
			t.Fatal(err)
		}
		if strings.Contains(out.String(), privateSentinel) || !strings.Contains(out.String(), "retrospective") {
			t.Fatal("private evaluator/worker prose leaked")
		}
		start, end := strings.Index(out.String(), "{\n"), strings.LastIndex(out.String(), "}")+1
		var parity Report
		if err := json.Unmarshal([]byte(out.String()[start:end]), &parity); err != nil || !reflect.DeepEqual(parity, *snapshot) {
			t.Fatalf("report parity: %v", err)
		}
	}
	if _, err := RetainReport(f.controller.root, f.evaluation.ID); err != nil {
		t.Fatal(err)
	}
	preserved, err := os.ReadFile(filepath.Join(before.Destination, "report.json"))
	if err != nil || !bytes.Equal(preserved, oldReport) {
		t.Fatal("previous retained report was overwritten")
	}
	status, err := Status(f.controller.root, f.evaluation.ID)
	if err != nil || !reflect.DeepEqual(status, f.progress) {
		t.Fatal("grading rewrote execution/progress evidence")
	}
	invalid := regrade
	invalid.ID, invalid.Operation, invalid.Judgments, invalid.Review, invalid.Supersedes = "invalidate-current", "invalidate-rubric", nil, nil, ""
	f.submit(t, invalid)
	if !gradingSnapshot(t, f).Snapshot.Grading.Rubrics[0].Invalidated || gradingSnapshot(t, f).Snapshot.Grading.Units[0].Initial != "unknown" {
		t.Fatal("invalidated rubric retained pass")
	}
}

func TestGradingReviewDisputesAndIndependentAdjudication(t *testing.T) {
	if controllerTestProcess(t) {
		return
	}
	f := newGradingFixture(t, true, false)
	unit := slices.IndexFunc(f.progress.Slots, func(s Slot) bool { return s.CaseID == "review-0-1" && s.Side == "baseline" }) + 1
	s, r, c := f.input(t, unit, 1, "review-good")
	validation := validationInput(t, f, s, r, c, "review-validation")
	f.submit(t, validation)
	dup := s.Review.Findings[0]
	dup.ID = "duplicate-trigger"
	s.Review.Findings = append(s.Review.Findings, dup)
	f.submit(t, s)
	report := gradingSnapshot(t, f)
	index := slices.IndexFunc(report.Snapshot.Attempts, func(a ReportAttempt) bool { return a.Unit.Ordinal == unit })
	if report.Snapshot.Attempts[index].Grade != "pass" || report.Snapshot.Attempts[index].Grading.Review.CaughtBlockers != 1 {
		t.Fatal("deduplicated real blocker not caught")
	}
	additional := s
	additional.ID, additional.Supersedes = "additional-defect", s.ID
	additional.Review = &ReviewJudgment{Verdict: "request-changes", Evidence: []string{"source"}, Reason: privateSentinel, Findings: append(slices.Clone(s.Review.Findings), FindingJudgment{"extra", "UNKEYED-REAL", "critical", "additional-real", []string{"source"}, privateSentinel})}
	f.submit(t, additional)
	report = gradingSnapshot(t, f)
	if report.Snapshot.Attempts[index].Grade != "unknown" || report.Snapshot.Attempts[index].Grading.Review.UnsupportedBlockers != 0 || len(report.Snapshot.Grading.Rubrics[0].OpenDisputes) != 1 {
		t.Fatal("additional alleged real defect became automatic false positive")
	}
	correction := s
	correction.ID, correction.Supersedes = "cannot-clear-dispute", additional.ID
	f.submit(t, correction)
	if gradingSnapshot(t, f).Snapshot.Attempts[index].Grade != "unknown" {
		t.Fatal("new grade erased unresolved dispute")
	}
	validation.ID, validation.Validation.Resolves = "same-version-adjudication", []string{additional.ID}
	file := filepath.Join(f.controller.inputs, "bad-validation.json")
	writeFixture(t, file, fixtureJSON(t, validation))
	if _, err := SubmitGrade(f.controller.repo, f.controller.root, f.evaluation.ID, unit, 1, file); err == nil {
		t.Fatal("unversioned dispute adjudication accepted")
	}
	r.Version++
	data := fixtureJSON(t, r)
	writeFixture(t, filepath.Join(f.controller.inputs, "review-rubric-v2.json"), data)
	validation.Rubric, validation.PreviousRubricSHA256 = Artifact{"review-rubric-v2.json", evalcorpus.Digest(data)}, s.Rubric.SHA256
	validation.ID = "independent-adjudication"
	f.submit(t, validation)
	report = gradingSnapshot(t, f)
	if report.Snapshot.Grading.Rubrics[0].AwaitingRegrade != 2 || len(report.Snapshot.Grading.Rubrics[0].OpenDisputes) != 0 || report.Snapshot.Attempts[index].Grade != "unknown" {
		t.Fatal("both sides must be regraded after rubric correction")
	}
	correction.ID, correction.Supersedes, correction.Rubric = "review-regrade", correction.ID, validation.Rubric
	f.submit(t, correction)
	peerUnit := slices.IndexFunc(f.progress.Slots, func(s Slot) bool { return s.CaseID == c.ID && s.Side == "candidate" }) + 1
	peer, _, _ := f.input(t, peerUnit, 1, "peer-explicit-unknown")
	peer.Rubric = validation.Rubric
	for i := range peer.Judgments {
		peer.Judgments[i].State, peer.Judgments[i].Evidence = "unknown", []string{}
	}
	peer.Review.Verdict, peer.Review.Evidence, peer.Review.Findings = "unknown", []string{}, []FindingJudgment{}
	f.submit(t, peer)
	report = gradingSnapshot(t, f)
	if report.Snapshot.Grading.Rubrics[0].AwaitingRegrade != 0 || report.Snapshot.Attempts[index].Grade != "pass" {
		t.Fatal("regrade/explicit unknown coverage")
	}
	dispute := correction
	dispute.ID, dispute.Operation, dispute.Judgments, dispute.Review, dispute.Supersedes, dispute.Target = "explicit-dispute", "dispute", nil, nil, "", correction.ID
	f.submit(t, dispute)
	if gradingSnapshot(t, f).Snapshot.Attempts[index].Grade != "unknown" {
		t.Fatal("explicit dispute did not invalidate affected grades")
	}
}

func TestGradingStrictBindingsAndFailurePreservation(t *testing.T) {
	if controllerTestProcess(t) {
		return
	}
	f := newGradingFixture(t, false, false)
	s, _, _ := f.input(t, 1, 1, "preserved-grade")
	f.submit(t, s)
	before := gradingSnapshot(t, f)
	file := filepath.Join(f.controller.inputs, "invalid-grade.json")
	for _, mutate := range []func(*GradeSubmission){
		func(s *GradeSubmission) { s.EvaluationID = "eval-" + strings.Repeat("a", 26) },
		func(s *GradeSubmission) { s.EvaluationSHA256 = strings.Repeat("0", 64) },
		func(s *GradeSubmission) { s.PlanDigest = "sha256:" + strings.Repeat("0", 64) },
		func(s *GradeSubmission) { s.CaseSHA256 = strings.Repeat("0", 64) },
		func(s *GradeSubmission) { s.PacketSHA256 = strings.Repeat("0", 64) },
		func(s *GradeSubmission) { s.AttemptSHA256 = strings.Repeat("0", 64) },
		func(s *GradeSubmission) { s.AttemptArtifacts[0].SHA256 = strings.Repeat("0", 64) },
		func(s *GradeSubmission) { s.AttemptArtifacts = s.AttemptArtifacts[1:] },
		func(s *GradeSubmission) { s.Judgments = s.Judgments[1:] },
		func(s *GradeSubmission) { s.Judgments[1].ID = s.Judgments[0].ID },
		func(s *GradeSubmission) { s.Judgments[0].ID = "unknown-requirement" },
		func(s *GradeSubmission) { s.Judgments[0].Evidence = []string{} },
		func(s *GradeSubmission) { s.Judgments[0].Evidence = []string{"missing"} },
		func(s *GradeSubmission) { s.Evaluator.Exposure = []string{} },
		func(s *GradeSubmission) { s.Evaluator.Identity = "" },
		func(s *GradeSubmission) {
			s.Evidence[1].Reproduction.Phase = "compiler"
			s.Evidence[1].Reproduction.Passed = []string{}
		},
		func(s *GradeSubmission) { s.Evidence[1].Reproduction.Phase = "setup" },
		func(s *GradeSubmission) { s.Rubric.Path = "../rubric.json" },
		func(s *GradeSubmission) { s.Rubric.SHA256 = strings.Repeat("0", 64) },
		func(s *GradeSubmission) { s.ID = "preserved-grade"; s.Reason = "conflicting replay" },
		func(s *GradeSubmission) { s.Supersedes = "nonexistent-grade" },
	} {
		var bad GradeSubmission
		if err := json.Unmarshal(fixtureJSON(t, s), &bad); err != nil {
			t.Fatal(err)
		}
		bad.ID, bad.Supersedes = "invalid-new-grade", s.ID
		mutate(&bad)
		writeFixture(t, file, fixtureJSON(t, bad))
		if _, err := SubmitGrade(f.controller.repo, f.controller.root, f.evaluation.ID, 1, 1, file); err == nil {
			t.Fatal("malformed/mismatched grading accepted")
		}
	}
	for _, bad := range []string{`{"schema_version":1,"schema_version":1}`, `{"schema_version":1,"unknown":true}`, `null`, string(bytes.Repeat([]byte("x"), MaxGradeBytes+1))} {
		writeFixture(t, file, []byte(bad))
		if _, err := SubmitGrade(f.controller.repo, f.controller.root, f.evaluation.ID, 1, 1, file); err == nil {
			t.Fatal("strict/bounded JSON accepted")
		}
	}
	if !reflect.DeepEqual(gradingSnapshot(t, f), before) {
		t.Fatal("failed publications changed retained grades")
	}
	source := filepath.Join(f.controller.inputs, s.Evidence[0].Artifact.Path)
	if err := os.Link(source, source+".alias"); err != nil {
		t.Fatal(err)
	}
	writeFixture(t, file, fixtureJSON(t, s))
	if _, err := SubmitGrade(f.controller.repo, f.controller.root, f.evaluation.ID, 1, 1, file); err == nil {
		t.Fatal("linked evidence accepted")
	}
	if err := os.Remove(source + ".alias"); err != nil {
		t.Fatal(err)
	}
	if err := Stop(f.controller.root, f.evaluation.ID); err != nil {
		t.Fatal(err)
	}
	// Tampered retained grade bytes fail closed in both inspection/publication.
	entry := before.Snapshot.Grading.History[0]
	stored := filepath.Join(f.controller.root, entry.Artifacts[2].Path)
	writeFixture(t, stored, []byte("tampered grade evidence"))
	if _, err := Inspect(f.controller.root, f.evaluation.ID); err == nil {
		t.Fatal("tampered retained grade inspected as valid")
	}
	if _, err := SubmitGrade(f.controller.repo, f.controller.root, f.evaluation.ID, 1, 1, file); err == nil {
		t.Fatal("tampered replay accepted")
	}
}

func TestGradingConcurrentReplayAndInterruptedPublication(t *testing.T) {
	if controllerTestProcess(t) {
		return
	}
	f := newGradingFixture(t, false, false)
	s, r, c := f.input(t, 1, 1, "concurrent-grade")
	f.submit(t, validationInput(t, f, s, r, c, "concurrent-validation"))
	file := filepath.Join(f.controller.inputs, s.ID+".json")
	writeFixture(t, file, fixtureJSON(t, s))
	var wg sync.WaitGroup
	results := make(chan error, 8)
	for range 8 {
		wg.Go(func() {
			_, err := SubmitGrade(f.controller.repo, f.controller.root, f.evaluation.ID, 1, 1, file)
			results <- err
		})
	}
	wg.Wait()
	close(results)
	successes := 0
	for err := range results {
		if err == nil {
			successes++
		} else if !strings.Contains(err.Error(), "publisher active or interrupted") {
			t.Fatal(err)
		}
	}
	if successes == 0 {
		t.Fatal("no concurrent publisher succeeded")
	}
	if _, err := SubmitGrade(f.controller.repo, f.controller.root, f.evaluation.ID, 1, 1, file); err != nil {
		t.Fatal("completed replay failed after bounded concurrent refusal", err)
	}
	report := gradingSnapshot(t, f)
	if len(report.Snapshot.Grading.History) != 2 || report.Snapshot.Attempts[0].Grade != "pass" {
		t.Fatal("concurrent identical replay appended or lost grades")
	}
	area := filepath.Join(f.controller.root, "grades", f.evaluation.ID)
	if err := os.Mkdir(filepath.Join(area, "publication"), 0o700); err != nil {
		t.Fatal(err)
	}
	if gradingSnapshot(t, f).Snapshot.Attempts[0].Grade != "unknown" {
		t.Fatal("unfinished publication promoted accepted evidence")
	}
	if _, err := SubmitGrade(f.controller.repo, f.controller.root, f.evaluation.ID, 1, 1, file); err != nil {
		t.Fatal("earlier completed replay inaccessible after interrupted claim")
	}
	newInput := s
	newInput.ID, newInput.Supersedes = "new-after-interruption", s.ID
	writeFixture(t, file, fixtureJSON(t, newInput))
	if _, err := SubmitGrade(f.controller.repo, f.controller.root, f.evaluation.ID, 1, 1, file); err == nil {
		t.Fatal("interrupted claim taken over")
	}
	if err := Stop(f.controller.root, f.evaluation.ID); err != nil {
		t.Fatal("grading consumed controller stop capacity", err)
	}
	if err := os.Remove(filepath.Join(area, "publication")); err != nil {
		t.Fatal(err)
	}
	orphan := filepath.Join(area, gradeBundleName(strings.Repeat("a", 64)))
	if err := os.Mkdir(orphan, 0o700); err != nil {
		t.Fatal(err)
	}
	writeFixture(t, filepath.Join(orphan, "partial.txt"), []byte("interrupted grading bytes"))
	if gradingSnapshot(t, f).Snapshot.Attempts[0].Grade != "unknown" {
		t.Fatal("orphan bundle treated as complete")
	}
	if _, err := SubmitGrade(f.controller.repo, f.controller.root, f.evaluation.ID, 1, 1, file); err == nil {
		t.Fatal("orphan publication silently replaced")
	}
}
