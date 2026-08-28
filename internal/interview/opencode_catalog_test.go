package interview

import (
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/kninetimmy/orch/internal/config"
	"github.com/kninetimmy/orch/internal/question"
)

func questionByID(t *testing.T, doc question.Document, id string) question.Question {
	t.Helper()
	for _, q := range doc.Questions {
		if q.ID == id {
			return q
		}
	}
	t.Fatalf("question %s not found in %+v", id, doc.Questions)
	return question.Question{}
}

func optionValuesOf(q question.Question) []string {
	values := make([]string, len(q.Options))
	for i, option := range q.Options {
		values[i] = option.Value
	}
	return values
}

func optionLabelOf(q question.Question, value string) string {
	for _, option := range q.Options {
		if option.Value == value {
			return option.Label
		}
	}
	return ""
}

func assertCatalogPagination(t *testing.T, q question.Question, want []string) {
	t.Helper()
	if q.FreeText {
		t.Errorf("%s requires free text", q.ID)
	}
	if q.Pagination == nil {
		t.Fatalf("%s has no pagination", q.ID)
	}
	for _, host := range []string{"claude", "codex", "opencode"} {
		if !slices.Contains(q.Pagination.Hosts, host) {
			t.Errorf("%s pagination hosts = %v, missing %s", q.ID, q.Pagination.Hosts, host)
		}
	}
	var got []string
	for _, page := range q.Pagination.Pages {
		if len(page.Options) < 2 || len(page.Options) > 3 {
			t.Errorf("%s page %d has %d options, want 2-3", q.ID, page.Index, len(page.Options))
		}
		for _, option := range page.Options {
			if option.Value != "" {
				got = append(got, option.Value)
			}
		}
	}
	if !slices.Equal(got, want) {
		t.Errorf("%s paginated models = %v, want each exactly once in order %v", q.ID, got, want)
	}
	if err := question.SpecCheck(q); err != nil {
		t.Errorf("SpecCheck(%s): %v", q.ID, err)
	}
}

