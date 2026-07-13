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

	usage := &metrics.Usage{InputTokens: 100, OutputTokens: 40, CacheReadTokens: 5, CacheCreationTokens: 2, DurationMS: 300}
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
		"usage:       input 100, output 40, cache read 5, cache creation 2, duration 300ms",
		"usage reported on 1 of 7 events",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q:\n%s", want, out)
		}
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
