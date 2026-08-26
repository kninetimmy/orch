package interview

import (
	"slices"
	"strings"
	"testing"

	"github.com/kninetimmy/orch/internal/question"
)

// TestDefaultProfilesMatchPRD pins defaultProfiles against the PRD §10
// table verbatim (plan verification string: codex
// sol-xhigh/luna-max/terra-max/sol-max/sol-xhigh/sol-high;
// claude opus-5-high/opus-5-low/opus-5-medium/opus-5-high/
// opus-5-high/opus-5-medium; OpenCode uses the same native variant values
// shown in the PRD table).
func TestDefaultProfilesMatchPRD(t *testing.T) {
	want := map[string]map[string]profile{
		"codex": {
			"architect":        {"gpt-5.6-sol", "xhigh"},
			"scout":            {"gpt-5.6-luna", "max"},
			"implementer":      {"gpt-5.6-terra", "max"},
			"specialist":       {"gpt-5.6-sol", "max"},
			"reviewer":         {"gpt-5.6-sol", "xhigh"},
			"review_downgrade": {"gpt-5.6-sol", "high"},
		},
		"claude": {
			"architect":        {"claude-opus-5", "high"},
			"scout":            {"claude-opus-5", "low"},
			"implementer":      {"claude-opus-5", "medium"},
			"specialist":       {"claude-opus-5", "high"},
			"reviewer":         {"claude-opus-5", "high"},
			"review_downgrade": {"claude-opus-5", "medium"},
		},
		"opencode": {
			"architect":        {"openai/gpt-5.6-sol", "xhigh"},
			"scout":            {"openai/gpt-5.6-luna", "max"},
			"implementer":      {"openai/gpt-5.6-terra", "max"},
			"specialist":       {"openai/gpt-5.6-sol", "max"},
			"reviewer":         {"openai/gpt-5.6-sol", "xhigh"},
			"review_downgrade": {"openai/gpt-5.6-sol", "high"},
		},
	}
	for host, roles := range want {
		for role, want := range roles {
			got := defaultProfiles[host][role]
			if got != want {
				t.Errorf("defaultProfiles[%s][%s] = %+v, want %+v", host, role, got, want)
			}
		}
	}
}

// bothHostsFacts is a stub Facts reporting both host CLIs, git, gh,
// and a healthy memhub — the golden-transcript fixture's starting
// point.
func bothHostsFacts() Facts {
	return Facts{
		ClaudeCLI:     true,
		CodexCLI:      true,
		Git:           true,
		GitRoot:       "/repo",
		Gh:            true,
		MemhubCLI:     true,
		MemhubHealthy: true,
	}
}

// answerAllWithDefaults walks Next from an empty answer set to the
// summary document, answering every "questions"-kind document with
// each question's Default, and returns the accumulated answers.
func answerAllWithDefaults(t *testing.T, facts Facts, repoRoot string) map[string]string {
	t.Helper()
	answers := map[string]string{}
	for i := 0; i < 100; i++ {
		doc, err := Next(facts, answers, repoRoot)
		if err != nil {
			t.Fatalf("Next: %v", err)
		}
		if doc.Kind != question.DocQuestions {
			return answers
		}
		for _, q := range doc.Questions {
			if q.Default == "" {
				t.Fatalf("question %s has no default to answer with", q.ID)
			}
			answers[q.ID] = q.Default
		}
	}
	t.Fatal("Next did not reach a non-questions document within 100 steps")
	return nil
}

