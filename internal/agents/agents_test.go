package agents

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/BurntSushi/toml"

	"github.com/kninetimmy/orch/adapters/codex"
	"github.com/kninetimmy/orch/internal/adaptertest"
	"github.com/kninetimmy/orch/internal/config"
)

// defaultCodexHost builds a *config.Host whose roles equal the PRD §10
// codex defaults (adaptertest.Profile("codex"), the same fixture
// adapters/codex/plugin_test.go's TestAgentTOMLs pins the shipped
// files against).
func defaultCodexHost() *config.Host {
	p := adaptertest.Profile("codex")
	rp := func(role string) config.RoleProfile {
		return config.RoleProfile{Model: p[role].Model, Effort: p[role].Effort}
	}
	return &config.Host{Roles: config.Roles{
		Architect:       rp("scout"), // architect has no agent file; value irrelevant here
		Scout:           rp("scout"),
		Implementer:     rp("implementer"),
		Specialist:      rp("specialist"),
		Reviewer:        rp("reviewer"),
		ReviewDowngrade: rp("reviewer-safe"),
	}}
}

// TestRenderDefaultProfileByteIdenticalToShipped pins acceptance
// criterion 3: with hosts.codex.roles equal to the PRD §10 defaults,
// every rendered file is byte-identical to its shipped counterpart
// under adapters/codex/agents/ (read here through the same embed
// Render itself uses, since that embed is the single canonical
// source — see adapters/codex/embed.go).
func TestRenderDefaultProfileByteIdenticalToShipped(t *testing.T) {
	files, err := Render(defaultCodexHost())
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	if len(files) != 5 {
		t.Fatalf("Render returned %d files, want 5", len(files))
	}
	for _, f := range files {
		want, err := codex.AgentTOMLs.ReadFile("agents/" + f.Name + ".toml")
		if err != nil {
			t.Fatalf("read shipped %s.toml: %v", f.Name, err)
		}
		if string(f.Content) != string(want) {
			t.Errorf("%s.toml does not match shipped file\n--- got ---\n%s\n--- want ---\n%s", f.Name, f.Content, want)
		}
	}
}

// TestRenderOrderAndNames pins the exact five file stems Render
// produces, in order.
func TestRenderOrderAndNames(t *testing.T) {
	files, err := Render(defaultCodexHost())
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	want := []string{"orch-scout", "orch-implementer", "orch-specialist", "orch-reviewer", "orch-reviewer-safe"}
	if len(files) != len(want) {
		t.Fatalf("got %d files, want %d", len(files), len(want))
	}
	for i, f := range files {
		if f.Name != want[i] {
			t.Errorf("files[%d].Name = %q, want %q", i, f.Name, want[i])
		}
	}
}

// agentTOML mirrors adapters/codex/plugin_test.go's strict decode
// shape, so this package's own tests can assert a rendered file still
// parses and carries the substituted values without duplicating that
// file's private type.
type agentTOML struct {
	Name                  string `toml:"name"`
	Description           string `toml:"description"`
	DeveloperInstructions string `toml:"developer_instructions"`
	Model                 string `toml:"model"`
	ModelReasoningEffort  string `toml:"model_reasoning_effort"`
}

