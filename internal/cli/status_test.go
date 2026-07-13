package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kninetimmy/orch/internal/lockfile"
	"github.com/kninetimmy/orch/internal/state"
)

func TestStatusNotInitialized(t *testing.T) {
	env, stdout, stderr := testEnv(t)
	if code := Run([]string{"status"}, env); code != ExitError {
		t.Errorf("exit = %d, want %d", code, ExitError)
	}
	if !strings.Contains(stderr.String(), "not initialized") {
		t.Errorf("stderr = %q", stderr.String())
	}
	// The version line prints before any repository check, so even an
	// uninitialized repository identifies the binary on PATH.
	if !strings.Contains(stdout.String(), "orch:   dev") {
		t.Errorf("stdout missing version line:\n%s", stdout.String())
	}
}

func TestStatusValidConfig(t *testing.T) {
	env, stdout, _ := testEnv(t)
	writeConfig(t, env.RepoRoot, validTOML)
	if code := Run([]string{"status"}, env); code != ExitOK {
		t.Fatalf("exit = %d, want %d", code, ExitOK)
	}
	out := stdout.String()
	for _, want := range []string{"orch:   dev", "mode:   assist", `revision "r1"`, "hosts:  claude"} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "local:") {
		t.Errorf("output has a local: line without a config.local.toml:\n%s", out)
	}
}

func TestStatusShowsLocalOverrides(t *testing.T) {
	env, stdout, _ := testEnv(t)
	writeConfig(t, env.RepoRoot, validTOML)
	local := "[concurrency]\nmax_subagents = 5\n"
	if err := os.WriteFile(filepath.Join(env.RepoRoot, ".orchestrator", "config.local.toml"), []byte(local), 0o644); err != nil {
		t.Fatal(err)
	}
	if code := Run([]string{"status"}, env); code != ExitOK {
		t.Fatalf("exit = %d, want %d", code, ExitOK)
	}
	out := stdout.String()
	if !strings.Contains(out, "local:  1 override(s) from .orchestrator/config.local.toml") {
		t.Errorf("output missing local override line:\n%s", out)
	}
}

func TestStatusDeliveryShowsRunAndLock(t *testing.T) {
	env, stdout, _ := testEnv(t)
	writeConfig(t, env.RepoRoot, validTOML)
	st, err := state.EnterDelivery(env.RepoRoot, "claude", testPlanRef(), testIssues())
	if err != nil {
		t.Fatal(err)
	}
	if code := Run([]string{"status"}, env); code != ExitOK {
		t.Fatalf("exit = %d, want %d", code, ExitOK)
	}
	out := stdout.String()
	for _, want := range []string{"mode:   delivery", st.Run.ID, "lock:   held by claude"} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "warning:") {
		t.Errorf("unexpected warning on consistent state:\n%s", out)
	}
}

func TestStatusCorruptState(t *testing.T) {
	env, _, stderr := testEnv(t)
	writeConfig(t, env.RepoRoot, validTOML)
	if err := os.WriteFile(filepath.Join(env.RepoRoot, ".orchestrator", "state.json"), []byte("{broken"), 0o644); err != nil {
		t.Fatal(err)
	}
	if code := Run([]string{"status"}, env); code != ExitError {
		t.Errorf("exit = %d, want %d", code, ExitError)
	}
	if !strings.Contains(stderr.String(), "state.json") {
		t.Errorf("stderr does not name the state file: %q", stderr.String())
	}
}

func TestStatusOrphanedLockWarnsButSucceeds(t *testing.T) {
	env, stdout, _ := testEnv(t)
	writeConfig(t, env.RepoRoot, validTOML)
	err := lockfile.Acquire(env.RepoRoot, lockfile.Owner{RunID: "run-orphan", Host: "codex", Hostname: "h", PID: 1, AcquiredAt: time.Now().UTC()})
	if err != nil {
		t.Fatal(err)
	}
	if code := Run([]string{"status"}, env); code != ExitOK {
		t.Fatalf("exit = %d, want %d (status inspects; doctor enforces)", code, ExitOK)
	}
	if !strings.Contains(stdout.String(), "warning:") {
		t.Errorf("output missing consistency warning:\n%s", stdout.String())
	}
}

func TestStatusInvalidConfig(t *testing.T) {
	env, _, stderr := testEnv(t)
	writeConfig(t, env.RepoRoot, validTOML+"\nbogus_key = true\n")
	if code := Run([]string{"status"}, env); code != ExitError {
		t.Errorf("exit = %d, want %d", code, ExitError)
	}
	if !strings.Contains(stderr.String(), "bogus_key") {
		t.Errorf("stderr does not name the unknown key: %q", stderr.String())
	}
}
