// Package manifest renders and parses the PRD §13 audit record that
// every Orch issue and PR body carries: the approved objective,
// acceptance criteria, and required tests, including which of those
// tests the plan declared the repository's CI does not run; the selected
// role, the exact executor/reviewer model+effort and how the host
// actually delivered that effort; the routing rationale, escalations and
// substitutions, the config revision, and named verification commands
// and results with the commit each was gathered at. Resume/recovery (PRD
// §23: interrupted runs resume) rebuilds run state from these posted
// bodies, so the record must render into a managed markdown region AND
// parse back out losslessly.
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
	"slices"
	"strings"
)

// SchemaVersion is the manifest schema this build renders and parses.
// It lives only in the JSON record (see Manifest.SchemaVersion); the
// markers are locators, never a second source of truth.
//
// v2 added the approved work text (objective, acceptance criteria,
// required tests) and the effort-delivery mechanism. v3 added the commit
// OID each verification was gathered at (Verification.CommitOID). v4
// adds the plan's declaration that the repository's CI does not run a
// given required test (Manifest.TestsCIDoesNotRun).
//
// There is no migration, in either direction. Reading down: a v1 record
// is missing the approved work, which would silently decode as empty,
// and a v2 record's verifications would decode as gathered at no commit
// — which in v3 is a claim about the evidence, not an absence of one —
// so Parse rejects both through the unsupported-version path. A v3
// record is now rejected the same way, though not for that kind of
// reason: an absent v4 declaration is honest about a plan that made
// none, so nothing in a v3 record is misread under v4. Before v4, Parse
// accepted a v3 record; after v4 it does not, and the operator's remedy
// is the one the message names. The version had to rise anyway because
// the new field renders, which is the rule the next paragraph states.
// Reading up: an older binary handed a newer record would drop the field
// it does not know on decode, re-render without it, and fail the drift
// compare with ErrDrift, accusing a human of a hand edit nobody made.
// Raising this constant is what makes the version check fire first and
// say the true thing, so it must rise with every rendered field.
const SchemaVersion = 4

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

// EffortDelivery records how the host actually applied the routed
// reasoning effort to the executor it spawned. An effort is a real host
// parameter on one host and only a sentence in a prompt on another, and
// a record that names an effort without saying which implies an
// enforcement the host may not provide. The set is closed; which value a
// host warrants is the run engine's call, not this package's.
type EffortDelivery string

const (
	// EffortDeliveryParameter means the host applied the routed effort as
	// an actual model parameter (Codex pins model_reasoning_effort in the
	// dispatched agent's own TOML).
	EffortDeliveryParameter EffortDelivery = "parameter"
	// EffortDeliveryPromptCue means the host has no per-spawn effort knob,
	// so the routed effort reached the executor only as a cue in its
	// prompt (Claude Code subagents). The recorded effort is then the
	// routing decision, not an enforced setting.
	EffortDeliveryPromptCue EffortDelivery = "prompt-cue"
)

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
//
// CommitOID is the commit the check was gathered at, so evidence from
// an earlier head stays distinguishable from evidence at the head that
// merges. The run engine stamps it from a head it read itself and
// accepts none from a caller; which head that is per verb is the
// engine's business, not this package's.
//
// It is optional, and an empty value is a claim, not a gap: it says no
// commit was in scope when the entry was written. Requiring one instead
// would force the engine either to invent an OID naming a commit the
// entry is not about, or to fail Render at a site with no head to read
// — after that verb's GitHub mutations had already landed, leaving the
// operator a half-finished record and no remedy. The format is
// unvalidated, exactly as At carries whatever timestamp it was given:
// this package owns the schema, not the vocabulary.
type Verification struct {
	Name      string `json:"name"`
	Command   string `json:"command,omitempty"`
	Result    string `json:"result"`
	Detail    string `json:"detail,omitempty"`
	CommitOID string `json:"commit_oid,omitempty"`
	At        string `json:"at,omitempty"`
}

