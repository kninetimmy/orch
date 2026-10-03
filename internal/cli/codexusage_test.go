package cli

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kninetimmy/orch/internal/metrics"
	"github.com/kninetimmy/orch/internal/state"
)

// Only field names were inspected in native host metadata. Fixtures contain no
// live transcript; timestamps and counter values below are deterministic data.
func codexRollout(t *testing.T) string {
	t.Helper()
	data, err := os.ReadFile("../codexusage/testdata/exact/executor.jsonl")
	if err != nil {
		t.Fatal(err)
	}
	return strings.ReplaceAll(string(data), `{"type":"event_msg"`, `{"timestamp":"2026-09-30T12:00:00Z","type":"event_msg"`)
}

func codexMetricFixture(t *testing.T) (Env, codexObservationRequest, string) {
	t.Helper()
	env, _, _ := testEnv(t)
	writeConfig(t, env.RepoRoot, validTOML+"\n[metrics]\nenabled = true\n")
	st, err := state.EnterDelivery(env.RepoRoot, "codex", testPlanRef(), testIssues())
	if err != nil {
		t.Fatal(err)
	}
	st.Run.Issues[0].Number = 282
	if err := state.Save(env.RepoRoot, st); err != nil {
		t.Fatal(err)
	}
	home := t.TempDir()
	t.Setenv("CODEX_HOME", home)
	sessions := filepath.Join(home, "sessions")
	if err := os.MkdirAll(sessions, 0o755); err != nil {
		t.Fatal(err)
	}
	return env, codexObservationRequest{SchemaVersion: 2, ParentThreadID: "parent-139", TaskIdentity: "/root/issue_139_executor",
		RunID: st.Run.ID, IssueNumber: 282, Role: "specialist", Attempt: "implementation-1",
		UnavailableID: "capture-check-1", UnavailableAt: "2026-09-30T12:10:00Z"}, sessions
}

func writeCodexRollout(t *testing.T, sessions, name, data string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(sessions, name+".jsonl"), []byte(data), 0o644); err != nil {
		t.Fatal(err)
	}
}

func forkedCodexRollout(t *testing.T) string {
	t.Helper()
	data, err := os.ReadFile("../codexusage/testdata/forked/executor.jsonl")
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func captureCodexLegacy(t *testing.T, req codexSubagentUsageRequest) codexSubagentUsageResponse {
	t.Helper()
	data, err := json.Marshal(req)
	if err != nil {
		t.Fatal(err)
	}
	var out, stderr bytes.Buffer
	if code := Run([]string{"hook", "codex", "subagent-usage"}, Env{Stdin: bytes.NewReader(data), Stdout: &out, Stderr: &stderr}); code != ExitOK {
		t.Fatalf("legacy capture: exit %d: %s", code, &stderr)
	}
	var response codexSubagentUsageResponse
	if err := json.Unmarshal(out.Bytes(), &response); err != nil {
		t.Fatalf("legacy capture response: %v %s", err, &out)
	}
	return response
}

func TestCodexCaptureProcess(t *testing.T) {
	if os.Getenv("ORCH_TEST_CODEX_CAPTURE") != "1" {
		return
	}
	os.Exit(Run([]string{"hook", "codex", "subagent-usage"}, Env{Stdin: os.Stdin, Stdout: os.Stdout, Stderr: os.Stderr}))
}

func captureCodexProcess(t *testing.T, req codexObservationRequest) codexObservationResponse {
	t.Helper()
	data, err := json.Marshal(req)
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(os.Args[0], "-test.run=^TestCodexCaptureProcess$")
	cmd.Env = append(os.Environ(), "ORCH_TEST_CODEX_CAPTURE=1")
	cmd.Stdin = bytes.NewReader(data)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("capture process: %v: %s", err, out)
	}
	var response codexObservationResponse
	if err := json.Unmarshal(out, &response); err != nil || response.SchemaVersion != 2 {
		t.Fatalf("capture response: %v %s", err, out)
	}
	return response
}

func recordCodexProcess(t *testing.T, env Env, o metrics.Observation, recorded bool) {
	t.Helper()
	out, err := metricProcess(t, env.RepoRoot, o).CombinedOutput()
	if err != nil || !strings.Contains(string(out), fmt.Sprintf(`"recorded":%t`, recorded)) {
		t.Fatalf("record process: %v: %s", err, out)
	}
}

