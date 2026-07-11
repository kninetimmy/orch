package cli

import (
	"bytes"
	"strings"
	"testing"
)

// TestEightCommandsRejectTrailingArgs proves noArgs still rejects a
// trailing argument for every PRD §22 command except the adapter-
// plumbing `run` verb, which parses its own argv.
func TestEightCommandsRejectTrailingArgs(t *testing.T) {
	for _, name := range []string{"init", "status", "doctor", "configure", "configure-local", "resume", "abort", "metrics"} {
		t.Run(name, func(t *testing.T) {
			env, _, stderr := testEnv(t)
			if code := Run([]string{name, "extra"}, env); code != ExitUsage {
				t.Errorf("exit = %d, want %d", code, ExitUsage)
			}
			if !strings.Contains(stderr.String(), "unexpected argument") {
				t.Errorf("stderr = %q", stderr.String())
			}
		})
	}
}

func TestRunVerbUsageErrors(t *testing.T) {
	cases := [][]string{
		{"run"},
		{"run", "bogus"},
		{"run", "status"},          // missing --json
		{"run", "plan", "extra"},   // too many args
		{"run", "status", "--xml"}, // wrong flag
	}
	for _, args := range cases {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			env, _, stderr := testEnv(t)
			if code := Run(args, env); code != ExitUsage {
				t.Errorf("exit = %d, want %d", code, ExitUsage)
			}
			if !strings.Contains(stderr.String(), "orch run: usage") {
				t.Errorf("stderr = %q", stderr.String())
			}
		})
	}
}

func TestRunPlanEndToEnd(t *testing.T) {
	env, stdout, stderr := testEnv(t)
	writeConfig(t, env.RepoRoot, validTOML)
	env.Stdin = bytes.NewReader([]byte(minimalPlanJSON))

	if code := Run([]string{"run", "plan"}, env); code != ExitOK {
		t.Fatalf("exit = %d, want %d; stderr = %s", code, ExitOK, stderr.String())
	}
	out := stdout.String()
	for _, want := range []string{`"schema_version"`, `"plan_digest"`, `"plan_title"`, `"fix-status-lock"`} {
		if !strings.Contains(out, want) {
			t.Errorf("stdout missing %q:\n%s", want, out)
		}
	}
}

func TestRunPlanMalformedStdinIsExitError(t *testing.T) {
	env, _, stderr := testEnv(t)
	writeConfig(t, env.RepoRoot, validTOML)
	env.Stdin = bytes.NewReader([]byte("{not valid json"))

	if code := Run([]string{"run", "plan"}, env); code != ExitError {
		t.Fatalf("exit = %d, want %d", code, ExitError)
	}
	if stderr.String() == "" {
		t.Error("stderr is empty, want a decode error")
	}
}

func TestRunStatusJSONEndToEnd(t *testing.T) {
	env, stdout, stderr := testEnv(t)
	writeConfig(t, env.RepoRoot, validTOML)

	if code := Run([]string{"run", "status", "--json"}, env); code != ExitOK {
		t.Fatalf("exit = %d, want %d; stderr = %s", code, ExitOK, stderr.String())
	}
	if !strings.Contains(stdout.String(), `"mode": "assist"`) {
		t.Errorf("stdout = %s", stdout.String())
	}
}

// blockingReader stands in for a console stdin that never reaches EOF:
// any Read is a bug that would hang the process.
type blockingReader struct{ t *testing.T }

func (r blockingReader) Read([]byte) (int, error) {
	r.t.Fatal("status read stdin; it must not (a console stdin never reaches EOF)")
	return 0, nil
}

// TestRunStatusNeverReadsStdin pins the fix for the interactive hang:
// `orch run status --json` invoked without piped stdin must not block
// waiting for EOF on the console.
func TestRunStatusNeverReadsStdin(t *testing.T) {
	env, stdout, stderr := testEnv(t)
	writeConfig(t, env.RepoRoot, validTOML)
	env.Stdin = blockingReader{t: t}

	if code := Run([]string{"run", "status", "--json"}, env); code != ExitOK {
		t.Fatalf("exit = %d, want %d; stderr = %s", code, ExitOK, stderr.String())
	}
	if !strings.Contains(stdout.String(), `"mode": "assist"`) {
		t.Errorf("stdout = %s", stdout.String())
	}
}

const minimalPlanJSON = `{
  "schema_version": 1,
  "host": "claude",
  "title": "Fix status lock",
  "issues": [
    {
      "id": "fix-status-lock",
      "title": "Fix the status lock race",
      "objective": "Make status reporting race-free",
      "acceptance_criteria": ["no data race under -race"],
      "type": "bug",
      "facts": {"read_only": false},
      "wave": 1,
      "required_tests": ["go test ./..."],
      "usage_class": "light"
    }
  ]
}`
