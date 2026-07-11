package routing

import (
	"strings"

	"github.com/kninetimmy/orch/internal/manifest"
)

// This file holds the deterministic, single-sentence rationale and
// reason builders. Every result is non-empty so it satisfies manifest's
// non-empty RoutingRationale / Escalation.Reason / Outcome.Reason
// validation. Risk domains are always rendered in Domains() order and
// downgrade refusals name the specific gap, so the audit record reads
// the same for the same inputs.

// renderDomains lists the present risk domains in canonical Domains()
// order, de-duplicated, comma-separated.
func renderDomains(ds []RiskDomain) string {
	present := make(map[RiskDomain]bool, len(ds))
	for _, d := range ds {
		present[d] = true
	}
	var names []string
	for _, d := range Domains() {
		if present[d] {
			names = append(names, string(d))
		}
	}
	return strings.Join(names, ", ")
}

// joinClauses joins reason clauses into one grammatical list.
func joinClauses(parts []string) string {
	switch len(parts) {
	case 0:
		return ""
	case 1:
		return parts[0]
	case 2:
		return parts[0] + " and " + parts[1]
	default:
		return strings.Join(parts[:len(parts)-1], ", ") + ", and " + parts[len(parts)-1]
	}
}

// escalationReasons collects the reasons a task escaped the plain route:
// risk domains (in canonical order), difficulty, and a failed model of
// the named role.
func escalationReasons(t Task, h History, failedRole string, failedModel string) []string {
	var reasons []string
	if len(t.RiskDomains) > 0 {
		reasons = append(reasons, "risk domains ("+renderDomains(t.RiskDomains)+")")
	}
	if t.UnusuallyDifficult {
		reasons = append(reasons, "the task is unusually difficult")
	}
	if h.FailedModel(failedModel) {
		reasons = append(reasons, "the "+failedRole+" model previously failed")
	}
	return reasons
}

func rationaleScoutPlain() string {
	return "Routed read-only work to a scout with no elevated risk or difficulty."
}

func rationaleScoutEscalated(t Task, h History, p Profile) string {
	reasons := escalationReasons(t, h, "scout", p.Scout.Model)
	return "Routed read-only work to a scout on the specialist model in read-only mode because " + joinClauses(reasons) + "."
}

func rationaleSpecialist(t Task, h History, p Profile) string {
	reasons := escalationReasons(t, h, "implementer", p.Implementer.Model)
	base := "Routed to a specialist with a strong reviewer because " + joinClauses(reasons)
	if t.Downgrade.Eligible() {
		base += ", overriding the reviewer downgrade the change would otherwise qualify for"
	}
	return base + "."
}

func rationaleDowngrade() string {
	return "Routed to an implementer with a downgraded reviewer because the change is affirmatively mechanical, low-risk, fully specified, and unsurprising."
}

func rationaleImplementer(t Task) string {
	if t.Downgrade.requested() {
		return "Routed to an implementer with a strong reviewer, declining the reviewer downgrade because the change is " + firstMissingFact(t.Downgrade) + "."
	}
	return "Routed to an implementer with a strong reviewer with no reviewer downgrade requested."
}

// firstMissingFact names the first affirmative downgrade fact the caller
// did not assert, in the canonical mechanical/low-risk/specified/
// unsurprising order, so a refusal always names one concrete gap.
func firstMissingFact(f DowngradeFacts) string {
	switch {
	case !f.Mechanical:
		return "not affirmatively mechanical"
	case !f.LowRisk:
		return "not affirmatively low-risk"
	case !f.FullySpecified:
		return "not affirmatively fully specified"
	case !f.Unsurprising:
		return "not affirmatively unsurprising"
	default:
		return "affirmatively downgrade-eligible"
	}
}

// rationaleReroute describes the executor selection after an escalation,
// for the rerouted Decision's RoutingRationale.
func rationaleReroute(newRole manifest.Role, tr Trigger) string {
	switch tr {
	case TriggerScoutUncertainty:
		return "Re-routed read-only work to a scout on the specialist model in read-only mode after scout uncertainty."
	case TriggerImplementerHardExecution:
		return "Re-routed to a specialist after the implementer hit unusually hard execution."
	case TriggerWeakModelFailure:
		if newRole == manifest.RoleScout {
			return "Re-routed read-only work to a scout on the specialist model in read-only mode after a weak-model failure."
		}
		return "Re-routed to a specialist after a weak-model failure on the implementer."
	default:
		return "Re-routed to a stronger executor after escalation."
	}
}

func rationaleReviewerEscalated() string {
	return "Escalated to a full strong-model review after the downgraded reviewer reported uncertainty."
}

// withDetail terminates a reason sentence, appending the caller's detail
// in parentheses when present. The base is always non-empty, so the
// result is too.
func withDetail(base, detail string) string {
	if detail == "" {
		return base + "."
	}
	return base + " (" + detail + ")."
}

func reasonExecutorReroute(fromRole manifest.Role, tr Trigger, detail string) string {
	var base string
	switch tr {
	case TriggerScoutUncertainty:
		base = "Scout reported uncertainty; escalated to the specialist model in read-only mode"
	case TriggerImplementerHardExecution:
		base = "Implementer hit unusually hard execution; transferred the worktree to a specialist"
	case TriggerWeakModelFailure:
		base = "Weak-model failure on the " + string(fromRole) + "; escalated to the next model tier"
	default:
		base = "Escalated to a stronger executor model"
	}
	return withDetail(base, detail)
}

func reasonReviewerRestored(detail string) string {
	return withDetail("Restored the strong reviewer because a task requiring escalation is, by evidence, no longer unsurprising", detail)
}

func reasonReviewerUncertainty(detail string) string {
	return withDetail("Downgraded reviewer reported uncertainty; escalated to a full strong-model review", detail)
}

func reasonExecutorExhausted(role manifest.Role, detail string) string {
	return withDetail("No stronger executor model remains after the "+string(role)+" failed; returning to the Architect", detail)
}

func reasonSpecialistExhausted(detail string) string {
	return withDetail("Specialist execution failed and no stronger executor model remains; returning to the Architect", detail)
}

func reasonReviewerDegenerate(detail string) string {
	return withDetail("Strong review is unavailable because the reviewer and downgrade profiles share a model; returning to the Architect", detail)
}

func reasonArchitecturalAmbiguity(detail string) string {
	return withDetail("Unresolved architectural ambiguity; returning to the Architect", detail)
}
