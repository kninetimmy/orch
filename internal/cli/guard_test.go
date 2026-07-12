package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kninetimmy/orch/internal/manifest"
	"github.com/kninetimmy/orch/internal/paths"
	"github.com/kninetimmy/orch/internal/state"
)

// guardRepo makes env.RepoRoot an Assist orch repo (an .orchestrator
// directory, no state.json) and returns the root plus a target path
// under it. The check-ignore probe is scripted by checkIgnoreExit.
func guardRepo(t *testing.T, checkIgnoreExit int) (Env, *bytes.Buffer, *bytes.Buffer, string) {
	t.Helper()
	env, stdout, stderr := testEnv(t)
	root := env.RepoRoot
	if err := os.Mkdir(filepath.Join(root, paths.OrchestratorDir), 0o755); err != nil {
		t.Fatal(err)
	}
	env.Runner = fakeRunner{toplevel: root, checkIgnoreExit: checkIgnoreExit}
	return env, stdout, stderr, root
}

// claudePayload marshals a minimal PreToolUse event.
func claudePayload(t *testing.T, tool, pathKey, pathVal, cwd string) string {
	t.Helper()
	m := map[string]any{
		"tool_name":  tool,
		"tool_input": map[string]string{pathKey: pathVal},
	}
	if cwd != "" {
		m["cwd"] = cwd
	}
	b, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// codexPayload marshals a minimal Codex CLI PreToolUse event whose
// tool_input carries envelope as its apply_patch command.
func codexPayload(t *testing.T, toolName, envelope, cwd string) string {
	t.Helper()
	m := map[string]any{
		"tool_name":  toolName,
		"tool_input": map[string]string{"command": envelope},
	}
	if cwd != "" {
		m["cwd"] = cwd
	}
	b, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// deliveryGuardEnv builds a Delivery repo with one dispatched issue
// (deliveryIssue) and its registered worktree checked out on the
// matching branch — the shared fixture for every guard test that needs
// an active run, so check and the host hook verbs exercise the same
// containment rules.
func deliveryGuardEnv(t *testing.T) (env Env, stdout, stderr *bytes.Buffer, worktreeAbs string) {
	t.Helper()
	env, stdout, stderr, root := guardRepo(t, 1)
	planned := []state.Issue{{PlanID: "a", Title: "A", Phase: state.PhasePlanned}}
	plan := state.PlanRef{Title: "t", Digest: "sha256:x", ConfigRevision: "r1"}
	if _, err := state.EnterDelivery(root, "claude", plan, planned); err != nil {
		t.Fatal(err)
	}
	st, err := state.Load(root)
	if err != nil {
		t.Fatal(err)
	}
	st.Run.Issues = []state.Issue{deliveryIssue()}
	if err := state.Save(root, st); err != nil {
		t.Fatal(err)
	}
	worktreeAbs = filepath.Join(root, "wt")
	writeGitPointer(t, worktreeAbs, "ref: refs/heads/feature-3\n")
	return env, stdout, stderr, worktreeAbs
}

func TestGuardCheckAllowIgnored(t *testing.T) {
	env, stdout, stderr, root := guardRepo(t, 0) // 0 = ignored
	target := filepath.Join(root, "build", "out.o")
	if code := Run([]string{"guard", "check", target}, env); code != ExitOK {
		t.Fatalf("exit = %d, want %d (stderr %q)", code, ExitOK, stderr.String())
	}
	if stdout.String() != "" || stderr.String() != "" {
		t.Errorf("allow was not silent: stdout %q stderr %q", stdout.String(), stderr.String())
	}
}

func TestGuardCheckDenyTracked(t *testing.T) {
	env, _, stderr, root := guardRepo(t, 1) // 1 = not ignored
	target := filepath.Join(root, "src", "x.go")
	if code := Run([]string{"guard", "check", target}, env); code != ExitError {
		t.Fatalf("exit = %d, want %d", code, ExitError)
	}
	if !strings.Contains(stderr.String(), "assist is read-only") {
		t.Errorf("stderr missing deny reason: %q", stderr.String())
	}
	// The verdict names the canonical path, which can differ from the
	// raw temp path (8.3 short segments on Windows, /var symlinks on
	// macOS), so canonicalize before comparing.
	canon, err := paths.Canonical(target)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stderr.String(), canon) {
		t.Errorf("stderr does not name the path %q: %q", canon, stderr.String())
	}
}

func TestGuardCheckUsage(t *testing.T) {
	cases := map[string][]string{
		"no verb":      {"guard"},
		"unknown verb": {"guard", "frobnicate"},
		"no paths":     {"guard", "check"},
		"bad flag":     {"guard", "check", "--nope", "x"},
		"bad role":     {"guard", "check", "--role", "wizard", "x"},
		"bad issue":    {"guard", "check", "--issue", "-2", "x"},
	}
	for name, args := range cases {
		t.Run(name, func(t *testing.T) {
			env, _, stderr, _ := guardRepo(t, 1)
			if code := Run(args, env); code != ExitUsage {
				t.Fatalf("exit = %d, want %d", code, ExitUsage)
			}
			if !strings.Contains(stderr.String(), "orch guard: usage") {
				t.Errorf("stderr missing guardUsage: %q", stderr.String())
			}
		})
	}
}

func TestGuardClaudeAllowSilent(t *testing.T) {
	env, stdout, stderr, root := guardRepo(t, 0) // ignored → allow
	target := filepath.Join(root, "build", "out.o")
	env.Stdin = strings.NewReader(claudePayload(t, "Write", "file_path", target, ""))
	if code := Run([]string{"guard", "claude"}, env); code != ExitOK {
		t.Fatalf("exit = %d, want %d (stderr %q)", code, ExitOK, stderr.String())
	}
	if stdout.String() != "" {
		t.Errorf("allow emitted output: %q", stdout.String())
	}
}

func TestGuardClaudeDenyJSON(t *testing.T) {
	tools := map[string][2]string{
		"Write":        {"Write", "file_path"},
		"Edit":         {"Edit", "file_path"},
		"NotebookEdit": {"NotebookEdit", "notebook_path"},
	}
	const wantReason = "assist is read-only for repository files; ask for a Delivery plan"
	want := `{"hookSpecificOutput":{"hookEventName":"PreToolUse","permissionDecision":"deny","permissionDecisionReason":"` + wantReason + `"}}`
	for name, tk := range tools {
		t.Run(name, func(t *testing.T) {
			env, stdout, _, root := guardRepo(t, 1) // not ignored → deny
			target := filepath.Join(root, "src", "x.go")
			env.Stdin = strings.NewReader(claudePayload(t, tk[0], tk[1], target, ""))
			if code := Run([]string{"guard", "claude"}, env); code != ExitOK {
				t.Fatalf("exit = %d, want %d", code, ExitOK)
			}
			if strings.TrimSpace(stdout.String()) != want {
				t.Errorf("stdout = %q, want %q", stdout.String(), want)
			}
		})
	}
}

func TestGuardClaudeUnknownToolDenies(t *testing.T) {
	env, stdout, _, _ := guardRepo(t, 1)
	env.Stdin = strings.NewReader(`{"tool_name":"Bash","tool_input":{"command":"rm -rf /"}}`)
	if code := Run([]string{"guard", "claude"}, env); code != ExitOK {
		t.Fatalf("exit = %d, want %d", code, ExitOK)
	}
	var out struct {
		HookSpecificOutput struct {
			PermissionDecision       string `json:"permissionDecision"`
			PermissionDecisionReason string `json:"permissionDecisionReason"`
		} `json:"hookSpecificOutput"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &out); err != nil {
		t.Fatalf("stdout is not the deny document: %q (%v)", stdout.String(), err)
	}
	if out.HookSpecificOutput.PermissionDecision != "deny" {
		t.Errorf("permissionDecision = %q, want deny", out.HookSpecificOutput.PermissionDecision)
	}
	if !strings.Contains(out.HookSpecificOutput.PermissionDecisionReason, "unrecognized tool_name") {
		t.Errorf("reason = %q, want unrecognized tool_name", out.HookSpecificOutput.PermissionDecisionReason)
	}
}

func TestGuardClaudeMalformedJSONExits2(t *testing.T) {
	env, stdout, _, _ := guardRepo(t, 1)
	env.Stdin = strings.NewReader(`{ not json`)
	if code := Run([]string{"guard", "claude"}, env); code != ExitUsage {
		t.Fatalf("exit = %d, want %d", code, ExitUsage)
	}
	if stdout.String() != "" {
		t.Errorf("internal failure emitted a decision: %q", stdout.String())
	}
}

func TestGuardClaudeRelativePathNoCWDExits2(t *testing.T) {
	env, _, _, _ := guardRepo(t, 1)
	env.Stdin = strings.NewReader(claudePayload(t, "Write", "file_path", "src/x.go", ""))
	if code := Run([]string{"guard", "claude"}, env); code != ExitUsage {
		t.Fatalf("exit = %d, want %d", code, ExitUsage)
	}
}

// TestGuardClaudeNeverExitsOne feeds a state-corruption scenario that
// `check` would surface as exit 1; `claude` must instead exit 2 (the
// hook's blocking code — exit 1 there is a fail-open trap).
func TestGuardClaudeNeverExitsOne(t *testing.T) {
	env, stdout, _, root := guardRepo(t, 1)
	if err := os.WriteFile(filepath.Join(root, filepath.FromSlash(state.Path)), []byte("{ not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(root, "src", "x.go")
	env.Stdin = strings.NewReader(claudePayload(t, "Write", "file_path", target, ""))
	if code := Run([]string{"guard", "claude"}, env); code != ExitUsage {
		t.Fatalf("exit = %d, want %d (blocking)", code, ExitUsage)
	}
	if stdout.String() != "" {
		t.Errorf("corruption emitted a decision instead of blocking: %q", stdout.String())
	}
	// The same corruption through `check` is exit 1, confirming the split.
	env2, _, _, root2 := guardRepo(t, 1)
	if err := os.WriteFile(filepath.Join(root2, filepath.FromSlash(state.Path)), []byte("{ not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	if code := Run([]string{"guard", "check", filepath.Join(root2, "src", "x.go")}, env2); code != ExitError {
		t.Fatalf("check exit = %d, want %d", code, ExitError)
	}
}

func TestGuardCheckDeliveryAllows(t *testing.T) {
	env, _, stderr, worktreeAbs := deliveryGuardEnv(t)
	target := filepath.Join(worktreeAbs, "src", "x.go")

	if code := Run([]string{"guard", "check", "--role", "implementer", "--issue", "3", target}, env); code != ExitOK {
		t.Fatalf("exit = %d, want %d (stderr %q)", code, ExitOK, stderr.String())
	}
}

func TestGuardCodexAllowSilent(t *testing.T) {
	env, stdout, _, worktreeAbs := deliveryGuardEnv(t)
	envelope := "*** Begin Patch\n*** Update File: src/x.go\n@@ hunk header\n-old\n+new\n*** End Patch"
	env.Stdin = strings.NewReader(codexPayload(t, "apply_patch", envelope, worktreeAbs))
	if code := Run([]string{"guard", "codex", "--role", "implementer", "--issue", "3"}, env); code != ExitOK {
		t.Fatalf("exit = %d, want %d (stdout %q)", code, ExitOK, stdout.String())
	}
	if stdout.String() != "" {
		t.Errorf("allow emitted output: %q", stdout.String())
	}
}

func TestGuardCodexDenyJSON(t *testing.T) {
	env, stdout, _, root := guardRepo(t, 1) // not ignored → deny
	envelope := "*** Begin Patch\n*** Update File: src/x.go\n@@ hunk header\n-old\n+new\n*** End Patch"
	env.Stdin = strings.NewReader(codexPayload(t, "apply_patch", envelope, root))
	const wantReason = "assist is read-only for repository files; ask for a Delivery plan"
	want := `{"hookSpecificOutput":{"hookEventName":"PreToolUse","permissionDecision":"deny","permissionDecisionReason":"` + wantReason + `"}}`
	if code := Run([]string{"guard", "codex"}, env); code != ExitOK {
		t.Fatalf("exit = %d, want %d", code, ExitOK)
	}
	if strings.TrimSpace(stdout.String()) != want {
		t.Errorf("stdout = %q, want %q", stdout.String(), want)
	}
}

func TestGuardCodexUnknownToolDenies(t *testing.T) {
	env, stdout, _, _ := guardRepo(t, 1)
	env.Stdin = strings.NewReader(`{"tool_name":"Bash","tool_input":{"command":"rm -rf /"}}`)
	if code := Run([]string{"guard", "codex"}, env); code != ExitOK {
		t.Fatalf("exit = %d, want %d", code, ExitOK)
	}
	var out struct {
		HookSpecificOutput struct {
			PermissionDecision       string `json:"permissionDecision"`
			PermissionDecisionReason string `json:"permissionDecisionReason"`
		} `json:"hookSpecificOutput"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &out); err != nil {
		t.Fatalf("stdout is not the deny document: %q (%v)", stdout.String(), err)
	}
	if out.HookSpecificOutput.PermissionDecision != "deny" {
		t.Errorf("permissionDecision = %q, want deny", out.HookSpecificOutput.PermissionDecision)
	}
	if !strings.Contains(out.HookSpecificOutput.PermissionDecisionReason, "unrecognized tool_name") {
		t.Errorf("reason = %q, want unrecognized tool_name", out.HookSpecificOutput.PermissionDecisionReason)
	}
}

// TestGuardCodexMalformedEnvelopeDeniesWithJSON confirms a garbage
// apply_patch envelope is a deny document at exit 0, not the blocking
// exit 2: ErrPatchEnvelope is guard's other deny-by-default sentinel,
// same status as ErrUnknownTool.
func TestGuardCodexMalformedEnvelopeDeniesWithJSON(t *testing.T) {
	env, stdout, _, root := guardRepo(t, 1)
	env.Stdin = strings.NewReader(codexPayload(t, "apply_patch", "not a patch envelope at all", root))
	if code := Run([]string{"guard", "codex"}, env); code != ExitOK {
		t.Fatalf("exit = %d, want %d (garbage envelope must deny, not block)", code, ExitOK)
	}
	var out struct {
		HookSpecificOutput struct {
			PermissionDecision       string `json:"permissionDecision"`
			PermissionDecisionReason string `json:"permissionDecisionReason"`
		} `json:"hookSpecificOutput"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &out); err != nil {
		t.Fatalf("stdout is not the deny document: %q (%v)", stdout.String(), err)
	}
	if out.HookSpecificOutput.PermissionDecision != "deny" {
		t.Errorf("permissionDecision = %q, want deny", out.HookSpecificOutput.PermissionDecision)
	}
}

func TestGuardCodexMalformedJSONExits2(t *testing.T) {
	env, stdout, _, _ := guardRepo(t, 1)
	env.Stdin = strings.NewReader(`{ not json`)
	if code := Run([]string{"guard", "codex"}, env); code != ExitUsage {
		t.Fatalf("exit = %d, want %d", code, ExitUsage)
	}
	if stdout.String() != "" {
		t.Errorf("internal failure emitted a decision: %q", stdout.String())
	}
}

// TestGuardCodexMoveDeniesWhenDestinationOutsideWorktree confirms a Move
// to destination is checked as its own write target: an Update inside
// the registered worktree paired with a Move that escapes it must deny,
// naming the escaping path.
func TestGuardCodexMoveDeniesWhenDestinationOutsideWorktree(t *testing.T) {
	env, stdout, _, worktreeAbs := deliveryGuardEnv(t)
	envelope := "*** Begin Patch\n*** Update File: src/x.go\n@@ hunk header\n-old\n+new\n*** Move to: ../escaped.go\n*** End Patch"
	env.Stdin = strings.NewReader(codexPayload(t, "apply_patch", envelope, worktreeAbs))
	if code := Run([]string{"guard", "codex"}, env); code != ExitOK {
		t.Fatalf("exit = %d, want %d", code, ExitOK)
	}
	var out struct {
		HookSpecificOutput struct {
			PermissionDecision       string `json:"permissionDecision"`
			PermissionDecisionReason string `json:"permissionDecisionReason"`
		} `json:"hookSpecificOutput"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &out); err != nil {
		t.Fatalf("stdout is not the deny document: %q (%v)", stdout.String(), err)
	}
	if out.HookSpecificOutput.PermissionDecision != "deny" {
		t.Errorf("permissionDecision = %q, want deny", out.HookSpecificOutput.PermissionDecision)
	}
	if !strings.Contains(out.HookSpecificOutput.PermissionDecisionReason, "outside every registered worktree") {
		t.Errorf("reason = %q, want outside every registered worktree", out.HookSpecificOutput.PermissionDecisionReason)
	}
}

func deliveryIssue() state.Issue {
	return state.Issue{
		PlanID:   "a",
		Title:    "A",
		Phase:    state.PhaseDispatched,
		Number:   3,
		Branch:   "feature-3",
		Worktree: "wt",
		Decision: &state.Decision{
			Role:      manifest.RoleImplementer,
			Executor:  manifest.Selection{Model: "m", Effort: "e"},
			Reviewer:  manifest.Selection{Model: "m", Effort: "e"},
			Rationale: "r",
		},
	}
}

func writeGitPointer(t *testing.T, worktreeAbs, head string) {
	t.Helper()
	gitMeta := filepath.Join(worktreeAbs, ".git-meta")
	if err := os.MkdirAll(gitMeta, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(gitMeta, "HEAD"), []byte(head), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(worktreeAbs, ".git"), []byte("gitdir: "+gitMeta+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestHelpListsGuard(t *testing.T) {
	env, stdout, _ := testEnv(t)
	if code := Run([]string{"help"}, env); code != ExitOK {
		t.Fatalf("exit = %d", code)
	}
	if !strings.Contains(stdout.String(), "guard") {
		t.Errorf("help does not list guard: %q", stdout.String())
	}
}
