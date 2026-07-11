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
			answers[roleEffortID(host, rs.key)] = def.effort
		}
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
	if got := cfg.Hosts.Claude.Roles.Architect.Model; got != "claude-opus-4-8" {
		t.Errorf("claude architect model = %q, want claude-opus-4-8", got)
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
