package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kninetimmy/orch/internal/manifest"
	"github.com/kninetimmy/orch/internal/metrics"
)

func TestMetricsReportNoHistoryCreatesNoStorage(t *testing.T) {
	env, stdout, _ := testEnv(t)
	writeConfig(t, env.RepoRoot, validTOML)
	if code := Run([]string{"metrics", "report"}, env); code != ExitOK {
		t.Fatalf("exit = %d, want %d", code, ExitOK)
	}
	out := stdout.String()
	if !strings.Contains(out, "metrics are disabled and none are recorded; no report to print.") || strings.Contains(out, "runs recorded") {
		t.Errorf("output = %q", out)
	}
	if _, err := os.Stat(filepath.Join(env.RepoRoot, filepath.FromSlash(metrics.Dir))); !os.IsNotExist(err) {
		t.Errorf("metrics dir exists after `orch metrics report` (stat err = %v), want absent", err)
	}
}

func TestMetricsReportRollsUpRunsWithCoverageDisclosed(t *testing.T) {
	env, stdout, _ := testEnv(t)
	writeConfig(t, env.RepoRoot, validTOML)
	value := func(v int64) *int64 { return &v }
	exec := &manifest.Selection{Model: "m1", Effort: "high"}
	rev := &manifest.Selection{Model: "m2", Effort: "medium"}
	sample := func(id, at, role, host string, tokens int64) metrics.Observation {
		return metrics.Observation{SchemaVersion: 2, RunID: "run-20260713T000000Z-aaaaaaaa", ID: id, At: at, Source: "native",
			Role: role, Host: host, Session: id,
			Sample: &metrics.CounterSample{Stream: "tokens", Mode: "delta", Sequence: 1, Counters: metrics.Counters{TotalTokens: value(tokens)}}}
	}
	runA := metrics.Document{SchemaVersion: 2, RunID: "run-20260713T000000Z-aaaaaaaa",
		Observations: []metrics.Observation{
			sample("a-exec", "2026-07-13T00:03:00Z", "implementer", "codex", 600),
			sample("a-arch", "2026-07-13T00:03:30Z", "architect", "codex", 200),
			sample("a-claude", "2026-07-13T00:04:00Z", "implementer", "claude", 50),
		},
		Events: []metrics.Event{
			{At: "2026-07-13T00:00:00Z", Verb: "activate", IssueNumber: 1, Role: "implementer", Executor: exec, Reviewer: rev, ApprovalSource: "human"},
			{At: "2026-07-13T00:01:00Z", Verb: "pr-open", IssueNumber: 1, Role: "implementer", Usage: &metrics.Usage{TotalTokens: 90}},
			{At: "2026-07-13T00:02:00Z", Verb: "review", IssueNumber: 1, Verdict: "approve", ReviewCycles: 1},
			{At: "2026-07-13T00:03:00Z", Verb: "merge", IssueNumber: 1, ApprovalSource: "grant:g1"},
			{At: "2026-07-13T00:04:00Z", Verb: "activate", IssueNumber: 2, Role: "implementer", Executor: exec, Reviewer: rev, ReviewerDowngraded: true, ApprovalSource: "grant:g1"},
			{At: "2026-07-13T00:05:00Z", Verb: "block", IssueNumber: 2, BlockClass: "hook"},
			{At: "2026-07-13T00:06:00Z", Verb: "abandon", IssueNumber: 2},
		}}
	// A pre-approval-source run: nothing records who approved, so nothing
	// may be counted as human.
	runB := metrics.Document{SchemaVersion: 1, RunID: "run-20260714T000000Z-bbbbbbbb",
		Events: []metrics.Event{
			{At: "2026-07-14T00:00:00Z", Verb: "dispatch", IssueNumber: 3},
			{At: "2026-07-14T00:01:00Z", Verb: "review", IssueNumber: 3, Verdict: "request-changes", ReviewCycles: 1},
			{At: "2026-07-14T00:02:00Z", Verb: "escalate", IssueNumber: 3},
			{At: "2026-07-14T00:03:00Z", Verb: "review", IssueNumber: 3, Verdict: "approve", ReviewCycles: 2},
			{At: "2026-07-14T00:04:00Z", Verb: "merge", IssueNumber: 3},
		}}
	writeMetricsFixture(t, env.RepoRoot, runA.RunID, runA)
	writeMetricsFixture(t, env.RepoRoot, runB.RunID, runB)
	dir := filepath.Join(env.RepoRoot, filepath.FromSlash(metrics.Dir))
	before := readDir(t, dir)

	if code := Run([]string{"metrics", "report"}, env); code != ExitOK {
		t.Fatalf("exit = %d, want %d\n%s", code, ExitOK, stdout.String())
	}
	if after := readDir(t, dir); after != before {
		t.Fatal("metrics report modified the metrics directory")
	}
	out := stdout.String()
	for _, want := range []string{
		"runs recorded: 2",
		"  role implementer; executor [model m1, effort high]; reviewer [model m2, effort medium]; reviewer downgraded no; approval at activation human\n" +
			"    issues 1; merged 1 (by merge approval: grant:g1: 1); abandoned 0; blocked 0; escalations 0; first-review approvals 1 of 1 reviewed; review cycles per reviewed issue 1.00",
		"reviewer downgraded yes; approval at activation grant:g1\n    issues 1; merged 0; abandoned 1; blocked 1; escalations 0; first-review approvals 0 of 0 reviewed; review cycles per reviewed issue unknown (no reviewed issues)",
		"  role unknown; executor [unknown]; reviewer [unknown]; reviewer downgraded unknown; approval at activation unknown\n" +
			"    issues 1; merged 1 (by merge approval: unknown: 1); abandoned 0; blocked 0; escalations 1; first-review approvals 0 of 1 reviewed; review cycles per reviewed issue 2.00",
		"  host claude; source native; stream tokens; 1 runs; 1 samples; 1 merged issues\n    input: unknown (0/1 measured)",
		"    total: 50 (1/1 measured); 50.0 per merged issue\n",
		"  host codex; source native; stream tokens; 1 runs; 2 samples; 1 merged issues",
		"    total: 800 (2/2 measured); 800.0 per merged issue; architect share 25.0% (200 of 800)",
		"  lifecycle-verb usage (host, source and stream not recorded); 1 runs; 1 samples; 1 merged issues",
		"    total: 90 (1/1 measured); 90.0 per merged issue\n",
		"architect usage unavailable: no architect-role counters in 1 of 2 runs",
		"run elapsed time (wall-clock from first to last recorded event, not active time):",
		"  run-20260713T000000Z-aaaaaaaa: 6m0s",
		"  run-20260714T000000Z-bbbbbbbb: 4m0s",
		"  sum of wall-clock times: 10m0s (2 of 2 runs measured)",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "approval at activation human\n    issues 2") {
		t.Errorf("an unrecorded approval was counted as human:\n%s", out)
	}
	t.Log("\n" + out)
}