// Manifest is the canonical audit record. SchemaVersion is first and
// mandatory, exactly like internal/state: Render refuses to write and
// Parse refuses to accept any other version rather than guess.
//
// Objective, AcceptanceCriteria, RequiredTests, and TestsCIDoesNotRun
// are the approved plan text verbatim. They belong in the record, not in
// run state alone, because resume rebuilds an interrupted run's issues
// from these posted bodies: a field held only in state comes back empty
// after a resume, and the dispatch that follows would hand an executor
// an empty objective without saying so.
//
// TestsCIDoesNotRun names the entries of RequiredTests the plan declared
// the repository's CI does not run, so a required test that only ever
// runs locally is distinguishable from one CI also holds. It is a
// declaration the plan author made and a human approved, never something
// this package or the engine derives: deciding CI coverage mechanically
// would mean reading workflow definitions and build tags, which nothing
// in this module does. An empty list is the honest reading of a plan that
// declared nothing, not a claim that CI runs every required test.
type Manifest struct {
	SchemaVersion      int            `json:"schema_version"`
	Objective          string         `json:"objective"`
	AcceptanceCriteria []string       `json:"acceptance_criteria"`
	RequiredTests      []string       `json:"required_tests"`
	TestsCIDoesNotRun  []string       `json:"tests_ci_does_not_run,omitempty"`
	Role               Role           `json:"role"`
	Executor           Selection      `json:"executor"`
	RoutingRationale   string         `json:"routing_rationale"`
	Reviewer           Selection      `json:"reviewer"`
	EffortDelivery     EffortDelivery `json:"effort_delivery"`
	Escalations        []Escalation   `json:"escalations,omitempty"`
	ConfigRevision     string         `json:"config_revision"`
	Verifications      []Verification `json:"verifications,omitempty"`
}

// validate reports the first schema-completeness violation in m. Render
// treats a violation as a caller bug and returns it plainly; Parse
// re-wraps a decoded record's violation as ErrBadManifest.
//
// The version mismatch names `orch abort` instead of telling the
// operator to regenerate the record. Nothing regenerates a posted
// record: every body-reading verb fails closed on this same check, so
// an operator who followed that advice had no command to run. Naming
// one concrete command for an unrepairable version mismatch follows
// state.Load's precedent (internal/state/state.go).
func (m Manifest) validate() error {
	if m.SchemaVersion != SchemaVersion {
		return fmt.Errorf("schema_version %d is unsupported (this build renders %d; a posted record cannot be migrated in place — run `orch abort` to return to assist, then re-plan and re-activate)", m.SchemaVersion, SchemaVersion)
	}
	if m.Objective == "" {
		return errors.New("objective is empty")
	}
	if err := validateTextList("acceptance_criteria", m.AcceptanceCriteria); err != nil {
		return err
	}
	if err := validateTextList("required_tests", m.RequiredTests); err != nil {
		return err
	}
	if err := validateTestsCIDoesNotRun(m.RequiredTests, m.TestsCIDoesNotRun); err != nil {
		return err
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
	if !validEffortDelivery(m.EffortDelivery) {
		return fmt.Errorf("effort_delivery %q is not one of %s", m.EffortDelivery, strings.Join(effortDeliveryNames(), ", "))
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

// validateTextList requires a non-empty list of non-empty entries — the
// same floor the plan gate already enforces on an approved issue's
// acceptance criteria and required tests. An issue that cannot pass the
// gate without them must not reach an audit record without them either,
// because dispatch transcribes this text into the executor's prompt.
func validateTextList(field string, values []string) error {
	if len(values) == 0 {
		return fmt.Errorf("%s is empty", field)
	}
	for i, v := range values {
		if v == "" {
			return fmt.Errorf("%s[%d] is empty", field, i)
		}
	}
	return nil
}

// validateTestsCIDoesNotRun requires every declared entry to be non-empty
// and to name one of the record's own required tests by exact command.
// The human view annotates a required test in place, matching on that
// exact string, so an entry naming anything else would render nowhere and
// exist only in the hidden data comment — the one shape the drift check
// cannot catch, since a re-render of the decoded record reproduces the
// same invisible field.
//
// This referential rule is TestsCIDoesNotRun's alone, not a rule about
// repeated string lists in a Manifest generally: it is the only list here
// whose entries name entries of another field. AcceptanceCriteria and
// RequiredTests are checked for presence and non-emptiness only
// (validateTextList), and nothing constrains them against each other.
func validateTestsCIDoesNotRun(required, declared []string) error {
	for i, t := range declared {
		if t == "" {
			return fmt.Errorf("tests_ci_does_not_run[%d] is empty", i)
		}
		if !slices.Contains(required, t) {
			return fmt.Errorf("tests_ci_does_not_run[%d] %q does not name one of required_tests", i, t)
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

func validEffortDelivery(d EffortDelivery) bool {
	switch d {
	case EffortDeliveryParameter, EffortDeliveryPromptCue:
		return true
	default:
		return false
	}
}

func effortDeliveryNames() []string {
	return []string{string(EffortDeliveryParameter), string(EffortDeliveryPromptCue)}
}
