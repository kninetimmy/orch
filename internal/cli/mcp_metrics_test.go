package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/kninetimmy/orch/internal/config"
	"github.com/kninetimmy/orch/internal/lockfile"
	"github.com/kninetimmy/orch/internal/manifest"
	"github.com/kninetimmy/orch/internal/metrics"
	"github.com/kninetimmy/orch/internal/state"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func mcpMetricsFixtures(t *testing.T) []metrics.Document {
	t.Helper()
	value := func(n int64) *int64 { return &n }
	const runID = "run-a"
	base := metrics.Observation{SchemaVersion: 2, RunID: runID, At: "2026-11-11T12:30:00Z",
		Source: "native", Host: "opencode", Session: "executor", Role: "implementer", IssueNumber: 7}
	sample := func(id, session string, seq int64, c metrics.Counters) metrics.Observation {
		o := base
		o.ID, o.Session = id, session
		o.Sample = &metrics.CounterSample{Stream: "tokens", Mode: "cumulative", Sequence: seq, Counters: c}
		return o
	}
	first := sample("first", "executor", 1, metrics.Counters{InputTokens: value(100), OutputTokens: value(0), TotalTokens: value(900), ReasoningOutputTokens: value(5)})
	first.Attempt = "implementation-1"
	first.Requested = &metrics.Profile{Model: "requested", Variant: new("")}
	first.Observed = &metrics.Profile{Model: "observed", Variant: new("high")}
	second := sample("omitted-input", "executor", 2, metrics.Counters{TotalTokens: value(940)})
	third := sample("input-resumes", "executor", 3, metrics.Counters{InputTokens: value(125), OutputTokens: value(0), TotalTokens: value(940)})
	architect := sample("architect", "architect", 1, metrics.Counters{InputTokens: value(7), TotalTokens: value(13)})
	architect.Role, architect.IssueNumber = "architect", 0
	otherHost := sample("other-host", "executor", 1, metrics.Counters{TotalTokens: value(3)})
	otherHost.Host = "codex"
	otherSource := sample("other-source", "executor", 1, metrics.Counters{TotalTokens: value(4)})
	otherSource.Source = "manual"
	otherStream := sample("other-stream", "executor", 1, metrics.Counters{TotalTokens: value(5)})
	otherStream.Sample.Stream = "other"
	interval := func(id, session, kind, start, end string) metrics.Observation {
		o := base
		o.ID, o.Session = id, session
		o.Interval = &metrics.Interval{Kind: kind, Start: "2026-11-11T12:" + start + ":00Z", End: "2026-11-11T12:" + end + ":00Z"}
		return o
	}
	outcome := base
	outcome.ID, outcome.Outcome = "reported-failure", "infrastructure-failure"
	unavailable := base
	unavailable.ID, unavailable.Role, unavailable.Session = "unavailable", "scout", ""
	unavailable.Unavailable = &metrics.Unavailable{Reason: "not-captured"}
	var zero metrics.Usage
	if err := json.Unmarshal([]byte(`{"input_tokens":0,"duration_ms":0,"total_tokens":20}`), &zero); err != nil {
		t.Fatal(err)
	}
	return []metrics.Document{
		{SchemaVersion: 2, RunID: runID, Host: "opencode", Observations: []metrics.Observation{
			first, second, third, architect, otherHost, otherSource, otherStream,
			interval("active-a", "executor", "active-agent", "00", "10"),
			interval("active-b", "architect", "active-agent", "05", "15"),
			interval("verification", "executor", "verification", "15", "15"),
			interval("ci", "", "ci-waiting", "15", "20"), outcome, unavailable,
		}, Events: []metrics.Event{
			{At: "2026-11-11T12:00:00Z", Verb: "activate", IssueNumber: 7, Role: "implementer",
				Executor: new(manifest.OpenCodeSelection("executor", "")), Reviewer: new(manifest.OpenCodeSelection("reviewer", "high")),
				ReviewerDowngraded: true, ApprovalSource: "human", RoutingFacts: &metrics.RoutingFacts{Mechanical: true}},
			{At: "2026-11-11T12:01:00Z", Verb: "review", IssueNumber: 7, Verdict: "request-changes", ReviewCycles: 1},
			{At: "2026-11-11T12:01:00Z", Verb: "review", IssueNumber: 7, Role: "implementer", ReviewCycles: 1, Usage: &zero},
			{At: "2026-11-11T12:02:00Z", Verb: "review", IssueNumber: 7, Verdict: "approve", ReviewCycles: 2},
			{At: "2026-11-11T12:03:00Z", Verb: "ci", IssueNumber: 7, CIState: "failing"},
			{At: "2026-11-11T12:04:00Z", Verb: "ci", IssueNumber: 7, CIState: "passing"},
			{At: "2026-11-11T12:05:00Z", Verb: "merge", IssueNumber: 7, PRNumber: 30, ApprovalSource: "grant:g1"},
			{At: "2026-11-11T12:06:00Z", Verb: "activate", IssueNumber: 8, RoutingFacts: &metrics.RoutingFacts{RiskDomains: []string{"concurrency"}, UnusuallyDifficult: true}},
			{At: "2026-11-11T12:07:00Z", Verb: "review", IssueNumber: 8, Verdict: "request-changes", ReviewBlockCause: metrics.BlockCauseWrongCriterion},
			{At: "2026-11-11T12:08:00Z", Verb: "review", IssueNumber: 8, Verdict: "request-changes", ReviewBlockCause: metrics.BlockCauseFixCycleLimit},
			{At: "2026-11-11T12:09:00Z", Verb: "block", IssueNumber: 8, BlockClass: "human"},
			{At: "2026-11-11T12:10:00Z", Verb: "escalate", IssueNumber: 8},
			{At: "2026-11-11T12:11:00Z", Verb: "abandon", IssueNumber: 8},
		}},
		{SchemaVersion: 2, RunID: "run-b", Host: "claude", Events: []metrics.Event{
			{At: "2026-11-12T12:00:00Z", Verb: "activate", IssueNumber: 9, Fixes: []int{30, 99}},
			{At: "2026-11-12T12:00:00Z", Verb: "activate", IssueNumber: 10, Fixes: []int{30}},
		}},
		{SchemaVersion: 1, RunID: "run-c", Events: []metrics.Event{
			{At: "legacy", Verb: "dispatch", IssueNumber: 11},
			{At: "legacy", Verb: "review", IssueNumber: 11, Verdict: "request-changes", Usage: &zero},
		}},
		{SchemaVersion: 1, RunID: "run-d"},
	}
}

