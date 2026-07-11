package run

import (
	"errors"
	"strings"
	"testing"

	"github.com/kninetimmy/orch/internal/ghops"
	"github.com/kninetimmy/orch/internal/manifest"
	"github.com/kninetimmy/orch/internal/routing"
)

func TestHostProfile(t *testing.T) {
	cfg := testConfig()
	p, err := hostProfile(cfg, "claude")
	if err != nil {
		t.Fatalf("hostProfile: %v", err)
	}
	if p.Implementer.Model != "claude-sonnet-5" || p.Implementer.Effort != "xhigh" {
		t.Errorf("Implementer = %+v", p.Implementer)
	}
	if _, err := hostProfile(cfg, "codex"); err == nil {
		t.Error("hostProfile accepted a disabled host")
	}
}

func TestDecideIssueFacts(t *testing.T) {
	cfg := testConfigTwoHosts()
	profile, err := hostProfile(cfg, "claude")
	if err != nil {
		t.Fatal(err)
	}

	cases := map[string]struct {
		facts      PlanFacts
		wantRole   manifest.Role
		downgraded bool
	}{
		"plain": {
			facts:    PlanFacts{},
			wantRole: manifest.RoleImplementer,
		},
		"unusually difficult": {
			facts:    PlanFacts{UnusuallyDifficult: true},
			wantRole: manifest.RoleSpecialist,
		},
		"risky": {
			facts:    PlanFacts{RiskDomains: []string{"concurrency"}},
			wantRole: manifest.RoleSpecialist,
		},
		"downgrade eligible": {
			facts: PlanFacts{Downgrade: PlanDowngrade{
				Mechanical: true, LowRisk: true, FullySpecified: true, Unsurprising: true,
			}},
			wantRole:   manifest.RoleImplementer,
			downgraded: true,
		},
		"downgrade conflicts with risk": {
			facts: PlanFacts{
				RiskDomains: []string{"security"},
				Downgrade: PlanDowngrade{
					Mechanical: true, LowRisk: true, FullySpecified: true, Unsurprising: true,
				},
			},
			wantRole:   manifest.RoleSpecialist,
			downgraded: false,
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			i := PlanIssue{ID: "x", Facts: tc.facts}
			d, err := decideIssue(profile, i)
			if err != nil {
				t.Fatalf("decideIssue: %v", err)
			}
			if d.Role != tc.wantRole {
				t.Errorf("Role = %s, want %s", d.Role, tc.wantRole)
			}
			if d.ReviewerDowngraded != tc.downgraded {
				t.Errorf("ReviewerDowngraded = %v, want %v", d.ReviewerDowngraded, tc.downgraded)
			}
			if d.Rationale == "" {
				t.Error("Rationale is empty")
			}
		})
	}
}

func TestDecideIssueBadDomainPropagates(t *testing.T) {
	cfg := testConfig()
	profile, err := hostProfile(cfg, "claude")
	if err != nil {
		t.Fatal(err)
	}
	_, err = decideIssue(profile, PlanIssue{ID: "x", Facts: PlanFacts{RiskDomains: []string{"bogus"}}})
	if !errors.Is(err, routing.ErrBadTask) {
		t.Fatalf("err = %v, want routing.ErrBadTask", err)
	}
}

func TestModelDenylist(t *testing.T) {
	one := modelDenylist(testConfig())
	if len(one) != 2 { // claude-opus-4-8, claude-sonnet-5
		t.Fatalf("one-host denylist = %v, want 2 models", one)
	}
	for _, want := range []string{"claude-opus-4-8", "claude-sonnet-5"} {
		found := false
		for _, m := range one {
			if m == want {
				found = true
			}
		}
		if !found {
			t.Errorf("denylist %v missing %q", one, want)
		}
	}

	two := modelDenylist(testConfigTwoHosts())
	if len(two) != 4 { // + gpt-5.6-sol, gpt-5.6-terra
		t.Fatalf("two-host denylist = %v, want 4 models", two)
	}

	// Overlay: an override changing a model name still surfaces in the
	// denylist since modelDenylist reads the passed *Config directly.
	overlaid := testConfig()
	overlaid.Hosts.Claude.Roles.Implementer.Model = "claude-sonnet-6"
	d := modelDenylist(overlaid)
	found := false
	for _, m := range d {
		if m == "claude-sonnet-6" {
			found = true
		}
	}
	if !found {
		t.Errorf("denylist %v missing overlaid model claude-sonnet-6", d)
	}
}

func TestDeriveRisk(t *testing.T) {
	if got := deriveRisk(PlanIssue{}); got != ghops.RiskStandard {
		t.Errorf("no risk domains = %s, want standard", got)
	}
	if got := deriveRisk(PlanIssue{Facts: PlanFacts{RiskDomains: []string{"secrets"}}}); got != ghops.RiskCritical {
		t.Errorf("with risk domains = %s, want critical", got)
	}
}

func TestIssueLabelsRolePerDecision(t *testing.T) {
	i := PlanIssue{Type: "bug", AreaLabels: []string{"core"}}

	implLabels := issueLabels(i, routing.Decision{Role: manifest.RoleImplementer})
	if implLabels.Role != ghops.RoleImplementer {
		t.Errorf("Role = %s, want implementer", implLabels.Role)
	}
	if implLabels.Status != ghops.StatusReady || implLabels.Type != ghops.TypeBug {
		t.Errorf("labels = %+v", implLabels)
	}

	specLabels := issueLabels(i, routing.Decision{Role: manifest.RoleSpecialist})
	if specLabels.Role != ghops.RoleSpecialist {
		t.Errorf("Role = %s, want specialist", specLabels.Role)
	}
}

func TestFlattenLabels(t *testing.T) {
	l := ghops.Labels{Status: ghops.StatusReady, Type: ghops.TypeBug, Role: ghops.RoleImplementer, Risk: ghops.RiskStandard, Areas: []string{"core", "cli"}}
	got := flattenLabels(l)
	want := []string{"ready", "bug", "implementer", "standard", "core", "cli"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("flattenLabels = %v, want %v", got, want)
	}
}
