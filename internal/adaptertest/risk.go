package adaptertest

import "testing"

// CheckProportionateRiskGuidance checks the planning, execution and verdict
// instructions shared by all hosts, not how a model follows them. Existing
// review checks still pin criterion judgments, required-test/security grounds
// and the evidence protocol.
func CheckProportionateRiskGuidance(t *testing.T, agentExtension string) {
	t.Helper()
	architect := "skills/orch-architect/SKILL.md"
	delivery := "skills/orch-delivery/SKILL.md"
	checkPhrases(t, "planning blocker bar", []string{
		"concrete trigger, who or what is affected, plausible impact and recoverability",
		"possibility alone is not a blocker",
		"a credible rare path to serious loss can justify stopping",
		"With approved criteria and required checks satisfied and security rules intact, carry advisory residual risk forward without expanding scope or another fix cycle",
		"Reopen an accepted or materially addressed tradeoff only with new evidence that changes the assessment",
	}, []string{architect, delivery})
	checkPhrases(t, "independent verdict boundary", []string{
		"your risk judgment never replaces the reviewer's verdict",
	}, []string{architect})
	checkPhrases(t, "independent verdict boundary", []string{
		"Preserve the independent reviewer's verdict and judgments",
		"ask that reviewer rather than silently rewriting its verdict",
		"reviewer uncertainty that could change the approved outcome",
	}, []string{delivery})

	checkPhrases(t, "executor risk guidance", []string{
		"Report residual risk without expanding scope for advisory edge cases",
		"If a material risk changes the approved outcome, pause and escalate it to the Architect",
		"concrete trigger, plausible impact and recoverability",
	}, []string{
		"agents/orch-implementer" + agentExtension,
		"agents/orch-specialist" + agentExtension,
	})

	checkPhrases(t, "review risk guidance", []string{
		"this stage is about coverage, not filtering",
		"Report low-severity findings and anything you are uncertain about",
		"Give each finding a severity and a confidence",
		"Decide the verdict after the findings are listed, as a separate judgment",
		"concrete trigger, who or what is affected, plausible impact and recoverability",
		"possibility alone is not a blocker",
		"A remote refresh race that leaves a stale display, with no lost data and an easy retry, is advisory",
		"A credible rare race that can delete the user's only copy of data is a verdict ground even if tests pass",
		"Report nits, preferences and other advisory findings without requiring code changes or another review cycle",
		"Reopen an accepted or materially addressed tradeoff only with new evidence that changes the assessment",
		"Acceptance criteria, required checks, security rules and independent review remain mandatory",
	}, []string{
		"agents/orch-reviewer" + agentExtension,
		"agents/orch-reviewer-safe" + agentExtension,
	})
}
