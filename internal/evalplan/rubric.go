package evalplan

import (
	"fmt"
	"slices"
	"strings"

	"github.com/kninetimmy/orch/internal/evalcorpus"
)

// Evaluator is attributed provenance, not an authenticated principal. Exposure
// and independence are assertions retained for a fresh reviewer to assess.
type Evaluator struct {
	Identity      string   `json:"identity"`
	Kind          string   `json:"kind"`
	Relationship  string   `json:"relationship"`
	IndependentOf []string `json:"independent_of"`
	SourceAccess  []string `json:"source_access"`
	Exposure      []string `json:"exposure"`
}

type Requirement struct {
	ID             string   `json:"id"`
	Kind           string   `json:"kind"`
	Expectation    string   `json:"expectation"`
	Severity       string   `json:"severity"`
	EvidenceRoutes []string `json:"evidence_routes"`
	Checks         []string `json:"checks"`
}

type KeyedDefect struct {
	ID          string `json:"id"`
	Severity    string `json:"severity"`
	Requirement string `json:"requirement"`
	Description string `json:"description"`
}

type ControlBinding struct {
	Name             string   `json:"name"`
	SHA256           string   `json:"sha256"`
	ExpectedFailures []string `json:"expected_failures"`
}

// Rubric is controller-only data. Case/control digests use storedBytes (indented
// JSON plus LF), exactly as the retained controller case. No grading code runs.
type Rubric struct {
	SchemaVersion  int              `json:"schema_version"`
	Version        int              `json:"version"`
	ControllerOnly bool             `json:"controller_only"`
	CorpusSHA256   string           `json:"corpus_sha256"`
	CaseID         string           `json:"case_id"`
	CaseVersion    int              `json:"case_version"`
	CaseSHA256     string           `json:"case_sha256"`
	PacketSHA256   string           `json:"packet_sha256"`
	KeySHA256      string           `json:"key_sha256"`
	ProbeSHA256    string           `json:"probe_sha256"`
	Role           string           `json:"role"`
	Classification string           `json:"classification,omitempty"`
	Author         Evaluator        `json:"author"`
	Controls       []ControlBinding `json:"controls"`
	Requirements   []Requirement    `json:"requirements"`
	Blockers       []KeyedDefect    `json:"blockers"`
	Alternatives   []string         `json:"alternatives"`
}

func boundedProse(s string) bool { return strings.TrimSpace(s) != "" && len(s) <= 4096 }

func uniqueNames(names []string, max int) bool {
	if names == nil || len(names) > max {
		return false
	}
	seen := map[string]bool{}
	for _, name := range names {
		if !identifierPattern.MatchString(name) || seen[name] {
			return false
		}
		seen[name] = true
	}
	return true
}

func (p Evaluator) validate() error {
	if !identifierPattern.MatchString(p.Identity) || !slices.Contains([]string{"human", "agent"}, p.Kind) ||
		!slices.Contains([]string{"author", "evaluator", "independent-validator"}, p.Relationship) ||
		!uniqueNames(p.IndependentOf, 32) || !uniqueNames(p.SourceAccess, 32) || len(p.SourceAccess) == 0 ||
		!uniqueNames(p.Exposure, 32) || len(p.Exposure) == 0 {
		return fmt.Errorf("require bounded attributed evaluator identity/kind/relationship, source access and exposure")
	}
	return nil
}

