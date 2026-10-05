package evalplan

import (
	"errors"
	"fmt"
	"io/fs"
	"slices"
	"strings"
	"time"
)

type ReviewScore struct {
	Verdict             string         `json:"verdict"`
	RequiredBlockers    int            `json:"required_blockers"`
	CaughtBlockers      int            `json:"caught_blockers"`
	MissedBlockers      int            `json:"missed_blockers"`
	CaughtBySeverity    map[string]int `json:"caught_by_severity"`
	MissedBySeverity    map[string]int `json:"missed_by_severity"`
	FalseApproval       bool           `json:"false_approval"`
	UnsupportedBlockers int            `json:"unsupported_blockers"`
	CleanWronglyBlocked bool           `json:"clean_wrongly_blocked"`
	Advisories          int            `json:"advisories"`
	UnresolvedFindings  int            `json:"unresolved_findings"`
}

type PublicJudgment struct {
	ID       string         `json:"id"`
	State    string         `json:"state"`
	Evidence []DigestedFile `json:"evidence"`
}

type GradeResult struct {
	ID                    string           `json:"id"`
	Result                string           `json:"result"`
	AssessmentEligibility string           `json:"assessment_eligibility"`
	AcceptedExecution     bool             `json:"accepted_execution"`
	PreparationOnly       bool             `json:"preparation_only"`
	Retrospective         bool             `json:"retrospective"`
	AfterRepairFeedback   bool             `json:"after_recorded_repair_feedback"`
	RubricVersion         int              `json:"rubric_version"`
	Rubric                DigestedFile     `json:"rubric"`
	Evidence              DigestedFile     `json:"evidence"`
	Judgments             []PublicJudgment `json:"judgments"`
	Review                *ReviewScore     `json:"review,omitempty"`
}

type GradeHistory struct {
	ID            string         `json:"id"`
	Operation     string         `json:"operation"`
	Unit          Unit           `json:"unit"`
	Attempt       int            `json:"attempt"`
	SubmittedAt   string         `json:"submitted_at"`
	Evaluator     Evaluator      `json:"evaluator_assertions"`
	Evidence      DigestedFile   `json:"evidence"`
	Artifacts     []DigestedFile `json:"artifacts"`
	Supersedes    string         `json:"supersedes,omitempty"`
	Target        string         `json:"target,omitempty"`
	RubricVersion int            `json:"rubric_version"`
}

type RubricSummary struct {
	CaseID              string       `json:"case_id"`
	Version             int          `json:"version"`
	Evidence            DigestedFile `json:"evidence"`
	Validation          string       `json:"independent_validation_assertion"`
	Invalidated         bool         `json:"invalidated"`
	OpenDisputes        []string     `json:"open_disputes"`
	AwaitingRegrade     int          `json:"affected_attempts_awaiting_regrade_or_explicit_unknown"`
	ComparativeValidity string       `json:"comparative_validity"`
}

type UnitGrades struct {
	Unit           Unit   `json:"unit"`
	Initial        string `json:"initial"`
	Final          string `json:"final_permitted_attempt"`
	InitialAttempt int    `json:"initial_attempt"`
	FinalAttempt   int    `json:"final_attempt"`
}

type GradeCoverage struct {
	Role        string   `json:"role"`
	Partition   string   `json:"partition"`
	Side        string   `json:"side"`
	Stage       string   `json:"stage"`
	Scheduled   int      `json:"scheduled"`
	Eligible    int      `json:"eligible_for_semantic_assessment"`
	Invalid     int      `json:"invalid_execution"`
	Unrun       int      `json:"unrun"`
	Unknown     int      `json:"unknown"`
	Passed      int      `json:"supported_semantic_passes"`
	Failed      int      `json:"supported_semantic_failures"`
	Accepted    int      `json:"accepted_model_executions"`
	Preparation int      `json:"preparation_examples"`
	Lower       *float64 `json:"correctness_lower_bound,omitempty"`
	Upper       *float64 `json:"correctness_upper_bound,omitempty"`
	Status      string   `json:"status"`
}

type GradingSummary struct {
	SchemaVersion    int             `json:"schema_version"`
	JournalStatus    string          `json:"journal_status"`
	TrustLimits      []string        `json:"trust_limits"`
	History          []GradeHistory  `json:"history"`
	Rubrics          []RubricSummary `json:"rubrics"`
	Units            []UnitGrades    `json:"units"`
	Coverage         []GradeCoverage `json:"coverage"`
	TrialEligibility string          `json:"comparison_execution_eligibility"`
}

