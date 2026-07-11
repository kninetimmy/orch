// Package manifest renders and parses the PRD §13 audit record that
// every Orch issue and PR body carries: the selected role, the exact
// executor/reviewer model+effort, the routing rationale, escalations
// and substitutions, the config revision, and named verification
// commands and results. Resume/recovery (PRD §23: interrupted runs
// resume) rebuilds run state from these posted bodies, so the record
// must render into a managed markdown region AND parse back out
// losslessly.
//
// Like gitops and ghops the package is policy-free: it owns the schema,
// the managed region, and drift detection, while the run engine decides
// content. It imports no config — model and effort vocabulary is
// config's job, not this package's.
//
// The managed region wraps two views of one canonical record: rendered
// human-readable markdown (PRD §23 requires the model and effort be
// visible in bodies) and, inside an HTML comment, the canonical JSON
// that Parse reads. Bytes outside the region are human-owned and
// preserved verbatim by Upsert. Parse fails closed on any drift: it
// re-renders the decoded record and byte-compares it against the found
// region, so a hand edit to either view that is not mirrored in the
// other is rejected. A hand edit that rewrites the JSON and the
// markdown consistently is undetectable in v1 (no signature); the check
// guarantees the region's internal consistency, not its provenance.
package manifest

import (
	"errors"
	"fmt"
	"strings"
)

// SchemaVersion is the manifest schema this build renders and parses.
// It lives only in the JSON record (see Manifest.SchemaVersion); the
// markers are locators, never a second source of truth.
const SchemaVersion = 1

// BeginMarker and EndMarker delimit the managed region. A line is a
// marker only if, after stripping at most one trailing "\r", it equals
// the marker exactly — mid-line mentions in prose are ordinary content.
const (
	BeginMarker = "<!-- orch:manifest:begin -->"
	EndMarker   = "<!-- orch:manifest:end -->"
)

// dataOpen introduces the canonical-JSON comment inside the region and
// dataClose terminates it. The JSON occupies the lines between a line
// equal to dataOpen and the first following line equal to dataClose;
// because dataOpen begins an HTML comment the JSON is invisible on
// GitHub.
const (
	dataOpen  = "<!-- orch:manifest:data"
	dataClose = "-->"
)

// Role is the routed agent role recorded in the audit record. The five
// values mirror config's role set; membership is validated here, but
// the model and effort chosen for a role are config's vocabulary.
type Role string

const (
	RoleArchitect   Role = "architect"
	RoleScout       Role = "scout"
	RoleImplementer Role = "implementer"
	RoleSpecialist  Role = "specialist"
	RoleReviewer    Role = "reviewer"
)

// Selection is an exact model and effort pairing (PRD §13).
type Selection struct {
	Model  string `json:"model"`
	Effort string `json:"effort"`
}

// Escalation records a routing change: an escalation to a stronger
// selection or a substitution of an equivalent one. From and To are the
// selections before and after; At is a caller-supplied RFC3339 UTC
// timestamp (a plain string so Render never touches the clock).
type Escalation struct {
	Kind   string    `json:"kind"` // "escalation" | "substitution"
	Role   Role      `json:"role,omitempty"`
	From   Selection `json:"from"`
	To     Selection `json:"to"`
	Reason string    `json:"reason"`
	At     string    `json:"at,omitempty"`
}

// Verification records one named check and its outcome. Command is
// empty for CI-state entries that report a status without a local
// command; Result is a free string ("pass", "CLEAN", ...).
type Verification struct {
	Name    string `json:"name"`
	Command string `json:"command,omitempty"`
	Result  string `json:"result"`
	Detail  string `json:"detail,omitempty"`
	At      string `json:"at,omitempty"`
}

// Manifest is the canonical audit record. SchemaVersion is first and
// mandatory, exactly like internal/state: Render refuses to write and
// Parse refuses to accept any other version rather than guess.
type Manifest struct {
	SchemaVersion    int            `json:"schema_version"`
	Role             Role           `json:"role"`
	Executor         Selection      `json:"executor"`
	RoutingRationale string         `json:"routing_rationale"`
	Reviewer         Selection      `json:"reviewer"`
	Escalations      []Escalation   `json:"escalations,omitempty"`
	ConfigRevision   string         `json:"config_revision"`
	Verifications    []Verification `json:"verifications,omitempty"`
}

// validate reports the first schema-completeness violation in m. Render
// treats a violation as a caller bug and returns it plainly; Parse
// re-wraps a decoded record's violation as ErrBadManifest.
func (m Manifest) validate() error {
	if m.SchemaVersion != SchemaVersion {
		return fmt.Errorf("schema_version %d is unsupported (this build renders %d; regenerate the record)", m.SchemaVersion, SchemaVersion)
	}
	if !validRole(m.Role) {
		return fmt.Errorf("role %q is not one of %s", m.Role, strings.Join(roleNames(), ", "))
	}
	if err := validateSelection("executor", m.Executor); err != nil {
		return err
	}
	if err := validateSelection("reviewer", m.Reviewer); err != nil {
		return err
	}
	if m.RoutingRationale == "" {
		return errors.New("routing_rationale is empty")
	}
	if m.ConfigRevision == "" {
		return errors.New("config_revision is empty")
	}
	for i, e := range m.Escalations {
		if err := validateEscalation(e); err != nil {
			return fmt.Errorf("escalations[%d]: %w", i, err)
		}
	}
	for i, v := range m.Verifications {
		if err := validateVerification(v); err != nil {
			return fmt.Errorf("verifications[%d]: %w", i, err)
		}
	}
	return nil
}

func validateSelection(field string, s Selection) error {
	if s.Model == "" {
		return fmt.Errorf("%s.model is empty", field)
	}
	if s.Effort == "" {
		return fmt.Errorf("%s.effort is empty", field)
	}
	return nil
}

func validateEscalation(e Escalation) error {
	switch e.Kind {
	case "escalation", "substitution":
	default:
		return fmt.Errorf("kind %q is not one of escalation, substitution", e.Kind)
	}
	if err := validateSelection("from", e.From); err != nil {
		return err
	}
	if err := validateSelection("to", e.To); err != nil {
		return err
	}
	if e.Reason == "" {
		return errors.New("reason is empty")
	}
	return nil
}

func validateVerification(v Verification) error {
	if v.Name == "" {
		return errors.New("name is empty")
	}
	if v.Result == "" {
		return errors.New("result is empty")
	}
	return nil
}

func validRole(r Role) bool {
	switch r {
	case RoleArchitect, RoleScout, RoleImplementer, RoleSpecialist, RoleReviewer:
		return true
	default:
		return false
	}
}

func roleNames() []string {
	return []string{
		string(RoleArchitect), string(RoleScout), string(RoleImplementer),
		string(RoleSpecialist), string(RoleReviewer),
	}
}
