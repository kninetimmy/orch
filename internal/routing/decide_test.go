package routing

import (
	"errors"
	"strings"
	"testing"

	"github.com/kninetimmy/orch/internal/manifest"
)

// testProfile is a complete profile with a distinct model string per
// role, so failed-set logic is unambiguous in tests.
func testProfile() Profile {
	return Profile{
		Architect:       manifest.Selection{Model: "architect-m", Effort: "xhigh"},
		Scout:           manifest.Selection{Model: "scout-m", Effort: "low"},
		Implementer:     manifest.Selection{Model: "impl-m", Effort: "high"},
		Specialist:      manifest.Selection{Model: "spec-m", Effort: "high"},
		Reviewer:        manifest.Selection{Model: "rev-m", Effort: "high"},
		ReviewDowngrade: manifest.Selection{Model: "revdown-m", Effort: "medium"},
	}
}

func TestDecideBaseline(t *testing.T) {
	p := testProfile()
	d, err := Decide(p, Task{}, nil)
	if err != nil {
		t.Fatalf("Decide: %v", err)
	}
	if d.Role != manifest.RoleImplementer {
		t.Errorf("role = %q, want implementer", d.Role)
	}
	if d.Executor != p.Implementer {
		t.Errorf("executor = %+v, want %+v", d.Executor, p.Implementer)
	}
	if d.Reviewer != p.Reviewer {
		t.Errorf("reviewer = %+v, want %+v", d.Reviewer, p.Reviewer)
	}
	if d.ReviewerDowngraded {
		t.Error("baseline task must not downgrade the reviewer")
	}
	if d.Rationale == "" {
		t.Error("rationale must be non-empty")
	}
}

func TestDecideRiskDomainsRouteToSpecialist(t *testing.T) {
	p := testProfile()
	for _, dom := range Domains() {
		t.Run(string(dom), func(t *testing.T) {
			d, err := Decide(p, Task{RiskDomains: []RiskDomain{dom}}, nil)
			if err != nil {
				t.Fatalf("Decide: %v", err)
			}
			if d.Role != manifest.RoleSpecialist {
				t.Errorf("role = %q, want specialist", d.Role)
			}
			if d.Executor != p.Specialist {
				t.Errorf("executor = %+v, want specialist", d.Executor)
			}
			if d.Reviewer != p.Reviewer {
				t.Errorf("reviewer = %+v, want strong reviewer", d.Reviewer)
			}
			if d.ReviewerDowngraded {
				t.Error("risk work must never downgrade the reviewer")
			}
		})
	}
}

// TestDecideDowngradeMatrix exhaustively covers 16 downgrade-fact combos
// × {risk, no-risk} × {difficult, not} = 64 cases and asserts the
// downgrade is granted in exactly one (all four facts, no risk, not
// difficult).
func TestDecideDowngradeMatrix(t *testing.T) {
	p := testProfile()
	grants := 0
	for combo := 0; combo < 16; combo++ {
		facts := DowngradeFacts{
			Mechanical:     combo&1 != 0,
			LowRisk:        combo&2 != 0,
			FullySpecified: combo&4 != 0,
			Unsurprising:   combo&8 != 0,
		}
		for _, risk := range []bool{false, true} {
			for _, difficult := range []bool{false, true} {
				task := Task{UnusuallyDifficult: difficult, Downgrade: facts}
				if risk {
					task.RiskDomains = []RiskDomain{RiskSecurity}
				}
				d, err := Decide(p, task, nil)
				if err != nil {
					t.Fatalf("combo=%d risk=%v difficult=%v: %v", combo, risk, difficult, err)
				}
				wantGrant := facts.Eligible() && !risk && !difficult
				if d.ReviewerDowngraded != wantGrant {
					t.Errorf("combo=%d risk=%v difficult=%v: downgraded=%v, want %v", combo, risk, difficult, d.ReviewerDowngraded, wantGrant)
				}
				switch {
				case wantGrant:
					grants++
					if d.Role != manifest.RoleImplementer || d.Reviewer != p.ReviewDowngrade {
						t.Errorf("granted downgrade: role=%q reviewer=%+v, want implementer + downgrade reviewer", d.Role, d.Reviewer)
					}
				case risk || difficult:
					if d.Role != manifest.RoleSpecialist || d.Reviewer != p.Reviewer {
						t.Errorf("combo=%d risk=%v difficult=%v: role=%q reviewer=%+v, want specialist + strong reviewer", combo, risk, difficult, d.Role, d.Reviewer)
					}
				default:
					if d.Role != manifest.RoleImplementer || d.Reviewer != p.Reviewer {
						t.Errorf("combo=%d: role=%q reviewer=%+v, want implementer + strong reviewer", combo, d.Role, d.Reviewer)
					}
				}
			}
		}
	}
	if grants != 1 {
		t.Errorf("downgrade granted in %d cases, want exactly 1", grants)
	}
}

