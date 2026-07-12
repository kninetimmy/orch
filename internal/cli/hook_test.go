package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kninetimmy/orch/internal/manifest"
	"github.com/kninetimmy/orch/internal/paths"
	"github.com/kninetimmy/orch/internal/state"
)

func TestHookSessionStartNoOrchAncestorSilent(t *testing.T) {
	env, stdout, stderr := testEnv(t)
	if code := Run([]string{"hook", "claude", "session-start"}, env); code != ExitOK {
		t.Fatalf("exit = %d, want %d (stderr %q)", code, ExitOK, stderr.String())
	}
	if stdout.String() != "" {
		t.Errorf("stdout = %q, want empty", stdout.String())
	}
}

func TestHookSessionStartAssistRepo(t *testing.T) {
	env, stdout, stderr := testEnv(t)
	writeConfig(t, env.RepoRoot, validTOML)
	if code := Run([]string{"hook", "claude", "session-start"}, env); code != ExitOK {
		t.Fatalf("exit = %d, want %d (stderr %q)", code, ExitOK, stderr.String())
	}
	out := stdout.String()
	for _, want := range []string{"mode: assist", "off", "orch-architect"} {
		if !strings.Contains(out, want) {
			t.Errorf("stdout missing %q:\n%s", want, out)
		}
	}
}

func TestHookSessionStartNestedCwdWalksUp(t *testing.T) {
	env, stdout, stderr := testEnv(t)
	writeConfig(t, env.RepoRoot, validTOML)
	nested := filepath.Join(env.RepoRoot, "a", "b", "c")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatal(err)
	}
	env.RepoRoot = nested
	if code := Run([]string{"hook", "claude", "session-start"}, env); code != ExitOK {
		t.Fatalf("exit = %d, want %d (stderr %q)", code, ExitOK, stderr.String())
	}
	if !strings.Contains(stdout.String(), "orch-architect") {
		t.Errorf("stdout missing context from the discovered root:\n%s", stdout.String())
	}
}

func TestHookSessionStartCorruptStateSilent(t *testing.T) {
	env, stdout, stderr := testEnv(t)
	writeConfig(t, env.RepoRoot, validTOML)
	if err := os.WriteFile(filepath.Join(env.RepoRoot, filepath.FromSlash(state.Path)), []byte("{ not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	if code := Run([]string{"hook", "claude", "session-start"}, env); code != ExitOK {
		t.Fatalf("exit = %d, want %d (stderr %q)", code, ExitOK, stderr.String())
	}
	if stdout.String() != "" {
		t.Errorf("stdout = %q, want empty", stdout.String())
	}
}

func TestHookSessionStartInvalidConfigSilent(t *testing.T) {
	env, stdout, stderr := testEnv(t)
	writeConfig(t, env.RepoRoot, validTOML+"\nbogus_key = true\n")
	if code := Run([]string{"hook", "claude", "session-start"}, env); code != ExitOK {
		t.Fatalf("exit = %d, want %d (stderr %q)", code, ExitOK, stderr.String())
	}
	if stdout.String() != "" {
		t.Errorf("stdout = %q, want empty", stdout.String())
	}
}

func TestHookSessionStartDeliveryRun(t *testing.T) {
	env, stdout, stderr := testEnv(t)
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
		{PlanID: "b", Title: "B", Phase: state.PhaseDispatched, Number: 2, Branch: "b2", Worktree: "wt2", Decision: decision},
		{PlanID: "c", Title: "C", Phase: state.PhaseInReview, Number: 3, Branch: "b3", Worktree: "wt3", Decision: decision, PRNumber: 3},
		{PlanID: "d", Title: "D", Phase: state.PhasePlanned},
	}
	if err := state.Save(env.RepoRoot, st); err != nil {
		t.Fatal(err)
	}

	if code := Run([]string{"hook", "claude", "session-start"}, env); code != ExitOK {
		t.Fatalf("exit = %d, want %d (stderr %q)", code, ExitOK, stderr.String())
	}
	out := stdout.String()
	if !strings.Contains(out, "mode: delivery") {
		t.Errorf("stdout missing delivery mode:\n%s", out)
	}
	want := "Delivery run " + st.Run.ID + " (host claude): 1 planned, 2 dispatched, 1 in-review."
	if !strings.Contains(out, want) {
		t.Errorf("stdout missing run summary %q:\n%s", want, out)
	}
}

func TestHookBadArgsUsage(t *testing.T) {
	cases := map[string][]string{
		"no args":       {"hook"},
		"unknown host":  {"hook", "codex", "session-start"},
		"unknown verb":  {"hook", "claude", "session-end"},
		"missing verb":  {"hook", "claude"},
		"trailing args": {"hook", "claude", "session-start", "extra"},
	}
	for name, args := range cases {
		t.Run(name, func(t *testing.T) {
			env, _, stderr := testEnv(t)
			if code := Run(args, env); code != ExitUsage {
				t.Fatalf("exit = %d, want %d", code, ExitUsage)
			}
			if !strings.Contains(stderr.String(), "orch hook: usage") {
				t.Errorf("stderr missing hookUsage: %q", stderr.String())
			}
		})
	}
}

// TestHookSessionStartFindsOutermostRoot confirms the walk uses
// FindOutermostRoot (matching guard), not FindRoot: a nested
// .orchestrator ancestor (as a Delivery worktree would carry) must not
// shadow the outermost one.
func TestHookSessionStartFindsOutermostRoot(t *testing.T) {
	env, stdout, stderr := testEnv(t)
	writeConfig(t, env.RepoRoot, validTOML)
	inner := filepath.Join(env.RepoRoot, "nested")
	if err := os.MkdirAll(filepath.Join(inner, paths.OrchestratorDir), 0o755); err != nil {
		t.Fatal(err)
	}
	env.RepoRoot = inner
	if code := Run([]string{"hook", "claude", "session-start"}, env); code != ExitOK {
		t.Fatalf("exit = %d, want %d (stderr %q)", code, ExitOK, stderr.String())
	}
	if !strings.Contains(stdout.String(), "orch-architect") {
		t.Errorf("stdout missing context from the outermost root:\n%s", stdout.String())
	}
}
