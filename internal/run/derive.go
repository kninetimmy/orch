package run

import (
	"fmt"
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
	var h *config.Host
	switch host {
	case "claude":
		h = cfg.Hosts.Claude
	case "codex":
		h = cfg.Hosts.Codex
	}
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
