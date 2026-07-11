package run

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strings"

	"github.com/kninetimmy/orch/internal/config"
	"github.com/kninetimmy/orch/internal/ghops"
	"github.com/kninetimmy/orch/internal/routing"
)

// PlanSchemaVersion is the plan-document schema this build accepts.
const PlanSchemaVersion = 1

// PlanDoc is the adapter-submitted plan: a set of issues, grouped into
// dependency-respecting waves, that `orch run plan` gates and
// `orch run activate` turns into a Delivery run.
type PlanDoc struct {
	SchemaVersion int         `json:"schema_version"`
	Host          string      `json:"host"`
	Title         string      `json:"title"`
	Summary       string      `json:"summary,omitempty"`
	Issues        []PlanIssue `json:"issues"`
}

// PlanIssue is one unit of work in the plan.
type PlanIssue struct {
	ID                 string    `json:"id"`
	Title              string    `json:"title"`
	Objective          string    `json:"objective"`
	AcceptanceCriteria []string  `json:"acceptance_criteria"`
	Type               string    `json:"type"` // ghops.Type
	AreaLabels         []string  `json:"area_labels,omitempty"`
	Facts              PlanFacts `json:"facts"`
	DependsOn          []string  `json:"depends_on,omitempty"`
	Wave               int       `json:"wave"`
	RequiredTests      []string  `json:"required_tests"`
	UsageClass         string    `json:"usage_class"` // light|medium|heavy
}

// PlanFacts carries the routing-relevant facts about one issue (PRD
// §9–§11). Routing is derived from these, never independently
// declared: "adjust routing" means revise facts and resubmit.
type PlanFacts struct {
	ReadOnly           bool          `json:"read_only"`
	UnusuallyDifficult bool          `json:"unusually_difficult"`
	RiskDomains        []string      `json:"risk_domains,omitempty"`
	Downgrade          PlanDowngrade `json:"downgrade"`
}

// PlanDowngrade mirrors routing.DowngradeFacts on the wire.
type PlanDowngrade struct {
	Mechanical     bool `json:"mechanical"`
	LowRisk        bool `json:"low_risk"`
	FullySpecified bool `json:"fully_specified"`
	Unsurprising   bool `json:"unsurprising"`
}

