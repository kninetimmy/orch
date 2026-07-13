package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/BurntSushi/toml"

	"github.com/kninetimmy/orch/adapters/codex"
	"github.com/kninetimmy/orch/internal/agents"
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
effort = "high"

[hosts.codex.roles.scout]
model  = "gpt-5.6-terra"
effort = "low"

[hosts.codex.roles.implementer]
model  = "gpt-5.6-terra"
effort = "high"

[hosts.codex.roles.specialist]
model  = "gpt-5.6-sol"
effort = "medium"

[hosts.codex.roles.reviewer]
model  = "gpt-5.6-sol"
effort = "medium"

[hosts.codex.roles.review_downgrade]
model  = "gpt-5.6-terra"
effort = "high"
`

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
	writeConfig(t, env.RepoRoot, "schema_version = 1\n")
	if code := Run([]string{"render-agents"}, env); code != ExitError {
		t.Errorf("exit = %d, want %d", code, ExitError)
	}
	if !strings.Contains(stderr.String(), "invalid configuration") {
		t.Errorf("stderr = %q", stderr.String())
	}
}

func TestRenderAgentsCodexDisabledRefusal(t *testing.T) {
	env, _, stderr := testEnv(t)
	writeConfig(t, env.RepoRoot, validTOML) // claude-only fixture
	if code := Run([]string{"render-agents"}, env); code != ExitError {
		t.Errorf("exit = %d, want %d", code, ExitError)
	}
	if !strings.Contains(stderr.String(), "hosts.codex is not enabled") {
		t.Errorf("stderr = %q", stderr.String())
	}
	if _, err := os.Stat(filepath.Join(env.RepoRoot, filepath.FromSlash(agents.Dir))); !os.IsNotExist(err) {
		t.Errorf(".codex/agents exists after refusal (stat err = %v), want absent", err)
	}
}

func TestRenderAgentsUnexpectedArgument(t *testing.T) {
	env, _, stderr := testEnv(t)
	writeConfig(t, env.RepoRoot, validCodexTOML)
	if code := Run([]string{"render-agents", "extra"}, env); code != ExitUsage {
		t.Errorf("exit = %d, want %d", code, ExitUsage)
	}
	if !strings.Contains(stderr.String(), "unexpected argument") {
		t.Errorf("stderr = %q", stderr.String())
	}
}

func TestRenderAgentsDefaultProfileByteIdentical(t *testing.T) {
	env, stdout, _ := testEnv(t)
	writeConfig(t, env.RepoRoot, validCodexTOML)
	if code := Run([]string{"render-agents"}, env); code != ExitOK {
		t.Fatalf("exit = %d, want %d\n%s", code, ExitOK, stdout.String())
	}

	dir := filepath.Join(env.RepoRoot, filepath.FromSlash(agents.Dir))
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	if len(entries) != 5 {
		t.Fatalf("wrote %d files, want 5", len(entries))
	}

	for _, name := range renderedAgentFiles {
		got, err := os.ReadFile(filepath.Join(dir, name+".toml"))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		want, err := codex.AgentTOMLs.ReadFile("agents/" + name + ".toml")
		if err != nil {
			t.Fatalf("read shipped %s: %v", name, err)
		}
		if string(got) != string(want) {
			t.Errorf("%s.toml does not match shipped file", name)
		}
		if !strings.Contains(stdout.String(), "wrote "+agents.Dir+"/"+name+".toml") {
			t.Errorf("stdout missing write confirmation for %s:\n%s", name, stdout.String())
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
	writeConfig(t, env.RepoRoot, validCodexOverrideTOML)
	if code := Run([]string{"render-agents"}, env); code != ExitOK {
		t.Fatalf("exit = %d, want %d", code, ExitOK)
	}

	dir := filepath.Join(env.RepoRoot, filepath.FromSlash(agents.Dir))
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
	writeConfig(t, env.RepoRoot, validCodexTOML)
	dir := filepath.Join(env.RepoRoot, filepath.FromSlash(agents.Dir))
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