func TestCodexCaptureToRecorderRestartRepairAndReviewers(t *testing.T) {
	env, req, sessions := codexMetricFixture(t)
	rollout := strings.Replace(codexRollout(t), `"total_tokens":782763`,
		`"total_tokens":782763,"input_tokens":0,"cached_input_tokens":0,"cache_write_input_tokens":0,"output_tokens":20,"reasoning_output_tokens":4`, 1)
	writeCodexRollout(t, sessions, "executor", rollout)
	initial := captureCodexProcess(t, req)
	if initial.Observation.Session != "child-executor" || initial.Observation.Sample == nil || len(initial.NativeSource) == 0 ||
		initial.Observation.Observed != nil || initial.Observation.Requested != nil || initial.Observation.Interval != nil {
		t.Fatalf("lost attribution or invented evidence: %+v", initial)
	}
	recordCodexProcess(t, env, initial.Observation, true)
	// Both capture and recording restart, so there is no conversational baseline.
	recordCodexProcess(t, env, captureCodexProcess(t, req).Observation, false)
	req.Attempt, req.ReviewCycle = "repair-1", 1
	rollout += "\n" + `{"timestamp":"2026-09-30T12:05:00Z","type":"event_msg","payload":{"type":"token_count","info":{"total_token_usage":{"total_tokens":782800,"reasoning_output_tokens":6}}}}` + "\n" +
		`{"type":"event_msg","payload":{"type":"task_complete"}}` + "\n"
	writeCodexRollout(t, sessions, "executor", rollout)
	repair := captureCodexProcess(t, req).Observation
	recordCodexProcess(t, env, repair, true)
	recordCodexProcess(t, env, captureCodexProcess(t, req).Observation, false)
	// Reattributing the same native sample must conflict, not double count it.
	conflict := repair
	conflict.Attempt = "repair-2"
	if _, err := metricProcess(t, env.RepoRoot, conflict).CombinedOutput(); err == nil {
		t.Fatal("same sample accepted under a different attempt")
	}
	for cycle := 1; cycle <= 2; cycle++ {
		req.Role, req.Attempt, req.ReviewCycle = "reviewer", fmt.Sprintf("review-%d", cycle), cycle
		req.TaskIdentity = fmt.Sprintf("/root/reviewer_%d", cycle)
		reviewer := strings.ReplaceAll(codexRollout(t), "child-executor", fmt.Sprintf("reviewer-%d", cycle))
		reviewer = strings.ReplaceAll(reviewer, "/root/issue_139_executor", req.TaskIdentity)
		writeCodexRollout(t, sessions, req.Attempt, reviewer)
		recordCodexProcess(t, env, captureCodexProcess(t, req).Observation, true)
	}
	docs, err := metrics.LoadAll(env.RepoRoot)
	if err != nil || len(docs) != 1 || len(docs[0].Observations) != 4 || len(docs[0].Events) != 0 {
		t.Fatalf("native history duplicated or legacy usage submitted: %v %+v", err, docs)
	}
	observations := docs[0].Observations
	deltas, err := metrics.CounterContributions(observations)
	if err != nil {
		t.Fatal(err)
	}
	for i, want := range []int64{782763, 37, 782763, 782763} {
		if deltas[i].TotalTokens == nil || *deltas[i].TotalTokens != want {
			t.Fatalf("delta %d = %+v, want %d", i, deltas[i], want)
		}
	}
	if deltas[0].InputTokens == nil || *deltas[0].InputTokens != 0 || *deltas[0].CacheReadTokens != 0 ||
		*deltas[0].CacheCreationTokens != 0 || *deltas[0].ReasoningOutputTokens != 4 || *deltas[1].ReasoningOutputTokens != 2 ||
		deltas[1].InputTokens != nil || observations[1].Attempt != "repair-1" || observations[1].ReviewCycle != 1 ||
		observations[2].Session == observations[3].Session || observations[3].ReviewCycle != 2 {
		t.Fatalf("presence or attribution lost: %+v %+v", observations, deltas)
	}
}

