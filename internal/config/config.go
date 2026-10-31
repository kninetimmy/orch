// Package config parses and validates the committed Orch configuration
// at .orchestrator/config.toml (PRD §17), then layers on the
// machine-local override file at config.local.toml when present. The
// overlay is restricted to a closed set of preference keys; any
// policy-bearing key set locally is rejected (see overlay.go).
package config

import "fmt"

// Config is the root of the committed configuration schema, after any
// machine-local overlay has been merged in.
type Config struct {
	SchemaVersion  int         `toml:"schema_version"`
	ConfigRevision string      `toml:"config_revision"`
	Concurrency    Concurrency `toml:"concurrency"`
	Merge          Merge       `toml:"merge"`
	Memhub         Memhub      `toml:"memhub"`
	Metrics        Metrics     `toml:"metrics"`
	Hosts          Hosts       `toml:"hosts"`

	// Protected is the repository's own grant-protected path list, added to
	// the floor every repository gets (grant.Protected). Nil when the
	// committed file has no [protected] section; it is omitted from the
	// revision then, so pre-#397 revisions are unchanged.
	Protected *Protected `toml:"protected" json:",omitempty"`

	// Overrides lists, sorted, the dotted keys applied from
	// config.local.toml. It is the audit surface for PRD §23: which
	// exact model/execution-profile values are in effect must always be visible.
	// Empty (nil) when no local override file exists or it sets
	// nothing. Never itself read from either TOML file.
	Overrides []string `toml:"-"`
}

// SurveyMethodologyVersion is the version of the survey methodology this
// binary uses to propose a repository's protected list. A grant refuses to
// start or merge under a list approved under any other version.
const SurveyMethodologyVersion = 2

// Protected is the committed [protected] section: the survey methodology
// version it was approved under and its paths, each with a one-line reason.
// It is policy: config.local.toml can set none of it.
type Protected struct {
	MethodologyVersion int             `toml:"methodology_version"`
	Paths              []ProtectedPath `toml:"paths"`
}

// ProtectedPath is one repository-relative path a grant can never merge a
// change to; a directory covers everything under it.
type ProtectedPath struct {
	Path   string `toml:"path"`
	Reason string `toml:"reason"`
}

// CheckGrantProtected returns why no grant may start or merge under c: it
// has no [protected] section, or one approved under another survey
// methodology version than this binary's. Nil when the section is usable,
// including one with no paths.
func (c *Config) CheckGrantProtected() error {
	switch {
	case c.Protected == nil:
		return fmt.Errorf("%s has no [protected] section, so this repository has no approved grant-protected path list; run /orch:configure to survey and approve one", Path)
	case c.Protected.MethodologyVersion != SurveyMethodologyVersion:
		return fmt.Errorf("%s's [protected] section was approved under survey methodology version %d, not this binary's version %d; run /orch:configure to re-survey and approve it", Path, c.Protected.MethodologyVersion, SurveyMethodologyVersion)
	}
	return nil
}

// Concurrency caps concurrent subagents (PRD §14; default 3).
type Concurrency struct {
	MaxSubagents int `toml:"max_subagents"`
}

// Merge selects the repository merge strategy (PRD §16; default squash).
type Merge struct {
	Strategy string `toml:"strategy"`
}

// Memhub selects the integration mode (PRD §20). There is no default:
// the mode is policy-bearing and must be chosen explicitly.
type Memhub struct {
	Mode string `toml:"mode"`
}

// Metrics enables optional local metrics (PRD §21; off by default).
type Metrics struct {
	Enabled bool `toml:"enabled"`
}

// Hosts holds one profile per enabled host. A nil host is not enabled.
type Hosts struct {
	Codex    *Host `toml:"codex"`
	Claude   *Host `toml:"claude"`
	OpenCode *Host `toml:"opencode"`
}

// Host is a per-host role profile (PRD §10).
type Host struct {
	Roles Roles `toml:"roles"`
}

// Roles maps every PRD §9 role, plus the safe review downgrade, to an
// exact model and execution profile. All six are required for an enabled host.
type Roles struct {
	Architect       RoleProfile `toml:"architect"`
	Scout           RoleProfile `toml:"scout"`
	Implementer     RoleProfile `toml:"implementer"`
	Specialist      RoleProfile `toml:"specialist"`
	Reviewer        RoleProfile `toml:"reviewer"`
	ReviewDowngrade RoleProfile `toml:"review_downgrade"`
}

// RoleProfile pins an exact model version and its host-native execution
// profile. Claude and Codex require Effort and never accept Variant. OpenCode
// uses optional Variant; an empty Variant means the bare provider/model. Effort
// remains accepted for OpenCode solely as the v0.8.0 compatibility spelling
// and has the same effective meaning as Variant.
type RoleProfile struct {
	Model   string `toml:"model"`
	Effort  string `toml:"effort"`
	Variant string `toml:"variant" json:",omitempty"`
}

// EffectiveOpenCodeVariant returns the variant an OpenCode role selects,
// interpreting v0.8.0 Effort as the compatibility spelling. This rule applies
// to every RoleProfile used under hosts.opencode and to no Claude/Codex role.
func (p RoleProfile) EffectiveOpenCodeVariant() string {
	if p.Effort != "" {
		return p.Effort
	}
	return p.Variant
}

// EnabledHosts returns the names of the configured hosts in stable order.
func (c *Config) EnabledHosts() []string {
	var names []string
	if c.Hosts.Claude != nil {
		names = append(names, "claude")
	}
	if c.Hosts.Codex != nil {
		names = append(names, "codex")
	}
	if c.Hosts.OpenCode != nil {
		names = append(names, "opencode")
	}
	return names
}

// Host returns the configured profile for name, or nil when it is disabled.
func (c *Config) Host(name string) *Host {
	switch name {
	case "claude":
		return c.Hosts.Claude
	case "codex":
		return c.Hosts.Codex
	case "opencode":
		return c.Hosts.OpenCode
	default:
		return nil
	}
}
