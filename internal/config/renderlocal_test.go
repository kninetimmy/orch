package config

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestRenderLocalGoldenFull(t *testing.T) {
	overrides := map[string]string{
		"concurrency.max_subagents":           "2",
		"metrics.enabled":                     "true",
		"hosts.claude.roles.architect.model":  "claude-fable-5",
		"hosts.claude.roles.architect.effort": "medium",
		"hosts.claude.roles.scout.model":      "claude-opus-4-8",
		"hosts.codex.roles.reviewer.effort":   "low",
	}
	got, err := RenderLocal(overrides)
	if err != nil {
		t.Fatalf("RenderLocal: %v", err)
	}
	checkGolden(t, filepath.Join("renderlocal", "full.golden.toml"), got)
}

func TestRenderLocalGoldenOneKey(t *testing.T) {
	got, err := RenderLocal(map[string]string{"metrics.enabled": "true"})
	if err != nil {
		t.Fatalf("RenderLocal: %v", err)
	}
	checkGolden(t, filepath.Join("renderlocal", "one_key.golden.toml"), got)
}

func TestRenderLocalEmptyMapIsError(t *testing.T) {
	_, err := RenderLocal(map[string]string{})
	if err == nil {
		t.Fatal("RenderLocal(empty) succeeded, want error")
	}
	if !strings.Contains(err.Error(), "RemoveLocal") {
		t.Errorf("error %q does not point at RemoveLocal", err)
	}
}

func TestRenderLocalRejectsPolicyKey(t *testing.T) {
	_, err := RenderLocal(map[string]string{"merge.strategy": "squash"})
	if err == nil {
		t.Fatal("RenderLocal(policy key) succeeded, want error")
	}
}

func TestRenderLocalRejectsInvalidValues(t *testing.T) {
	tests := map[string]string{
		"concurrency.max_subagents":          "0",
		"metrics.enabled":                    "maybe",
		"hosts.claude.roles.architect.model": "has space",
		"hosts.codex.roles.reviewer.effort":  "ultra",
	}
	for key, value := range tests {
		t.Run(key, func(t *testing.T) {
			if _, err := RenderLocal(map[string]string{key: value}); err == nil {
				t.Fatalf("RenderLocal(%s=%q) succeeded, want error", key, value)
			}
		})
	}
}

// TestRenderLocalSelfCheckCatchesMismatch directly exercises
// selfCheckRenderLocal, RenderLocal's own internal guard, with
// deliberately mismatched inputs a correct RenderLocal call would never
// produce.
func TestRenderLocalSelfCheckCatchesMismatch(t *testing.T) {
	t.Run("missing key", func(t *testing.T) {
		data := []byte("[concurrency]\nmax_subagents = 3\n")
		err := selfCheckRenderLocal(data, map[string]string{"metrics.enabled": "true"})
		if err == nil {
			t.Fatal("self-check succeeded, want error (rendered output is missing the requested key)")
		}
	})
	t.Run("unexpected key", func(t *testing.T) {
		data := []byte("[concurrency]\nmax_subagents = 3\n\n[metrics]\nenabled = true\n")
		err := selfCheckRenderLocal(data, map[string]string{"concurrency.max_subagents": "3"})
		if err == nil {
			t.Fatal("self-check succeeded, want error (rendered output carries an unexpected key)")
		}
	})
}

// TestRenderLocalKeysMatchPreferenceClass walks every key
// PreferenceKeys lists, renders a single-key override for it, and
// confirms MergeLocal accepts the result and records exactly that one
// key as an override — proving RenderLocal and the closed
// classification table agree on the complete preference-key set.
func TestRenderLocalKeysMatchPreferenceClass(t *testing.T) {
	wantCount := 2 + 2*6*2 // concurrency + metrics, 2 hosts * 6 roles * (model, effort)
	if got := len(PreferenceKeys()); got != wantCount {
		t.Fatalf("len(PreferenceKeys()) = %d, want %d", got, wantCount)
	}

	committed := bothHostsConfig()
	for _, key := range PreferenceKeys() {
		t.Run(key, func(t *testing.T) {
			data, err := RenderLocal(map[string]string{key: sampleValueFor(key)})
			if err != nil {
				t.Fatalf("RenderLocal(%s): %v", key, err)
			}
			merged, err := MergeLocal(committed, data)
			if err != nil {
				t.Fatalf("MergeLocal(%s): %v", key, err)
			}
			if len(merged.Overrides) != 1 || merged.Overrides[0] != key {
				t.Errorf("Overrides = %v, want exactly [%s]", merged.Overrides, key)
			}
		})
	}
}

// sampleValueFor returns a legal value for key, of whatever shape
// RenderLocal expects for it.
func sampleValueFor(key string) string {
	switch key {
	case "concurrency.max_subagents":
		return "2"
	case "metrics.enabled":
		return "true"
	}
	if strings.HasSuffix(key, ".effort") {
		return "high" // valid in both codex's and claude's effort domains
	}
	return "claude-fable-5" // any non-whitespace string is a legal free-text model
}
