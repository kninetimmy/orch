package cli

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kninetimmy/orch/internal/execx"
	"github.com/kninetimmy/orch/internal/state"
)

// testPlanRef and testIssues supply the minimal valid
// state.EnterDelivery arguments the delivery-mode cli tests need;
// their content is irrelevant to what those tests assert.
func testPlanRef() state.PlanRef {
	return state.PlanRef{Title: "t", Digest: "sha256:test", ConfigRevision: "r1"}
}

func testIssues() []state.Issue {
	return []state.Issue{{PlanID: "iss-a", Title: "A", Phase: state.PhasePlanned}}
}

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
		Runner:   fakeRunner{toplevel: root},
	}
	return env, &stdout, &stderr
}

// fakeRunner answers the doctor probes: `git rev-parse
// --show-toplevel` with a fixed top level, `gh auth status` and
// `gh repo view` with scripted exits (zero values report healthy).
type fakeRunner struct {
	toplevel  string
	gitExit   int
	gitStderr string
	authExit  int
	repoExit  int
	repoJSON  string
	// checkIgnoreExit scripts `git check-ignore`: 0 ignored, 1 not
	// ignored, anything else an error (the guard ignore probe).
	checkIgnoreExit int
	// memhubStatusExit/memhubRecallExit script the memhub doctor check
	// (zero values report healthy; recall answers with valid empty-
	// results JSON by default).
	memhubStatusExit   int
	memhubStatusStderr string
	memhubRecallExit   int
	memhubRecallStderr string
	// releaseTag, releaseExit, and releaseStderr script the release
	// check's `gh api .../releases/latest` call (zero releaseExit with
	// an empty releaseTag reports "v0.0.0-test", a harmless default
	// only reachable by tests that stamp Version).
	releaseTag    string
	releaseExit   int
	releaseStderr string
}

func (f fakeRunner) Run(_ context.Context, c execx.Cmd) (execx.Result, error) {
	switch c.Name {
	case "git":
		if len(c.Args) > 0 && c.Args[0] == "check-ignore" {
			return execx.Result{ExitCode: f.checkIgnoreExit}, nil
		}
		if f.gitExit != 0 {
			return execx.Result{Stderr: f.gitStderr, ExitCode: f.gitExit}, nil
		}
		return execx.Result{Stdout: f.toplevel + "\n"}, nil
	case "gh":
		switch c.Args[0] {
		case "auth":
			return execx.Result{ExitCode: f.authExit}, nil
		case "repo":
			if f.repoExit != 0 {
				return execx.Result{Stderr: "none of the git remotes point to a known GitHub host", ExitCode: f.repoExit}, nil
			}
			j := f.repoJSON
			if j == "" {
				j = `{"nameWithOwner":"o/r","defaultBranchRef":{"name":"main"},"url":"https://github.com/o/r"}`
			}
			return execx.Result{Stdout: j}, nil
		case "api":
			if f.releaseExit != 0 {
				return execx.Result{Stderr: f.releaseStderr, ExitCode: f.releaseExit}, nil
			}
			tag := f.releaseTag
			if tag == "" {
				tag = "v0.0.0-test"
			}
			return execx.Result{Stdout: tag + "\n"}, nil
		}
	case "memhub":
		switch c.Args[0] {
		case "status":
			return execx.Result{Stderr: f.memhubStatusStderr, ExitCode: f.memhubStatusExit}, nil
		case "recall":
			if f.memhubRecallExit != 0 {
				return execx.Result{Stderr: f.memhubRecallStderr, ExitCode: f.memhubRecallExit}, nil
			}
			return execx.Result{Stdout: `{"results":[]}`}, nil
		}
	}
	return execx.Result{}, fmt.Errorf("fakeRunner: unexpected command %s %v", c.Name, c.Args)
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
	for _, name := range []string{"init", "status", "doctor", "configure", "configure-local", "resume", "abort", "metrics", "run"} {
		if !strings.Contains(stdout.String(), name) {
			t.Errorf("help output missing command %q", name)
		}
	}
	if !strings.Contains(stdout.String(), "plumbing") {
		t.Error("help output does not label `run` as adapter plumbing (F2)")
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

func TestRunDefaultsLookPath(t *testing.T) {
	// Run must not panic when LookPath is nil (main.go passes none).
	var stdout, stderr bytes.Buffer
	env := Env{RepoRoot: t.TempDir(), Stdout: &stdout, Stderr: &stderr}
	if code := Run([]string{"help"}, env); code != ExitOK {
		t.Errorf("exit = %d, want %d", code, ExitOK)
	}
}

var errNotFound = errors.New("executable file not found")
