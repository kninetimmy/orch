package interview

import (
	"errors"
	"strings"
	"testing"

	"github.com/kninetimmy/orch/internal/config"
	"github.com/kninetimmy/orch/internal/question"
)

const protectedTwo = `[{"path":"internal/auth","reason":"session and token handling"},{"path":"scripts/release.sh","reason":"publishes releases"}]`

// initSummary answers init with defaults plus the given protected answer and
// returns the summary document.
func initSummary(t *testing.T, protected string) question.Summary {
	t.Helper()
	facts := Facts{ClaudeCLI: true, Git: true, GitRoot: "/repo", Gh: true}
	root := t.TempDir()
	answers := answerAllWithDefaults(t, facts, root)
	if protected != "" {
		answers[idProtectedPaths] = protected
	}
	doc, err := Next(facts, answers, root)
	if err != nil {
		t.Fatalf("Next: %v", err)
	}
	if doc.Kind != question.DocSummary {
		t.Fatalf("Kind = %q, want summary", doc.Kind)
	}
	return *doc.Summary
}

func TestInitAsksProtectedPathsAndDefaultsToDeferral(t *testing.T) {
	facts := Facts{ClaudeCLI: true, Git: true, GitRoot: "/repo", Gh: true}
	answers := answerAllWithDefaults(t, facts, t.TempDir())
	if answers[idProtectedPaths] != protectedDeferAnswer {
		t.Fatalf("protected.paths = %q, want the deferral default", answers[idProtectedPaths])
	}
	if err := question.SpecCheck(protectedDoc(false).questions[0]); err != nil {
		t.Fatal(err)
	}
}

func TestInitProtectedMaterialization(t *testing.T) {
	t.Run("deferral writes no section", func(t *testing.T) {
		toml := initSummary(t, "").ConfigTOML
		if strings.Contains(toml, "[protected]") {
			t.Errorf("deferral wrote a protected section:\n%s", toml)
		}
	})
	t.Run("approved entries with the binary's version", func(t *testing.T) {
		s := initSummary(t, protectedTwo)
		for _, want := range []string{
			"[protected]", "methodology_version = 2",
			`path   = "internal/auth"`, `reason = "session and token handling"`,
			`path   = "scripts/release.sh"`, `reason = "publishes releases"`,
		} {
			if !strings.Contains(s.ConfigTOML, want) {
				t.Errorf("summary TOML missing %q:\n%s", want, s.ConfigTOML)
			}
		}
		cfg, err := config.Parse([]byte(s.ConfigTOML))
		if err != nil {
			t.Fatal(err)
		}
		if len(cfg.Protected.Paths) != 2 || cfg.Protected.MethodologyVersion != config.SurveyMethodologyVersion {
			t.Errorf("Protected = %+v, want 2 entries at version %d", cfg.Protected, config.SurveyMethodologyVersion)
		}
		if rev, err := config.Revision(cfg); err != nil || rev != s.ConfigRevision {
			t.Errorf("revision of the rendered config = %q (%v), summary says %q", rev, err, s.ConfigRevision)
		}
	})
	t.Run("approved empty list writes an empty section", func(t *testing.T) {
		s := initSummary(t, protectedEmptyAnswer)
		cfg, err := config.Parse([]byte(s.ConfigTOML))
		if err != nil {
			t.Fatal(err)
		}
		if cfg.Protected == nil || len(cfg.Protected.Paths) != 0 || cfg.CheckGrantProtected() != nil {
			t.Errorf("Protected = %+v, want an approved empty section", cfg.Protected)
		}
		if rev, err := config.Revision(cfg); err != nil || rev != s.ConfigRevision {
			t.Errorf("revision of the rendered config = %q (%v), summary says %q", rev, err, s.ConfigRevision)
		}
	})
}

func TestProtectedAnswerRejectsMalformed(t *testing.T) {
	facts := Facts{ClaudeCLI: true, Git: true, GitRoot: "/repo", Gh: true}
	root := t.TempDir()
	base := answerAllWithDefaults(t, facts, root)
	for name, bad := range map[string]string{
		"not json":         "internal/auth",
		"object not array": `{"path":"a","reason":"b"}`,
		"unknown field":    `[{"path":"a","reason":"b","extra":1}]`,
		"trailing content": `[] x`,
		"missing reason":   `[{"path":"a"}]`,
		"blank reason":     `[{"path":"a","reason":"  "}]`,
		"multi-line":       `[{"path":"a","reason":"x\ny"}]`,
		"absolute":         `[{"path":"/etc","reason":"b"}]`,
		"escaping":         `[{"path":"../x","reason":"b"}]`,
		"dot":              `[{"path":".","reason":"b"}]`,
		"drive":            `[{"path":"C:/x","reason":"b"}]`,
	} {
		answers := map[string]string{}
		for k, v := range base {
			answers[k] = v
		}
		answers[idProtectedPaths] = bad
		if _, err := Next(facts, answers, root); !errors.Is(err, ErrBadAnswer) {
			t.Errorf("%s: err = %v, want ErrBadAnswer", name, err)
		}
	}
}

