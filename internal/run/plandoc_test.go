package run

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

func mustDecodePlan(t *testing.T, js string) *PlanDoc {
	t.Helper()
	p, err := DecodePlan([]byte(js))
	if err != nil {
		t.Fatalf("DecodePlan: %v", err)
	}
	return p
}

func TestDecodePlanValid(t *testing.T) {
	p := mustDecodePlan(t, validPlanJSON())
	if p.SchemaVersion != 1 || p.Host != "claude" || len(p.Issues) != 1 {
		t.Fatalf("decoded plan = %+v", p)
	}
	if err := p.Validate(testConfig()); err != nil {
		t.Fatalf("Validate: %v", err)
	}
}

func TestDecodePlanRejectsUnknownFields(t *testing.T) {
	cases := map[string]string{
		"top level": `{"schema_version":1,"host":"claude","title":"t","issues":[],"bogus":true}`,
		"issue level": `{"schema_version":1,"host":"claude","title":"t","issues":[
			{"id":"a","title":"t","objective":"o","acceptance_criteria":["x"],"type":"bug",
			 "facts":{"read_only":false},"wave":1,"required_tests":["t"],"usage_class":"light","bogus":true}]}`,
		"facts level": `{"schema_version":1,"host":"claude","title":"t","issues":[
			{"id":"a","title":"t","objective":"o","acceptance_criteria":["x"],"type":"bug",
			 "facts":{"read_only":false,"bogus":true},"wave":1,"required_tests":["t"],"usage_class":"light"}]}`,
		"downgrade level": `{"schema_version":1,"host":"claude","title":"t","issues":[
			{"id":"a","title":"t","objective":"o","acceptance_criteria":["x"],"type":"bug",
			 "facts":{"read_only":false,"downgrade":{"mechanical":true,"bogus":true}},
			 "wave":1,"required_tests":["t"],"usage_class":"light"}]}`,
	}
	for name, js := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := DecodePlan([]byte(js))
			if !errors.Is(err, ErrPlanInvalid) {
				t.Fatalf("err = %v, want ErrPlanInvalid", err)
			}
			if !strings.Contains(err.Error(), "unknown field") {
				t.Errorf("err = %v, want mention of the unknown field", err)
			}
		})
	}
}

func TestDecodePlanRejectsTrailingData(t *testing.T) {
	_, err := DecodePlan([]byte(validPlanJSON() + `{}`))
	if !errors.Is(err, ErrPlanInvalid) {
		t.Fatalf("err = %v, want ErrPlanInvalid", err)
	}
}

func TestPlanDocValidate(t *testing.T) {
	cases := map[string]struct {
		mutate func(p *PlanDoc)
		wantIn string
	}{
		"wrong version": {
			mutate: func(p *PlanDoc) { p.SchemaVersion = 2 },
			wantIn: "schema_version: unsupported version 2",
		},
		"disabled host": {
			mutate: func(p *PlanDoc) { p.Host = "codex" },
			wantIn: `host: "codex" is not enabled`,
		},
		"empty title": {
			mutate: func(p *PlanDoc) { p.Title = "" },
			wantIn: "title: must not be empty",
		},
		"no issues": {
			mutate: func(p *PlanDoc) { p.Issues = nil },
			wantIn: "issues: must have at least one entry",
		},
		"bad id pattern": {
			mutate: func(p *PlanDoc) { p.Issues[0].ID = "Not_Valid" },
			wantIn: "id must match",
		},
		"empty objective": {
			mutate: func(p *PlanDoc) { p.Issues[0].Objective = "" },
			wantIn: "objective must not be empty",
		},
		"empty acceptance criteria": {
			mutate: func(p *PlanDoc) { p.Issues[0].AcceptanceCriteria = nil },
			wantIn: "acceptance_criteria must have at least one entry",
		},
		"empty required tests": {
			mutate: func(p *PlanDoc) { p.Issues[0].RequiredTests = nil },
			wantIn: "required_tests must have at least one entry",
		},
		"bad usage class": {
			mutate: func(p *PlanDoc) { p.Issues[0].UsageClass = "extreme" },
			wantIn: `usage_class "extreme" is not one of`,
		},
		"bad wave": {
			mutate: func(p *PlanDoc) { p.Issues[0].Wave = 0 },
			wantIn: "wave must be >= 1",
		},
		"read only rejected": {
			mutate: func(p *PlanDoc) { p.Issues[0].Facts.ReadOnly = true },
			wantIn: "facts.read_only must be false",
		},
		"invalid risk domain": {
			mutate: func(p *PlanDoc) { p.Issues[0].Facts.RiskDomains = []string{"bogus-domain"} },
			wantIn: `unknown domain "bogus-domain"`,
		},
		"bad type": {
			mutate: func(p *PlanDoc) { p.Issues[0].Type = "not-a-type" },
			wantIn: `type "not-a-type" is not one of`,
		},
		"area label hits model denylist": {
			mutate: func(p *PlanDoc) { p.Issues[0].AreaLabels = []string{"claude-opus-4-8"} },
			wantIn: "forbidden",
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			p := mustDecodePlan(t, validPlanJSON())
			tc.mutate(p)
			err := p.Validate(testConfig())
			if err == nil {
				t.Fatal("Validate succeeded, want error")
			}
			if !errors.Is(err, ErrPlanInvalid) {
				t.Errorf("err = %v, want ErrPlanInvalid", err)
			}
			if !strings.Contains(err.Error(), tc.wantIn) {
				t.Errorf("err = %v, want to contain %q", err, tc.wantIn)
			}
		})
	}
}

