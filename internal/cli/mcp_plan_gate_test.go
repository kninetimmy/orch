package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/kninetimmy/orch/internal/config"
	"github.com/kninetimmy/orch/internal/execx/execxtest"
	"github.com/kninetimmy/orch/internal/lockfile"
	"github.com/kninetimmy/orch/internal/manifest"
	"github.com/kninetimmy/orch/internal/memhub"
	"github.com/kninetimmy/orch/internal/paths"
	"github.com/kninetimmy/orch/internal/run"
	"github.com/kninetimmy/orch/internal/state"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func mcpGatePlan() run.PlanDoc {
	return run.PlanDoc{SchemaVersion: run.PlanSchemaVersion, Host: "opencode", Title: "Gate parity", Summary: "Preserve the effective contract", Issues: []run.PlanIssue{
		{ID: "a", Title: "Mechanical change", Objective: "Keep the declared work", AcceptanceCriteria: []string{"A works"}, Type: "feature",
			Facts: run.PlanFacts{Downgrade: run.PlanDowngrade{Mechanical: true, LowRisk: true, FullySpecified: true, Unsurprising: true}},
			Wave:  1, RequiredTests: []string{"go test ./...", "local check"}, TestsCIDoesNotRun: []string{"local check"}, Fixes: []int{12}, UsageClass: "light"},
		{ID: "b", Title: "Risk-domain change", Objective: "Keep the contributed criterion", AcceptanceCriteria: []string{"B works"}, Type: "bug", AreaLabels: []string{"area:gate"},
			Facts: run.PlanFacts{RiskDomains: []string{"authorization"}}, DependsOn: []string{"a"}, Wave: 2, RequiredTests: []string{"go vet ./..."}, UsageClass: "medium"},
	}}
}