func TestMetricsReportWithoutCountersOrArchitectSaysUnavailable(t *testing.T) {
	r, err := buildMetricsReport([]metrics.Document{{SchemaVersion: 1, RunID: "run-x",
		Events: []metrics.Event{{At: "not-a-time", Verb: "dispatch", IssueNumber: 1}}}})
	if err != nil {
		t.Fatal(err)
	}
	var out strings.Builder
	printMetricsReport(&out, r)
	for _, want := range []string{
		"none recorded; counters unknown",
		"architect usage unavailable: no architect-role counters in 1 of 1 runs",
		"  run-x: unknown (no parseable first and last event time)",
		"sum of wall-clock times: unknown (0 of 1 runs measured)",
	} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("output missing %q:\n%s", want, out.String())
		}
	}
}

// readDir returns a comparable snapshot of every file name and content in dir.
func readDir(t *testing.T, dir string) string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var b strings.Builder
	for _, e := range entries {
		data, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			t.Fatal(err)
		}
		b.WriteString(e.Name() + "\x00" + string(data) + "\x00")
	}
	return b.String()
}

func TestMetricsReportBreaksOutcomesDownByHostAndRoutingFacts(t *testing.T) {
	env, stdout, _ := testEnv(t)
	writeConfig(t, env.RepoRoot, validTOML)
	facts := func(difficult, mechanical bool, risk ...string) *metrics.RoutingFacts {
		return &metrics.RoutingFacts{RiskDomains: risk, UnusuallyDifficult: difficult, Mechanical: mechanical}
	}
	newRun := metrics.Document{SchemaVersion: 2, RunID: "run-20260715T000000Z-cccccccc", Host: "codex",
		Events: []metrics.Event{
			{At: "2026-07-15T00:00:00Z", Verb: "activate", IssueNumber: 1, RoutingFacts: facts(false, true)},
			{At: "2026-07-15T00:01:00Z", Verb: "review", IssueNumber: 1, Verdict: "request-changes", ReviewCycles: 1},
			{At: "2026-07-15T00:02:00Z", Verb: "merge", IssueNumber: 1, PRNumber: 5},
			{At: "2026-07-15T00:03:00Z", Verb: "activate", IssueNumber: 2, RoutingFacts: facts(true, false, "concurrency")},
			{At: "2026-07-15T00:04:00Z", Verb: "review", IssueNumber: 2, Verdict: "request-changes", ReviewCycles: 1, ReviewBlockCause: metrics.BlockCauseWrongCriterion},
			{At: "2026-07-15T00:05:00Z", Verb: "review", IssueNumber: 2, Verdict: "request-changes", ReviewCycles: 2, ReviewBlockCause: metrics.BlockCauseFixCycleLimit},
		}}
	oldRun := metrics.Document{SchemaVersion: 2, RunID: "run-20260714T000000Z-bbbbbbbb",
		Events: []metrics.Event{
			{At: "2026-07-14T00:00:00Z", Verb: "activate", IssueNumber: 3},
			{At: "2026-07-14T00:01:00Z", Verb: "review", IssueNumber: 3, Verdict: "request-changes", ReviewCycles: 1},
		}}
	writeMetricsFixture(t, env.RepoRoot, newRun.RunID, newRun)
	writeMetricsFixture(t, env.RepoRoot, oldRun.RunID, oldRun)

	if code := Run([]string{"metrics", "report"}, env); code != ExitOK {
		t.Fatalf("exit = %d, want %d\n%s", code, ExitOK, stdout.String())
	}
	out := stdout.String()
	for _, want := range []string{
		"issue outcomes by host:\n  host codex\n    issues 2;",
		"  host unknown\n    issues 1;",
		"request-changes fix cycles 1; reviews that blocked their issue 2 (wrong criterion 1, fix-cycle limit 1); request-changes reviews with block cause not recorded 0",
		"request-changes fix cycles 0; reviews that blocked their issue 0 (wrong criterion 0, fix-cycle limit 0); request-changes reviews with block cause not recorded 1",
		"issue outcomes by any risk domain declared:\n  any risk domain declared no\n    issues 1;",
		"  any risk domain declared yes\n    issues 1;",
		"  any risk domain declared unknown\n    issues 1;",
		"  unusually difficult yes\n",
		"  downgrade fact mechanical yes\n    issues 1; merged 1",
		"  downgrade fact unsurprising unknown\n    issues 1;",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q:\n%s", want, out)
		}
	}
}

