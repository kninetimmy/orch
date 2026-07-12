// guard_parity_test.go pins the PRD §23 cross-host invariant directly:
// the Claude Code and Codex CLI adapters answer the exact same
// PreToolUse/SessionStart questions the exact same way, modulo each
// host's own event envelope and (for session-start) its one host-varying
// closing sentence. internal/adaptertest pins the plugin artifacts;
// these tests pin the protocol-level behavior behind them, at the
// internal/guard and internal/cli layer both hosts' hooks actually call.
package cli

import (
	"bytes"
	"errors"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/kninetimmy/orch/internal/guard"
	"github.com/kninetimmy/orch/internal/manifest"
	"github.com/kninetimmy/orch/internal/state"
)

// equalPathSets reports whether a and b contain the same paths, ignoring
// order: PathsFromClaudeEvent and PathsFromCodexEvent extract targets in
// each host's own envelope order, which need not match between hosts for
// an equivalent event.
func equalPathSets(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	sortedA := append([]string(nil), a...)
	sortedB := append([]string(nil), b...)
	sort.Strings(sortedA)
	sort.Strings(sortedB)
	for i := range sortedA {
		if sortedA[i] != sortedB[i] {
			return false
		}
	}
	return true
}

// TestGuardEventPathExtractionParity asserts guard.PathsFromClaudeEvent
// and guard.PathsFromCodexEvent extract the identical absolute write
// target(s) from equivalent host events: a file create, an edit, a
// notebook edit, and a relative path resolved against cwd. It also
// confirms an unrecognized tool_name is ErrUnknownTool on both hosts.
func TestGuardEventPathExtractionParity(t *testing.T) {
	root := t.TempDir()
	createTarget := filepath.Join(root, "new.go")
	editTarget := filepath.Join(root, "existing.go")
	notebookTarget := filepath.Join(root, "notebook.ipynb")

	cases := []struct {
		name        string
		claudeEvent []byte
		codexEvent  []byte
	}{
		{
			name:        "create",
			claudeEvent: []byte(claudePayload(t, "Write", "file_path", createTarget, "")),
			codexEvent:  []byte(codexPayload(t, "apply_patch", "*** Begin Patch\n*** Add File: "+createTarget+"\n+contents\n*** End Patch", "")),
		},
		{
			name:        "edit",
			claudeEvent: []byte(claudePayload(t, "Edit", "file_path", editTarget, "")),
			codexEvent:  []byte(codexPayload(t, "apply_patch", "*** Begin Patch\n*** Update File: "+editTarget+"\n@@ hunk header\n-old\n+new\n*** End Patch", "")),
		},
		{
			name:        "notebook",
			claudeEvent: []byte(claudePayload(t, "NotebookEdit", "notebook_path", notebookTarget, "")),
			codexEvent:  []byte(codexPayload(t, "apply_patch", "*** Begin Patch\n*** Update File: "+notebookTarget+"\n@@ hunk header\n-old\n+new\n*** End Patch", "")),
		},
		{
			name:        "relative path resolved against cwd",
			claudeEvent: []byte(claudePayload(t, "Write", "file_path", "sub/new.go", root)),
			codexEvent:  []byte(codexPayload(t, "apply_patch", "*** Begin Patch\n*** Add File: sub/new.go\n+x\n*** End Patch", root)),
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			claudePaths, err := guard.PathsFromClaudeEvent(tc.claudeEvent)
			if err != nil {
				t.Fatalf("PathsFromClaudeEvent err = %v", err)
			}
			codexPaths, err := guard.PathsFromCodexEvent(tc.codexEvent)
			if err != nil {
				t.Fatalf("PathsFromCodexEvent err = %v", err)
			}
			if !equalPathSets(claudePaths, codexPaths) {
				t.Errorf("path sets differ: claude = %v, codex = %v", claudePaths, codexPaths)
			}
		})
	}

	t.Run("unknown tool", func(t *testing.T) {
		bashEvent := []byte(`{"tool_name":"Bash","tool_input":{"command":"rm -rf /"}}`)
		_, claudeErr := guard.PathsFromClaudeEvent(bashEvent)
		if !errors.Is(claudeErr, guard.ErrUnknownTool) {
			t.Errorf("claude err = %v, want ErrUnknownTool", claudeErr)
		}
		_, codexErr := guard.PathsFromCodexEvent(bashEvent)
		if !errors.Is(codexErr, guard.ErrUnknownTool) {
			t.Errorf("codex err = %v, want ErrUnknownTool", codexErr)
		}
	})
}

