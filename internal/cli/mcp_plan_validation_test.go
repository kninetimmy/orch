package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/kninetimmy/orch/internal/config"
	"github.com/kninetimmy/orch/internal/lockfile"
	"github.com/kninetimmy/orch/internal/run"
	"github.com/kninetimmy/orch/internal/state"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func mcpPlanValidationExercise(t *testing.T, binary string) {
	t.Helper()
	root, cwd := t.TempDir(), t.TempDir()
	writeConfigOnly(t, root, "invalid configuration [")
	writeConfigOnly(t, cwd, strings.Replace(validTOML, `"r1"`, `"wrong-repository"`, 1))
	cwdBefore := mcpTree(t, cwd)
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	session, closeSession := mcpConnect(t, ctx, binary, cwd, root, "--repo", root)
	mcpDiscover(t, ctx, session, root)
	// Invalid configuration does not prevent connection/discovery; the call
	// exposes the loader's failure and then recovers on the same connection.
	mcpPlanValidationParity(t, ctx, root, session, minimalPlanJSON, false)
	committed, local := filepath.Join(root, config.Path), filepath.Join(root, config.LocalOverridePath)
	// Required memhub and enabled metrics must not cause a probe or an event.
	current := strings.Replace(validTOML, `mode = "off"`, `mode = "required"`, 1) + "\n[metrics]\nenabled = true\n"
	mcpWrite(t, committed, []byte(current))
	mcpPlanValidationChecks(t, ctx, root, session)
	// Neither an active run nor even unreadable state/locks is a validation
	// precondition. They remain exclusively the plan gate/lifecycle's concern.
	if _, err := state.EnterDelivery(root, "claude", testPlanRef(), testIssues()); err != nil {
		t.Fatal(err)
	}
	mcpPlanValidationParity(t, ctx, root, session, minimalPlanJSON, true)
	mcpWrite(t, filepath.Join(root, state.Path), []byte("{corrupt state"))
	mcpWrite(t, filepath.Join(root, lockfile.Path), []byte("{corrupt lock"))
	mcpPlanValidationParity(t, ctx, root, session, minimalPlanJSON, true)
	// A revision change is visible, and replacing the enabled host invalidates
	// the previously valid plan without reconnecting.
	mcpWrite(t, committed, []byte(strings.Replace(current, `"r1"`, `"r2"`, 1)))
	mcpPlanValidationParity(t, ctx, root, session, minimalPlanJSON, true)
	mcpWrite(t, committed, []byte(validOpenCodeTOML))
	mcpPlanValidationParity(t, ctx, root, session, minimalPlanJSON, false)
	mcpPlanValidationParity(t, ctx, root, session, strings.Replace(minimalPlanJSON, `"claude"`, `"opencode"`, 1), true)
	mcpWrite(t, committed, []byte(current))
	// The loader's local model preferences affect the validator's label
	// denylist even though config_revision itself remains the committed value.
	labelPlan := strings.Replace(minimalPlanJSON, `"type": "bug"`, `"type": "bug", "area_labels": ["new-local-model"]`, 1)
	mcpPlanValidationParity(t, ctx, root, session, labelPlan, true)
	mcpWrite(t, local, []byte("[hosts.claude.roles.architect]\nmodel = \"new-local-model\"\n"))
	mcpPlanValidationParity(t, ctx, root, session, labelPlan, false)
	for _, data := range []string{"invalid overlay [", "[merge]\nstrategy = \"rebase\"\n"} {
		mcpWrite(t, local, []byte(data))
		mcpPlanValidationParity(t, ctx, root, session, minimalPlanJSON, false)
	}
	if err := os.Remove(local); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(committed); err != nil {
		t.Fatal(err)
	}
	mcpPlanValidationParity(t, ctx, root, session, minimalPlanJSON, false)
	mcpWrite(t, committed, []byte(current))
	mcpPlanValidationParity(t, ctx, root, session, minimalPlanJSON, true)
	if mcpTree(t, cwd) != cwdBefore {
		t.Fatal("plan validation changed launching repository")
	}
	closeSession()
	mcpPlanValidationRawStdio(t, ctx, binary, cwd, root)
}

