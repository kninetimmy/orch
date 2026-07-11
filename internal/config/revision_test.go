package config

import (
	"strings"
	"testing"
)

func TestRevisionDeterministic(t *testing.T) {
	c := bothHostsConfig()
	a, err := Revision(c)
	if err != nil {
		t.Fatalf("Revision: %v", err)
	}
	b, err := Revision(c)
	if err != nil {
		t.Fatalf("Revision: %v", err)
	}
	if a != b {
		t.Errorf("Revision is not deterministic: %q vs %q", a, b)
	}
	if !strings.HasPrefix(a, "sha256:") {
		t.Errorf("Revision = %q, want sha256: prefix", a)
	}
	if len(a) != len("sha256:")+12 {
		t.Errorf("Revision = %q, want 12 hex characters after the prefix", a)
	}
}

func TestRevisionInsensitiveToConfigRevisionAndOverrides(t *testing.T) {
	c := bothHostsConfig()
	base, err := Revision(c)
	if err != nil {
		t.Fatalf("Revision: %v", err)
	}

	c.ConfigRevision = "sha256:somethingelse"
	afterRevision, err := Revision(c)
	if err != nil {
		t.Fatalf("Revision: %v", err)
	}
	if afterRevision != base {
		t.Errorf("Revision changed when ConfigRevision changed: %q vs %q", afterRevision, base)
	}

	c.Overrides = []string{"concurrency.max_subagents"}
	afterOverrides, err := Revision(c)
	if err != nil {
		t.Fatalf("Revision: %v", err)
	}
	if afterOverrides != base {
		t.Errorf("Revision changed when Overrides changed: %q vs %q", afterOverrides, base)
	}
}

func TestRevisionSensitiveToSemanticChange(t *testing.T) {
	base, err := Revision(bothHostsConfig())
	if err != nil {
		t.Fatalf("Revision: %v", err)
	}

	mutations := map[string]func(*Config){
		"max_subagents":  func(c *Config) { c.Concurrency.MaxSubagents = 5 },
		"merge strategy": func(c *Config) { c.Merge.Strategy = "rebase" },
		"memhub mode":    func(c *Config) { c.Memhub.Mode = "off" },
		"metrics":        func(c *Config) { c.Metrics.Enabled = true },
		"model":          func(c *Config) { c.Hosts.Claude.Roles.Architect.Model = "claude-fable-5" },
		"disable host":   func(c *Config) { c.Hosts.Codex = nil },
	}
	for name, mutate := range mutations {
		t.Run(name, func(t *testing.T) {
			c := bothHostsConfig()
			mutate(c)
			got, err := Revision(c)
			if err != nil {
				t.Fatalf("Revision: %v", err)
			}
			if got == base {
				t.Errorf("Revision unchanged after mutating %s", name)
			}
		})
	}
}