func reviewScore(s *GradeSubmission, r *Rubric) *ReviewScore {
	v := s.Review
	if v == nil {
		return nil
	}
	score := &ReviewScore{Verdict: v.Verdict, RequiredBlockers: len(r.Blockers), CaughtBySeverity: map[string]int{"critical": 0, "major": 0}, MissedBySeverity: map[string]int{"critical": 0, "major": 0}}
	// Deduplicate by defect identity/severity, never number of finding paragraphs.
	findings := map[string]FindingJudgment{}
	for _, f := range v.Findings {
		key := f.DefectID + "/" + f.Severity
		if old, ok := findings[key]; ok && old.State != f.State {
			f.State = "disputed"
		}
		findings[key] = f
	}
	for _, f := range findings {
		if f.State == "disputed" || f.State == "additional-real" || f.State == "unknown" && f.Severity != "advisory" {
			score.UnresolvedFindings++
			continue
		}
		if f.Severity == "advisory" {
			score.Advisories++
		} else if f.State == "unsupported" {
			score.UnsupportedBlockers++
		}
	}
	for _, b := range r.Blockers {
		if f, ok := findings[b.ID+"/"+b.Severity]; ok && f.State == "supported" {
			score.CaughtBlockers++
			score.CaughtBySeverity[b.Severity]++
		} else {
			score.MissedBlockers++
			score.MissedBySeverity[b.Severity]++
		}
	}
	score.FalseApproval = v.Verdict == "approve" && len(r.Blockers) != 0
	score.CleanWronglyBlocked = v.Verdict == "request-changes" && len(r.Blockers) == 0 && score.UnresolvedFindings == 0
	return score
}

func semanticResult(s *GradeSubmission, r *Rubric) (string, *ReviewScore) {
	result, score := judgmentResult(s.Judgments), reviewScore(s, r)
	if score == nil {
		return result, nil
	}
	if result == "unknown" || score.Verdict == "unknown" || score.UnresolvedFindings > 0 {
		return "unknown", score
	}
	if score.FalseApproval || score.MissedBlockers > 0 || score.UnsupportedBlockers > 0 || score.CleanWronglyBlocked {
		return "fail", score
	}
	return result, score
}

func assessmentEligibility(a ReportAttempt) string {
	if a.Outcome == "unknown" || a.FinishedAt == "" {
		return "unfinished"
	}
	if !slices.Contains([]string{"native-completed", "task-failure"}, a.Outcome) {
		return "invalid-execution"
	}
	if !slices.Contains([]string{"removed-clean", "preserved-dirty"}, a.Cleanup.Status) ||
		!slices.Contains([]string{"retained-artifact-integrity-only; no semantic grade"}, a.Verification) {
		return "unfinished"
	}
	return "eligible"
}

func gradeReference(id string, entry retainedGrade) DigestedFile {
	return DigestedFile{"grades/" + id + "/" + gradeRecordName(entry.receipt.Sequence), storedDigest(entry.receipt)}
}

func gradeArtifact(id string, entry retainedGrade, name, sha string) DigestedFile {
	return DigestedFile{"grades/" + id + "/" + gradeBundleName(entry.receipt.SubmissionSHA256) + "/" + name, sha}
}

