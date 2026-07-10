package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDoctorAllChecksPass(t *testing.T) {
	env, stdout, _ := testEnv(t)
	writeConfig(t, env.RepoRoot, validTOML)
	if code := Run([]string{"doctor"}, env); code != ExitOK {
		t.Fatalf("exit = %d, want %d\n%s", code, ExitOK, stdout.String())
	}
	out := stdout.String()
	for _, want := range []string{"ok    git on PATH", "ok    gh on PATH", "ok    configuration"} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q:\n%s", want, out)
		}
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

func TestDoctorNotesLocalOverride(t *testing.T) {
	env, stdout, _ := testEnv(t)
	writeConfig(t, env.RepoRoot, validTOML)
	if err := os.WriteFile(filepath.Join(env.RepoRoot, ".orchestrator", "config.local.toml"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if code := Run([]string{"doctor"}, env); code != ExitOK {
		t.Fatalf("exit = %d, want %d", code, ExitOK)
	}
	if !strings.Contains(stdout.String(), "overrides are not yet applied") {
		t.Errorf("stdout missing local-override note:\n%s", stdout.String())
	}
}