func validateRubric(r *Rubric, c evalcorpus.Case, corpus string) error {
	if r.SchemaVersion != 1 || r.Version < 1 || r.Version > 10000 || !r.ControllerOnly || r.CorpusSHA256 != corpus ||
		r.CaseID != c.ID || r.CaseVersion != c.Version || r.CaseSHA256 != storedDigest(c) ||
		r.PacketSHA256 != c.PacketSHA256 || r.KeySHA256 != c.Key.SHA256 || r.ProbeSHA256 != c.Probe.SHA256 ||
		r.Role != c.Role || r.Version == 1 && r.Classification != c.Classification ||
		r.Role == "review" && !slices.Contains([]string{"clean", "defective"}, r.Classification) || r.Role != "review" && r.Classification != "" {
		return fmt.Errorf("rubric schema/corpus/case/packet/key/probe identity mismatch")
	}
	if err := r.Author.validate(); err != nil || r.Author.Relationship != "author" {
		return fmt.Errorf("rubric author provenance invalid: %v", err)
	}
	if len(r.Controls) != len(c.Controls) {
		return fmt.Errorf("rubric must pin every control")
	}
	for i, control := range c.Controls {
		binding := r.Controls[i]
		if binding.Name != control.Name || binding.SHA256 != storedDigest(control) || !slices.Equal(binding.ExpectedFailures, control.ExpectedFailures) || binding.ExpectedFailures == nil {
			return fmt.Errorf("rubric control binding mismatch: %s", control.Name)
		}
	}
	if len(r.Requirements) == 0 || len(r.Requirements) > 64 || r.Blockers == nil || len(r.Blockers) > 32 || len(r.Alternatives) == 0 || len(r.Alternatives) > 16 {
		return fmt.Errorf("rubric requires bounded requirements, blockers and acceptable alternatives")
	}
	ids, kinds := map[string]bool{}, map[string]bool{}
	for _, req := range r.Requirements {
		if !casePattern.MatchString(req.ID) || ids[req.ID] || !boundedProse(req.Expectation) ||
			!slices.Contains([]string{"conclusion", "behavior", "regression", "scope"}, req.Kind) ||
			!slices.Contains([]string{"critical", "major"}, req.Severity) || !uniqueNames(req.EvidenceRoutes, 3) || len(req.EvidenceRoutes) == 0 ||
			!uniqueNames(req.Checks, 64) {
			return fmt.Errorf("invalid or duplicate rubric requirement %q", req.ID)
		}
		for _, route := range req.EvidenceRoutes {
			if !slices.Contains([]string{"source", "reproduction", "scope"}, route) {
				return fmt.Errorf("unsupported evidence route")
			}
		}
		// An implementation's runtime behavior cannot be established by a
		// completion label or compiler failure. Scope may use source analysis.
		if c.Role == "implementation" && req.Kind != "scope" && !slices.Equal(req.EvidenceRoutes, []string{"reproduction"}) {
			return fmt.Errorf("implementation behavior/regression requires reproduction evidence")
		}
		ids[req.ID], kinds[req.Kind] = true, true
	}
	if c.Role == "implementation" && (!kinds["behavior"] || !kinds["regression"] || !kinds["scope"]) ||
		c.Role != "implementation" && !kinds["conclusion"] {
		return fmt.Errorf("rubric lacks role-required behavior/regression/scope or conclusions")
	}
	defects := map[string]bool{}
	for _, b := range r.Blockers {
		if !identifierPattern.MatchString(b.ID) || defects[b.ID] || !ids[b.Requirement] ||
			!slices.Contains([]string{"critical", "major"}, b.Severity) || !boundedProse(b.Description) {
			return fmt.Errorf("invalid or duplicate keyed defect")
		}
		defects[b.ID] = true
	}
	if c.Role != "review" && len(r.Blockers) != 0 || r.Classification == "clean" && len(r.Blockers) != 0 || r.Classification == "defective" && len(r.Blockers) == 0 {
		return fmt.Errorf("rubric blocker classification mismatch")
	}
	for _, alternative := range r.Alternatives {
		if !boundedProse(alternative) {
			return fmt.Errorf("acceptable alternative must be bounded nonempty prose")
		}
	}
	return nil
}

// Validation requires semantic checks as well as actual control reproductions.
// Their truth remains an explicitly attributed assertion, not machine proof.
func validationChecks(c evalcorpus.Case, r *Rubric) []string {
	ids := []string{"key", "probe", "alternatives", "classification"}
	for _, input := range c.Inputs {
		ids = append(ids, "source:"+input.Path)
	}
	for _, control := range c.Controls {
		ids = append(ids, "control:"+control.Name)
	}
	for _, req := range r.Requirements {
		ids = append(ids, "requirement:"+req.ID)
	}
	return ids
}