func TestCodexCaptureUnavailableAndPartialCounters(t *testing.T) {
	base := codexRollout(t)
	for _, tc := range []struct {
		name, rollout, reason string
		duplicate             bool
	}{
		{name: "wrong parent", rollout: strings.ReplaceAll(base, "parent-139", "other-parent"), reason: "matching-child-unavailable"},
		{name: "sibling", rollout: strings.ReplaceAll(base, "/root/issue_139_executor", "/root/sibling"), reason: "matching-child-unavailable"},
		{name: "duplicate", rollout: base, duplicate: true, reason: "duplicate-matching-rollouts"},
		{name: "malformed", rollout: base + "\nnot-json\n", reason: "identified-rollout-invalid-or-unfinished"},
		{name: "unfinished", rollout: base + "\n" + `{"type":"event_msg","payload":{"type":"task_started"}}`, reason: "identified-rollout-invalid-or-unfinished"},
		{name: "absent", rollout: strings.Replace(base, `"total_tokens":782763`, `"total_tokens":null`, 1), reason: "native-counters-absent"},
		{name: "negative", rollout: strings.Replace(base, `"total_tokens":782763`, `"total_tokens":-1`, 1), reason: "invalid-terminal-usage"},
		{name: "malformed counter", rollout: strings.Replace(base, `"total_tokens":782763`, `"input_tokens":"bad"`, 1), reason: "invalid-terminal-usage"},
		{name: "conflicting source", rollout: strings.Replace(base, `"depth":1`, `"depth":1,"agent_path":"/root/other"`, 1), reason: "conflicting-source-task-identity"},
		{name: "invalid native identity", rollout: strings.ReplaceAll(base, "child-executor", "child executor"), reason: "invalid-native-identity"},
		{name: "missing timestamp", rollout: strings.ReplaceAll(base, `"timestamp":"2026-09-30T12:00:00Z",`, ""), reason: "native-timestamp-unavailable"},
		{name: "zero aggregate", rollout: strings.Replace(base, `"total_tokens":782763`, `"total_tokens":0`, 1)},
		{name: "absent aggregate", rollout: strings.Replace(base, `"total_tokens":782763`, `"input_tokens":0,"total_tokens":null`, 1)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			env, req, sessions := codexMetricFixture(t)
			writeCodexRollout(t, sessions, "executor", tc.rollout)
			if tc.duplicate {
				writeCodexRollout(t, sessions, "duplicate", tc.rollout)
			}
			o := captureCodexProcess(t, req).Observation
			if tc.reason != "" {
				if o.Unavailable == nil || o.Unavailable.Reason != tc.reason || o.Sample != nil || o.Session != "" || o.Outcome != "" {
					t.Fatalf("unavailable: %+v, want %s", o, tc.reason)
				}
			} else if o.Sample == nil || o.Unavailable != nil {
				t.Fatalf("zero/partial counters lost: %+v", o)
			} else if tc.name == "absent aggregate" && (o.Sample.Counters.TotalTokens != nil || o.Sample.Counters.InputTokens == nil || *o.Sample.Counters.InputTokens != 0) {
				t.Fatalf("fabricated aggregate or lost zero: %+v", o.Sample.Counters)
			} else if tc.name == "zero aggregate" && (o.Sample.Counters.TotalTokens == nil || *o.Sample.Counters.TotalTokens != 0) {
				t.Fatalf("lost aggregate zero: %+v", o.Sample.Counters)
			}
			recordCodexProcess(t, env, o, true)
			recordCodexProcess(t, env, o, false)
		})
	}
}

func TestCodexPlanningScoutAndMissingCoverage(t *testing.T) {
	env, req, sessions := codexMetricFixture(t)
	req.Role, req.IssueNumber, req.Attempt = "scout", 0, ""
	writeCodexRollout(t, sessions, "scout", codexRollout(t))
	recordCodexProcess(t, env, captureCodexProcess(t, req).Observation, true)
	// Unidentified corruption cannot poison the matching scout.
	writeCodexRollout(t, sessions, "other", "{not-json")
	recordCodexProcess(t, env, captureCodexProcess(t, req).Observation, false)
	for _, role := range []string{"architect", "scout"} {
		req.Role, req.TaskIdentity, req.UnavailableID = role, "/root", "missing-"+role
		o := captureCodexProcess(t, req).Observation
		if o.Unavailable == nil || o.Session != "" {
			t.Fatalf("invented root attribution: %+v", o)
		}
		recordCodexProcess(t, env, o, true)
	}
	// Explicit attributable planning evidence uses the generic recorder; capture
	// does not broaden child matching into an inferred root-session parser.
	o := metrics.Observation{SchemaVersion: 1, RunID: req.RunID, ID: "planning-evidence",
		At: req.UnavailableAt, Source: "provided-native-evidence", Host: "codex", Session: "planning-session", Role: "architect",
		Sample: &metrics.CounterSample{Stream: "tokens", Mode: "cumulative", Sequence: 1, Counters: metrics.Counters{TotalTokens: new(int64)}}}
	recordCodexProcess(t, env, o, true)
}