// Used by both real stdio and the rejecting LookPath/Runner test so validation
// successes and failures share decoder/validator parity and no-command coverage.
func mcpPlanValidationChecks(t *testing.T, ctx context.Context, root string, session *mcp.ClientSession) {
	t.Helper()
	mcpPlanValidationParity(t, ctx, root, session, minimalPlanJSON, true)
	for _, tc := range []struct{ name, plan string }{
		{"unsupported schema", strings.Replace(minimalPlanJSON, `"schema_version": 1`, `"schema_version": 99`, 1)},
		{"fractional schema literal", strings.Replace(minimalPlanJSON, `"schema_version": 1`, `"schema_version": 1.0`, 1)},
		{"unknown host", strings.Replace(minimalPlanJSON, `"claude"`, `"unknown"`, 1)},
		{"disabled host", strings.Replace(minimalPlanJSON, `"claude"`, `"codex"`, 1)},
		{"unknown plan field", strings.Replace(minimalPlanJSON, `"host":`, `"unexpected":true,"host":`, 1)},
		{"unknown issue field", strings.Replace(minimalPlanJSON, `"id":`, `"unexpected":true,"id":`, 1)},
		{"unknown facts field", strings.Replace(minimalPlanJSON, `"read_only": false`, `"read_only":false,"unexpected":true`, 1)},
		{"unknown downgrade field", strings.Replace(minimalPlanJSON, `"read_only": false`, `"downgrade":{"unexpected":true}`, 1)},
		{"non-object issue", strings.Replace(minimalPlanJSON, `"issues": [`, `"issues": [42,`, 1)},
		{"non-object facts", strings.Replace(minimalPlanJSON, `{"read_only": false}`, `[]`, 1)},
		{"non-object downgrade", strings.Replace(minimalPlanJSON, `"read_only": false`, `"downgrade":[]`, 1)},
		{"read-only work", strings.Replace(minimalPlanJSON, `"read_only": false`, `"read_only":true`, 1)},
		{"unknown risk", strings.Replace(minimalPlanJSON, `"read_only": false`, `"risk_domains":["unknown"]`, 1)},
		{"missing required tests", strings.Replace(minimalPlanJSON, `["go test ./..."]`, `[]`, 1)},
		{"invalid test declaration", strings.Replace(minimalPlanJSON, `"wave": 1`, `"wave":1,"tests_ci_does_not_run":["not a required test"]`, 1)},
		{"invalid issue type", strings.Replace(minimalPlanJSON, `"type": "bug"`, `"type":"unknown"`, 1)},
		{"empty objective", strings.Replace(minimalPlanJSON, `"Make status reporting race-free"`, `""`, 1)},
	} {
		t.Run(tc.name, func(t *testing.T) { mcpPlanValidationParity(t, ctx, root, session, tc.plan, false) })
	}
	for _, tc := range []struct {
		name   string
		valid  bool
		mutate func(*run.PlanDoc)
	}{
		{"valid dependency waves and declarations", true, func(*run.PlanDoc) {}},
		{"duplicate issue id", false, func(p *run.PlanDoc) { p.Issues[1].ID = p.Issues[0].ID }},
		{"missing dependency", false, func(p *run.PlanDoc) { p.Issues[1].DependsOn = []string{"missing"} }},
		{"self dependency", false, func(p *run.PlanDoc) { p.Issues[1].DependsOn = []string{"second"} }},
		{"same-wave dependency", false, func(p *run.PlanDoc) { p.Issues[1].Wave = 1 }},
		{"cyclic dependency", false, func(p *run.PlanDoc) { p.Issues[0].DependsOn = []string{"second"} }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p, err := run.DecodePlan([]byte(minimalPlanJSON))
			if err != nil {
				t.Fatal(err)
			}
			p.Summary = "Synthetic validation fixture, not approval or a CI coverage claim"
			p.Issues[0].Facts.RiskDomains = []string{"authorization"}
			p.Issues[0].TestsCIDoesNotRun = []string{"go test ./..."}
			p.Issues[0].Fixes = []int{123}
			second := p.Issues[0]
			second.ID, second.Wave, second.DependsOn = "second", 2, []string{p.Issues[0].ID}
			p.Issues = append(p.Issues, second)
			tc.mutate(p)
			data, err := json.Marshal(p)
			if err != nil {
				t.Fatal(err)
			}
			mcpPlanValidationParity(t, ctx, root, session, string(data), tc.valid)
		})
	}
	for _, args := range []any{
		map[string]any{}, json.RawMessage("null"), []any{}, "not an object", 1, true,
		map[string]any{"plan": nil}, map[string]any{"plan": []any{}}, map[string]any{"plan": "{malformed"},
		map[string]any{"plan": 1}, map[string]any{"plan": true},
		map[string]any{"plan": json.RawMessage(minimalPlanJSON), "repo": root},
		map[string]any{"plan": json.RawMessage(minimalPlanJSON), "repo_root": root},
		map[string]any{"plan": json.RawMessage(minimalPlanJSON), "unexpected": true},
	} {
		result := mcpCall(t, ctx, session, root, "orch_plan_validate", args)
		if !result.IsError || result.StructuredContent != nil {
			t.Fatalf("plan validation accepted %#v: %+v", args, result)
		}
	}
}

