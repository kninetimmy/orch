package interview

import (
	"testing"

	"github.com/kninetimmy/orch/internal/question"
)

// TestDefaultProfilesMatchPRD pins defaultProfiles against the PRD §10
// table verbatim (plan verification string: codex
// sol-high/terra-low/terra-high/sol-medium/sol-medium/terra-high;
// claude opus-4-8-xhigh/sonnet-5-low/sonnet-5-xhigh/opus-4-8-high/
// opus-4-8-high/sonnet-5-high).
func TestDefaultProfilesMatchPRD(t *testing.T) {
	want := map[string]map[string]profile{
		"codex": {
			"architect":        {"gpt-5.6-sol", "high"},
			"scout":            {"gpt-5.6-terra", "low"},
			"implementer":      {"gpt-5.6-terra", "high"},
			"specialist":       {"gpt-5.6-sol", "medium"},
			"reviewer":         {"gpt-5.6-sol", "medium"},
			"review_downgrade": {"gpt-5.6-terra", "high"},
		},
		"claude": {
			"architect":        {"claude-opus-4-8", "xhigh"},
			"scout":            {"claude-sonnet-5", "low"},
			"implementer":      {"claude-sonnet-5", "xhigh"},
			"specialist":       {"claude-opus-4-8", "high"},
			"reviewer":         {"claude-opus-4-8", "high"},
			"review_downgrade": {"claude-sonnet-5", "high"},
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

func TestRoleModelOptionsExcludeFableFive(t *testing.T) {
	for _, host := range []string{"claude", "codex"} {
		for _, m := range hostModels[host] {
			if m == "claude-fable-5" {
				t.Errorf("hostModels[%s] includes claude-fable-5, a local-override-only model (PRD §10)", host)
			}
		}
	}
}