func TestCodexCaptureVersionAndRequestValidation(t *testing.T) {
	_, req, _ := codexMetricFixture(t)
	data, _ := json.Marshal(req)
	for _, request := range []string{
		strings.Replace(string(data), `"schema_version":2`, `"schema_version":1`, 1),
		strings.Replace(string(data), `"schema_version":2`, `"schema_version":null`, 1),
		strings.Replace(string(data), `"schema_version":2`, `"schema_version":2,"previous_total_tokens":0`, 1),
		strings.Replace(string(data), `"schema_version":2`, `"schema_version":2,"observed":{"model":"requested"}`, 1),
		strings.Replace(string(data), `"attempt":"implementation-1"`, `"attempt":""`, 1),
		string(data) + `{}`,
	} {
		env, _, _ := testEnv(t)
		env.Stdin = strings.NewReader(request)
		if code := Run([]string{"hook", "codex", "subagent-usage"}, env); code == ExitOK {
			t.Fatalf("accepted unsupported request: %s", request)
		}
	}
}

func TestForkedCodexCaptureContractsAndRecorderReplay(t *testing.T) {
	env, req, sessions := codexMetricFixture(t)
	rollout := forkedCodexRollout(t)
	writeCodexRollout(t, sessions, "executor", rollout)
	legacy := codexSubagentUsageRequest{ParentThreadID: req.ParentThreadID, TaskIdentity: req.TaskIdentity}
	if got := captureCodexLegacy(t, legacy); got.TotalTokens == nil || *got.TotalTokens != 123 {
		t.Fatalf("legacy child total: %+v", got)
	}
	for _, tc := range []struct {
		previous, want int64
		available      bool
	}{
		{previous: 100, want: 23, available: true},
		{previous: 123, available: true},
		{previous: 124},
		{previous: -1},
	} {
		legacy.PreviousTotalTokens = &tc.previous
		got := captureCodexLegacy(t, legacy)
		if (got.TotalTokens != nil) != tc.available || (got.TotalTokens != nil && *got.TotalTokens != tc.want) {
			t.Fatalf("legacy delta from %d: %+v, want %d/%t", tc.previous, got, tc.want, tc.available)
		}
	}
	initial := captureCodexProcess(t, req)
	o := initial.Observation
	if o.Sample == nil || o.ID != "codex:child-executor:tokens:8" || o.Sample.Sequence != 8 ||
		o.Session != "child-executor" || o.At != "2026-10-02T12:00:00Z" ||
		!strings.Contains(string(initial.NativeSource), `"agent_role":"orch-specialist"`) {
		t.Fatalf("forked child attribution or physical position lost: %+v", initial)
	}
	recordCodexProcess(t, env, o, true)
	replayed := captureCodexProcess(t, req).Observation
	if replayed.Sample == nil || replayed.ID != o.ID || replayed.At != o.At || replayed.Sample.Sequence != o.Sample.Sequence {
		t.Fatalf("forked capture identity changed on restart: %+v", replayed)
	}
	recordCodexProcess(t, env, replayed, false)
	rollout += "\n" + `{"ordinal":9,"type":"event_msg","payload":{"type":"task_started"}}` + "\n" +
		`{"ordinal":10,"timestamp":"2026-10-02T12:05:00Z","type":"event_msg","payload":{"type":"token_count","info":{"total_token_usage":{"input_tokens":110,"cached_input_tokens":0,"output_tokens":40,"reasoning_output_tokens":7,"total_tokens":150}}}}` + "\n" +
		`{"ordinal":11,"type":"event_msg","payload":{"type":"task_complete"}}` + "\n"
	writeCodexRollout(t, sessions, "executor", rollout)
	req.Attempt = "repair-1"
	repair := captureCodexProcess(t, req).Observation
	if repair.Sample == nil || repair.ID != "codex:child-executor:tokens:11" || repair.Sample.Sequence != 11 ||
		repair.At != "2026-10-02T12:05:00Z" || repair.Session != o.Session || repair.Attempt != "repair-1" {
		t.Fatalf("forked repair attribution lost: %+v", repair)
	}
	recordCodexProcess(t, env, repair, true)
	recordCodexProcess(t, env, captureCodexProcess(t, req).Observation, false)
	legacy.PreviousTotalTokens = o.Sample.Counters.TotalTokens
	if got := captureCodexLegacy(t, legacy); got.TotalTokens == nil || *got.TotalTokens != 27 {
		t.Fatalf("legacy repair delta: %+v", got)
	}
	docs, err := metrics.LoadAll(env.RepoRoot)
	if err != nil || len(docs) != 1 || len(docs[0].Observations) != 2 || len(docs[0].Events) != 0 {
		t.Fatalf("forked history duplicated or legacy evidence recorded: %v %+v", err, docs)
	}
	deltas, err := metrics.CounterContributions(docs[0].Observations)
	if err != nil {
		t.Fatal(err)
	}
	for i, want := range []struct{ total, input, output, reasoning int64 }{{123, 100, 23, 5}, {27, 10, 17, 2}} {
		c := deltas[i]
		if c.TotalTokens == nil || *c.TotalTokens != want.total || c.InputTokens == nil || *c.InputTokens != want.input ||
			c.OutputTokens == nil || *c.OutputTokens != want.output || c.ReasoningOutputTokens == nil || *c.ReasoningOutputTokens != want.reasoning ||
			c.CacheReadTokens == nil || *c.CacheReadTokens != 0 || c.CacheCreationTokens != nil {
			t.Fatalf("forked contribution %d lost presence or included parent: %+v", i, c)
		}
	}
}

