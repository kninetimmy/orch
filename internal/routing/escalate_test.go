package routing

import (
	"errors"
	"testing"

	"github.com/kninetimmy/orch/internal/manifest"
)

// scoutDecision, implementerDecision, specialistDecision, and
// downgradedDecision build the canonical Decide outputs the escalation
// triggers act on.
func scoutDecision(t *testing.T, p Profile) Decision {
	t.Helper()
	d, err := Decide(p, Task{ReadOnly: true}, nil)
	if err != nil {
		t.Fatalf("scout decision: %v", err)
	}
	return d
}

func implementerDecision(t *testing.T, p Profile) Decision {
	t.Helper()
	d, err := Decide(p, Task{}, nil)
	if err != nil {
		t.Fatalf("implementer decision: %v", err)
	}
	return d
}

func specialistDecision(t *testing.T, p Profile) Decision {
	t.Helper()
	d, err := Decide(p, Task{UnusuallyDifficult: true}, nil)
	if err != nil {
		t.Fatalf("specialist decision: %v", err)
	}
	return d
}

func downgradedDecision(t *testing.T, p Profile) Decision {
	t.Helper()
	d, err := Decide(p, Task{Downgrade: DowngradeFacts{Mechanical: true, LowRisk: true, FullySpecified: true, Unsurprising: true}}, nil)
	if err != nil {
		t.Fatalf("downgraded decision: %v", err)
	}
	if !d.ReviewerDowngraded {
		t.Fatalf("expected a downgraded reviewer decision, got %+v", d)
	}
	return d
}

func TestEscalateScoutUncertainty(t *testing.T) {
	p := testProfile()
	d := scoutDecision(t, p)
	out, err := Escalate(p, d, nil, TriggerScoutUncertainty, "cannot resolve read-only")
	if err != nil {
		t.Fatalf("Escalate: %v", err)
	}
	if out.Kind != OutcomeReroute {
		t.Fatalf("kind = %q, want reroute", out.Kind)
	}
	if out.Decision.Role != manifest.RoleScout || out.Decision.Executor != p.Specialist {
		t.Errorf("decision role=%q executor=%+v, want scout on specialist model", out.Decision.Role, out.Decision.Executor)
	}
	if len(out.Escalations) != 1 {
		t.Fatalf("escalations = %d, want 1", len(out.Escalations))
	}
	if out.Escalations[0].From != p.Scout || out.Escalations[0].To != p.Specialist {
		t.Errorf("escalation %+v, want scout->specialist", out.Escalations[0])
	}
	if !out.History.FailedModel(p.Scout.Model) {
		t.Error("returned history must retire the scout model")
	}
}

func TestEscalateImplementerHardExecution(t *testing.T) {
	p := testProfile()
	d := implementerDecision(t, p)
	out, err := Escalate(p, d, nil, TriggerImplementerHardExecution, "")
	if err != nil {
		t.Fatalf("Escalate: %v", err)
	}
	if out.Kind != OutcomeReroute {
		t.Fatalf("kind = %q, want reroute", out.Kind)
	}
	if out.Decision.Role != manifest.RoleSpecialist || out.Decision.Executor != p.Specialist {
		t.Errorf("decision role=%q executor=%+v, want specialist", out.Decision.Role, out.Decision.Executor)
	}
	if len(out.Escalations) != 1 {
		t.Fatalf("escalations = %d, want 1", len(out.Escalations))
	}
	if !out.History.FailedModel(p.Implementer.Model) {
		t.Error("returned history must retire the implementer model")
	}
}

func TestEscalateImplementerHardWithDowngradedReviewer(t *testing.T) {
	p := testProfile()
	d := downgradedDecision(t, p)
	out, err := Escalate(p, d, nil, TriggerImplementerHardExecution, "hidden invariant")
	if err != nil {
		t.Fatalf("Escalate: %v", err)
	}
	if out.Kind != OutcomeReroute {
		t.Fatalf("kind = %q, want reroute", out.Kind)
	}
	if out.Decision.ReviewerDowngraded {
		t.Error("reviewer must be restored to strong on escalation")
	}
	if out.Decision.Reviewer != p.Reviewer {
		t.Errorf("reviewer = %+v, want strong reviewer", out.Decision.Reviewer)
	}
	if len(out.Escalations) != 2 {
		t.Fatalf("escalations = %d, want 2 (executor + reviewer restore)", len(out.Escalations))
	}
	if out.Escalations[0].Role != manifest.RoleSpecialist {
		t.Errorf("first escalation role = %q, want specialist", out.Escalations[0].Role)
	}
	if out.Escalations[1].Role != manifest.RoleReviewer || out.Escalations[1].From != p.ReviewDowngrade || out.Escalations[1].To != p.Reviewer {
		t.Errorf("second escalation %+v, want downgrade->strong reviewer", out.Escalations[1])
	}
}

