// Package agents_test is external because adaptertest imports
// internal/run, which imports internal/agents for activation preflight.
package agents_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/BurntSushi/toml"

	"github.com/kninetimmy/orch/adapters/claude"
	"github.com/kninetimmy/orch/adapters/codex"
	"github.com/kninetimmy/orch/internal/adaptertest"
	"github.com/kninetimmy/orch/internal/agents"
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

func defaultClaudeHost() *config.Host {
	p := adaptertest.Profile("claude")
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

func defaultOpenCodeHost() *config.Host {
	p := adaptertest.Profile("opencode")
	rp := func(role string) config.RoleProfile {
		return config.RoleProfile{Model: p[role].Model, Effort: p[role].Effort}
	}
	return &config.Host{Roles: config.Roles{
		Architect: rp("scout"), Scout: rp("scout"), Implementer: rp("implementer"),
		Specialist: rp("specialist"), Reviewer: rp("reviewer"), ReviewDowngrade: rp("reviewer-safe"),
	}}
}

func TestRenderOpenCodeNativeAgents(t *testing.T) {
	files, err := agents.Render("opencode", defaultOpenCodeHost())
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 5 {
		t.Fatalf("len(files) = %d, want 5", len(files))
	}
	for _, file := range files {
		text := string(file.Content)
		if !strings.HasPrefix(file.Path, agents.OpenCodeDir+"/") || !strings.HasSuffix(file.Path, ".md") {
			t.Errorf("Path = %q", file.Path)
		}
		if !strings.Contains(text, "mode: subagent") || !strings.Contains(text, "model: openai/") || !strings.Contains(text, "#") {
			t.Errorf("%s is not a native V2 model/variant agent", file.Path)
		}
	}
}

func stem(f agents.File) string {
	return strings.TrimSuffix(filepath.Base(f.Path), filepath.Ext(f.Path))
}

// TestRenderDefaultProfileByteIdenticalToShipped pins acceptance
// criterion 3: with hosts.codex.roles equal to the PRD §10 defaults,
// every rendered file is byte-identical to its shipped counterpart
// under adapters/codex/agents/ (read here through the same embed
// Render itself uses, since that embed is the single canonical
// source — see adapters/codex/embed.go).
func TestRenderDefaultProfileByteIdenticalToShipped(t *testing.T) {
	files, err := agents.Render("codex", defaultCodexHost())
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	if len(files) != 5 {
		t.Fatalf("Render returned %d files, want 5", len(files))
	}
	for _, f := range files {
		name := stem(f)
		want, err := codex.AgentTOMLs.ReadFile("agents/" + name + ".toml")
		if err != nil {
			t.Fatalf("read shipped %s.toml: %v", name, err)
		}
		if string(f.Content) != string(want) {
			t.Errorf("%s.toml does not match shipped file\n--- got ---\n%s\n--- want ---\n%s", name, f.Content, want)
		}
	}
}

func TestRenderClaudeDefaultProfileByteIdenticalToShipped(t *testing.T) {
	files, err := agents.Render("claude", defaultClaudeHost())
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	if len(files) != 5 {
		t.Fatalf("Render returned %d files, want 5", len(files))
	}
	for _, f := range files {
		name := stem(f)
		want, err := claude.AgentDefinitions.ReadFile("agents/" + name + ".md")
		if err != nil {
			t.Fatalf("read shipped %s.md: %v", name, err)
		}
		if string(f.Content) != string(want) {
			t.Errorf("%s.md does not match shipped file", name)
		}
		if f.Path != agents.ClaudeDir+"/"+name+".md" {
			t.Errorf("Path = %q, want %s/%s.md", f.Path, agents.ClaudeDir, name)
		}
	}
}

// TestRenderOrderAndNames pins the exact five file stems Render
// produces, in order.
func TestRenderOrderAndNames(t *testing.T) {
	files, err := agents.Render("codex", defaultCodexHost())
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	want := []string{"orch-scout", "orch-implementer", "orch-specialist", "orch-reviewer", "orch-reviewer-safe"}
	if len(files) != len(want) {
		t.Fatalf("got %d files, want %d", len(files), len(want))
	}
	for i, f := range files {
		if got := stem(f); got != want[i] {
			t.Errorf("files[%d] stem = %q, want %q", i, got, want[i])
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
	files, err := agents.Render("codex", h)
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
		name := stem(f)
		var a agentTOML
		meta, err := toml.Decode(string(f.Content), &a)
		if err != nil {
			t.Fatalf("decode %s: %v", name, err)
		}
		if undecoded := meta.Undecoded(); len(undecoded) != 0 {
			t.Errorf("%s: unrecognized keys %v", name, undecoded)
		}
		wp := want[name]
		if a.Model != wp.Model {
			t.Errorf("%s: model = %q, want %q", name, a.Model, wp.Model)
		}
		if a.ModelReasoningEffort != wp.Effort {
			t.Errorf("%s: model_reasoning_effort = %q, want %q", name, a.ModelReasoningEffort, wp.Effort)
		}
		if a.Name != name {
			t.Errorf("%s: name = %q, want %q", name, a.Name, name)
		}
		if a.Description == "" {
			t.Errorf("%s: description is empty", name)
		}
		if a.DeveloperInstructions == "" {
			t.Errorf("%s: developer_instructions is empty", name)
		}

		shipped, err := codex.AgentTOMLs.ReadFile("agents/" + name + ".toml")
		if err != nil {
			t.Fatalf("read shipped %s.toml: %v", name, err)
		}
		var sa agentTOML
		if _, err := toml.Decode(string(shipped), &sa); err != nil {
			t.Fatalf("decode shipped %s: %v", name, err)
		}
		if a.Description != sa.Description {
			t.Errorf("%s: description changed from shipped text", name)
		}
		if a.DeveloperInstructions != sa.DeveloperInstructions {
			t.Errorf("%s: developer_instructions changed from shipped text", name)
		}
	}
}

func TestRenderClaudeOverrideChangesOnlyModelLines(t *testing.T) {
	h := defaultClaudeHost()
	h.Roles.Scout.Model = "claude-haiku-5"
	h.Roles.Implementer.Model = "claude-sonnet-5"
	h.Roles.Specialist.Model = "claude-opus-5-1"
	h.Roles.Reviewer.Model = "claude-opus-5-2"
	h.Roles.ReviewDowngrade.Model = "claude-sonnet-5-1"
	// Effort is intentionally different too: this adapter conveys it in
	// the spawn prompt, so it must change no definition byte.
	h.Roles.Scout.Effort = "max"
	h.Roles.Implementer.Effort = "low"

	files, err := agents.Render("claude", h)
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	wantModel := map[string]string{
		"orch-scout":         h.Roles.Scout.Model,
		"orch-implementer":   h.Roles.Implementer.Model,
		"orch-specialist":    h.Roles.Specialist.Model,
		"orch-reviewer":      h.Roles.Reviewer.Model,
		"orch-reviewer-safe": h.Roles.ReviewDowngrade.Model,
	}
	for _, f := range files {
		name := stem(f)
		shipped, err := claude.AgentDefinitions.ReadFile("agents/" + name + ".md")
		if err != nil {
			t.Fatal(err)
		}
		gotLines := strings.Split(string(f.Content), "\n")
		wantLines := strings.Split(string(shipped), "\n")
		if len(gotLines) != len(wantLines) {
			t.Fatalf("%s line count changed: got %d, want %d", name, len(gotLines), len(wantLines))
		}
		for i := range gotLines {
			if gotLines[i] == wantLines[i] {
				continue
			}
			if !strings.HasPrefix(gotLines[i], "model: ") || !strings.HasPrefix(wantLines[i], "model: ") {
				t.Errorf("%s line %d changed outside model frontmatter:\n got %q\nwant %q", name, i+1, gotLines[i], wantLines[i])
			}
		}
		if !strings.Contains(string(f.Content), "\nmodel: \""+wantModel[name]+"\"\n") {
			t.Errorf("%s does not pin model %q", name, wantModel[name])
		}
	}
}

func TestRenderClaudeEffortOnlyIsByteIdentical(t *testing.T) {
	h := defaultClaudeHost()
	h.Roles.Scout.Effort = "max"
	h.Roles.Implementer.Effort = "low"
	h.Roles.Specialist.Effort = "medium"
	h.Roles.Reviewer.Effort = "xhigh"
	h.Roles.ReviewDowngrade.Effort = "high"
	files, err := agents.Render("claude", h)
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range files {
		want, err := claude.AgentDefinitions.ReadFile("agents/" + stem(f) + ".md")
		if err != nil {
			t.Fatal(err)
		}
		if string(f.Content) != string(want) {
			t.Errorf("%s changed for an effort-only override", f.Path)
		}
	}
}

func TestRenderClaudeQuotesUnsafeModel(t *testing.T) {
	h := defaultClaudeHost()
	h.Roles.Scout.Model = "wrong\ntools: Bash"
	files, err := agents.Render("claude", h)
	if err != nil {
		t.Fatal(err)
	}
	content := string(files[0].Content)
	if !strings.Contains(content, `model: "wrong\ntools: Bash"`) {
		t.Errorf("unsafe model was not quoted:\n%s", content)
	}
	if strings.Count(content, "\ntools:") != 1 {
		t.Errorf("unsafe model injected a tools field:\n%s", content)
	}

	h.Roles.Scout.Model = "true"
	files, err = agents.Render("claude", h)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(files[0].Content), `model: "true"`) {
		t.Errorf("YAML keyword model was not quoted:\n%s", files[0].Content)
	}
}

func TestRenderNilHostFailsClosed(t *testing.T) {
	if _, err := agents.Render("codex", nil); err == nil {
		t.Error("agents.Render(nil) succeeded, want an error")
	}
}

func TestRenderUnknownHostFailsClosed(t *testing.T) {
	if _, err := agents.Render("other", &config.Host{}); err == nil {
		t.Error("Render accepted an unsupported host")
	}
}

// TestRenderNoTrailingCR guards the LF-only convention render.go's
// package documents: a rendered file must never introduce a carriage
// return the shipped source did not already contain.
func TestRenderNoTrailingCR(t *testing.T) {
	files, err := agents.Render("codex", defaultCodexHost())
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	for _, f := range files {
		if strings.Contains(string(f.Content), "\r") {
			t.Errorf("%s: rendered content contains a carriage return", f.Path)
		}
	}
}

func TestWriteCreatesDirectoryAndFiles(t *testing.T) {
	root := t.TempDir()
	files, err := agents.Render("codex", defaultCodexHost())
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	if err := agents.Write(root, files); err != nil {
		t.Fatalf("Write: %v", err)
	}

	dir := filepath.Join(root, filepath.FromSlash(agents.CodexDir))
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	if len(entries) != 5 {
		t.Fatalf("wrote %d files, want 5", len(entries))
	}
	for _, f := range files {
		got, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(f.Path)))
		if err != nil {
			t.Fatalf("read written %s: %v", f.Path, err)
		}
		if string(got) != string(f.Content) {
			t.Errorf("%s: written content does not match Render's output", f.Path)
		}
	}

	// Left-over temp files must never survive a successful Write.
	for _, e := range entries {
		if strings.Contains(e.Name(), ".tmp") {
			t.Errorf("leftover temp file %q in %s", e.Name(), dir)
		}
	}
}

