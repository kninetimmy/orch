package interview

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kninetimmy/orch/internal/config"
	"github.com/kninetimmy/orch/internal/instructions"
	"github.com/kninetimmy/orch/internal/lockfile"
	"github.com/kninetimmy/orch/internal/run"
	"github.com/kninetimmy/orch/internal/state"
)

// TestBaseGitignoreLinesMatchSourceConstants pins summary.go's literal
// gitignore patterns to the constants they mirror. The literals exist
// so the pre-init-safe interview package need not import
// internal/run/state/lockfile in production code; this test-only
// import makes that duplication drift-proof instead of merely
// documented.
func TestBaseGitignoreLinesMatchSourceConstants(t *testing.T) {
	want := []string{
		run.WorktreeContainer + "/",
		config.LocalOverridePath,
		state.Path,
		lockfile.Path,
	}
	if len(baseGitignoreLines) != len(want) {
		t.Fatalf("baseGitignoreLines = %v, want %v", baseGitignoreLines, want)
	}
	for i, w := range want {
		if baseGitignoreLines[i] != w {
			t.Errorf("baseGitignoreLines[%d] = %q, want %q (source constant changed?)", i, baseGitignoreLines[i], w)
		}
	}
}

func TestBuildSummaryFreshRepo(t *testing.T) {
	cfg, err := materialize(fullAnswers())
	if err != nil {
		t.Fatalf("materialize: %v", err)
	}
	root := t.TempDir()

	summary, err := buildSummary(cfg, root)
	if err != nil {
		t.Fatalf("buildSummary: %v", err)
	}
	if len(summary.Blockers) != 0 {
		t.Fatalf("Blockers = %v, want none", summary.Blockers)
	}
	if len(summary.Conflicts) != 0 {
		t.Fatalf("Conflicts = %v, want none", summary.Conflicts)
	}
	if summary.ConfigRevision != cfg.ConfigRevision {
		t.Errorf("ConfigRevision = %q, want %q", summary.ConfigRevision, cfg.ConfigRevision)
	}
	if !strings.Contains(summary.ConfigTOML, "schema_version") {
		t.Errorf("ConfigTOML does not look like rendered TOML: %s", summary.ConfigTOML)
	}

	if len(summary.Files) != 2 {
		t.Fatalf("Files = %v, want 2 entries (CLAUDE.md, AGENTS.md)", summary.Files)
	}
	if summary.Files[0].Path != "CLAUDE.md" || summary.Files[1].Path != "AGENTS.md" {
		t.Errorf("Files order = [%s, %s], want [CLAUDE.md, AGENTS.md]", summary.Files[0].Path, summary.Files[1].Path)
	}
	for _, f := range summary.Files {
		if f.Existed {
			t.Errorf("%s: Existed = true, want false for a fresh repo", f.Path)
		}
		if !strings.Contains(f.NewContent, "orchestrator:managed:start") {
			t.Errorf("%s: NewContent does not carry the managed block: %s", f.Path, f.NewContent)
		}
	}

	want := []string{
		".orchestrator/worktrees/",
		".orchestrator/config.local.toml",
		".orchestrator/state.json",
		".orchestrator/delivery.lock",
	}
	if len(summary.GitignoreLines) != len(want) {
		t.Fatalf("GitignoreLines = %v, want %v", summary.GitignoreLines, want)
	}
	for i, line := range want {
		if summary.GitignoreLines[i] != line {
			t.Errorf("GitignoreLines[%d] = %q, want %q", i, summary.GitignoreLines[i], line)
		}
	}
}

func TestBuildSummaryMetricsEnabledAddsGitignoreLine(t *testing.T) {
	answers := fullAnswers()
	answers[idMetricsEnabled] = "yes"
	cfg, err := materialize(answers)
	if err != nil {
		t.Fatalf("materialize: %v", err)
	}
	summary, err := buildSummary(cfg, t.TempDir())
	if err != nil {
		t.Fatalf("buildSummary: %v", err)
	}
	found := false
	for _, l := range summary.GitignoreLines {
		if l == metricsGitignoreLine {
			found = true
		}
	}
	if !found {
		t.Errorf("GitignoreLines = %v, want %q present when metrics is enabled", summary.GitignoreLines, metricsGitignoreLine)
	}
}

