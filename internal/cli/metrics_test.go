package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kninetimmy/orch/internal/metrics"
)

func TestMetricsNotInitialized(t *testing.T) {
	env, stdout, stderr := testEnv(t)
	if code := Run([]string{"metrics"}, env); code != ExitError {
		t.Errorf("exit = %d, want %d", code, ExitError)
	}
	if !strings.Contains(stderr.String(), "not initialized") {
		t.Errorf("stderr = %q", stderr.String())
	}
	// The version line prints before any repository check, matching
	// status's convention (status_test.go's TestStatusNotInitialized).
	if !strings.Contains(stdout.String(), "orch:   dev") {
		t.Errorf("stdout missing version line:\n%s", stdout.String())
	}
}

func TestMetricsNoHistoryCreatesNoStorage(t *testing.T) {
	env, stdout, _ := testEnv(t)
	writeConfig(t, env.RepoRoot, validTOML)
	if code := Run([]string{"metrics"}, env); code != ExitOK {
		t.Fatalf("exit = %d, want %d", code, ExitOK)
	}
	out := stdout.String()
	for _, want := range []string{"orch:   dev", "metrics enabled: false", "no metrics recorded."} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q:\n%s", want, out)
		}
	}
	if _, err := os.Stat(filepath.Join(env.RepoRoot, filepath.FromSlash(metrics.Dir))); !os.IsNotExist(err) {
		t.Errorf("metrics dir exists after `orch metrics` (stat err = %v), want absent", err)
	}
}

// writeMetricsFixture writes a hand-built metrics document directly to
// disk (bypassing metrics.Append), so the test controls exact event
// shapes without spinning up a Delivery run.
func writeMetricsFixture(t *testing.T, root, runID string, doc metrics.Document) {
	t.Helper()
	dir := filepath.Join(root, filepath.FromSlash(metrics.Dir))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	// Document's fields are exported and json-tagged, so marshaling it
	// directly is exactly the shape metrics.Append itself would have
	// written.
	data, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, runID+".json"), data, 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestMetricsSummarizesFixtureRun(t *testing.T) {
	env, stdout, _ := testEnv(t)
	writeConfig(t, env.RepoRoot, validTOML)

	// TotalTokens is deliberately not the sum of the four split fields
	// (metrics.Usage documents it as independent: it is what a host
	// reporting one aggregate with no split records). Keeping it apart
	// is what makes the run-level "total 900" assertion below a real
	// guard — a printRunSummary that summed the split fields instead of
	// printing the accumulated TotalTokens would print 147 and fail,
	// which is exactly the bug #135 fixed. Do not "correct" 900 to 147.
	usage := &metrics.Usage{InputTokens: 100, OutputTokens: 40, CacheReadTokens: 5, CacheCreationTokens: 2, TotalTokens: 900, DurationMS: 300}
	doc := metrics.Document{
		SchemaVersion: metrics.SchemaVersion,
		RunID:         "run-20260713T000000Z-aaaaaaaa",
		Events: []metrics.Event{
			{At: "2026-07-13T00:00:00Z", Verb: "dispatch", IssueNumber: 1, Role: "implementer"},
			{At: "2026-07-13T00:01:00Z", Verb: "pr-open", IssueNumber: 1, Usage: usage},
			{At: "2026-07-13T00:02:00Z", Verb: "review", IssueNumber: 1, Verdict: "approve", ReviewCycles: 1},
			{At: "2026-07-13T00:03:00Z", Verb: "ci", IssueNumber: 1, CIState: "passing"},
			{At: "2026-07-13T00:04:00Z", Verb: "merge", IssueNumber: 1},
			{At: "2026-07-13T00:05:00Z", Verb: "block", IssueNumber: 2, BlockClass: "hook"},
			{At: "2026-07-13T00:06:00Z", Verb: "complete", Merged: 1, Abandoned: 0},
		},
	}
	writeMetricsFixture(t, env.RepoRoot, doc.RunID, doc)

	if code := Run([]string{"metrics"}, env); code != ExitOK {
		t.Fatalf("exit = %d, want %d\n%s", code, ExitOK, stdout.String())
	}
	out := stdout.String()
	for _, want := range []string{
		"orch:   dev",
		"metrics enabled: false",
		"run:         run-20260713T000000Z-aaaaaaaa",
		"events:      7 (first 2026-07-13T00:00:00Z, last 2026-07-13T00:06:00Z)",
		"issues:      2 seen; merged 1, abandoned 0, blocked 1 (hook: 1)",
		"escalations: 0",
		"reviews:     1 cycles; first-pass approve: 1 of 1 reviewed issues",
		"ci:          passing: 1",
		"legacy usage by event (host/source/session unavailable; no combined total):",
		"input 100, output 40, cache read 5, cache creation 2, total 900, reasoning output unknown; unclassified reported duration 300ms",
		"observed usage: none recorded; native counters unknown",
		"roles without recorded counters: architect, implementer, reviewer",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q:\n%s", want, out)
		}
	}
}

