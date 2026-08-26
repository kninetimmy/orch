package interview

import (
	"errors"
	"strings"
	"testing"
)

// fullAnswers returns a complete, valid both-hosts answer set (every
// PRD §10 default, squash/3/off/no settings) as a base for mutation in
// individual test cases.
func fullAnswers() map[string]string {
	answers := map[string]string{
		idHostClaudeEnabled: "yes",
		idHostCodexEnabled:  "yes",
		idMaxSubagents:      "3",
		idMergeStrategy:     "squash",
		idMemhubMode:        "off",
		idMetricsEnabled:    "no",
	}
	for _, host := range []string{"claude", "codex"} {
		for _, rs := range roleSpecs {
			def := defaultProfiles[host][rs.key]
			answers[roleModelID(host, rs.key)] = def.model
			answers[roleEffortID(host, rs.key)] = def.execution
		}
	}
	return answers
}

func openCodeOnlyAnswers() map[string]string {
	answers := fullAnswers()
	answers[idHostClaudeEnabled] = "no"
	answers[idHostCodexEnabled] = "no"
	answers[idHostOpenCodeEnabled] = "yes"
	for _, rs := range roleSpecs {
		def := defaultProfiles["opencode"][rs.key]
		answers[roleModelID("opencode", rs.key)] = def.model
		answers[roleVariantID("opencode", rs.key)] = variantAnswer(def.execution)
	}
	return answers
}

func TestMaterializeDefaults(t *testing.T) {
	cfg, err := materialize(fullAnswers())
	if err != nil {
		t.Fatalf("materialize: %v", err)
	}
	if cfg.SchemaVersion != 1 {
		t.Errorf("SchemaVersion = %d, want 1", cfg.SchemaVersion)
	}
	if cfg.ConfigRevision == "" {
		t.Error("ConfigRevision is empty")
	}
	if cfg.Hosts.Claude == nil || cfg.Hosts.Codex == nil {
		t.Fatal("expected both hosts materialized")
	}
	if got := cfg.Hosts.Claude.Roles.Architect.Model; got != "claude-opus-5" {
		t.Errorf("claude architect model = %q, want claude-opus-5", got)
	}
	if got := cfg.Hosts.Codex.Roles.ReviewDowngrade.Effort; got != "high" {
		t.Errorf("codex review_downgrade effort = %q, want high", got)
	}
}

func TestMaterializeSingleHost(t *testing.T) {
	answers := fullAnswers()
	answers[idHostCodexEnabled] = "no"
	cfg, err := materialize(answers)
	if err != nil {
		t.Fatalf("materialize: %v", err)
	}
	if cfg.Hosts.Codex != nil {
		t.Error("Hosts.Codex is set, want nil for a disabled host")
	}
	if cfg.Hosts.Claude == nil {
		t.Error("Hosts.Claude is nil, want set")
	}
}

func TestMaterializeOpenCodeOnly(t *testing.T) {
	answers := openCodeOnlyAnswers()
	answers[roleVariantID("opencode", "architect")] = "provider-custom"
	answers[roleVariantID("opencode", "scout")] = noVariantAnswer
	cfg, err := materialize(answers)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Hosts.Claude != nil || cfg.Hosts.Codex != nil || cfg.Hosts.OpenCode == nil {
		t.Fatalf("Hosts = %+v, want OpenCode only", cfg.Hosts)
	}
	if got := cfg.Hosts.OpenCode.Roles.Specialist.Model; got != "openai/gpt-5.6-sol" {
		t.Errorf("specialist model = %q", got)
	}
	if p := cfg.Hosts.OpenCode.Roles.Architect; p.Variant != "provider-custom" || p.Effort != "" {
		t.Errorf("architect profile = %+v, want native provider-custom variant", p)
	}
	if p := cfg.Hosts.OpenCode.Roles.Scout; p.Variant != "" || p.Effort != "" {
		t.Errorf("scout profile = %+v, want native no-variant selection", p)
	}
}

func TestMaterializeFreeTextModel(t *testing.T) {
	answers := fullAnswers()
	answers[roleModelID("claude", "architect")] = "claude-fable-5"
	cfg, err := materialize(answers)
	if err != nil {
		t.Fatalf("materialize: %v", err)
	}
	if got := cfg.Hosts.Claude.Roles.Architect.Model; got != "claude-fable-5" {
		t.Errorf("architect model = %q, want claude-fable-5", got)
	}
}

func TestMaterializeFreeTextModelRejectsWhitespace(t *testing.T) {
	answers := fullAnswers()
	answers[roleModelID("claude", "architect")] = "claude fable 5"
	_, err := materialize(answers)
	if !errors.Is(err, ErrBadAnswer) {
		t.Fatalf("materialize err = %v, want ErrBadAnswer", err)
	}
}

// TestMaterializeRejectsOutOfDomainFreeTextEffort proves issue #124
// criterion 4: an effort value the effort question's FreeText escape
// hatch admits past question.ValidateAnswer, but internal/config would
// reject for that host, never materializes into a config.Config —
// materialize's own Render/Parse round-trip catches it, naming the
// host's full accepted domain (effortList) in the returned error.
func TestMaterializeRejectsOutOfDomainFreeTextEffort(t *testing.T) {
	answers := fullAnswers()
	answers[roleEffortID("codex", "architect")] = "minimal"
	_, err := materialize(answers)
	if err == nil {
		t.Fatal("materialize succeeded, want an error for codex effort=minimal")
	}
	if !strings.Contains(err.Error(), "low, medium, high, xhigh, max, ultra") {
		t.Errorf("error %q does not name codex's full accepted effort domain", err)
	}
}

