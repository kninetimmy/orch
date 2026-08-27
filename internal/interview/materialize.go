package interview

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/kninetimmy/orch/internal/config"
	"github.com/kninetimmy/orch/internal/opencode"
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
// whitespace and must not newly type a near miss of a known model id
// (validateModelAnswer), and the concurrency value must parse as an
// integer >= 1.
// Once the struct is built, Revision computes its content hash, and
// then Render/Parse round-trip it — the exact artifact bootstrap would
// commit is proven loadable, and the real validate() enums (which
// config deliberately does not export) are enforced this way rather
// than duplicated here.
func materialize(answers map[string]string) (*config.Config, error) {
	return materializeWithCatalog(answers, opencode.Catalog{})
}

func materializeWithCatalog(answers map[string]string, catalog opencode.Catalog) (*config.Config, error) {
	cfg := &config.Config{SchemaVersion: 1}

	if answers[idHostClaudeEnabled] == "yes" {
		host, err := materializeHost("claude", answers, nil, catalog)
		if err != nil {
			return nil, err
		}
		cfg.Hosts.Claude = host
	}
	if answers[idHostCodexEnabled] == "yes" {
		host, err := materializeHost("codex", answers, nil, catalog)
		if err != nil {
			return nil, err
		}
		cfg.Hosts.Codex = host
	}
	if answers[idHostOpenCodeEnabled] == "yes" {
		host, err := materializeHost("opencode", answers, nil, catalog)
		if err != nil {
			return nil, err
		}
		cfg.Hosts.OpenCode = host
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

// materializeHost builds host's six-role config.Host from answers. Before
// optional OpenCode variants it wrote Effort for every host; after, every
// OpenCode role writes Variant (empty for noVariantAnswer), while Claude/Codex
// retain the same Effort path.
// current is host's committed profile set, so an answer left at a
// committed near-miss model still round-trips (validateModelAnswer);
// it is nil for init and for any host a session newly enables, neither
// of which has a committed profile to leave alone.
func materializeHost(host string, answers map[string]string, current *config.Host, catalog opencode.Catalog) (*config.Host, error) {
	var roles config.Roles
	for _, rs := range roleSpecs {
		modelID := roleModelID(host, rs.key)
		model := answers[modelID]
		if err := validateModelAnswer(modelID, model, currentModel(current, rs.key)); err != nil {
			return nil, err
		}
		profile := config.RoleProfile{Model: model}
		if host == "opencode" {
			variant, answered := answers[roleVariantID(host, rs.key)]
			switch {
			case answered:
				profile.Variant = variant
				if profile.Variant == noVariantAnswer {
					profile.Variant = ""
				}
			case current != nil:
				previous := committedProfile(current, rs.key)
				if previous.Model == model {
					if _, advertised := openCodeCatalogModel(catalog, model); !advertised {
						profile.Effort = previous.Effort
						profile.Variant = previous.Variant
					}
				}
			}
		} else {
			profile.Effort = answers[roleEffortID(host, rs.key)]
		}
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

// validateModelFreeText enforces the shape rule every model string
// must satisfy wherever one is ingested: non-empty after trimming, and
// no internal whitespace (an exact model version string never contains
// any). Deliberately not the near-miss rule — this is also the filter
// seedOverrides runs over a config.local.toml already on disk, which
// must keep whatever it already holds (validateModelAnswer).
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

// validateModelAnswer is validateModelFreeText plus the near-miss rule,
// and is what an answered model question goes through. current is the
// value that key already carries (which is what the question offered as
// its Default): answering it back is not new input, so a configuration
// written before this rule existed stays answerable — the interview
// that could repair it must not be the one thing that wedges on it.
func validateModelAnswer(id, value, current string) error {
	if err := validateModelFreeText(id, value); err != nil {
		return err
	}
	if value == current {
		return nil
	}
	if suggestions := nearMissModels(id, value); len(suggestions) > 0 {
		return fmt.Errorf("%w: %s: model %q looks like a shortened or mis-cased form of a known %s model; did you mean %s?",
			ErrBadAnswer, id, value, modelHost(id), strings.Join(suggestions, ", "))
	}
	return nil
}

// currentModel returns h's committed model for role, or "" when h is
// nil — a host with no committed profile at all.
func currentModel(h *config.Host, role string) string {
	if h == nil {
		return ""
	}
	return committedProfile(h, role).Model
}

// nearMissModels returns every known model id for id's host that
// carries value as a substring, case-insensitively — the did-you-mean
// set for a shortened form like "fable-5" or a mis-cased
// "Claude-Opus-5". The model domain stays otherwise open (routing
// treats ids as opaque strings): a value equal to a known id is not a
// near miss, and a value resembling no known id yields no suggestions
// and so passes.
func nearMissModels(id, value string) []string {
	known := hostLocalModels[modelHost(id)]
	lowered := strings.ToLower(value)
	var suggestions []string
	for _, model := range known {
		if model == value {
			return nil
		}
		if strings.Contains(model, lowered) {
			suggestions = append(suggestions, model)
		}
	}
	return suggestions
}

// modelHost returns the host name a model question id carries in its
// second dotted segment — "host.<host>.role.<role>.model" for init and
// `orch configure`, "hosts.<host>.roles.<role>.model" for
// `orch configure-local` — or "" for an id shaped like neither.
func modelHost(id string) string {
	parts := strings.Split(id, ".")
	if len(parts) < 2 {
		return ""
	}
	return parts[1]
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
