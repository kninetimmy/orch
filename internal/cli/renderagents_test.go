package cli

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/BurntSushi/toml"

	"github.com/kninetimmy/orch/adapters/claude"
	"github.com/kninetimmy/orch/adapters/codex"
	"github.com/kninetimmy/orch/internal/agents"
	"github.com/kninetimmy/orch/internal/execx"
)

// validCodexTOML is a minimal valid configuration enabling only the
// codex host, at the PRD §10 default model profile (the same values
// adaptertest.Profile("codex") and adapters/codex/plugin_test.go's
// TestAgentTOMLs pin the shipped files against).
const validCodexTOML = `
schema_version  = 1
config_revision = "r1"

[memhub]
mode = "off"

[hosts.codex.roles.architect]
model  = "gpt-5.6-sol"
effort = "xhigh"

[hosts.codex.roles.scout]
model  = "gpt-5.6-luna"
effort = "max"

[hosts.codex.roles.implementer]
model  = "gpt-5.6-terra"
effort = "max"

[hosts.codex.roles.specialist]
model  = "gpt-5.6-sol"
effort = "max"

[hosts.codex.roles.reviewer]
model  = "gpt-5.6-sol"
effort = "xhigh"

[hosts.codex.roles.review_downgrade]
model  = "gpt-5.6-sol"
effort = "high"
`

var validBothHostsTOML = validTOML + validCodexTOML[strings.Index(validCodexTOML, "[hosts.codex"):]

const validClaudeDefaultTOML = `
schema_version  = 1
config_revision = "r1"

[memhub]
mode = "off"

[hosts.claude.roles.architect]
model  = "claude-opus-5"
effort = "high"

[hosts.claude.roles.scout]
model  = "claude-opus-5"
effort = "low"

[hosts.claude.roles.implementer]
model  = "claude-opus-5"
effort = "medium"

[hosts.claude.roles.specialist]
model  = "claude-opus-5"
effort = "high"

[hosts.claude.roles.reviewer]
model  = "claude-opus-5"
effort = "high"

[hosts.claude.roles.review_downgrade]
model  = "claude-opus-5"
effort = "medium"
`

var validDefaultBothHostsTOML = validClaudeDefaultTOML + validCodexTOML[strings.Index(validCodexTOML, "[hosts.codex"):]

// validCodexOverrideTOML enables only the codex host with model/effort
// values that diverge from the PRD §10 defaults.
const validCodexOverrideTOML = `
schema_version  = 1
config_revision = "r1"

[memhub]
mode = "off"

[hosts.codex.roles.architect]
model  = "gpt-9000-ultra"
effort = "high"

[hosts.codex.roles.scout]
model  = "gpt-9000"
effort = "low"

[hosts.codex.roles.implementer]
model  = "gpt-9000"
effort = "high"

[hosts.codex.roles.specialist]
model  = "gpt-9000-ultra"
effort = "medium"

[hosts.codex.roles.reviewer]
model  = "gpt-9000-ultra"
effort = "medium"

[hosts.codex.roles.review_downgrade]
model  = "gpt-9000"
effort = "high"
`

var renderedAgentFiles = []string{
	"orch-scout", "orch-implementer", "orch-specialist", "orch-reviewer", "orch-reviewer-safe",
}

func TestRenderAgentsNotInitialized(t *testing.T) {
	env, _, stderr := testEnv(t)
	if code := Run([]string{"render-agents"}, env); code != ExitError {
		t.Errorf("exit = %d, want %d", code, ExitError)
	}
	if !strings.Contains(stderr.String(), "not initialized") {
		t.Errorf("stderr = %q", stderr.String())
	}
}

func TestRenderAgentsInvalidConfig(t *testing.T) {
	env, _, stderr := testEnv(t)
	writeConfigOnly(t, env.RepoRoot, "schema_version = 1\n")
	if code := Run([]string{"render-agents"}, env); code != ExitError {
		t.Errorf("exit = %d, want %d", code, ExitError)
	}
	if !strings.Contains(stderr.String(), "invalid configuration") {
		t.Errorf("stderr = %q", stderr.String())
	}
}