func mcpMetricsExercise(t *testing.T, binary string) {
	t.Helper()
	root, cwd := t.TempDir(), t.TempDir()
	writeConfig(t, root, validTOML)
	writeConfig(t, cwd, "invalid launching configuration [")
	// Unlike status inspection, metrics must not consult state or any locks.
	mcpWrite(t, filepath.Join(root, state.Path), []byte("{corrupt state"))
	mcpWrite(t, filepath.Join(root, lockfile.Path), []byte("{corrupt lock"))
	cwdBefore := mcpTree(t, cwd)
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	session, closeSession := mcpConnect(t, ctx, binary, cwd, root, "--repo", root)
	mcpDiscover(t, ctx, session, root)
	mcpMetricsParity(t, ctx, binary, root, session)
	for _, tool := range []string{"orch_metrics", "orch_metrics_report"} {
		for _, args := range []any{json.RawMessage("null"), []any{}, "object", 1, true,
			map[string]any{"repo": cwd}, map[string]any{"repo_root": cwd}, map[string]any{"run_id": "run-a"}, map[string]any{"unexpected": true}} {
			result := mcpCall(t, ctx, session, root, tool, args)
			if !result.IsError || result.StructuredContent != nil {
				t.Fatalf("%s accepted %#v: %+v", tool, args, result)
			}
		}
	}
	local := filepath.Join(root, config.LocalOverridePath)
	mcpWrite(t, local, []byte("[metrics]\nenabled = true\n"))
	mcpMetricsParity(t, ctx, binary, root, session) // enabled, still no storage
	for _, doc := range mcpMetricsFixtures(t) {
		writeMetricsFixture(t, root, doc.RunID, doc)
	}
	mcpMetricsParity(t, ctx, binary, root, session)
	mcpWrite(t, local, []byte("[metrics]\nenabled = false\n"))
	mcpMetricsParity(t, ctx, binary, root, session) // disabled history remains visible

	committed := filepath.Join(root, config.Path)
	for _, tc := range []struct{ name, data string }{
		{"missing configuration", ""}, {"invalid configuration", "invalid ["},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if tc.data == "" {
				if err := os.Remove(committed); err != nil {
					t.Fatal(err)
				}
			} else {
				mcpWrite(t, committed, []byte(tc.data))
			}
			mcpMetricsErrors(t, ctx, binary, root, session)
			mcpWrite(t, committed, []byte(validTOML))
			mcpMetricsParity(t, ctx, binary, root, session)
		})
	}
	errorPath := filepath.Join(root, filepath.FromSlash(metrics.Dir), "run-error.json")
	for _, data := range []string{
		"{corrupt metrics", `{"schema_version":99,"run_id":"run-error"}`,
		`{"schema_version":1,"run_id":"run-wrong"}`, `{"schema_version":1,"run_id":"run-error","extra":true}`,
		`{"schema_version":1,"run_id":"run-error","events":[{"at":"t","verb":"review","usage":{"input_tokens":-1}}]}`,
	} {
		mcpWrite(t, errorPath, []byte(data))
		mcpMetricsErrors(t, ctx, binary, root, session)
	}
	// A valid recorded history can still fail compatible-group aggregation.
	overflow := mcpMetricsFixtures(t)[0]
	overflow.RunID, overflow.Events = "run-error", nil
	overflow.Observations = overflow.Observations[:2]
	for i := range overflow.Observations {
		o := &overflow.Observations[i]
		o.RunID, o.Session = overflow.RunID, fmt.Sprint(i)
		o.Sample.Sequence = 1
		o.Sample.Counters = metrics.Counters{TotalTokens: new(int64(math.MaxInt64))}
	}
	writeMetricsFixture(t, root, overflow.RunID, overflow)
	mcpMetricsErrors(t, ctx, binary, root, session)
	// Invalid cumulative order fails in the shared recorder replay, not MCP.
	overflow.Observations[1].Session = overflow.Observations[0].Session
	writeMetricsFixture(t, root, overflow.RunID, overflow)
	mcpMetricsErrors(t, ctx, binary, root, session)
	if err := os.Remove(errorPath); err != nil {
		t.Fatal(err)
	}
	mcpMetricsParity(t, ctx, binary, root, session)
	mcpDiscover(t, ctx, session, root)
	if mcpTree(t, cwd) != cwdBefore {
		t.Fatal("metrics inspection changed launching repository")
	}
	closeSession()
	mcpMetricsOmittedArguments(t, ctx, binary, cwd, root)
}

