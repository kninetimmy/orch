package opencode

import (
	"fmt"
	"slices"
	"strings"

	"github.com/kninetimmy/orch/internal/config"
)

// SelectionCheck reports whether one configured OpenCode role is present in
// the project-scoped live catalog.
type SelectionCheck struct {
	Role string
	Err  error
}

// CheckSelections checks all six configured roles in configuration order. It
// never includes other catalog entries in diagnostics.
func CheckSelections(roles config.Roles, catalog Catalog) []SelectionCheck {
	profiles := []struct {
		role    string
		profile config.RoleProfile
	}{
		{"architect", roles.Architect},
		{"scout", roles.Scout},
		{"implementer", roles.Implementer},
		{"specialist", roles.Specialist},
		{"reviewer", roles.Reviewer},
		{"review_downgrade", roles.ReviewDowngrade},
	}
	checks := make([]SelectionCheck, len(profiles))
	for i, item := range profiles {
		checks[i] = SelectionCheck{Role: item.role, Err: checkSelection(item.role, item.profile, catalog)}
	}
	return checks
}

func checkSelection(role string, profile config.RoleProfile, catalog Catalog) error {
	path := "hosts.opencode.roles." + role
	variant := profile.EffectiveOpenCodeVariant()
	provider, _, exact := strings.Cut(profile.Model, "/")
	providerFound := false
	var found *Model
	for i := range catalog.Models {
		modelProvider, _, _ := strings.Cut(catalog.Models[i].ID, "/")
		if exact && modelProvider == provider {
			providerFound = true
		}
		if catalog.Models[i].ID == profile.Model {
			found = &catalog.Models[i]
			break
		}
	}

	repair := "run `orch configure` or `orch configure-local` to choose an available OpenCode selection for this role"
	switch {
	case found == nil && exact && !providerFound:
		return fmt.Errorf("%s selects unavailable provider %q for model %q; %s", path, provider, profile.Model, repair)
	case found == nil:
		return fmt.Errorf("%s selects unavailable model %q; %s", path, profile.Model, repair)
	case variant != "" && !slices.Contains(found.Variants, variant):
		return fmt.Errorf("%s selects unavailable variant %q for model %q; %s", path, variant, profile.Model, repair)
	default:
		return nil
	}
}
