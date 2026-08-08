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
		"empty ci declaration": {
			mutate: func(p *PlanDoc) { p.Issues[0].TestsCIDoesNotRun = []string{""} },
			wantIn: "tests_ci_does_not_run[0] must not be empty",
		},
		"ci declaration names no required test": {
			mutate: func(p *PlanDoc) {
				p.Issues[0].TestsCIDoesNotRun = []string{"go test -tags golden ./..."}
			},
			wantIn: `tests_ci_does_not_run[0] "go test -tags golden ./..." does not name one of this issue's required_tests`,
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

// preCIDeclarationDigest is validPlanJSON's digest as builds before the
// tests_ci_does_not_run declaration existed computed it, recomputed
// independently from that build's PlanDoc/PlanIssue definitions rather
// than copied from this build's output.
//
// It is pinned as a literal on purpose: `orch run plan` and
// `orch run activate` both recompute the digest from the submitted
// document, and a plan approved by one build must activate on another, so
// a plan that declares nothing has to marshal to the same bytes it always
// did. An optional field with a non-omitempty tag, or one given a
// non-nil empty default anywhere on the decode path, would break that
// silently — every existing plan and every replay of a prior one would
// fail approval with a digest mismatch and nothing would say why.
const preCIDeclarationDigest = "sha256:00b4d082433c906b228cc85941ea2a16a957253af75e45d96c9b65e73a60902e"

func TestDigestUnchangedForAPlanThatDeclaresNothing(t *testing.T) {
	p := mustDecodePlan(t, validPlanJSON())
	if p.Issues[0].TestsCIDoesNotRun != nil {
		t.Fatalf("a plan declaring nothing decoded to %q, want nil", p.Issues[0].TestsCIDoesNotRun)
	}
	got, err := p.Digest()
	if err != nil {
		t.Fatal(err)
	}
	if got != preCIDeclarationDigest {
		t.Errorf("Digest = %q, want the pre-declaration digest %q: a plan that declares nothing must marshal to the bytes it always did", got, preCIDeclarationDigest)
	}

	data, err := json.Marshal(p)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "tests_ci_does_not_run") {
		t.Errorf("canonical marshal mentions the declaration a plan never made:\n%s", data)
	}
}

// TestDigestCoversTheCIDeclaration is the other half: once a plan does
// declare, the declaration is part of what the human approved, so it must
// be inside the digest the approval is tied to rather than a field an
// adapter could revise between gate and activation.
func TestDigestCoversTheCIDeclaration(t *testing.T) {
	silent := mustDecodePlan(t, validPlanJSON())
	declaring := mustDecodePlan(t, ciDeclarationPlanJSON())
	if err := declaring.Validate(testConfig()); err != nil {
		t.Fatalf("Validate: %v", err)
	}
	a, err := silent.Digest()
	if err != nil {
		t.Fatal(err)
	}
	b, err := declaring.Digest()
	if err != nil {
		t.Fatal(err)
	}
	if a == b {
		t.Error("declaring a test CI does not run left the digest unchanged; the approval would not cover it")
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