func TestDecideReadOnly(t *testing.T) {
	p := testProfile()
	tests := map[string]struct {
		task         Task
		history      History
		wantExecutor manifest.Selection
	}{
		"plain read-only goes to scout": {
			Task{ReadOnly: true},
			nil,
			p.Scout,
		},
		"risky read-only goes to specialist model": {
			Task{ReadOnly: true, RiskDomains: []RiskDomain{RiskSecrets}},
			nil,
			p.Specialist,
		},
		"difficult read-only goes to specialist model": {
			Task{ReadOnly: true, UnusuallyDifficult: true},
			nil,
			p.Specialist,
		},
		"read-only with failed scout goes to specialist model": {
			Task{ReadOnly: true},
			History{{Role: manifest.RoleScout, Selection: p.Scout, Failed: true}},
			p.Specialist,
		},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			d, err := Decide(p, tt.task, tt.history)
			if err != nil {
				t.Fatalf("Decide: %v", err)
			}
			if d.Role != manifest.RoleScout {
				t.Errorf("role = %q, want scout (read-only)", d.Role)
			}
			if d.Executor != tt.wantExecutor {
				t.Errorf("executor = %+v, want %+v", d.Executor, tt.wantExecutor)
			}
			if d.ReviewerDowngraded {
				t.Error("read-only work must not downgrade the reviewer")
			}
		})
	}
}

func TestDecideConflictPrefersStrongerRoute(t *testing.T) {
	p := testProfile()
	// All four downgrade facts assert, but the task is unusually
	// difficult: the stronger route wins and the refusal is recorded.
	d, err := Decide(p, Task{
		UnusuallyDifficult: true,
		Downgrade:          DowngradeFacts{Mechanical: true, LowRisk: true, FullySpecified: true, Unsurprising: true},
	}, nil)
	if err != nil {
		t.Fatalf("Decide: %v", err)
	}
	if d.Role != manifest.RoleSpecialist {
		t.Errorf("role = %q, want specialist", d.Role)
	}
	if d.ReviewerDowngraded {
		t.Error("conflict must not downgrade the reviewer")
	}
	if want := "overriding the reviewer downgrade"; !strings.Contains(d.Rationale, want) {
		t.Errorf("rationale %q does not record the refusal (%q)", d.Rationale, want)
	}
}

func TestDecideHistoryAware(t *testing.T) {
	p := testProfile()

	t.Run("failed implementer escalates to specialist", func(t *testing.T) {
		h := History{{Role: manifest.RoleImplementer, Selection: p.Implementer, Failed: true}}
		d, err := Decide(p, Task{}, h)
		if err != nil {
			t.Fatalf("Decide: %v", err)
		}
		if d.Role != manifest.RoleSpecialist || d.Executor != p.Specialist {
			t.Errorf("role=%q executor=%+v, want specialist", d.Role, d.Executor)
		}
	})

	t.Run("both implementer and specialist failed exhaust the route", func(t *testing.T) {
		h := History{
			{Role: manifest.RoleImplementer, Selection: p.Implementer, Failed: true},
			{Role: manifest.RoleSpecialist, Selection: p.Specialist, Failed: true},
		}
		_, err := Decide(p, Task{}, h)
		if !errors.Is(err, ErrNoStrongerRoute) {
			t.Errorf("err = %v, want ErrNoStrongerRoute", err)
		}
	})

	t.Run("read-only exhausts when specialist failed", func(t *testing.T) {
		h := History{{Role: manifest.RoleSpecialist, Selection: p.Specialist, Failed: true}}
		_, err := Decide(p, Task{ReadOnly: true, RiskDomains: []RiskDomain{RiskSecurity}}, h)
		if !errors.Is(err, ErrNoStrongerRoute) {
			t.Errorf("err = %v, want ErrNoStrongerRoute", err)
		}
	})
}

