package evalplan

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/kninetimmy/orch/internal/evalcorpus"
)

func TestGradingPinnedDefinitions(t *testing.T) {
	data, err := os.ReadFile("../../evaluation/reference-v1/manifest.json")
	if err != nil {
		t.Fatal(err)
	}
	m, err := evalcorpus.Load(data)
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range m.Cases {
		t.Run(c.ID, func(t *testing.T) {
			dataRubric, err := os.ReadFile("../../evaluation/grading-v1/" + c.ID + ".json")
			if err != nil {
				t.Fatal(err)
			}
			var r Rubric
			if err := strictStored(dataRubric, &r); err != nil {
				t.Fatal(err)
			}
			if err := validateRubric(&r, c, evalcorpus.Digest(data)); err != nil {
				t.Fatal(err)
			}
			key, err := os.ReadFile("../../evaluation/reference-v1/" + c.Key.Source)
			if err != nil || evalcorpus.Digest(key) != c.Key.SHA256 {
				t.Fatalf("pinned key: %v", err)
			}
			count := 0
			for _, line := range strings.Split(string(key), "\n") {
				cols := strings.Split(line, "|")
				if len(cols) != 6 || !strings.Contains(cols[1], " / ") {
					continue
				}
				id := strings.TrimSpace(strings.SplitN(cols[1], " / ", 2)[1])
				index := slices.IndexFunc(r.Requirements, func(req Requirement) bool { return req.ID == id })
				if index < 0 || r.Requirements[index].Expectation != strings.TrimSpace(cols[2]) || r.Requirements[index].Severity != strings.TrimSpace(cols[4]) {
					t.Fatalf("key distinction dropped/changed: %s", id)
				}
				count++
			}
			if c.Role == "implementation" {
				count++
			}
			if count != len(r.Requirements) {
				t.Fatal("unmapped rubric requirement")
			}
			for _, mutate := range []func(*Rubric){
				func(r *Rubric) { r.CaseSHA256 = strings.Repeat("0", 64) },
				func(r *Rubric) { r.PacketSHA256 = strings.Repeat("0", 64) },
				func(r *Rubric) { r.KeySHA256 = strings.Repeat("0", 64) },
				func(r *Rubric) { r.Controls[0].SHA256 = strings.Repeat("0", 64) },
			} {
				var invalid Rubric
				if err := json.Unmarshal(dataRubric, &invalid); err != nil {
					t.Fatal(err)
				}
				mutate(&invalid)
				if err := validateRubric(&invalid, c, evalcorpus.Digest(data)); err == nil {
					t.Fatal("tampered rubric accepted")
				}
			}
		})
	}
	t.Log("Author mapping check only: exact frozen key distinctions/digests. Fresh independent semantic/source/control review remains pending.")
}

