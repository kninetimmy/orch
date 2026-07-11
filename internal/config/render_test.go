package config

import (
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// update regenerates the golden files instead of comparing against
// them (instructions.Render's -update convention).
var update = flag.Bool("update", false, "regenerate golden files")

func bothHostsConfig() *Config {
	return &Config{
		SchemaVersion:  1,
		ConfigRevision: "sha256:abcdef012345",
		Concurrency:    Concurrency{MaxSubagents: 3},
		Merge:          Merge{Strategy: "squash"},
		Memhub:         Memhub{Mode: "required"},
		Metrics:        Metrics{Enabled: false},
		Hosts: Hosts{
			Claude: &Host{Roles: Roles{
				Architect:       RoleProfile{Model: "claude-opus-4-8", Effort: "xhigh"},
				Scout:           RoleProfile{Model: "claude-sonnet-5", Effort: "low"},
				Implementer:     RoleProfile{Model: "claude-sonnet-5", Effort: "xhigh"},
				Specialist:      RoleProfile{Model: "claude-opus-4-8", Effort: "high"},
				Reviewer:        RoleProfile{Model: "claude-opus-4-8", Effort: "high"},
				ReviewDowngrade: RoleProfile{Model: "claude-sonnet-5", Effort: "high"},
			}},
			Codex: &Host{Roles: Roles{
				Architect:       RoleProfile{Model: "gpt-5.6-sol", Effort: "high"},
				Scout:           RoleProfile{Model: "gpt-5.6-terra", Effort: "low"},
				Implementer:     RoleProfile{Model: "gpt-5.6-terra", Effort: "high"},
				Specialist:      RoleProfile{Model: "gpt-5.6-sol", Effort: "medium"},
				Reviewer:        RoleProfile{Model: "gpt-5.6-sol", Effort: "medium"},
				ReviewDowngrade: RoleProfile{Model: "gpt-5.6-terra", Effort: "high"},
			}},
		},
	}
}

func singleHostConfig() *Config {
	return &Config{
		SchemaVersion:  1,
		ConfigRevision: "sha256:abcdef012345",
		Concurrency:    Concurrency{MaxSubagents: 3},
		Merge:          Merge{Strategy: "squash"},
		Memhub:         Memhub{Mode: "off"},
		Metrics:        Metrics{Enabled: false},
		Hosts: Hosts{
			Claude: &Host{Roles: Roles{
				Architect:       RoleProfile{Model: "claude-opus-4-8", Effort: "xhigh"},
				Scout:           RoleProfile{Model: "claude-sonnet-5", Effort: "low"},
				Implementer:     RoleProfile{Model: "claude-sonnet-5", Effort: "xhigh"},
				Specialist:      RoleProfile{Model: "claude-opus-4-8", Effort: "high"},
				Reviewer:        RoleProfile{Model: "claude-opus-4-8", Effort: "high"},
				ReviewDowngrade: RoleProfile{Model: "claude-sonnet-5", Effort: "high"},
			}},
		},
	}
}

func checkGolden(t *testing.T, name string, got []byte) {
	t.Helper()
	path := filepath.Join("testdata", "render", name)
	if *update {
		if err := os.WriteFile(path, got, 0o644); err != nil {
			t.Fatalf("write golden: %v", err)
		}
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read golden (run `go test ./internal/config -update`): %v", err)
	}
	if string(got) != string(want) {
		t.Errorf("Render does not match %s\n--- got ---\n%s\n--- want ---\n%s", path, got, want)
	}
}

func TestRenderGoldenBothHosts(t *testing.T) {
	got, err := Render(bothHostsConfig())
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	checkGolden(t, "both_hosts.golden.toml", got)
}

func TestRenderGoldenSingleHost(t *testing.T) {
	got, err := Render(singleHostConfig())
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	checkGolden(t, "single_host.golden.toml", got)
}

func TestRenderParseRoundTrip(t *testing.T) {
	for name, cfg := range map[string]*Config{
		"both hosts":  bothHostsConfig(),
		"single host": singleHostConfig(),
	} {
		t.Run(name, func(t *testing.T) {
			data, err := Render(cfg)
			if err != nil {
				t.Fatalf("Render: %v", err)
			}
			got, err := Parse(data)
			if err != nil {
				t.Fatalf("Parse(Render(c)): %v\n%s", err, data)
			}
			if got.ConfigRevision != cfg.ConfigRevision {
				t.Errorf("round-tripped ConfigRevision = %q, want %q", got.ConfigRevision, cfg.ConfigRevision)
			}
			if got.Hosts.Claude.Roles.Architect.Model != cfg.Hosts.Claude.Roles.Architect.Model {
				t.Errorf("round-tripped claude architect model = %q, want %q", got.Hosts.Claude.Roles.Architect.Model, cfg.Hosts.Claude.Roles.Architect.Model)
			}
		})
	}
}

func TestRenderNoTrailingCR(t *testing.T) {
	got, err := Render(bothHostsConfig())
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	if strings.Contains(string(got), "\r") {
		t.Error("Render emitted a carriage return")
	}
}

func TestRenderOnlyEnabledHostsGetTables(t *testing.T) {
	got, err := Render(singleHostConfig())
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	if strings.Contains(string(got), "hosts.codex") {
		t.Error("Render wrote a [hosts.codex...] table for a disabled host")
	}
}
