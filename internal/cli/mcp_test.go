package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/kninetimmy/orch/internal/execx"
	"github.com/kninetimmy/orch/internal/lockfile"
	"github.com/kninetimmy/orch/internal/manifest"
	"github.com/kninetimmy/orch/internal/run"
	"github.com/kninetimmy/orch/internal/state"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestMCPStdio(t *testing.T) {
	binary := filepath.Join(t.TempDir(), "orch-mcp")
	if runtime.GOOS == "windows" {
		binary += ".exe"
	}
	build := exec.CommandContext(t.Context(), "go", "build", "-o", binary, "../../cmd/orch")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build orch: %v: %s", err, out)
	}

	t.Run("live status", func(t *testing.T) {
		root, cwd := t.TempDir(), t.TempDir()
		// Neither configuration nor Git is needed. A different, corrupt cwd
		// ensures --repo binds the requested checkout, not the launching host.
		writeConfig(t, root, "not valid TOML [")
		writeConfig(t, cwd, "not valid TOML [")
		mcpWrite(t, filepath.Join(cwd, state.Path), []byte("{corrupt cwd"))
		ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
		defer cancel()
		session, closeSession := mcpConnect(t, ctx, binary, cwd, root, "--repo", root)
		mcpDiscover(t, ctx, session, root)
		if doc := mcpParity(t, ctx, binary, root, session); doc.Mode != state.ModeAssist || !doc.Consistent {
			t.Fatalf("initial status = %+v", doc)
		}
		for _, tool := range []string{"orch_run_status", "orch_run_next"} {
			for _, args := range []any{map[string]any{"repo": cwd}, map[string]any{"repo_root": cwd}, map[string]any{"unexpected": true}, []any{}, "not an object", 1} {
				result := mcpCall(t, ctx, session, root, tool, args)
				if !result.IsError || result.StructuredContent != nil {
					t.Fatalf("%s accepted unexpected arguments: %+v", tool, result)
				}
			}
		}

		// Update fixtures while the same client remains connected. Include
		// resolved and pending blocks, native selections and approval metadata.
		at := time.Date(2026, 11, 11, 12, 0, 0, 0, time.UTC)
		st := &state.State{SchemaVersion: state.SchemaVersion, Mode: state.ModeDelivery, Run: &state.Run{
			ID: "run-mcp-test", Host: "opencode", StartedAt: at,
			Plan: state.PlanRef{Title: "MCP fixture", Digest: "sha256:test", ApprovedBy: "human", ApprovedAt: at,
				ConfigRevision: "r1", ContractVersion: run.ContractVersion, ExecutionDigest: "sha256:execution"},
			Issues: []state.Issue{{
				PlanID: "status", Title: "Live status", Number: 444, Phase: state.PhaseBlocked,
				Branch: "orch/issue-444", Worktree: ".orchestrator/worktrees/issue-444", BlockedReason: "need a decision",
				Decision: &state.Decision{Role: manifest.RoleImplementer,
					Executor: manifest.OpenCodeSelection("openai/gpt-6.1-sol", "xhigh"),
					Reviewer: manifest.OpenCodeSelection("example/model", ""), Rationale: "fixture"},
				Blocks: []state.Block{
					{ID: 1, Cause: state.BlockOperational, Reason: "temporary failure", Resolution: &state.BlockResolution{
						ResolvedBy: "human", Detail: "recovered", At: at.Format(time.RFC3339), Statement: "resolve-block-without-scope-change"}},
					{ID: 2, Cause: state.BlockHuman, Reason: "need a decision"},
				},
			}},
		}}
		if err := state.Save(root, st); err != nil {
			t.Fatal(err)
		}
		if err := lockfile.Acquire(root, lockfile.Owner{RunID: st.Run.ID, Host: "opencode", Hostname: "fixture", PID: 123, AcquiredAt: at}); err != nil {
			t.Fatal(err)
		}
		doc := mcpParity(t, ctx, binary, root, session)
		if doc.Mode != state.ModeDelivery || !doc.Consistent || doc.Lock == nil || doc.Run == nil ||
			doc.Run.Issues[0].Phase != state.PhaseBlocked || len(doc.Run.Issues[0].Blocks) != 2 || doc.Run.Issues[0].Blocks[0].Resolution == nil {
			t.Fatalf("blocked Delivery status = %+v", doc)
		}
		if err := os.Remove(filepath.Join(root, lockfile.Path)); err != nil {
			t.Fatal(err)
		}
		if doc := mcpParity(t, ctx, binary, root, session); doc.Consistent || doc.Warning == "" || doc.Run == nil {
			t.Fatalf("missing-lock status = %+v", doc)
		}
		if err := state.Save(root, &state.State{SchemaVersion: state.SchemaVersion, Mode: state.ModeAssist}); err != nil {
			t.Fatal(err)
		}
		if err := lockfile.Acquire(root, lockfile.Owner{RunID: "orphan", Host: "codex", Hostname: "fixture", PID: 456, AcquiredAt: at}); err != nil {
			t.Fatal(err)
		}
		if doc := mcpParity(t, ctx, binary, root, session); doc.Consistent || doc.Warning == "" || doc.Lock == nil || doc.Run != nil {
			t.Fatalf("orphaned-lock status = %+v", doc)
		}
		mcpWrite(t, filepath.Join(root, state.Path), []byte("{corrupt state"))
		result := mcpCall(t, ctx, session, root, "orch_run_status", map[string]any{})
		if !result.IsError || result.StructuredContent != nil || len(result.Content) != 1 {
			t.Fatalf("corrupt state result = %+v", result)
		}
		text, ok := result.Content[0].(*mcp.TextContent)
		if !ok || !strings.Contains(text.Text, "parse .orchestrator/state.json") {
			t.Fatalf("missing explicit state error: %+v", result.Content)
		}
		t.Logf("corrupt state tool error: %s", text.Text)
		// A tool failure must not break the connection or prevent shutdown.
		mcpDiscover(t, ctx, session, root)
		closeSession()
	})

	t.Run("next guidance", func(t *testing.T) {
		root, cwd := t.TempDir(), t.TempDir()
		writeConfig(t, root, validTOML+"\n[metrics]\nenabled = true\n")
		writeConfig(t, cwd, "invalid other repository [")
		mcpWrite(t, filepath.Join(cwd, state.Path), []byte("{corrupt cwd"))
		ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
		defer cancel()
		session, closeSession := mcpConnect(t, ctx, binary, cwd, root, "--repo", root)
		mcpDiscover(t, ctx, session, root)
		if doc := mcpNextParity(t, ctx, binary, root, session); doc.Mode != state.ModeAssist || doc.Actions[0].Action != "plan" || doc.Actions[0].ApprovalRequired == "" {
			t.Fatalf("Assist guidance = %+v", doc)
		}
		st, err := state.EnterDelivery(root, "claude", testApprovedPlanRef(t, root), []state.Issue{{PlanID: "a", Phase: state.PhasePlanned}, {PlanID: "b", Phase: state.PhasePlanned}})
		if err != nil {
			t.Fatal(err)
		}
		selection := manifest.Selection{Model: "claude-sonnet-5", Effort: "xhigh"}
		for i := range st.Run.Issues {
			issue := &st.Run.Issues[i]
			issue.Number = i + 1
			issue.Branch = fmt.Sprintf("orch/issue-%d", issue.Number)
			issue.Worktree = fmt.Sprintf(".orchestrator/worktrees/issue-%d", issue.Number)
			issue.Objective = "Preserve the approved work"
			issue.AcceptanceCriteria = []string{"inspection never changes the repository"}
			issue.RequiredTests = []string{"go test ./..."}
			issue.Decision = &state.Decision{Role: manifest.RoleImplementer, Executor: selection, Reviewer: selection, Rationale: "fixture"}
		}
		st.Run.Issues[0].Phase = state.PhaseDispatched
		st.Run.Issues[1].Phase = state.PhaseWorktreeReady
		st.Run.Issues[1].DependsOn = []string{"a"}
		save := func() {
			t.Helper()
			if err := state.Save(root, st); err != nil {
				t.Fatal(err)
			}
		}
		save()
		doc := mcpNextParity(t, ctx, binary, root, session)
		if doc.Actions[0].Action != "implement" || doc.Actions[1].Action != "wait" || len(doc.Actions[1].Blockers) == 0 {
			t.Fatalf("dependency guidance = %+v", doc)
		}
		// Exercise each remaining phase in the same real stdio connection.
		for _, tc := range []struct {
			phase  state.Phase
			action string
		}{
			{state.PhasePROpen, "review"},
			{state.PhaseInReview, "merge-report"},
			{state.PhaseAwaitingMerge, "merge"},
			{state.PhaseMerged, "cleanup"},
		} {
			st.Run.Issues[0].Phase = tc.phase
			st.Run.Issues[0].PRNumber = 10
			if tc.phase != state.PhasePROpen {
				st.Run.Issues[0].LastReviewVerdict = run.VerdictApprove
			}
			if tc.phase == state.PhaseAwaitingMerge || tc.phase == state.PhaseMerged {
				st.Run.Issues[0].ApprovedHeadOID = "fixture-approved-head"
			}
			save()
			doc := mcpNextParity(t, ctx, binary, root, session)
			if doc.Actions[0].Action != tc.action || len(doc.Actions[0].Prerequisites) == 0 {
				t.Fatalf("phase %s guidance = %+v", tc.phase, doc)
			}
			if tc.phase == state.PhaseAwaitingMerge && !strings.Contains(doc.Actions[0].ApprovalRequired, run.MergeApprovalStatement) {
				t.Fatal("missing human merge approval")
			}
		}
		st.Run.Issues[0].Phase = state.PhaseAbandoned
		save()
		if doc := mcpNextParity(t, ctx, binary, root, session); doc.Actions[1].Action != "replan" || doc.Actions[1].ApprovalRequired == "" {
			t.Fatalf("abandoned dependency = %+v", doc)
		}
		st.Run.Issues[0].Phase = state.PhaseBlocked
		st.Run.Issues[0].BlockedReason = "criterion needs a decision"
		st.Run.Issues[0].Blocks = []state.Block{{ID: 1, Cause: state.BlockWrong, Reason: st.Run.Issues[0].BlockedReason}}
		save()
		if doc := mcpNextParity(t, ctx, binary, root, session); doc.Actions[0].Action != "resolve-block" {
			t.Fatalf("decision guidance = %+v", doc)
		}
		st.Run.Issues[0].Blocks[0].Resolution = &state.BlockResolution{ResolvedBy: "human", Detail: "scope unchanged", At: "2026-11-11T12:00:00Z", Statement: run.ResolveBlockStatement}
		save()
		if doc := mcpNextParity(t, ctx, binary, root, session); doc.Actions[0].Action != "resume" {
			t.Fatalf("recovery guidance = %+v", doc)
		}
		st.Run.StoppedReason = "secret remediation"
		save()
		if doc := mcpNextParity(t, ctx, binary, root, session); len(doc.Actions) != 1 || doc.Actions[0].Action != "recover-stopped-run" {
			t.Fatalf("stopped guidance = %+v", doc)
		}
		st.Run.StoppedReason = ""
		for i := range st.Run.Issues {
			st.Run.Issues[i].Phase = state.PhaseCleaned
		}
		save()
		if doc := mcpNextParity(t, ctx, binary, root, session); doc.Actions[0].Action != "complete" {
			t.Fatalf("completion guidance = %+v", doc)
		}
		local := filepath.Join(root, ".orchestrator", "config.local.toml")
		mcpWrite(t, local, []byte("[concurrency]\nmax_subagents = 2\n"))
		if doc := mcpNextParity(t, ctx, binary, root, session); doc.Actions[0].Action != "restore-or-replan" || len(doc.Actions[0].Blockers) == 0 {
			t.Fatalf("effective drift = %+v", doc)
		}
		if err := os.Remove(local); err != nil {
			t.Fatal(err)
		}
		if err := os.Remove(filepath.Join(root, lockfile.Path)); err != nil {
			t.Fatal(err)
		}
		if doc := mcpNextParity(t, ctx, binary, root, session); doc.Actions[0].Action != "recover" {
			t.Fatalf("inconsistent guidance = %+v", doc)
		}
		mcpWrite(t, filepath.Join(root, state.Path), []byte("{corrupt state"))
		before := mcpTree(t, root)
		cmd := mcpProcess(ctx, binary, root, "run", "next", "--json")
		out, err := cmd.CombinedOutput()
		result := mcpCall(t, ctx, session, root, "orch_run_next", map[string]any{})
		if err == nil || !strings.Contains(string(out), "parse .orchestrator/state.json") || !result.IsError || result.StructuredContent != nil || len(result.Content) != 1 {
			t.Fatalf("corrupt next: CLI=%s err=%v MCP=%+v", out, err, result)
		}
		text, ok := result.Content[0].(*mcp.TextContent)
		if !ok || !strings.Contains(string(out), text.Text) || mcpTree(t, root) != before {
			t.Fatalf("corrupt-state error parity/no-write failed: %+v", result)
		}
		mcpDiscover(t, ctx, session, root)
		closeSession()
	})

	t.Run("review and merge readiness", func(t *testing.T) {
		mcpReadinessExercise(t, binary)
	})
	t.Run("doctor diagnostics", func(t *testing.T) {
		mcpDoctorExercise(t, binary)
	})
	t.Run("effective configuration", func(t *testing.T) {
		mcpConfigExercise(t, binary)
	})
	t.Run("recorded metrics", func(t *testing.T) {
		mcpMetricsExercise(t, binary)
	})
	t.Run("retained evaluations", func(t *testing.T) {
		mcpEvalExercise(t, binary)
	})
	t.Run("plan gate", func(t *testing.T) {
		mcpPlanGateExercise(t, binary)
	})

	t.Run("repository binding", func(t *testing.T) {
		root := t.TempDir()
		writeConfig(t, root, validTOML)
		alias := root + string(filepath.Separator) + "."
		if runtime.GOOS != "windows" {
			alias = filepath.Join(t.TempDir(), "repo alias")
			mcpDirectoryLink(t, root, alias)
		}
		for _, tc := range []struct {
			name string
			cwd  string
			args []string
		}{
			{"cwd", root, nil},
			{"relative", root, []string{"--repo", "."}},
			{"canonical alias", t.TempDir(), []string{"--repo", alias}},
		} {
			t.Run(tc.name, func(t *testing.T) {
				ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
				defer cancel()
				session, closeSession := mcpConnect(t, ctx, binary, tc.cwd, root, tc.args...)
				mcpDiscover(t, ctx, session, root)
				mcpParity(t, ctx, binary, root, session)
				mcpNextParity(t, ctx, binary, root, session)
				mcpConfigParity(t, ctx, root, session)
				mcpMetricsParity(t, ctx, binary, root, session)
				closeSession()
			})
		}
	})

	t.Run("invalid targets and arguments", func(t *testing.T) {
		root := t.TempDir()
		writeConfig(t, root, "fixture")
		child := filepath.Join(root, "child")
		if err := os.Mkdir(child, 0o755); err != nil {
			t.Fatal(err)
		}
		fileRoot, linkRoot := t.TempDir(), t.TempDir()
		mcpWrite(t, filepath.Join(fileRoot, ".orchestrator"), []byte("not a directory"))
		mcpDirectoryLink(t, filepath.Join(root, ".orchestrator"), filepath.Join(linkRoot, ".orchestrator"))
		for _, tc := range []struct {
			name string
			cwd  string
			args []string
			want string
		}{
			{"missing root", root, []string{"--repo", filepath.Join(root, "missing")}, "repository directory"},
			{"root is file", root, []string{"--repo", filepath.Join(root, ".orchestrator", "config.toml")}, "is not a directory"},
			{"no ancestor discovery explicit", root, []string{"--repo", child}, "must contain"},
			{"no ancestor discovery cwd", child, nil, "must contain"},
			{"metadata file", root, []string{"--repo", fileRoot}, "not a real directory"},
			{"metadata link", root, []string{"--repo", linkRoot}, "not a real directory"},
			{"missing value", root, []string{"--repo"}, "usage:"},
			{"empty value", root, []string{"--repo", ""}, "usage:"},
			{"extra argument", root, []string{"--repo", root, "extra"}, "usage:"},
			{"unknown flag", root, []string{"--other", root}, "usage:"},
		} {
			t.Run(tc.name, func(t *testing.T) {
				before := mcpTree(t, root)
				cmd := mcpProcess(t.Context(), binary, tc.cwd, append([]string{"mcp"}, tc.args...)...)
				var stdout, stderr bytes.Buffer
				cmd.Stdout, cmd.Stderr = &stdout, &stderr
				if err := cmd.Run(); err == nil || stdout.Len() != 0 || !strings.Contains(stderr.String(), tc.want) {
					t.Fatalf("invalid launch: err=%v stdout=%q stderr=%q", err, stdout.String(), stderr.String())
				}
				if after := mcpTree(t, root); after != before {
					t.Fatal("invalid launch changed repository files")
				}
			})
		}
	})
}

