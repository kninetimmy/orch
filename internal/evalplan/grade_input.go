package evalplan

import (
	"fmt"
	"reflect"
	"slices"
	"strings"

	"github.com/kninetimmy/orch/internal/evalcorpus"
)

const (
	MaxGradeBytes       = 64 * 1024
	maxGradeRecords     = 2048
	maxGradeBundleBytes = 16 * 1024 * 1024
)

type Reproduction struct {
	Command []string `json:"command"`
	Phase   string   `json:"phase"`
	Passed  []string `json:"passed"`
	Failed  []string `json:"failed"`
}

type GradeEvidence struct {
	ID           string        `json:"id"`
	Artifact     Artifact      `json:"artifact"`
	Route        string        `json:"route"`
	Citation     string        `json:"citation"`
	Reproduction *Reproduction `json:"reproduction,omitempty"`
}

type Judgment struct {
	ID       string   `json:"id"`
	State    string   `json:"state"`
	Evidence []string `json:"evidence"`
	Reason   string   `json:"reason"`
}

type FindingJudgment struct {
	ID       string   `json:"id"`
	DefectID string   `json:"defect_id"`
	Severity string   `json:"severity"`
	State    string   `json:"state"`
	Evidence []string `json:"evidence"`
	Reason   string   `json:"reason"`
}

type ReviewJudgment struct {
	Verdict  string            `json:"verdict"`
	Evidence []string          `json:"evidence"`
	Reason   string            `json:"reason"`
	Findings []FindingJudgment `json:"findings"`
}

type RubricValidation struct {
	Checks   []Judgment `json:"checks"`
	Resolves []string   `json:"resolves"`
}

// GradeSubmission is data only. Private evaluator prose, reproduction commands
// and worker bytes never enter report summaries or an execution API.
type GradeSubmission struct {
	SchemaVersion        int               `json:"schema_version"`
	ID                   string            `json:"id"`
	Operation            string            `json:"operation"`
	EvaluationID         string            `json:"evaluation_id"`
	EvaluationSHA256     string            `json:"evaluation_sha256"`
	PlanDigest           string            `json:"plan_digest"`
	Unit                 Unit              `json:"unit"`
	Attempt              int               `json:"attempt"`
	AttemptSHA256        string            `json:"attempt_sha256"`
	CaseSHA256           string            `json:"case_sha256"`
	PacketSHA256         string            `json:"packet_sha256"`
	AttemptArtifacts     []DigestedFile    `json:"attempt_artifacts"`
	Rubric               Artifact          `json:"rubric"`
	PreviousRubricSHA256 string            `json:"previous_rubric_sha256,omitempty"`
	Evaluator            Evaluator         `json:"evaluator"`
	Reason               string            `json:"reason"`
	Evidence             []GradeEvidence   `json:"evidence"`
	Judgments            []Judgment        `json:"judgments,omitempty"`
	Review               *ReviewJudgment   `json:"review,omitempty"`
	Validation           *RubricValidation `json:"validation,omitempty"`
	Supersedes           string            `json:"supersedes,omitempty"`
	Target               string            `json:"target,omitempty"`
}

func attemptArtifacts(a *AttemptRecord) []DigestedFile {
	files := append(slices.Clone(a.Initial), a.Artifacts...)
	files = append(files, DigestedFile{"case.json", a.CaseSHA256})
	for _, f := range []*DigestedFile{a.Output, a.InvalidNative} {
		if f != nil {
			files = append(files, *f)
		}
	}
	slices.SortFunc(files, func(a, b DigestedFile) int { return strings.Compare(a.Path, b.Path) })
	return files
}

func evidenceMap(s *GradeSubmission) (map[string]GradeEvidence, error) {
	if s.Evidence == nil || len(s.Evidence) > 32 {
		return nil, fmt.Errorf("require explicit evidence array of at most 32 artifacts")
	}
	items := map[string]GradeEvidence{}
	for _, e := range s.Evidence {
		if !casePattern.MatchString(e.ID) || items[e.ID].ID != "" || !digestPattern.MatchString(e.Artifact.SHA256) ||
			!slices.Contains([]string{"source", "reproduction", "scope"}, e.Route) || !boundedProse(e.Citation) {
			return nil, fmt.Errorf("invalid or duplicate grade evidence")
		}
		if _, err := localPath("", e.Artifact.Path, false); err != nil {
			return nil, err
		}
		if e.Route == "reproduction" {
			p := e.Reproduction
			if p == nil || len(p.Command) == 0 || len(p.Command) > 32 ||
				!slices.Contains([]string{"setup", "compiler", "behavior"}, p.Phase) || !uniqueNames(p.Passed, 128) || !uniqueNames(p.Failed, 128) {
				return nil, fmt.Errorf("reproduction requires bounded command, phase and observed test identities")
			}
			for _, arg := range p.Command {
				if !boundedProse(arg) {
					return nil, fmt.Errorf("reproduction command arguments must be bounded data")
				}
			}
			for _, test := range p.Failed {
				if slices.Contains(p.Passed, test) {
					return nil, fmt.Errorf("reproduction test cannot both pass and fail")
				}
			}
			if p.Phase == "behavior" && len(p.Passed)+len(p.Failed) == 0 || p.Phase != "behavior" && len(p.Passed)+len(p.Failed) != 0 {
				return nil, fmt.Errorf("setup/compiler failure is not an intended behavioral failure")
			}
		} else if e.Reproduction != nil {
			return nil, fmt.Errorf("reproduction details on non-reproduction evidence")
		}
		items[e.ID] = e
	}
	return items, nil
}

