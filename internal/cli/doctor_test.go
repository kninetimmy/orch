package cli

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kninetimmy/orch/internal/lockfile"
	"github.com/kninetimmy/orch/internal/state"
)

func TestDoctorAllChecksPass(t *testing.T) {
	env, stdout, _ := testEnv(t)
	writeConfig(t, env.RepoRoot, validTOML)
	if code := Run([]string{"doctor"}, env); code != ExitOK {
		t.Fatalf("exit = %d, want %d\n%s", code, ExitOK, stdout.String())
	}
	out := stdout.String()
	for _, want := range []string{"note  orch version: dev", "ok    git on PATH", "ok    git repository", "ok    gh on PATH", "ok    gh authentication", "ok    gh repository: o/r", "ok    configuration"} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q:\n%s", want, out)
		}
	}
}

func TestDoctorUnauthenticatedGh(t *testing.T) {
	env, stdout, _ := testEnv(t)
	writeConfig(t, env.RepoRoot, validTOML)
	env.Runner = fakeRunner{toplevel: env.RepoRoot, authExit: 1}
	if code := Run([]string{"doctor"}, env); code != ExitError {
		t.Errorf("exit = %d, want %d", code, ExitError)
	}
	out := stdout.String()
	if !strings.Contains(out, "FAIL  gh authentication") {
		t.Errorf("stdout missing auth failure:\n%s", out)
	}
	if strings.Contains(out, "gh repository") {
		t.Errorf("repository probe ran without authentication:\n%s", out)
	}
}

func TestDoctorNoGitHubRepoIsNote(t *testing.T) {
	env, stdout, _ := testEnv(t)
	writeConfig(t, env.RepoRoot, validTOML)
	env.Runner = fakeRunner{toplevel: env.RepoRoot, repoExit: 1}
	if code := Run([]string{"doctor"}, env); code != ExitOK {
		t.Fatalf("exit = %d, want %d (Assist works without a remote)\n%s", code, ExitOK, stdout.String())
	}
	if !strings.Contains(stdout.String(), "note  no GitHub repository resolved") {
		t.Errorf("stdout missing no-repo note:\n%s", stdout.String())
	}
}

func TestDoctorNotAGitRepo(t *testing.T) {
	env, stdout, _ := testEnv(t)
	writeConfig(t, env.RepoRoot, validTOML)
	env.Runner = fakeRunner{gitExit: 128, gitStderr: "fatal: not a git repository"}
	if code := Run([]string{"doctor"}, env); code != ExitError {
		t.Errorf("exit = %d, want %d", code, ExitError)
	}
	if !strings.Contains(stdout.String(), "FAIL  git repository") {
		t.Errorf("stdout missing repository failure:\n%s", stdout.String())
	}
}

func TestDoctorSkipsRepoCheckWithoutGit(t *testing.T) {
	env, stdout, _ := testEnv(t)
	writeConfig(t, env.RepoRoot, validTOML)
	env.LookPath = func(name string) (string, error) {
		if name == "git" {
			return "", errNotFound
		}
		return "/fake/" + name, nil
	}
	if code := Run([]string{"doctor"}, env); code != ExitError {
		t.Errorf("exit = %d, want %d", code, ExitError)
	}
	out := stdout.String()
	if !strings.Contains(out, "FAIL  git on PATH") {
		t.Errorf("stdout missing git failure:\n%s", out)
	}
	if strings.Contains(out, "git repository") {
		t.Errorf("repository check ran without git on PATH:\n%s", out)
	}
}

func TestDoctorMissingTool(t *testing.T) {
	env, stdout, stderr := testEnv(t)
	writeConfig(t, env.RepoRoot, validTOML)
	env.LookPath = func(name string) (string, error) {
		if name == "gh" {
			return "", errNotFound
		}
		return "/fake/" + name, nil
	}
	if code := Run([]string{"doctor"}, env); code != ExitError {
		t.Errorf("exit = %d, want %d", code, ExitError)
	}
	if !strings.Contains(stdout.String(), "FAIL  gh on PATH") {
		t.Errorf("stdout missing gh failure:\n%s", stdout.String())
	}
	if strings.Contains(stdout.String(), "gh authentication") {
		t.Errorf("authentication check ran without gh on PATH:\n%s", stdout.String())
	}
	if !strings.Contains(stdout.String(), "ok    git on PATH") {
		t.Errorf("doctor stopped at first failure instead of running all checks:\n%s", stdout.String())
	}
	if !strings.Contains(stderr.String(), "one or more checks failed") {
		t.Errorf("stderr = %q", stderr.String())
	}
}

func TestDoctorMissingConfig(t *testing.T) {
	env, stdout, _ := testEnv(t)
	if code := Run([]string{"doctor"}, env); code != ExitError {
		t.Errorf("exit = %d, want %d", code, ExitError)
	}
	if !strings.Contains(stdout.String(), "FAIL  configuration") {
		t.Errorf("stdout missing configuration failure:\n%s", stdout.String())
	}
}

