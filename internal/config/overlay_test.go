package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// loadOverlayFixture copies a committed and a local testdata file
// into a temp repo layout and loads it.
func loadOverlayFixture(t *testing.T, committedName, localName string) (*Config, error) {
	t.Helper()
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, ".orchestrator"), 0o755); err != nil {
		t.Fatal(err)
	}
	committed, err := os.ReadFile(filepath.Join("testdata", filepath.FromSlash(committedName)))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, ".orchestrator", "config.toml"), committed, 0o644); err != nil {
		t.Fatal(err)
	}
	local, err := os.ReadFile(filepath.Join("testdata", filepath.FromSlash(localName)))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, ".orchestrator", "config.local.toml"), local, 0o644); err != nil {
		t.Fatal(err)
	}
	return Load(root)
}

func TestOverlayFableArchitectOverride(t *testing.T) {
	cfg, err := loadOverlayFixture(t, "valid/full.toml", "local/valid/fable_architect.toml")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	arch := cfg.Hosts.Claude.Roles.Architect
	if arch.Model != "claude-fable-5" || arch.Effort != "medium" {
		t.Errorf("claude architect = %+v, want claude-fable-5/medium", arch)
	}
	// Other claude roles untouched.
	if got := cfg.Hosts.Claude.Roles.Scout.Model; got != "claude-sonnet-5" {
		t.Errorf("claude scout model = %q, want unchanged claude-sonnet-5", got)
	}
	// Codex host entirely untouched.
	if got := cfg.Hosts.Codex.Roles.Architect.Model; got != "gpt-5.6-sol" {
		t.Errorf("codex architect model = %q, want unchanged gpt-5.6-sol", got)
	}
	want := []string{"hosts.claude.roles.architect.effort", "hosts.claude.roles.architect.model"}
	if !equalStrings(cfg.Overrides, want) {
		t.Errorf("Overrides = %v, want %v", cfg.Overrides, want)
	}
}

func TestOverlayPartialLeafKeepsCommittedEffort(t *testing.T) {
	cfg, err := loadOverlayFixture(t, "valid/full.toml", "local/valid/architect_model_only.toml")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	arch := cfg.Hosts.Claude.Roles.Architect
	if arch.Model != "claude-fable-5" {
		t.Errorf("model = %q, want claude-fable-5", arch.Model)
	}
	if arch.Effort != "xhigh" {
		t.Errorf("effort = %q, want committed xhigh to survive a model-only override", arch.Effort)
	}
	want := []string{"hosts.claude.roles.architect.model"}
	if !equalStrings(cfg.Overrides, want) {
		t.Errorf("Overrides = %v, want %v", cfg.Overrides, want)
	}
}

func TestOverlayPreferenceScalars(t *testing.T) {
	cfg, err := loadOverlayFixture(t, "valid/full.toml", "local/valid/preferences.toml")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Concurrency.MaxSubagents != 7 {
		t.Errorf("MaxSubagents = %d, want 7", cfg.Concurrency.MaxSubagents)
	}
	if !cfg.Metrics.Enabled {
		t.Error("Metrics.Enabled = false, want true")
	}
	want := []string{"concurrency.max_subagents", "metrics.enabled"}
	if !equalStrings(cfg.Overrides, want) {
		t.Errorf("Overrides = %v, want %v", cfg.Overrides, want)
	}
}

func TestOverlayEmptyLocalFile(t *testing.T) {
	cfg, err := loadOverlayFixture(t, "valid/full.toml", "local/valid/empty.toml")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(cfg.Overrides) != 0 {
		t.Errorf("Overrides = %v, want none", cfg.Overrides)
	}
	if got := cfg.Hosts.Claude.Roles.Architect.Model; got != "claude-opus-4-8" {
		t.Errorf("architect model = %q, want committed value unchanged", got)
	}
}

func TestOverlayPolicyViolations(t *testing.T) {
	tests := []struct {
		fixture    string
		wantInErrs []string
	}{
		{"local/invalid/policy_schema_version.toml", []string{"schema_version", LocalOverridePath}},
		{"local/invalid/policy_config_revision.toml", []string{"config_revision", LocalOverridePath}},
		{"local/invalid/policy_merge_strategy.toml", []string{"merge.strategy", LocalOverridePath}},
		{"local/invalid/policy_memhub_mode.toml", []string{"memhub.mode", LocalOverridePath}},
		{"local/invalid/policy_multiple.toml", []string{"schema_version", "merge.strategy"}},
	}
	for _, tt := range tests {
		t.Run(tt.fixture, func(t *testing.T) {
			cfg, err := loadOverlayFixture(t, "valid/full.toml", tt.fixture)
			if err == nil {
				t.Fatal("Load succeeded, want error")
			}
			if cfg != nil {
				t.Error("Load returned a Config alongside an error")
			}
			for _, want := range tt.wantInErrs {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("error %q does not mention %q", err, want)
				}
			}
		})
	}
}