func mcpProcess(ctx context.Context, binary, cwd string, args ...string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, binary, args...)
	cmd.Dir = cwd
	// No Git, gh, memhub or host executable is available to the server.
	cmd.Env = append(os.Environ(), "PATH=", "CLAUDE_CODE_SESSION_ID=aaaaaaaa-0000-4000-8000-000000000001")
	cmd.WaitDelay = 2 * time.Second
	return cmd
}

func mcpConnect(t *testing.T, ctx context.Context, binary, cwd, root string, args ...string) (*mcp.ClientSession, func()) {
	t.Helper()
	before, cwdBefore := mcpTree(t, root), mcpTree(t, cwd)
	cmd := mcpProcess(ctx, binary, cwd, append([]string{"mcp"}, args...)...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	client := mcp.NewClient(&mcp.Implementation{Name: "orch-test", Version: "1"}, nil)
	session, err := client.Connect(ctx, &mcp.CommandTransport{Command: cmd}, nil)
	if err != nil {
		t.Fatalf("MCP initialize: %v; stderr=%s", err, stderr.String())
	}
	closed := false
	closeSession := func() {
		t.Helper()
		if closed {
			return
		}
		closed = true
		before, cwdBefore := mcpTree(t, root), mcpTree(t, cwd)
		if err := session.Close(); err != nil {
			t.Errorf("MCP disconnect: %v", err)
		}
		if cmd.ProcessState == nil || !cmd.ProcessState.Success() || stderr.Len() != 0 {
			t.Errorf("server shutdown: state=%v stderr=%s", cmd.ProcessState, stderr.String())
		}
		if mcpTree(t, root) != before || mcpTree(t, cwd) != cwdBefore {
			t.Error("shutdown changed repository files")
		}
	}
	t.Cleanup(closeSession)
	if mcpTree(t, root) != before || mcpTree(t, cwd) != cwdBefore {
		t.Fatal("initialization changed repository files")
	}
	return session, closeSession
}

func mcpDiscover(t *testing.T, ctx context.Context, session *mcp.ClientSession, root string) {
	t.Helper()
	before := mcpTree(t, root)
	list, err := session.ListTools(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(list.Tools) != 9 || list.NextCursor != "" {
		t.Fatalf("tool discovery = %+v", list)
	}
	want := mcpDecoded(t, []byte(`{"type":"object","properties":{},"additionalProperties":false}`))
	seen := map[string]bool{}
	for _, tool := range list.Tools {
		readiness := tool.Name == "orch_run_readiness"
		doctor := tool.Name == "orch_doctor"
		metrics := tool.Name == "orch_metrics" || tool.Name == "orch_metrics_report"
		evaluation := tool.Name == "orch_eval_status"
		planGate := tool.Name == "orch_plan_gate"
		if (tool.Name != "orch_run_status" && tool.Name != "orch_run_next" && tool.Name != "orch_config_effective" && !readiness && !doctor && !metrics && !evaluation && !planGate) || seen[tool.Name] || tool.Annotations == nil || tool.Annotations.ReadOnlyHint == planGate || tool.Annotations.OpenWorldHint == nil || *tool.Annotations.OpenWorldHint != (readiness || doctor || planGate) {
			t.Fatalf("tool = %+v", tool)
		}
		seen[tool.Name] = true
		wantSchema := want
		if readiness {
			wantSchema = mcpDecoded(t, []byte(`{"type":"object","properties":{"issue_number":{"type":"integer","minimum":1}},"required":["issue_number"],"additionalProperties":false}`))
		}
		if doctor {
			wantSchema = mcpDecoded(t, []byte(`{"type":"object","properties":{"host":{"type":"string","enum":["claude","codex","opencode"]}},"additionalProperties":false}`))
		}
		if evaluation {
			wantSchema = mcpDecoded(t, []byte(`{"type":"object","properties":{"run_id":{"type":"string","minLength":1}},"required":["run_id"],"additionalProperties":false}`))
		}
		if planGate {
			wantSchema = mcpDecoded(t, []byte(`{"type":"object","properties":{"plan":{"type":"object"}},"required":["plan"],"additionalProperties":false}`))
			mcpPlanGateSchema(t, tool)
		}
		if !reflect.DeepEqual(tool.InputSchema, wantSchema) {
			t.Fatalf("%s input schema = %#v, want %#v", tool.Name, tool.InputSchema, wantSchema)
		}
	}
	if mcpTree(t, root) != before {
		t.Fatal("discovery changed repository files")
	}
}

func mcpCall(t *testing.T, ctx context.Context, session *mcp.ClientSession, root, tool string, args any) *mcp.CallToolResult {
	t.Helper()
	before := mcpTree(t, root)
	result, err := session.CallTool(ctx, &mcp.CallToolParams{Name: tool, Arguments: args})
	if err != nil {
		t.Fatal(err)
	}
	if mcpTree(t, root) != before {
		t.Fatal("tool call changed repository files")
	}
	return result
}

func mcpParity(t *testing.T, ctx context.Context, binary, root string, session *mcp.ClientSession) *run.StatusDoc {
	t.Helper()
	out := mcpDocumentParity(t, ctx, binary, root, session, "status")
	var doc run.StatusDoc
	if err := json.Unmarshal(out, &doc); err != nil {
		t.Fatal(err)
	}
	if doc.SchemaVersion != 4 {
		t.Fatalf("status schema changed: %d", doc.SchemaVersion)
	}
	t.Logf("CLI = structured = JSON text: status schema=%d mode=%s consistent=%t", doc.SchemaVersion, doc.Mode, doc.Consistent)
	return &doc
}

func mcpNextParity(t *testing.T, ctx context.Context, binary, root string, session *mcp.ClientSession) *run.NextDoc {
	t.Helper()
	out := mcpDocumentParity(t, ctx, binary, root, session, "next")
	var doc run.NextDoc
	if err := json.Unmarshal(out, &doc); err != nil {
		t.Fatal(err)
	}
	if doc.SchemaVersion != 1 || len(doc.Actions) == 0 || len(doc.Limitations) == 0 {
		t.Fatalf("incomplete next guidance: %+v", doc)
	}
	var actions []string
	for _, a := range doc.Actions {
		actions = append(actions, a.Action)
	}
	t.Logf("CLI = structured = JSON text: next schema=%d mode=%s actions=%v; repository unchanged", doc.SchemaVersion, doc.Mode, actions)
	return &doc
}

func mcpDocumentParity(t *testing.T, ctx context.Context, binary, root string, session *mcp.ClientSession, verb string) []byte {
	t.Helper()
	before := mcpTree(t, root)
	cmd := mcpProcess(ctx, binary, root, "run", verb, "--json")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("CLI %s: %v: %s", verb, err, out)
	}
	result := mcpCall(t, ctx, session, root, "orch_run_"+verb, map[string]any{})
	if result.IsError || len(result.Content) != 1 {
		t.Fatalf("%s result = %+v", verb, result)
	}
	text, ok := result.Content[0].(*mcp.TextContent)
	if !ok {
		t.Fatalf("%s content = %T", verb, result.Content[0])
	}
	want := mcpDecoded(t, out)
	if !reflect.DeepEqual(result.StructuredContent, want) || !reflect.DeepEqual(mcpDecoded(t, []byte(text.Text)), want) {
		t.Fatalf("%s mismatch: CLI=%s structured=%v text=%s", verb, out, result.StructuredContent, text.Text)
	}
	if mcpTree(t, root) != before {
		t.Fatalf("%s changed repository files", verb)
	}
	return out
}

func mcpDecoded(t *testing.T, data []byte) any {
	t.Helper()
	var value any
	if err := json.Unmarshal(data, &value); err != nil {
		t.Fatal(err)
	}
	return value
}

// Snapshot names, kinds, modes, file modification times and bytes, including
// nested metadata. Directory timestamps can lag fixture creation on Windows;
// access times may change on reads. Neither is compared.
func mcpTree(t *testing.T, root string) string {
	t.Helper()
	var snapshot strings.Builder
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		fmt.Fprintf(&snapshot, "%s\x00%s\x00", path, info.Mode())
		if info.Mode().IsRegular() {
			fmt.Fprintf(&snapshot, "%d\x00", info.ModTime().UnixNano())
			data, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			snapshot.Write(data)
		}
		snapshot.WriteByte(0)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return snapshot.String()
}

func mcpWrite(t *testing.T, path string, data []byte) {
	t.Helper()
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
}

func mcpDirectoryLink(t *testing.T, target, link string) {
	t.Helper()
	if runtime.GOOS == "windows" {
		cmd := exec.CommandContext(t.Context(), "cmd", "/c", "mklink", "/J", link, target)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("create junction: %v: %s", err, out)
		}
	} else if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
}

