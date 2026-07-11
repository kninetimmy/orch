package ghops

import (
	"context"
	"fmt"
	"strings"
)

// The PRD §13 label taxonomy: every issue carries exactly one label
// from each of the four groups, plus optional repository-defined area
// labels. The groups are closed Go types so nothing outside these
// values — in particular no model name (PRD §13: models do not become
// GitHub labels) — can ever occupy a taxonomy slot.

// Status is the exactly-one workflow-status label (PRD §13).
type Status string

// The five status labels.
const (
	StatusReady          Status = "ready"
	StatusInProgress     Status = "in-progress"
	StatusBlocked        Status = "blocked"
	StatusNeedsHuman     Status = "needs-human"
	StatusAwaitingReview Status = "awaiting-review"
)

// Type is the exactly-one change-type label (PRD §13).
type Type string

// The six type labels.
const (
	TypeFeature  Type = "feature"
	TypeBug      Type = "bug"
	TypeChore    Type = "chore"
	TypeInfra    Type = "infra"
	TypeDocs     Type = "docs"
	TypeResearch Type = "research"
)

// Role is the exactly-one executor-role label (PRD §13).
type Role string

// The two role labels.
const (
	RoleImplementer Role = "implementer"
	RoleSpecialist  Role = "specialist"
)

// Risk is the exactly-one risk-class label (PRD §13); risk domains
// route to critical (PRD §11).
type Risk string

// The two risk labels.
const (
	RiskStandard Risk = "standard"
	RiskCritical Risk = "critical"
)

// statuses lists all status labels in canonical order; SetStatus and
// the taxonomy table depend on this order being stable.
var statuses = []Status{StatusReady, StatusInProgress, StatusBlocked, StatusNeedsHuman, StatusAwaitingReview}

// labelDef pins a taxonomy label's repository definition so
// EnsureLabelTaxonomy is deterministic and transcripts are stable.
type labelDef struct {
	name        string
	color       string // 6-digit hex, no leading #
	description string
}

// taxonomy is every label EnsureLabelTaxonomy guarantees, in creation
// order: statuses, types, roles, risks. Colors group by kind so the
// GitHub issue list reads at a glance.
var taxonomy = []labelDef{
	{"ready", "1D76DB", "orch status label — exactly one per issue (PRD §13)"},
	{"in-progress", "1D76DB", "orch status label — exactly one per issue (PRD §13)"},
	{"blocked", "1D76DB", "orch status label — exactly one per issue (PRD §13)"},
	{"needs-human", "1D76DB", "orch status label — exactly one per issue (PRD §13)"},
	{"awaiting-review", "1D76DB", "orch status label — exactly one per issue (PRD §13)"},
	{"feature", "0E8A16", "orch type label — exactly one per issue (PRD §13)"},
	{"bug", "0E8A16", "orch type label — exactly one per issue (PRD §13)"},
	{"chore", "0E8A16", "orch type label — exactly one per issue (PRD §13)"},
	{"infra", "0E8A16", "orch type label — exactly one per issue (PRD §13)"},
	{"docs", "0E8A16", "orch type label — exactly one per issue (PRD §13)"},
	{"research", "0E8A16", "orch type label — exactly one per issue (PRD §13)"},
	{"implementer", "5319E7", "orch role label — exactly one per issue (PRD §13)"},
	{"specialist", "5319E7", "orch role label — exactly one per issue (PRD §13)"},
	{"standard", "FBCA04", "orch risk label — exactly one per issue (PRD §13)"},
	{"critical", "B60205", "orch risk label — exactly one per issue (PRD §13)"},
}

// Labels is the complete PRD §13 taxonomy for one issue: exactly one
// value per group plus optional repository-defined area labels.
type Labels struct {
	Status Status
	Type   Type
	Role   Role
	Risk   Risk
	Areas  []string
}

