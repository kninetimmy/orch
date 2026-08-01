package codexusage

import (
	"os"
	"path/filepath"
	"testing"
)

const (
	parentThread = "parent-139"
	executorTask = "/root/issue_139_executor"
)

func fixture(t *testing.T, name string) string {
	t.Helper()
	return filepath.Join("testdata", name)
}

func TestTotalTokensSelectsOnlyExactCompletedChild(t *testing.T) {
	got, ok := TotalTokens(fixture(t, "exact"), parentThread, executorTask, nil)
	if !ok {
		t.Fatal("TotalTokens reported unavailable")
	}
	if got != 782763 {
		t.Errorf("total_tokens = %d, want 782763", got)
	}
}

func exactRollout(t *testing.T) []byte {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(fixture(t, "exact"), "executor.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	return data
}

// A rollout that does not identify itself as the requested child is another
// session's business: nothing wrong with it may suppress the exact total.
func TestTotalTokensIsolatesUnrelatedRolloutCorruption(t *testing.T) {
	exact := exactRollout(t)
	unrelated, err := os.ReadFile(filepath.Join(fixture(t, "exact"), "sibling.jsonl"))
	if err != nil {
		t.Fatal(err)
	}

	for _, tc := range []struct {
		name  string
		other []byte
	}{
		{
			name:  "zero-byte rollout",
			other: nil,
		},
		{
			name:  "no session metadata",
			other: []byte("{\"type\":\"event_msg\",\"payload\":{\"type\":\"token_count\"}}\n"),
		},
		{
			name:  "unparseable first line",
			other: []byte("{\"type\":\"session_me"),
		},
		{
			name:  "unrecognized session metadata shape",
			other: []byte("{\"type\":\"session_meta\",\"payload\":{\"id\":\"other\",\"source\":{\"mcp\":\"gateway\"}}}\n"),
		},
		{
			name:  "non-object session metadata payload",
			other: []byte("{\"type\":\"session_meta\",\"payload\":\"other\"}\n"),
		},
		{
			name:  "unidentified rollout",
			other: []byte("{\"type\":\"session_meta\",\"payload\":{}}\nthis is not json\n"),
		},
		{
			name:  "identified unrelated rollout",
			other: append(unrelated, []byte("this is not json\n")...),
		},
		{
			name:  "object-valued unrelated source",
			other: []byte("{\"type\":\"session_meta\",\"payload\":{\"id\":\"review\",\"session_id\":\"review\",\"thread_source\":\"subagent\",\"source\":{\"subagent\":\"review\"}}}\nthis is not json\n"),
		},
		{
			name:  "defaulted unrelated source",
			other: []byte("{\"type\":\"session_meta\",\"payload\":{\"id\":\"root\",\"session_id\":\"root\",\"thread_source\":\"user\"}}\nthis is not json\n"),
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			if err := os.WriteFile(filepath.Join(dir, "exact.jsonl"), exact, 0o644); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(dir, "other.jsonl"), tc.other, 0o644); err != nil {
				t.Fatal(err)
			}

			got, ok := TotalTokens(dir, parentThread, executorTask, nil)
			if !ok {
				t.Fatal("TotalTokens reported unavailable")
			}
			if got != 782763 {
				t.Errorf("total_tokens = %d, want 782763", got)
			}
		})
	}
}

// A rollout that does identify itself as the requested child is still checked
// in full and still fails closed.
func TestTotalTokensFailsClosedForIdentifiedChild(t *testing.T) {
	exact := exactRollout(t)

	for _, tc := range []struct {
		name    string
		rollout []byte
	}{
		{
			name:    "malformed record",
			rollout: append(exactRollout(t), []byte("this is not json\n")...),
		},
		{
			name:    "duplicate session metadata",
			rollout: append(exactRollout(t), exact...),
		},
		{
			name:    "malformed source",
			rollout: []byte("{\"type\":\"session_meta\",\"payload\":{\"id\":\"unknown\",\"session_id\":\"parent-139\",\"parent_thread_id\":\"parent-139\",\"thread_source\":\"subagent\",\"agent_path\":\"/root/issue_139_executor\",\"source\":{\"subagent\":\"not-a-variant\"}}}\nthis is not json\n"),
		},
		{
			name:    "missing source",
			rollout: []byte("{\"type\":\"session_meta\",\"payload\":{\"id\":\"child-executor\",\"session_id\":\"parent-139\",\"parent_thread_id\":\"parent-139\",\"thread_source\":\"subagent\",\"agent_path\":\"/root/issue_139_executor\"}}\n"),
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			if err := os.WriteFile(filepath.Join(dir, "rollout.jsonl"), tc.rollout, 0o644); err != nil {
				t.Fatal(err)
			}

			if _, ok := TotalTokens(dir, parentThread, executorTask, nil); ok {
				t.Error("TotalTokens reported usage for an invalid identified child")
			}
		})
	}
}

