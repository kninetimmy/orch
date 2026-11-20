package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/kninetimmy/orch/internal/config"
	"github.com/kninetimmy/orch/internal/execx/execxtest"
	"github.com/kninetimmy/orch/internal/lockfile"
	"github.com/kninetimmy/orch/internal/manifest"
	"github.com/kninetimmy/orch/internal/metrics"
	"github.com/kninetimmy/orch/internal/paths"
	"github.com/kninetimmy/orch/internal/run"
	"github.com/kninetimmy/orch/internal/state"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const mcpActivationConfig = validTOML + "\n[metrics]\nenabled = true\n"

func TestMCPActivationHelp(t *testing.T) {
	env, stdout, stderr := testEnv(t)
	if code := Run([]string{"help"}, env); code != ExitOK || stderr.Len() != 0 ||
		!strings.Contains(stdout.String(), "plan decision gates, human-approved activation and issue dispatch over stdio") {
		t.Fatalf("activation help: stdout=%s stderr=%s exit=%d", stdout, stderr, code)
	}
}

func mcpActivationSchema(t *testing.T, tool *mcp.Tool) {
	t.Helper()
	if tool.Annotations.ReadOnlyHint || tool.Annotations.IdempotentHint ||
		tool.Annotations.DestructiveHint == nil || !*tool.Annotations.DestructiveHint ||
		tool.Annotations.OpenWorldHint == nil || !*tool.Annotations.OpenWorldHint {
		t.Fatalf("activation mutation annotations = %+v", tool.Annotations)
	}
	for _, disclosure := range []string{"human", "Non-idempotent", "GitHub", "Partial failures", "never automatically retry", "Does not dispatch"} {
		if !strings.Contains(tool.Description, disclosure) {
			t.Fatalf("activation description omits %q: %s", disclosure, tool.Description)
		}
	}
	schema, ok := tool.OutputSchema.(map[string]any)
	if !ok || schema["type"] != "object" {
		t.Fatalf("activation output schema = %#v", tool.OutputSchema)
	}
	properties, ok := schema["properties"].(map[string]any)
	if !ok || len(properties) != 4 {
		t.Fatalf("activation output properties = %#v", schema)
	}
	for _, field := range []string{"schema_version", "run_id", "issues", "warnings"} {
		if properties[field] == nil {
			t.Fatalf("activation output omits %s: %#v", field, schema)
		}
	}
}

func mcpActivationRequest(t *testing.T, root string) []byte {
	t.Helper()
	return mcpApprovalForGate(t, mcpFixtureGate(t, root, []byte(minimalPlanJSON)), mustMCPActivationPlan(t))
}