func TestDoctorMemhubModeOffNeverProbes(t *testing.T) {
	env, stdout, _ := testEnv(t)
	writeConfig(t, env.RepoRoot, validTOML) // memhub mode = "off"
	if code := Run([]string{"doctor"}, env); code != ExitOK {
		t.Fatalf("exit = %d, want %d\n%s", code, ExitOK, stdout.String())
	}
	if !strings.Contains(stdout.String(), "note  memhub: skipped (mode off)") {
		t.Errorf("stdout missing memhub skip note:\n%s", stdout.String())
	}
}

func TestDoctorMemhubBestEffortHealthy(t *testing.T) {
	env, stdout, _ := testEnv(t)
	writeConfig(t, env.RepoRoot, strings.Replace(validTOML, `mode = "off"`, `mode = "best-effort"`, 1))
	if code := Run([]string{"doctor"}, env); code != ExitOK {
		t.Fatalf("exit = %d, want %d\n%s", code, ExitOK, stdout.String())
	}
	if !strings.Contains(stdout.String(), "ok    memhub") {
		t.Errorf("stdout missing healthy memhub:\n%s", stdout.String())
	}
}

func TestDoctorMemhubBestEffortUnhealthyNonFatal(t *testing.T) {
	env, stdout, _ := testEnv(t)
	writeConfig(t, env.RepoRoot, strings.Replace(validTOML, `mode = "off"`, `mode = "best-effort"`, 1))
	env.Runner = fakeRunner{toplevel: env.RepoRoot, memhubStatusExit: 1, memhubStatusStderr: "down"}
	if code := Run([]string{"doctor"}, env); code != ExitOK {
		t.Fatalf("exit = %d, want %d (best-effort must never fail doctor)\n%s", code, ExitOK, stdout.String())
	}
	if !strings.Contains(stdout.String(), "note  memhub:") {
		t.Errorf("stdout missing memhub note:\n%s", stdout.String())
	}
}

func TestDoctorMemhubRequiredHealthy(t *testing.T) {
	env, stdout, _ := testEnv(t)
	writeConfig(t, env.RepoRoot, strings.Replace(validTOML, `mode = "off"`, `mode = "required"`, 1))
	if code := Run([]string{"doctor"}, env); code != ExitOK {
		t.Fatalf("exit = %d, want %d\n%s", code, ExitOK, stdout.String())
	}
	if !strings.Contains(stdout.String(), "ok    memhub") {
		t.Errorf("stdout missing healthy memhub:\n%s", stdout.String())
	}
}

func TestDoctorMemhubRequiredHealthFailureFailsDoctor(t *testing.T) {
	env, stdout, _ := testEnv(t)
	writeConfig(t, env.RepoRoot, strings.Replace(validTOML, `mode = "off"`, `mode = "required"`, 1))
	env.Runner = fakeRunner{toplevel: env.RepoRoot, memhubStatusExit: 1, memhubStatusStderr: "unreachable"}
	if code := Run([]string{"doctor"}, env); code != ExitError {
		t.Errorf("exit = %d, want %d", code, ExitError)
	}
	if !strings.Contains(stdout.String(), "FAIL  memhub") {
		t.Errorf("stdout missing memhub failure:\n%s", stdout.String())
	}
}

func TestDoctorMemhubRequiredRecallFailureFailsDoctor(t *testing.T) {
	env, stdout, _ := testEnv(t)
	writeConfig(t, env.RepoRoot, strings.Replace(validTOML, `mode = "off"`, `mode = "required"`, 1))
	env.Runner = fakeRunner{toplevel: env.RepoRoot, memhubRecallExit: 1, memhubRecallStderr: "wedged"}
	if code := Run([]string{"doctor"}, env); code != ExitError {
		t.Errorf("exit = %d, want %d", code, ExitError)
	}
	if !strings.Contains(stdout.String(), "FAIL  memhub") {
		t.Errorf("stdout missing memhub failure:\n%s", stdout.String())
	}
}

func TestDoctorConsistentDeliveryPasses(t *testing.T) {
	env, stdout, _ := testEnv(t)
	writeConfig(t, env.RepoRoot, validTOML)
	if _, err := state.EnterDelivery(env.RepoRoot, "claude", testPlanRef(), testIssues()); err != nil {
		t.Fatal(err)
	}
	if code := Run([]string{"doctor"}, env); code != ExitOK {
		t.Fatalf("exit = %d, want %d\n%s", code, ExitOK, stdout.String())
	}
	out := stdout.String()
	for _, want := range []string{"ok    state file", "ok    delivery lock", "ok    state/lock consistency"} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q:\n%s", want, out)
		}
	}
}

func TestDoctorOrphanedLockFails(t *testing.T) {
	env, stdout, _ := testEnv(t)
	writeConfig(t, env.RepoRoot, validTOML)
	err := lockfile.Acquire(env.RepoRoot, lockfile.Owner{RunID: "run-orphan", Host: "codex", Hostname: "h", PID: 1, AcquiredAt: time.Now().UTC()})
	if err != nil {
		t.Fatal(err)
	}
	if code := Run([]string{"doctor"}, env); code != ExitError {
		t.Errorf("exit = %d, want %d", code, ExitError)
	}
	out := stdout.String()
	if !strings.Contains(out, "FAIL  state/lock consistency") {
		t.Errorf("output missing consistency failure:\n%s", out)
	}
	if !strings.Contains(out, "orch abort") {
		t.Errorf("output missing abort remediation:\n%s", out)
	}
}