// TestGuardVerdictParityAcrossHosts asserts `orch guard claude` and
// `orch guard codex` reach the identical verdict for the equivalent
// scenario: an Assist-mode write to a tracked file (both deny), a
// Delivery write inside the registered worktree (both allow, silently),
// and a Delivery write outside every registered worktree (both deny).
// Each host gets its own fresh fixture (guard state lives on disk), built
// by the same closure so both fixtures place the write at the same
// scenario-relative location; the deny document text carries only the
// verdict's Reason, never its Path, so identical wording is the only bar
// to clear even though each fixture's temp-dir root differs.
func TestGuardVerdictParityAcrossHosts(t *testing.T) {
	cases := []struct {
		name  string
		build func(t *testing.T) (env Env, target string)
	}{
		{
			name: "assist tracked file denies",
			build: func(t *testing.T) (Env, string) {
				env, _, _, root := guardRepo(t, 1) // not ignored → deny
				return env, filepath.Join(root, "src", "x.go")
			},
		},
		{
			name: "delivery write inside registered worktree allows",
			build: func(t *testing.T) (Env, string) {
				env, _, _, worktreeAbs := deliveryGuardEnv(t)
				return env, filepath.Join(worktreeAbs, "src", "x.go")
			},
		},
		{
			name: "delivery write outside registered worktree denies",
			build: func(t *testing.T) (Env, string) {
				env, _, _, worktreeAbs := deliveryGuardEnv(t)
				return env, filepath.Join(filepath.Dir(worktreeAbs), "outside.go")
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			claudeEnv, claudeTarget := tc.build(t)
			var claudeStdout bytes.Buffer
			claudeEnv.Stdout = &claudeStdout
			claudeEnv.Stdin = strings.NewReader(claudePayload(t, "Write", "file_path", claudeTarget, ""))
			claudeCode := Run([]string{"guard", "claude"}, claudeEnv)

			codexEnv, codexTarget := tc.build(t)
			var codexStdout bytes.Buffer
			codexEnv.Stdout = &codexStdout
			envelope := "*** Begin Patch\n*** Update File: " + codexTarget + "\n@@ hunk header\n-old\n+new\n*** End Patch"
			codexEnv.Stdin = strings.NewReader(codexPayload(t, "apply_patch", envelope, ""))
			codexCode := Run([]string{"guard", "codex"}, codexEnv)

			if claudeCode != codexCode {
				t.Fatalf("exit codes differ: claude = %d, codex = %d", claudeCode, codexCode)
			}
			claudeOut := strings.TrimSpace(claudeStdout.String())
			codexOut := strings.TrimSpace(codexStdout.String())
			if claudeOut != codexOut {
				t.Errorf("deny documents differ:\nclaude: %q\ncodex:  %q", claudeOut, codexOut)
			}
		})
	}
}

// TestGuardInternalFailureParityExitsTwo asserts malformed JSON on stdin
// is the identical internal failure on both hosts: exit 2 (the hook
// protocol's blocking code), silent stdout — never the deny document, and
// never exit 1.
func TestGuardInternalFailureParityExitsTwo(t *testing.T) {
	claudeEnv, claudeStdout, _, _ := guardRepo(t, 1)
	claudeEnv.Stdin = strings.NewReader(`{ not json`)
	claudeCode := Run([]string{"guard", "claude"}, claudeEnv)

	codexEnv, codexStdout, _, _ := guardRepo(t, 1)
	codexEnv.Stdin = strings.NewReader(`{ not json`)
	codexCode := Run([]string{"guard", "codex"}, codexEnv)

	if claudeCode != ExitUsage || codexCode != ExitUsage {
		t.Fatalf("exit codes = claude %d, codex %d, want both %d", claudeCode, codexCode, ExitUsage)
	}
	if claudeStdout.String() != "" || codexStdout.String() != "" {
		t.Errorf("internal failure emitted output: claude %q, codex %q", claudeStdout.String(), codexStdout.String())
	}
}

// TestSessionStartParity extends TestHookSessionStartSharedLinesAcrossHosts
// (which already pins shared-lines parity for an Assist-mode repo) one
// notch further: a Delivery-mode repo with dispatched and in-review
// issues, mirroring TestHookCodexSessionStartDeliveryRun's fixture, must
// also render identical output across hosts apart from the closing
// sentence — in particular this exercises deliveryRunSummary's rendering,
// which the Assist-mode fixture never reaches.
func TestSessionStartParity(t *testing.T) {
	env, stdoutClaude, stderr := testEnv(t)
	writeConfig(t, env.RepoRoot, validTOML)
	planned := []state.Issue{{PlanID: "a", Title: "A", Phase: state.PhasePlanned}}
	plan := state.PlanRef{Title: "t", Digest: "sha256:x", ConfigRevision: "r1"}
	st, err := state.EnterDelivery(env.RepoRoot, "claude", plan, planned)
	if err != nil {
		t.Fatal(err)
	}
	decision := &state.Decision{
		Role:      manifest.RoleImplementer,
		Executor:  manifest.Selection{Model: "m", Effort: "e"},
		Reviewer:  manifest.Selection{Model: "m", Effort: "e"},
		Rationale: "r",
	}
	st.Run.Issues = []state.Issue{
		{PlanID: "a", Title: "A", Phase: state.PhaseDispatched, Number: 1, Branch: "b1", Worktree: "wt1", Decision: decision},
		{PlanID: "b", Title: "B", Phase: state.PhaseInReview, Number: 2, Branch: "b2", Worktree: "wt2", Decision: decision, PRNumber: 2},
	}
	if err := state.Save(env.RepoRoot, st); err != nil {
		t.Fatal(err)
	}

	if code := Run([]string{"hook", "claude", "session-start"}, env); code != ExitOK {
		t.Fatalf("claude exit = %d, want %d (stderr %q)", code, ExitOK, stderr.String())
	}

	var stdoutCodex bytes.Buffer
	envCodex := env
	envCodex.Stdout = &stdoutCodex
	if code := Run([]string{"hook", "codex", "session-start"}, envCodex); code != ExitOK {
		t.Fatalf("codex exit = %d, want %d", code, ExitOK)
	}

	claudeShared := strings.TrimSuffix(stdoutClaude.String(), sessionStartClosing["claude"]+"\n")
	codexShared := strings.TrimSuffix(stdoutCodex.String(), sessionStartClosing["codex"]+"\n")
	if claudeShared != codexShared {
		t.Errorf("delivery-state shared lines differ:\nclaude: %q\ncodex:  %q", claudeShared, codexShared)
	}
}