func TestMetricsReportAttributesEscapedDefects(t *testing.T) {
	env, stdout, _ := testEnv(t)
	writeConfig(t, env.RepoRoot, validTOML)
	exec := &manifest.Selection{Model: "m1", Effort: "high"}
	rev := &manifest.Selection{Model: "m2", Effort: "medium"}
	origin := metrics.Document{SchemaVersion: 2, RunID: "run-20260715T000000Z-aaaaaaaa", Host: "claude",
		Events: []metrics.Event{
			{At: "2026-07-15T00:00:00Z", Verb: "activate", IssueNumber: 7, Role: "implementer", Executor: exec, Reviewer: rev, ReviewerDowngraded: true, ApprovalSource: "human"},
			{At: "2026-07-15T00:01:00Z", Verb: "review", IssueNumber: 7, Verdict: "request-changes", ReviewCycles: 1},
			{At: "2026-07-15T00:02:00Z", Verb: "review", IssueNumber: 7, Verdict: "approve", ReviewCycles: 2},
			{At: "2026-07-15T00:03:00Z", Verb: "merge", IssueNumber: 7, PRNumber: 30, ApprovalSource: "grant:g1"},
		}}
	// Both issues name PR 30, which counts once; PR 99 has no merge event.
	fixer := metrics.Document{SchemaVersion: 2, RunID: "run-20260716T000000Z-bbbbbbbb", Host: "claude",
		Events: []metrics.Event{
			{At: "2026-07-16T00:00:00Z", Verb: "activate", IssueNumber: 8, Fixes: []int{30, 99}},
			{At: "2026-07-16T00:00:00Z", Verb: "activate", IssueNumber: 9, Fixes: []int{30}},
		}}
	writeMetricsFixture(t, env.RepoRoot, origin.RunID, origin)
	writeMetricsFixture(t, env.RepoRoot, fixer.RunID, fixer)

	if code := Run([]string{"metrics", "report"}, env); code != ExitOK {
		t.Fatalf("exit = %d, want %d\n%s", code, ExitOK, stdout.String())
	}
	out := stdout.String()
	for _, want := range []string{
		"  PR #30, fixed by run run-20260716T000000Z-bbbbbbbb issue #8; original run run-20260715T000000Z-aaaaaaaa issue #7; role implementer; executor [model m1, effort high]; reviewer [model m2, effort medium]; reviewer downgraded yes; first review verdict request-changes; review cycles 2; approval at activation human; approval at merge grant:g1\n",
		"  unattributed (no recorded merge event names the PR): 1 [99]\n",
		"approval at activation human\n    issues 1; merged 1",
		"    escaped defects attributed to these issues 1\n",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q:\n%s", want, out)
		}
	}
	if n := strings.Count(out, "PR #30,"); n != 1 {
		t.Errorf("PR 30 listed %d times, want once:\n%s", n, out)
	}
	if strings.Contains(out, "none declared") {
		t.Errorf("declared fixes reported as none:\n%s", out)
	}
}