func TestOverlayUnknownKey(t *testing.T) {
	cfg, err := loadOverlayFixture(t, "valid/full.toml", "local/invalid/unknown_key.toml")
	if err == nil {
		t.Fatal("Load succeeded, want error")
	}
	if cfg != nil {
		t.Error("Load returned a Config alongside an error")
	}
	if !strings.Contains(err.Error(), LocalOverridePath) {
		t.Errorf("error %q does not attribute to %s", err, LocalOverridePath)
	}
	if !strings.Contains(err.Error(), "bogus_local_key") {
		t.Errorf("error %q does not name bogus_local_key", err)
	}
}

func TestOverlayHostNotEnabled(t *testing.T) {
	cfg, err := loadOverlayFixture(t, "valid/minimal.toml", "local/invalid/host_not_enabled.toml")
	if err == nil {
		t.Fatal("Load succeeded, want error")
	}
	if cfg != nil {
		t.Error("Load returned a Config alongside an error")
	}
	if !strings.Contains(err.Error(), "hosts.codex") {
		t.Errorf("error %q does not name hosts.codex", err)
	}
	if !strings.Contains(err.Error(), "Delivery PR") {
		t.Errorf("error %q does not mention the Delivery PR remediation", err)
	}
}

func TestOverlayInvalidPreferenceValue(t *testing.T) {
	cfg, err := loadOverlayFixture(t, "valid/full.toml", "local/invalid/bad_effort.toml")
	if err == nil {
		t.Fatal("Load succeeded, want error")
	}
	if cfg != nil {
		t.Error("Load returned a Config alongside an error")
	}
	if !strings.Contains(err.Error(), LocalOverridePath) {
		t.Errorf("error %q does not attribute to %s", err, LocalOverridePath)
	}
	if !strings.Contains(err.Error(), `"ultra"`) {
		t.Errorf("error %q does not name the bad value", err)
	}
}

func TestOverlaySkippedWhenCommittedInvalid(t *testing.T) {
	cfg, err := loadOverlayFixture(t, "invalid/bad_schema_version.toml", "local/valid/preferences.toml")
	if err == nil {
		t.Fatal("Load succeeded, want error")
	}
	if cfg != nil {
		t.Error("Load returned a Config alongside an error")
	}
	if !strings.Contains(err.Error(), "schema_version") {
		t.Errorf("error %q does not mention schema_version", err)
	}
	if !strings.Contains(err.Error(), Path) {
		t.Errorf("error %q is not attributed to %s", err, Path)
	}
	if strings.Contains(err.Error(), LocalOverridePath) {
		t.Errorf("error %q should not mention %s: the local file must not be consulted before the committed config is valid", err, LocalOverridePath)
	}
}

// TestOverlayLoadDoesNotLeakBetweenCalls exercises the same repo root
// with and without the local file, on two independent Load calls, to
// confirm the overlay never leaves the committed values changed.
func TestOverlayLoadDoesNotLeakBetweenCalls(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, ".orchestrator"), 0o755); err != nil {
		t.Fatal(err)
	}
	committed, err := os.ReadFile(filepath.Join("testdata", "valid", "full.toml"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, ".orchestrator", "config.toml"), committed, 0o644); err != nil {
		t.Fatal(err)
	}
	local, err := os.ReadFile(filepath.Join("testdata", "local", "valid", "fable_architect.toml"))
	if err != nil {
		t.Fatal(err)
	}
	localPath := filepath.Join(root, ".orchestrator", "config.local.toml")
	if err := os.WriteFile(localPath, local, 0o644); err != nil {
		t.Fatal(err)
	}

	cfg1, err := Load(root)
	if err != nil {
		t.Fatalf("Load with override: %v", err)
	}
	if got := cfg1.Hosts.Claude.Roles.Architect.Model; got != "claude-fable-5" {
		t.Fatalf("overridden model = %q, want claude-fable-5", got)
	}

	if err := os.Remove(localPath); err != nil {
		t.Fatal(err)
	}
	cfg2, err := Load(root)
	if err != nil {
		t.Fatalf("Load without override: %v", err)
	}
	if got := cfg2.Hosts.Claude.Roles.Architect.Model; got != "claude-opus-4-8" {
		t.Errorf("model after removing override = %q, want committed claude-opus-4-8", got)
	}
}

