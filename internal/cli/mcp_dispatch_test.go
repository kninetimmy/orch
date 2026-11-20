package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/kninetimmy/orch/internal/config"
	"github.com/kninetimmy/orch/internal/execx"
	"github.com/kninetimmy/orch/internal/execx/execxtest"
	"github.com/kninetimmy/orch/internal/lockfile"
	"github.com/kninetimmy/orch/internal/metrics"
	"github.com/kninetimmy/orch/internal/paths"
	"github.com/kninetimmy/orch/internal/run"
	"github.com/kninetimmy/orch/internal/state"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const mcpDispatchRequest = `{"schema_version":4,"issue_number":71}`
const mcpDispatchSession = "aaaaaaaa-0000-4000-8000-000000000001"

func mcpDispatchSchema(t *testing.T, tool *mcp.Tool) {
	t.Helper()
	if tool.Annotations.IdempotentHint || tool.Annotations.DestructiveHint == nil || !*tool.Annotations.DestructiveHint {
		t.Fatalf("dispatch mutation annotations = %+v", tool.Annotations)
	}
	for _, disclosure := range []string{"Non-idempotent", "mutating", "externally effectful", "GitHub", "Partial failures", "cancellation", "never automatically retry", "Does not spawn an agent", "choose a model", "grant approval"} {
		if !strings.Contains(tool.Description, disclosure) {
			t.Fatalf("dispatch description omits %q: %s", disclosure, tool.Description)
		}
	}
	schema, ok := tool.OutputSchema.(map[string]any)
	if !ok || schema["type"] != "object" {
		t.Fatalf("dispatch output schema = %#v", tool.OutputSchema)
	}
	properties, ok := schema["properties"].(map[string]any)
	fields := []string{"schema_version", "issue_number", "branch", "worktree", "executor", "reviewer", "rationale", "objective", "acceptance_criteria", "required_tests", "tests_ci_does_not_run"}
	if !ok || len(properties) != len(fields) {
		t.Fatalf("dispatch output properties = %#v", schema)
	}
	for _, field := range fields {
		if properties[field] == nil {
			t.Fatalf("dispatch output omits %s: %#v", field, schema)
		}
	}
}

func mcpDispatchFixture(t *testing.T, root, host string) *state.State {
	t.Helper()
	configuration := validTOML
	if host == "opencode" {
		// The approved executor is bare, the reviewer has a variant. Neither
		// may be inferred, dropped or replaced by the MCP client.
		configuration = strings.Replace(mixedOpenCodeTOML, `variant = "fast"`, "", 1)
	}
	writeConfig(t, root, configuration+"\n[metrics]\nenabled = true\n")
	plan := mustMCPActivationPlan(t)
	plan.Host = host
	plan.Issues[0].Objective = "  Preserve approved text: café / 日本語.\nDo not paraphrase.  "
	plan.Issues[0].AcceptanceCriteria = []string{"Keep order and whitespace.\nSecond line.", "No agent launch."}
	plan.Issues[0].RequiredTests = []string{"go test ./...", "approved local-only check"}
	plan.Issues[0].TestsCIDoesNotRun = []string{"approved local-only check"}
	gate := mcpFixtureGate(t, root, mcpEvalBytes(t, plan))
	digest, err := gate.Execution.Digest()
	if err != nil {
		t.Fatal(err)
	}
	work := gate.Issues[0]
	st, err := state.EnterDelivery(root, host, state.PlanRef{
		Title: gate.PlanTitle, Digest: gate.PlanDigest, ConfigRevision: gate.ConfigRevision,
		ApprovedBy: "fixture-human", ApprovedAt: time.Date(2026, 11, 14, 12, 0, 0, 0, time.UTC),
		ContractVersion: run.ContractVersion, ExecutionDigest: digest,
	}, []state.Issue{{
		PlanID: work.ID, Title: work.Title, Number: 71, Phase: state.PhasePlanned,
		Branch: "orch/issue-71", Worktree: ".orchestrator/worktrees/issue-71",
		Objective: work.Objective, AcceptanceCriteria: work.AcceptanceCriteria,
		RequiredTests: work.RequiredTests, TestsCIDoesNotRun: work.TestsCIDoesNotRun,
		Decision: &state.Decision{Role: work.Role, Executor: work.Executor, Reviewer: work.Reviewer, Rationale: work.RoutingRationale},
	}})
	if err != nil {
		t.Fatal(err)
	}
	st.Run.Issues[0].Phase = state.PhaseWorktreeReady
	if err := state.Save(root, st); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, filepath.FromSlash(st.Run.Issues[0].Worktree)), 0o755); err != nil {
		t.Fatal(err)
	}
	return st
}