// idPattern is the closed issue-id shape: lowercase, starting with a
// letter or digit, otherwise letters, digits, and hyphens.
var idPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9-]*$`)

// usageClasses is the closed usage_class set.
var usageClasses = map[string]bool{"light": true, "medium": true, "heavy": true}

// DecodePlan decodes data into a PlanDoc, rejecting any field this
// build does not recognize at any level (schema-versioned documents
// fail closed on drift rather than silently ignore it).
func DecodePlan(data []byte) (*PlanDoc, error) {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	var p PlanDoc
	if err := dec.Decode(&p); err != nil {
		return nil, fmt.Errorf("%w: decode plan document: %v", ErrPlanInvalid, err)
	}
	if dec.More() {
		return nil, fmt.Errorf("%w: trailing data after plan document", ErrPlanInvalid)
	}
	return &p, nil
}

// Digest returns the plan's stable content hash: "sha256:" followed by
// the hex SHA-256 of the re-marshaled struct. Marshaling the decoded
// struct (rather than hashing the raw input bytes) makes the digest
// agree regardless of the adapter's original whitespace or key order,
// so plan and activate compute the same value for the same content.
func (p *PlanDoc) Digest() (string, error) {
	data, err := json.Marshal(p)
	if err != nil {
		return "", fmt.Errorf("encode plan for digest: %w", err)
	}
	sum := sha256.Sum256(data)
	return "sha256:" + hex.EncodeToString(sum[:]), nil
}

// issuesInWaveOrder returns a copy of p.Issues stably sorted by
// ascending Wave; issues within the same wave keep their original
// relative order.
func (p *PlanDoc) issuesInWaveOrder() []PlanIssue {
	out := make([]PlanIssue, len(p.Issues))
	copy(out, p.Issues)
	sort.SliceStable(out, func(i, j int) bool { return out[i].Wave < out[j].Wave })
	return out
}

// Validate checks the plan against cfg, collecting every violation
// into one error wrapping ErrPlanInvalid rather than stopping at the
// first problem (config-package house style). It never mutates p.
//
// Strictly increasing waves across a dependency edge (dep.wave <
// issue.wave) prove the dependency graph acyclic without a separate
// topological sort. Risk is not independently checked here beyond
// domain membership: it is derived from facts.risk_domains (see
// deriveRisk), never declared, so it cannot contradict them.
func (p *PlanDoc) Validate(cfg *config.Config) error {
	var problems []string
	fail := func(format string, args ...any) {
		problems = append(problems, fmt.Sprintf(format, args...))
	}

	if p.SchemaVersion != PlanSchemaVersion {
		fail("schema_version: unsupported version %d (this build supports %d)", p.SchemaVersion, PlanSchemaVersion)
	}
	hostEnabled := false
	for _, h := range cfg.EnabledHosts() {
		if h == p.Host {
			hostEnabled = true
			break
		}
	}
	if !hostEnabled {
		fail("host: %q is not enabled in configuration", p.Host)
	}
	if p.Title == "" {
		fail("title: must not be empty")
	}
	if len(p.Issues) == 0 {
		fail("issues: must have at least one entry")
	}

	denylist := modelDenylist(cfg)
	waveByID := map[string]int{}
	for _, iss := range p.Issues {
		waveByID[strings.ToLower(iss.ID)] = iss.Wave
	}

	seenIDs := map[string]bool{}
	for i, iss := range p.Issues {
		prefix := fmt.Sprintf("issues[%d] (%s)", i, iss.ID)
		folded := strings.ToLower(iss.ID)

		if !idPattern.MatchString(iss.ID) {
			fail("%s: id must match %s", prefix, idPattern.String())
		}
		if seenIDs[folded] {
			fail("%s: duplicate id", prefix)
		}
		seenIDs[folded] = true

		if iss.Title == "" {
			fail("%s: title must not be empty", prefix)
		}
		if iss.Objective == "" {
			fail("%s: objective must not be empty", prefix)
		}
		if len(iss.AcceptanceCriteria) == 0 {
			fail("%s: acceptance_criteria must have at least one entry", prefix)
		}
		for j, ac := range iss.AcceptanceCriteria {
			if ac == "" {
				fail("%s: acceptance_criteria[%d] must not be empty", prefix, j)
			}
		}
		if len(iss.RequiredTests) == 0 {
			fail("%s: required_tests must have at least one entry", prefix)
		}
		for j, rt := range iss.RequiredTests {
			if rt == "" {
				fail("%s: required_tests[%d] must not be empty", prefix, j)
			}
		}
		if !usageClasses[iss.UsageClass] {
			fail("%s: usage_class %q is not one of light, medium, heavy", prefix, iss.UsageClass)
		}
		if iss.Wave < 1 {
			fail("%s: wave must be >= 1, got %d", prefix, iss.Wave)
		}
		if iss.Facts.ReadOnly {
			fail("%s: facts.read_only must be false (read-only work stays in Assist)", prefix)
		}
		for _, d := range iss.Facts.RiskDomains {
			if !routing.RiskDomain(d).Valid() {
				fail("%s: facts.risk_domains: unknown domain %q", prefix, d)
			}
		}
		for _, dep := range iss.DependsOn {
			depFolded := strings.ToLower(dep)
			if depFolded == folded {
				fail("%s: depends_on: self-reference to %q", prefix, dep)
				continue
			}
			depWave, ok := waveByID[depFolded]
			if !ok {
				fail("%s: depends_on: %q does not resolve to a plan issue", prefix, dep)
				continue
			}
			if !(depWave < iss.Wave) {
				fail("%s: depends_on: %q is in wave %d, which is not strictly before this issue's wave %d", prefix, dep, depWave, iss.Wave)
			}
		}

		// Label-shape validation: the plan type and area labels are
		// checked here through the taxonomy itself (status and role are
		// placeholders — routing has not run yet — but they are always
		// valid, so only type and areas can fail this call).
		labels := ghops.Labels{
			Status: ghops.StatusReady,
			Type:   ghops.Type(iss.Type),
			Role:   ghops.RoleImplementer,
			Risk:   deriveRisk(iss),
			Areas:  iss.AreaLabels,
		}
		if err := labels.Validate(denylist...); err != nil {
			fail("%s: %v", prefix, err)
		}
	}

	if len(problems) > 0 {
		return fmt.Errorf("%w:\n  - %s", ErrPlanInvalid, strings.Join(problems, "\n  - "))
	}
	return nil
}