func TestPlanDocValidateDependencyGraph(t *testing.T) {
	cases := map[string]struct {
		mutate func(p *PlanDoc)
		wantIn string
	}{
		"duplicate id": {
			mutate: func(p *PlanDoc) { p.Issues[1].ID = "a" },
			wantIn: "duplicate id",
		},
		"dangling dependency": {
			mutate: func(p *PlanDoc) { p.Issues[1].DependsOn = []string{"missing"} },
			wantIn: `"missing" does not resolve to a plan issue`,
		},
		"self reference": {
			mutate: func(p *PlanDoc) { p.Issues[1].DependsOn = []string{"b"} },
			wantIn: "self-reference",
		},
		"wave not strictly increasing": {
			mutate: func(p *PlanDoc) { p.Issues[1].Wave = 1 }, // same wave as its dependency
			wantIn: "not strictly before",
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			p := mustDecodePlan(t, twoIssuePlanJSON())
			tc.mutate(p)
			err := p.Validate(testConfig())
			if err == nil {
				t.Fatal("Validate succeeded, want error")
			}
			if !errors.Is(err, ErrPlanInvalid) {
				t.Errorf("err = %v, want ErrPlanInvalid", err)
			}
			if !strings.Contains(err.Error(), tc.wantIn) {
				t.Errorf("err = %v, want to contain %q", err, tc.wantIn)
			}
		})
	}
}

func TestPlanDocValidateTwoIssuePlanOK(t *testing.T) {
	p := mustDecodePlan(t, twoIssuePlanJSON())
	if err := p.Validate(testConfig()); err != nil {
		t.Fatalf("Validate: %v", err)
	}
}

func TestDigestStableAcrossWhitespaceAndKeyOrder(t *testing.T) {
	a := mustDecodePlan(t, validPlanJSON())

	reordered := `{
  "issues": [
    {
      "wave": 1,
      "usage_class": "light",
      "required_tests": ["go test ./..."],
      "facts": {"read_only": false},
      "type": "bug",
      "acceptance_criteria": ["no data race under -race"],
      "objective": "Make status reporting race-free",
      "title": "Fix the status lock race",
      "id": "fix-status-lock"
    }
  ],
  "title": "Fix status lock",
  "host": "claude",
  "schema_version": 1
}`
	b := mustDecodePlan(t, reordered)

	// Sanity: the two documents really do decode to the same struct.
	aj, _ := json.Marshal(a)
	bj, _ := json.Marshal(b)
	if string(aj) != string(bj) {
		t.Fatalf("decoded structs differ:\n%s\n%s", aj, bj)
	}

	da, err := a.Digest()
	if err != nil {
		t.Fatal(err)
	}
	db, err := b.Digest()
	if err != nil {
		t.Fatal(err)
	}
	if da != db {
		t.Errorf("Digest = %q, %q; want equal", da, db)
	}
	if !strings.HasPrefix(da, "sha256:") {
		t.Errorf("Digest = %q, want sha256: prefix", da)
	}
}

func TestIssuesInWaveOrder(t *testing.T) {
	p := mustDecodePlan(t, twoIssuePlanJSON())
	// Reverse the input order; wave order must still be a, then b.
	p.Issues[0], p.Issues[1] = p.Issues[1], p.Issues[0]
	ordered := p.issuesInWaveOrder()
	if len(ordered) != 2 || ordered[0].ID != "a" || ordered[1].ID != "b" {
		t.Fatalf("issuesInWaveOrder = %+v, want [a b]", ordered)
	}
}