func mcpDispatchCalls(root, scenario string) []execxtest.Call {
	calls := []execxtest.Call{
		{Name: "gh", Args: []string{"auth", "status"}},
		{Name: "gh", Args: []string{"repo", "view", "--json", "nameWithOwner,defaultBranchRef,url"}, Stdout: `{"nameWithOwner":"fixture/isolated","defaultBranchRef":{"name":"main"},"url":"https://github.com/fixture/isolated"}`},
		{Name: "git", Args: []string{"rev-parse", "--show-toplevel"}, Stdout: root},
		{Name: "git", Args: []string{"fetch", "origin"}},
		{Name: "git", Args: []string{"merge", "--ff-only", "origin/main"}},
		{Name: "gh", Args: []string{"issue", "edit", "71", "--remove-label", "ready", "--remove-label", "blocked", "--remove-label", "needs-human", "--remove-label", "awaiting-review", "--remove-label", "delivered", "--add-label", "in-progress"}},
	}
	for i := range calls {
		calls[i].Dir = root
	}
	calls[4].Dir = filepath.Join(root, ".orchestrator", "worktrees", "issue-71")
	failAt := -1
	switch scenario {
	case "fetch failure":
		failAt = 3
	case "fast-forward failure":
		failAt = 4
	case "partial external failure":
		failAt = 5
	}
	if failAt >= 0 {
		calls[failAt].Exit, calls[failAt].Stderr = 1, "fixture external failure"
		calls = calls[:failAt+1]
	}
	return calls
}

