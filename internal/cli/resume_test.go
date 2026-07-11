package cli

import (
	"strings"
	"testing"

	"github.com/kninetimmy/orch/internal/state"
)

// TestResumeUnknownFlag proves an unrecognized flag is a usage mistake
// (exit 2) naming the usage line, not an operational failure.
func TestResumeUnknownFlag(t *testing.T) {
	env, _, stderr := testEnv(t)
	if code := Run([]string{"resume", "--bogus"}, env); code != ExitUsage {
		t.Fatalf("exit = %d, want %d", code, ExitUsage)
	}
	if !strings.Contains(stderr.String(), "usage: orch resume") {
		t.Errorf("stderr = %q", stderr.String())
	}
}

// TestResumeJSON proves `--json` emits the schema-versioned report.
func TestResumeJSON(t *testing.T) {
	env, stdout, _ := testEnv(t)
	writeConfig(t, env.RepoRoot, validTOML)
	if _, err := state.EnterDelivery(env.RepoRoot, "claude", testPlanRef(), testIssues()); err != nil {
		t.Fatal(err)
	}
	if code := Run([]string{"resume", "--json"}, env); code != ExitOK {
		t.Fatalf("exit = %d, want %d", code, ExitOK)
	}
	out := stdout.String()
	for _, want := range []string{`"schema_version": 1`, `"mode": "delivery"`} {
		if !strings.Contains(out, want) {
			t.Errorf("json output missing %q:\n%s", want, out)
		}
	}
}

// TestResumeHumanRender proves the default rendering names the run and
// each issue line.
func TestResumeHumanRender(t *testing.T) {
	env, stdout, _ := testEnv(t)
	writeConfig(t, env.RepoRoot, validTOML)
	st, err := state.EnterDelivery(env.RepoRoot, "claude", testPlanRef(), testIssues())
	if err != nil {
		t.Fatal(err)
	}
	if code := Run([]string{"resume"}, env); code != ExitOK {
		t.Fatalf("exit = %d, want %d", code, ExitOK)
	}
	out := stdout.String()
	for _, want := range []string{"run:", st.Run.ID, "iss-a"} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q:\n%s", want, out)
		}
	}
}

// TestResumeImplemented is the regression guard: resume is a real command
// now, never the "not implemented" stub.
func TestResumeImplemented(t *testing.T) {
	env, stdout, stderr := testEnv(t)
	if code := Run([]string{"resume"}, env); code != ExitOK {
		t.Fatalf("exit = %d, want %d (stderr %q)", code, ExitOK, stderr.String())
	}
	if strings.Contains(stdout.String(), "not implemented") || strings.Contains(stderr.String(), "not implemented") {
		t.Errorf("resume still reports not implemented:\nstdout %q\nstderr %q", stdout.String(), stderr.String())
	}
	if !strings.Contains(stdout.String(), "no delivery run to resume") {
		t.Errorf("assist resume output = %q", stdout.String())
	}
}
