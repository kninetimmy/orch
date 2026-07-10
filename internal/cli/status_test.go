package cli

import (
	"strings"
	"testing"
)

func TestStatusNotInitialized(t *testing.T) {
	env, _, stderr := testEnv(t)
	if code := Run([]string{"status"}, env); code != ExitError {
		t.Errorf("exit = %d, want %d", code, ExitError)
	}
	if !strings.Contains(stderr.String(), "not initialized") {
		t.Errorf("stderr = %q", stderr.String())
	}
}

func TestStatusValidConfig(t *testing.T) {
	env, stdout, _ := testEnv(t)
	writeConfig(t, env.RepoRoot, validTOML)
	if code := Run([]string{"status"}, env); code != ExitOK {
		t.Fatalf("exit = %d, want %d", code, ExitOK)
	}
	out := stdout.String()
	for _, want := range []string{"mode:   assist", `revision "r1"`, "hosts:  claude"} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q:\n%s", want, out)
		}
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
