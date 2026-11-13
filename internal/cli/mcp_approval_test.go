package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/kninetimmy/orch/internal/execx"
	"github.com/kninetimmy/orch/internal/execx/execxtest"
	"github.com/kninetimmy/orch/internal/grant"
	"github.com/kninetimmy/orch/internal/lockfile"
	"github.com/kninetimmy/orch/internal/manifest"
	"github.com/kninetimmy/orch/internal/paths"
	"github.com/kninetimmy/orch/internal/run"
	"github.com/kninetimmy/orch/internal/state"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func mcpApprovalExercise(t *testing.T, binary string) {
	t.Helper()
	const readinessTestHead = "0123456789abcdef0123456789abcdef01234567"
	root, cwd, common, probes := t.TempDir(), t.TempDir(), t.TempDir(), t.TempDir()
	writeConfig(t, root, validTOML+"\n[metrics]\nenabled = true\n")
	writeConfig(t, cwd, "invalid launching repository [")
	mcpWrite(t, filepath.Join(cwd, state.Path), []byte("{corrupt cwd"))
	canonical, err := paths.Canonical(root)
	if err != nil {
		t.Fatal(err)
	}
	common, err = paths.Canonical(common)
	if err != nil {
		t.Fatal(err)
	}
	suffix := ""
	if runtime.GOOS == "windows" {
		suffix = ".exe"
	}
	// Reuse the existing scripted executable, with only these two Git reads
	// in its fixture. It also checks cwd identity and logs outside all stores.
	build := exec.CommandContext(t.Context(), "go", "build", "-o", filepath.Join(probes, "git"+suffix), "./testdata/doctor-probe")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build approval probes: %v: %s", err, out)
	}
	const topProbe = "git rev-parse --show-toplevel"
	const commonProbe = "git rev-parse --path-format=absolute --git-common-dir"
	fixturePath, logPath := filepath.Join(probes, "fixture.json"), filepath.Join(probes, "commands.log")
	setProbes := func(top, commonResult execx.Result) {
		t.Helper()
		data, err := json.Marshal(map[string]any{"Root": canonical, "Log": logPath, "Probes": map[string]execx.Result{
			topProbe: top, commonProbe: commonResult,
		}})
		if err != nil {
			t.Fatal(err)
		}
		mcpWrite(t, fixturePath, data)
	}
	resetProbes := func() { setProbes(execx.Result{Stdout: canonical}, execx.Result{Stdout: common}) }
	resetProbes()
	ctx, cancel := context.WithTimeout(t.Context(), 90*time.Second)
	defer cancel()
	cmd := mcpProcess(ctx, binary, cwd, "mcp", "--repo", root)
	cmd.Env = append(cmd.Env, "PATH="+probes, "ORCH_TEST_DOCTOR_FIXTURE="+fixturePath)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	before, cwdBefore, commonBefore := mcpTree(t, root), mcpTree(t, cwd), mcpTree(t, common)
	client := mcp.NewClient(&mcp.Implementation{Name: "approval-test", Version: "1"}, nil)
	session, err := client.Connect(ctx, &mcp.CommandTransport{Command: cmd}, nil)
	if err != nil {
		t.Fatalf("connect: %v: %s", err, &stderr)
	}
	closed := false
	closeSession := func() {
		t.Helper()
		if closed {
			return
		}
		closed = true
		before, cwdBefore, commonBefore := mcpTree(t, root), mcpTree(t, cwd), mcpTree(t, common)
		if err := session.Close(); err != nil {
			t.Error(err)
		}
		if cmd.ProcessState == nil || !cmd.ProcessState.Success() || stderr.Len() != 0 ||
			mcpTree(t, root) != before || mcpTree(t, cwd) != cwdBefore || mcpTree(t, common) != commonBefore {
			t.Errorf("approval shutdown/no-write: state=%v stderr=%s", cmd.ProcessState, &stderr)
		}
	}
	t.Cleanup(closeSession)
	call := func(args any) *mcp.CallToolResult {
		t.Helper()
		commonBefore, cwdBefore := mcpTree(t, common), mcpTree(t, cwd)
		result := mcpCall(t, ctx, session, root, "orch_approval_status", args)
		if mcpTree(t, common) != commonBefore || mcpTree(t, cwd) != cwdBefore {
			t.Fatal("approval call changed common-directory or launching-repository files")
		}
		return result
	}
	errorCall := func(want string) {
		t.Helper()
		result := call(map[string]any{})
		if !result.IsError || result.StructuredContent != nil || len(result.Content) != 1 {
			t.Fatalf("approval failure returned a report: %+v", result)
		}
		text, ok := result.Content[0].(*mcp.TextContent)
		if !ok || !strings.Contains(text.Text, want) {
			t.Fatalf("approval error missing %q: %+v", want, result.Content)
		}
	}
	parity := func(wantGrant *grant.Grant) *run.ApprovalStatusDoc {
		t.Helper()
		script := &execxtest.Script{T: t, Calls: []execxtest.Call{
			{Name: "git", Args: []string{"rev-parse", "--show-toplevel"}, Dir: canonical, Stdout: canonical},
			{Name: "git", Args: []string{"rev-parse", "--path-format=absolute", "--git-common-dir"}, Dir: canonical, Stdout: common},
		}}
		want, err := run.ApprovalStatus(ctx, run.Env{RepoRoot: canonical, Runner: script})
		script.AssertExhausted()
		if err != nil || want.SchemaVersion != 1 || !reflect.DeepEqual(want.ActiveGrant, wantGrant) {
			t.Fatalf("shared snapshot: %+v, %v, want grant %+v", want, err, wantGrant)
		}
		encoded, err := json.Marshal(want)
		if err != nil {
			t.Fatal(err)
		}
		result := call(map[string]any{})
		if result.IsError || len(result.Content) != 1 {
			t.Fatalf("approval result: %+v", result)
		}
		text, ok := result.Content[0].(*mcp.TextContent)
		if !ok || !reflect.DeepEqual(result.StructuredContent, mcpDecoded(t, encoded)) || !reflect.DeepEqual(mcpDecoded(t, []byte(text.Text)), mcpDecoded(t, encoded)) {
			t.Fatalf("approval shared/structured/text mismatch: want=%s result=%+v", encoded, result)
		}
		status, err := mcpProcess(ctx, binary, root, "run", "status", "--json").CombinedOutput()
		if err != nil {
			t.Fatalf("status CLI: %v: %s", err, status)
		}
		if !reflect.DeepEqual(result.StructuredContent.(map[string]any)["status"], mcpDecoded(t, status)) {
			t.Fatalf("approval status differs from existing CLI status: %s", status)
		}
		if wantGrant == nil && !strings.Contains(text.Text, `"active_grant":null`) {
			t.Fatal("grant absence is not explicit")
		}
		t.Logf("approval: shared = structured = JSON text; nested status = CLI; active grant=%t; repositories/common-directory unchanged", wantGrant != nil)
		return want
	}
	mcpDiscover(t, ctx, session, root)
	for _, args := range []any{json.RawMessage("null"), []any{}, "object", 1, true,
		map[string]any{"repo": cwd}, map[string]any{"repo_root": cwd}, map[string]any{"grant_id": "other"},
		map[string]any{"issue_number": 465}, map[string]any{"unexpected": true}} {
		if result := call(args); !result.IsError || result.StructuredContent != nil {
			t.Fatalf("approval accepted %#v: %+v", args, result)
		}
	}
	if _, err := os.Stat(logPath); !os.IsNotExist(err) {
		t.Fatalf("initialization/discovery/invalid inputs launched a probe: %v", err)
	}
	if mcpTree(t, root) != before || mcpTree(t, cwd) != cwdBefore || mcpTree(t, common) != commonBefore {
		t.Fatal("initialization/discovery/invalid inputs changed files")
	}
	parity(nil) // Missing grant directory stays missing, even with session/metrics context.
	dir := filepath.Join(common, grant.DirName)
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatalf("inspection created the grant directory: %v", err)
	}
	if err := os.Mkdir(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	parity(nil) // Existing empty directory must not acquire a lock file either.
	at := time.Date(2026, 11, 11, 12, 0, 0, 0, time.UTC)
	g := &grant.Grant{SchemaVersion: grant.SchemaVersion, Terms: grant.Terms{
		ID: "grant-20261111T120000Z-00000001", Scope: []grant.ScopeItem{{Name: "approval", Description: "visibility only"}},
		Gates: []string{"plan", "merge"}, ExpiresAt: at.AddDate(100, 0, 0), RunLimit: 1, MergeLimit: 1,
		FixCycleLimit: 2, ContextThreshold: 450000, RelayPermissionMode: "acceptEdits", SessionID: "creator",
	}, ApprovedBy: "human", ApprovedAt: at, CreatedAt: at, SessionID: "relayed-holder",
		Runs: []grant.Use{{Ref: "run-recorded", At: at}}, Merges: []grant.Use{{Ref: "run-recorded#465", At: at}},
		Approvals: []grant.RecordedApproval{{Gate: "plan", RunID: "run-recorded", At: at}, {Gate: "merge", RunID: "run-recorded", Issue: 465, PR: 466, Head: readinessTestHead, At: at}},
		Relays:    []grant.Relay{{From: "creator", To: "relayed-holder", At: at}},
	}
	g.Digest, err = grant.Digest(g.Terms)
	if err != nil {
		t.Fatal(err)
	}
	writeGrant := func(record *grant.Grant) {
		t.Helper()
		data, err := json.Marshal(record)
		if err != nil {
			t.Fatal(err)
		}
		mcpWrite(t, filepath.Join(dir, record.Terms.ID+".json"), data)
	}
	writeGrant(g)
	parity(g) // Exhausted limits and a different holder do not imply authorization.
	plan := state.PlanRef{Title: "approval fixture", Digest: "sha256:plan", ApprovedBy: "human", ApprovedAt: at,
		ConfigRevision: "r1", ContractVersion: run.ContractVersion, ExecutionDigest: "sha256:execution"}
	st, err := state.EnterDelivery(root, "claude", plan, testIssues())
	if err != nil {
		t.Fatal(err)
	}
	st.Run.Issues = []state.Issue{{PlanID: "a", Number: 465, Title: "pinned", Phase: state.PhaseAwaitingMerge,
		Branch: "orch/issue-465", Worktree: ".orchestrator/worktrees/issue-465", PRNumber: 466, ApprovedHeadOID: readinessTestHead,
		Decision: &state.Decision{Role: manifest.RoleSpecialist, Executor: manifest.Selection{Model: "executor", Effort: "high"}, Reviewer: manifest.Selection{Model: "reviewer", Effort: "high"}},
	}}
	if err := state.Save(root, st); err != nil {
		t.Fatal(err)
	}
	doc := parity(g)
	if doc.Status.Run.Plan != plan || doc.Status.Run.Issues[0].ApprovedHeadOID != readinessTestHead {
		t.Fatal("plan metadata or merge pin lost")
	}
	if err := os.Remove(filepath.Join(root, lockfile.Path)); err != nil {
		t.Fatal(err)
	}
	if doc := parity(g); doc.Status.Consistent || doc.Status.Warning == "" {
		t.Fatal("inconsistent-state warning lost")
	}
	g.SessionID = "fresh-holder"
	writeGrant(g)
	parity(g)
	g.RevokedAt = &at
	writeGrant(g)
	parity(nil)
	g.RevokedAt, g.Terms.ExpiresAt = nil, at.AddDate(-100, 0, 0)
	writeGrant(g)
	parity(nil)
	g.Terms.ExpiresAt = at.AddDate(100, 0, 0)
	writeGrant(g)
	other := *g
	other.Terms.ID = "grant-20261111T120000Z-00000002"
	writeGrant(&other)
	errorCall("both active; refusing to choose")
	otherPath := filepath.Join(dir, other.Terms.ID+".json")
	for _, data := range []string{"{malformed", `{"schema_version":99}`, `{"schema_version":1,"unknown":true}`} {
		mcpWrite(t, otherPath, []byte(data))
		errorCall("autonomy grant")
	}
	if err := os.Remove(otherPath); err != nil {
		t.Fatal(err)
	}
	parity(g)
	for _, tc := range []struct {
		top, common execx.Result
		want        string
	}{
		{execx.Result{ExitCode: 1, Stderr: "top probe failed"}, execx.Result{Stdout: common}, "top probe failed"},
		{execx.Result{Stdout: common}, execx.Result{Stdout: common}, "not the git top level"},
		{execx.Result{Stdout: canonical}, execx.Result{ExitCode: 1, Stderr: "common probe failed"}, "common probe failed"},
		{execx.Result{Stdout: canonical}, execx.Result{}, "must be absolute"},
		{execx.Result{Stdout: canonical}, execx.Result{Stdout: filepath.Join(common, "missing")}, "inspect git common directory"},
	} {
		setProbes(tc.top, tc.common)
		errorCall(tc.want)
	}
	resetProbes()
	parity(g) // Same-connection recovery from every grant/Git failure.
	mcpWrite(t, filepath.Join(root, state.Path), []byte("{corrupt state"))
	errorCall("parse .orchestrator/state.json")
	if err := state.Save(root, st); err != nil {
		t.Fatal(err)
	}
	parity(g)
	if _, err := os.Stat(filepath.Join(dir, "lock")); !os.IsNotExist(err) {
		t.Fatalf("approval observation created a grant lock file: %v", err)
	}
	log, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatal(err)
	}
	for _, line := range strings.Split(strings.TrimSpace(string(log)), "\n") {
		parts := strings.SplitN(line, "\t", 2)
		if len(parts) != 2 || (parts[1] != topProbe && parts[1] != commonProbe) {
			t.Fatalf("unexpected approval probe: %q", line)
		}
	}
	closeSession()
	// Missing Git must fail explicitly rather than masquerade as no grant.
	noGit, closeNoGit := mcpConnect(t, ctx, binary, cwd, root, "--repo", root)
	result := mcpCall(t, ctx, noGit, root, "orch_approval_status", map[string]any{})
	if !result.IsError || result.StructuredContent != nil {
		t.Fatalf("missing Git returned a snapshot: %+v", result)
	}
	closeNoGit()
}
