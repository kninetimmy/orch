package run

import (
	"fmt"
	"slices"
	"sort"

	"github.com/kninetimmy/orch/internal/config"
	"github.com/kninetimmy/orch/internal/ghops"
	"github.com/kninetimmy/orch/internal/manifest"
	"github.com/kninetimmy/orch/internal/routing"
)

// hostProfile maps cfg's per-host RoleProfile set (PRD §10) to the
// routing.Profile the pure routing package consumes. config owns the
// model/effort vocabulary; routing never sees a config.Host directly.
func hostProfile(cfg *config.Config, host string) (routing.Profile, error) {
	h := cfg.Host(host)
	if h == nil {
		return routing.Profile{}, fmt.Errorf("host %q is not enabled in configuration", host)
	}
	sel := func(rp config.RoleProfile) manifest.Selection {
		return manifest.Selection{Model: rp.Model, Effort: rp.Effort}
	}
	return routing.Profile{
		Architect:       sel(h.Roles.Architect),
		Scout:           sel(h.Roles.Scout),
		Implementer:     sel(h.Roles.Implementer),
		Specialist:      sel(h.Roles.Specialist),
		Reviewer:        sel(h.Roles.Reviewer),
		ReviewDowngrade: sel(h.Roles.ReviewDowngrade),
	}, nil
}

// effortDelivery reports how host actually applies a routed reasoning
// effort to a spawned executor, for the audit record. Codex pins
// model_reasoning_effort in the dispatched agent's own TOML, so the
// routed effort is a real parameter there; Claude Code subagent spawns
// take no effort knob, so it reaches the executor only as a prompt cue.
// An unknown host is an error rather than a guess: recording an effort
// without saying how it was delivered is the claim this field exists to
// stop making.
func effortDelivery(host string) (manifest.EffortDelivery, error) {
	switch host {
	case "codex", "opencode":
		return manifest.EffortDeliveryParameter, nil
	case "claude":
		return manifest.EffortDeliveryPromptCue, nil
	default:
		return "", fmt.Errorf("host %q has no recorded effort-delivery mechanism", host)
	}
}

// issueTask converts a plan issue's facts into the routing.Task Decide
// consumes.
func issueTask(i PlanIssue) routing.Task {
	domains := make([]routing.RiskDomain, len(i.Facts.RiskDomains))
	for idx, d := range i.Facts.RiskDomains {
		domains[idx] = routing.RiskDomain(d)
	}
	return routing.Task{
		ReadOnly:           i.Facts.ReadOnly,
		UnusuallyDifficult: i.Facts.UnusuallyDifficult,
		RiskDomains:        domains,
		Downgrade: routing.DowngradeFacts{
			Mechanical:     i.Facts.Downgrade.Mechanical,
			LowRisk:        i.Facts.Downgrade.LowRisk,
			FullySpecified: i.Facts.Downgrade.FullySpecified,
			Unsurprising:   i.Facts.Downgrade.Unsurprising,
		},
	}
}

// modelDenylist returns every configured model name across enabled
// hosts (post-overlay), deduplicated and sorted. It is the
// ghops.Labels.Validate forbidden-areas argument: PRD §13's "models
// never become GitHub labels" as a mechanical check.
func modelDenylist(cfg *config.Config) []string {
	seen := map[string]bool{}
	add := func(h *config.Host) {
		if h == nil {
			return
		}
		for _, rp := range []config.RoleProfile{
			h.Roles.Architect, h.Roles.Scout, h.Roles.Implementer,
			h.Roles.Specialist, h.Roles.Reviewer, h.Roles.ReviewDowngrade,
		} {
			if rp.Model != "" {
				seen[rp.Model] = true
			}
		}
	}
	add(cfg.Hosts.Claude)
	add(cfg.Hosts.Codex)
	add(cfg.Hosts.OpenCode)
	names := make([]string, 0, len(seen))
	for m := range seen {
		names = append(names, m)
	}
	sort.Strings(names)
	return names
}