func TestEscalateWeakModelFailure(t *testing.T) {
	p := testProfile()
	tests := map[string]struct {
		decision func(*testing.T, Profile) Decision
		wantKind OutcomeKind
		wantRole manifest.Role
	}{
		"scout climbs to specialist model": {
			scoutDecision, OutcomeReroute, manifest.RoleScout,
		},
		"implementer climbs to specialist": {
			implementerDecision, OutcomeReroute, manifest.RoleSpecialist,
		},
		"specialist returns to architect": {
			specialistDecision, OutcomeReturnToArchitect, "",
		},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			d := tt.decision(t, p)
			out, err := Escalate(p, d, nil, TriggerWeakModelFailure, "weak model")
			if err != nil {
				t.Fatalf("Escalate: %v", err)
			}
			if out.Kind != tt.wantKind {
				t.Fatalf("kind = %q, want %q", out.Kind, tt.wantKind)
			}
			if out.Reason == "" {
				t.Error("reason must be non-empty")
			}
			if tt.wantKind == OutcomeReroute {
				if out.Decision.Role != tt.wantRole || out.Decision.Executor != p.Specialist {
					t.Errorf("decision role=%q executor=%+v, want %q on specialist", out.Decision.Role, out.Decision.Executor, tt.wantRole)
				}
			} else {
				if len(out.Escalations) != 0 {
					t.Errorf("return-to-architect must emit no escalations, got %d", len(out.Escalations))
				}
				if !out.History.FailedModel(p.Specialist.Model) {
					t.Error("returned history must retire the specialist model")
				}
			}
		})
	}
}

func TestEscalateReviewerUncertainty(t *testing.T) {
	p := testProfile()
	d := downgradedDecision(t, p)
	out, err := Escalate(p, d, nil, TriggerReviewerUncertainty, "unsure about acceptance")
	if err != nil {
		t.Fatalf("Escalate: %v", err)
	}
	if out.Kind != OutcomeReroute {
		t.Fatalf("kind = %q, want reroute", out.Kind)
	}
	if out.Decision.Reviewer != p.Reviewer || out.Decision.ReviewerDowngraded {
		t.Errorf("reviewer=%+v downgraded=%v, want strong reviewer, not downgraded", out.Decision.Reviewer, out.Decision.ReviewerDowngraded)
	}
	if out.Decision.Executor != p.Implementer {
		t.Errorf("executor = %+v, want unchanged implementer", out.Decision.Executor)
	}
	if len(out.Escalations) != 1 || out.Escalations[0].Role != manifest.RoleReviewer {
		t.Errorf("escalations = %+v, want one reviewer escalation", out.Escalations)
	}
}

func TestEscalateArchitecturalAmbiguityFromEveryRole(t *testing.T) {
	p := testProfile()
	decisions := map[string]Decision{
		"scout":       scoutDecision(t, p),
		"implementer": implementerDecision(t, p),
		"specialist":  specialistDecision(t, p),
		"downgraded":  downgradedDecision(t, p),
	}
	for name, d := range decisions {
		t.Run(name, func(t *testing.T) {
			out, err := Escalate(p, d, nil, TriggerArchitecturalAmbiguity, "contract is ambiguous")
			if err != nil {
				t.Fatalf("Escalate: %v", err)
			}
			if out.Kind != OutcomeReturnToArchitect {
				t.Errorf("kind = %q, want return-to-architect", out.Kind)
			}
			if len(out.Escalations) != 0 {
				t.Errorf("escalations = %d, want 0", len(out.Escalations))
			}
			if out.Reason == "" {
				t.Error("reason must be non-empty")
			}
		})
	}
}

func TestEscalateTriggerMismatch(t *testing.T) {
	p := testProfile()
	tests := map[string]struct {
		decision Decision
		trigger  Trigger
	}{
		"scout-uncertainty on implementer": {
			implementerDecision(t, p), TriggerScoutUncertainty,
		},
		"implementer-hard on scout": {
			scoutDecision(t, p), TriggerImplementerHardExecution,
		},
		"reviewer-uncertainty on non-downgraded decision": {
			implementerDecision(t, p), TriggerReviewerUncertainty,
		},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			_, err := Escalate(p, tt.decision, nil, tt.trigger, "")
			if !errors.Is(err, ErrTriggerMismatch) {
				t.Errorf("err = %v, want ErrTriggerMismatch", err)
			}
		})
	}
}

func TestEscalateUnknownTrigger(t *testing.T) {
	p := testProfile()
	_, err := Escalate(p, implementerDecision(t, p), nil, Trigger("meltdown"), "")
	if !errors.Is(err, ErrBadTrigger) {
		t.Errorf("err = %v, want ErrBadTrigger", err)
	}
}