// Validate enforces the taxonomy mechanically: each group value must
// be a member of its closed set (a zero value fails — exactly one
// per group), and every area label must be non-empty, unique, not a
// reserved taxonomy name, and not equal to any caller-supplied
// forbidden string (all case-insensitive). The run engine passes the
// configured model names as forbidden, making PRD §13's "models do
// not become GitHub labels" a mechanical check rather than a
// convention. All failures wrap ErrBadLabels.
func (l Labels) Validate(forbiddenAreas ...string) error {
	var problems []string
	if !memberOf(string(l.Status), statusNames()) {
		problems = append(problems, fmt.Sprintf("status %q is not one of %s", l.Status, strings.Join(statusNames(), ", ")))
	}
	types := []string{string(TypeFeature), string(TypeBug), string(TypeChore), string(TypeInfra), string(TypeDocs), string(TypeResearch)}
	if !memberOf(string(l.Type), types) {
		problems = append(problems, fmt.Sprintf("type %q is not one of %s", l.Type, strings.Join(types, ", ")))
	}
	roles := []string{string(RoleImplementer), string(RoleSpecialist)}
	if !memberOf(string(l.Role), roles) {
		problems = append(problems, fmt.Sprintf("role %q is not one of %s", l.Role, strings.Join(roles, ", ")))
	}
	risks := []string{string(RiskStandard), string(RiskCritical)}
	if !memberOf(string(l.Risk), risks) {
		problems = append(problems, fmt.Sprintf("risk %q is not one of %s", l.Risk, strings.Join(risks, ", ")))
	}
	seen := map[string]bool{}
	for _, area := range l.Areas {
		folded := strings.ToLower(area)
		switch {
		case area == "":
			problems = append(problems, "area label is empty")
		case seen[folded]:
			problems = append(problems, fmt.Sprintf("area %q is duplicated", area))
		case reservedName(folded):
			problems = append(problems, fmt.Sprintf("area %q is a reserved taxonomy label", area))
		case matchesAny(folded, forbiddenAreas):
			problems = append(problems, fmt.Sprintf("area %q is forbidden (models never become labels)", area))
		}
		seen[folded] = true
	}
	if len(problems) > 0 {
		return fmt.Errorf("%w: %s", ErrBadLabels, strings.Join(problems, "; "))
	}
	return nil
}

// flatten validates and returns the labels in deterministic order:
// status, type, role, risk, then areas as given.
func (l Labels) flatten(forbiddenAreas ...string) ([]string, error) {
	if err := l.Validate(forbiddenAreas...); err != nil {
		return nil, err
	}
	out := []string{string(l.Status), string(l.Type), string(l.Role), string(l.Risk)}
	return append(out, l.Areas...), nil
}

func statusNames() []string {
	names := make([]string, len(statuses))
	for i, s := range statuses {
		names[i] = string(s)
	}
	return names
}

func memberOf(v string, set []string) bool {
	for _, s := range set {
		if v == s {
			return true
		}
	}
	return false
}

func reservedName(folded string) bool {
	for _, def := range taxonomy {
		if folded == def.name {
			return true
		}
	}
	return false
}

func matchesAny(folded string, forbidden []string) bool {
	for _, f := range forbidden {
		if folded == strings.ToLower(f) {
			return true
		}
	}
	return false
}

// EnsureLabelTaxonomy lists the repository's labels and creates any
// missing taxonomy label with its pinned color and description;
// existing labels are never modified (no --force: repository
// customizations survive). Idempotent; returns the names it created,
// in taxonomy order, for the audit trail. The list-then-create gap is
// not atomic; the run engine serializes conflicting writes (PRD §14).
func (g *GH) EnsureLabelTaxonomy(ctx context.Context) ([]string, error) {
	var existing []struct {
		Name string `json:"name"`
	}
	// gh's default list limit is 30; --limit must be explicit.
	if err := g.ghJSON(ctx, &existing, "label", "list", "--json", "name", "--limit", "1000"); err != nil {
		return nil, err
	}
	present := map[string]bool{}
	for _, l := range existing {
		present[strings.ToLower(l.Name)] = true
	}
	var created []string
	for _, def := range taxonomy {
		if present[def.name] {
			continue
		}
		if _, err := g.gh(ctx, "label", "create", def.name, "--color", def.color, "--description", def.description); err != nil {
			return created, err
		}
		created = append(created, def.name)
	}
	return created, nil
}