func TestConfigurePickerDefaultsFollowCommittedSection(t *testing.T) {
	cases := map[string]struct {
		section *config.Protected
		want    string
	}{
		"missing":       {nil, "yes"},
		"older version": {&config.Protected{MethodologyVersion: config.SurveyMethodologyVersion - 1}, "yes"},
		"newer version": {&config.Protected{MethodologyVersion: config.SurveyMethodologyVersion + 1}, "yes"},
		"current":       {&config.Protected{MethodologyVersion: config.SurveyMethodologyVersion}, "no"},
	}
	for name, tc := range cases {
		root := t.TempDir()
		committed := writeCommittedConfigLocal(t, root)
		committed.Protected = tc.section
		writeCommittedConfig(t, root, committed)
		doc, err := NextConfigure(configureFacts(), map[string]string{idPickHosts: "no", idPickRolesClaude: "no", idPickRolesCodex: "no", idPickSettings: "no"}, root)
		if err != nil {
			t.Fatal(err)
		}
		var got *question.Question
		for i := range doc.Questions {
			if doc.Questions[i].ID == idPickProtected {
				got = &doc.Questions[i]
			}
		}
		if got == nil {
			t.Fatalf("%s: no %s question offered", name, idPickProtected)
		}
		if got.Default != tc.want {
			t.Errorf("%s: default = %q, want %q", name, got.Default, tc.want)
		}
	}
}

func TestConfigureReviewWritesProtectedSection(t *testing.T) {
	root := t.TempDir()
	writeCommittedConfigLocal(t, root)
	writeInstalledBlock(t, root, "CLAUDE.md")
	writeInstalledBlock(t, root, "AGENTS.md")
	answers := map[string]string{
		idPickHosts: "no", idPickRolesClaude: "no", idPickRolesCodex: "no", idPickSettings: "no",
		idPickProtected: "yes", idProtectedPaths: protectedTwo,
	}
	doc, err := NextConfigure(configureFacts(), answers, root)
	if err != nil {
		t.Fatal(err)
	}
	if doc.Kind != question.DocSummary {
		t.Fatalf("Kind = %q, want summary", doc.Kind)
	}
	for _, want := range []string{"internal/auth", "session and token handling", "scripts/release.sh", "publishes releases", "methodology_version = 2"} {
		if !strings.Contains(doc.Summary.ConfigTOML, want) || !strings.Contains(doc.Summary.ConfigDiff, want) {
			t.Errorf("summary TOML/diff missing %q", want)
		}
	}
}

func TestConfigureWithoutReviewKeepsProtectedSection(t *testing.T) {
	root := t.TempDir()
	committed := writeCommittedConfigLocal(t, root)
	committed.Protected = &config.Protected{
		MethodologyVersion: 1,
		Paths:              []config.ProtectedPath{{Path: "internal/keep", Reason: "kept as is"}},
	}
	committed.Merge.Strategy = "rebase"
	writeCommittedConfig(t, root, committed)
	writeInstalledBlock(t, root, "CLAUDE.md")
	writeInstalledBlock(t, root, "AGENTS.md")
	for name, answers := range map[string]map[string]string{
		"not reviewed": {idPickHosts: "no", idPickRolesClaude: "no", idPickRolesCodex: "no", idPickSettings: "yes", idPickProtected: "no", idMergeStrategy: "squash", idMaxSubagents: "3", idMemhubMode: "off", idMetricsEnabled: "no"},
		"deferred":     {idPickHosts: "no", idPickRolesClaude: "no", idPickRolesCodex: "no", idPickSettings: "yes", idPickProtected: "yes", idProtectedPaths: protectedDeferAnswer, idMergeStrategy: "squash", idMaxSubagents: "3", idMemhubMode: "off", idMetricsEnabled: "no"},
	} {
		doc, err := NextConfigure(configureFacts(), answers, root)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		toml := doc.Summary.ConfigTOML
		if !strings.Contains(toml, "methodology_version = 1") || !strings.Contains(toml, `path   = "internal/keep"`) {
			t.Errorf("%s: protected section changed:\n%s", name, toml)
		}
		cfg, err := config.Parse([]byte(toml))
		if err != nil {
			t.Fatal(err)
		}
		if err := cfg.CheckGrantProtected(); err == nil {
			t.Errorf("%s: the legacy list was treated as re-approved", name)
		}
	}
}

func TestProtectedPathsUnreachableUnlessReviewed(t *testing.T) {
	root := t.TempDir()
	writeCommittedConfigLocal(t, root)
	_, err := NextConfigure(configureFacts(), map[string]string{idPickProtected: "no", idProtectedPaths: protectedDeferAnswer}, root)
	if !errors.Is(err, ErrUnknownAnswer) {
		t.Errorf("err = %v, want ErrUnknownAnswer", err)
	}
}