// TestMetricsReportsPerEventUsageAttributedByRoleAndCycle pins item 5's
// rework: per-event usage detail attributed by role, and by review
// cycle for review events, alongside the run-level totals. It also
// pins the reviewCycles fix that goes with it: a review-verb event
// with no Verdict (the executor's fix-cycle usage sibling, item 3)
// shares its ReviewCycles number with the reviewer's own event rather
// than counting as an additional cycle.
func TestMetricsReportsPerEventUsageAttributedByRoleAndCycle(t *testing.T) {
	env, stdout, _ := testEnv(t)
	writeConfig(t, env.RepoRoot, validTOML)

	prOpenUsage := &metrics.Usage{InputTokens: 10, OutputTokens: 5}
	reviewerUsage := &metrics.Usage{InputTokens: 80, OutputTokens: 30}
	executorUsage := &metrics.Usage{InputTokens: 300, OutputTokens: 120, TotalTokens: 420}
	doc := metrics.Document{
		SchemaVersion: metrics.SchemaVersion,
		RunID:         "run-20260713T000000Z-cccccccc",
		Events: []metrics.Event{
			{At: "t0", Verb: "dispatch", IssueNumber: 1, Role: "implementer"},
			{At: "t1", Verb: "pr-open", IssueNumber: 1, Role: "implementer", Usage: prOpenUsage},
			{At: "t2", Verb: "review", IssueNumber: 1, Verdict: "request-changes", ReviewCycles: 1, Usage: reviewerUsage},
			{At: "t3", Verb: "review", IssueNumber: 1, ReviewCycles: 1, Role: "implementer", Usage: executorUsage},
			{At: "t4", Verb: "review", IssueNumber: 1, Verdict: "approve", ReviewCycles: 2, Usage: reviewerUsage},
		},
	}
	writeMetricsFixture(t, env.RepoRoot, doc.RunID, doc)

	if code := Run([]string{"metrics"}, env); code != ExitOK {
		t.Fatalf("exit = %d, want %d\n%s", code, ExitOK, stdout.String())
	}
	out := stdout.String()
	for _, want := range []string{
		"reviews:     2 cycles; first-pass approve: 0 of 1 reviewed issues",
		"legacy usage by event",
		"role implementer",
		"role reviewer",
		"cycle 1",
		"cycle 2",
		"total 420",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q:\n%s", want, out)
		}
	}
	// dispatch never carries usage, so it must not appear in the
	// per-event usage detail (only pr-open and review do).
	if strings.Contains(out, "1. dispatch") {
		t.Errorf("usage detail lists a dispatch event, which never carries usage:\n%s", out)
	}
}

// TestMetricsUsageDetailUnattributedWhenRoleMissing pins the fallback
// for a usage-carrying event recorded before this build started
// attributing pr-open by role: it prints plainly as unattributed
// rather than guessing.
func TestMetricsUsageDetailUnattributedWhenRoleMissing(t *testing.T) {
	env, stdout, _ := testEnv(t)
	writeConfig(t, env.RepoRoot, validTOML)
	doc := metrics.Document{
		SchemaVersion: metrics.SchemaVersion,
		RunID:         "run-20260713T000000Z-eeeeeeee",
		Events: []metrics.Event{
			{At: "t0", Verb: "pr-open", IssueNumber: 1, Usage: &metrics.Usage{InputTokens: 5}},
		},
	}
	writeMetricsFixture(t, env.RepoRoot, doc.RunID, doc)
	if code := Run([]string{"metrics"}, env); code != ExitOK {
		t.Fatalf("exit = %d, want %d\n%s", code, ExitOK, stdout.String())
	}
	if !strings.Contains(stdout.String(), "role unattributed") {
		t.Errorf("output missing the unattributed fallback for a Role-less pr-open event:\n%s", stdout.String())
	}
}