func TestGradingSemanticAndReviewOutcomes(t *testing.T) {
	r := Rubric{Requirements: []Requirement{{ID: "fact"}}, Blockers: []KeyedDefect{{ID: "DEFECT", Severity: "major"}}}
	base := GradeSubmission{Judgments: []Judgment{{ID: "fact", State: "satisfied"}}, Review: &ReviewJudgment{Verdict: "request-changes", Findings: []FindingJudgment{{ID: "one", DefectID: "DEFECT", Severity: "major", State: "supported"}}}}
	tests := []struct {
		name                                    string
		change                                  func(*GradeSubmission, *Rubric)
		want                                    string
		caught, missed, unsupported, unresolved int
		falseApproval, clean                    bool
	}{
		{"good", func(*GradeSubmission, *Rubric) {}, "pass", 1, 0, 0, 0, false, false},
		{"duplicate-blocker", func(s *GradeSubmission, _ *Rubric) {
			f := s.Review.Findings[0]
			f.ID = "two"
			s.Review.Findings = append(s.Review.Findings, f)
		}, "pass", 1, 0, 0, 0, false, false},
		{"missed-blocker", func(s *GradeSubmission, _ *Rubric) { s.Review.Findings = []FindingJudgment{} }, "fail", 0, 1, 0, 0, false, false},
		{"false-approval", func(s *GradeSubmission, _ *Rubric) { s.Review.Verdict = "approve" }, "fail", 1, 0, 0, 0, true, false},
		{"unsupported", func(s *GradeSubmission, _ *Rubric) {
			s.Review.Findings = append(s.Review.Findings, FindingJudgment{ID: "extra", DefectID: "NOT-REAL", Severity: "major", State: "unsupported"})
		}, "fail", 1, 0, 1, 0, false, false},
		{"additional-real", func(s *GradeSubmission, _ *Rubric) {
			s.Review.Findings = append(s.Review.Findings, FindingJudgment{ID: "extra", DefectID: "UNKEYED", Severity: "critical", State: "additional-real"})
		}, "unknown", 1, 0, 0, 1, false, false},
		{"disputed-match", func(s *GradeSubmission, _ *Rubric) { s.Review.Findings[0].State = "disputed" }, "unknown", 0, 1, 0, 1, false, false},
		{"conflicting-duplicates", func(s *GradeSubmission, _ *Rubric) {
			f := s.Review.Findings[0]
			f.ID, f.State = "two", "unsupported"
			s.Review.Findings = append(s.Review.Findings, f)
		}, "unknown", 0, 1, 0, 1, false, false},
		{"advisory-misses-blocker", func(s *GradeSubmission, _ *Rubric) { s.Review.Findings[0].Severity = "advisory" }, "fail", 0, 1, 0, 0, false, false},
		{"clean-approval", func(s *GradeSubmission, r *Rubric) {
			r.Blockers = []KeyedDefect{}
			s.Review.Verdict = "approve"
			s.Review.Findings = []FindingJudgment{}
		}, "pass", 0, 0, 0, 0, false, false},
		{"clean-wrongly-blocked", func(s *GradeSubmission, r *Rubric) {
			r.Blockers = []KeyedDefect{}
			s.Review.Findings = []FindingJudgment{}
		}, "fail", 0, 0, 0, 0, false, true},
		{"unknown-conclusion", func(s *GradeSubmission, _ *Rubric) { s.Judgments[0].State = "unknown" }, "unknown", 1, 0, 0, 0, false, false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var s GradeSubmission
			if err := json.Unmarshal(fixtureJSON(t, base), &s); err != nil {
				t.Fatal(err)
			}
			copyRubric := r
			test.change(&s, &copyRubric)
			got, score := semanticResult(&s, &copyRubric)
			if got != test.want || score.CaughtBlockers != test.caught || score.MissedBlockers != test.missed || score.UnsupportedBlockers != test.unsupported || score.UnresolvedFindings != test.unresolved || score.FalseApproval != test.falseApproval || score.CleanWronglyBlocked != test.clean {
				t.Fatalf("result=%s score=%+v", got, score)
			}
		})
	}
	for _, state := range []string{"satisfied", "violated", "unknown", "disputed"} {
		got, score := semanticResult(&GradeSubmission{Judgments: []Judgment{{State: state}}}, &Rubric{})
		want := map[string]string{"satisfied": "pass", "violated": "fail", "unknown": "unknown", "disputed": "unknown"}[state]
		if got != want || score != nil {
			t.Fatal("scout/implementation judgment derivation")
		}
	}
	critical := base
	critical.Review = &ReviewJudgment{Verdict: "request-changes", Findings: []FindingJudgment{}}
	result, score := semanticResult(&critical, &Rubric{Blockers: []KeyedDefect{{ID: "DEFECT", Severity: "critical"}}})
	if result != "fail" || score.MissedBySeverity["critical"] != 1 {
		t.Fatal("critical missed blocker lost severity")
	}
}