func support(ids []string, items map[string]GradeEvidence, routes []string, state string) error {
	if !uniqueNames(ids, 32) {
		return fmt.Errorf("invalid or duplicate evidence references")
	}
	usable := false
	for _, id := range ids {
		e, ok := items[id]
		if !ok {
			return fmt.Errorf("unknown evidence reference %s", id)
		}
		if !slices.Contains(routes, e.Route) {
			continue
		}
		if e.Route != "reproduction" {
			usable = true
		} else if p := e.Reproduction; p.Phase == "behavior" {
			// Alternative test names/algorithms are welcome. We check that a
			// behavioral check really is asserted, never reference-code shape.
			usable = usable || state == "satisfied" && len(p.Passed) > 0 && len(p.Failed) == 0 ||
				state == "behavior-violation" && len(p.Failed) > 0 ||
				state != "satisfied" && state != "behavior-violation" && len(p.Passed)+len(p.Failed) > 0
		}
	}
	if !slices.Contains([]string{"unknown", "disputed"}, state) && !usable {
		return fmt.Errorf("conclusive judgment requires supporting source/behavioral reproduction evidence")
	}
	return nil
}

func validateJudgments(judgments []Judgment, requirements []Requirement, items map[string]GradeEvidence) error {
	if len(judgments) != len(requirements) {
		return fmt.Errorf("require exactly one judgment for every required distinction")
	}
	seen := map[string]bool{}
	for _, j := range judgments {
		index := slices.IndexFunc(requirements, func(r Requirement) bool { return r.ID == j.ID })
		if index < 0 || seen[j.ID] || !slices.Contains([]string{"satisfied", "violated", "unknown", "disputed"}, j.State) || !boundedProse(j.Reason) {
			return fmt.Errorf("unknown, duplicate or invalid required judgment %q", j.ID)
		}
		state := j.State
		if requirements[index].Kind == "control" && state == "satisfied" {
			state = "observed-control-outcome"
		} else if slices.Contains([]string{"behavior", "regression"}, requirements[index].Kind) && state == "violated" {
			state = "behavior-violation"
		}
		if err := support(j.Evidence, items, requirements[index].EvidenceRoutes, state); err != nil {
			return fmt.Errorf("judgment %s: %w", j.ID, err)
		}
		seen[j.ID] = true
	}
	return nil
}

func validateReview(review *ReviewJudgment, r *Rubric, items map[string]GradeEvidence) error {
	if review == nil || !slices.Contains([]string{"approve", "request-changes", "unknown"}, review.Verdict) ||
		!boundedProse(review.Reason) || review.Findings == nil || len(review.Findings) > 64 {
		return fmt.Errorf("review requires an explicit verdict and bounded finding judgments")
	}
	if err := support(review.Evidence, items, []string{"source", "reproduction"}, review.Verdict); err != nil {
		return err
	}
	seen := map[string]bool{}
	for _, f := range review.Findings {
		if !casePattern.MatchString(f.ID) || seen[f.ID] || !identifierPattern.MatchString(f.DefectID) ||
			!slices.Contains([]string{"critical", "major", "advisory"}, f.Severity) ||
			!slices.Contains([]string{"supported", "unsupported", "additional-real", "disputed", "unknown"}, f.State) || !boundedProse(f.Reason) {
			return fmt.Errorf("invalid or duplicate review finding identity")
		}
		b := slices.IndexFunc(r.Blockers, func(b KeyedDefect) bool { return b.ID == f.DefectID })
		if b >= 0 && f.Severity != "advisory" && f.Severity != r.Blockers[b].Severity || b < 0 && f.State == "supported" && f.Severity != "advisory" {
			return fmt.Errorf("keyed severity mismatch or unadjudicated additional blocker")
		}
		if err := support(f.Evidence, items, []string{"source", "reproduction"}, f.State); err != nil {
			return err
		}
		seen[f.ID] = true
	}
	return nil
}

