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
		for _, args := range []any{map[string]any{"repo": cwd}, map[string]any{"unexpected": true}, []any{}} {
			result := mcpCall(t, ctx, session, root, args)
			if !result.IsError || result.StructuredContent != nil {
				t.Fatalf("unexpected arguments accepted: %+v", result)
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
		result := mcpCall(t, ctx, session, root, map[string]any{})
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

	t.Run("repository binding", func(t *testing.T) {
		root := t.TempDir()
		writeConfig(t, root, "invalid config is not loaded")
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
	cmd.Env = append(os.Environ(), "PATH=")
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
	if len(list.Tools) != 1 || list.NextCursor != "" {
		t.Fatalf("tool discovery = %+v", list)
	}
	tool := list.Tools[0]
	if tool.Name != "orch_run_status" || tool.Annotations == nil || !tool.Annotations.ReadOnlyHint {
		t.Fatalf("tool = %+v", tool)
	}
	want := mcpDecoded(t, []byte(`{"type":"object","properties":{},"additionalProperties":false}`))
	if !reflect.DeepEqual(tool.InputSchema, want) {
		t.Fatalf("input schema = %#v, want %#v", tool.InputSchema, want)
	}
	if mcpTree(t, root) != before {
		t.Fatal("discovery changed repository files")
	}
}

func mcpCall(t *testing.T, ctx context.Context, session *mcp.ClientSession, root string, args any) *mcp.CallToolResult {
	t.Helper()
	before := mcpTree(t, root)
	result, err := session.CallTool(ctx, &mcp.CallToolParams{Name: "orch_run_status", Arguments: args})
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
	before := mcpTree(t, root)
	cmd := mcpProcess(ctx, binary, root, "run", "status", "--json")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("CLI status: %v: %s", err, out)
	}
	result := mcpCall(t, ctx, session, root, map[string]any{})
	if result.IsError || len(result.Content) != 1 {
		t.Fatalf("status result = %+v", result)
	}
	text, ok := result.Content[0].(*mcp.TextContent)
	if !ok {
		t.Fatalf("status content = %T", result.Content[0])
	}
	want := mcpDecoded(t, out)
	if !reflect.DeepEqual(result.StructuredContent, want) || !reflect.DeepEqual(mcpDecoded(t, []byte(text.Text)), want) {
		t.Fatalf("status mismatch: CLI=%s structured=%v text=%s", out, result.StructuredContent, text.Text)
	}
	if mcpTree(t, root) != before {
		t.Fatal("discovery/status changed repository files")
	}
	var doc run.StatusDoc
	if err := json.Unmarshal(out, &doc); err != nil {
		t.Fatal(err)
	}
	t.Logf("CLI = structured = JSON text: schema=%d mode=%s consistent=%t", doc.SchemaVersion, doc.Mode, doc.Consistent)
	return &doc
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

func TestMCPNeverRunsExternalCommands(t *testing.T) {
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
	mcpCall(t, ctx, session, env.RepoRoot, map[string]any{})
	mcpWrite(t, filepath.Join(env.RepoRoot, state.Path), []byte("{corrupt state"))
	mcpCall(t, ctx, session, env.RepoRoot, map[string]any{})
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
	if err := output.Close(); err != nil {
		t.Fatal(err)
	}
	if err := input.Close(); err != nil {
		t.Fatal(err)
	}
}
