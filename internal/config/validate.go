package config

import (
	"errors"
	"fmt"
	"strings"
)

var mergeStrategies = map[string]bool{"squash": true, "rebase": true, "merge-commit": true}

var memhubModes = map[string]bool{"required": true, "best-effort": true, "off": true}

// effortsByHost lists the reasoning-effort levels each host accepts
// (PRD §10 model tables).
var effortsByHost = map[string]map[string]bool{
	"codex":  {"low": true, "medium": true, "high": true},
	"claude": {"low": true, "medium": true, "high": true, "xhigh": true},
}

// validate collects every violation and reports them together in one
// error; it never stops at the first problem.
func (c *Config) validate() error {
	var problems []string
	fail := func(format string, args ...any) {
		problems = append(problems, fmt.Sprintf(format, args...))
	}

	if c.SchemaVersion != 1 {
		fail("schema_version: unsupported version %d (this build supports 1)", c.SchemaVersion)
	}
	if c.ConfigRevision == "" {
		fail("config_revision: must be a non-empty revision identifier")
	}
	if c.Concurrency.MaxSubagents < 1 {
		fail("concurrency.max_subagents: must be >= 1, got %d", c.Concurrency.MaxSubagents)
	}
	if !mergeStrategies[c.Merge.Strategy] {
		fail("merge.strategy: %q is not one of squash, rebase, merge-commit", c.Merge.Strategy)
	}
	if !memhubModes[c.Memhub.Mode] {
		fail("memhub.mode: %q is not one of required, best-effort, off", c.Memhub.Mode)
	}

	if c.Hosts.Codex == nil && c.Hosts.Claude == nil {
		fail("hosts: at least one of hosts.codex or hosts.claude must be configured")
	}
	if c.Hosts.Codex != nil {
		validateHost("codex", c.Hosts.Codex, fail)
	}
	if c.Hosts.Claude != nil {
		validateHost("claude", c.Hosts.Claude, fail)
	}

	if len(problems) > 0 {
		return errors.New("invalid configuration:\n  - " + strings.Join(problems, "\n  - "))
	}
	return nil
}

func validateHost(name string, h *Host, fail func(string, ...any)) {
	roles := []struct {
		key     string
		profile RoleProfile
	}{
		{"architect", h.Roles.Architect},
		{"scout", h.Roles.Scout},
		{"implementer", h.Roles.Implementer},
		{"specialist", h.Roles.Specialist},
		{"reviewer", h.Roles.Reviewer},
		{"review_downgrade", h.Roles.ReviewDowngrade},
	}
	for _, r := range roles {
		prefix := "hosts." + name + ".roles." + r.key
		if r.profile.Model == "" {
			fail("%s.model: must be an exact model version", prefix)
		}
		if !effortsByHost[name][r.profile.Effort] {
			fail("%s.effort: %q is not one of %s", prefix, r.profile.Effort, effortList(name))
		}
	}
}

func effortList(host string) string {
	if host == "claude" {
		return "low, medium, high, xhigh"
	}
	return "low, medium, high"
}
