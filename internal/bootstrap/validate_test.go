package bootstrap

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kninetimmy/orch/internal/config"
	"github.com/kninetimmy/orch/internal/instructions"
	"github.com/kninetimmy/orch/internal/question"
)

// validComplete builds a self-consistent Complete document (config
// round-trips, revision matches, one current instruction file, one
// gitignore line) so each test can corrupt exactly one aspect of the
// worktree it validates against.
func validComplete(t *testing.T) *question.Complete {
	t.Helper()
	cfg := &config.Config{
		SchemaVersion: 1,
		Concurrency:   config.Concurrency{MaxSubagents: 3},
		Merge:         config.Merge{Strategy: "squash"},
		Memhub:        config.Memhub{Mode: "off"},
		Hosts: config.Hosts{Claude: &config.Host{Roles: config.Roles{
			Architect:       config.RoleProfile{Model: "claude-opus-4-8", Effort: "xhigh"},
			Scout:           config.RoleProfile{Model: "claude-sonnet-5", Effort: "low"},
			Implementer:     config.RoleProfile{Model: "claude-sonnet-5", Effort: "xhigh"},
			Specialist:      config.RoleProfile{Model: "claude-opus-4-8", Effort: "high"},
			Reviewer:        config.RoleProfile{Model: "claude-opus-4-8", Effort: "high"},
			ReviewDowngrade: config.RoleProfile{Model: "claude-sonnet-5", Effort: "high"},
		}}},
	}
	rev, err := config.Revision(cfg)
	if err != nil {
		t.Fatal(err)
	}
	cfg.ConfigRevision = rev
	rendered, err := config.Render(cfg)
	if err != nil {
		t.Fatal(err)
	}
	block, err := instructions.Render(instructions.CurrentVersion)
	if err != nil {
		t.Fatal(err)
	}
	return &question.Complete{
		Summary: question.Summary{
			ConfigTOML:     string(rendered),
			ConfigRevision: rev,
			Files:          []question.FileChange{{Path: "CLAUDE.md", NewContent: block}},
			GitignoreLines: []string{".orchestrator/state.json"},
		},
		Detection:      map[string]string{"git": "yes", "gh": "yes"},
		BootstrapReady: true,
	}
}

// installValid writes exactly what validComplete describes into dir,
// so a passing baseline test can flip one aspect at a time.
func installValid(t *testing.T, dir string, complete *question.Complete) {
	t.Helper()
	if err := writeFiles(dir, complete); err != nil {
		t.Fatalf("writeFiles: %v", err)
	}
}

func TestValidateInstallationPasses(t *testing.T) {
	dir := t.TempDir()
	complete := validComplete(t)
	installValid(t, dir, complete)

	entries, err := validateInstallation(dir, complete, time.Now())
	if err != nil {
		t.Fatalf("validateInstallation: %v", err)
	}
	if len(entries) != 4 {
		t.Fatalf("entries = %+v, want 4 (config.Load, config.Revision, instructions:CLAUDE.md, gitignore)", entries)
	}
	for _, e := range entries {
		if e.Result != "pass" {
			t.Errorf("entry %s = %+v, want pass", e.Name, e)
		}
		if e.At == "" {
			t.Errorf("entry %s carries no At stamp", e.Name)
		}
	}
}

func TestValidateInstallationConfigLoadFails(t *testing.T) {
	dir := t.TempDir()
	complete := validComplete(t)
	installValid(t, dir, complete)
	if err := os.Remove(filepath.Join(dir, config.Path)); err != nil {
		t.Fatal(err)
	}

	entries, err := validateInstallation(dir, complete, time.Now())
	if !errors.Is(err, ErrValidationFailed) {
		t.Fatalf("err = %v, want ErrValidationFailed", err)
	}
	if len(entries) != 1 || entries[0].Name != "config.Load" || entries[0].Result != "fail" {
		t.Fatalf("entries = %+v, want a single failing config.Load entry", entries)
	}
}

func TestValidateInstallationRevisionMismatch(t *testing.T) {
	dir := t.TempDir()
	complete := validComplete(t)
	installValid(t, dir, complete)
	complete.Summary.ConfigRevision = "sha256:deadbeefdead"

	entries, err := validateInstallation(dir, complete, time.Now())
	if !errors.Is(err, ErrValidationFailed) {
		t.Fatalf("err = %v, want ErrValidationFailed", err)
	}
	last := entries[len(entries)-1]
	if last.Name != "config.Revision" || last.Result != "fail" {
		t.Fatalf("last entry = %+v, want a failing config.Revision", last)
	}
}

func TestValidateInstallationInstructionsDrifted(t *testing.T) {
	dir := t.TempDir()
	complete := validComplete(t)
	installValid(t, dir, complete)
	drifted := strings.Replace(complete.Summary.Files[0].NewContent, "This file", "THIS FILE", 1)
	if err := os.WriteFile(filepath.Join(dir, "CLAUDE.md"), []byte(drifted), 0o644); err != nil {
		t.Fatal(err)
	}

	entries, err := validateInstallation(dir, complete, time.Now())
	if !errors.Is(err, ErrValidationFailed) {
		t.Fatalf("err = %v, want ErrValidationFailed", err)
	}
	last := entries[len(entries)-1]
	if last.Name != "instructions:CLAUDE.md" || last.Result != "fail" {
		t.Fatalf("last entry = %+v, want a failing instructions:CLAUDE.md", last)
	}
}

func TestValidateInstallationGitignoreMissingLine(t *testing.T) {
	dir := t.TempDir()
	complete := validComplete(t)
	installValid(t, dir, complete)
	if err := os.Remove(filepath.Join(dir, ".gitignore")); err != nil {
		t.Fatal(err)
	}

	entries, err := validateInstallation(dir, complete, time.Now())
	if !errors.Is(err, ErrValidationFailed) {
		t.Fatalf("err = %v, want ErrValidationFailed", err)
	}
	last := entries[len(entries)-1]
	if last.Name != "gitignore" || last.Result != "fail" {
		t.Fatalf("last entry = %+v, want a failing gitignore", last)
	}
	if !strings.Contains(last.Detail, ".orchestrator/state.json") {
		t.Errorf("Detail = %q, want it to name the missing line", last.Detail)
	}
}

func TestValidateInstallationStampsWithNow(t *testing.T) {
	dir := t.TempDir()
	complete := validComplete(t)
	installValid(t, dir, complete)

	entries, err := validateInstallation(dir, complete, fixedNow())
	if err != nil {
		t.Fatalf("validateInstallation: %v", err)
	}
	want := fixedNow().UTC().Format(time.RFC3339)
	for _, e := range entries {
		if e.At != want {
			t.Errorf("entry %s At = %q, want %q", e.Name, e.At, want)
		}
	}
}
