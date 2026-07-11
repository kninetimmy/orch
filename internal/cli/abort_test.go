package cli

import (
	"strings"
	"testing"

	"github.com/kninetimmy/orch/internal/lockfile"
	"github.com/kninetimmy/orch/internal/state"
)

func TestAbortNotInitialized(t *testing.T) {
	env, _, stderr := testEnv(t)
	if code := Run([]string{"abort"}, env); code != ExitError {
		t.Errorf("exit = %d, want %d", code, ExitError)
	}
	if !strings.Contains(stderr.String(), "not initialized") {
		t.Errorf("stderr = %q", stderr.String())
	}
}

func TestAbortDeliveryRun(t *testing.T) {
	env, stdout, _ := testEnv(t)
	writeConfig(t, env.RepoRoot, validTOML)
	st, err := state.EnterDelivery(env.RepoRoot, "claude", testPlanRef(), testIssues())
	if err != nil {
		t.Fatal(err)
	}
	if code := Run([]string{"abort"}, env); code != ExitOK {
		t.Fatalf("exit = %d, want %d", code, ExitOK)
	}
	out := stdout.String()
	for _, want := range []string{"aborted delivery run " + st.Run.ID, "released delivery lock", "preserved"} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q:\n%s", want, out)
		}
	}
	if o, err := lockfile.Inspect(env.RepoRoot); o != nil || err != nil {
		t.Errorf("lock still present after abort: %+v, %v", o, err)
	}
}

func TestAbortIdempotent(t *testing.T) {
	env, stdout, _ := testEnv(t)
	writeConfig(t, env.RepoRoot, validTOML)
	if _, err := state.EnterDelivery(env.RepoRoot, "claude", testPlanRef(), testIssues()); err != nil {
		t.Fatal(err)
	}
	if code := Run([]string{"abort"}, env); code != ExitOK {
		t.Fatalf("first abort exit = %d", code)
	}
	stdout.Reset()
	if code := Run([]string{"abort"}, env); code != ExitOK {
		t.Fatalf("second abort exit = %d", code)
	}
	if !strings.Contains(stdout.String(), "nothing to abort") {
		t.Errorf("second abort output = %q", stdout.String())
	}
}

func TestAbortClearsOrphanedLock(t *testing.T) {
	env, stdout, _ := testEnv(t)
	writeConfig(t, env.RepoRoot, validTOML)
	err := lockfile.Acquire(env.RepoRoot, lockfile.Owner{RunID: "run-orphan", Host: "codex", Hostname: "elsewhere", PID: 1})
	if err != nil {
		t.Fatal(err)
	}
	if code := Run([]string{"abort"}, env); code != ExitOK {
		t.Fatalf("exit = %d, want %d", code, ExitOK)
	}
	out := stdout.String()
	// The released owner is printed so a takeover is visible.
	for _, want := range []string{"released delivery lock", "codex", "elsewhere"} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q:\n%s", want, out)
		}
	}
}
