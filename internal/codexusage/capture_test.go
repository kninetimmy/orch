package codexusage

import (
	"os"
	"path/filepath"
	"strings"
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

func TestLegacyTotalIgnoresUnsupportedOptionalFields(t *testing.T) {
	dir := t.TempDir()
	data := strings.ReplaceAll(string(exactRollout(t)), `"type":"event_msg"`, `"timestamp":false,"type":"event_msg"`)
	data = strings.ReplaceAll(data, `"type":`, `"ordinal":false,"type":`)
	data = strings.Replace(data, `"total_tokens":782763`, `"total_tokens":782763,"reasoning_output_tokens":-1`, 1)
	if err := os.WriteFile(filepath.Join(dir, "child.jsonl"), []byte(data), 0o644); err != nil {
		t.Fatal(err)
	}
	if total, ok := TotalTokens(dir, parentThread, executorTask, nil); !ok || total != 782763 {
		t.Fatalf("legacy optional fields changed aggregate: %d %t", total, ok)
	}
	if got := Completed(dir, parentThread, executorTask); got.Reason != "invalid-terminal-usage" {
		t.Fatalf("new capture accepted invalid native counter: %+v", got)
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
// in full and still fails closed. Each broken rollout sits beside the exact
// matching rollout, so unavailable can only mean the broken one poisoned a
// capture that would otherwise have resolved: were it merely skipped as a
// non-candidate, the exact total would still be reported.
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
			if err := os.WriteFile(filepath.Join(dir, "exact.jsonl"), exact, 0o644); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(dir, "rollout.jsonl"), tc.rollout, 0o644); err != nil {
				t.Fatal(err)
			}

			if _, ok := TotalTokens(dir, parentThread, executorTask, nil); ok {
				t.Error("TotalTokens reported usage despite an invalid identified child")
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

func forkedRollout(t *testing.T) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(fixture(t, "forked"), "executor.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func TestForkedRolloutCapturesOnlyChildUsage(t *testing.T) {
	dir := fixture(t, "forked")
	if total, ok := TotalTokens(dir, parentThread, executorTask, nil); !ok || total != 123 {
		t.Fatalf("child total = %d, available %t, want 123", total, ok)
	}
	for _, tc := range []struct {
		previous, want int64
		available      bool
	}{
		{previous: 100, want: 23, available: true},
		{previous: 123, want: 0, available: true},
		{previous: 124},
		{previous: -1},
	} {
		if total, ok := TotalTokens(dir, parentThread, executorTask, &tc.previous); ok != tc.available || total != tc.want {
			t.Fatalf("previous %d: delta %d, available %t, want %d/%t", tc.previous, total, ok, tc.want, tc.available)
		}
	}
	capture := Completed(dir, parentThread, executorTask)
	if capture.Reason != "" || capture.Session != "child-executor" || capture.Sequence != 8 ||
		capture.At != "2026-10-02T12:00:00Z" || capture.SampleID() != "codex:child-executor:tokens:8" ||
		capture.Counters.TotalTokens == nil || *capture.Counters.TotalTokens != 123 ||
		capture.Counters.InputTokens == nil || *capture.Counters.InputTokens != 100 ||
		capture.Counters.CachedInputTokens == nil || *capture.Counters.CachedInputTokens != 0 ||
		capture.Counters.CacheWriteInputTokens != nil || capture.Counters.OutputTokens == nil || *capture.Counters.OutputTokens != 23 ||
		capture.Counters.ReasoningOutputTokens == nil || *capture.Counters.ReasoningOutputTokens != 5 {
		t.Fatalf("child counters, position or timestamp lost: %+v", capture)
	}
	if again := Completed(dir, parentThread, executorTask); again.Reason != "" || again.SampleID() != capture.SampleID() || again.At != capture.At {
		t.Fatalf("capture identity changed on replay: %+v", again)
	}
}

func TestForkedRolloutRejectsInvalidHistory(t *testing.T) {
	base := forkedRollout(t)
	for _, tc := range []struct {
		name, rollout string
	}{
		{name: "unclaimed inherited history", rollout: strings.Replace(base, `,"forked_from_id":"parent-139","subagent_history_start_ordinal":5`, "", 1)},
		{name: "missing fork parent", rollout: strings.Replace(base, `"forked_from_id":"parent-139",`, "", 1)},
		{name: "null fork parent", rollout: strings.Replace(base, `"forked_from_id":"parent-139"`, `"forked_from_id":null`, 1)},
		{name: "malformed fork parent", rollout: strings.Replace(base, `"forked_from_id":"parent-139"`, `"forked_from_id":1`, 1)},
		{name: "wrong fork parent", rollout: strings.Replace(base, `"forked_from_id":"parent-139"`, `"forked_from_id":"other-parent"`, 1)},
		{name: "missing boundary", rollout: strings.Replace(base, `,"subagent_history_start_ordinal":5`, "", 1)},
		{name: "null boundary", rollout: strings.Replace(base, `"subagent_history_start_ordinal":5`, `"subagent_history_start_ordinal":null`, 1)},
		{name: "malformed boundary", rollout: strings.Replace(base, `"subagent_history_start_ordinal":5`, `"subagent_history_start_ordinal":"5"`, 1)},
		{name: "fractional boundary", rollout: strings.Replace(base, `"subagent_history_start_ordinal":5`, `"subagent_history_start_ordinal":5.5`, 1)},
		{name: "negative boundary", rollout: strings.Replace(base, `"subagent_history_start_ordinal":5`, `"subagent_history_start_ordinal":-1`, 1)},
		{name: "boundary at child metadata", rollout: strings.Replace(base, `"subagent_history_start_ordinal":5`, `"subagent_history_start_ordinal":0`, 1)},
		{name: "boundary at parent metadata", rollout: strings.Replace(base, `"subagent_history_start_ordinal":5`, `"subagent_history_start_ordinal":1`, 1)},
		{name: "boundary at inherited token", rollout: strings.Replace(base, `"subagent_history_start_ordinal":5`, `"subagent_history_start_ordinal":3`, 1)},
		{name: "boundary at inherited completion", rollout: strings.Replace(base, `"subagent_history_start_ordinal":5`, `"subagent_history_start_ordinal":4`, 1)},
		{name: "boundary at child token", rollout: strings.Replace(base, `"subagent_history_start_ordinal":5`, `"subagent_history_start_ordinal":7`, 1)},
		{name: "boundary beyond end", rollout: strings.Replace(base, `"subagent_history_start_ordinal":5`, `"subagent_history_start_ordinal":9`, 1)},
		{name: "overflow boundary", rollout: strings.Replace(base, `"subagent_history_start_ordinal":5`, `"subagent_history_start_ordinal":9223372036854775808`, 1)},
		{name: "missing header ordinal", rollout: strings.Replace(base, `"ordinal":0,`, "", 1)},
		{name: "null header ordinal", rollout: strings.Replace(base, `"ordinal":0`, `"ordinal":null`, 1)},
		{name: "header not first", rollout: "{\"ordinal\":0,\"type\":\"event_msg\",\"payload\":{}}\n" + base},
		{name: "missing inherited ordinal", rollout: strings.Replace(base, `"ordinal":3,`, "", 1)},
		{name: "malformed ordinal", rollout: strings.Replace(base, `"ordinal":3`, `"ordinal":"3"`, 1)},
		{name: "negative ordinal", rollout: strings.Replace(base, `"ordinal":3`, `"ordinal":-1`, 1)},
		{name: "noncontiguous ordinal", rollout: strings.Replace(base, `"ordinal":3`, `"ordinal":4`, 1)},
		{name: "missing child ordinal", rollout: strings.Replace(base, `"ordinal":7,`, "", 1)},
		{name: "null child ordinal", rollout: strings.Replace(base, `"ordinal":7`, `"ordinal":null`, 1)},
		{name: "conflicting inherited id", rollout: strings.Replace(base, `"id":"parent-139"`, `"id":"other-parent"`, 1)},
		{name: "conflicting inherited session", rollout: strings.Replace(base, `"id":"parent-139","session_id":"parent-139"`, `"id":"parent-139","session_id":"other-parent"`, 1)},
		{name: "inherited subagent", rollout: strings.Replace(base, `"thread_source":"user"`, `"thread_source":"subagent"`, 1)},
		{name: "inherited parent reference", rollout: strings.Replace(base, `"thread_source":"user"`, `"thread_source":"user","parent_thread_id":"other-parent"`, 1)},
		{name: "inherited task identity", rollout: strings.Replace(base, `"thread_source":"user"`, `"thread_source":"user","agent_path":"/root/sibling"`, 1)},
		{name: "inherited fork claim", rollout: strings.Replace(base, `"thread_source":"user"`, `"thread_source":"user","forked_from_id":"other-parent"`, 1)},
		{name: "malformed inherited source", rollout: strings.Replace(base, `"source":"vscode"`, `"source":{"unknown":"source"}`, 1)},
		{name: "missing inherited metadata", rollout: strings.Replace(base, `"ordinal":1,"type":"session_meta"`, `"ordinal":1,"type":"event_msg"`, 1)},
		{name: "metadata later in prefix", rollout: strings.Replace(base, `"ordinal":2,"type":"event_msg"`, `"ordinal":2,"type":"session_meta"`, 1)},
		{name: "metadata at boundary", rollout: strings.Replace(base, `"ordinal":5,"type":"event_msg"`, `"ordinal":5,"type":"session_meta"`, 1)},
		{name: "metadata in child tail", rollout: base + "{\"ordinal\":9,\"type\":\"session_meta\",\"payload\":{}}\n"},
		{name: "conflicting source task", rollout: strings.Replace(base, `"depth":1,"agent_path":"/root/issue_139_executor"`, `"depth":1,"agent_path":"/root/sibling"`, 1)},
		{name: "conflicting source parent", rollout: strings.Replace(base, `"thread_spawn":{"parent_thread_id":"parent-139"`, `"thread_spawn":{"parent_thread_id":"other-parent"`, 1)},
		{name: "wrong start marker", rollout: strings.Replace(base, `"ordinal":5,"type":"event_msg","payload":{"type":"thread_settings_applied"}`, `"ordinal":5,"type":"event_msg","payload":{"type":"token_count"}`, 1)},
		{name: "wrong start event type", rollout: strings.Replace(base, `"ordinal":5,"type":"event_msg"`, `"ordinal":5,"type":"response_item"`, 1)},
		{name: "missing task start", rollout: strings.Replace(base, `"ordinal":6,"type":"event_msg","payload":{"type":"task_started"}`, `"ordinal":6,"type":"event_msg","payload":{"type":"task_complete"}`, 1)},
		{name: "parent terminal only", rollout: strings.Split(base, `{"ordinal":5`)[0]},
		{name: "parent counters and child completion", rollout: strings.Split(base, `{"ordinal":7`)[0] + "{\"ordinal\":7,\"type\":\"event_msg\",\"payload\":{\"type\":\"task_complete\"}}\n"},
		{name: "unfinished child", rollout: strings.Split(base, `{"ordinal":8`)[0]},
		{name: "malformed child tail", rollout: base + "not-json\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			// A malformed identified child must fail closed, not be skipped in
			// favor of an otherwise valid match beside it.
			for name, data := range map[string]string{"valid.jsonl": base, "invalid.jsonl": tc.rollout} {
				if err := os.WriteFile(filepath.Join(dir, name), []byte(data), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			if _, ok := TotalTokens(dir, parentThread, executorTask, nil); ok {
				t.Fatal("legacy capture accepted invalid fork evidence")
			}
			if got := Completed(dir, parentThread, executorTask); got.Reason != "identified-rollout-invalid-or-unfinished" {
				t.Fatalf("invalid fork skipped or accepted: %+v", got)
			}
		})
	}
}

func TestForkedRolloutIsolatesOtherSessions(t *testing.T) {
	base := forkedRollout(t)
	for _, tc := range []struct {
		name, other, reason string
	}{
		{name: "wrong parent", other: strings.ReplaceAll(base, parentThread, "other-parent") + "not-json\n"},
		{name: "sibling", other: strings.ReplaceAll(base, executorTask, "/root/sibling") + "not-json\n"},
		{name: "unidentified corruption", other: "not-json\n"},
		{name: "duplicate matching file", other: base, reason: "duplicate-matching-rollouts"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			for name, data := range map[string]string{"child.jsonl": base, "other.jsonl": tc.other} {
				if err := os.WriteFile(filepath.Join(dir, name), []byte(data), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			capture := Completed(dir, parentThread, executorTask)
			if capture.Reason != tc.reason {
				t.Fatalf("other session changed capture: %+v, want reason %q", capture, tc.reason)
			}
			total, ok := TotalTokens(dir, parentThread, executorTask, nil)
			if tc.reason == "" && (!ok || total != 123) {
				t.Fatalf("other session suppressed child usage: %d/%t", total, ok)
			}
			if tc.reason != "" && ok {
				t.Fatal("duplicate matching file accepted by legacy capture")
			}
		})
	}
}
