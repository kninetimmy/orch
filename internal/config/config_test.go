package config

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// loadFixture copies a testdata file into a temp repo layout and loads it.
func loadFixture(t *testing.T, name string) (*Config, error) {
	t.Helper()
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, ".orchestrator"), 0o755); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join("testdata", filepath.FromSlash(name)))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, ".orchestrator", "config.toml"), data, 0o644); err != nil {
		t.Fatal(err)
	}
	return Load(root)
}

func TestLoadMinimalAppliesDefaults(t *testing.T) {
	cfg, err := loadFixture(t, "valid/minimal.toml")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got := cfg.Concurrency.MaxSubagents; got != 3 {
		t.Errorf("MaxSubagents default = %d, want 3", got)
	}
	if got := cfg.Merge.Strategy; got != "squash" {
		t.Errorf("Merge.Strategy default = %q, want squash", got)
	}
	if got := cfg.EnabledHosts(); len(got) != 1 || got[0] != "claude" {
		t.Errorf("EnabledHosts = %v, want [claude]", got)
	}
	if cfg.Metrics.Enabled {
		t.Error("Metrics.Enabled = true, want false by default")
	}
}

func TestLoadFull(t *testing.T) {
	cfg, err := loadFixture(t, "valid/full.toml")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got := cfg.EnabledHosts(); len(got) != 2 || got[0] != "claude" || got[1] != "codex" {
		t.Errorf("EnabledHosts = %v, want [claude codex]", got)
	}
	if got := cfg.Hosts.Codex.Roles.Architect.Model; got != "gpt-5.6-sol" {
		t.Errorf("codex architect model = %q, want gpt-5.6-sol", got)
	}
	if got := cfg.Hosts.Claude.Roles.ReviewDowngrade.Effort; got != "high" {
		t.Errorf("claude review_downgrade effort = %q, want high", got)
	}
	if got := cfg.Memhub.Mode; got != "required" {
		t.Errorf("memhub.mode = %q, want required", got)
	}
}

func TestLoadInvalid(t *testing.T) {
	tests := []struct {
		fixture   string
		wantInErr string
	}{
		{"invalid/unknown_key.toml", "bogus_key"},
		{"invalid/bad_schema_version.toml", "schema_version"},
		{"invalid/missing_revision.toml", "config_revision"},
		{"invalid/bad_effort.toml", `"ultra"`},
		{"invalid/bad_merge_strategy.toml", `"fast-forward"`},
		{"invalid/bad_memhub_mode.toml", `"maybe"`},
		{"invalid/no_hosts.toml", "at least one of hosts.codex or hosts.claude"},
		{"invalid/missing_role.toml", "hosts.claude.roles.reviewer.model"},
	}
	for _, tt := range tests {
		t.Run(tt.fixture, func(t *testing.T) {
			cfg, err := loadFixture(t, tt.fixture)
			if err == nil {
				t.Fatal("Load succeeded, want error")
			}
			if cfg != nil {
				t.Error("Load returned a partial Config alongside an error")
			}
			if !strings.Contains(err.Error(), tt.wantInErr) {
				t.Errorf("error %q does not mention %q", err, tt.wantInErr)
			}
		})
	}
}

func TestLoadMissingConfig(t *testing.T) {
	cfg, err := Load(t.TempDir())
	if !errors.Is(err, ErrNotInitialized) {
		t.Fatalf("Load err = %v, want ErrNotInitialized", err)
	}
	if cfg != nil {
		t.Error("Load returned a Config for a missing file")
	}
}

func TestHasLocalOverride(t *testing.T) {
	root := t.TempDir()
	if HasLocalOverride(root) {
		t.Error("HasLocalOverride = true in empty repo")
	}
	if err := os.Mkdir(filepath.Join(root, ".orchestrator"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, ".orchestrator", "config.local.toml"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if !HasLocalOverride(root) {
		t.Error("HasLocalOverride = false with override file present")
	}
}