// TestRenderOverrideSubstitution asserts a non-default configuration's
// model/effort values land in the rendered TOML, and only there: name,
// description, and developer_instructions stay exactly the shipped
// prose.
func TestRenderOverrideSubstitution(t *testing.T) {
	h := &config.Host{Roles: config.Roles{
		Scout:           config.RoleProfile{Model: "gpt-9000", Effort: "low"},
		Implementer:     config.RoleProfile{Model: "gpt-9000", Effort: "high"},
		Specialist:      config.RoleProfile{Model: "gpt-9000-ultra", Effort: "medium"},
		Reviewer:        config.RoleProfile{Model: "gpt-9000-ultra", Effort: "medium"},
		ReviewDowngrade: config.RoleProfile{Model: "gpt-9000", Effort: "high"},
	}}
	files, err := Render(h)
	if err != nil {
		t.Fatalf("Render: %v", err)
	}

	want := map[string]config.RoleProfile{
		"orch-scout":         h.Roles.Scout,
		"orch-implementer":   h.Roles.Implementer,
		"orch-specialist":    h.Roles.Specialist,
		"orch-reviewer":      h.Roles.Reviewer,
		"orch-reviewer-safe": h.Roles.ReviewDowngrade,
	}
	for _, f := range files {
		var a agentTOML
		meta, err := toml.Decode(string(f.Content), &a)
		if err != nil {
			t.Fatalf("decode %s: %v", f.Name, err)
		}
		if undecoded := meta.Undecoded(); len(undecoded) != 0 {
			t.Errorf("%s: unrecognized keys %v", f.Name, undecoded)
		}
		wp := want[f.Name]
		if a.Model != wp.Model {
			t.Errorf("%s: model = %q, want %q", f.Name, a.Model, wp.Model)
		}
		if a.ModelReasoningEffort != wp.Effort {
			t.Errorf("%s: model_reasoning_effort = %q, want %q", f.Name, a.ModelReasoningEffort, wp.Effort)
		}
		if a.Name != f.Name {
			t.Errorf("%s: name = %q, want %q", f.Name, a.Name, f.Name)
		}
		if a.Description == "" {
			t.Errorf("%s: description is empty", f.Name)
		}
		if a.DeveloperInstructions == "" {
			t.Errorf("%s: developer_instructions is empty", f.Name)
		}

		shipped, err := codex.AgentTOMLs.ReadFile("agents/" + f.Name + ".toml")
		if err != nil {
			t.Fatalf("read shipped %s.toml: %v", f.Name, err)
		}
		var sa agentTOML
		if _, err := toml.Decode(string(shipped), &sa); err != nil {
			t.Fatalf("decode shipped %s: %v", f.Name, err)
		}
		if a.Description != sa.Description {
			t.Errorf("%s: description changed from shipped text", f.Name)
		}
		if a.DeveloperInstructions != sa.DeveloperInstructions {
			t.Errorf("%s: developer_instructions changed from shipped text", f.Name)
		}
	}
}

func TestRenderNilHostFailsClosed(t *testing.T) {
	if _, err := Render(nil); err == nil {
		t.Error("Render(nil) succeeded, want an error")
	}
}

// TestRenderNoTrailingCR guards the LF-only convention render.go's
// package documents: a rendered file must never introduce a carriage
// return the shipped source did not already contain.
func TestRenderNoTrailingCR(t *testing.T) {
	files, err := Render(defaultCodexHost())
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	for _, f := range files {
		if strings.Contains(string(f.Content), "\r") {
			t.Errorf("%s: rendered content contains a carriage return", f.Name)
		}
	}
}

func TestWriteCreatesDirectoryAndFiles(t *testing.T) {
	root := t.TempDir()
	files, err := Render(defaultCodexHost())
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	if err := Write(root, files); err != nil {
		t.Fatalf("Write: %v", err)
	}

	dir := filepath.Join(root, filepath.FromSlash(Dir))
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	if len(entries) != 5 {
		t.Fatalf("wrote %d files, want 5", len(entries))
	}
	for _, f := range files {
		got, err := os.ReadFile(filepath.Join(dir, f.Name+".toml"))
		if err != nil {
			t.Fatalf("read written %s: %v", f.Name, err)
		}
		if string(got) != string(f.Content) {
			t.Errorf("%s: written content does not match Render's output", f.Name)
		}
	}

	// Left-over temp files must never survive a successful Write.
	for _, e := range entries {
		if strings.Contains(e.Name(), ".tmp") {
			t.Errorf("leftover temp file %q in %s", e.Name(), dir)
		}
	}
}

// TestWriteOverwritesExisting asserts a second Write replaces stale
// content rather than leaving it or appending to it.
func TestWriteOverwritesExisting(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, filepath.FromSlash(Dir))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	stale := filepath.Join(dir, "orch-scout.toml")
	if err := os.WriteFile(stale, []byte("stale content"), 0o644); err != nil {
		t.Fatal(err)
	}

	files, err := Render(defaultCodexHost())
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	if err := Write(root, files); err != nil {
		t.Fatalf("Write: %v", err)
	}

	got, err := os.ReadFile(stale)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(got), "stale content") {
		t.Error("stale content survived Write")
	}
}
