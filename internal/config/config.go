// Package config parses and validates the committed Orch configuration
// at .orchestrator/config.toml (PRD §17). Machine-local overrides in
// config.local.toml are detected but not yet applied.
package config

// Config is the root of the committed configuration schema.
type Config struct {
	SchemaVersion  int         `toml:"schema_version"`
	ConfigRevision string      `toml:"config_revision"`
	Concurrency    Concurrency `toml:"concurrency"`
	Merge          Merge       `toml:"merge"`
	Memhub         Memhub      `toml:"memhub"`
	Metrics        Metrics     `toml:"metrics"`
	Hosts          Hosts       `toml:"hosts"`
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
	Codex  *Host `toml:"codex"`
	Claude *Host `toml:"claude"`
}

// Host is a per-host model/effort profile (PRD §10).
type Host struct {
	Roles Roles `toml:"roles"`
}

// Roles maps every PRD §9 role, plus the safe review downgrade, to an
// exact model and effort. All six are required for an enabled host.
type Roles struct {
	Architect       RoleProfile `toml:"architect"`
	Scout           RoleProfile `toml:"scout"`
	Implementer     RoleProfile `toml:"implementer"`
	Specialist      RoleProfile `toml:"specialist"`
	Reviewer        RoleProfile `toml:"reviewer"`
	ReviewDowngrade RoleProfile `toml:"review_downgrade"`
}

// RoleProfile pins an exact model version and reasoning effort.
type RoleProfile struct {
	Model  string `toml:"model"`
	Effort string `toml:"effort"`
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
	return names
}