func TestOpenCodeInitOffersEveryCatalogModelWithoutStaticFallbacks(t *testing.T) {
	facts := withOpenCodeTestCatalog(Facts{})
	doc, err := Next(facts, map[string]string{
		idHostClaudeEnabled: "no", idHostCodexEnabled: "no", idHostOpenCodeEnabled: "yes",
	}, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	q := questionByID(t, doc, roleModelID("opencode", "architect"))
	got := optionValuesOf(q)
	for _, model := range facts.OpenCodeCatalog.Models {
		if !slices.Contains(got, model.ID) {
			t.Errorf("model options %v omit catalog model %q", got, model.ID)
		}
	}
	if len(got) != len(facts.OpenCodeCatalog.Models) {
		t.Errorf("model options = %v, want exactly the %d catalog models", got, len(facts.OpenCodeCatalog.Models))
	}
	if slices.Contains(got, "opencode/x-preview-f-free") || q.FreeText {
		t.Errorf("catalog model question retained stale/manual choices: %+v", q)
	}
	if len(q.Options) <= 4 {
		t.Fatalf("fixture has %d options, want a catalog large enough to exercise native pagination", len(q.Options))
	}
	assertCatalogPagination(t, q, got)
}

func TestOpenCodeOneModelCatalogIsHostValidInEverySetupPath(t *testing.T) {
	facts := withOpenCodeTestCatalog(Facts{})
	facts.OpenCodeCatalog.Models = facts.OpenCodeCatalog.Models[:1]
	want := []string{facts.OpenCodeCatalog.Models[0].ID}

	initDoc, err := Next(facts, map[string]string{
		idHostClaudeEnabled: "no", idHostCodexEnabled: "no", idHostOpenCodeEnabled: "yes",
	}, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	assertCatalogPagination(t, questionByID(t, initDoc, roleModelID("opencode", "architect")), want)

	root := t.TempDir()
	writeCommittedConfigOpenCodeOnly(t, root)
	configureDoc, err := NextConfigure(facts, map[string]string{
		idPickHosts: "no", idPickRolesOpenCode: "yes", idPickSettings: "no",
	}, root)
	if err != nil {
		t.Fatal(err)
	}
	assertCatalogPagination(t, questionByID(t, configureDoc, roleModelID("opencode", "architect")), want)

	localDoc, err := NextConfigureLocalWithFacts(facts, map[string]string{idPickOpenCode: "yes", idPickSettings: "no"}, root)
	if err != nil {
		t.Fatal(err)
	}
	assertCatalogPagination(t, questionByID(t, localDoc, localRoleModelID("opencode", "architect")), want)
}

func TestOpenCodeVariantsFollowTheSelectedModel(t *testing.T) {
	facts := withOpenCodeTestCatalog(Facts{})
	answers := map[string]string{
		idHostClaudeEnabled: "no", idHostCodexEnabled: "no", idHostOpenCodeEnabled: "yes",
		roleModelID("opencode", "architect"): "anthropic/claude-sonnet-5",
	}
	doc, err := Next(facts, answers, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	q := questionByID(t, doc, roleVariantID("opencode", "architect"))
	if got, want := optionValuesOf(q), []string{noVariantAnswer, "fast", "max"}; !slices.Equal(got, want) {
		t.Errorf("variant options = %v, want %v", got, want)
	}
	if q.FreeText {
		t.Error("variant question admits an unadvertised free-text value")
	}
	assertCatalogPagination(t, q, []string{noVariantAnswer, "fast", "max"})

	answers[q.ID] = "xhigh"
	if _, err := Next(facts, answers, t.TempDir()); err == nil || !strings.Contains(err.Error(), "not one of") {
		t.Fatalf("unadvertised variant error = %v", err)
	}
}

func TestOpenCodeModelWithoutVariantsNeedsNoSuffixQuestion(t *testing.T) {
	facts := withOpenCodeTestCatalog(Facts{})
	root := t.TempDir()
	answers := map[string]string{
		idHostClaudeEnabled: "no", idHostCodexEnabled: "no", idHostOpenCodeEnabled: "yes",
	}
	for i := 0; i < 100; i++ {
		doc, err := Next(facts, answers, root)
		if err != nil {
			t.Fatal(err)
		}
		if doc.Kind == question.DocSummary {
			cfg, err := config.Parse([]byte(doc.Summary.ConfigTOML))
			if err != nil {
				t.Fatal(err)
			}
			for _, rs := range roleSpecs {
				profile := committedProfile(cfg.Hosts.OpenCode, rs.key)
				if profile.Model != "github-copilot/gpt-5-mini" || profile.EffectiveOpenCodeVariant() != "" {
					t.Errorf("%s profile = %+v, want bare github-copilot/gpt-5-mini", rs.key, profile)
				}
			}
			return
		}
		if doc.Kind != question.DocQuestions {
			t.Fatalf("unexpected document kind %q", doc.Kind)
		}
		for _, q := range doc.Questions {
			if strings.Contains(q.ID, "host.opencode.role") && strings.HasSuffix(q.ID, ".model") {
				if len(q.Options) != len(facts.OpenCodeCatalog.Models) {
					t.Errorf("%s offers %d models, want all %d", q.ID, len(q.Options), len(facts.OpenCodeCatalog.Models))
				}
				answers[q.ID] = "github-copilot/gpt-5-mini"
				continue
			}
			if strings.Contains(q.ID, "host.opencode.role") && strings.HasSuffix(q.ID, ".variant") {
				t.Fatalf("no-variant model unexpectedly produced question %s", q.ID)
			}
			answers[q.ID] = q.Default
		}
	}
	t.Fatal("OpenCode no-variant interview did not reach summary")
}

func TestConfigurePreservesOpenCodeRolesWithoutCatalogForUnrelatedEdit(t *testing.T) {
	root := t.TempDir()
	committed := writeCommittedConfigOpenCodeOnly(t, root)
	answers := map[string]string{}
	for i := 0; i < 100; i++ {
		doc, err := NextConfigure(Facts{}, answers, root)
		if err != nil {
			t.Fatal(err)
		}
		if doc.Kind == question.DocSummary {
			got, err := config.Parse([]byte(doc.Summary.ConfigTOML))
			if err != nil {
				t.Fatal(err)
			}
			if got.Hosts.OpenCode == nil || got.Hosts.OpenCode.Roles != committed.Hosts.OpenCode.Roles {
				t.Errorf("OpenCode roles changed without catalog: got %+v want %+v", got.Hosts.OpenCode, committed.Hosts.OpenCode)
			}
			if got.Merge.Strategy != "rebase" {
				t.Errorf("merge strategy = %q, want unrelated edit to land", got.Merge.Strategy)
			}
			return
		}
		for _, q := range doc.Questions {
			switch q.ID {
			case idPickSettings:
				answers[q.ID] = "yes"
			case idMergeStrategy:
				answers[q.ID] = "rebase"
			default:
				answers[q.ID] = q.Default
			}
		}
	}
	t.Fatal("configure did not reach summary")
}

func TestOpenCodeRoleEditsFailClearlyWithoutCatalog(t *testing.T) {
	root := t.TempDir()
	writeCommittedConfigOpenCodeOnly(t, root)
	facts := Facts{OpenCodeCLI: true, OpenCodeCatalogError: "safe discovery failure; retry"}
	_, err := Next(facts, map[string]string{
		idHostClaudeEnabled: "no", idHostCodexEnabled: "no", idHostOpenCodeEnabled: "yes",
	}, root)
	if !errors.Is(err, ErrOpenCodeCatalogUnavailable) || !strings.Contains(err.Error(), "safe discovery failure") {
		t.Fatalf("init catalog error = %v", err)
	}

	configureAnswers := map[string]string{idPickHosts: "no", idPickRolesOpenCode: "yes", idPickSettings: "no"}
	_, err = NextConfigure(facts, configureAnswers, root)
	if !errors.Is(err, ErrOpenCodeCatalogUnavailable) || !strings.Contains(err.Error(), "safe discovery failure") || !strings.Contains(err.Error(), "cannot be edited") {
		t.Fatalf("configure catalog error = %v", err)
	}

	_, err = NextConfigureLocal(map[string]string{idPickOpenCode: "yes", idPickSettings: "no"}, root)
	if !errors.Is(err, ErrOpenCodeCatalogUnavailable) || !strings.Contains(err.Error(), "opencode2") {
		t.Fatalf("configure-local catalog error = %v", err)
	}
}

func TestExistingOpenCodeModelIsTheOnlyNonCatalogOption(t *testing.T) {
	root := t.TempDir()
	committed := writeCommittedConfigOpenCodeOnly(t, root)
	committed.Hosts.OpenCode.Roles.Architect.Model = "legacy-provider/retired-model"
	committed.Hosts.OpenCode.Roles.Architect.Variant = "retired-variant"
	writeCommittedConfig(t, root, committed)

	facts := withOpenCodeTestCatalog(Facts{})
	answers := map[string]string{
		idPickHosts: "no", idPickRolesOpenCode: "yes", idPickSettings: "no",
	}
	doc, err := NextConfigure(facts, answers, root)
	if err != nil {
		t.Fatal(err)
	}
	q := questionByID(t, doc, roleModelID("opencode", "architect"))
	values := optionValuesOf(q)
	if !slices.Contains(values, "legacy-provider/retired-model") || slices.Contains(values, "opencode/x-preview-f-free") {
		t.Errorf("model options = %v, want live catalog plus only the committed stale model", values)
	}
	if q.Default != "legacy-provider/retired-model" {
		t.Errorf("default = %q, want committed model", q.Default)
	}
	if got, want := optionLabelOf(q, "legacy-provider/retired-model"), "legacy-provider/retired-model (committed, unavailable)"; got != want {
		t.Errorf("retained model label = %q, want %q", got, want)
	}

	for i := 0; i < 100; i++ {
		for _, q := range doc.Questions {
			answers[q.ID] = q.Default
		}
		doc, err = NextConfigure(facts, answers, root)
		if err != nil {
			t.Fatal(err)
		}
		if doc.Kind == question.DocSummary {
			got, err := config.Parse([]byte(doc.Summary.ConfigTOML))
			if err != nil {
				t.Fatal(err)
			}
			if profile := got.Hosts.OpenCode.Roles.Architect; profile.Model != "legacy-provider/retired-model" || profile.Variant != "retired-variant" {
				t.Errorf("stale committed selection changed: %+v", profile)
			}
			return
		}
	}
	t.Fatal("configure did not reach summary")
}

func TestUnavailableLegacyOpenCodeSelectionRemainsLoadable(t *testing.T) {
	root := t.TempDir()
	committed := writeCommittedConfigOpenCodeOnly(t, root)
	committed.Hosts.OpenCode.Roles.Architect = config.RoleProfile{Model: "retired-model", Effort: "xhigh"}
	writeCommittedConfig(t, root, committed)
	facts := withOpenCodeTestCatalog(Facts{})
	answers := map[string]string{idPickHosts: "no", idPickRolesOpenCode: "yes", idPickSettings: "no"}
	for i := 0; i < 100; i++ {
		doc, err := NextConfigure(facts, answers, root)
		if err != nil {
			t.Fatal(err)
		}
		if doc.Kind == question.DocSummary {
			got, err := config.Parse([]byte(doc.Summary.ConfigTOML))
			if err != nil {
				t.Fatal(err)
			}
			if profile := got.Hosts.OpenCode.Roles.Architect; profile.Model != "retired-model" || profile.Effort != "xhigh" || profile.Variant != "" {
				t.Errorf("legacy selection changed: %+v", profile)
			}
			return
		}
		for _, q := range doc.Questions {
			answers[q.ID] = q.Default
		}
	}
	t.Fatal("configure did not reach summary")
}

func TestConfigureLocalOffersEveryCatalogModel(t *testing.T) {
	root := t.TempDir()
	writeCommittedConfigOpenCodeOnly(t, root)
	writeLocalOverrideFile(t, root, `[hosts.opencode.roles.architect]
model = "opencode/x-preview-f-free"
`)
	facts := withOpenCodeTestCatalog(Facts{})
	doc, err := NextConfigureLocalWithFacts(facts, map[string]string{idPickOpenCode: "yes", idPickSettings: "no"}, root)
	if err != nil {
		t.Fatal(err)
	}
	q := questionByID(t, doc, localRoleModelID("opencode", "architect"))
	if len(q.Options) != len(facts.OpenCodeCatalog.Models) {
		t.Errorf("local model options = %v, want every catalog model exactly once", optionValuesOf(q))
	}
	if slices.Contains(optionValuesOf(q), "opencode/x-preview-f-free") || q.Default == "opencode/x-preview-f-free" {
		t.Errorf("local question offered stale non-committed model: %+v", q)
	}
}

func TestConfigureLocalClearsStaleModelToUnavailableCommittedProfile(t *testing.T) {
	profiles := map[string]config.RoleProfile{
		"native variant": {Model: "retired-provider/model-a", Variant: "provider-variant"},
		"legacy effort":  {Model: "retired-provider/model-a", Effort: "xhigh"},
	}
	for name, want := range profiles {
		t.Run(name, func(t *testing.T) {
			root := t.TempDir()
			committed := writeCommittedConfigOpenCodeOnly(t, root)
			committed.Hosts.OpenCode.Roles.Architect = want
			writeCommittedConfig(t, root, committed)
			writeLocalOverrideFile(t, root, `[hosts.opencode.roles.architect]
model = "retired-provider/model-b"
`)

			facts := withOpenCodeTestCatalog(Facts{})
			answers := map[string]string{idPickOpenCode: "yes", idPickSettings: "no"}
			for i := 0; i < 100; i++ {
				doc, err := NextConfigureLocalWithFacts(facts, answers, root)
				if err != nil {
					t.Fatal(err)
				}
				if doc.Kind == question.DocSummary {
					change := doc.Summary.Files[0]
					effective := committed
					if !change.Delete {
						effective, err = config.MergeLocal(committed, []byte(change.NewContent))
						if err != nil {
							t.Fatal(err)
						}
					}
					if got := effective.Hosts.OpenCode.Roles.Architect; got != want {
						t.Errorf("effective architect = %+v, want exact committed profile %+v", got, want)
					}
					if !change.Delete || change.NewContent != "" {
						t.Fatalf("stale override repair = %+v, want local file deletion inheriting %+v", change, want)
					}
					return
				}
				if doc.Kind != question.DocQuestions {
					t.Fatalf("unexpected document kind %q", doc.Kind)
				}
				for _, q := range doc.Questions {
					if q.ID == localRoleModelID("opencode", "architect") {
						if q.Default != want.Model || slices.Contains(optionValuesOf(q), "retired-provider/model-b") {
							t.Errorf("architect model question = %+v", q)
						}
						if got, label := optionLabelOf(q, want.Model), want.Model+" (committed, unavailable)"; got != label {
							t.Errorf("retained local model label = %q, want %q", got, label)
						}
					}
					answers[q.ID] = q.Default
				}
			}
			t.Fatal("configure-local did not reach summary")
		})
	}
}