func mcpDispatchExercise(t *testing.T, binary string) {
	t.Helper()
	probes := mcpProbeBin(t, "git", "gh", "opencode2")
	for _, scenario := range []string{"success and contention", "claude profiles", "fetch failure", "fast-forward failure", "partial external failure", "post-save metrics failure"} {
		t.Run(scenario, func(t *testing.T) {
			root, sharedRoot, cwd := t.TempDir(), t.TempDir(), t.TempDir()
			root, err := paths.Canonical(root)
			if err != nil {
				t.Fatal(err)
			}
			sharedRoot, err = paths.Canonical(sharedRoot)
			if err != nil {
				t.Fatal(err)
			}
			host := "opencode"
			if scenario == "claude profiles" {
				host = "claude"
			}
			st := mcpDispatchFixture(t, root, host)
			sharedFixture := mcpDispatchFixture(t, sharedRoot, host)
			writeConfig(t, cwd, "invalid launching configuration [")
			mcpWrite(t, filepath.Join(cwd, state.Path), []byte("{corrupt launching state"))
			if scenario == "post-save metrics failure" {
				for _, repo := range []string{root, sharedRoot} {
					s, err := state.Load(repo)
					if err != nil {
						t.Fatal(err)
					}
					if err := os.MkdirAll(filepath.Join(repo, ".orchestrator", "metrics"), 0o755); err != nil {
						t.Fatal(err)
					}
					mcpWrite(t, filepath.Join(repo, ".orchestrator", "metrics", s.Run.ID+".json"), []byte("{corrupt metrics"))
				}
			}
			workspace := append(mcpWorkspaceCalls(root, mcpMatchingHost(t, os.Getpid(), root)), mcpPrimaryCalls(root)...)
			calls := append(slices.Clone(workspace), mcpDispatchCalls(root, scenario)...)
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
			if scenario == "success and contention" {
				lock, err := lockfile.AcquireMutation(root)
				if err != nil {
					t.Fatal(err)
				}
				defer func() {
					if err := lock.Release(); err != nil {
						t.Error(err)
					}
				}()
				mcpDispatchInvalidInputs(t, ctx, root, session)
				mcpMetricsOmittedArguments(t, ctx, binary, cwd, root)
				if _, err := os.Stat(logPath); !os.IsNotExist(err) {
					t.Fatalf("invalid inputs ran commands: %v", err)
				}
				if err := lock.Release(); err != nil {
					t.Fatal(err)
				}
				mcpDispatchPrerequisites(t, ctx, root, session, st)
				prior = mcpReadProbeLog(t, logPath)
				mcpOnlyProbes(t, prior, workspace)
			}
			mcpIssueContextParity(t, ctx, binary, root, session, 71)
			script := &execxtest.Script{T: t, Calls: mcpDispatchCalls(sharedRoot, scenario)}
			want, wantErr := run.Dispatch(ctx, run.Env{RepoRoot: sharedRoot, Runner: script, ClaudeSession: mcpDispatchSession}, []byte(mcpDispatchRequest))
			script.AssertExhausted()
			invoke := func() (*mcp.CallToolResult, error) {
				return session.CallTool(ctx, &mcp.CallToolParams{Meta: mcpWorkspaceMeta(), Name: "orch_run_dispatch", Arguments: json.RawMessage(mcpDispatchRequest)})
			}
			var result *mcp.CallToolResult
			var callErr error
			if scenario == "success and contention" {
				lock, err := lockfile.AcquireMutation(root)
				if err != nil {
					t.Fatal(err)
				}
				defer func() {
					if err := lock.Release(); err != nil {
						t.Error(err)
					}
				}()
				before := mcpTree(t, root)
				done := make(chan struct{})
				go func() { result, callErr = invoke(); close(done) }()
				select {
				case <-done:
					t.Fatal("dispatch bypassed the repository mutation serializer")
				case <-time.After(200 * time.Millisecond):
				}
				if mcpTree(t, root) != before {
					t.Fatal("contending dispatch mutated before acquiring the serializer")
				}
				if current := mcpReadProbeLog(t, logPath); !bytes.Equal(current, prior) {
					t.Fatal("contending dispatch ran commands before acquiring the serializer")
				}
				// Inspection is still unlocked while the mutator waits.
				mcpResourceReports(t, ctx, root, session, 71)
				if err := lock.Release(); err != nil {
					t.Fatal(err)
				}
				<-done
			} else {
				result, callErr = invoke()
			}
			if callErr != nil {
				t.Fatal(callErr)
			}
			log := mcpReadProbeLog(t, logPath)
			if want := string(prior) + mcpProbeLog(calls); string(log) != want {
				t.Fatalf("dispatch escaped binding, retried or exceeded the expected transcript:\ngot %s\nwant %s", log, want)
			}
			if wantErr != nil {
				expected := strings.ReplaceAll(wantErr.Error(), sharedRoot, root)
				expected = strings.ReplaceAll(expected, sharedFixture.Run.ID, st.Run.ID)
				mcpEvalError(t, result, expected)
				if result.Content[0].(*mcp.TextContent).Text != expected {
					t.Fatalf("engine/MCP error mismatch: shared=%q MCP=%+v", expected, result)
				}
				t.Logf("shared engine = real-stdio refusal (no successful brief): %s", expected)
			} else {
				mcpDispatchResultParity(t, result, want)
				cliEnv, stdout, stderr := testEnv(t)
				cliEnv.RepoRoot, err = paths.Canonical(cliEnv.RepoRoot)
				if err != nil {
					t.Fatal(err)
				}
				mcpDispatchFixture(t, cliEnv.RepoRoot, host)
				cliScript := &execxtest.Script{T: t, Calls: mcpDispatchCalls(cliEnv.RepoRoot, scenario)}
				cliEnv.Runner, cliEnv.Stdin = cliScript, strings.NewReader(mcpDispatchRequest)
				if code := Run([]string{"run", "dispatch"}, cliEnv); code != ExitOK {
					t.Fatalf("CLI dispatch exit=%d stderr=%s", code, stderr)
				}
				cliScript.AssertExhausted()
				if !reflect.DeepEqual(mcpDecoded(t, stdout.Bytes()), mcpDecoded(t, mcpEvalBytes(t, want))) {
					t.Fatalf("CLI/shared engine brief mismatch: CLI=%s shared=%+v", stdout, want)
				}
				t.Log("CLI = shared engine = real-stdio structured = JSON text; approved text/test declarations and selections preserved verbatim")
			}
			got, err := state.Load(root)
			if err != nil {
				t.Fatal(err)
			}
			shared, err := state.Load(sharedRoot)
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(got.Run.Issues, shared.Run.Issues) || !reflect.DeepEqual(got.Run.Plan, st.Run.Plan) {
				t.Fatalf("dispatch changed approval or diverged from engine state: got=%+v shared=%+v", got, shared)
			}
			if got.Run.Issues[0].Phase == state.PhaseDispatched {
				mcpDispatchRefusalParity(t, ctx, root, session) // Repeat is a refusal, not idempotent success.
			}
			if scenario != "post-save metrics failure" {
				records, err := metrics.LoadAll(root)
				if err != nil {
					t.Fatal(err)
				}
				if wantErr == nil && (len(records) != 1 || len(records[0].Events) != 1 || records[0].Events[0].Verb != "dispatch") {
					t.Fatalf("dispatch-only metrics = %+v", records)
				}
				if host == "claude" {
					sessions, err := metrics.ArchitectSessions(root, got.Run.ID)
					if err != nil || !reflect.DeepEqual(sessions, []string{mcpDispatchSession}) {
						t.Fatalf("existing session attribution lost: %v, %v", sessions, err)
					}
				}
			}
			mcpParity(t, ctx, binary, root, session)
			mcpIssueContextParity(t, ctx, binary, root, session, 71)
			mcpResourceReports(t, ctx, root, session, 71)
			mcpDiscover(t, ctx, session, root)
			closeSession()
			mcpOnlyProbes(t, mcpReadProbeLog(t, logPath)[len(log):], workspace)
			if mcpTree(t, cwd) != cwdBefore {
				t.Fatal("dispatch mutated the launching checkout")
			}
		})
	}
	t.Run("malformed transport", func(t *testing.T) {
		root := t.TempDir()
		writeConfig(t, root, validTOML)
		before := mcpTree(t, root)
		cmd := mcpProcess(t.Context(), binary, root, "mcp", "--repo", root)
		cmd.Stdin = strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"orch_run_dispatch","arguments":{"schema_version":` + "\n")
		var stdout, stderr bytes.Buffer
		cmd.Stdout, cmd.Stderr = &stdout, &stderr
		if err := cmd.Run(); err == nil || cmd.ProcessState == nil || cmd.ProcessState.ExitCode() != ExitError ||
			stdout.Len() != 0 || !strings.Contains(stderr.String(), "unexpected EOF") || mcpTree(t, root) != before {
			t.Fatalf("malformed dispatch transport: err=%v stdout=%s stderr=%s", err, &stdout, &stderr)
		}
		t.Log("truncated raw dispatch JSON: explicit transport error, protocol-only stdout and unchanged files")
	})
}

func mcpDispatchResultParity(t *testing.T, result *mcp.CallToolResult, want *run.DispatchResult) {
	t.Helper()
	if result.IsError || len(result.Content) != 1 {
		t.Fatalf("dispatch result = %+v", result)
	}
	text, ok := result.Content[0].(*mcp.TextContent)
	expected := mcpDecoded(t, mcpEvalBytes(t, want))
	if !ok || want.SchemaVersion != 4 || !reflect.DeepEqual(result.StructuredContent, expected) ||
		!reflect.DeepEqual(mcpDecoded(t, []byte(text.Text)), expected) {
		t.Fatalf("engine/structured/text brief mismatch: shared=%+v MCP=%+v", want, result)
	}
}

func mcpDispatchInvalidInputs(t *testing.T, ctx context.Context, root string, session *mcp.ClientSession) {
	t.Helper()
	// The caller holds the repository serializer: malformed input must fail
	// without waiting for it, recording a session or running a command.
	for _, args := range []any{nil, json.RawMessage("null"), []any{}, "not an object", true, 1, map[string]any{}} {
		mcpEvalError(t, mcpCall(t, ctx, session, root, "orch_run_dispatch", args), "")
	}
	for _, field := range []string{"schema_version", "issue_number"} {
		for _, value := range []any{nil, "", "4", true, []any{}, map[string]any{}, 0, -1, 1.5, 1e100} {
			input := mcpDecoded(t, []byte(mcpDispatchRequest)).(map[string]any)
			input[field] = value
			mcpEvalError(t, mcpCall(t, ctx, session, root, "orch_run_dispatch", input), "")
		}
		input := mcpDecoded(t, []byte(mcpDispatchRequest)).(map[string]any)
		delete(input, field)
		mcpEvalError(t, mcpCall(t, ctx, session, root, "orch_run_dispatch", input), "")
	}
	for _, version := range []int{1, 2, 3, 5, 99} {
		mcpEvalError(t, mcpCall(t, ctx, session, root, "orch_run_dispatch", map[string]any{"schema_version": version, "issue_number": 71}), "")
	}
	for _, field := range []string{"root", "repo", "repo_root", "eval_storage_root", "model", "executor", "reviewer", "approval", "approved_by", "unexpected"} {
		input := mcpDecoded(t, []byte(mcpDispatchRequest)).(map[string]any)
		input[field] = "forbidden override"
		mcpEvalError(t, mcpCall(t, ctx, session, root, "orch_run_dispatch", input), "")
	}
	for _, raw := range []string{
		`{"schema_version":4.0,"issue_number":71}`, `{"schema_version":4e0,"issue_number":71}`,
		`{"schema_version":4,"issue_number":71.0}`, `{"schema_version":4,"issue_number":71e0}`,
		`{"schema_version":4,"issue_number":999999999999999999999999999999}`,
	} {
		mcpEvalError(t, mcpCall(t, ctx, session, root, "orch_run_dispatch", json.RawMessage(raw)), "")
	}
	t.Log("strict object/version/positive-int schema and raw number literals rejected before the held serializer; unchanged files")
}

func mcpDispatchPrerequisites(t *testing.T, ctx context.Context, root string, session *mcp.ClientSession, baseline *state.State) {
	t.Helper()
	original := mcpEvalBytes(t, baseline)
	owner, err := os.ReadFile(filepath.Join(root, lockfile.Path))
	if err != nil {
		t.Fatal(err)
	}
	configuration, err := os.ReadFile(filepath.Join(root, config.Path))
	if err != nil {
		t.Fatal(err)
	}
	for _, scenario := range []string{"Assist", "stopped", "blocked", "wrong phase", "unmet dependency", "abandoned dependency", "missing work", "missing routing", "unknown issue", "contract version", "committed drift", "effective local drift", "missing lock", "corrupt state"} {
		var st state.State
		if err := json.Unmarshal(original, &st); err != nil {
			t.Fatal(err)
		}
		issue := &st.Run.Issues[0]
		switch scenario {
		case "Assist":
			st.Mode, st.Run = state.ModeAssist, nil
		case "stopped":
			st.Run.StoppedReason = "fixture stop"
		case "blocked":
			issue.Phase, issue.BlockedReason = state.PhaseBlocked, "pending human decision"
			issue.Blocks = []state.Block{{ID: 1, Cause: state.BlockHuman, Reason: issue.BlockedReason}}
		case "wrong phase":
			issue.Phase = state.PhaseDispatched
		case "unmet dependency", "abandoned dependency":
			dep := *issue
			dep.PlanID, dep.Number, dep.DependsOn = "dependency", 72, nil
			dep.Phase = state.PhaseDispatched
			if scenario == "abandoned dependency" {
				dep.Phase = state.PhaseAbandoned
			}
			issue.DependsOn = []string{dep.PlanID}
			st.Run.Issues = append(st.Run.Issues, dep)
		case "missing work":
			issue.Objective, issue.AcceptanceCriteria, issue.RequiredTests = "", nil, nil
		case "missing routing":
			issue.Decision = nil
		case "unknown issue":
			issue.Number = 72
		case "contract version":
			st.Run.Plan.ContractVersion--
		case "committed drift":
			mcpWrite(t, filepath.Join(root, config.Path), []byte(strings.Replace(string(configuration), `"r1"`, `"r2"`, 1)))
		case "effective local drift":
			mcpWrite(t, filepath.Join(root, config.LocalOverridePath), []byte("[concurrency]\nmax_subagents = 7\n"))
		}
		if err := state.Save(root, &st); err != nil {
			t.Fatal(err)
		}
		if scenario == "Assist" || scenario == "missing lock" {
			if err := os.Remove(filepath.Join(root, lockfile.Path)); err != nil {
				t.Fatal(err)
			}
		}
		if scenario == "corrupt state" {
			mcpWrite(t, filepath.Join(root, state.Path), []byte("{corrupt state"))
		}
		mcpDispatchRefusalParity(t, ctx, root, session)
		mcpWrite(t, filepath.Join(root, state.Path), original)
		mcpWrite(t, filepath.Join(root, lockfile.Path), owner)
		mcpWrite(t, filepath.Join(root, config.Path), configuration)
		if scenario == "effective local drift" {
			if err := os.Remove(filepath.Join(root, config.LocalOverridePath)); err != nil {
				t.Fatal(err)
			}
		}
	}
}

func mcpDispatchRefusalParity(t *testing.T, ctx context.Context, root string, session *mcp.ClientSession) {
	t.Helper()
	before := mcpTree(t, root)
	script := &execxtest.Script{T: t}
	doc, wantErr := run.Dispatch(ctx, run.Env{RepoRoot: root, Runner: script, ClaudeSession: mcpDispatchSession}, []byte(mcpDispatchRequest))
	script.AssertExhausted()
	if wantErr == nil || doc != nil {
		t.Fatal("shared engine unexpectedly accepted refusal fixture")
	}
	result := mcpCall(t, ctx, session, root, "orch_run_dispatch", json.RawMessage(mcpDispatchRequest))
	mcpEvalError(t, result, wantErr.Error())
	if result.Content[0].(*mcp.TextContent).Text != wantErr.Error() || mcpTree(t, root) != before {
		t.Fatalf("engine/MCP refusal or unchanged-files mismatch: shared=%v MCP=%+v", wantErr, result)
	}
	t.Logf("shared engine = real-stdio prerequisite/repeat refusal: %s", wantErr)
}

type mcpCancelDispatchRunner struct {
	*execxtest.Script
	entered, canceled chan struct{}
}

func (r mcpCancelDispatchRunner) Run(ctx context.Context, cmd execx.Cmd) (execx.Result, error) {
	result, err := r.Script.Run(ctx, cmd)
	if cmd.Name == "gh" && len(cmd.Args) > 1 && cmd.Args[0] == "issue" && cmd.Args[1] == "edit" {
		close(r.entered)
		<-ctx.Done()
		close(r.canceled)
		return execx.Result{}, ctx.Err()
	}
	return result, err
}

func TestMCPDispatchCancellation(t *testing.T) {
	env, _, stderr := testEnv(t)
	root, err := paths.Canonical(env.RepoRoot)
	if err != nil {
		t.Fatal(err)
	}
	env.RepoRoot = root
	mcpDispatchFixture(t, env.RepoRoot, "opencode")
	workspace := append(mcpWorkspaceCalls(root, mcpMatchingHost(t, os.Getppid(), root)), mcpPrimaryCalls(root)...)
	script := &execxtest.Script{T: t, Calls: append(workspace, mcpDispatchCalls(root, "")...)}
	entered, canceled := make(chan struct{}), make(chan struct{})
	env.Runner = mcpCancelDispatchRunner{script, entered, canceled}
	input, clientWriter := io.Pipe()
	clientReader, output := io.Pipe()
	defer func() {
		_ = input.Close()
		_ = output.Close()
	}()
	env.Stdin, env.Stdout = input, output
	before := mcpTree(t, env.RepoRoot)
	done := make(chan int, 1)
	go func() { done <- Run([]string{"mcp"}, env) }()
	ctx, stop := context.WithTimeout(t.Context(), 10*time.Second)
	defer stop()
	client := mcp.NewClient(&mcp.Implementation{Name: "dispatch-cancellation-test", Version: "1"}, nil)
	session, err := client.Connect(ctx, &mcp.IOTransport{Reader: clientReader, Writer: clientWriter}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = session.Close() }()
	callCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	finished := make(chan struct{})
	var result *mcp.CallToolResult
	var callErr error
	go func() {
		result, callErr = session.CallTool(callCtx, &mcp.CallToolParams{Meta: mcpWorkspaceMeta(), Name: "orch_run_dispatch", Arguments: json.RawMessage(mcpDispatchRequest)})
		close(finished)
	}()
	select {
	case <-entered: // Fetch and fast-forward were attempted; status has not completed.
	case <-ctx.Done():
		t.Fatal("dispatch did not reach the cancellable runner")
	}
	cancel()
	select {
	case <-canceled:
	case <-ctx.Done():
		t.Fatal("MCP cancellation did not reach the shared engine runner")
	}
	<-finished
	if !errors.Is(callErr, context.Canceled) || result != nil {
		t.Fatalf("cancellation fabricated a dispatch receipt: result=%+v err=%v", result, callErr)
	}
	// Wait for the full handler, including serializer release, not just the
	// client's cancellation. No automatic retry is allowed after earlier effects.
	if err := withDeliveryMutation(env.RepoRoot, func() error { return nil }); err != nil {
		t.Fatal(err)
	}
	script.AssertExhausted()
	if mcpTree(t, env.RepoRoot) != before {
		t.Fatal("canceled status command saved a successful phase or metrics")
	}
	mcpDiscover(t, ctx, session, env.RepoRoot)
	if err := session.Close(); err != nil {
		t.Fatal(err)
	}
	if code := <-done; code != ExitOK || stderr.Len() != 0 {
		t.Fatalf("cancellation server shutdown: exit=%d stderr=%s", code, stderr)
	}
	t.Log("MCP cancellation reaches the shared runner after earlier commands; no brief, retry, phase advancement or leaked serializer")
}