func validateValidation(v *RubricValidation, s *GradeSubmission, c evalcorpus.Case, r *Rubric, items map[string]GradeEvidence) error {
	if v == nil || !uniqueNames(v.Resolves, 64) || s.Evaluator.Relationship != "independent-validator" ||
		s.Evaluator.Identity == r.Author.Identity || !slices.Contains(s.Evaluator.IndependentOf, r.Author.Identity) ||
		!slices.Contains(s.Evaluator.SourceAccess, "pinned-source") || !slices.Contains(s.Evaluator.SourceAccess, "key") || !slices.Contains(s.Evaluator.SourceAccess, "controls") {
		return fmt.Errorf("rubric validation requires fresh attributed independence from the author and source/key/control access")
	}
	reqs := []Requirement{}
	for _, id := range validationChecks(c, r) {
		kind := "semantic"
		if strings.HasPrefix(id, "control:") {
			kind = "control"
		}
		reqs = append(reqs, Requirement{ID: id, Kind: kind, EvidenceRoutes: []string{"source", "reproduction", "scope"}})
	}
	if err := validateJudgments(v.Checks, reqs, items); err != nil {
		return err
	}
	for _, control := range c.Controls {
		j := v.Checks[slices.IndexFunc(v.Checks, func(j Judgment) bool { return j.ID == "control:"+control.Name })]
		if j.State != "satisfied" {
			continue // failed/unavailable controls remain failures/unknowns
		}
		reproduced := false
		for _, id := range j.Evidence {
			p := items[id].Reproduction
			if p != nil && p.Phase == "behavior" && slices.Equal(p.Command, c.Command) {
				failures, expected := slices.Clone(p.Failed), slices.Clone(control.ExpectedFailures)
				slices.Sort(failures)
				slices.Sort(expected)
				reproduced = reproduced || slices.Equal(failures, expected)
			}
		}
		if !reproduced {
			return fmt.Errorf("control %s lacks exact command and intended behavioral outcomes", control.Name)
		}
	}
	return nil
}

func validateGradeInput(s *GradeSubmission, e *Evaluation, hash string, a *AttemptRecord, ref AttemptRef, c evalcorpus.Case, r *Rubric) error {
	if s.SchemaVersion != 1 || !casePattern.MatchString(s.ID) ||
		!slices.Contains([]string{"grade", "dispute", "invalidate-rubric", "validate-rubric"}, s.Operation) ||
		s.EvaluationID != e.ID || s.EvaluationSHA256 != hash || s.PlanDigest != e.Preparation.PlanDigest ||
		s.Unit != a.Unit || s.Attempt != a.Number || s.AttemptSHA256 != ref.SHA256 ||
		s.CaseSHA256 != a.CaseSHA256 || s.PacketSHA256 != a.PacketSHA256 ||
		!boundedProse(s.Reason) || !reflect.DeepEqual(s.AttemptArtifacts, attemptArtifacts(a)) ||
		!digestPattern.MatchString(s.Rubric.SHA256) || s.PreviousRubricSHA256 != "" && !digestPattern.MatchString(s.PreviousRubricSHA256) {
		return fmt.Errorf("grading identity, artifact bindings, operation or schema mismatch")
	}
	if _, err := localPath("", s.Rubric.Path, false); err != nil {
		return err
	}
	if err := s.Evaluator.validate(); err != nil {
		return err
	}
	if err := validateRubric(r, c, e.Preparation.Plan.Corpus.SHA256); err != nil {
		return err
	}
	items, err := evidenceMap(s)
	if err != nil {
		return err
	}
	for _, id := range []string{s.Supersedes, s.Target} {
		if id != "" && !casePattern.MatchString(id) {
			return fmt.Errorf("invalid prior submission identity")
		}
	}
	switch s.Operation {
	case "grade":
		if s.Target != "" || s.Validation != nil {
			return fmt.Errorf("grade cannot contain dispute/validation fields")
		}
		if err := validateJudgments(s.Judgments, r.Requirements, items); err != nil {
			return err
		}
		if r.Role == "review" {
			return validateReview(s.Review, r, items)
		}
		if s.Review != nil {
			return fmt.Errorf("review judgment on non-review attempt")
		}
	case "validate-rubric":
		if s.Judgments != nil || s.Review != nil || s.Target != "" || s.Supersedes != "" {
			return fmt.Errorf("rubric validation cannot contain grade/dispute fields")
		}
		return validateValidation(s.Validation, s, c, r, items)
	case "dispute", "invalidate-rubric":
		if s.Judgments != nil || s.Review != nil || s.Validation != nil || s.Supersedes != "" ||
			s.Operation == "dispute" && s.Target == "" || s.Operation == "invalidate-rubric" && s.Target != "" {
			return fmt.Errorf("invalid dispute/invalidation shape")
		}
		ids := sortedKeys(items)
		return support(ids, items, []string{"source", "reproduction", "scope"}, "violated")
	}
	return nil
}