// The SDK client replaces nil arguments with {}, so use raw stdio to exercise
// genuinely omitted arguments rather than accidentally testing a valid object.
func mcpMetricsOmittedArguments(t *testing.T, ctx context.Context, binary, cwd, root string) {
	t.Helper()
	before, cwdBefore := mcpTree(t, root), mcpTree(t, cwd)
	cmd := mcpProcess(ctx, binary, cwd, "mcp", "--repo", root)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	input, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	output, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	waited := false
	t.Cleanup(func() {
		if !waited {
			_ = input.Close()
			_ = cmd.Wait()
		}
	})
	enc, dec := json.NewEncoder(input), json.NewDecoder(output)
	send := func(v any) {
		t.Helper()
		if err := enc.Encode(v); err != nil {
			t.Fatal(err)
		}
	}
	receive := func() map[string]any {
		t.Helper()
		var response map[string]any
		if err := dec.Decode(&response); err != nil {
			t.Fatal(err)
		}
		return response
	}
	send(map[string]any{"jsonrpc": "2.0", "id": 1, "method": "initialize", "params": map[string]any{
		"protocolVersion": "2025-06-18", "capabilities": map[string]any{}, "clientInfo": map[string]any{"name": "raw-metrics-test", "version": "1"},
	}})
	if response := receive(); response["error"] != nil || response["result"] == nil {
		t.Fatalf("raw initialize: %+v", response)
	}
	send(map[string]any{"jsonrpc": "2.0", "method": "notifications/initialized"})
	for i, tool := range []string{"orch_metrics", "orch_metrics_report"} {
		send(map[string]any{"jsonrpc": "2.0", "id": i + 2, "method": "tools/call", "params": map[string]any{"name": tool}})
		response := receive()
		result, ok := response["result"].(map[string]any)
		if !ok || result["isError"] != true || result["structuredContent"] != nil {
			t.Fatalf("%s accepted omitted arguments: %+v", tool, response)
		}
	}
	if err := input.Close(); err != nil {
		t.Fatal(err)
	}
	err = cmd.Wait()
	waited = true
	if err != nil || stderr.Len() != 0 || mcpTree(t, root) != before || mcpTree(t, cwd) != cwdBefore {
		t.Fatalf("raw stdio shutdown/no-write: err=%v stderr=%s", err, stderr.String())
	}
}

