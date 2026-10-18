package run

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/kninetimmy/orch/internal/claudeusage"
	"github.com/kninetimmy/orch/internal/config"
	"github.com/kninetimmy/orch/internal/state"
)

const architectSession = "aaaaaaaa-0000-4000-8000-000000000001"

func TestLoadVerbRecordsTheClaudeSessionOnce(t *testing.T) {
	root := setupDeliveryRepoWithConfig(t, metricsEnabledConfigTOML, "r1", []state.Issue{fixtureIssue("a", 1, state.PhaseCleaned)})
	for _, session := range []string{architectSession, architectSession, "", "not a uuid", "../x"} {
		if _, err := loadVerb(Env{RepoRoot: root, Now: fixedNow, ClaudeSession: session}, 0, nil, false); err != nil {
			t.Fatal(err)
		}
	}
	docs := loadMetricsDocs(t, root)
	if len(docs) != 1 || !slices.Equal(docs[0].ArchitectSessions, []string{architectSession, invalidSession}) || docs[0].SchemaVersion != 2 {
		t.Fatalf("docs = %+v, want the session once and every non-UUID as %s", docs, invalidSession)
	}
}

func TestArchitectSessionNotRecordedWhenDisabledOrNotClaude(t *testing.T) {
	root := setupDeliveryRepoWithConfig(t, testConfigTOML, "r1", []state.Issue{fixtureIssue("a", 1, state.PhaseCleaned)})
	if _, err := loadVerb(Env{RepoRoot: root, Now: fixedNow, ClaudeSession: architectSession}, 0, nil, false); err != nil {
		t.Fatal(err)
	}
	assertNoMetricsDir(t, root)

	enabled := &config.Config{}
	enabled.Metrics.Enabled = true
	root = t.TempDir()
	codex := &state.Run{ID: "run-20261018T120000Z-aaaaaaaa", Host: "codex"}
	if err := recordArchitectSession(Env{RepoRoot: root, ClaudeSession: architectSession}, enabled, codex); err != nil {
		t.Fatal(err)
	}
	c := &verbCtx{env: Env{RepoRoot: root}, cfg: enabled, st: &state.State{Run: codex}}
	if err := c.recordArchitectUsage(time.Now()); err != nil {
		t.Fatal(err)
	}
	assertNoMetricsDir(t, root)
}

func TestRecordArchitectUsageRecordsEachSessionAndUnavailable(t *testing.T) {
	root := setupDeliveryRepoWithConfig(t, metricsEnabledConfigTOML, "r1", []state.Issue{fixtureIssue("a", 1, state.PhaseCleaned)})
	other := "bbbbbbbb-0000-4000-8000-000000000002"
	for _, s := range []string{architectSession, other} {
		if _, err := loadVerb(Env{RepoRoot: root, Now: fixedNow, ClaudeSession: s}, 0, nil, false); err != nil {
			t.Fatal(err)
		}
	}
	c, err := loadVerb(Env{RepoRoot: root, Now: fixedNow}, 0, nil, false)
	if err != nil {
		t.Fatal(err)
	}
	start := fixedNow().Add(-time.Hour)
	c.st.Run.StartedAt = start
	line := `{"type":"assistant","sessionId":"%s","version":"2.1.301","timestamp":"%s","isSidechain":false,"message":{"id":"m%d","usage":{"input_tokens":1,"output_tokens":2,"cache_read_input_tokens":3,"cache_creation_input_tokens":4}}}` + "\n"
	transcript := filepath.Join(t.TempDir(), "t.jsonl")
	body := fmt.Sprintf(line, architectSession, start.Add(time.Minute).Format(time.RFC3339), 1) +
		fmt.Sprintf(line, architectSession, start.Add(2*time.Minute).Format(time.RFC3339), 2) +
		fmt.Sprintf(line, architectSession, start.Add(-time.Minute).Format(time.RFC3339), 3) // before activation
	if err := os.WriteFile(transcript, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := claudeusage.RecordSession(root, architectSession, transcript, start); err != nil {
		t.Fatal(err)
	}
	if err := c.recordArchitectUsage(fixedNow()); err != nil {
		t.Fatal(err)
	}

	docs := loadMetricsDocs(t, root)
	if len(docs) != 1 || docs[0].SchemaVersion != 2 || len(docs[0].Observations) != 2 {
		t.Fatalf("docs = %+v, want two observations", docs)
	}
	got, unavailable := docs[0].Observations[0], docs[0].Observations[1]
	n := got.Sample.Counters
	if got.ID != "claude-architect:"+architectSession || got.Role != "architect" || got.Host != "claude" || got.Session != architectSession ||
		got.Source != "claude-transcript" || got.Sample.Mode != "delta" || n.TotalTokens != nil ||
		*n.InputTokens != 2 || *n.OutputTokens != 4 || *n.CacheReadTokens != 6 || *n.CacheCreationTokens != 8 {
		t.Errorf("sample = %+v %+v", got, n)
	}
	if unavailable.Session != other || unavailable.Role != "architect" || unavailable.Sample != nil ||
		unavailable.Unavailable == nil || unavailable.Unavailable.Reason != claudeusage.ReasonPathUnrecorded {
		t.Errorf("unavailable = %+v", unavailable)
	}
}

func TestRecordArchitectUsageWithoutSessionsSaysSo(t *testing.T) {
	root := setupDeliveryRepoWithConfig(t, metricsEnabledConfigTOML, "r1", []state.Issue{fixtureIssue("a", 1, state.PhaseCleaned)})
	c, err := loadVerb(Env{RepoRoot: root, Now: fixedNow}, 0, nil, false)
	if err != nil {
		t.Fatal(err)
	}
	if err := c.recordArchitectUsage(fixedNow()); err != nil {
		t.Fatal(err)
	}
	docs := loadMetricsDocs(t, root)
	if len(docs) != 1 || len(docs[0].Observations) != 1 {
		t.Fatalf("docs = %+v", docs)
	}
	o := docs[0].Observations[0]
	if o.ID != "claude-architect" || o.Role != "architect" || o.Session != "" || o.Unavailable == nil || o.Unavailable.Reason != claudeusage.ReasonNoSession {
		t.Errorf("observation = %+v", o)
	}
}