func TestBuildSummaryPreservesExistingUserContent(t *testing.T) {
	cfg, err := materialize(fullAnswers())
	if err != nil {
		t.Fatalf("materialize: %v", err)
	}
	root := t.TempDir()
	userContent := "# My project\n\nSome human-authored notes.\n"
	if err := os.WriteFile(filepath.Join(root, "CLAUDE.md"), []byte(userContent), 0o644); err != nil {
		t.Fatal(err)
	}

	summary, err := buildSummary(cfg, root)
	if err != nil {
		t.Fatalf("buildSummary: %v", err)
	}
	var found bool
	for _, f := range summary.Files {
		if f.Path != "CLAUDE.md" {
			continue
		}
		found = true
		if !f.Existed {
			t.Error("Existed = false, want true for a pre-existing file")
		}
		if !strings.HasPrefix(f.NewContent, userContent) {
			t.Errorf("NewContent does not preserve the original user content verbatim:\n%s", f.NewContent)
		}
		if !strings.Contains(f.NewContent, "orchestrator:managed:start") {
			t.Error("NewContent does not carry the managed block")
		}
	}
	if !found {
		t.Fatal("no CLAUDE.md entry in Files")
	}
}

func TestBuildSummaryDriftedBlockIsABlocker(t *testing.T) {
	cfg, err := materialize(fullAnswers())
	if err != nil {
		t.Fatalf("materialize: %v", err)
	}
	root := t.TempDir()
	block, err := instructions.Render(instructions.CurrentVersion)
	if err != nil {
		t.Fatal(err)
	}
	drifted := strings.Replace(block, "This file", "THIS FILE", 1)
	if err := os.WriteFile(filepath.Join(root, "CLAUDE.md"), []byte(drifted), 0o644); err != nil {
		t.Fatal(err)
	}

	summary, err := buildSummary(cfg, root)
	if err != nil {
		t.Fatalf("buildSummary: %v", err)
	}
	if len(summary.Blockers) != 1 {
		t.Fatalf("Blockers = %v, want exactly one", summary.Blockers)
	}
	if !strings.Contains(summary.Blockers[0], "CLAUDE.md") {
		t.Errorf("blocker %q does not name CLAUDE.md", summary.Blockers[0])
	}
	for _, f := range summary.Files {
		if f.Path == "CLAUDE.md" {
			t.Error("Files still carries the drifted CLAUDE.md; it should have been excluded as a blocker")
		}
	}
	// AGENTS.md is unaffected: it still gets a proposed Files entry.
	found := false
	for _, f := range summary.Files {
		if f.Path == "AGENTS.md" {
			found = true
		}
	}
	if !found {
		t.Error("AGENTS.md is missing from Files; only CLAUDE.md should have been blocked")
	}
}

func TestBuildSummaryNestedConflict(t *testing.T) {
	cfg, err := materialize(fullAnswers())
	if err != nil {
		t.Fatalf("materialize: %v", err)
	}
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	block, err := instructions.Render(instructions.CurrentVersion)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "sub", "AGENTS.md"), []byte(block), 0o644); err != nil {
		t.Fatal(err)
	}

	summary, err := buildSummary(cfg, root)
	if err != nil {
		t.Fatalf("buildSummary: %v", err)
	}
	if len(summary.Conflicts) != 1 {
		t.Fatalf("Conflicts = %v, want exactly one", summary.Conflicts)
	}
	if !strings.Contains(summary.Conflicts[0], filepath.ToSlash(filepath.Join("sub", "AGENTS.md"))) {
		t.Errorf("conflict %q does not name sub/AGENTS.md", summary.Conflicts[0])
	}
}

func TestBuildSummaryFiltersExistingGitignoreLines(t *testing.T) {
	cfg, err := materialize(fullAnswers())
	if err != nil {
		t.Fatalf("materialize: %v", err)
	}
	root := t.TempDir()
	existing := ".orchestrator/worktrees/\nnode_modules/\n.orchestrator/state.json\n"
	if err := os.WriteFile(filepath.Join(root, ".gitignore"), []byte(existing), 0o644); err != nil {
		t.Fatal(err)
	}

	summary, err := buildSummary(cfg, root)
	if err != nil {
		t.Fatalf("buildSummary: %v", err)
	}
	want := []string{".orchestrator/config.local.toml", ".orchestrator/delivery.lock"}
	if len(summary.GitignoreLines) != len(want) {
		t.Fatalf("GitignoreLines = %v, want %v", summary.GitignoreLines, want)
	}
	for i, line := range want {
		if summary.GitignoreLines[i] != line {
			t.Errorf("GitignoreLines[%d] = %q, want %q", i, summary.GitignoreLines[i], line)
		}
	}
}
