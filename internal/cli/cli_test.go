package cli

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kninetimmy/orch/internal/execx"
)

// validTOML is a minimal valid configuration (one host, defaults).
const validTOML = `
schema_version  = 1
config_revision = "r1"

[memhub]
mode = "off"

[hosts.claude.roles.architect]
model  = "claude-opus-4-8"
effort = "xhigh"

[hosts.claude.roles.scout]
model  = "claude-sonnet-5"
effort = "low"

[hosts.claude.roles.implementer]
model  = "claude-sonnet-5"
effort = "xhigh"

[hosts.claude.roles.specialist]
model  = "claude-opus-4-8"
effort = "high"

[hosts.claude.roles.reviewer]
model  = "claude-opus-4-8"
effort = "high"

[hosts.claude.roles.review_downgrade]
model  = "claude-sonnet-5"
effort = "high"
`

// testEnv returns an Env writing to fresh buffers, rooted in an empty
// temp dir, with every PATH lookup succeeding and a Runner that
// reports the repo root as a healthy git top level.
func testEnv(t *testing.T) (Env, *bytes.Buffer, *bytes.Buffer) {
	t.Helper()
	var stdout, stderr bytes.Buffer
	root := t.TempDir()
	env := Env{
		RepoRoot: root,
		Stdout:   &stdout,
		Stderr:   &stderr,
		LookPath: func(name string) (string, error) { return "/fake/" + name, nil },
		Runner:   fakeGitRunner{toplevel: root},
	}
	return env, &stdout, &stderr
}

// fakeGitRunner answers `git rev-parse --show-toplevel` (the doctor
// repository probe) with a fixed top level; exit carries a scripted
// failure.
type fakeGitRunner struct {
	toplevel string
	exit     int
	stderr   string
}

func (f fakeGitRunner) Run(context.Context, execx.Cmd) (execx.Result, error) {
	if f.exit != 0 {
		return execx.Result{Stderr: f.stderr, ExitCode: f.exit}, nil
	}
	return execx.Result{Stdout: f.toplevel + "\n"}, nil
}

func writeConfig(t *testing.T, root, content string) {
	t.Helper()
	if err := os.Mkdir(filepath.Join(root, ".orchestrator"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, ".orchestrator", "config.toml"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestRunNoArgs(t *testing.T) {
	env, _, stderr := testEnv(t)
	if code := Run(nil, env); code != ExitUsage {
		t.Errorf("exit = %d, want %d", code, ExitUsage)
	}
	if !strings.Contains(stderr.String(), "usage: orch") {
		t.Errorf("stderr missing usage text: %q", stderr.String())
	}
}

func TestRunHelpListsAllCommands(t *testing.T) {
	env, stdout, _ := testEnv(t)
	if code := Run([]string{"help"}, env); code != ExitOK {
		t.Errorf("exit = %d, want %d", code, ExitOK)
	}
	for _, name := range []string{"init", "status", "doctor", "configure", "configure-local", "resume", "abort", "metrics"} {
		if !strings.Contains(stdout.String(), name) {
			t.Errorf("help output missing command %q", name)
		}
	}
}

func TestRunUnknownCommand(t *testing.T) {
	env, _, stderr := testEnv(t)
	if code := Run([]string{"deploy"}, env); code != ExitUsage {
		t.Errorf("exit = %d, want %d", code, ExitUsage)
	}
	if !strings.Contains(stderr.String(), `unknown command "deploy"`) {
		t.Errorf("stderr = %q", stderr.String())
	}
}

func TestRunUnexpectedArgument(t *testing.T) {
	env, _, stderr := testEnv(t)
	if code := Run([]string{"status", "--verbose"}, env); code != ExitUsage {
		t.Errorf("exit = %d, want %d", code, ExitUsage)
	}
	if !strings.Contains(stderr.String(), "unexpected argument") {
		t.Errorf("stderr = %q", stderr.String())
	}
}

func TestNotImplementedCommandsFailClosed(t *testing.T) {
	for _, name := range []string{"init", "configure", "configure-local", "resume", "metrics"} {
		t.Run(name, func(t *testing.T) {
			env, _, stderr := testEnv(t)
			if code := Run([]string{name}, env); code != ExitError {
				t.Errorf("exit = %d, want %d", code, ExitError)
			}
			if !strings.Contains(stderr.String(), "not implemented") {
				t.Errorf("stderr = %q", stderr.String())
			}
		})
	}
}

func TestRunDefaultsLookPath(t *testing.T) {
	// Run must not panic when LookPath is nil (main.go passes none).
	var stdout, stderr bytes.Buffer
	env := Env{RepoRoot: t.TempDir(), Stdout: &stdout, Stderr: &stderr}
	if code := Run([]string{"help"}, env); code != ExitOK {
		t.Errorf("exit = %d, want %d", code, ExitOK)
	}
}

var errNotFound = errors.New("executable file not found")
