package interview

import (
	"errors"
	"maps"
	"reflect"
	"strings"
	"testing"

	"github.com/kninetimmy/orch/internal/config"
	"github.com/kninetimmy/orch/internal/opencode"
	"github.com/kninetimmy/orch/internal/question"
)

var wantDeepSeekProfiles = map[string]config.RoleProfile{
	"architect":        {Model: "opencode-go/deepseek-v4.1-flash", Variant: "max"},
	"scout":            {Model: "opencode-go/glm-5.3-flash", Variant: "high"},
	"implementer":      {Model: "openai/gpt-6.1-sol", Variant: "high"},
	"specialist":       {Model: "openai/gpt-6.1-sol", Variant: "xhigh"},
	"reviewer":         {Model: "openai/gpt-6.1-sol", Variant: "xhigh"},
	"review_downgrade": {Model: "opencode-go/deepseek-v4.1-flash", Variant: "max"},
}

func deepSeekFacts() Facts {
	f := withOpenCodeTestCatalog(Facts{})
	f.OpenCodeCatalog.Models = append(f.OpenCodeCatalog.Models,
		opencode.Model{ID: "opencode-go/deepseek-v4.1-flash", Variants: []string{"max"}},
		opencode.Model{ID: "opencode-go/glm-5.3-flash", Variants: []string{"high"}},
		opencode.Model{ID: "openai/gpt-6.1-sol", Variants: []string{"high", "xhigh"}},
	)
	return f
}

// Exercise the same stateless sequence with each interview's real on-disk seeds.
func presetInterview(t *testing.T, kind string) (func(Facts, map[string]string) (question.Document, error), map[string]string, *config.Config) {
	t.Helper()
	root := t.TempDir()
	if kind == "init" {
		return func(f Facts, a map[string]string) (question.Document, error) { return Next(f, a, root) }, map[string]string{
			idHostClaudeEnabled: "no", idHostCodexEnabled: "no", idHostOpenCodeEnabled: "yes",
		}, nil
	}
	cfg := writeCommittedConfigLocal(t, root)
	open := writeCommittedConfigOpenCodeOnly(t, t.TempDir())
	cfg.Hosts.OpenCode = open.Hosts.OpenCode
	writeCommittedConfig(t, root, cfg)
	if kind == "configure" {
		return func(f Facts, a map[string]string) (question.Document, error) { return NextConfigure(f, a, root) }, map[string]string{
			idPickHosts: "no", idPickRolesClaude: "no", idPickRolesCodex: "no", idPickRolesOpenCode: "yes", idPickSettings: "no", idPickProtected: "no",
		}, cfg
	}
	writeLocalOverrideFile(t, root, `[hosts.opencode.roles.architect]
model = "anthropic/claude-sonnet-5"
variant = "fast"
[hosts.codex.roles.scout]
model = "custom-codex"
[concurrency]
max_subagents = 7
`)
	return func(f Facts, a map[string]string) (question.Document, error) {
		return NextConfigureLocalWithFacts(f, a, root)
	}, map[string]string{idPickClaude: "no", idPickCodex: "no", idPickOpenCode: "yes", idPickSettings: "no"}, cfg
}

func walkPresetSummary(t *testing.T, next func(Facts, map[string]string) (question.Document, error), f Facts, a map[string]string, customize func(question.Question) string) question.Document {
	t.Helper()
	for range 100 {
		doc, err := next(f, a)
		if err != nil {
			t.Fatal(err)
		}
		if doc.Kind == question.DocSummary {
			return doc
		}
		if doc.Kind != question.DocQuestions {
			t.Fatalf("unexpected document %s", doc.Kind)
		}
		for _, q := range doc.Questions {
			if err := question.SpecCheck(q); err != nil {
				t.Fatal(err)
			}
			a[q.ID] = q.Default
			if customize != nil {
				if v := customize(q); v != "" {
					a[q.ID] = v
				}
			}
		}
	}
	t.Fatal("interview did not reach summary")
	return question.Document{}
}

func presetSummaryConfig(t *testing.T, kind string, doc question.Document, committed *config.Config) *config.Config {
	t.Helper()
	var cfg *config.Config
	var err error
	if kind == "configure-local" {
		cfg, err = config.MergeLocal(committed, []byte(doc.Summary.ConfigTOML))
	} else {
		cfg, err = config.Parse([]byte(doc.Summary.ConfigTOML))
	}
	if err != nil {
		t.Fatal(err)
	}
	return cfg
}