func TestForkedCodexCaptureBoundaryAndCounterFailures(t *testing.T) {
	base := forkedCodexRollout(t)
	for _, tc := range []struct {
		name, rollout, reason string
		total                 int64
		legacyAvailable       bool
	}{
		{name: "inherited terminal only", rollout: strings.Split(base, `{"ordinal":5`)[0], reason: "identified-rollout-invalid-or-unfinished"},
		{name: "boundary at inherited token", rollout: strings.Replace(base, `"subagent_history_start_ordinal":5`, `"subagent_history_start_ordinal":3`, 1), reason: "identified-rollout-invalid-or-unfinished"},
		{name: "malformed boundary", rollout: strings.Replace(base, `"subagent_history_start_ordinal":5`, `"subagent_history_start_ordinal":"5"`, 1), reason: "identified-rollout-invalid-or-unfinished"},
		{name: "malformed child tail", rollout: base + "not-json\n", reason: "identified-rollout-invalid-or-unfinished"},
		{name: "malformed child counter", rollout: strings.Replace(base, `"total_tokens":123`, `"total_tokens":"123"`, 1), reason: "invalid-terminal-usage"},
		{name: "absent child counters", rollout: strings.Replace(base, `"input_tokens":100,"cached_input_tokens":0,"cache_write_input_tokens":null,"output_tokens":23,"reasoning_output_tokens":5,"total_tokens":123`, "", 1), reason: "native-counters-absent"},
		{name: "absent child timestamp", rollout: strings.Replace(base, `"timestamp":"2026-10-02T12:00:00Z",`, "", 1), reason: "native-timestamp-unavailable", total: 123, legacyAvailable: true},
		{name: "measured zero", rollout: strings.Replace(base, `"total_tokens":123`, `"total_tokens":0`, 1), legacyAvailable: true},
		{name: "absent aggregate", rollout: strings.Replace(base, `"total_tokens":123`, `"total_tokens":null`, 1)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, req, sessions := codexMetricFixture(t)
			writeCodexRollout(t, sessions, "executor", tc.rollout)
			legacy := captureCodexLegacy(t, codexSubagentUsageRequest{ParentThreadID: req.ParentThreadID, TaskIdentity: req.TaskIdentity})
			if (legacy.TotalTokens != nil) != tc.legacyAvailable || (legacy.TotalTokens != nil && *legacy.TotalTokens != tc.total) {
				t.Fatalf("legacy response: %+v, want %d/%t", legacy, tc.total, tc.legacyAvailable)
			}
			o := captureCodexProcess(t, req).Observation
			if tc.reason != "" {
				if o.Unavailable == nil || o.Unavailable.Reason != tc.reason || o.Sample != nil || o.Session != "" {
					t.Fatalf("forked unavailable response: %+v, want %s", o, tc.reason)
				}
			} else if o.Sample == nil || o.Unavailable != nil {
				t.Fatalf("forked zero/partial counters lost: %+v", o)
			} else if tc.name == "measured zero" && (o.Sample.Counters.TotalTokens == nil || *o.Sample.Counters.TotalTokens != 0) {
				t.Fatalf("forked aggregate zero lost: %+v", o.Sample.Counters)
			} else if tc.name == "absent aggregate" && (o.Sample.Counters.TotalTokens != nil || o.Sample.Counters.InputTokens == nil || *o.Sample.Counters.InputTokens != 100) {
				t.Fatalf("forked aggregate inferred from child or parent: %+v", o.Sample.Counters)
			}
		})
	}
}