// decideIssue derives the routing decision for one plan issue against
// profile, starting from a fresh (nil) History: plan-time routing has
// no prior attempts. ErrNoStrongerRoute propagates unwrapped — an
// exhausted initial route is a plan-gate problem, not a validation-
// table entry the caller should fold into ErrPlanInvalid.
func decideIssue(profile routing.Profile, i PlanIssue) (routing.Decision, error) {
	return routing.Decide(profile, issueTask(i), nil)
}

// deriveRisk derives PRD §11's risk classification from facts alone:
// critical iff the issue names any risk domain, standard otherwise.
// There is no independent "risk" field that could contradict it.
func deriveRisk(i PlanIssue) ghops.Risk {
	if len(i.Facts.RiskDomains) > 0 {
		return ghops.RiskCritical
	}
	return ghops.RiskStandard
}

// BlastRadiusCriterion is the one acceptance criterion the engine
// contributes itself, appended to every plan issue that declares a risk
// domain (issueAcceptanceCriteria). A plan document's criteria are the
// whole standard the executor optimizes against and the reviewer is
// measured against — checkJudgments requires one judgment per criterion
// and refuses an approve verdict over any unsatisfied one — so a
// preservation clause naming one thing to protect protects that one
// thing and nothing adjacent to it. This criterion is what puts
// "account for what the change actually touched" into that standard
// rather than leaving it to what the planner happened to enumerate.
//
// It is a package-level constant, not prose duplicated per call site,
// because internal/adaptertest pins both hosts' delivery skills and all
// four shipped reviewer definitions against this exact text: the
// engine's demand and the shipped instructions describing it cannot
// drift apart without failing a test.
const BlastRadiusCriterion = "Blast radius (contributed by Orch because this issue declares a risk domain, not by the plan document): name every element of the structure this change touches and state, for each, whether the behavior it had before this change still holds; record a behavior this change removes as a before-and-after in the same document that stated the old behavior, rather than deleting that statement; and where a restriction is attributed to one named symbol, establish whether it holds for that symbol alone or for every symbol of its kind, and say which."

// issueAcceptanceCriteria is one issue's full approved criteria list:
// the plan document's entries, plus BlastRadiusCriterion last when the
// issue declares at least one risk domain. It is the single source every
// consumer of a plan issue's criteria reads — the gate document, run
// state, the audit record, and the created issue body — so dispatch,
// review, and resume, which all read one of those, see the same list
// without knowing this function exists. An issue declaring no risk
// domain gets exactly what its plan document listed.
//
// Risk domains are the trigger for the same reason deriveRisk uses them:
// they are declared facts, already computed, and already what routes an
// issue to the specialist role.
//
// It never mutates i, and appends into a fresh slice rather than onto
// the plan's own backing array: PlanDoc.Digest marshals the submitted
// plan struct, so a criterion that reached i.AcceptanceCriteria would
// make `orch run plan` and `orch run activate` compute different digests
// for the same document.
func issueAcceptanceCriteria(i PlanIssue) []string {
	if len(i.Facts.RiskDomains) == 0 {
		return i.AcceptanceCriteria
	}
	return slices.Concat(i.AcceptanceCriteria, []string{BlastRadiusCriterion})
}

// issueLabels assembles the full PRD §13 taxonomy for one issue: ready
// status, the plan's type, the routing decision's role, the derived
// risk, and the plan's area labels.
func issueLabels(i PlanIssue, d routing.Decision) ghops.Labels {
	role := ghops.RoleImplementer
	if d.Role == manifest.RoleSpecialist {
		role = ghops.RoleSpecialist
	}
	return ghops.Labels{
		Status: ghops.StatusReady,
		Type:   ghops.Type(i.Type),
		Role:   role,
		Risk:   deriveRisk(i),
		Areas:  i.AreaLabels,
	}
}

// flattenLabels mirrors ghops's internal label flattening order
// (status, type, role, risk, then areas) for the gate document's
// labels preview, without depending on ghops's unexported flatten.
func flattenLabels(l ghops.Labels) []string {
	return append([]string{string(l.Status), string(l.Type), string(l.Role), string(l.Risk)}, l.Areas...)
}