func TestWriteCreatesBothHostDirectories(t *testing.T) {
	root := t.TempDir()
	claudeFiles, err := agents.Render("claude", defaultClaudeHost())
	if err != nil {
		t.Fatal(err)
	}
	codexFiles, err := agents.Render("codex", defaultCodexHost())
	if err != nil {
		t.Fatal(err)
	}
	if err := agents.Write(root, append(claudeFiles, codexFiles...)); err != nil {
		t.Fatal(err)
	}
	for _, dir := range []string{agents.ClaudeDir, agents.CodexDir} {
		entries, err := os.ReadDir(filepath.Join(root, filepath.FromSlash(dir)))
		if err != nil {
			t.Fatal(err)
		}
		if len(entries) != 5 {
			t.Errorf("%s has %d files, want 5", dir, len(entries))
		}
	}
}

// TestWriteOverwritesExisting asserts a second Write replaces stale
// content rather than leaving it or appending to it.
func TestWriteOverwritesExisting(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, filepath.FromSlash(agents.CodexDir))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	stale := filepath.Join(dir, "orch-scout.toml")
	if err := os.WriteFile(stale, []byte("stale content"), 0o644); err != nil {
		t.Fatal(err)
	}

	files, err := agents.Render("codex", defaultCodexHost())
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	if err := agents.Write(root, files); err != nil {
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

func TestStaleReportsEveryBadFile(t *testing.T) {
	root := t.TempDir()
	h := defaultCodexHost()
	files, err := agents.Render("codex", h)
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	if err := agents.Write(root, files); err != nil {
		t.Fatalf("Write: %v", err)
	}

	dir := filepath.Join(root, filepath.FromSlash(agents.CodexDir))
	if err := os.Remove(filepath.Join(dir, "orch-scout.toml")); err != nil {
		t.Fatal(err)
	}
	unreadable := filepath.Join(dir, "orch-implementer.toml")
	if err := os.Remove(unreadable); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(unreadable, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "orch-reviewer.toml"), []byte("stale\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	stale, err := agents.Stale(root, "codex", h)
	want := []string{
		agents.CodexDir + "/orch-scout.toml",
		agents.CodexDir + "/orch-implementer.toml",
		agents.CodexDir + "/orch-reviewer.toml",
	}
	if strings.Join(stale, ",") != strings.Join(want, ",") {
		t.Errorf("Stale = %v, want %v", stale, want)
	}
	if err == nil || !strings.Contains(err.Error(), agents.CodexDir+"/orch-implementer.toml") {
		t.Errorf("err = %v, want unreadable file named", err)
	}
}

func TestStaleTracksEffectiveConfiguration(t *testing.T) {
	root := t.TempDir()
	files, err := agents.Render("codex", defaultCodexHost())
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	if err := agents.Write(root, files); err != nil {
		t.Fatalf("Write: %v", err)
	}

	h := defaultCodexHost()
	h.Roles.Reviewer = config.RoleProfile{Model: "gpt-9000-ultra", Effort: "low"}
	stale, err := agents.Stale(root, "codex", h)
	if err != nil {
		t.Fatalf("Stale: %v", err)
	}
	want := agents.CodexDir + "/orch-reviewer.toml"
	if len(stale) != 1 || stale[0] != want {
		t.Errorf("Stale = %v, want [%s]", stale, want)
	}
}

func TestStaleTracksClaudeEffectiveConfiguration(t *testing.T) {
	root := t.TempDir()
	h := defaultClaudeHost()
	files, err := agents.Render("claude", h)
	if err != nil {
		t.Fatal(err)
	}
	if err := agents.Write(root, files); err != nil {
		t.Fatal(err)
	}
	h.Roles.Reviewer.Model = "claude-opus-5-1"
	stale, err := agents.Stale(root, "claude", h)
	if err != nil {
		t.Fatal(err)
	}
	want := agents.ClaudeDir + "/orch-reviewer.md"
	if len(stale) != 1 || stale[0] != want {
		t.Errorf("Stale = %v, want [%s]", stale, want)
	}
}