func TestDoctorCorruptLockFails(t *testing.T) {
	env, stdout, _ := testEnv(t)
	writeConfig(t, env.RepoRoot, validTOML)
	if err := os.WriteFile(filepath.Join(env.RepoRoot, ".orchestrator", "delivery.lock"), []byte("{broken"), 0o644); err != nil {
		t.Fatal(err)
	}
	if code := Run([]string{"doctor"}, env); code != ExitError {
		t.Errorf("exit = %d, want %d", code, ExitError)
	}
	if !strings.Contains(stdout.String(), "FAIL  delivery lock") {
		t.Errorf("output missing lock failure:\n%s", stdout.String())
	}
}

func TestDoctorNotesDeadAcquirer(t *testing.T) {
	env, stdout, _ := testEnv(t)
	writeConfig(t, env.RepoRoot, validTOML)
	hostname, err := os.Hostname()
	if err != nil {
		t.Fatal(err)
	}
	st, err := state.EnterDelivery(env.RepoRoot, "claude", testPlanRef(), testIssues())
	if err != nil {
		t.Fatal(err)
	}
	// Rewrite the lock with an exited process's PID and this hostname.
	if err := lockfile.Release(env.RepoRoot); err != nil {
		t.Fatal(err)
	}
	err = lockfile.Acquire(env.RepoRoot, lockfile.Owner{
		RunID: st.Run.ID, Host: "claude", Hostname: hostname,
		PID: exitedPID(t), AcquiredAt: time.Now().UTC(),
	})
	if err != nil {
		t.Fatal(err)
	}
	if code := Run([]string{"doctor"}, env); code != ExitOK {
		t.Fatalf("exit = %d, want %d (a dead acquirer is normal between commands)\n%s", code, ExitOK, stdout.String())
	}
	if !strings.Contains(stdout.String(), "no longer running") {
		t.Errorf("output missing dead-acquirer note:\n%s", stdout.String())
	}
}

// exitedPID runs this test binary with no matching tests so it exits
// immediately, returning its (now dead) PID.
func exitedPID(t *testing.T) int {
	t.Helper()
	cmd := exec.Command(os.Args[0], "-test.run=TestNoSuchTestExists")
	if err := cmd.Start(); err != nil {
		t.Fatalf("cannot start helper process: %v", err)
	}
	pid := cmd.Process.Pid
	if err := cmd.Wait(); err != nil {
		t.Fatalf("helper process: %v", err)
	}
	return pid
}

func TestDoctorNotesLocalOverride(t *testing.T) {
	env, stdout, _ := testEnv(t)
	writeConfig(t, env.RepoRoot, validTOML)
	if err := os.WriteFile(filepath.Join(env.RepoRoot, ".orchestrator", "config.local.toml"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if code := Run([]string{"doctor"}, env); code != ExitOK {
		t.Fatalf("exit = %d, want %d", code, ExitOK)
	}
	if !strings.Contains(stdout.String(), "note  .orchestrator/config.local.toml present; no overrides set") {
		t.Errorf("stdout missing local-override note:\n%s", stdout.String())
	}
}

func TestDoctorNotesAppliedLocalOverride(t *testing.T) {
	env, stdout, _ := testEnv(t)
	writeConfig(t, env.RepoRoot, validTOML)
	local := "[concurrency]\nmax_subagents = 5\n"
	if err := os.WriteFile(filepath.Join(env.RepoRoot, ".orchestrator", "config.local.toml"), []byte(local), 0o644); err != nil {
		t.Fatal(err)
	}
	if code := Run([]string{"doctor"}, env); code != ExitOK {
		t.Fatalf("exit = %d, want %d\n%s", code, ExitOK, stdout.String())
	}
	if !strings.Contains(stdout.String(), "note  .orchestrator/config.local.toml applied; overrides: concurrency.max_subagents") {
		t.Errorf("stdout missing applied-override note:\n%s", stdout.String())
	}
}

func TestDoctorPolicyViolatingLocalOverrideFails(t *testing.T) {
	env, stdout, _ := testEnv(t)
	writeConfig(t, env.RepoRoot, validTOML)
	local := "schema_version = 2\n"
	if err := os.WriteFile(filepath.Join(env.RepoRoot, ".orchestrator", "config.local.toml"), []byte(local), 0o644); err != nil {
		t.Fatal(err)
	}
	if code := Run([]string{"doctor"}, env); code != ExitError {
		t.Errorf("exit = %d, want %d", code, ExitError)
	}
	out := stdout.String()
	if !strings.Contains(out, "FAIL  configuration") {
		t.Errorf("stdout missing configuration failure:\n%s", out)
	}
	if !strings.Contains(out, "schema_version") {
		t.Errorf("stdout missing policy-violation detail:\n%s", out)
	}
}
