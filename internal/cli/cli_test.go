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

	"github.com/kninetimmy/orch/internal/agents"
	"github.com/kninetimmy/orch/internal/config"
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

// fakeRunner answers the doctor probes: git and gh with scripted exits,
// plus each host's plugin listing (zero values report healthy).
type fakeRunner struct {
	toplevel            string
	gitExit             int
	gitStderr           string
	authExit            int
	repoExit            int
	repoJSON            string
	beforeRun           func(execx.Cmd)
	claudePluginJSON    string
	claudePluginExit    int
	claudePluginStderr  string
	claudePluginErr     error
	codexPluginJSON     string
	codexPluginExit     int
	codexPluginStderr   string
	codexPluginErr      error
	opencodeVersion     string
	opencodePlugins     string
	opencodePluginExit  int
	opencodePluginErr   error
	opencodeCatalog     string
	opencodeCatalogExit int
	opencodeCatalogErr  error
	// checkIgnoreExit scripts `git check-ignore`: 0 ignored, 1 not
	// ignored, anything else an error (the guard ignore probe).
	checkIgnoreExit    int
	checkIgnoreExitFor func(execx.Cmd) int
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
	if f.beforeRun != nil {
		f.beforeRun(c)
	}
	switch c.Name {
	case "git":
		if len(c.Args) > 0 && c.Args[0] == "check-ignore" {
			exit := f.checkIgnoreExit
			if f.checkIgnoreExitFor != nil {
				exit = f.checkIgnoreExitFor(c)
			}
			return execx.Result{ExitCode: exit}, nil
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
	case "claude", "codex":
		if len(c.Args) != 3 || c.Args[0] != "plugin" || c.Args[1] != "list" || c.Args[2] != "--json" {
			return execx.Result{}, fmt.Errorf("fakeRunner: unexpected command %s %v", c.Name, c.Args)
		}
		var stdout, stderr string
		var exit int
		var err error
		var spec adapterSpec
		if c.Name == "claude" {
			stdout, stderr, exit, err = f.claudePluginJSON, f.claudePluginStderr, f.claudePluginExit, f.claudePluginErr
			spec = claudeAdapter
		} else {
			stdout, stderr, exit, err = f.codexPluginJSON, f.codexPluginStderr, f.codexPluginExit, f.codexPluginErr
			spec = codexAdapter
		}
		if err != nil {
			return execx.Result{}, err
		}
		if stdout == "" {
			version, versionErr := adapterVersion(spec.manifestJSON)
			if versionErr != nil {
				return execx.Result{}, versionErr
			}
			if c.Name == "claude" {
				stdout = fmt.Sprintf(`[{"id":"orch-claude@orch","version":%q,"enabled":true}]`, version)
			} else {
				stdout = fmt.Sprintf(`{"installed":[{"pluginId":"orch@orch","version":%q,"installed":true,"enabled":true}]}`, version)
			}
		}
		return execx.Result{Stdout: stdout, Stderr: stderr, ExitCode: exit}, nil
	case "opencode2":
		if len(c.Args) == 1 && c.Args[0] == "--version" {
			version := f.opencodeVersion
			if version == "" {
				version = pinnedOpenCodeVersion
			}
			return execx.Result{Stdout: version + "\n"}, nil
		}
		if len(c.Args) == 3 && c.Args[0] == "api" && c.Args[1] == "get" && c.Args[2] == "/api/plugin" {
			if f.opencodePluginErr != nil {
				return execx.Result{}, f.opencodePluginErr
			}
			plugins := f.opencodePlugins
			if plugins == "" {
				plugins = `{"data":[{"id":"orch.delivery"}]}`
			}
			return execx.Result{Stdout: plugins, ExitCode: f.opencodePluginExit}, nil
		}
		if len(c.Args) == 3 && c.Args[0] == "api" && c.Args[1] == "get" && strings.HasPrefix(c.Args[2], "/api/model?") {
			if f.opencodeCatalogErr != nil {
				return execx.Result{}, f.opencodeCatalogErr
			}
			catalog := f.opencodeCatalog
			if catalog == "" {
				catalog = fmt.Sprintf(`{"location":{"directory":%q},"data":[
					{"id":"gpt-5.6-sol","providerID":"openai","enabled":true,"capabilities":{"tools":true,"input":["text"],"output":["text"]},"variants":[{"id":"high"},{"id":"xhigh"},{"id":"max"}]},
					{"id":"gpt-5.6-luna","providerID":"openai","enabled":true,"capabilities":{"tools":true,"input":["text"],"output":["text"]},"variants":[{"id":"max"}]},
					{"id":"gpt-5.6-terra","providerID":"openai","enabled":true,"capabilities":{"tools":true,"input":["text"],"output":["text"]},"variants":[{"id":"max"}]}
				]}`, c.Dir)
			}
			return execx.Result{Stdout: catalog, ExitCode: f.opencodeCatalogExit}, nil
		}
		return execx.Result{}, fmt.Errorf("fakeRunner: unexpected command %s %v", c.Name, c.Args)
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
	writeConfigOnly(t, root, content)
	cfg, err := config.Load(root)
	if err != nil {
		return
	}
	var files []agents.File
	for _, host := range cfg.EnabledHosts() {
		h := cfg.Host(host)
		rendered, err := agents.Render(host, h)
		if err != nil {
			t.Fatal(err)
		}
		files = append(files, rendered...)
	}
	if err := agents.Write(root, files); err != nil {
		t.Fatal(err)
	}
}

func writeConfigOnly(t *testing.T, root, content string) {
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