func TestOpenCodeDeepSeekPresetInterviews(t *testing.T) {
	for _, kind := range []string{"init", "configure", "configure-local"} {
		for _, choice := range []string{"yes", "no", "customize"} {
			t.Run(kind+"/"+choice, func(t *testing.T) {
				next, answers, committed := presetInterview(t, kind)
				facts := deepSeekFacts()
				doc, err := next(facts, answers)
				if err != nil {
					t.Fatal(err)
				}
				q := questionByID(t, doc, idOpenCodeDeepSeekPreset)
				if q.Default != "no" || !reflect.DeepEqual(optionValuesOf(q), []string{"yes", "no"}) {
					t.Fatalf("preset is not explicit opt-in: %+v", q)
				}
				answers[q.ID] = "yes"
				if choice == "no" {
					answers[q.ID] = "no"
				}
				doc = walkPresetSummary(t, next, facts, answers, func(q question.Question) string {
					if choice == "customize" && strings.HasSuffix(q.ID, ".scout.model") {
						return "github-copilot/gpt-5-mini"
					}
					if choice == "customize" && strings.HasSuffix(q.ID, ".implementer.variant") {
						return "xhigh"
					}
					return ""
				})
				cfg := presetSummaryConfig(t, kind, doc, committed)
				for _, rs := range roleSpecs {
					want := wantDeepSeekProfiles[rs.key]
					if choice == "customize" && rs.key == "scout" {
						want = config.RoleProfile{Model: "github-copilot/gpt-5-mini"}
					}
					if choice == "customize" && rs.key == "implementer" {
						want.Variant = "xhigh"
					}
					if choice == "no" {
						p := defaultProfiles["opencode"][rs.key]
						want = config.RoleProfile{Model: p.model, Variant: p.execution}
						if committed != nil {
							want = committedProfile(committed.Hosts.OpenCode, rs.key)
						}
						if kind == "configure-local" && rs.key == "architect" {
							want = config.RoleProfile{Model: "anthropic/claude-sonnet-5", Variant: "fast"}
						}
					}
					if got := committedProfile(cfg.Hosts.OpenCode, rs.key); got != want {
						t.Errorf("%s = %+v, want %+v", rs.key, got, want)
					}
				}
				if committed != nil {
					want := deepCopyConfig(committed)
					if kind == "configure-local" {
						want.Hosts.Codex.Roles.Scout.Model = "custom-codex"
						want.Concurrency.MaxSubagents = 7
					}
					cfg.Hosts.OpenCode, want.Hosts.OpenCode = nil, nil
					cfg.ConfigRevision, want.ConfigRevision = "", ""
					cfg.Overrides, want.Overrides = nil, nil
					if !reflect.DeepEqual(cfg, want) {
						t.Errorf("unrelated configuration changed: got %+v, want %+v", cfg, want)
					}
				}
				t.Logf("proposed configuration:\n%s", doc.Summary.ConfigTOML)
				if choice == "no" && kind != "init" {
					return // Existing no-change blockers remain in force.
				}
				if len(doc.Summary.Blockers) != 0 || len(doc.Questions) != 1 || doc.Questions[0].ID != idApproval {
					t.Fatalf("approval boundary = %+v", doc)
				}
				for _, approval := range []string{"abort", "approve"} {
					answers[idApproval] = approval
					got, err := next(facts, answers)
					if err != nil {
						t.Fatal(err)
					}
					want := question.DocAborted
					if approval == "approve" {
						want = question.DocComplete
						if got.Complete == nil || got.Complete.Summary.ConfigTOML != doc.Summary.ConfigTOML {
							t.Fatal("approved configuration differs from proposal")
						}
					}
					if got.Kind != want {
						t.Fatalf("approval %s: %s, want %s", approval, got.Kind, want)
					}
				}
			})
		}
	}
}

func TestOpenCodeDeepSeekPresetConfigureNewHost(t *testing.T) {
	root := t.TempDir()
	writeCommittedConfigLocal(t, root)
	answers := map[string]string{
		idPickHosts: "yes", idPickRolesClaude: "no", idPickRolesCodex: "no", idPickSettings: "no", idPickProtected: "no",
		idHostClaudeEnabled: "yes", idHostCodexEnabled: "yes", idHostOpenCodeEnabled: "yes",
	}
	next := func(f Facts, a map[string]string) (question.Document, error) { return NextConfigure(f, a, root) }
	doc, err := next(deepSeekFacts(), answers)
	if err != nil {
		t.Fatal(err)
	}
	questionByID(t, doc, idOpenCodeDeepSeekPreset)
	answers[idOpenCodeDeepSeekPreset] = "yes"
	doc = walkPresetSummary(t, next, deepSeekFacts(), answers, nil)
	cfg := presetSummaryConfig(t, "configure", doc, nil)
	for role, want := range wantDeepSeekProfiles {
		if got := committedProfile(cfg.Hosts.OpenCode, role); got != want {
			t.Errorf("%s = %+v, want %+v", role, got, want)
		}
	}
}