// TestMergeOverrideDoesNotMutateCommittedHost is a direct, white-box
// check of the aliasing property: mergeOverride must never write
// through committed's *Host pointers.
func TestMergeOverrideDoesNotMutateCommittedHost(t *testing.T) {
	committed := &Config{
		Hosts: Hosts{
			Claude: &Host{Roles: Roles{Architect: RoleProfile{Model: "claude-opus-4-8", Effort: "xhigh"}}},
		},
	}
	local := &Config{
		Hosts: Hosts{
			Claude: &Host{Roles: Roles{Architect: RoleProfile{Model: "claude-fable-5", Effort: "medium"}}},
		},
	}
	merged := mergeOverride(committed, local, []string{"hosts.claude.roles.architect.effort", "hosts.claude.roles.architect.model"})

	if got := committed.Hosts.Claude.Roles.Architect.Model; got != "claude-opus-4-8" {
		t.Errorf("committed mutated: model = %q, want unchanged claude-opus-4-8", got)
	}
	if got := committed.Hosts.Claude.Roles.Architect.Effort; got != "xhigh" {
		t.Errorf("committed mutated: effort = %q, want unchanged xhigh", got)
	}
	if got := merged.Hosts.Claude.Roles.Architect.Model; got != "claude-fable-5" {
		t.Errorf("merged model = %q, want claude-fable-5", got)
	}
	if committed.Hosts.Claude == merged.Hosts.Claude {
		t.Error("merged Host shares committed's pointer")
	}
}

// TestMergeLocalEquivalentToLoad proves MergeLocal, called directly on
// raw committed and local bytes, produces the same result Load's
// applyLocalOverride wrapper does when reading those same bytes from
// disk — the split changed nothing behaviorally.
func TestMergeLocalEquivalentToLoad(t *testing.T) {
	committedData, err := os.ReadFile(filepath.Join("testdata", "valid", "full.toml"))
	if err != nil {
		t.Fatal(err)
	}
	committed, err := Parse(committedData)
	if err != nil {
		t.Fatal(err)
	}
	localData, err := os.ReadFile(filepath.Join("testdata", "local", "valid", "fable_architect.toml"))
	if err != nil {
		t.Fatal(err)
	}

	viaMergeLocal, err := MergeLocal(committed, localData)
	if err != nil {
		t.Fatalf("MergeLocal: %v", err)
	}
	viaLoad, err := loadOverlayFixture(t, "valid/full.toml", "local/valid/fable_architect.toml")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if viaMergeLocal.Hosts.Claude.Roles.Architect.Model != viaLoad.Hosts.Claude.Roles.Architect.Model {
		t.Errorf("MergeLocal architect model = %q, want %q (Load's)", viaMergeLocal.Hosts.Claude.Roles.Architect.Model, viaLoad.Hosts.Claude.Roles.Architect.Model)
	}
	if !equalStrings(viaMergeLocal.Overrides, viaLoad.Overrides) {
		t.Errorf("MergeLocal Overrides = %v, want %v (Load's)", viaMergeLocal.Overrides, viaLoad.Overrides)
	}
}

// TestMergeLocalRejectsPolicyKey proves MergeLocal itself (not just the
// Load wrapper) still fails closed on a policy-bearing key.
func TestMergeLocalRejectsPolicyKey(t *testing.T) {
	committedData, err := os.ReadFile(filepath.Join("testdata", "valid", "full.toml"))
	if err != nil {
		t.Fatal(err)
	}
	committed, err := Parse(committedData)
	if err != nil {
		t.Fatal(err)
	}
	localData, err := os.ReadFile(filepath.Join("testdata", "local", "invalid", "policy_merge_strategy.toml"))
	if err != nil {
		t.Fatal(err)
	}

	if _, err := MergeLocal(committed, localData); err == nil {
		t.Fatal("MergeLocal succeeded, want error for a policy-bearing key")
	}
}

func equalStrings(got, want []string) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}