func mcpGateJSON(t *testing.T, plan run.PlanDoc) []byte {
	t.Helper()
	data, err := json.Marshal(plan)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func mcpPlanGateSchema(t *testing.T, tool *mcp.Tool) {
	t.Helper()
	schema, ok := tool.OutputSchema.(map[string]any)
	if !ok || schema["type"] != "object" {
		t.Fatalf("gate has no object output schema: %#v", tool.OutputSchema)
	}
	properties, ok := schema["properties"].(map[string]any)
	if !ok {
		t.Fatalf("gate output properties: %#v", schema)
	}
	for _, field := range []string{"schema_version", "plan_digest", "plan_title", "host", "config_revision", "config_overrides", "merge_strategy", "execution", "memhub", "ci", "issues"} {
		if properties[field] == nil {
			t.Fatalf("gate output schema omitted %s: %#v", field, schema)
		}
	}
	if tool.Annotations.IdempotentHint || (tool.Annotations.DestructiveHint != nil && !*tool.Annotations.DestructiveHint) {
		t.Fatalf("gate weakens conservative external-probe annotations: %+v", tool.Annotations)
	}
}

func mcpPlanGateExercise(t *testing.T, binary string) {
	t.Helper()
	root, cwd := t.TempDir(), t.TempDir()
	allHosts := validBothHostsTOML + validOpenCodeTOML[strings.Index(validOpenCodeTOML, "[hosts.opencode"):]
	writeConfigOnly(t, root, allHosts)
	writeConfigOnly(t, cwd, "invalid launching configuration [")
	mcpWrite(t, filepath.Join(cwd, state.Path), []byte("{corrupt launching state"))
	// Include real Git metadata in every no-write snapshot, not just Orch files.
	if out, err := exec.CommandContext(t.Context(), "git", "init", "--initial-branch=main", root).CombinedOutput(); err != nil {
		t.Fatalf("initialize isolated Git fixture: %v: %s", err, out)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 60*time.Second)
	defer cancel()
	cwdBefore := mcpTree(t, cwd)
	session, closeSession := mcpConnect(t, ctx, binary, cwd, root, "--repo", root)
	mcpDiscover(t, ctx, session, root)
	plan := mcpGatePlan()
	check := func() *run.GateDoc {
		t.Helper()
		return mcpPlanGateParity(t, ctx, binary, root, session, mcpGateJSON(t, plan))
	}
	for _, host := range []string{"claude", "codex", "opencode"} {
		plan.Host = host
		doc := check()
		legacy, err := plan.Digest()
		if err != nil {
			t.Fatal(err)
		}
		if doc.SchemaVersion != run.GateSchemaVersion || doc.Host != host || doc.PlanDigest == legacy || doc.Execution.Profiles.Architect.Model == "" || doc.Execution.BlastRadiusCriterion != run.BlastRadiusCriterion ||
			!doc.Issues[0].ReviewerDowngraded || doc.Issues[0].RoutingRationale == "" || !reflect.DeepEqual(doc.Issues[0].TestsCIDoesNotRun, []string{"local check"}) || !reflect.DeepEqual(doc.Issues[0].Fixes, []int{12}) ||
			doc.Issues[1].AcceptanceCriteria[1] != run.BlastRadiusCriterion || doc.Issues[1].Wave != 2 || !reflect.DeepEqual(doc.Issues[1].DependsOn, []string{"a"}) || !reflect.DeepEqual(doc.Issues[1].Labels, []string{"ready", "bug", "specialist", "critical", "area:gate"}) {
			t.Fatalf("gate lost approved scope/routing: %+v", doc)
		}
	}
	base := check()
	local := filepath.Join(root, config.LocalOverridePath)
	mcpWrite(t, local, []byte(`[metrics]
enabled = true
[hosts.opencode.roles.implementer]
model = "example/bare-model"
variant = ""
[hosts.opencode.roles.reviewer]
variant = "review-variant"
[concurrency]
max_subagents = 7
`))
	changed := check()
	if changed.PlanDigest == base.PlanDigest || changed.Execution.MaxSubagents != 7 || !changed.Execution.MetricsEnabled ||
		changed.Issues[0].Executor != manifest.OpenCodeSelection("example/bare-model", "") || changed.Issues[1].Reviewer.Variant != "review-variant" ||
		!reflect.DeepEqual(changed.ConfigOverrides, []string{"concurrency.max_subagents", "hosts.opencode.roles.implementer.model", "hosts.opencode.roles.implementer.variant", "hosts.opencode.roles.reviewer.variant", "metrics.enabled"}) {
		t.Fatalf("local execution preferences not preserved: %+v", changed)
	}
	wf := filepath.Join(root, ".github", "workflows")
	if err := os.MkdirAll(wf, 0o755); err != nil {
		t.Fatal(err)
	}
	mcpWrite(t, filepath.Join(wf, "ci.yml"), []byte("name: fixture\n"))
	withCI := check()
	if base.CI.WorkflowsPresent || !withCI.CI.WorkflowsPresent || withCI.CI.Statement == base.CI.Statement || withCI.PlanDigest != changed.PlanDigest {
		t.Fatalf("CI observation changed contract or lost statement: %+v", withCI)
	}
	// As for the CLI, no mutation serializer is acquired even with metrics and
	// a host session id enabled. The gate never records those as lifecycle use.
	mutation, err := lockfile.AcquireMutation(root)
	if err != nil {
		t.Fatal(err)
	}
	check()
	if err := mutation.Release(); err != nil {
		t.Fatal(err)
	}
	raw := mcpGateJSON(t, plan)
	for _, args := range []any{nil, json.RawMessage("null"), []any{}, "object", 1, true,
		map[string]any{"plan": nil}, map[string]any{"plan": []any{}}, map[string]any{"plan": "object"}, map[string]any{"plan": 1},
		map[string]any{"plan": json.RawMessage(raw), "repo": cwd}, map[string]any{"plan": json.RawMessage(raw), "repo_root": cwd},
		map[string]any{"plan": json.RawMessage(raw), "approval": "approve-and-enter-delivery"},
	} {
		result := mcpCall(t, ctx, session, root, "orch_plan_gate", args)
		if !result.IsError || result.StructuredContent != nil {
			t.Fatalf("invalid gate input accepted: %#v => %+v", args, result)
		}
	}
	for _, invalid := range []string{
		strings.Replace(string(raw), `"schema_version":`, `"unknown":true,"schema_version":`, 1),
		strings.Replace(string(raw), `"id":`, `"unknown":true,"id":`, 1),
		strings.Replace(string(raw), `"read_only":`, `"unknown":true,"read_only":`, 1),
		strings.Replace(string(raw), `"mechanical":`, `"unknown":true,"mechanical":`, 1),
		strings.Replace(string(raw), `"schema_version":1`, `"schema_version":99`, 1),
		strings.Replace(string(raw), `"host":"opencode"`, `"host":"other"`, 1),
		strings.Replace(string(raw), `"wave":2`, `"wave":1`, 1),
		strings.Replace(string(raw), `"wave":1`, `"wave":"one"`, 1),
		strings.Replace(string(raw), `"tests_ci_does_not_run":["local check"]`, `"tests_ci_does_not_run":["undeclared"]`, 1),
	} {
		if doc := mcpPlanGateParity(t, ctx, binary, root, session, []byte(invalid)); doc != nil {
			t.Fatalf("invalid nested plan succeeded: %s", invalid)
		}
	}
	committed := filepath.Join(root, config.Path)
	for _, invalid := range []string{"invalid [", strings.Replace(allHosts, "schema_version  = 1", "schema_version = 99", 1)} {
		mcpWrite(t, committed, []byte(invalid))
		if check() != nil {
			t.Fatal("invalid configuration succeeded")
		}
	}
	mcpWrite(t, committed, []byte(allHosts))
	mcpWrite(t, local, []byte("[merge]\nstrategy = \"rebase\"\n"))
	if check() != nil {
		t.Fatal("forbidden local policy succeeded")
	}
	if err := os.Remove(local); err != nil {
		t.Fatal(err)
	}
	// PATH is empty for both real processes: a required missing memhub must
	// fail closed; best-effort must retain the same explicit probe failure.
	for _, mode := range []string{"required", "best-effort"} {
		mcpWrite(t, committed, []byte(strings.Replace(allHosts, `mode = "off"`, `mode = "`+mode+`"`, 1)))
		doc := check()
		if mode == "required" && doc != nil || mode == "best-effort" && (doc == nil || doc.Memhub.Probe != "unhealthy" || doc.Memhub.Recall != "skipped" || doc.Memhub.Detail == "") {
			t.Fatalf("missing memhub %s result: %+v", mode, doc)
		}
	}
	mcpWrite(t, committed, []byte(allHosts))
	st, err := state.EnterDelivery(root, "opencode", testPlanRef(), testIssues())
	if err != nil {
		t.Fatal(err)
	}
	if check() != nil {
		t.Fatal("active run accepted a new gate")
	}
	if err := state.Save(root, &state.State{SchemaVersion: state.SchemaVersion, Mode: state.ModeAssist}); err != nil {
		t.Fatal(err)
	}
	if check() != nil {
		t.Fatal("orphan Delivery lock accepted a gate")
	}
	mcpWrite(t, filepath.Join(root, lockfile.Path), []byte("{corrupt lock"))
	if check() != nil {
		t.Fatal("corrupt lock accepted a gate")
	}
	if err := os.Remove(filepath.Join(root, lockfile.Path)); err != nil {
		t.Fatal(err)
	}
	if err := state.Save(root, st); err != nil {
		t.Fatal(err)
	}
	if check() != nil {
		t.Fatal("Delivery without a lock accepted a gate")
	}
	mcpWrite(t, filepath.Join(root, state.Path), []byte("{corrupt state"))
	if check() != nil {
		t.Fatal("corrupt state accepted a gate")
	}
	if err := os.Remove(filepath.Join(root, state.Path)); err != nil {
		t.Fatal(err)
	}
	check() // Same-connection recovery; no activation or approval is needed.
	if mcpTree(t, cwd) != cwdBefore {
		t.Fatal("gate changed launching repository")
	}
	closeSession()
}

func mcpPlanGateParity(t *testing.T, ctx context.Context, binary, root string, session *mcp.ClientSession, plan []byte) *run.GateDoc {
	t.Helper()
	before := mcpTree(t, root)
	cmd := mcpProcess(ctx, binary, root, "run", "plan")
	cmd.Stdin = bytes.NewReader(plan)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	err := cmd.Run()
	result := mcpCall(t, ctx, session, root, "orch_plan_gate", map[string]any{"plan": json.RawMessage(plan)})
	doc := mcpGateResultParity(t, stdout.Bytes(), stderr.String(), err == nil, result)
	if mcpTree(t, root) != before {
		t.Fatal("CLI/MCP gate changed Orch/Git artifacts")
	}
	return doc
}

func mcpGateResultParity(t *testing.T, stdout []byte, stderr string, success bool, result *mcp.CallToolResult) *run.GateDoc {
	t.Helper()
	if len(result.Content) != 1 {
		t.Fatalf("gate result = %+v", result)
	}
	text, ok := result.Content[0].(*mcp.TextContent)
	if !ok {
		t.Fatalf("gate content = %+v", result.Content)
	}
	if !success {
		if len(stdout) != 0 || !result.IsError || result.StructuredContent != nil || strings.TrimSpace(stderr) != "orch run: "+text.Text {
			t.Fatalf("gate error parity: CLI stdout=%s stderr=%s MCP=%+v", stdout, stderr, result)
		}
		t.Logf("CLI = MCP error: %s; artifacts unchanged", text.Text)
		return nil
	}
	want := mcpDecoded(t, stdout)
	if stderr != "" || result.IsError || !reflect.DeepEqual(want, result.StructuredContent) || !reflect.DeepEqual(want, mcpDecoded(t, []byte(text.Text))) {
		t.Fatalf("gate parity: CLI=%s stderr=%s structured=%v text=%s", stdout, stderr, result.StructuredContent, text.Text)
	}
	var doc run.GateDoc
	if err := json.Unmarshal(stdout, &doc); err != nil {
		t.Fatal(err)
	}
	t.Logf("CLI = structured = JSON text: full GateDoc schema=%d digest=%s; artifacts unchanged", doc.SchemaVersion, doc.PlanDigest)
	return &doc
}

func TestMCPPlanGateMemhubParity(t *testing.T) {
	for _, tc := range []struct {
		name, mode, probe, recall string
		statusExit, recallExit    int
		recallJSON                string
		spawnError                bool
		wantError                 bool
	}{
		{name: "off", mode: "off", probe: "skipped", recall: "skipped"},
		{name: "best effort healthy", mode: "best-effort", probe: "healthy", recall: "healthy", recallJSON: `{"results":[]}`},
		{name: "best effort status fails", mode: "best-effort", probe: "unhealthy", recall: "skipped", statusExit: 1},
		{name: "best effort recall fails", mode: "best-effort", probe: "healthy", recall: "unhealthy", recallExit: 1},
		{name: "required healthy", mode: "required", probe: "healthy", recall: "healthy", recallJSON: `{"results":[]}`},
		{name: "required status fails", mode: "required", statusExit: 1, wantError: true},
		{name: "required recall fails", mode: "required", recallExit: 1, wantError: true},
		{name: "required invalid recall", mode: "required", recallJSON: "not JSON", wantError: true},
		{name: "required spawn fails", mode: "required", spawnError: true, wantError: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			env, stdout, stderr := testEnv(t)
			root, err := paths.Canonical(env.RepoRoot)
			if err != nil {
				t.Fatal(err)
			}
			env.RepoRoot = root
			writeConfigOnly(t, root, strings.Replace(validOpenCodeTOML, `mode = "off"`, `mode = "`+tc.mode+`"`, 1))
			var calls []execxtest.Call
			if tc.mode != "off" {
				status := execxtest.Call{Name: "memhub", Args: []string{"status"}, Dir: root, Exit: tc.statusExit, Stderr: "fixture status failure"}
				if tc.spawnError {
					status.Err = errors.New("fixture memhub unavailable")
				}
				calls = append(calls, status)
				if tc.statusExit == 0 && !tc.spawnError {
					calls = append(calls, execxtest.Call{Name: "memhub", Args: []string{"recall", memhub.RecallProbeQuery, "--json", "--max-results", "1"}, Dir: root, Stdout: tc.recallJSON, Exit: tc.recallExit, Stderr: "fixture recall failure"})
				}
			}
			// Exact ordered calls for CLI then MCP; no real memhub is reachable.
			script := &execxtest.Script{T: t, Calls: append(append([]execxtest.Call{}, calls...), calls...)}
			env.Runner = script
			plan := mcpGateJSON(t, mcpGatePlan())
			env.Stdin = bytes.NewReader(plan)
			before := mcpTree(t, root)
			code := Run([]string{"run", "plan"}, env)
			if (code == ExitError) != tc.wantError || code != ExitOK && code != ExitError {
				t.Fatalf("CLI exit=%d stderr=%s", code, stderr)
			}
			input, writer := io.Pipe()
			reader, output := io.Pipe()
			defer func() {
				_ = input.Close()
				_ = output.Close()
			}()
			env.Stdin, env.Stdout = input, output
			var serverStderr bytes.Buffer
			env.Stderr = &serverStderr
			done := make(chan int, 1)
			go func() { done <- Run([]string{"mcp"}, env) }()
			ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
			defer cancel()
			client := mcp.NewClient(&mcp.Implementation{Name: "gate-probe-test", Version: "1"}, nil)
			session, err := client.Connect(ctx, &mcp.IOTransport{Reader: reader, Writer: writer}, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = session.Close() }()
			result := mcpCall(t, ctx, session, root, "orch_plan_gate", map[string]any{"plan": json.RawMessage(plan)})
			doc := mcpGateResultParity(t, stdout.Bytes(), stderr.String(), code == ExitOK, result)
			if doc != nil && (doc.Memhub.Mode != tc.mode || doc.Memhub.Probe != tc.probe || doc.Memhub.Recall != tc.recall) {
				t.Fatalf("memhub policy changed: %+v", doc.Memhub)
			}
			if err := session.Close(); err != nil {
				t.Fatal(err)
			}
			select {
			case code := <-done:
				if code != ExitOK || serverStderr.Len() != 0 {
					t.Fatalf("server exit=%d stderr=%s", code, &serverStderr)
				}
			case <-ctx.Done():
				t.Fatal("server did not shut down")
			}
			script.AssertExhausted()
			if mcpTree(t, root) != before {
				t.Fatal("gate/probes/disconnect changed repository")
			}
		})
	}
}