// Compare human CLI output to the shared results as well as MCP structured/text
// encoding. This preserves the CLI contract without adding a new JSON CLI mode.
func mcpMetricsParity(t *testing.T, ctx context.Context, binary, root string, session *mcp.ClientSession) {
	t.Helper()
	history, err := inspectMetrics(root)
	if err != nil {
		t.Fatal(err)
	}
	rollup, err := inspectMetricsReport(root)
	if err != nil {
		t.Fatal(err)
	}
	if history.SchemaVersion != 1 || rollup.SchemaVersion != 1 || history.HistoryScope != metricsHistoryScope || rollup.HistoryScope != metricsHistoryScope || history.Enabled != rollup.Enabled || len(history.Runs) != rollup.Report.runs {
		t.Fatalf("incomplete reports: %+v / %+v", history, rollup)
	}
	for _, tc := range []struct {
		tool string
		doc  any
		args []string
	}{
		{"orch_metrics", history, []string{"metrics"}},
		{"orch_metrics_report", rollup, []string{"metrics", "report"}},
	} {
		before := mcpTree(t, root)
		var wantCLI strings.Builder
		fmt.Fprintf(&wantCLI, "orch:   %s\nmetrics enabled: %t\n", Version, history.Enabled)
		if tc.tool == "orch_metrics" {
			for i, s := range history.Runs {
				if i > 0 {
					fmt.Fprintln(&wantCLI)
				}
				printRunSummary(&wantCLI, s)
			}
			if len(history.Runs) == 0 {
				fmt.Fprintln(&wantCLI, "no metrics recorded.")
			}
		} else if rollup.Report.runs > 0 {
			printMetricsReport(&wantCLI, rollup.Report)
		} else if history.Enabled {
			fmt.Fprintln(&wantCLI, "no metrics recorded; no report to print.")
		} else {
			fmt.Fprintln(&wantCLI, "metrics are disabled and none are recorded; no report to print.")
		}
		out, err := mcpProcess(ctx, binary, root, tc.args...).CombinedOutput()
		if err != nil || string(out) != wantCLI.String() {
			t.Fatalf("%s CLI/shared mismatch: err=%v\ngot=%s\nwant=%s", tc.tool, err, out, wantCLI.String())
		}
		encoded, err := json.Marshal(tc.doc)
		if err != nil {
			t.Fatal(err)
		}
		result := mcpCall(t, ctx, session, root, tc.tool, map[string]any{})
		if result.IsError || len(result.Content) != 1 {
			t.Fatalf("%s result = %+v", tc.tool, result)
		}
		text, ok := result.Content[0].(*mcp.TextContent)
		want := mcpDecoded(t, encoded)
		if !ok || !reflect.DeepEqual(result.StructuredContent, want) || !reflect.DeepEqual(mcpDecoded(t, []byte(text.Text)), want) {
			t.Fatalf("%s structured/text/shared mismatch: want=%s result=%+v", tc.tool, encoded, result)
		}
		if mcpTree(t, root) != before {
			t.Fatal("CLI/shared/MCP metrics inspection changed repository")
		}
		t.Logf("%s: CLI = shared formatting; shared JSON = structured = JSON text; enabled=%t runs=%d; unchanged files", tc.tool, history.Enabled, len(history.Runs))
	}
}

