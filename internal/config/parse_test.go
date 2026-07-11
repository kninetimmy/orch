package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// readTestdata is Parse's own file-free counterpart to loadFixture: it
// reads a testdata fixture's bytes without ever writing them to a temp
// repo, so Parse's contract (in-memory TOML in, *Config out) is
// exercised directly.
func readTestdata(t *testing.T, name string) []byte {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", filepath.FromSlash(name)))
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func TestParseMinimalAppliesDefaults(t *testing.T) {
	cfg, err := Parse(readTestdata(t, "valid/minimal.toml"))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if got := cfg.Concurrency.MaxSubagents; got != 3 {
		t.Errorf("MaxSubagents default = %d, want 3", got)
	}
	if got := cfg.Merge.Strategy; got != "squash" {
		t.Errorf("Merge.Strategy default = %q, want squash", got)
	}
}

func TestParseFull(t *testing.T) {
	cfg, err := Parse(readTestdata(t, "valid/full.toml"))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if got := cfg.EnabledHosts(); len(got) != 2 || got[0] != "claude" || got[1] != "codex" {
		t.Errorf("EnabledHosts = %v, want [claude codex]", got)
	}
}

func TestParseInvalid(t *testing.T) {
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
			cfg, err := Parse(readTestdata(t, tt.fixture))
			if err == nil {
				t.Fatal("Parse succeeded, want error")
			}
			if cfg != nil {
				t.Error("Parse returned a partial Config alongside an error")
			}
			if !strings.Contains(err.Error(), tt.wantInErr) {
				t.Errorf("error %q does not mention %q", err, tt.wantInErr)
			}
		})
	}
}

func TestParseMalformedTOML(t *testing.T) {
	_, err := Parse([]byte("schema_version = ["))
	if err == nil {
		t.Fatal("Parse succeeded on malformed TOML, want error")
	}
}