func TestMetricsReportShowsClaudeArchitectUsage(t *testing.T) {
	n := int64(7)
	run := "run-20261018T120000Z-aaaaaaaa"
	session := "aaaaaaaa-0000-4000-8000-000000000001"
	r, err := buildMetricsReport([]metrics.Document{{SchemaVersion: 2, RunID: run, Host: "claude", ArchitectSessions: []string{session, "invalid-session-id"},
		Events: []metrics.Event{{At: "2026-10-18T12:00:00Z", Verb: "complete"}},
		Observations: []metrics.Observation{
			{SchemaVersion: 2, RunID: run, ID: "claude-architect:" + session, At: "2026-10-18T12:00:00Z", Source: "claude-transcript", Role: "architect", Host: "claude", Session: session,
				Sample: &metrics.CounterSample{Stream: "claude-main-assistant-usage", Mode: "delta", Sequence: 1,
					Counters: metrics.Counters{InputTokens: &n, OutputTokens: &n, CacheReadTokens: &n, CacheCreationTokens: &n}}},
			{SchemaVersion: 2, RunID: run, ID: "claude-architect:invalid-session-id", At: "2026-10-18T12:00:00Z", Source: "claude-transcript", Role: "architect", Host: "claude",
				Session: "invalid-session-id", Unavailable: &metrics.Unavailable{Reason: "session-id-not-uuid"}},
		}}})
	if err != nil {
		t.Fatal(err)
	}
	var out strings.Builder
	printMetricsReport(&out, r)
	for _, want := range []string{
		"host claude; source claude-transcript; stream claude-main-assistant-usage; 1 runs; 1 samples",
		"architect share 100.0% (7 of 7)",
		"architect usage: architect-role counters recorded in all 1 runs",
	} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("output missing %q:\n%s", want, out.String())
		}
	}
}