// Before readiness every tool was local-only. This restriction still applies
// to status/next/configuration/metrics/evaluation calls (including failures), initialization and
// discovery; readiness's and doctor's external read allowlists use real stdio clients.
func TestMCPLocalToolsNeverRunExternalCommands(t *testing.T) {
	env, _, stderr := testEnv(t)
	writeConfig(t, env.RepoRoot, "invalid config is not loaded")
	env.LookPath = func(name string) (string, error) {
		t.Errorf("unexpected executable lookup: %s", name)
		return "", fmt.Errorf("external commands forbidden")
	}
	env.Runner = fakeRunner{beforeRun: func(cmd execx.Cmd) { t.Errorf("unexpected external command: %+v", cmd) }}
	input, clientWriter := io.Pipe()
	clientReader, output := io.Pipe()
	env.Stdin, env.Stdout = input, output
	before := mcpTree(t, env.RepoRoot)
	done := make(chan int, 1)
	go func() { done <- Run([]string{"mcp"}, env) }()
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	client := mcp.NewClient(&mcp.Implementation{Name: "no-exec-test", Version: "1"}, nil)
	session, err := client.Connect(ctx, &mcp.IOTransport{Reader: clientReader, Writer: clientWriter}, nil)
	if err != nil {
		t.Fatal(err)
	}
	mcpDiscover(t, ctx, session, env.RepoRoot)
	if mcpTree(t, env.RepoRoot) != before {
		t.Fatal("initialization/discovery changed repository files")
	}
	mcpCall(t, ctx, session, env.RepoRoot, "orch_run_status", map[string]any{})
	mcpCall(t, ctx, session, env.RepoRoot, "orch_run_next", map[string]any{})
	if result := mcpCall(t, ctx, session, env.RepoRoot, "orch_eval_status", map[string]any{"run_id": mcpEvalID}); !result.IsError {
		t.Fatalf("unbound evaluation storage result = %+v", result)
	}
	if result := mcpCall(t, ctx, session, env.RepoRoot, "orch_config_effective", map[string]any{}); !result.IsError || result.StructuredContent != nil {
		t.Fatalf("invalid configuration result = %+v", result)
	}
	for _, tool := range []string{"orch_metrics", "orch_metrics_report"} {
		if result := mcpCall(t, ctx, session, env.RepoRoot, tool, map[string]any{}); !result.IsError {
			t.Fatalf("invalid metrics configuration result = %+v", result)
		}
	}
	mcpWrite(t, filepath.Join(env.RepoRoot, ".orchestrator", "config.toml"), []byte(validTOML))
	for _, tool := range []string{"orch_metrics", "orch_metrics_report"} {
		if result := mcpCall(t, ctx, session, env.RepoRoot, tool, map[string]any{}); result.IsError {
			t.Fatalf("empty metrics result = %+v", result)
		}
	}
	for _, doc := range mcpMetricsFixtures(t) {
		writeMetricsFixture(t, env.RepoRoot, doc.RunID, doc)
	}
	for _, tool := range []string{"orch_metrics", "orch_metrics_report"} {
		if result := mcpCall(t, ctx, session, env.RepoRoot, tool, map[string]any{}); result.IsError {
			t.Fatalf("recorded metrics result = %+v", result)
		}
	}
	mcpWrite(t, filepath.Join(env.RepoRoot, ".orchestrator", "metrics", "run-corrupt.json"), []byte("{corrupt metrics"))
	for _, tool := range []string{"orch_metrics", "orch_metrics_report"} {
		if result := mcpCall(t, ctx, session, env.RepoRoot, tool, map[string]any{}); !result.IsError {
			t.Fatalf("corrupt metrics result = %+v", result)
		}
	}
	if _, err := state.EnterDelivery(env.RepoRoot, "claude", testApprovedPlanRef(t, env.RepoRoot), testIssues()); err != nil {
		t.Fatal(err)
	}
	mcpCall(t, ctx, session, env.RepoRoot, "orch_run_next", map[string]any{})
	mcpConfigParity(t, ctx, env.RepoRoot, session)
	mcpWrite(t, filepath.Join(env.RepoRoot, state.Path), []byte("{corrupt state"))
	mcpCall(t, ctx, session, env.RepoRoot, "orch_run_status", map[string]any{})
	mcpCall(t, ctx, session, env.RepoRoot, "orch_run_next", map[string]any{})
	mcpConfigParity(t, ctx, env.RepoRoot, session)
	before = mcpTree(t, env.RepoRoot)
	if err := session.Close(); err != nil {
		t.Fatal(err)
	}
	select {
	case code := <-done:
		if code != ExitOK || stderr.Len() != 0 {
			t.Fatalf("server exit=%d stderr=%s", code, stderr.String())
		}
	case <-ctx.Done():
		t.Fatal("server did not shut down")
	}
	if mcpTree(t, env.RepoRoot) != before {
		t.Fatal("shutdown changed repository files")
	}
	if err := output.Close(); err != nil {
		t.Fatal(err)
	}
	if err := input.Close(); err != nil {
		t.Fatal(err)
	}
}