func TestEscalateBadProfile(t *testing.T) {
	p := testProfile()
	d := implementerDecision(t, p)
	p.Specialist = manifest.Selection{} // corrupt after building the decision
	_, err := Escalate(p, d, nil, TriggerImplementerHardExecution, "")
	if !errors.Is(err, ErrBadProfile) {
		t.Errorf("err = %v, want ErrBadProfile", err)
	}
}

// TestEscalateDoubleClosesToArchitect round-trips the History from a
// first escalation into a second and confirms the ladder closes at the
// Architect.
func TestEscalateDoubleClosesToArchitect(t *testing.T) {
	p := testProfile()

	t.Run("scout uncertainty twice", func(t *testing.T) {
		d := scoutDecision(t, p)
		first, err := Escalate(p, d, nil, TriggerScoutUncertainty, "still unsure")
		if err != nil {
			t.Fatalf("first Escalate: %v", err)
		}
		if first.Kind != OutcomeReroute {
			t.Fatalf("first kind = %q, want reroute", first.Kind)
		}
		second, err := Escalate(p, first.Decision, first.History, TriggerScoutUncertainty, "specialist also unsure")
		if err != nil {
			t.Fatalf("second Escalate: %v", err)
		}
		if second.Kind != OutcomeReturnToArchitect {
			t.Errorf("second kind = %q, want return-to-architect", second.Kind)
		}
		if len(second.Escalations) != 0 {
			t.Errorf("closing outcome must emit no escalations, got %d", len(second.Escalations))
		}
	})

	t.Run("weak model failure implementer then specialist", func(t *testing.T) {
		d := implementerDecision(t, p)
		first, err := Escalate(p, d, nil, TriggerWeakModelFailure, "impl too weak")
		if err != nil {
			t.Fatalf("first Escalate: %v", err)
		}
		second, err := Escalate(p, first.Decision, first.History, TriggerWeakModelFailure, "spec too weak")
		if err != nil {
			t.Fatalf("second Escalate: %v", err)
		}
		if second.Kind != OutcomeReturnToArchitect {
			t.Errorf("second kind = %q, want return-to-architect", second.Kind)
		}
	})
}

func TestEscalateDegenerateProfiles(t *testing.T) {
	t.Run("specialist model equals implementer returns to architect", func(t *testing.T) {
		p := testProfile()
		p.Specialist = p.Implementer
		d := implementerDecision(t, p)
		out, err := Escalate(p, d, nil, TriggerImplementerHardExecution, "hard")
		if err != nil {
			t.Fatalf("Escalate: %v", err)
		}
		if out.Kind != OutcomeReturnToArchitect {
			t.Errorf("kind = %q, want return-to-architect (no stronger model)", out.Kind)
		}
	})

	t.Run("reviewer equals downgrade returns to architect", func(t *testing.T) {
		p := testProfile()
		p.ReviewDowngrade = p.Reviewer
		d := downgradedDecision(t, p)
		out, err := Escalate(p, d, nil, TriggerReviewerUncertainty, "unsure")
		if err != nil {
			t.Fatalf("Escalate: %v", err)
		}
		if out.Kind != OutcomeReturnToArchitect {
			t.Errorf("kind = %q, want return-to-architect (no stronger reviewer)", out.Kind)
		}
	})
}

// TestEscalateManifestRoundTrip is the end-to-end proof: every emitted
// escalation, embedded in a manifest.Manifest built from the rerouted
// Decision, passes manifest.Render.
func TestEscalateManifestRoundTrip(t *testing.T) {
	p := testProfile()
	reroutes := map[string]struct {
		decision Decision
		trigger  Trigger
	}{
		"scout uncertainty":          {scoutDecision(t, p), TriggerScoutUncertainty},
		"implementer hard":           {implementerDecision(t, p), TriggerImplementerHardExecution},
		"weak model implementer":     {implementerDecision(t, p), TriggerWeakModelFailure},
		"implementer hard downgrade": {downgradedDecision(t, p), TriggerImplementerHardExecution},
		"reviewer uncertainty":       {downgradedDecision(t, p), TriggerReviewerUncertainty},
	}
	for name, tt := range reroutes {
		t.Run(name, func(t *testing.T) {
			out, err := Escalate(p, tt.decision, nil, tt.trigger, "detail for the record")
			if err != nil {
				t.Fatalf("Escalate: %v", err)
			}
			if out.Kind != OutcomeReroute {
				t.Fatalf("kind = %q, want reroute", out.Kind)
			}
			m := manifest.Manifest{
				SchemaVersion:    manifest.SchemaVersion,
				Role:             out.Decision.Role,
				Executor:         out.Decision.Executor,
				RoutingRationale: out.Decision.Rationale,
				Reviewer:         out.Decision.Reviewer,
				Escalations:      out.Escalations,
				ConfigRevision:   "cfg-2026-07-11",
			}
			if _, err := manifest.Render(m); err != nil {
				t.Fatalf("manifest.Render on escalated decision: %v", err)
			}
		})
	}
}