func TestRenderAgentsClaudeOnly(t *testing.T) {
	env, stdout, _ := testEnv(t)
	writeConfigOnly(t, env.RepoRoot, validTOML) // claude-only fixture
	if code := Run([]string{"render-agents"}, env); code != ExitOK {
		t.Fatalf("exit = %d, want %d", code, ExitOK)
	}
	entries, err := os.ReadDir(filepath.Join(env.RepoRoot, filepath.FromSlash(agents.ClaudeDir)))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 5 {
		t.Errorf("wrote %d Claude definitions, want 5", len(entries))
	}
	if _, err := os.Stat(filepath.Join(env.RepoRoot, filepath.FromSlash(agents.CodexDir))); !os.IsNotExist(err) {
		t.Errorf("%s exists for a Claude-only config (stat err = %v)", agents.CodexDir, err)
	}
	if !strings.Contains(stdout.String(), "wrote "+agents.ClaudeDir+"/orch-scout.md") {
		t.Errorf("stdout missing Claude write confirmation:\n%s", stdout.String())
	}
}

func TestRenderAgentsAnyUnignoredDestinationRefusesBeforeWriting(t *testing.T) {
	env, _, stderr := testEnv(t)
	writeConfigOnly(t, env.RepoRoot, validBothHostsTOML)
	env.Runner = fakeRunner{
		toplevel: env.RepoRoot,
		checkIgnoreExitFor: func(c execx.Cmd) int {
			query := filepath.ToSlash(c.Args[len(c.Args)-1])
			if strings.HasPrefix(query, agents.CodexDir+"/") {
				return 1
			}
			return 0
		},
	}

	if code := Run([]string{"render-agents"}, env); code != ExitError {
		t.Errorf("exit = %d, want %d", code, ExitError)
	}
	if !strings.Contains(stderr.String(), "orch configure") {
		t.Errorf("stderr = %q, want orch configure remediation", stderr.String())
	}
	if !strings.Contains(stderr.String(), agents.CodexDir+"/") {
		t.Errorf("stderr = %q, want %s/ ignore entry", stderr.String(), agents.CodexDir)
	}
	for _, dir := range []string{agents.ClaudeDir, agents.CodexDir} {
		if _, err := os.Stat(filepath.Join(env.RepoRoot, filepath.FromSlash(dir))); !os.IsNotExist(err) {
			t.Errorf("%s exists after refusal (stat err = %v), want absent", dir, err)
		}
	}
}

func TestRenderAgentsNegatedOutputRefusesBeforeWriting(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skipf("git not on PATH: %v", err)
	}
	env, _, stderr := testEnv(t)
	local := execx.Local{}
	res, err := local.Run(context.Background(), execx.Cmd{
		Name: "git", Args: []string{"init", "-b", "main"}, Dir: env.RepoRoot,
	})
	if err != nil || res.ExitCode != 0 {
		t.Fatalf("git init: result = %+v, err = %v", res, err)
	}
	env.Runner = local
	writeConfigOnly(t, env.RepoRoot, validTOML)
	ignore := ".claude/agents/*\n!.claude/agents/orch-scout.md\n"
	if err := os.WriteFile(filepath.Join(env.RepoRoot, ".gitignore"), []byte(ignore), 0o644); err != nil {
		t.Fatal(err)
	}

	if code := Run([]string{"render-agents"}, env); code != ExitError {
		t.Errorf("exit = %d, want %d", code, ExitError)
	}
	if !strings.Contains(stderr.String(), agents.ClaudeDir+"/orch-scout.md") {
		t.Errorf("stderr = %q, want negated output path", stderr.String())
	}
	if _, err := os.Stat(filepath.Join(env.RepoRoot, filepath.FromSlash(agents.ClaudeDir))); !os.IsNotExist(err) {
		t.Errorf("%s exists after refusal (stat err = %v), want absent", agents.ClaudeDir, err)
	}
}

func TestRenderAgentsUnexpectedArgument(t *testing.T) {
	env, _, stderr := testEnv(t)
	writeConfigOnly(t, env.RepoRoot, validCodexTOML)
	if code := Run([]string{"render-agents", "extra"}, env); code != ExitUsage {
		t.Errorf("exit = %d, want %d", code, ExitUsage)
	}
	if !strings.Contains(stderr.String(), "unexpected argument") {
		t.Errorf("stderr = %q", stderr.String())
	}
}