func mcpApprovalForGate(t *testing.T, gate *run.GateDoc, plan *run.PlanDoc) []byte {
	t.Helper()
	// Synthetic human evidence is supplied explicitly, never inferred from
	// the successful gate. No test invokes a live GitHub or host command.
	data, err := json.Marshal(run.ActivationRequest{
		SchemaVersion: run.ActivationSchemaVersion, Plan: *plan,
		Approval: run.Approval{PlanDigest: gate.PlanDigest, Target: gate.Target, ApprovedBy: "fixture-human",
			ApprovedAt: time.Date(2026, 11, 13, 12, 0, 0, 0, time.UTC), Statement: run.ApprovalStatement},
	})
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func mustMCPActivationPlan(t *testing.T) *run.PlanDoc {
	t.Helper()
	plan, err := run.DecodePlan([]byte(minimalPlanJSON))
	if err != nil {
		t.Fatal(err)
	}
	return plan
}

const mcpActivationGHHost = "github.com"
const mcpActivationRepo = mcpActivationGHHost + "/fixture/isolated"

func mcpActivationGraphQLArgs() []string {
	return []string{"api", "graphql", "--hostname", mcpActivationGHHost, "--method", "POST", "--header", "Content-Type: application/json", "--input", "-"}
}

func mcpActivationLabelPage(repoID string) string {
	var nodes []string
	for _, name := range []string{"in-progress", "blocked", "needs-human", "awaiting-review", "delivered", "feature", "bug",
		"chore", "infra", "docs", "research", "implementer", "specialist", "standard", "critical"} {
		nodes = append(nodes, fmt.Sprintf(`{"id":"LA_%s","name":"%s"}`, name, name))
	}
	return fmt.Sprintf(`{"data":{"node":{"id":%q,"labels":{"nodes":[%s],"pageInfo":{"hasNextPage":false}}}}}`, repoID, strings.Join(nodes, ","))
}

const mcpActivationLabelReply = `{"data":{"createLabel":{"label":{"id":"LA_ready","name":"ready"}}}}`

func mcpActivationIssueReply(repo, repoID string) string {
	return fmt.Sprintf(`{"data":{"createIssue":{"issue":{"number":71,"url":"https://%s/issues/71","repository":{"id":%q}}}}}`, repo, repoID)
}

// Exact activation transcript, shared by the engine's scripted runner and the
// existing compiled doctor-probe executable. Git mutations are simulated too:
// PATH contains only these fixture executables, never production git/gh/hosts.
func mcpActivationCalls(root, scenario string, probe bool) []execxtest.Call {
	graphQL := func(operation, reply string) execxtest.Call {
		args := mcpActivationGraphQLArgs()
		if probe {
			// The compiled probe keys the otherwise identical command lines
			// on operationName in stdin, not an extra process argument.
			args = append(args, operation)
		}
		return execxtest.Call{Name: "gh", Args: args, Stdout: reply}
	}
	tail := []execxtest.Call{
		{Name: "git", Args: []string{"status", "--porcelain=v1", "--untracked-files=all"}},
		{Name: "git", Args: []string{"symbolic-ref", "--short", "HEAD"}, Stdout: "main"},
		{Name: "git", Args: []string{"check-ignore", "-q", "--", ".orchestrator/worktrees/orch-check-ignore-probe"}},
		{Name: "git", Args: []string{"check-ignore", "-q", "--", ".orchestrator/worktrees/0"}},
		graphQL("OrchActivationLabels", mcpActivationLabelPage(mcpGitHubRepoID)),
		graphQL("OrchCreateLabel", mcpActivationLabelReply),
		graphQL("OrchCreateIssue", mcpActivationIssueReply(mcpActivationRepo, mcpGitHubRepoID)),
		{Name: "git", Args: []string{"check-ignore", "-q", "--", ".orchestrator/worktrees/issue-71/orch-check-ignore-probe"}},
		{Name: "git", Args: []string{"check-ignore", "-q", "--", ".orchestrator/worktrees/issue-71/0"}},
		{Name: "git", Args: []string{"rev-parse", "--verify", "--quiet", "refs/heads/orch/issue-71-fix-the-status-lock-race"}, Exit: 1},
		{Name: "git", Args: []string{"worktree", "add", "-b", "orch/issue-71-fix-the-status-lock-race", filepath.Join(root, ".orchestrator", "worktrees", "issue-71"), "main"}},
		{Name: "git", Args: []string{"rev-parse", "--verify", "orch/issue-71-fix-the-status-lock-race^{commit}"}, Stdout: strings.Repeat("a", 40)},
	}
	calls := append(mcpTargetCalls(root), tail...)
	failAt := -1
	switch scenario {
	case "dirty checkout":
		calls[5].Stdout, failAt = " M README.md", 5
	case "authentication":
		failAt = 3
	case "non-default branch":
		calls[6].Stdout, failAt = "feature", 6
	case "label failure":
		failAt = 10
	case "issue failure":
		failAt = 11
	case "worktree failure":
		failAt = 15
	}
	if failAt >= 0 {
		if scenario != "dirty checkout" && scenario != "non-default branch" {
			calls[failAt].Exit, calls[failAt].Stderr = 1, "fixture external failure"
		}
		calls = calls[:failAt+1]
	}
	for i := range calls {
		calls[i].Dir = root
	}
	return calls
}

func mcpActivationExercise(t *testing.T, binary string) {
	t.Helper()
	probes := mcpProbeBin(t, "git", "gh", "opencode2")
	for _, scenario := range []string{"success", "dirty checkout", "authentication", "non-default branch", "label failure", "issue failure", "worktree failure"} {
		t.Run(scenario, func(t *testing.T) {
			root, sharedRoot, cwd := t.TempDir(), t.TempDir(), t.TempDir()
			for _, repo := range []string{root, sharedRoot} {
				writeConfig(t, repo, mcpActivationConfig)
				mcpWrite(t, filepath.Join(repo, ".gitignore"), []byte(".orchestrator/worktrees/\n.orchestrator/metrics/\n"))
			}
			writeConfigOnly(t, cwd, "invalid launching configuration [")
			mcpWrite(t, filepath.Join(cwd, state.Path), []byte("{corrupt launching state"))
			root, err := paths.Canonical(root)
			if err != nil {
				t.Fatal(err)
			}
			sharedRoot, err = paths.Canonical(sharedRoot)
			if err != nil {
				t.Fatal(err)
			}
			request := mcpActivationRequest(t, root)
			sharedRequest := mcpActivationRequest(t, sharedRoot)
			if reflect.DeepEqual(request, sharedRequest) {
				t.Fatal("isolated roots reused the same approval")
			}
			workspace := mcpWorkspaceCalls(root, mcpMatchingHost(t, os.Getpid(), root))
			calls := append(slices.Clone(workspace), mcpActivationCalls(root, scenario, true)...)
			logPath := filepath.Join(t.TempDir(), "commands.log")
			fixture := mcpProbeFile(t, root, logPath, calls)
			ctx, cancel := context.WithTimeout(t.Context(), 60*time.Second)
			defer cancel()
			cmd := mcpProcess(ctx, binary, cwd, "mcp", "--repo", root)
			cmd.Env = append(cmd.Env, "PATH="+probes, "ORCH_TEST_DOCTOR_FIXTURE="+fixture)
			session, closeSession := mcpConnectCommand(t, ctx, cmd, root)
			cwdBefore := mcpTree(t, cwd)
			mcpDiscover(t, ctx, session, root)
			if _, err := os.Stat(logPath); !os.IsNotExist(err) {
				t.Fatalf("initialization/discovery ran commands: %v", err)
			}
			var prior []byte
			if scenario == "success" {
				mcpActivationRefusals(t, ctx, root, session, request)
				prior = mcpReadProbeLog(t, logPath)
				mcpOnlyProbes(t, prior, append(slices.Clone(workspace), mcpTargetCalls(root)...))
			}
			script := &execxtest.Script{T: t, Calls: mcpActivationCalls(sharedRoot, scenario, false)}
			want, wantErr := run.Activate(ctx, run.Env{RepoRoot: sharedRoot, Runner: script,
				ClaudeSession: "aaaaaaaa-0000-4000-8000-000000000001"}, sharedRequest)
			script.AssertExhausted()
			result, err := session.CallTool(ctx, &mcp.CallToolParams{Meta: mcpWorkspaceMeta(), Name: "orch_run_activate", Arguments: json.RawMessage(request)})
			if err != nil {
				t.Fatal(err)
			}
			if wantErr != nil {
				expected := strings.ReplaceAll(wantErr.Error(), sharedRoot, root)
				mcpEvalError(t, result, expected)
				if text := result.Content[0].(*mcp.TextContent).Text; text != expected {
					t.Fatalf("engine/MCP error mismatch: want %q, got %q", expected, text)
				}
				if scenario == "issue failure" || scenario == "worktree failure" {
					for _, recovery := range []string{"state and the delivery lock are preserved", "orch abort", "orch resume"} {
						if !strings.Contains(expected, recovery) {
							t.Fatalf("partial failure lost recovery guidance: %s", expected)
						}
					}
				}
				t.Logf("shared engine = real-stdio MCP refusal: %s", expected)
			} else {
				if result.IsError || len(result.Content) != 1 {
					t.Fatalf("activation result = %+v", result)
				}
				text, ok := result.Content[0].(*mcp.TextContent)
				if !ok {
					t.Fatalf("activation content = %T", result.Content[0])
				}
				var got run.ActivationResult
				if err := json.Unmarshal([]byte(text.Text), &got); err != nil {
					t.Fatal(err)
				}
				if got.SchemaVersion != run.ActivationSchemaVersion || got.RunID == "" || got.RunID == want.RunID {
					t.Fatalf("activation schema/independent run identities: got=%+v shared=%+v", got, want)
				}
				// Only the fresh run ID differs across the independent activations.
				want.RunID = got.RunID
				if expected := mcpDecoded(t, mcpEvalBytes(t, want)); !reflect.DeepEqual(result.StructuredContent, expected) ||
					!reflect.DeepEqual(mcpDecoded(t, []byte(text.Text)), expected) {
					t.Fatalf("engine/structured/text parity: shared=%+v MCP=%+v", want, result)
				}
				var request struct {
					Variables struct{ Input struct{ Body string } }
				}
				if err := json.Unmarshal([]byte(script.StdinAt(11)), &request); err != nil {
					t.Fatalf("decode issue mutation payload: %v", err)
				}
				m, err := manifest.Parse(request.Variables.Input.Body)
				if err != nil || m.Objective != mustMCPActivationPlan(t).Issues[0].Objective || m.Executor.Model == "" {
					t.Fatalf("shared engine activation audit lost approved work: %+v err=%v", m, err)
				}
				t.Log("shared engine = real-stdio structured = JSON text (independent run IDs); explicit approval and protocol-only stdout")
			}
			log := mcpReadProbeLog(t, logPath)
			if want := string(prior) + mcpProbeLog(calls); string(log) != want {
				t.Fatalf("activation commands escaped binding, retried or exceeded engine transcript:\ngot %s\nwant %s", log, want)
			}
			// Fresh status, tools and all resources still use their ordinary
			// read-only reports, even after success or interrupted activation.
			mcpActivationStateParity(t, ctx, binary, root, sharedRoot, session, request)
			mcpOnlyProbes(t, mcpReadProbeLog(t, logPath)[len(log):], workspace)
			log = mcpReadProbeLog(t, logPath)
			mcpDiscover(t, ctx, session, root)
			closeSession()
			after, err := os.ReadFile(logPath)
			if err != nil || string(after) != string(log) || mcpTree(t, cwd) != cwdBefore {
				t.Fatalf("inspection/disconnect ran commands or changed launching checkout: %v", err)
			}
		})
	}
	root := t.TempDir()
	writeConfig(t, root, mcpActivationConfig)
	before := mcpTree(t, root)
	malformed := mcpProcess(t.Context(), binary, root, "mcp", "--repo", root)
	malformed.Stdin = strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"orch_run_activate","arguments":{"schema_version":` + "\n")
	var stdout, stderr bytes.Buffer
	malformed.Stdout, malformed.Stderr = &stdout, &stderr
	if err := malformed.Run(); err == nil || malformed.ProcessState == nil || malformed.ProcessState.ExitCode() != ExitError ||
		stdout.Len() != 0 || !strings.Contains(stderr.String(), "unexpected EOF") || mcpTree(t, root) != before {
		t.Fatalf("malformed activation transport: err=%v stdout=%s stderr=%s", err, &stdout, &stderr)
	}
	t.Log("malformed raw stdio: explicit transport error, pure stdout and unchanged files")
}

func mcpActivationRefusals(t *testing.T, ctx context.Context, root string, session *mcp.ClientSession, request []byte) {
	t.Helper()
	// A successful validation or full gate is not human approval.
	mcpPlanValidationParity(t, ctx, root, session, minimalPlanJSON, true)
	gate := mcpCall(t, ctx, session, root, "orch_plan_gate", map[string]any{"plan": json.RawMessage(minimalPlanJSON)})
	if gate.IsError {
		t.Fatalf("fixture gate failed: %+v", gate)
	}
	for _, args := range []any{nil, json.RawMessage("null"), []any{}, true, 1, "not an object", gate.StructuredContent} {
		mcpEvalError(t, mcpCall(t, ctx, session, root, "orch_run_activate", args), "")
	}
	for _, key := range []string{"schema_version", "plan", "approval"} {
		for _, value := range []any{nil, "wrong type", []any{}} {
			input := mcpDecoded(t, request).(map[string]any)
			input[key] = value
			mcpEvalError(t, mcpCall(t, ctx, session, root, "orch_run_activate", input), "")
		}
		input := mcpDecoded(t, request).(map[string]any)
		delete(input, key)
		mcpEvalError(t, mcpCall(t, ctx, session, root, "orch_run_activate", input), "")
	}
	for _, key := range []string{"plan_digest", "target", "approved_by", "approved_at", "statement"} {
		for _, value := range []any{nil, "", 1, []any{}} {
			input := mcpDecoded(t, request).(map[string]any)
			input["approval"].(map[string]any)[key] = value
			mcpEvalError(t, mcpCall(t, ctx, session, root, "orch_run_activate", input), "")
		}
		input := mcpDecoded(t, request).(map[string]any)
		delete(input["approval"].(map[string]any), key)
		mcpEvalError(t, mcpCall(t, ctx, session, root, "orch_run_activate", input), "")
	}
	for _, tc := range []struct{ key, value, errorText string }{
		{"approved_by", " \t", "nonempty approved_by"},
		{"approved_at", "0001-01-01T00:00:00Z", "nonzero approved_at"},
		{"approved_at", "not a timestamp", "decode activation request"},
		{"statement", run.GrantApprovalStatement, "grant-based activation is not supported"},
		{"statement", "validation succeeded", "requires human approval statement"},
		{"approved_by", "grant:synthetic", "names a grant"},
		{"plan_digest", "sha256:stale", "does not match the recomputed digest"},
	} {
		input := mcpDecoded(t, request).(map[string]any)
		input["approval"].(map[string]any)[tc.key] = tc.value
		mcpEvalError(t, mcpCall(t, ctx, session, root, "orch_run_activate", input), tc.errorText)
	}
	for _, key := range []string{"repo", "repo_root", "eval_storage_root", "unexpected"} {
		input := mcpDecoded(t, request).(map[string]any)
		input[key] = t.TempDir()
		mcpEvalError(t, mcpCall(t, ctx, session, root, "orch_run_activate", input), "")
	}
	unknownApproval := mcpDecoded(t, request).(map[string]any)
	unknownApproval["approval"].(map[string]any)["unexpected"] = true
	mcpEvalError(t, mcpCall(t, ctx, session, root, "orch_run_activate", unknownApproval), "")
	unknownTarget := mcpDecoded(t, request).(map[string]any)
	unknownTarget["approval"].(map[string]any)["target"].(map[string]any)["repo"] = "wrong"
	mcpEvalError(t, mcpCall(t, ctx, session, root, "orch_run_activate", unknownTarget), "")
	for _, id := range []any{nil, 42, "invalid ID"} {
		input := mcpDecoded(t, request).(map[string]any)
		github := input["approval"].(map[string]any)["target"].(map[string]any)["github"].(map[string]any)
		if id == nil {
			delete(github, "id")
		} else {
			github["id"] = id
		}
		mcpEvalError(t, mcpCall(t, ctx, session, root, "orch_run_activate", input), "")
	}
	// Original bytes must reach DecodeActivation: the SDK's validation pass
	// may otherwise normalize 3.0 or discard unknown nested fields.
	for _, raw := range []string{
		strings.Replace(string(request), `"schema_version":3`, `"schema_version":3.0`, 1),
		strings.Replace(string(request), `"schema_version":3`, `"schema_version":2`, 1),
		strings.Replace(string(request), `"schema_version":3`, `"schema_version":99`, 1),
		strings.Replace(string(request), `"host":`, `"unexpected":true,"host":`, 1),
		strings.Replace(string(request), `"id":`, `"unexpected":true,"id":`, 1),
		strings.Replace(string(request), `"read_only":`, `"unexpected":true,"read_only":`, 1),
		strings.Replace(string(request), `"mechanical":`, `"unexpected":true,"mechanical":`, 1),
		strings.Replace(string(request), `"schema_version":1`, `"schema_version":99`, 1),
		strings.Replace(string(request), `"wave":1`, `"wave":1.0`, 1),
		strings.Replace(string(request), `"host":"claude"`, `"host":"unknown"`, 1),
		strings.Replace(string(request), `"objective":`, `"objective":null,"ignored":`, 1),
	} {
		if raw == string(request) {
			t.Fatal("malformed activation fixture did not change input")
		}
		mcpActivationRefusalParity(t, ctx, root, session, []byte(raw))
	}
	legacy, err := mustMCPActivationPlan(t).Digest()
	if err != nil {
		t.Fatal(err)
	}
	mcpActivationRefusalParity(t, ctx, root, session, []byte(strings.Replace(string(request), mcpDecoded(t, request).(map[string]any)["approval"].(map[string]any)["plan_digest"].(string), legacy, 1)))
	mcpActivationRefusalParity(t, ctx, root, session, []byte(strings.Replace(string(request), "Make status reporting race-free", "Changed unapproved work", 1)))
	// Same connection, fresh committed/local configuration and recovery.
	committed, local := filepath.Join(root, config.Path), filepath.Join(root, config.LocalOverridePath)
	mcpWrite(t, committed, []byte("invalid configuration ["))
	mcpActivationRefusalParity(t, ctx, root, session, request)
	mcpWrite(t, committed, []byte(strings.Replace(mcpActivationConfig, `"r1"`, `"r2"`, 1)))
	mcpActivationRefusalParity(t, ctx, root, session, request)
	mcpWrite(t, committed, []byte(mcpActivationConfig))
	mcpWrite(t, local, []byte("[concurrency]\nmax_subagents = 7\n"))
	mcpActivationRefusalParity(t, ctx, root, session, request)
	if err := os.Remove(local); err != nil {
		t.Fatal(err)
	}
	st, err := state.EnterDelivery(root, "claude", testPlanRef(), testIssues())
	if err != nil {
		t.Fatal(err)
	}
	mcpActivationRefusalParity(t, ctx, root, session, request)
	if err := state.Save(root, &state.State{SchemaVersion: state.SchemaVersion, Mode: state.ModeAssist}); err != nil {
		t.Fatal(err)
	}
	mcpActivationRefusalParity(t, ctx, root, session, request) // Held orphan lock.
	mcpWrite(t, filepath.Join(root, lockfile.Path), []byte("{corrupt lock"))
	mcpActivationRefusalParity(t, ctx, root, session, request)
	if err := os.Remove(filepath.Join(root, lockfile.Path)); err != nil {
		t.Fatal(err)
	}
	if err := state.Save(root, st); err != nil {
		t.Fatal(err)
	}
	mcpActivationRefusalParity(t, ctx, root, session, request) // Delivery without lock.
	if err := os.Remove(filepath.Join(root, state.Path)); err != nil {
		t.Fatal(err)
	}
	t.Log("strict outer/nested schema, explicit metadata/human-only approval, stale digests/configuration and active/held/corrupt lock refusals; no mutations")
}

func mcpActivationRefusalParity(t *testing.T, ctx context.Context, root string, session *mcp.ClientSession, request []byte) {
	t.Helper()
	before := mcpTree(t, root)
	script := &execxtest.Script{T: t}
	doc, wantErr := run.Activate(ctx, run.Env{RepoRoot: root, Runner: script}, request)
	script.AssertExhausted()
	if wantErr == nil || doc != nil {
		t.Fatalf("shared engine unexpectedly accepted refusal fixture: %s", request)
	}
	result := mcpCall(t, ctx, session, root, "orch_run_activate", json.RawMessage(request))
	mcpEvalError(t, result, "")
	if result.Content[0].(*mcp.TextContent).Text != wantErr.Error() || mcpTree(t, root) != before {
		t.Fatalf("engine/MCP refusal or unchanged-files mismatch: shared=%v MCP=%s", wantErr, result.Content[0].(*mcp.TextContent).Text)
	}
}

func mcpActivationStateParity(t *testing.T, ctx context.Context, binary, root, sharedRoot string, session *mcp.ClientSession, request []byte) {
	t.Helper()
	got := mcpParity(t, ctx, binary, root, session)
	want, err := run.Status(ctx, run.Env{RepoRoot: sharedRoot})
	if err != nil {
		t.Fatal(err)
	}
	if got.Mode != want.Mode || got.Consistent != want.Consistent || (got.Run == nil) != (want.Run == nil) || (got.Lock == nil) != (want.Lock == nil) {
		t.Fatalf("fresh activation status differs: shared=%+v MCP=%+v", want, got)
	}
	if got.Run != nil {
		approval, err := run.DecodeActivation(request)
		if err != nil || got.Run.Plan.Digest != approval.Approval.PlanDigest || got.Run.Plan.Digest == want.Run.Plan.Digest {
			t.Fatalf("run lost its checkout-specific approval: %+v, %v", got.Run.Plan, err)
		}
		sharedPlan := want.Run.Plan
		sharedPlan.Digest = got.Run.Plan.Digest // Distinct checkout identities are expected.
		if !reflect.DeepEqual(got.Run.Plan, sharedPlan) || !reflect.DeepEqual(got.Run.Issues, want.Run.Issues) ||
			got.Lock.RunID != got.Run.ID || got.Lock.Host != got.Run.Host {
			t.Fatalf("activation lost approval/work/routing/phase/lock: shared=%+v MCP=%+v", want, got)
		}
		for _, issue := range got.Run.Issues {
			if issue.PRNumber != 0 || issue.Phase != state.PhasePlanned && issue.Phase != state.PhaseIssueCreated && issue.Phase != state.PhaseWorktreeReady {
				t.Fatalf("activation advanced beyond its authority: %+v", issue)
			}
		}
		events, err := metrics.LoadAll(root)
		if err != nil {
			t.Fatal(err)
		}
		for _, record := range events {
			for _, event := range record.Events {
				if event.Verb != "activate" || event.ApprovalSource != "human" {
					t.Fatalf("activation performed another lifecycle/grant action: %+v", event)
				}
			}
		}
		if got.Run.Issues[0].Phase == state.PhaseWorktreeReady && len(events) != 1 {
			t.Fatalf("successful activation lost enabled metrics: %+v", events)
		}
		mcpActivationRefusalParity(t, ctx, root, session, request)
	}
	mcpResourceReports(t, ctx, root, session, 71)
}