// TestSequenceQuestionsSpecCheck walks the full both-hosts sequence and
// asserts every emitted question passes question.SpecCheck — the
// engine must never emit a malformed question of its own making.
func TestSequenceQuestionsSpecCheck(t *testing.T) {
	facts := bothHostsFacts()
	answers := map[string]string{}
	seen := map[string]bool{}
	for i := 0; i < 100; i++ {
		doc, err := Next(facts, answers, t.TempDir())
		if err != nil {
			t.Fatalf("Next: %v", err)
		}
		if doc.Kind != question.DocQuestions {
			break
		}
		for _, q := range doc.Questions {
			if err := question.SpecCheck(q); err != nil {
				t.Errorf("SpecCheck(%s): %v", q.ID, err)
			}
			seen[q.ID] = true
			answers[q.ID] = q.Default
		}
	}
	// 2 (host toggles) + 6 roles * 2 questions * 2 hosts + 4 (settings) = 30.
	if len(seen) != 30 {
		t.Errorf("saw %d distinct question ids, want 30: %v", len(seen), seen)
	}
}

// TestHostEffortsAcceptedByConfig proves hostEfforts' full per-host
// domain — the domain validEffort (configurelocal.go) checks a typed
// free-text effort against — never names a value internal/config
// itself would reject for that host (issue #124 criterion 5): every
// value materializes into a config.toml that round-trips through
// config.Render/config.Parse cleanly.
func TestHostEffortsAcceptedByConfig(t *testing.T) {
	for host, efforts := range hostEfforts {
		for _, effort := range efforts {
			t.Run(host+"/"+effort, func(t *testing.T) {
				answers := fullAnswers()
				for _, rs := range roleSpecs {
					answers[roleEffortID(host, rs.key)] = effort
				}
				if _, err := materialize(answers); err != nil {
					t.Fatalf("materialize(%s effort=%s): %v", host, effort, err)
				}
			})
		}
	}
}

// TestEffortsOfferedIsSubsetOfHostEfforts proves effortsOffered stays
// within question.SpecCheck's 2-4-option cap and never offers a value
// hostEfforts does not itself list for that host — combined with
// TestHostEffortsAcceptedByConfig, this closes issue #124 criterion 5
// for every interview-offered effort option too. It also proves the
// window is a contiguous run of the host enum that contains the
// question's own default, whatever that default is (issue #208): the
// Codex roles defaulting to "max" must be able to select it.
func TestEffortsOfferedIsSubsetOfHostEfforts(t *testing.T) {
	for host, efforts := range hostEfforts {
		for _, def := range efforts {
			offered := effortsOffered(host, def)
			if len(offered) < 2 || len(offered) > 4 {
				t.Fatalf("effortsOffered(%s, %s) = %v, want 2-4 options", host, def, offered)
			}
			if !slices.Contains(offered, def) {
				t.Errorf("effortsOffered(%s, %s) = %v, does not offer its own default", host, def, offered)
			}
			if i := slices.Index(efforts, offered[0]); i < 0 || !slices.Equal(efforts[i:i+len(offered)], offered) {
				t.Errorf("effortsOffered(%s, %s) = %v, not a contiguous run of hostEfforts[%s] = %v", host, def, offered, host, efforts)
			}
		}
	}
}

// TestEffortOptionsLabelLiteralTokens proves every effort option an
// interview surface offers is labelled with the literal enum token it
// submits, not a prose gloss that hides it (issue #208) — the token
// config.toml and the FreeText escape hatch both use.
func TestEffortOptionsLabelLiteralTokens(t *testing.T) {
	for host, efforts := range hostEfforts {
		for _, def := range efforts {
			for _, o := range effortOptions(host, def) {
				if !strings.Contains(o.Label, o.Value) {
					t.Errorf("effortOptions(%s, %s): label %q omits token %q", host, def, o.Label, o.Value)
				}
			}
			for _, o := range effortOptionsLocal(host, def, def) {
				if !strings.Contains(o.Label, o.Value) {
					t.Errorf("effortOptionsLocal(%s, %s): label %q omits token %q", host, def, o.Label, o.Value)
				}
			}
		}
	}
}

func TestRoleModelOptionsExcludeFableFive(t *testing.T) {
	for _, host := range []string{"claude", "codex"} {
		for _, m := range hostModels[host] {
			if m == "claude-fable-5" {
				t.Errorf("hostModels[%s] includes claude-fable-5, a local-override-only model (PRD §10)", host)
			}
		}
	}
}