func mcpMetricsErrors(t *testing.T, ctx context.Context, binary, root string, session *mcp.ClientSession) {
	t.Helper()
	_, historyErr := inspectMetrics(root)
	_, reportErr := inspectMetricsReport(root)
	for _, tc := range []struct {
		tool string
		err  error
		args []string
	}{
		{"orch_metrics", historyErr, []string{"metrics"}},
		{"orch_metrics_report", reportErr, []string{"metrics", "report"}},
	} {
		if tc.err == nil {
			t.Fatalf("%s invalid fixture succeeded", tc.tool)
		}
		before := mcpTree(t, root)
		out, err := mcpProcess(ctx, binary, root, tc.args...).CombinedOutput()
		result := mcpCall(t, ctx, session, root, tc.tool, map[string]any{})
		if err == nil || !strings.Contains(string(out), tc.err.Error()) || !result.IsError || result.StructuredContent != nil || len(result.Content) != 1 {
			t.Fatalf("%s error mismatch: shared=%v CLI=%s err=%v MCP=%+v", tc.tool, tc.err, out, err, result)
		}
		text, ok := result.Content[0].(*mcp.TextContent)
		if !ok || text.Text != tc.err.Error() || mcpTree(t, root) != before {
			t.Fatalf("%s error/no-write mismatch: %+v", tc.tool, result)
		}
	}
}