func mcpPlanValidationParity(t *testing.T, ctx context.Context, root string, session *mcp.ClientSession, data string, valid bool) {
	t.Helper()
	cfg, wantErr := config.Load(root)
	var plan *run.PlanDoc
	if wantErr == nil {
		plan, wantErr = run.DecodePlan([]byte(data))
		if wantErr == nil {
			wantErr = plan.Validate(cfg)
		}
	}
	if (wantErr == nil) != valid {
		t.Fatalf("fixture validity = %t, want %t: %v", wantErr == nil, valid, wantErr)
	}
	result := mcpCall(t, ctx, session, root, "orch_plan_validate", map[string]any{"plan": json.RawMessage(data)})
	if len(result.Content) != 1 {
		t.Fatalf("validation result = %+v", result)
	}
	text, ok := result.Content[0].(*mcp.TextContent)
	if !ok {
		t.Fatalf("validation content = %T", result.Content[0])
	}
	if wantErr != nil {
		if !result.IsError || result.StructuredContent != nil || text.Text != wantErr.Error() {
			t.Fatalf("validation error mismatch: shared=%v MCP=%+v text=%s", wantErr, result, text.Text)
		}
		t.Logf("decoder/validator/configuration error = MCP error; unchanged files: %s", text.Text)
		return
	}
	// Assert the entire transport result, including no digest, readiness,
	// contributed gate criteria, routing, approval or dispatch fields.
	wantJSON, err := json.Marshal(map[string]any{
		"schema_version": 1, "validation_scope": "structural-configuration-only", "config_revision": cfg.ConfigRevision, "plan": plan,
	})
	if err != nil {
		t.Fatal(err)
	}
	want := mcpDecoded(t, wantJSON)
	if result.IsError || !reflect.DeepEqual(result.StructuredContent, want) || !reflect.DeepEqual(mcpDecoded(t, []byte(text.Text)), want) {
		t.Fatalf("validation mismatch: shared=%s structured=%v text=%s", wantJSON, result.StructuredContent, text.Text)
	}
	t.Logf("DecodePlan + Validate + config.Load = structured = JSON text; config=%s; no authority or writes", cfg.ConfigRevision)
}

func mcpPlanValidationRawStdio(t *testing.T, ctx context.Context, binary, cwd, root string) {
	t.Helper()
	before, cwdBefore := mcpTree(t, root), mcpTree(t, cwd)
	// Raw JSON exercises omitted arguments and malformed requests without the
	// SDK client's nil-to-{} substitution or JSON marshaling protections.
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
	send := func(data string) {
		t.Helper()
		if err := enc.Encode(json.RawMessage(data)); err != nil {
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
	send(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"raw-validation-test","version":"1"}}}`)
	if initialized := receive(); initialized["result"] == nil || initialized["error"] != nil {
		t.Fatalf("raw initialize = %+v", initialized)
	}
	send(`{"jsonrpc":"2.0","method":"notifications/initialized"}`)
	send(`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"orch_plan_validate"}}`)
	omitted := receive()
	result, ok := omitted["result"].(map[string]any)
	if !ok || result["isError"] != true || result["structuredContent"] != nil {
		t.Fatalf("raw omitted validation arguments = %+v", omitted)
	}
	if err := input.Close(); err != nil {
		t.Fatal(err)
	}
	err = cmd.Wait()
	waited = true
	if err != nil || stderr.Len() != 0 {
		t.Fatalf("raw validation shutdown: err=%v stderr=%s", err, stderr.String())
	}
	// Truncated JSON is rejected by the existing stdio transport itself: it
	// exits with an explicit error, rather than producing a validation receipt.
	malformed := mcpProcess(ctx, binary, cwd, "mcp", "--repo", root)
	malformed.Stdin = strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"orch_plan_validate","arguments":{"plan":` + "\n")
	var stdout, malformedStderr bytes.Buffer
	malformed.Stdout, malformed.Stderr = &stdout, &malformedStderr
	if err := malformed.Run(); err == nil || malformed.ProcessState == nil || malformed.ProcessState.ExitCode() != ExitError || !strings.Contains(malformedStderr.String(), "unexpected EOF") || stdout.Len() != 0 {
		t.Fatalf("malformed JSON rejection: err=%v stdout=%s stderr=%s", err, stdout.String(), malformedStderr.String())
	}
	if mcpTree(t, root) != before || mcpTree(t, cwd) != cwdBefore {
		t.Fatal("raw validation changed repository files")
	}
	t.Log("raw stdio: omitted arguments are a tool error; malformed JSON is an explicit transport error; unchanged files")
}