func TestRenderAgentsDefaultProfilesByteIdentical(t *testing.T) {
	env, stdout, _ := testEnv(t)
	writeConfigOnly(t, env.RepoRoot, validDefaultBothHostsTOML)
	if code := Run([]string{"render-agents"}, env); code != ExitOK {
		t.Fatalf("exit = %d, want %d\n%s", code, ExitOK, stdout.String())
	}

	for _, name := range renderedAgentFiles {
		got, err := os.ReadFile(filepath.Join(env.RepoRoot, filepath.FromSlash(agents.ClaudeDir), name+".md"))
		if err != nil {
			t.Fatalf("read Claude %s: %v", name, err)
		}
		want, err := claude.AgentDefinitions.ReadFile("agents/" + name + ".md")
		if err != nil {
			t.Fatalf("read shipped Claude %s: %v", name, err)
		}
		if string(got) != string(want) {
			t.Errorf("%s.md does not match shipped file", name)
		}
		if !strings.Contains(stdout.String(), "wrote "+agents.ClaudeDir+"/"+name+".md") {
			t.Errorf("stdout missing Claude write confirmation for %s:\n%s", name, stdout.String())
		}

		got, err = os.ReadFile(filepath.Join(env.RepoRoot, filepath.FromSlash(agents.CodexDir), name+".toml"))
		if err != nil {
			t.Fatalf("read Codex %s: %v", name, err)
		}
		want, err = codex.AgentTOMLs.ReadFile("agents/" + name + ".toml")
		if err != nil {
			t.Fatalf("read shipped Codex %s: %v", name, err)
		}
		if string(got) != string(want) {
			t.Errorf("%s.toml does not match shipped file", name)
		}
		if !strings.Contains(stdout.String(), "wrote "+agents.CodexDir+"/"+name+".toml") {
			t.Errorf("stdout missing Codex write confirmation for %s:\n%s", name, stdout.String())
		}
	}
}

type renderedAgentTOML struct {
	Name                  string `toml:"name"`
	Description           string `toml:"description"`
	DeveloperInstructions string `toml:"developer_instructions"`
	Model                 string `toml:"model"`
	ModelReasoningEffort  string `toml:"model_reasoning_effort"`
}

func TestRenderAgentsOverrideSubstitution(t *testing.T) {
	env, _, _ := testEnv(t)
	writeConfigOnly(t, env.RepoRoot, validCodexOverrideTOML)
	if code := Run([]string{"render-agents"}, env); code != ExitOK {
		t.Fatalf("exit = %d, want %d", code, ExitOK)
	}

	dir := filepath.Join(env.RepoRoot, filepath.FromSlash(agents.CodexDir))
	want := map[string]struct{ model, effort string }{
		"orch-scout":         {"gpt-9000", "low"},
		"orch-implementer":   {"gpt-9000", "high"},
		"orch-specialist":    {"gpt-9000-ultra", "medium"},
		"orch-reviewer":      {"gpt-9000-ultra", "medium"},
		"orch-reviewer-safe": {"gpt-9000", "high"},
	}
	for name, wp := range want {
		data, err := os.ReadFile(filepath.Join(dir, name+".toml"))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		var a renderedAgentTOML
		if _, err := toml.Decode(string(data), &a); err != nil {
			t.Fatalf("decode %s: %v", name, err)
		}
		if a.Model != wp.model {
			t.Errorf("%s: model = %q, want %q", name, a.Model, wp.model)
		}
		if a.ModelReasoningEffort != wp.effort {
			t.Errorf("%s: model_reasoning_effort = %q, want %q", name, a.ModelReasoningEffort, wp.effort)
		}
		if a.Name != name {
			t.Errorf("%s: name = %q, want %q", name, a.Name, name)
		}
		if a.Description == "" || a.DeveloperInstructions == "" {
			t.Errorf("%s: description or developer_instructions empty", name)
		}
	}
}

// TestRenderAgentsOverwritesStaleFile asserts a second run replaces
// hand-edited content rather than leaving it in place.
func TestRenderAgentsOverwritesStaleFile(t *testing.T) {
	env, _, _ := testEnv(t)
	writeConfigOnly(t, env.RepoRoot, validCodexTOML)
	dir := filepath.Join(env.RepoRoot, filepath.FromSlash(agents.CodexDir))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "orch-scout.toml"), []byte("hand-edited"), 0o644); err != nil {
		t.Fatal(err)
	}

	if code := Run([]string{"render-agents"}, env); code != ExitOK {
		t.Fatalf("exit = %d, want %d", code, ExitOK)
	}
	got, err := os.ReadFile(filepath.Join(dir, "orch-scout.toml"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(got), "hand-edited") {
		t.Error("hand-edited content survived render-agents")
	}
}

func TestRenderAgentsInHelp(t *testing.T) {
	env, stdout, _ := testEnv(t)
	if code := Run([]string{"help"}, env); code != ExitOK {
		t.Errorf("exit = %d, want %d", code, ExitOK)
	}
	if !strings.Contains(stdout.String(), "render-agents") {
		t.Errorf("help output missing render-agents:\n%s", stdout.String())
	}
}
