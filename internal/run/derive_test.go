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

// TestEffortDelivery pins the per-host mechanism the audit record names:
// Codex applies the routed effort as a real parameter, Claude only as a
// prompt cue, and an unrecognized host is an error rather than a guess.
func TestEffortDelivery(t *testing.T) {
	cases := map[string]manifest.EffortDelivery{
		"codex":  manifest.EffortDeliveryParameter,
		"claude": manifest.EffortDeliveryPromptCue,
	}
	for host, want := range cases {
		got, err := effortDelivery(host)
		if err != nil {
			t.Fatalf("effortDelivery(%s): %v", host, err)
		}
		if got != want {
			t.Errorf("effortDelivery(%s) = %q, want %q", host, got, want)
		}
	}
	if _, err := effortDelivery("fable"); err == nil {
		t.Error("effortDelivery accepted an unknown host")
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

// TestIssueAcceptanceCriteriaFollowsRiskDomains pins the trigger and the
// position: a risk-domain issue's list is the plan's plus
// BlastRadiusCriterion last, and an issue declaring no domain gets
// exactly what the plan listed and nothing more.
func TestIssueAcceptanceCriteriaFollowsRiskDomains(t *testing.T) {
	plain := PlanIssue{AcceptanceCriteria: []string{"one", "two"}}
	got := issueAcceptanceCriteria(plain)
	if len(got) != 2 || got[0] != "one" || got[1] != "two" {
		t.Errorf("no risk domains = %q, want exactly the plan's two criteria", got)
	}

	risky := PlanIssue{
		AcceptanceCriteria: []string{"one", "two"},
		Facts:              PlanFacts{RiskDomains: []string{"data-integrity"}},
	}
	got = issueAcceptanceCriteria(risky)
	if len(got) != 3 {
		t.Fatalf("with a risk domain = %q, want the plan's two plus one contributed", got)
	}
	if got[0] != "one" || got[1] != "two" {
		t.Errorf("plan criteria = %q, want them preserved in order and unaltered", got[:2])
	}
	if got[2] != BlastRadiusCriterion {
		t.Errorf("contributed criterion = %q, want BlastRadiusCriterion last", got[2])
	}
}

// TestIssueAcceptanceCriteriaDoesNotMutatePlan is the digest guard:
// PlanDoc.Digest marshals the submitted plan struct, so a contributed
// criterion that leaked into the plan's own slice would make `orch run
// plan` and `orch run activate` compute different digests for the same
// document — the failure ActivationRequest's approval check would then
// report as a plan the human never approved.
func TestIssueAcceptanceCriteriaDoesNotMutatePlan(t *testing.T) {
	p, err := DecodePlan([]byte(twoIssuePlanJSON()))
	if err != nil {
		t.Fatal(err)
	}
	before, err := p.Digest()
	if err != nil {
		t.Fatal(err)
	}
	for _, iss := range p.Issues {
		_ = issueAcceptanceCriteria(iss)
	}
	after, err := p.Digest()
	if err != nil {
		t.Fatal(err)
	}
	if before != after {
		t.Errorf("digest changed after reading criteria: %q -> %q", before, after)
	}
	// The risky issue's own slice is the plan's, untouched: reading it a
	// second time must not accumulate a second copy.
	if b := p.Issues[1]; len(b.AcceptanceCriteria) != 1 || b.AcceptanceCriteria[0] != "B works" {
		t.Errorf("plan issue b criteria = %q, want the submitted list untouched", b.AcceptanceCriteria)
	}
}

// TestBlastRadiusCriterionStatesItsThreeDemands pins the criterion's
// substance, not just its presence: internal/adaptertest holds both
// hosts' shipped prose to these same clauses, so a reworded criterion
// that dropped one would otherwise ship as a silently weaker standard.
func TestBlastRadiusCriterionStatesItsThreeDemands(t *testing.T) {
	for _, clause := range []string{
		"contributed by Orch because this issue declares a risk domain, not by the plan document",
		"name every element of the structure this change touches and state, for each, whether the behavior it had before this change still holds",
		"record a behavior this change removes as a before-and-after in the same document that stated the old behavior, rather than deleting that statement",
		"where a restriction is attributed to one named symbol, establish whether it holds for that symbol alone or for every symbol of its kind, and say which",
	} {
		if !strings.Contains(BlastRadiusCriterion, clause) {
			t.Errorf("BlastRadiusCriterion does not state %q", clause)
		}
	}
}

// TestReviewJudgesTheContributedCriterion proves the contributed
// criterion is enforced on exactly the terms every plan-supplied one is:
// Review counts the criteria from state (issue.AcceptanceCriteria), so a
// review that judges only the plan's is refused for missing coverage,
// and an approve verdict over the contributed criterion judged
// unsatisfied is refused as a self-contradiction.
func TestReviewJudgesTheContributedCriterion(t *testing.T) {
	criteria := issueAcceptanceCriteria(PlanIssue{
		AcceptanceCriteria: []string{"B works"},
		Facts:              PlanFacts{RiskDomains: []string{"data-integrity"}},
	})

	planOnly := []CriterionJudgment{{Criterion: 1, Judgment: JudgmentSatisfied, Reason: "read it"}}
	err := checkJudgments(planOnly, VerdictApprove, criteria)
	if !errors.Is(err, ErrBadRequest) {
		t.Fatalf("err = %v, want ErrBadRequest for the unjudged contributed criterion", err)
	}
	if !strings.Contains(err.Error(), "acceptance criterion 2 carries no judgment") {
		t.Errorf("err = %v, want it to name criterion 2", err)
	}

	unsatisfied := append(planOnly, CriterionJudgment{Criterion: 2, Judgment: JudgmentUnsatisfied, Reason: "no enumeration in the PR body"})
	if err := checkJudgments(unsatisfied, VerdictApprove, criteria); !errors.Is(err, ErrBadRequest) {
		t.Fatalf("err = %v, want ErrBadRequest refusing approve over an unsatisfied contributed criterion", err)
	}
	if err := checkJudgments(unsatisfied, VerdictRequestChanges, criteria); err != nil {
		t.Errorf("request-changes over an unsatisfied contributed criterion = %v, want accepted", err)
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
