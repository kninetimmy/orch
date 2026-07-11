package interview

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/kninetimmy/orch/internal/config"
)

// materialize turns a complete answer set into a validated, revisioned
// *config.Config. answers must already carry a legal value for every
// question buildSequence's fully-known sequence produces — Next
// guarantees this by validating every answer against
// question.ValidateAnswer before ever calling materialize.
//
// Free-text answers get one more ingestion-time check materialize
// itself owns, beyond question.ValidateAnswer's generic
// membership/non-blank contract: a model string must contain no
// whitespace, and the concurrency value must parse as an integer >= 1.
// Once the struct is built, Revision computes its content hash, and
// then Render/Parse round-trip it — the exact artifact bootstrap would
// commit is proven loadable, and the real validate() enums (which
// config deliberately does not export) are enforced this way rather
// than duplicated here.
func materialize(answers map[string]string) (*config.Config, error) {
	cfg := &config.Config{SchemaVersion: 1}

	if answers[idHostClaudeEnabled] == "yes" {
		host, err := materializeHost("claude", answers)
		if err != nil {
			return nil, err
		}
		cfg.Hosts.Claude = host
	}
	if answers[idHostCodexEnabled] == "yes" {
		host, err := materializeHost("codex", answers)
		if err != nil {
			return nil, err
		}
		cfg.Hosts.Codex = host
	}

	n, err := parseConcurrency(answers[idMaxSubagents])
	if err != nil {
		return nil, err
	}
	cfg.Concurrency.MaxSubagents = n
	cfg.Merge.Strategy = answers[idMergeStrategy]
	cfg.Memhub.Mode = answers[idMemhubMode]
	cfg.Metrics.Enabled = answers[idMetricsEnabled] == "yes"

	rev, err := config.Revision(cfg)
	if err != nil {
		return nil, fmt.Errorf("compute configuration revision: %w", err)
	}
	cfg.ConfigRevision = rev

	rendered, err := config.Render(cfg)
	if err != nil {
		return nil, fmt.Errorf("render generated configuration: %w", err)
	}
	if _, err := config.Parse(rendered); err != nil {
		return nil, fmt.Errorf("materialized configuration failed its own round-trip check: %w", err)
	}

	return cfg, nil
}

// materializeHost builds host's six-role config.Host from answers.
func materializeHost(host string, answers map[string]string) (*config.Host, error) {
	var roles config.Roles
	for _, rs := range roleSpecs {
		modelID := roleModelID(host, rs.key)
		model := answers[modelID]
		if err := validateModelFreeText(modelID, model); err != nil {
			return nil, err
		}
		profile := config.RoleProfile{Model: model, Effort: answers[roleEffortID(host, rs.key)]}
		setRoleProfile(&roles, rs.key, profile)
	}
	return &config.Host{Roles: roles}, nil
}

// setRoleProfile writes p onto r's field for role.
func setRoleProfile(r *config.Roles, role string, p config.RoleProfile) {
	switch role {
	case "architect":
		r.Architect = p
	case "scout":
		r.Scout = p
	case "implementer":
		r.Implementer = p
	case "specialist":
		r.Specialist = p
	case "reviewer":
		r.Reviewer = p
	case "review_downgrade":
		r.ReviewDowngrade = p
	}
}

// validateModelFreeText enforces the free-text ingestion rule for a
// model answer: non-empty after trimming, and no internal whitespace
// (an exact model version string never contains any).
func validateModelFreeText(id, value string) error {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return fmt.Errorf("%w: %s: model must not be empty", ErrBadAnswer, id)
	}
	if strings.ContainsAny(value, " \t\n\r") {
		return fmt.Errorf("%w: %s: model %q must not contain whitespace", ErrBadAnswer, id, value)
	}
	return nil
}

// parseConcurrency enforces the free-text ingestion rule for
// concurrency.max_subagents: an integer >= 1.
func parseConcurrency(value string) (int, error) {
	n, err := strconv.Atoi(strings.TrimSpace(value))
	if err != nil || n < 1 {
		return 0, fmt.Errorf("%w: %s: %q is not an integer >= 1", ErrBadAnswer, idMaxSubagents, value)
	}
	return n, nil
}
