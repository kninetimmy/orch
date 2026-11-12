package cli

import "encoding/json"

// These encoders expose the existing calculation results, not another metrics
// engine. In particular, array slots used internally become named counters and
// unmeasured subtotals/durations become null rather than a fabricated zero.
func (s runSummary) MarshalJSON() ([]byte, error) {
	return json.Marshal(map[string]any{
		"run_id": s.runID, "event_count": s.eventCount, "first_at": s.firstAt, "last_at": s.lastAt,
		"issues_seen": s.issuesSeen, "merged": s.merged, "abandoned": s.abandoned, "blocked": s.blocked,
		"block_classes": s.blockClasses, "escalations": s.escalations,
		"review_cycles": s.reviewCycles, "reviewed_issues": s.reviewedIssues, "first_pass_approve": s.firstPassApprove,
		"ci_by_state": s.ciByState, "legacy_usage": s.legacyUsage,
		"usage_samples": s.usageSamples, "usage_groups": s.usageGroups,
		"recorded_usage_roles": s.recordedUsageRoles, "missing_usage_roles": s.missingUsageRoles,
		"usage_sessions": s.usageSessions, "missing_usage_sessions": s.missingUsageSessions,
		"unknown_role_records": s.unknownRoleRecords, "incomplete_session_records": s.incompleteSessionRecords,
		"unavailable": s.unavailable, "timing": s.timing,
		"reported_outcomes": s.reportedOutcomes, "engine_outcomes": s.engineOutcomes,
	})
}

func (u usageEventRow) MarshalJSON() ([]byte, error) {
	return json.Marshal(map[string]any{
		"index": u.index, "verb": u.verb, "issue_number": u.issueNumber,
		"role": u.role, "review_cycle": u.reviewCycle, "usage": u.usage,
	})
}

func (u usageSampleRow) MarshalJSON() ([]byte, error) {
	return json.Marshal(map[string]any{"observation": u.observation, "counters": u.counters})
}

func (k usageGroupKey) MarshalJSON() ([]byte, error) {
	return json.Marshal(map[string]string{"host": k.host, "source": k.source, "stream": k.stream})
}

var counterNames = [6]string{
	"input_tokens", "output_tokens", "cache_read_tokens", "cache_creation_tokens", "total_tokens", "reasoning_output_tokens",
}

type counterMeasurement struct {
	Total    *int64 `json:"total"`
	Measured int    `json:"measured"`
}

func counterMeasurements(totals [6]int64, measured [6]int) map[string]counterMeasurement {
	result := make(map[string]counterMeasurement, len(counterNames))
	for i, name := range counterNames {
		m := counterMeasurement{Measured: measured[i]}
		if measured[i] > 0 {
			m.Total = &totals[i]
		}
		result[name] = m
	}
	return result
}

func (g usageGroup) MarshalJSON() ([]byte, error) {
	return json.Marshal(map[string]any{
		"key": g.key, "samples": g.samples, "counters": counterMeasurements(g.totals, g.measured),
	})
}

func (t timingSummary) MarshalJSON() ([]byte, error) {
	var sessionNS *int64
	if t.incompleteSessions < t.intervals {
		n := int64(t.sessionEffort)
		sessionNS = &n
	}
	return json.Marshal(map[string]any{
		"intervals": t.intervals, "incomplete_sessions": t.incompleteSessions,
		"session_effort_ns": sessionNS, "wall_clock_ns": int64(t.wallClock),
	})
}

func (r metricsReport) MarshalJSON() ([]byte, error) {
	total, measured := r.elapsedSummary()
	var totalNS *int64
	if measured > 0 {
		totalNS = new(int64(total))
	}
	return json.Marshal(map[string]any{
		"runs": r.runs, "routes": r.routes, "dimensions": r.dimensions, "usage": r.usage,
		"runs_without_arch_use": r.runsWithoutArchUse, "elapsed": r.elapsed,
		"escapes": r.escapes, "unattributed": r.unattributed,
		"elapsed_total_ns": totalNS, "elapsed_measured": measured,
	})
}

func (k routeKey) MarshalJSON() ([]byte, error) {
	return json.Marshal(map[string]string{
		"role": k.role, "executor": k.executor, "reviewer": k.reviewer,
		"downgraded": k.downgraded, "approval": k.approval,
	})
}

func (o outcomes) MarshalJSON() ([]byte, error) {
	return json.Marshal(map[string]any{
		"issues": o.issues, "merged": o.merged, "abandoned": o.abandoned, "blocked": o.blocked,
		"escalations": o.escalations, "reviewed": o.reviewed, "first_approved": o.firstApproved,
		"review_cycles": o.reviewCycles, "merge_approval": o.mergeApproval,
		"fix_cycles": o.fixCycles, "review_blocks": o.reviewBlocks, "cause_unrecorded": o.causeUnrecorded,
		"escaped_defects":                  o.escapedDefects,
		"review_cycles_per_reviewed_issue": o.cyclesPerReviewedIssue(),
	})
}

func (g routeGroup) MarshalJSON() ([]byte, error) {
	return json.Marshal(map[string]any{"key": g.key, "outcomes": g.outcomes})
}

func (g dimensionGroup) MarshalJSON() ([]byte, error) {
	return json.Marshal(map[string]any{"value": g.value, "outcomes": g.outcomes})
}

func (d dimensionReport) MarshalJSON() ([]byte, error) {
	return json.Marshal(map[string]any{"title": d.title, "groups": d.groups})
}

func (g reportUsageGroup) MarshalJSON() ([]byte, error) {
	perMerged, architectShare := map[string]*float64{}, map[string]*float64{}
	for i, name := range counterNames {
		perMerged[name] = g.perMergedIssue(i)
		architectShare[name] = g.architectSharePercent(i)
	}
	return json.Marshal(map[string]any{
		"key": g.key.usageGroupKey, "lifecycle": g.key.lifecycle,
		"samples": g.samples, "runs": g.runs, "merged_runs": g.mergedRuns,
		"counters":           counterMeasurements(g.totals, g.measured),
		"architect_counters": counterMeasurements(g.archTotals, g.archSeen),
		"per_merged_issue":   perMerged, "architect_share_percent": architectShare,
	})
}

func (e runElapsed) MarshalJSON() ([]byte, error) {
	var ns *int64
	if e.measured {
		n := int64(e.elapsed)
		ns = &n
	}
	return json.Marshal(map[string]any{"run_id": e.runID, "elapsed_ns": ns, "measured": e.measured})
}

func (e escapedDefect) MarshalJSON() ([]byte, error) {
	f := e.origin
	var cycles *int
	if f.firstVerdict != "" {
		cycles = &f.cycles
	}
	return json.Marshal(map[string]any{
		"pr": e.pr, "fixed_by_run": e.fixedByRun, "fixed_by_issue": e.fixedByIssue,
		"origin_run": e.originRun, "origin_issue": e.originIssue,
		"origin": map[string]any{
			"route":         routeKey{f.role, f.executor, f.reviewer, f.downgraded, f.approval},
			"first_verdict": orUnknown(f.firstVerdict), "review_cycles": cycles, "merge_approval": f.mergeSource,
		},
	})
}