func inspectGrading(g *guardedDir, e *Evaluation, hash string, p *Progress, s *Snapshot) error {
	area, err := openGradeArea(e.Preparation.Plan.StorageRoot, e.ID, false)
	if errors.Is(err, fs.ErrNotExist) {
		area, err = nil, nil
	}
	if err != nil {
		return err
	}
	if area != nil {
		defer area.close()
	}
	j, err := readGradeJournal(area, g, e, hash, p, false)
	if err != nil {
		return err
	}
	state, err := gradingState(j)
	if err != nil {
		return err
	}
	summary := &GradingSummary{SchemaVersion: 1, JournalStatus: "complete immutable journal; absent grades remain unknown",
		History: []GradeHistory{}, Rubrics: []RubricSummary{}, Units: []UnitGrades{}, Coverage: []GradeCoverage{},
		TrialEligibility: "unestablished; native production execution remains refused",
		TrustLimits: []string{
			"Evaluator identity, source access, exposure, independence and semantic correctness are attributed assertions, not authentication or machine proof.",
			"Independent validation requires source/key/rubric checks and exact control reproductions; agreement and keyword matching do not establish validity.",
			"All grades are retrospective. No initial grade is claimed to have existed before already-recorded repair feedback.",
			"no-model-test-script results are preparation examples, never evaluation model-trial performance or accepted production execution.",
			"Unknowns/disputes/invalid rubrics cannot increase acceptance; regrades retain every earlier record and guarded reason.",
		}}
	if j.pending {
		summary.JournalStatus = "publication active or interrupted; affected grades unknown, prior evidence preserved"
	}
	for _, entry := range j.entries {
		sub := entry.submission
		artifacts := []DigestedFile{}
		for _, f := range entry.receipt.Files {
			artifacts = append(artifacts, gradeArtifact(e.ID, entry, f.Path, f.SHA256))
		}
		summary.History = append(summary.History, GradeHistory{ID: sub.ID, Operation: sub.Operation, Unit: sub.Unit, Attempt: sub.Attempt,
			SubmittedAt: entry.receipt.SubmittedAt, Evaluator: sub.Evaluator, Evidence: gradeReference(e.ID, entry), Artifacts: artifacts,
			Supersedes: sub.Supersedes, Target: sub.Target, RubricVersion: entry.rubric.Version})
	}
	for i := range s.Attempts {
		a := &s.Attempts[i]
		entry, exists := state.latest[attemptName(a.Unit.Ordinal, a.Number)]
		if !exists {
			continue
		}
		sub := &entry.submission
		result, score := semanticResult(sub, &entry.rubric)
		current := state.rubrics[a.Unit.CaseID]
		if j.pending || current.sha != sub.Rubric.SHA256 || current.invalid || len(current.disputes) != 0 || current.validation == nil || judgmentResult(current.validation.submission.Validation.Checks) != "pass" {
			result = "unknown"
		}
		eligibility := assessmentEligibility(*a)
		if eligibility == "invalid-execution" {
			result = "invalid"
		} else if eligibility != "eligible" {
			result = "unknown"
		}
		grade := &GradeResult{ID: sub.ID, Result: result, AssessmentEligibility: eligibility,
			PreparationOnly: a.ExecutionSource == "no-model-test-script", Retrospective: true, RubricVersion: entry.rubric.Version,
			Rubric: gradeArtifact(e.ID, entry, "rubric.json", sub.Rubric.SHA256), Evidence: gradeReference(e.ID, entry), Judgments: []PublicJudgment{}, Review: score}
		// No production model execution source is currently permitted. Even a
		// supported semantic pass cannot promote scripted/refused evidence.
		for _, repair := range s.Attempts {
			if repair.Unit == a.Unit && repair.Kind == "repair" {
				started, parseErr := time.Parse(time.RFC3339Nano, repair.StartedAt)
				submitted, _ := time.Parse(time.RFC3339Nano, entry.receipt.SubmittedAt)
				grade.AfterRepairFeedback = grade.AfterRepairFeedback || parseErr == nil && !submitted.Before(started)
			}
		}
		for _, judgment := range sub.Judgments {
			evidence := []DigestedFile{}
			for _, id := range judgment.Evidence {
				item := sub.Evidence[slices.IndexFunc(sub.Evidence, func(item GradeEvidence) bool { return item.ID == id })]
				evidence = append(evidence, gradeArtifact(e.ID, entry, "evidence-"+id, item.Artifact.SHA256))
			}
			grade.Judgments = append(grade.Judgments, PublicJudgment{judgment.ID, judgment.State, evidence})
		}
		a.Grade, a.Grading = result, grade
	}
	for _, caseID := range sortedKeys(state.rubrics) {
		current := state.rubrics[caseID]
		validation := "unknown; no independent validation assertion"
		var reference DigestedFile
		if current.validation != nil {
			validation = judgmentResult(current.validation.submission.Validation.Checks) + "; attributed independent validation, not authenticated proof"
			reference = gradeArtifact(e.ID, *current.validation, "rubric.json", current.sha)
		} else {
			for _, entry := range j.entries {
				if entry.submission.Rubric.SHA256 == current.sha {
					reference = gradeArtifact(e.ID, entry, "rubric.json", current.sha)
					break
				}
			}
		}
		awaiting := 0
		for _, slot := range p.Slots {
			if slot.CaseID != caseID {
				continue
			}
			for _, attempt := range slot.Attempts {
				entry, exists := state.latest[attemptName(slot.Ordinal, attempt.Number)]
				if attempt.SHA256 != "" && (!exists || entry.submission.Rubric.SHA256 != current.sha) {
					awaiting++
				}
			}
		}
		validity := "unknown; valid undisputed validation or affected regrades remain unestablished"
		if !j.pending && !current.invalid && len(current.disputes) == 0 && current.validation != nil && judgmentResult(current.validation.submission.Validation.Checks) == "pass" && awaiting == 0 {
			validity = "all affected retained attempts regraded or explicitly unknown; native/measurement comparison eligibility still unestablished"
		}
		summary.Rubrics = append(summary.Rubrics, RubricSummary{caseID, current.rubric.Version, reference, validation, current.invalid, sortedKeys(current.disputes), awaiting, validity})
	}
	gradeCoverage(s, summary)
	s.Grading = summary
	return nil
}

