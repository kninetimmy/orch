package config

import (
	"errors"
	"fmt"
	"strings"

	"github.com/BurntSushi/toml"
)

var mergeStrategies = map[string]bool{"squash": true, "rebase": true, "merge-commit": true}

var memhubModes = map[string]bool{"required": true, "best-effort": true, "off": true}

// effortsByHost lists the reasoning-effort levels Claude and Codex accept
// (PRD §10 model tables; Codex CLI ReasoningEffort wire tokens,
// lowercase, no separators). "none" and "minimal" exist in the Codex
// enum but are deliberately excluded: no orch role should route below
// low. OpenCode is deliberately absent: its variants are model-specific,
// not members of a host-wide effort enum.
var effortsByHost = map[string]map[string]bool{
	"codex":  {"low": true, "medium": true, "high": true, "xhigh": true, "max": true, "ultra": true},
	"claude": {"low": true, "medium": true, "high": true, "xhigh": true, "max": true},
}

// legacyOpenCodeEfforts is exactly the v0.8.0 OpenCode effort domain. It is
// intentionally separate from effortsByHost: these values remain loadable as
// legacy aliases for same-named variants, but do not restrict native Variant.
var legacyOpenCodeEfforts = map[string]bool{"low": true, "medium": true, "high": true, "xhigh": true, "max": true}

// validateHostSpecificKeys rejects structurally decoded keys that do not
// belong to that host. RoleProfile is shared so BurntSushi/toml can decode all
// hosts without custom unmarshalling; metadata preserves which optional key was
// actually present, including an explicitly empty one.
func validateHostSpecificKeys(c *Config, md toml.MetaData) error {
	var problems []string
	for _, host := range []string{"claude", "codex"} {
		for _, role := range roleNames {
			if md.IsDefined("hosts", host, "roles", role, "variant") {
				problems = append(problems, fmt.Sprintf("hosts.%s.roles.%s.variant: only OpenCode roles accept model variants", host, role))
			}
		}
	}
	if c.Hosts.OpenCode != nil {
		for _, role := range roleNames {
			effort := md.IsDefined("hosts", "opencode", "roles", role, "effort")
			variant := md.IsDefined("hosts", "opencode", "roles", role, "variant")
			switch {
			case effort && variant:
				problems = append(problems, "hosts.opencode.roles."+role+": effort and variant cannot both be set")
			case effort && roleProfileOf(c.Hosts.OpenCode.Roles, role).Effort == "":
				problems = append(problems, "hosts.opencode.roles."+role+".effort: the legacy compatibility value must not be empty")
			}
		}
	}
	if len(problems) > 0 {
		return errors.New("invalid configuration:\n  - " + strings.Join(problems, "\n  - "))
	}
	return nil
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

	if c.Hosts.Codex == nil && c.Hosts.Claude == nil && c.Hosts.OpenCode == nil {
		fail("hosts: at least one of hosts.codex, hosts.claude, or hosts.opencode must be configured")
	}
	if c.Hosts.Codex != nil {
		validateHost("codex", c.Hosts.Codex, fail)
	}
	if c.Hosts.Claude != nil {
		validateHost("claude", c.Hosts.Claude, fail)
	}
	if c.Hosts.OpenCode != nil {
		validateHost("opencode", c.Hosts.OpenCode, fail)
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
		if name == "opencode" {
			validateOpenCodeProfile(prefix, r.profile, fail)
			continue
		}
		if r.profile.Variant != "" {
			fail("%s.variant: only OpenCode roles accept model variants", prefix)
		}
		if !effortsByHost[name][r.profile.Effort] {
			fail("%s.effort: %q is not one of %s", prefix, r.profile.Effort, effortList(name))
		}
	}
}

func validateOpenCodeProfile(prefix string, p RoleProfile, fail func(string, ...any)) {
	if p.Effort != "" {
		if p.Variant != "" {
			fail("%s: effort and variant cannot both be set", prefix)
		}
		if !legacyOpenCodeEfforts[p.Effort] {
			fail("%s.effort: %q is not one of %s (effort is the legacy v0.8.0 spelling; new configurations use optional variant)", prefix, p.Effort, legacyOpenCodeEffortList())
		}
		return
	}
	if strings.Contains(p.Model, "#") {
		fail("%s.model: %q must be a bare provider/model; put the OpenCode variant in variant", prefix, p.Model)
	}
	if strings.ContainsAny(p.Model, " \t\r\n") {
		fail("%s.model: %q must not contain whitespace", prefix, p.Model)
	}
	provider, model, ok := strings.Cut(p.Model, "/")
	if !ok || provider == "" || model == "" || strings.Contains(model, "/") {
		fail("%s.model: %q must be an exact provider/model", prefix, p.Model)
	}
	if strings.ContainsAny(p.Variant, "# \t\r\n") {
		fail("%s.variant: %q must be one model-specific variant token", prefix, p.Variant)
	}
}

func effortList(host string) string {
	if host == "claude" {
		return "low, medium, high, xhigh, max"
	}
	return "low, medium, high, xhigh, max, ultra"
}

func legacyOpenCodeEffortList() string { return "low, medium, high, xhigh, max" }