func TestMaterializeConcurrency(t *testing.T) {
	tests := []struct {
		value   string
		wantN   int
		wantErr bool
	}{
		{value: "3", wantN: 3},
		{value: "7", wantN: 7},
		{value: "0", wantErr: true},
		{value: "x", wantErr: true},
		{value: "-1", wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.value, func(t *testing.T) {
			answers := fullAnswers()
			answers[idMaxSubagents] = tt.value
			cfg, err := materialize(answers)
			if tt.wantErr {
				if !errors.Is(err, ErrBadAnswer) {
					t.Fatalf("materialize err = %v, want ErrBadAnswer", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("materialize: %v", err)
			}
			if cfg.Concurrency.MaxSubagents != tt.wantN {
				t.Errorf("MaxSubagents = %d, want %d", cfg.Concurrency.MaxSubagents, tt.wantN)
			}
		})
	}
}

func TestMaterializeRoundTripsThroughRenderAndParse(t *testing.T) {
	cfg, err := materialize(fullAnswers())
	if err != nil {
		t.Fatalf("materialize: %v", err)
	}
	if !strings.Contains(cfg.ConfigRevision, "sha256:") {
		t.Errorf("ConfigRevision = %q, want a sha256: prefix", cfg.ConfigRevision)
	}
}

// TestValidateModelAnswerNearMiss proves issue #207's shared seam:
// a typed model that shortens (or mis-cases) a known model id for the
// question's host is rejected with every matching full id suggested,
// while a known id and an id resembling nothing known both pass. Both
// question id shapes are covered — init/`orch configure`'s
// "host.<host>.role.<role>.model" and configure-local's
// "hosts.<host>.roles.<role>.model" — since all three interviews reach
// this one function.
func TestValidateModelAnswerNearMiss(t *testing.T) {
	tests := []struct {
		id       string
		value    string
		wantErr  bool
		wantText string
	}{
		{id: roleModelID("claude", "architect"), value: "fable-5", wantErr: true, wantText: "claude-fable-5"},
		{id: roleModelID("claude", "architect"), value: "opus-5", wantErr: true, wantText: "claude-opus-5"},
		{id: roleModelID("claude", "architect"), value: "Claude-Opus-5", wantErr: true, wantText: "claude-opus-5"},
		{id: roleModelID("codex", "architect"), value: "sol", wantErr: true, wantText: "gpt-5.6-sol"},
		{id: localRoleModelID("claude", "reviewer"), value: "fable-5", wantErr: true, wantText: "claude-fable-5"},
		{id: roleModelID("claude", "architect"), value: "claude-opus-5"},
		{id: roleModelID("claude", "architect"), value: "claude-fable-5"},
		{id: roleModelID("claude", "architect"), value: "claude-opus-6"},
		{id: roleModelID("claude", "architect"), value: "claude-opus-4-8"},
		{id: roleModelID("codex", "architect"), value: "gpt-5.6-sol"},
		{id: localRoleModelID("codex", "reviewer"), value: "gpt-5.7-nova"},
	}
	for _, tt := range tests {
		t.Run(tt.id+"="+tt.value, func(t *testing.T) {
			err := validateModelAnswer(tt.id, tt.value, "")
			if !tt.wantErr {
				if err != nil {
					t.Fatalf("validateModelAnswer(%q, %q) = %v, want nil", tt.id, tt.value, err)
				}
				return
			}
			if !errors.Is(err, ErrBadAnswer) {
				t.Fatalf("validateModelAnswer(%q, %q) = %v, want ErrBadAnswer", tt.id, tt.value, err)
			}
			if !strings.Contains(err.Error(), tt.wantText) {
				t.Errorf("error %q does not suggest the full id %q", err, tt.wantText)
			}
		})
	}
}

// TestValidateModelAnswerSuggestsEveryMatch proves a value shortening
// more than one known id names all of them, not just the first.
func TestValidateModelAnswerSuggestsEveryMatch(t *testing.T) {
	err := validateModelAnswer(roleModelID("claude", "architect"), "claude", "")
	if !errors.Is(err, ErrBadAnswer) {
		t.Fatalf("validateModelAnswer err = %v, want ErrBadAnswer", err)
	}
	for _, want := range hostLocalModels["claude"] {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not suggest %q", err, want)
		}
	}
}

// TestValidateModelAnswerExemptsCurrentValue proves the near-miss rule
// targets new input only: the value a key already carries — what its
// question offered as the Default — answers back unchanged, so a
// configuration written before the rule existed stays answerable. The
// shape rule is not exempted with it.
func TestValidateModelAnswerExemptsCurrentValue(t *testing.T) {
	id := roleModelID("claude", "architect")
	if err := validateModelAnswer(id, "opus-5", "opus-5"); err != nil {
		t.Errorf("validateModelAnswer with value == current = %v, want nil", err)
	}
	if err := validateModelAnswer(id, "opus-5", "claude-opus-4-8"); !errors.Is(err, ErrBadAnswer) {
		t.Errorf("validateModelAnswer with a newly typed near miss = %v, want ErrBadAnswer", err)
	}
	if err := validateModelAnswer(id, "", ""); !errors.Is(err, ErrBadAnswer) {
		t.Errorf("validateModelAnswer with an empty current value = %v, want ErrBadAnswer", err)
	}
}

// TestStringifyLeafValueKeepsNearMissModel proves the near-miss rule
// never reaches seedOverrides' filter: a model already written to
// config.local.toml classifies as a valid preference whatever it looks
// like, so an unpicked area's overrides survive materializeLocal
// untouched.
func TestStringifyLeafValueKeepsNearMissModel(t *testing.T) {
	got, ok := stringifyLeafValue(localRoleModelID("claude", "architect"), "opus-5")
	if !ok || got != "opus-5" {
		t.Errorf("stringifyLeafValue = (%q, %v), want (\"opus-5\", true)", got, ok)
	}
}