func gradeCoverage(s *Snapshot, summary *GradingSummary) {
	sides := []string{"baseline"}
	if s.Scope.Preparation.Plan.Candidate != nil {
		sides = append(sides, "candidate")
	}
	rows := map[string]*GradeCoverage{}
	for _, role := range []string{"scout", "implementation", "review"} {
		for _, partition := range []string{"development", "held-out"} {
			for _, side := range sides {
				for _, stage := range []string{"initial", "final"} {
					row := &GradeCoverage{Role: role, Partition: partition, Side: side, Stage: stage}
					rows[strings.Join([]string{role, partition, side, stage}, "/")] = row
				}
			}
		}
	}
	for _, slot := range s.Progress.Slots {
		c := s.Scope.Preparation.Plan.Cases[slices.IndexFunc(s.Scope.Preparation.Plan.Cases, func(c PublicCase) bool { return c.ID == slot.CaseID })]
		unit := UnitGrades{Unit: slot.Unit, Initial: "unknown", Final: "unknown"}
		for _, stage := range []string{"initial", "final"} {
			row := rows[strings.Join([]string{c.Role, c.Partition, slot.Side, stage}, "/")]
			row.Scheduled++
			number := 1
			if stage == "final" {
				number = len(slot.Attempts)
			}
			index := slices.IndexFunc(s.Attempts, func(a ReportAttempt) bool { return a.Unit == slot.Unit && a.Number == number })
			result := "unknown"
			if len(slot.Attempts) == 0 {
				row.Unrun++
			} else if index >= 0 {
				a := s.Attempts[index]
				eligible := assessmentEligibility(a)
				switch eligible {
				case "invalid-execution":
					row.Invalid++
					result = "invalid"
				case "eligible":
					row.Eligible++
					result = a.Grade
				}
				if a.ExecutionSource == "no-model-test-script" {
					row.Preparation++
				}
			}
			if stage == "initial" {
				unit.Initial, unit.InitialAttempt = result, min(1, len(slot.Attempts))
			} else {
				unit.Final, unit.FinalAttempt = result, len(slot.Attempts)
			}
			switch result {
			case "pass":
				row.Passed++
			case "fail":
				row.Failed++
			case "unknown":
				row.Unknown++
			}
		}
		summary.Units = append(summary.Units, unit)
	}
	for _, key := range sortedKeys(rows) {
		row := rows[key]
		row.Status = "not-applicable; zero scheduled opportunities"
		if row.Scheduled != 0 {
			lower, upper := float64(row.Passed)/float64(row.Scheduled), float64(row.Passed+row.Unknown)/float64(row.Scheduled)
			row.Lower, row.Upper = &lower, &upper
			row.Status = "bounds on attributed semantic assessments; preparation is not model-trial performance"
		}
		summary.Coverage = append(summary.Coverage, *row)
	}
}

func gradingHeading(s *Snapshot) string {
	if s.Grading == nil { // preserve historical schema-1 report rendering
		return "grades unknown; decision inconclusive"
	}
	return fmt.Sprintf("%d grading records; decision inconclusive", len(s.Grading.History))
}
