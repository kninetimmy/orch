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
		"usage:       input 100, output 40, cache read 5, cache creation 2, total 900, duration 300ms",
		"usage reported on 1 of 7 events",
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
		"usage by event:",
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
	if strings.Contains(stdout.String(), "usage:") {
		t.Errorf("output has a usage: line though no event carried usage:\n%s", stdout.String())
	}
}