func TestGradingLegacyReportRendering(t *testing.T) {
	legacy := &Report{SchemaVersion: 1, SnapshotSHA256: "legacy-digest", Destination: "legacy-destination", Snapshot: Snapshot{SchemaVersion: 1, EvaluationID: "legacy", Progress: Progress{State: "refused"}}}
	data, err := storedBytes(legacy)
	if err != nil || bytes.Contains(data, []byte(`"grading"`)) {
		t.Fatalf("legacy optional wire shape: %v", err)
	}
	expected := map[string]string{
		"json":     string(data),
		"text":     fmt.Sprintf("Evaluation legacy: refused\nEvidence complete: false; grades unknown; decision inconclusive.\nSnapshot: legacy-digest\nReport destination (publication verified separately): legacy-destination\n\n%s", data),
		"markdown": fmt.Sprintf("# Evaluation legacy\n\nState: **refused**. Evidence complete: **false**. Grades unknown; decision inconclusive.\n\nSnapshot: `legacy-digest`\n\nReport destination (publication verified separately): `legacy-destination`\n\n```json\n%s```\n", data),
	}
	for format, want := range expected {
		var out bytes.Buffer
		if err := RenderReport(&out, legacy, format); err != nil || out.String() != want {
			t.Fatalf("legacy %s bytes changed: %v", format, err)
		}
		var decoded Report
		if err := json.Unmarshal(data, &decoded); err != nil || !reflect.DeepEqual(decoded, *legacy) {
			t.Fatalf("schema-1 decode: %v", err)
		}
	}
}

func TestGradingStateCorrectionsDisputesAndVersions(t *testing.T) {
	unit := Unit{Ordinal: 1, CaseID: "case", CaseVersion: 1, Repetition: 1, Side: "baseline"}
	r := Rubric{Version: 1, Author: Evaluator{Identity: "author"}}
	s := GradeSubmission{ID: "initial", Operation: "grade", Unit: unit, Attempt: 1, Rubric: Artifact{SHA256: strings.Repeat("a", 64)}, Evaluator: Evaluator{Identity: "evaluator"}, Judgments: []Judgment{{ID: "fact", State: "satisfied"}}}
	j := &gradeJournal{entries: []retainedGrade{{submission: s, rubric: r}}}
	appendEntry := func(input GradeSubmission, rubric Rubric) {
		j.entries = append(j.entries, retainedGrade{submission: input, rubric: rubric})
	}
	bad := s
	bad.ID, bad.Supersedes = "wrong-correction", "absent"
	if _, err := gradingState(&gradeJournal{entries: append(slices.Clone(j.entries), retainedGrade{submission: bad, rubric: r})}); err == nil {
		t.Fatal("mismatched correction accepted")
	}
	corrected := s
	corrected.ID, corrected.Supersedes = "corrected", s.ID
	appendEntry(corrected, r)
	dispute := s
	dispute.ID, dispute.Operation, dispute.Target, dispute.Evaluator.Identity = "dispute", "dispute", corrected.ID, "disputer"
	appendEntry(dispute, r)
	state, err := gradingState(j)
	if err != nil || len(state.rubrics[unit.CaseID].disputes) != 1 || state.latest[gradeAttemptKey(&s)].submission.ID != corrected.ID {
		t.Fatalf("retained correction/dispute: %v", err)
	}
	validation := s
	validation.ID, validation.Operation = "adjudication", "validate-rubric"
	validation.Evaluator = Evaluator{Identity: "validator", IndependentOf: []string{"author", "evaluator", "disputer"}}
	validation.Validation = &RubricValidation{Checks: []Judgment{{State: "satisfied"}}, Resolves: []string{dispute.ID}}
	if _, err := gradingState(&gradeJournal{entries: append(slices.Clone(j.entries), retainedGrade{submission: validation, rubric: r})}); err == nil {
		t.Fatal("same-version adjudication accepted")
	}
	r.Version++
	validation.PreviousRubricSHA256, validation.Rubric.SHA256 = s.Rubric.SHA256, strings.Repeat("b", 64)
	self := validation
	self.Evaluator.Identity = "evaluator"
	if _, err := gradingState(&gradeJournal{entries: append(slices.Clone(j.entries), retainedGrade{submission: self, rubric: r})}); err == nil {
		t.Fatal("self adjudication accepted")
	}
	appendEntry(validation, r)
	state, err = gradingState(j)
	if err != nil || len(state.rubrics[unit.CaseID].disputes) != 0 || state.rubrics[unit.CaseID].rubric.Version != 2 || state.latest[gradeAttemptKey(&s)].submission.Rubric.SHA256 == validation.Rubric.SHA256 {
		t.Fatalf("version correction erased old grade or restored regrade coverage: %v", err)
	}
	regrade := corrected
	regrade.ID, regrade.Supersedes, regrade.Rubric = "regrade", corrected.ID, validation.Rubric
	appendEntry(regrade, r)
	invalid := regrade
	invalid.ID, invalid.Operation = "invalidate", "invalidate-rubric"
	appendEntry(invalid, r)
	validation.ID, validation.PreviousRubricSHA256, validation.Validation.Resolves = "same-version-revalidate", "", []string{}
	appendEntry(validation, r)
	state, err = gradingState(j)
	if err != nil || !state.rubrics[unit.CaseID].invalid || len(state.byID) != 7 {
		t.Fatalf("invalidation/history was lost: %v", err)
	}
}