// TestDecideBadProfile generates the 12 incompleteness cases: 6
// selections × {model, effort} empty.
func TestDecideBadProfile(t *testing.T) {
	fields := []struct {
		name string
		set  func(*Profile, manifest.Selection)
	}{
		{"architect", func(p *Profile, s manifest.Selection) { p.Architect = s }},
		{"scout", func(p *Profile, s manifest.Selection) { p.Scout = s }},
		{"implementer", func(p *Profile, s manifest.Selection) { p.Implementer = s }},
		{"specialist", func(p *Profile, s manifest.Selection) { p.Specialist = s }},
		{"reviewer", func(p *Profile, s manifest.Selection) { p.Reviewer = s }},
		{"review_downgrade", func(p *Profile, s manifest.Selection) { p.ReviewDowngrade = s }},
	}
	missings := map[string]manifest.Selection{
		"empty model":  {Model: "", Effort: "high"},
		"empty effort": {Model: "m", Effort: ""},
	}
	for _, f := range fields {
		for miss, sel := range missings {
			t.Run(f.name+" "+miss, func(t *testing.T) {
				p := testProfile()
				f.set(&p, sel)
				_, err := Decide(p, Task{}, nil)
				if !errors.Is(err, ErrBadProfile) {
					t.Errorf("err = %v, want ErrBadProfile", err)
				}
			})
		}
	}
}

func TestDecideUnknownRiskDomain(t *testing.T) {
	p := testProfile()
	_, err := Decide(p, Task{RiskDomains: []RiskDomain{"performance"}}, nil)
	if !errors.Is(err, ErrBadTask) {
		t.Errorf("err = %v, want ErrBadTask", err)
	}
}

func TestDecideRationaleGolden(t *testing.T) {
	p := testProfile()
	tests := map[string]struct {
		task Task
		want string
	}{
		"plain implementer": {
			Task{},
			"Routed to an implementer with a strong reviewer with no reviewer downgrade requested.",
		},
		"granted downgrade": {
			Task{Downgrade: DowngradeFacts{Mechanical: true, LowRisk: true, FullySpecified: true, Unsurprising: true}},
			"Routed to an implementer with a downgraded reviewer because the change is affirmatively mechanical, low-risk, fully specified, and unsurprising.",
		},
		"partial downgrade refused names the gap": {
			Task{Downgrade: DowngradeFacts{Mechanical: true, FullySpecified: true}},
			"Routed to an implementer with a strong reviewer, declining the reviewer downgrade because the change is not affirmatively low-risk.",
		},
		"specialist for a single risk domain": {
			Task{RiskDomains: []RiskDomain{RiskSecurity}},
			"Routed to a specialist with a strong reviewer because risk domains (security).",
		},
		"specialist renders domains in canonical order": {
			Task{RiskDomains: []RiskDomain{RiskConcurrency, RiskSecurity}},
			"Routed to a specialist with a strong reviewer because risk domains (security, concurrency).",
		},
		"plain read-only scout": {
			Task{ReadOnly: true},
			"Routed read-only work to a scout with no elevated risk or difficulty.",
		},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			d, err := Decide(p, tt.task, nil)
			if err != nil {
				t.Fatalf("Decide: %v", err)
			}
			if d.Rationale != tt.want {
				t.Errorf("rationale =\n  %q\nwant\n  %q", d.Rationale, tt.want)
			}
		})
	}
}