func TestTotalTokensValidatesThreadSpawnSource(t *testing.T) {
	for _, tc := range []struct {
		name      string
		source    string
		available bool
	}{
		{
			name:      "complete",
			source:    `{"subagent":{"thread_spawn":{"parent_thread_id":"parent-139","depth":1,"agent_path":"/root/task","agent_nickname":"worker","agent_role":"default"}}}`,
			available: true,
		},
		{
			name:      "agent type alias",
			source:    `{"subagent":{"thread_spawn":{"parent_thread_id":"parent-139","depth":1,"agent_type":"default"}}}`,
			available: true,
		},
		{
			name:   "missing depth",
			source: `{"subagent":{"thread_spawn":{"parent_thread_id":"parent-139"}}}`,
		},
		{
			name:   "wrong typed depth",
			source: `{"subagent":{"thread_spawn":{"parent_thread_id":"parent-139","depth":"1"}}}`,
		},
		{
			name:   "wrong typed agent path",
			source: `{"subagent":{"thread_spawn":{"parent_thread_id":"parent-139","depth":1,"agent_path":1}}}`,
		},
		{
			name:   "invalid agent path",
			source: `{"subagent":{"thread_spawn":{"parent_thread_id":"parent-139","depth":1,"agent_path":"/root/Bad"}}}`,
		},
		{
			name:   "wrong typed agent nickname",
			source: `{"subagent":{"thread_spawn":{"parent_thread_id":"parent-139","depth":1,"agent_nickname":true}}}`,
		},
		{
			name:   "wrong typed agent role",
			source: `{"subagent":{"thread_spawn":{"parent_thread_id":"parent-139","depth":1,"agent_role":[]}}}`,
		},
		{
			name:   "duplicate agent role alias",
			source: `{"subagent":{"thread_spawn":{"parent_thread_id":"parent-139","depth":1,"agent_role":"default","agent_type":"default"}}}`,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			rollout := `{"type":"session_meta","payload":{"id":"child-executor","session_id":"parent-139","parent_thread_id":"parent-139","thread_source":"subagent","agent_path":"/root/issue_139_executor","source":` + tc.source + "}}\n" +
				"{\"type\":\"event_msg\",\"payload\":{\"type\":\"token_count\",\"info\":{\"total_token_usage\":{\"total_tokens\":782763}}}}\n" +
				"{\"type\":\"event_msg\",\"payload\":{\"type\":\"task_complete\"}}\n"
			if err := os.WriteFile(filepath.Join(dir, "rollout.jsonl"), []byte(rollout), 0o644); err != nil {
				t.Fatal(err)
			}

			got, ok := TotalTokens(dir, parentThread, executorTask, nil)
			if ok != tc.available {
				t.Fatalf("TotalTokens availability = %t, want %t", ok, tc.available)
			}
			if ok && got != 782763 {
				t.Errorf("total_tokens = %d, want 782763", got)
			}
		})
	}
}

func TestTotalTokensReturnsExactResumeDelta(t *testing.T) {
	for _, tc := range []struct {
		name     string
		previous int64
		want     int64
	}{
		{name: "additional usage", previous: 782000, want: 763},
		{name: "zero usage", previous: 782763, want: 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := TotalTokens(fixture(t, "exact"), parentThread, executorTask, &tc.previous)
			if !ok {
				t.Fatal("TotalTokens reported unavailable")
			}
			if got != tc.want {
				t.Errorf("total_tokens = %d, want %d", got, tc.want)
			}
		})
	}
}

func TestTotalTokensUnavailable(t *testing.T) {
	t.Run("missing persistence", func(t *testing.T) {
		if _, ok := TotalTokens(filepath.Join(t.TempDir(), "missing"), parentThread, executorTask, nil); ok {
			t.Error("TotalTokens reported usage for missing persistence")
		}
	})

	t.Run("no matching child", func(t *testing.T) {
		if _, ok := TotalTokens(fixture(t, "exact"), parentThread, "/root/missing", nil); ok {
			t.Error("TotalTokens reported usage for no matching child")
		}
	})

	t.Run("parent rollout", func(t *testing.T) {
		if _, ok := TotalTokens(fixture(t, "exact"), parentThread, "/root", nil); ok {
			t.Error("TotalTokens reported the parent session's usage")
		}
	})

	t.Run("malformed final total", func(t *testing.T) {
		if _, ok := TotalTokens(fixture(t, "unavailable"), parentThread, executorTask, nil); ok {
			t.Error("TotalTokens reported usage for malformed final total")
		}
	})

	t.Run("ambiguous children", func(t *testing.T) {
		dir := t.TempDir()
		data, err := os.ReadFile(filepath.Join(fixture(t, "exact"), "executor.jsonl"))
		if err != nil {
			t.Fatal(err)
		}
		for _, name := range []string{"one.jsonl", "two.jsonl"} {
			if err := os.WriteFile(filepath.Join(dir, name), data, 0o644); err != nil {
				t.Fatal(err)
			}
		}
		if _, ok := TotalTokens(dir, parentThread, executorTask, nil); ok {
			t.Error("TotalTokens reported usage for ambiguous children")
		}
	})

	t.Run("invalid previous total", func(t *testing.T) {
		for _, previous := range []int64{-1, 782764} {
			if _, ok := TotalTokens(fixture(t, "exact"), parentThread, executorTask, &previous); ok {
				t.Errorf("TotalTokens reported usage for previous_total_tokens = %d", previous)
			}
		}
	})
}