func TestMetricsOmitsUsageLinesWhenNoEventCarriesUsage(t *testing.T) {
	env, stdout, _ := testEnv(t)
	writeConfig(t, env.RepoRoot, validTOML)
	doc := metrics.Document{
		SchemaVersion: metrics.SchemaVersion,
		RunID:         "run-20260713T000000Z-bbbbbbbb",
		Events:        []metrics.Event{{At: "2026-07-13T00:00:00Z", Verb: "dispatch", IssueNumber: 1}},
	}
	writeMetricsFixture(t, env.RepoRoot, doc.RunID, doc)

	if code := Run([]string{"metrics"}, env); code != ExitOK {
		t.Fatalf("exit = %d, want %d\n%s", code, ExitOK, stdout.String())
	}
	if strings.Contains(stdout.String(), "legacy usage by event") {
		t.Errorf("output has legacy usage detail though no event carried usage:\n%s", stdout.String())
	}
}

func TestMetricsReportsObservedCoverageTimingAndOutcomes(t *testing.T) {
	env, stdout, _ := testEnv(t)
	writeConfig(t, env.RepoRoot, validTOML)
	runID := "run-20260930T120000Z-28300000"
	value := func(v int64) *int64 { return &v }
	observation := func(id, at, role, session string) metrics.Observation {
		return metrics.Observation{SchemaVersion: 2, RunID: runID, ID: id, At: at,
			Source: "codex-session-log", IssueNumber: 283, Role: role, Host: "codex", Session: session}
	}
	sample := func(id, at, role, session, attempt string, cycle int, sequence int64, counters metrics.Counters) metrics.Observation {
		o := observation(id, at, role, session)
		o.Attempt, o.ReviewCycle = attempt, cycle
		o.Sample = &metrics.CounterSample{Stream: "native-token-usage", Mode: "cumulative", Sequence: sequence, Counters: counters}
		return o
	}
	interval := func(id, at, role, session, kind, start, end string) metrics.Observation {
		o := observation(id, at, role, session)
		o.Interval = &metrics.Interval{Kind: kind, Start: start, End: end}
		return o
	}
	outcome := func(id, kind string) metrics.Observation {
		o := observation(id, "2026-09-30T00:30:00Z", "specialist", "executor")
		o.Outcome = kind
		return o
	}

	first := sample("executor-1", "2026-09-30T00:10:00Z", "specialist", "executor", "implementation-1", 0, 1,
		metrics.Counters{InputTokens: value(100), OutputTokens: value(0), TotalTokens: value(160)})
	first.Requested = &metrics.Profile{Model: "requested-model", Effort: "high"}
	first.Observed = &metrics.Profile{Model: "observed-model", Effort: "medium"}
	second := sample("executor-2", "2026-09-30T00:16:00Z", "specialist", "executor", "repair-1", 0, 2,
		metrics.Counters{InputTokens: value(150), OutputTokens: value(0), TotalTokens: value(200)})
	repeated := sample("executor-3", "2026-09-30T00:17:00Z", "specialist", "executor", "repair-1", 0, 3,
		metrics.Counters{InputTokens: value(150), OutputTokens: value(0), TotalTokens: value(200)})
	reviewer := sample("reviewer-1", "2026-09-30T00:21:00Z", "reviewer", "reviewer-1", "review-1", 1, 1,
		metrics.Counters{InputTokens: value(20), TotalTokens: value(30)})
	otherSource := sample("reviewer-2", "2026-09-30T00:22:00Z", "reviewer", "reviewer-2", "review-2", 2, 1,
		metrics.Counters{InputTokens: value(0), TotalTokens: value(0)})
	otherSource.Source = "manual-host-report"

	observations := []metrics.Observation{
		first, second, repeated, reviewer, otherSource,
		interval("active-1", "2026-09-30T00:10:00Z", "specialist", "executor", "active-agent", "2026-09-30T00:00:00Z", "2026-09-30T00:10:00Z"),
		interval("active-2", "2026-09-30T00:15:00Z", "specialist", "executor", "active-agent", "2026-09-30T00:05:00Z", "2026-09-30T00:15:00Z"),
		interval("active-3", "2026-09-30T00:20:00Z", "reviewer", "reviewer-1", "active-agent", "2026-09-30T00:10:00Z", "2026-09-30T00:20:00Z"),
		interval("verification-0", "2026-09-30T00:20:00Z", "specialist", "executor", "verification", "2026-09-30T00:20:00Z", "2026-09-30T00:20:00Z"),
		interval("ci-unknown-session", "2026-09-30T00:25:00Z", "specialist", "", "ci-waiting", "2026-09-30T00:20:00Z", "2026-09-30T00:25:00Z"),
	}
	for _, kind := range []string{"implementation-failure", "infrastructure-failure", "evidence-correction", "wrong-requirement", "escalation", "approval"} {
		observations = append(observations, outcome("outcome-"+kind, kind))
	}
	unavailable := observation("architect-unavailable", "2026-09-30T00:30:00Z", "architect", "")
	unavailable.IssueNumber, unavailable.Host = 0, ""
	unavailable.Unavailable = &metrics.Unavailable{Reason: "root-capture-unsupported"}
	observations = append(observations, unavailable)

	var legacyZero metrics.Usage
	if err := json.Unmarshal([]byte(`{"input_tokens":0,"duration_ms":0}`), &legacyZero); err != nil {
		t.Fatal(err)
	}
	doc := metrics.Document{SchemaVersion: 2, RunID: runID, Observations: observations, Events: []metrics.Event{
		{At: "t0", Verb: "dispatch", IssueNumber: 283, Role: "specialist"},
		{At: "t1", Verb: "review", IssueNumber: 283, Verdict: "request-changes", ReviewCycles: 1},
		{At: "t2", Verb: "review", IssueNumber: 283, Verdict: "approve", ReviewCycles: 2},
		{At: "t3", Verb: "escalate", IssueNumber: 283},
		{At: "t4", Verb: "pr-open", IssueNumber: 283, Role: "specialist", Usage: &legacyZero},
	}}
	legacyRunID := "run-20260929T120000Z-legacy01"
	writeMetricsFixture(t, env.RepoRoot, legacyRunID, metrics.Document{SchemaVersion: 1, RunID: legacyRunID,
		Events: []metrics.Event{{At: "legacy", Verb: "dispatch", IssueNumber: 1, Role: "implementer"}}})
	writeMetricsFixture(t, env.RepoRoot, runID, doc)
	path := filepath.Join(env.RepoRoot, filepath.FromSlash(metrics.Dir), runID+".json")
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if code := Run([]string{"metrics"}, env); code != ExitOK {
		t.Fatalf("exit = %d, want %d\n%s", code, ExitOK, stdout.String())
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != string(before) {
		t.Fatal("metrics report mutated its schema-2 fixture")
	}
	out := stdout.String()
	for _, want := range []string{
		"run:         " + legacyRunID,
		"run:         " + runID,
		"input 0, output unknown",
		"unclassified reported duration 0ms",
		"executor-1; issue #283; role specialist; native session codex/executor; attempt implementation-1; review cycle unknown",
		"requested [model requested-model, effort high]; observed [model observed-model, effort medium]",
		"executor-3; issue #283; role specialist; native session codex/executor; attempt repair-1; review cycle unknown; source codex-session-log; requested [unknown]; observed [unknown]; input 0",
		"reviewer-1; issue #283; role reviewer; native session codex/reviewer-1; attempt review-1; review cycle 1",
		"reviewer-2; issue #283; role reviewer; native session codex/reviewer-2; attempt review-2; review cycle 2",
		"host codex; source codex-session-log; stream native-token-usage; input 170 (4/4 measured), output 0 (3/4 measured)",
		"host codex; source manual-host-report; stream native-token-usage; input 0 (1/1 measured)",
		"roles without recorded counters: architect",
		"complete native session count unknown",
		"observations with unknown role: 0; incomplete host/session: 2",
		"architect-unavailable; run-level; role architect; native session unknown; attempt unknown; review cycle unknown; reason root-capture-unsupported",
		"active-agent: session-summed 25m0s; wall-clock 20m0s; 3 intervals; 0 incomplete host/session intervals",
		"verification: session-summed 0s; wall-clock 0s; 1 intervals",
		"ci-waiting: session-summed unknown (no complete host/session intervals); wall-clock 5m0s; 1 intervals; 1 incomplete host/session intervals",
		"human-waiting: unknown (no measured intervals)",
		"reported outcomes: approval: 1, escalation: 1, evidence-correction: 1, implementation-failure: 1, infrastructure-failure: 1, wrong-requirement: 1",
		"engine outcomes: approval: 1, escalation: 1",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "input 170 (5/5 measured)") {
		t.Errorf("report combined incompatible sources:\n%s", out)
	}
}

func TestMetricsRejectsCompatibleAggregateOverflow(t *testing.T) {
	value := func(v int64) *int64 { return &v }
	doc := metrics.Document{SchemaVersion: 2, RunID: "run-overflow", Observations: []metrics.Observation{
		{SchemaVersion: 2, RunID: "run-overflow", ID: "a", At: "2026-09-30T00:00:00Z", Source: "native", Host: "codex", Session: "a",
			Sample: &metrics.CounterSample{Stream: "tokens", Mode: "delta", Sequence: 1, Counters: metrics.Counters{TotalTokens: value(int64(^uint64(0) >> 1))}}},
		{SchemaVersion: 2, RunID: "run-overflow", ID: "b", At: "2026-09-30T00:00:00Z", Source: "native", Host: "codex", Session: "b",
			Sample: &metrics.CounterSample{Stream: "tokens", Mode: "delta", Sequence: 1, Counters: metrics.Counters{TotalTokens: value(1)}}},
	}}
	if _, err := summarizeRun(doc); err == nil || !strings.Contains(err.Error(), "overflow") {
		t.Fatalf("summarizeRun error = %v, want compatible total overflow", err)
	}
}

func TestMetricsCoverageNamesKnownSessionsWithoutCounters(t *testing.T) {
	tokens := int64(10)
	doc := metrics.Document{SchemaVersion: 2, RunID: "run-session-coverage", Observations: []metrics.Observation{
		{SchemaVersion: 2, RunID: "run-session-coverage", ID: "review-usage", At: "2026-09-30T00:00:00Z", Source: "native",
			IssueNumber: 1, Role: "reviewer", ReviewCycle: 1, Host: "codex", Session: "reviewer-1",
			Sample: &metrics.CounterSample{Stream: "tokens", Mode: "cumulative", Sequence: 1, Counters: metrics.Counters{TotalTokens: &tokens}}},
		{SchemaVersion: 2, RunID: "run-session-coverage", ID: "approval-only", At: "2026-09-30T00:01:00Z", Source: "native",
			IssueNumber: 1, Role: "reviewer", ReviewCycle: 2, Host: "codex", Session: "reviewer-2", Outcome: "approval"},
	}}
	summary, err := summarizeRun(doc)
	if err != nil {
		t.Fatal(err)
	}
	var out strings.Builder
	printObservedUsage(&out, summary)
	for _, want := range []string{
		"native sessions with counters: codex/reviewer-1",
		"known native sessions without counters: codex/reviewer-2",
	} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("coverage missing %q:\n%s", want, out.String())
		}
	}
}

func TestMetricsTimingExcludesIncompleteHostSessionIdentity(t *testing.T) {
	interval := func(id, host string) metrics.Observation {
		return metrics.Observation{SchemaVersion: 2, RunID: "run-timing-identity", ID: id, At: "2026-09-30T00:10:00Z", Source: "native",
			Role: "specialist", Host: host, Session: "a",
			Interval: &metrics.Interval{Kind: "active-agent", Start: "2026-09-30T00:00:00Z", End: "2026-09-30T00:10:00Z"}}
	}
	summary, err := summarizeRun(metrics.Document{SchemaVersion: 2, RunID: "run-timing-identity",
		Observations: []metrics.Observation{interval("known", "codex"), interval("missing-host", "")}})
	if err != nil {
		t.Fatal(err)
	}
	var out strings.Builder
	printTiming(&out, summary.timing)
	want := "active-agent: known-session subtotal 10m0s; wall-clock 10m0s; 2 intervals; 1 incomplete host/session intervals excluded"
	if !strings.Contains(out.String(), want) {
		t.Errorf("timing missing %q:\n%s", want, out.String())
	}
}