func TestOpenCodeDeepSeekPresetRequiresCompleteCurrentCatalog(t *testing.T) {
	mutations := map[string]func(*Facts){
		"missing deepseek": func(f *Facts) {
			f.OpenCodeCatalog.Models = append(f.OpenCodeCatalog.Models[:6:6], f.OpenCodeCatalog.Models[7:]...)
		},
		"missing glm": func(f *Facts) {
			f.OpenCodeCatalog.Models = append(f.OpenCodeCatalog.Models[:7:7], f.OpenCodeCatalog.Models[8:]...)
		},
		"missing sol":      func(f *Facts) { f.OpenCodeCatalog.Models = f.OpenCodeCatalog.Models[:8] },
		"missing max":      func(f *Facts) { f.OpenCodeCatalog.Models[6].Variants = nil },
		"missing high":     func(f *Facts) { f.OpenCodeCatalog.Models[8].Variants = []string{"xhigh"} },
		"missing xhigh":    func(f *Facts) { f.OpenCodeCatalog.Models[8].Variants = []string{"high"} },
		"missing glm high": func(f *Facts) { f.OpenCodeCatalog.Models[7].Variants = nil },
		"empty":            func(f *Facts) { f.OpenCodeCatalog.Models = []opencode.Model{} },
		"unavailable": func(f *Facts) {
			f.OpenCodeCatalog = opencode.Catalog{}
			f.OpenCodeCatalogError = "discovery failed; retry"
		},
	}
	for _, kind := range []string{"init", "configure", "configure-local"} {
		for name, mutate := range mutations {
			t.Run(kind+"/"+name, func(t *testing.T) {
				next, initial, _ := presetInterview(t, kind)
				bad := deepSeekFacts()
				mutate(&bad)
				selected := maps.Clone(initial)
				selected[idOpenCodeDeepSeekPreset] = "yes"
				if _, err := next(bad, selected); err == nil || (!strings.Contains(err.Error(), "current project catalog") && !errors.Is(err, ErrOpenCodeCatalogUnavailable)) {
					t.Fatalf("new preset selection error = %v", err)
				}
				// Even customizing away every preset model cannot waive its
				// catalog-completeness requirement at proposal or application.
				walkPresetSummary(t, next, deepSeekFacts(), selected, func(q question.Question) string {
					if strings.HasSuffix(q.ID, ".model") {
						return "github-copilot/gpt-5-mini"
					}
					return ""
				})
				for _, approved := range []bool{false, true} {
					if approved {
						selected[idApproval] = "approve"
					}
					if _, err := next(bad, selected); err == nil {
						t.Fatalf("changed catalog accepted (approved=%t)", approved)
					}
				}
				if len(bad.OpenCodeCatalog.Models) == 0 {
					return
				}
				// With no opt-in, the incomplete catalog still allows manual edits.
				doc, err := next(bad, initial)
				if err != nil || !strings.Contains(doc.Questions[0].Hint, "preset unavailable") {
					t.Fatalf("manual-path explanation: doc=%+v err=%v", doc, err)
				}
				walkPresetSummary(t, next, bad, initial, func(q question.Question) string {
					if strings.HasSuffix(q.ID, ".model") {
						return "github-copilot/gpt-5-mini"
					}
					return ""
				})
				// A previously submitted decline also survives catalog changes.
				initial[idOpenCodeDeepSeekPreset] = "no"
				if _, err := next(bad, initial); err != nil {
					t.Fatal(err)
				}
			})
		}
	}
}

func TestOpenCodeDeepSeekPresetAnswersAreScoped(t *testing.T) {
	for _, kind := range []string{"init", "configure", "configure-local"} {
		t.Run(kind, func(t *testing.T) {
			next, a, _ := presetInterview(t, kind)
			f := deepSeekFacts()
			a[idOpenCodeDeepSeekPreset] = "yes"
			if kind == "init" {
				a[idHostCodexEnabled] = "yes"
			}
			f.OpenCodeCLI = false
			if _, err := next(f, a); !errors.Is(err, ErrUnknownAnswer) {
				t.Fatalf("undetected runtime: %v", err)
			}
			f.OpenCodeCLI = true
			switch kind {
			case "init":
				a[idHostOpenCodeEnabled] = "no"
				a[idHostCodexEnabled] = "yes"
			case "configure":
				a[idPickRolesOpenCode] = "no"
			case "configure-local":
				a[idPickOpenCode] = "no"
			}
			if _, err := next(f, a); !errors.Is(err, ErrUnknownAnswer) {
				t.Fatalf("unpicked roles: %v", err)
			}
		})
	}
}