func TestGradingBoundedInputAndCoverage(t *testing.T) {
	manifestBytes, err := os.ReadFile("../../evaluation/reference-v1/manifest.json")
	if err != nil {
		t.Fatal(err)
	}
	m, err := evalcorpus.Load(manifestBytes)
	if err != nil {
		t.Fatal(err)
	}
	c := m.Cases[slices.IndexFunc(m.Cases, func(c evalcorpus.Case) bool { return c.ID == "implement-dev-ci-empty" })]
	rubricBytes, err := os.ReadFile("../../evaluation/grading-v1/" + c.ID + ".json")
	if err != nil {
		t.Fatal(err)
	}
	var rubric Rubric
	if err := strictStored(rubricBytes, &rubric); err != nil {
		t.Fatal(err)
	}
	unit := Unit{Ordinal: 1, CaseID: c.ID, CaseVersion: c.Version, Repetition: 1, Side: "baseline"}
	e := Evaluation{ID: "eval-" + strings.Repeat("a", 26), Preparation: Record{PlanDigest: "sha256:" + strings.Repeat("b", 64), Plan: Plan{Corpus: Artifact{SHA256: evalcorpus.Digest(manifestBytes)}}}}
	a := AttemptRecord{EvaluationID: e.ID, PlanDigest: e.Preparation.PlanDigest, Unit: unit, Number: 1, Kind: "initial", CaseSHA256: storedDigest(c), PacketSHA256: c.PacketSHA256, Initial: []DigestedFile{}, Artifacts: []DigestedFile{}}
	hash, ref := storedDigest(e), AttemptRef{Number: 1, Kind: "initial", SHA256: storedDigest(a)}
	s := GradeSubmission{SchemaVersion: 1, ID: "typed-check", Operation: "grade", EvaluationID: e.ID, EvaluationSHA256: hash, PlanDigest: e.Preparation.PlanDigest, Unit: unit, Attempt: 1,
		AttemptSHA256: ref.SHA256, CaseSHA256: a.CaseSHA256, PacketSHA256: a.PacketSHA256, AttemptArtifacts: attemptArtifacts(&a), Rubric: Artifact{Path: "rubric.json", SHA256: evalcorpus.Digest(rubricBytes)},
		Evaluator: Evaluator{Identity: "synthetic-evaluator", Kind: "human", Relationship: "evaluator", IndependentOf: []string{}, SourceAccess: []string{"pinned-source"}, Exposure: []string{"synthetic-only"}}, Reason: "Typed preparation check",
		Evidence: []GradeEvidence{
			{ID: "source", Artifact: Artifact{Path: "source.txt", SHA256: strings.Repeat("c", 64)}, Route: "source", Citation: "Scoped source trace"},
			{ID: "behavior", Artifact: Artifact{Path: "behavior.txt", SHA256: strings.Repeat("d", 64)}, Route: "reproduction", Citation: "Alternative behavioral route", Reproduction: &Reproduction{Command: []string{"never-executed"}, Phase: "behavior", Passed: []string{"Equivalent/check"}, Failed: []string{}}},
		}, Judgments: []Judgment{}}
	for _, req := range rubric.Requirements {
		id := "behavior"
		if req.Kind == "scope" {
			id = "source"
		}
		s.Judgments = append(s.Judgments, Judgment{ID: req.ID, State: "satisfied", Evidence: []string{id}, Reason: "Supported independent wording/algorithm"})
	}
	if err := validateGradeInput(&s, &e, hash, &a, ref, c, &rubric); err != nil {
		t.Fatal(err)
	}
	for _, mutate := range []func(*GradeSubmission){
		func(s *GradeSubmission) { s.EvaluationSHA256 = strings.Repeat("0", 64) },
		func(s *GradeSubmission) { s.AttemptSHA256 = strings.Repeat("0", 64) },
		func(s *GradeSubmission) { s.CaseSHA256 = strings.Repeat("0", 64) },
		func(s *GradeSubmission) { s.PacketSHA256 = strings.Repeat("0", 64) },
		func(s *GradeSubmission) { s.AttemptArtifacts = []DigestedFile{} },
		func(s *GradeSubmission) { s.Judgments = s.Judgments[1:] },
		func(s *GradeSubmission) { s.Judgments[1].ID = s.Judgments[0].ID },
		func(s *GradeSubmission) { s.Judgments[0].ID = "unknown" },
		func(s *GradeSubmission) { s.Judgments[0].Evidence = []string{} },
		func(s *GradeSubmission) {
			s.Evidence[1].Reproduction.Phase = "compiler"
			s.Evidence[1].Reproduction.Passed = []string{}
		},
		func(s *GradeSubmission) { s.Evidence[1].Reproduction.Phase = "setup" },
		func(s *GradeSubmission) { s.Rubric.Path = "../unsafe.json" },
		func(s *GradeSubmission) { s.Evaluator.Exposure = []string{} },
	} {
		var bad GradeSubmission
		if err := json.Unmarshal(fixtureJSON(t, s), &bad); err != nil {
			t.Fatal(err)
		}
		mutate(&bad)
		if err := validateGradeInput(&bad, &e, hash, &a, ref, c, &rubric); err == nil {
			t.Fatal("malformed binding/judgment/evidence accepted")
		}
	}
	for _, malformed := range []string{`{"schema_version":1,"schema_version":1}`, `{"schema_version":1,"unknown":true}`, `null`, `{"schema_version":1} {}`} {
		var bad GradeSubmission
		if err := strictJSON([]byte(malformed), &bad); err == nil {
			t.Fatal("strict JSON accepted malformed submission")
		}
	}
	unrun := Unit{Ordinal: 2, CaseID: "unrun", CaseVersion: 1, Repetition: 1, Side: "baseline"}
	snapshot := Snapshot{Scope: ApprovalScope{Preparation: Record{Plan: Plan{Cases: []PublicCase{{ID: c.ID, Role: c.Role, Partition: c.Partition}, {ID: "unrun", Role: "scout", Partition: "held-out"}}}}},
		Progress: Progress{Slots: []Slot{{Unit: unit, Status: "native-completed", Attempts: []AttemptRef{{Number: 1, Kind: "initial"}, {Number: 2, Kind: "repair"}}}, {Unit: unrun, Status: "unrun"}}},
		Attempts: []ReportAttempt{
			{Unit: unit, Number: 1, Grade: "fail", Outcome: "task-failure", FinishedAt: "recorded", ExecutionSource: "no-model-test-script", Verification: "retained-artifact-integrity-only; no semantic grade", Cleanup: Cleanup{Status: "removed-clean"}},
			{Unit: unit, Number: 2, Grade: "pass", Outcome: "native-completed", FinishedAt: "recorded", ExecutionSource: "no-model-test-script", Verification: "retained-artifact-integrity-only; no semantic grade", Cleanup: Cleanup{Status: "removed-clean"}},
		}}
	summary := &GradingSummary{Units: []UnitGrades{}, Coverage: []GradeCoverage{}}
	gradeCoverage(&snapshot, summary)
	if summary.Units[0].Initial != "fail" || summary.Units[0].Final != "pass" || summary.Units[1].Final != "unknown" {
		t.Fatal("initial/final/unknown coverage collapsed")
	}
	for _, row := range summary.Coverage {
		if row.Accepted != 0 {
			t.Fatal("preparation promoted to model acceptance")
		}
		if row.Scheduled == 0 && (row.Lower != nil || row.Upper != nil || !strings.HasPrefix(row.Status, "not-applicable")) {
			t.Fatal("zero opportunities were measured")
		}
		if row.Role == "scout" && row.Partition == "held-out" && (row.Unrun != 1 || row.Unknown != 1 || *row.Lower != 0 || *row.Upper != 1) {
			t.Fatal("unrun correctness bounds")
		}
	}
}
