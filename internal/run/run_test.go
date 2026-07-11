package run

import (
	"github.com/kninetimmy/orch/internal/config"
)

// testConfig returns a minimal valid configuration with one host
// (claude) enabled, matching the PRD §10 default model profiles.
func testConfig() *config.Config {
	return &config.Config{
		SchemaVersion:  1,
		ConfigRevision: "r1",
		Concurrency:    config.Concurrency{MaxSubagents: 3},
		Merge:          config.Merge{Strategy: "squash"},
		Memhub:         config.Memhub{Mode: "off"},
		Hosts: config.Hosts{
			Claude: &config.Host{Roles: config.Roles{
				Architect:       config.RoleProfile{Model: "claude-opus-4-8", Effort: "xhigh"},
				Scout:           config.RoleProfile{Model: "claude-sonnet-5", Effort: "low"},
				Implementer:     config.RoleProfile{Model: "claude-sonnet-5", Effort: "xhigh"},
				Specialist:      config.RoleProfile{Model: "claude-opus-4-8", Effort: "high"},
				Reviewer:        config.RoleProfile{Model: "claude-opus-4-8", Effort: "high"},
				ReviewDowngrade: config.RoleProfile{Model: "claude-sonnet-5", Effort: "high"},
			}},
		},
	}
}

// testConfigTwoHosts adds an enabled codex host to testConfig, for
// denylist/host-selection tests that need more than one host.
func testConfigTwoHosts() *config.Config {
	cfg := testConfig()
	cfg.Hosts.Codex = &config.Host{Roles: config.Roles{
		Architect:       config.RoleProfile{Model: "gpt-5.6-sol", Effort: "high"},
		Scout:           config.RoleProfile{Model: "gpt-5.6-terra", Effort: "low"},
		Implementer:     config.RoleProfile{Model: "gpt-5.6-terra", Effort: "high"},
		Specialist:      config.RoleProfile{Model: "gpt-5.6-sol", Effort: "medium"},
		Reviewer:        config.RoleProfile{Model: "gpt-5.6-sol", Effort: "medium"},
		ReviewDowngrade: config.RoleProfile{Model: "gpt-5.6-terra", Effort: "high"},
	}}
	return cfg
}

// validPlanJSON is a minimal single-issue plan that passes Validate
// against testConfig().
func validPlanJSON() string {
	return `{
  "schema_version": 1,
  "host": "claude",
  "title": "Fix status lock",
  "issues": [
    {
      "id": "fix-status-lock",
      "title": "Fix the status lock race",
      "objective": "Make status reporting race-free",
      "acceptance_criteria": ["no data race under -race"],
      "type": "bug",
      "facts": {"read_only": false},
      "wave": 1,
      "required_tests": ["go test ./..."],
      "usage_class": "light"
    }
  ]
}`
}

// twoIssuePlanJSON is a two-issue, two-wave plan where "b" depends on
// "a", passing Validate against testConfig().
func twoIssuePlanJSON() string {
	return `{
  "schema_version": 1,
  "host": "claude",
  "title": "Two issue plan",
  "issues": [
    {
      "id": "a",
      "title": "Issue A",
      "objective": "Do A",
      "acceptance_criteria": ["A works"],
      "type": "feature",
      "facts": {"read_only": false},
      "wave": 1,
      "required_tests": ["go test ./..."],
      "usage_class": "light"
    },
    {
      "id": "b",
      "title": "Issue B",
      "objective": "Do B",
      "acceptance_criteria": ["B works"],
      "type": "feature",
      "facts": {"read_only": false, "risk_domains": ["concurrency"]},
      "depends_on": ["a"],
      "wave": 2,
      "required_tests": ["go test ./..."],
      "usage_class": "medium"
    }
  ]
}`
}