func TestMetricsStructuredResults(t *testing.T) {
	env, _, _ := testEnv(t)
	writeConfig(t, env.RepoRoot, validTOML)
	for _, doc := range mcpMetricsFixtures(t) {
		writeMetricsFixture(t, env.RepoRoot, doc.RunID, doc)
	}
	before := mcpTree(t, env.RepoRoot)
	history, err := inspectMetrics(env.RepoRoot)
	if err != nil {
		t.Fatal(err)
	}
	report, err := inspectMetricsReport(env.RepoRoot)
	if err != nil {
		t.Fatal(err)
	}
	encode := func(v any) any {
		t.Helper()
		data, err := json.Marshal(v)
		if err != nil {
			t.Fatal(err)
		}
		return mcpDecoded(t, data)
	}
	h, r := encode(history), encode(report)
	check := func(doc any, want string, path ...any) {
		t.Helper()
		got := doc
		for _, key := range path {
			switch k := key.(type) {
			case string:
				m, ok := got.(map[string]any)
				if !ok {
					t.Fatalf("%v: not an object at %s: %#v", path, k, got)
				}
				got = m[k]
			case int:
				a, ok := got.([]any)
				if !ok || k >= len(a) {
					t.Fatalf("%v: missing array element %d: %#v", path, k, got)
				}
				got = a[k]
			}
		}
		if !reflect.DeepEqual(got, mcpDecoded(t, []byte(want))) {
			t.Errorf("%v = %#v, want %s", path, got, want)
		}
	}
	check(h, `1`, "schema_version")
	check(h, `false`, "enabled")
	check(h, `"recorded-metrics-only"`, "history_scope")
	check(h, `"run-a"`, "runs", 0, "run_id")
	check(h, `4`, "runs", 0, "review_cycles") // executor sibling is not a review
	check(h, `2`, "runs", 0, "reviewed_issues")
	check(h, `{"passing":1}`, "runs", 0, "ci_by_state")
	check(h, `{"input_tokens":0,"total_tokens":20,"duration_ms":0}`, "runs", 0, "legacy_usage", 0, "usage")
	check(h, `{"total_tokens":40}`, "runs", 0, "usage_samples", 1, "counters") // omission is not zero
	check(h, `{"input_tokens":25,"output_tokens":0,"total_tokens":0}`, "runs", 0, "usage_samples", 2, "counters")
	check(h, `{"model":"requested","variant":""}`, "runs", 0, "usage_samples", 0, "observation", "requested")
	check(h, `{"host":"opencode","source":"native","stream":"tokens"}`, "runs", 0, "usage_groups", 3, "key")
	check(h, `4`, "runs", 0, "usage_groups", 3, "samples")
	for _, tc := range []struct{ name, want string }{
		{"input_tokens", `{"total":132,"measured":3}`},
		{"output_tokens", `{"total":0,"measured":2}`},
		{"total_tokens", `{"total":953,"measured":4}`}, // independent of the splits
		{"reasoning_output_tokens", `{"total":5,"measured":1}`},
		{"cache_read_tokens", `{"total":null,"measured":0}`},
	} {
		check(h, tc.want, "runs", 0, "usage_groups", 3, "counters", tc.name)
		check(r, tc.want, "report", "usage", 3, "counters", tc.name)
	}
	// Identical session/sequence identifiers in another host/source/stream are
	// separate streams, not repeated native totals or a global aggregate.
	for i, total := range []int{3, 4, 5} {
		check(h, fmt.Sprint(total), "runs", 0, "usage_groups", i, "counters", "total_tokens", "total")
	}
	check(h, `{"intervals":2,"incomplete_sessions":0,"session_effort_ns":1200000000000,"wall_clock_ns":900000000000}`, "runs", 0, "timing", "active-agent")
	check(h, `{"intervals":1,"incomplete_sessions":0,"session_effort_ns":0,"wall_clock_ns":0}`, "runs", 0, "timing", "verification")
	check(h, `{"intervals":1,"incomplete_sessions":1,"session_effort_ns":null,"wall_clock_ns":300000000000}`, "runs", 0, "timing", "ci-waiting")
	check(h, `null`, "runs", 0, "timing", "human-waiting")
	check(h, `{"infrastructure-failure":1}`, "runs", 0, "reported_outcomes")
	check(h, `{"approval":1,"escalation":1}`, "runs", 0, "engine_outcomes")
	check(h, `"not-captured"`, "runs", 0, "unavailable", 0, "unavailable", "reason")
	check(h, `["reviewer","scout"]`, "runs", 0, "missing_usage_roles")
	check(h, `"run-c"`, "runs", 2, "run_id") // schema-1 history is not upgraded
	check(h, `null`, "runs", 2, "usage_groups")
	check(h, `0`, "runs", 3, "event_count")
	check(r, `4`, "report", "runs")
	check(r, `1`, "report", "routes", 0, "outcomes", "escaped_defects")
	check(r, `1`, "report", "routes", 0, "outcomes", "fix_cycles")
	check(r, `2`, "report", "routes", 0, "outcomes", "review_cycles")
	check(r, `2`, "report", "routes", 0, "outcomes", "review_cycles_per_reviewed_issue")
	check(r, `{"grant:g1":1}`, "report", "routes", 0, "outcomes", "merge_approval")
	check(r, `"unknown"`, "report", "routes", 1, "key", "approval")
	check(r, `1`, "report", "routes", 1, "outcomes", "cause_unrecorded")
	check(r, `{"wrong_criterion":1,"fix_cycle_limit":1}`, "report", "routes", 1, "outcomes", "review_blocks")
	for i, title := range []string{"host", "any risk domain declared", "unusually difficult", "downgrade fact mechanical", "downgrade fact low risk", "downgrade fact fully specified", "downgrade fact unsurprising"} {
		check(r, fmt.Sprintf("%q", title), "report", "dimensions", i, "title")
	}
	check(r, `"unknown"`, "report", "dimensions", 0, "groups", 2, "value")
	check(r, `true`, "report", "usage", 4, "lifecycle")
	check(r, `{"total":40,"measured":2}`, "report", "usage", 4, "counters", "total_tokens")
	check(r, `{"total":13,"measured":1}`, "report", "usage", 3, "architect_counters", "total_tokens")
	check(r, `953`, "report", "usage", 3, "per_merged_issue", "total_tokens")
	check(r, `null`, "report", "usage", 3, "per_merged_issue", "cache_read_tokens")
	check(r, `null`, "report", "usage", 3, "architect_share_percent", "output_tokens")
	check(r, fmt.Sprint(100*float64(13)/953), "report", "usage", 3, "architect_share_percent", "total_tokens")
	check(r, `3`, "report", "runs_without_arch_use")
	check(r, `{"run_id":"run-a","elapsed_ns":660000000000,"measured":true}`, "report", "elapsed", 0)
	check(r, `{"run_id":"run-b","elapsed_ns":0,"measured":true}`, "report", "elapsed", 1)
	check(r, `{"run_id":"run-c","elapsed_ns":null,"measured":false}`, "report", "elapsed", 2)
	check(r, `{"run_id":"run-d","elapsed_ns":null,"measured":false}`, "report", "elapsed", 3)
	check(r, `660000000000`, "report", "elapsed_total_ns")
	check(r, `2`, "report", "elapsed_measured")
	check(r, `[{"pr":30,"fixed_by_run":"run-b","fixed_by_issue":9,"origin_run":"run-a","origin_issue":7,"origin":{"route":{"role":"implementer","executor":"model executor, variant none","reviewer":"model reviewer, variant high","downgraded":"yes","approval":"human"},"first_verdict":"request-changes","review_cycles":2,"merge_approval":"grant:g1"}}]`, "report", "escapes")
	check(r, `[99]`, "report", "unattributed")
	if mcpTree(t, env.RepoRoot) != before {
		t.Fatal("shared result inspection changed files")
	}
}
