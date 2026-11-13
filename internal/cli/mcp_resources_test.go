package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/kninetimmy/orch/internal/config"
	"github.com/kninetimmy/orch/internal/lockfile"
	"github.com/kninetimmy/orch/internal/manifest"
	"github.com/kninetimmy/orch/internal/paths"
	"github.com/kninetimmy/orch/internal/run"
	"github.com/kninetimmy/orch/internal/state"
	"github.com/modelcontextprotocol/go-sdk/jsonrpc"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func mcpDiscoverResources(t *testing.T, ctx context.Context, session *mcp.ClientSession, root string) {
	t.Helper()
	before := mcpTree(t, root)
	caps := session.InitializeResult().Capabilities
	if caps.Logging == nil { //nolint:staticcheck // SA1019: SDK v1.8.0 has no non-deprecated way to assert retained logging.
		t.Fatal("resource increment removed existing logging capability")
	}
	if caps.Resources == nil || caps.Resources.Subscribe || caps.Resources.ListChanged || caps.Prompts != nil ||
		caps.Tools == nil || !caps.Tools.ListChanged {
		t.Fatalf("resource increment changed unexpected capabilities: %+v", caps)
	}
	list, err := session.ListResources(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{
		"orch://run/status":       "orch_run_status",
		"orch://run/next":         "orch_run_next",
		"orch://config/effective": "orch_config_effective",
		"orch://metrics/history":  "orch_metrics",
	}
	if len(list.Resources) != len(want) || list.NextCursor != "" {
		t.Fatalf("resource discovery = %+v", list)
	}
	for _, resource := range list.Resources {
		name, ok := want[resource.URI]
		if !ok || resource.Name != name || resource.MIMEType != "application/json" || resource.Description == "" {
			t.Fatalf("resource = %+v", resource)
		}
		delete(want, resource.URI)
	}
	templates, err := session.ListResourceTemplates(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(templates.ResourceTemplates) != 1 || templates.NextCursor != "" {
		t.Fatalf("resource template discovery = %+v", templates)
	}
	template := templates.ResourceTemplates[0]
	if template.URITemplate != "orch://run/issues/{issue_number}" || template.Name != "orch_issue_context" ||
		template.MIMEType != "application/json" || template.Description == "" {
		t.Fatalf("resource template = %+v", template)
	}
	if mcpTree(t, root) != before {
		t.Fatal("resource discovery changed repository files")
	}
}

// Run under both real stdio (with empty PATH) and the rejecting LookPath/Runner
// fixture. Compare entire documents and errors, not a second report projection.
func mcpResourceReports(t *testing.T, ctx context.Context, root string, session *mcp.ClientSession, number int) {
	t.Helper()
	root, err := paths.Canonical(root)
	if err != nil {
		t.Fatal(err)
	}
	before := mcpTree(t, root)
	status, statusErr := run.Status(ctx, run.Env{RepoRoot: root})
	next, nextErr := run.Next(ctx, run.Env{RepoRoot: root})
	cfg, configErr := config.Load(root)
	history, metricsErr := inspectMetrics(root)
	issue, issueErr := run.IssueContext(ctx, run.Env{RepoRoot: root}, number)
	for _, tc := range []struct {
		uri, tool string
		args      any
		doc       any
		err       error
	}{
		{"orch://run/status", "orch_run_status", map[string]any{}, status, statusErr},
		{"orch://run/next", "orch_run_next", map[string]any{}, next, nextErr},
		{"orch://config/effective", "orch_config_effective", map[string]any{}, map[string]any{"schema_version": 1, "configuration": cfg}, configErr},
		{"orch://metrics/history", "orch_metrics", map[string]any{}, history, metricsErr},
		{fmt.Sprintf("orch://run/issues/%d", number), "orch_issue_context", map[string]any{"issue_number": number}, issue, issueErr},
	} {
		result := mcpCall(t, ctx, session, root, tc.tool, tc.args)
		if len(result.Content) != 1 {
			t.Fatalf("%s tool result = %+v", tc.tool, result)
		}
		text, ok := result.Content[0].(*mcp.TextContent)
		if !ok {
			t.Fatalf("%s tool content = %T", tc.tool, result.Content[0])
		}
		resource, readErr := session.ReadResource(ctx, &mcp.ReadResourceParams{URI: tc.uri})
		if mcpTree(t, root) != before {
			t.Fatalf("%s resource read changed repository files", tc.uri)
		}
		if tc.err != nil {
			var rpcErr *jsonrpc.Error
			if !result.IsError || result.StructuredContent != nil || text.Text != tc.err.Error() ||
				resource != nil || !errors.As(readErr, &rpcErr) || rpcErr.Message != tc.err.Error() {
				t.Fatalf("%s error parity: shared=%v tool=%+v resource=%+v err=%v", tc.uri, tc.err, result, resource, readErr)
			}
			t.Logf("%s: shared = tool = resource error; unchanged files: %v", tc.uri, tc.err)
			continue
		}
		if readErr != nil || resource == nil || len(resource.Contents) != 1 || resource.TTLMs != 0 || resource.CacheScope != "private" {
			t.Fatalf("%s resource=%+v err=%v", tc.uri, resource, readErr)
		}
		content := resource.Contents[0]
		data, err := json.Marshal(tc.doc)
		if err != nil {
			t.Fatal(err)
		}
		want := mcpDecoded(t, data)
		if result.IsError || content.URI != tc.uri || content.MIMEType != "application/json" || content.Blob != nil ||
			!reflect.DeepEqual(result.StructuredContent, want) || !reflect.DeepEqual(mcpDecoded(t, []byte(text.Text)), want) ||
			!reflect.DeepEqual(mcpDecoded(t, []byte(content.Text)), want) {
			t.Fatalf("%s parity: shared=%s tool=%+v resource=%+v", tc.uri, data, result, resource)
		}
		t.Logf("%s: shared = tool structured = tool text = application/json resource; unchanged files", tc.uri)
	}
}

func mcpResourceRejectURIs(t *testing.T, ctx context.Context, root string, session *mcp.ClientSession) {
	t.Helper()
	before := mcpTree(t, root)
	uris := []string{
		"", "orch://", "orch:/run/status", "orch:run/status", "/etc/passwd", "file:///etc/passwd",
		filepath.Join(root, config.Path), "file://" + filepath.ToSlash(filepath.Join(root, config.Path)),
		"orch://run/issues/{issue_number}", "orch://run/activate", "orch://run/dispatch",
		"orch://run/readiness", "orch://approval/status", "orch://metrics/report",
		"orch://eval/preview", "orch://configure/step", "orch://configure-local/step", "orch://plan/gate",
		"orch://run/%73tatus", "orch://%72un/status", "orch://config/%65ffective",
		"orch://metrics/%68istory", "orch://run/../config/effective",
		"orch://run/%2e%2e/config/effective", "orch://run/issues/../status",
		"orch://run/status\x00", "orch://run/next\a", "orch://config/effective\v", "orch://metrics/history\x7f",
	}
	for _, uri := range []string{"orch://run/status", "orch://run/next", "orch://config/effective", "orch://metrics/history", "orch://run/issues/1"} {
		authority, path, _ := strings.Cut(strings.TrimPrefix(uri, "orch://"), "/")
		uris = append(uris, uri+"/", uri+"?", uri+"?repo="+root, uri+"#", uri+"#fragment",
			" "+uri, uri+" ", strings.Replace(uri, "orch:", "ORCH:", 1),
			"orch://"+strings.ToUpper(authority)+"/"+path, "orch://user@"+authority+"/"+path,
			"orch://"+authority+":80/"+path, "orch://"+authority+".other/"+path,
			"orch://"+authority+"//"+path, "orch://"+authority+"/./"+path,
			"orch://"+authority+`\`+path, "orch:///"+authority+"/"+path)
	}
	for _, number := range []string{
		"0", "-1", "+1", "01", "1.0", "1e0", " 1", "1 ", "１", "١",
		"%31", "%2531", "%2b1", "1%2f2", "1/../../config/effective",
		"99999999999999999999999999999999999999999", "1\x00",
	} {
		uris = append(uris, "orch://run/issues/"+number)
	}
	for _, uri := range uris {
		result, err := session.ReadResource(ctx, &mcp.ReadResourceParams{URI: uri})
		var rpcErr *jsonrpc.Error
		if result != nil || !errors.As(err, &rpcErr) || rpcErr.Code != jsonrpc.CodeInvalidParams || rpcErr.Message != "Resource not found" {
			t.Fatalf("noncanonical URI %q: result=%+v err=%v", uri, result, err)
		}
		if !reflect.DeepEqual(mcpDecoded(t, rpcErr.Data), map[string]any{"uri": uri}) {
			t.Fatalf("resource-not-found error lost original URI %q: %+v", uri, rpcErr)
		}
		if mcpTree(t, root) != before {
			t.Fatalf("rejected resource %q changed repository files", uri)
		}
	}
	t.Logf("rejected %d noncanonical/unsupported URIs without exposing files", len(uris))
}

func mcpResourcesExercise(t *testing.T, binary string) {
	t.Helper()
	root, cwd := t.TempDir(), t.TempDir()
	writeConfigOnly(t, root, "invalid configuration [")
	writeConfig(t, cwd, strings.Replace(validTOML, `"r1"`, `"launching-repository"`, 1))
	mcpWrite(t, filepath.Join(root, state.Path), []byte("{corrupt state"))
	mcpWrite(t, filepath.Join(root, lockfile.Path), []byte("{corrupt lock"))
	mcpWrite(t, filepath.Join(cwd, "unrelated.txt"), []byte("must never be exposed"))
	cwdBefore := mcpTree(t, cwd)
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	session, closeSession := mcpConnect(t, ctx, binary, cwd, root, "--repo", root)
	mcpDiscover(t, ctx, session, root) // Invalid reports cannot prevent discovery.
	mcpResourceReports(t, ctx, root, session, 1)
	mcpResourceRejectURIs(t, ctx, root, session)
	current := strings.Replace(validTOML, `mode = "off"`, `mode = "required"`, 1) + "\n[metrics]\nenabled = true\n"
	mcpWrite(t, filepath.Join(root, config.Path), []byte(current))
	if err := state.Save(root, &state.State{SchemaVersion: state.SchemaVersion, Mode: state.ModeAssist}); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(root, lockfile.Path)); err != nil {
		t.Fatal(err)
	}
	mcpResourceReports(t, ctx, root, session, 1) // Assist issue error, empty history.
	// Build synthetic approval metadata through the shared gate with the
	// existing fake probes; required memhub must never reach a real process.
	gate, err := run.Plan(ctx, run.Env{RepoRoot: root, Runner: fakeRunner{}}, []byte(minimalPlanJSON))
	if err != nil {
		t.Fatal(err)
	}
	ref := testPlanRef()
	ref.ContractVersion = run.ContractVersion
	ref.ExecutionDigest, err = gate.Execution.Digest()
	if err != nil {
		t.Fatal(err)
	}
	st, err := state.EnterDelivery(root, "claude", ref, testIssues())
	if err != nil {
		t.Fatal(err)
	}
	issue := &st.Run.Issues[0]
	issue.Number, issue.Phase = 1, state.PhaseWorktreeReady
	issue.Branch, issue.Worktree = "orch/issue-1", ".orchestrator/worktrees/issue-1"
	issue.Objective, issue.AcceptanceCriteria = "Preserve current context: café / 日本語.", []string{"No authority"}
	issue.RequiredTests = []string{"go test ./..."}
	issue.Decision = &state.Decision{Role: manifest.RoleImplementer,
		Executor: manifest.Selection{Model: "claude-sonnet-5", Effort: "xhigh"},
		Reviewer: manifest.Selection{Model: "claude-opus-4-8", Effort: "high"}, Rationale: "fixture"}
	save := func() {
		t.Helper()
		if err := state.Save(root, st); err != nil {
			t.Fatal(err)
		}
	}
	save()
	for _, doc := range mcpMetricsFixtures(t) {
		writeMetricsFixture(t, root, doc.RunID, doc)
	}
	mcpResourceReports(t, ctx, root, session, 1)
	mcpResourceReports(t, ctx, root, session, 999) // Valid URI, unknown issue error.
	issue.Phase, issue.Decision.Role = state.PhaseDispatched, manifest.RoleSpecialist
	issue.Decision.Executor = manifest.Selection{Model: "claude-opus-4-8", Effort: "high"}
	save()
	mcpResourceReports(t, ctx, root, session, 1) // Fresh phase and routing.
	st.Run.StoppedReason = "fixture stop"
	save()
	mcpResourceReports(t, ctx, root, session, 1) // Run-level guidance retained.
	st.Run.StoppedReason, issue.Decision, issue.Objective = "", nil, ""
	issue.Phase = state.PhaseWorktreeReady
	save()
	mcpResourceReports(t, ctx, root, session, 1) // Missing work/routing stays missing.
	local := filepath.Join(root, config.LocalOverridePath)
	mcpWrite(t, local, []byte("[concurrency]\nmax_subagents = 7\n[metrics]\nenabled = false\n"))
	mcpResourceReports(t, ctx, root, session, 1) // Fresh local settings/drift/history.
	for _, data := range []string{"invalid local [", "[merge]\nstrategy = \"rebase\"\n"} {
		mcpWrite(t, local, []byte(data))
		mcpResourceReports(t, ctx, root, session, 1)
	}
	if err := os.Remove(local); err != nil {
		t.Fatal(err)
	}
	for _, data := range []string{
		strings.Replace(current, `"r1"`, `"current-files"`, 1), "invalid committed [",
		strings.Replace(current, "schema_version  = 1", "schema_version  = 99", 1),
	} {
		mcpWrite(t, filepath.Join(root, config.Path), []byte(data))
		mcpResourceReports(t, ctx, root, session, 1)
		mcpDiscover(t, ctx, session, root)
	}
	if err := os.Remove(filepath.Join(root, config.Path)); err != nil {
		t.Fatal(err)
	}
	mcpResourceReports(t, ctx, root, session, 1)
	mcpWrite(t, filepath.Join(root, config.Path), []byte(current))
	errorPath := filepath.Join(root, ".orchestrator", "metrics", "run-error.json")
	for _, data := range []string{
		"{corrupt metrics", `{"schema_version":99,"run_id":"run-error"}`,
		`{"schema_version":1,"run_id":"run-wrong"}`,
	} {
		mcpWrite(t, errorPath, []byte(data))
		mcpResourceReports(t, ctx, root, session, 1)
	}
	if err := os.Remove(errorPath); err != nil {
		t.Fatal(err)
	}
	lockPath := filepath.Join(root, lockfile.Path)
	owner, err := os.ReadFile(lockPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(lockPath); err != nil {
		t.Fatal(err)
	}
	mcpResourceReports(t, ctx, root, session, 1) // Inconsistency is not an error.
	mcpWrite(t, lockPath, []byte("{corrupt lock"))
	mcpResourceReports(t, ctx, root, session, 1)
	mcpWrite(t, lockPath, owner)
	for _, data := range []string{"{corrupt state", `{"schema_version":999,"mode":"assist"}`} {
		mcpWrite(t, filepath.Join(root, state.Path), []byte(data))
		mcpResourceReports(t, ctx, root, session, 1)
		mcpDiscover(t, ctx, session, root)
	}
	save()
	st.Run.Issues = append(st.Run.Issues, st.Run.Issues[0])
	save()
	mcpResourceReports(t, ctx, root, session, 1) // Identity ambiguity remains an error.
	st.Run.Issues = st.Run.Issues[:1]
	save()
	mcpResourceReports(t, ctx, root, session, 1) // Same-connection recovery.
	mcpDiscover(t, ctx, session, root)
	if mcpTree(t, cwd) != cwdBefore {
		t.Fatal("resource operations changed launching repository")
	}
	closeSession()
	mcpResourcesRawStdio(t, ctx, binary, cwd, root)
}

func mcpResourcesRawStdio(t *testing.T, ctx context.Context, binary, cwd, root string) {
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
	exchange := func(request string) map[string]any {
		t.Helper()
		if err := enc.Encode(json.RawMessage(request)); err != nil {
			t.Fatal(err)
		}
		var response map[string]any
		if err := dec.Decode(&response); err != nil {
			t.Fatal(err)
		}
		return response
	}
	// Exercise actual legacy subscription methods. The newer SDK Subscribe
	// instead uses subscriptions/listen, filtering unsupported capabilities;
	// Unsubscribe then only cancels client-local bookkeeping.
	initialized := exchange(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-11-25","capabilities":{},"clientInfo":{"name":"raw-resource-test","version":"1"}}}`)
	if initialized["result"] == nil || initialized["error"] != nil {
		t.Fatalf("raw resource initialize = %+v", initialized)
	}
	if err := enc.Encode(json.RawMessage(`{"jsonrpc":"2.0","method":"notifications/initialized"}`)); err != nil {
		t.Fatal(err)
	}
	for _, method := range []string{"resources/subscribe", "resources/unsubscribe"} {
		response := exchange(fmt.Sprintf(`{"jsonrpc":"2.0","id":2,"method":%q,"params":{"uri":"orch://run/status"}}`, method))
		failure, ok := response["error"].(map[string]any)
		if !ok || failure["code"] != float64(jsonrpc.CodeMethodNotFound) || response["result"] != nil {
			t.Fatalf("raw %s unexpectedly supported: %+v", method, response)
		}
	}
	if err := input.Close(); err != nil {
		t.Fatal(err)
	}
	err = cmd.Wait()
	waited = true
	if err != nil || stderr.Len() != 0 {
		t.Fatalf("raw resource shutdown: err=%v stderr=%s", err, stderr.String())
	}
	if mcpTree(t, root) != before || mcpTree(t, cwd) != cwdBefore {
		t.Fatal("raw resource requests changed repository files")
	}
	t.Log("raw stdio: legacy subscriptions are unsupported; clean shutdown and unchanged files")
}
