package opencode

import (
	"strings"
	"testing"

	"github.com/kninetimmy/orch/internal/config"
)

func TestCheckSelectionsReportsEveryUnavailableRole(t *testing.T) {
	roles := config.Roles{
		Architect:       config.RoleProfile{Model: "missing/architect", Variant: "deep"},
		Scout:           config.RoleProfile{Model: "available/missing"},
		Implementer:     config.RoleProfile{Model: "available/agent", Variant: "missing"},
		Specialist:      config.RoleProfile{Model: "available/agent", Variant: "fast"},
		Reviewer:        config.RoleProfile{Model: "available/agent"},
		ReviewDowngrade: config.RoleProfile{Model: "legacy-bare-model", Effort: "high"},
	}
	catalog := Catalog{Models: []Model{{ID: "available/agent", Variants: []string{"fast"}}}}
	checks := CheckSelections(roles, catalog)
	if len(checks) != 6 {
		t.Fatalf("checks = %d, want 6", len(checks))
	}
	wants := []string{"unavailable provider", "unavailable model", "unavailable variant", "", "", "unavailable model"}
	for i, want := range wants {
		if want == "" {
			if checks[i].Err != nil {
				t.Errorf("%s error = %v, want nil", checks[i].Role, checks[i].Err)
			}
			continue
		}
		if checks[i].Err == nil || !strings.Contains(checks[i].Err.Error(), want) || !strings.Contains(checks[i].Err.Error(), "this role") {
			t.Errorf("%s error = %v, want %q and role-specific repair", checks[i].Role, checks[i].Err, want)
		}
	}
}

func TestCheckSelectionsAcceptsMixedProvidersAndNoVariant(t *testing.T) {
	roles := config.Roles{
		Architect:       config.RoleProfile{Model: "openai/architect", Variant: "deep"},
		Scout:           config.RoleProfile{Model: "copilot/scout"},
		Implementer:     config.RoleProfile{Model: "local/team/implementer", Variant: "fast"},
		Specialist:      config.RoleProfile{Model: "openai/specialist"},
		Reviewer:        config.RoleProfile{Model: "copilot/reviewer", Variant: "thorough"},
		ReviewDowngrade: config.RoleProfile{Model: "local/reviewer-lite"},
	}
	catalog := Catalog{Models: []Model{
		{ID: "openai/architect", Variants: []string{"deep"}},
		{ID: "copilot/scout"},
		{ID: "local/team/implementer", Variants: []string{"fast"}},
		{ID: "openai/specialist"},
		{ID: "copilot/reviewer", Variants: []string{"thorough"}},
		{ID: "local/reviewer-lite"},
	}}
	for _, check := range CheckSelections(roles, catalog) {
		if check.Err != nil {
			t.Errorf("%s error = %v", check.Role, check.Err)
		}
	}
}
