package cli

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/kninetimmy/orch/internal/config"
	"github.com/kninetimmy/orch/internal/lockfile"
	"github.com/kninetimmy/orch/internal/state"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func mcpConfigExercise(t *testing.T, binary string) {
	t.Helper()
	root, cwd := t.TempDir(), t.TempDir()
	writeConfigOnly(t, root, "invalid configuration [")
	writeConfig(t, cwd, strings.Replace(validTOML, `"r1"`, `"launching-repository"`, 1))
	if _, err := state.EnterDelivery(cwd, "claude", testPlanRef(), testIssues()); err != nil {
		t.Fatal(err)
	}
	// Configuration inspection must not need readable run state or locks.
	mcpWrite(t, filepath.Join(root, state.Path), []byte("{corrupt state"))
	mcpWrite(t, filepath.Join(root, lockfile.Path), []byte("{corrupt lock"))
	cwdBefore := mcpTree(t, cwd)
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	session, closeSession := mcpConnect(t, ctx, binary, cwd, root, "--repo", root)
	mcpDiscover(t, ctx, session, root)
	committed, local := filepath.Join(root, config.Path), filepath.Join(root, config.LocalOverridePath)
	mcpWrite(t, committed, []byte(validTOML))
	for _, args := range []any{
		map[string]any{"repo": cwd}, map[string]any{"repo_root": cwd}, map[string]any{"unexpected": true},
		[]any{}, "not an object", 1, true, json.RawMessage("null"),
	} {
		result := mcpCall(t, ctx, session, root, "orch_config_effective", args)
		if !result.IsError || result.StructuredContent != nil {
			t.Fatalf("invalid configuration arguments accepted: %#v => %+v", args, result)
		}
	}
	// Every failure must match the loader, expose no partial report, and recover
	// on this same connection when the files are repaired.
	for _, tc := range []struct {
		name, committed, local string
	}{
		{"malformed committed", "invalid configuration [", ""},
		{"missing committed", "", ""},
		{"unsupported schema", strings.Replace(validTOML, "schema_version  = 1", "schema_version  = 99", 1), ""},
		{"invalid committed", validTOML + "\n[concurrency]\nmax_subagents = 0\n", ""},
		{"unknown committed key", validTOML + "\n[unknown]\nvalue = true\n", ""},
		{"malformed local", validTOML, "invalid overlay ["},
		{"invalid local", validTOML, "[concurrency]\nmax_subagents = 0\n"},
		{"forbidden local policy", validTOML, "[merge]\nstrategy = \"rebase\"\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if tc.committed == "" {
				if err := os.Remove(committed); err != nil {
					t.Fatal(err)
				}
			} else {
				mcpWrite(t, committed, []byte(tc.committed))
			}
			mcpWrite(t, local, []byte(tc.local))
			_, loadErr := config.Load(root)
			if loadErr == nil {
				t.Fatal("invalid fixture loaded successfully")
			}
			result := mcpCall(t, ctx, session, root, "orch_config_effective", map[string]any{})
			if !result.IsError || result.StructuredContent != nil || len(result.Content) != 1 {
				t.Fatalf("configuration failure returned a report: %+v", result)
			}
			text, ok := result.Content[0].(*mcp.TextContent)
			if !ok || text.Text != loadErr.Error() {
				t.Fatalf("configuration error mismatch: loader=%v MCP=%+v", loadErr, result)
			}
			mcpDiscover(t, ctx, session, root)
			mcpWrite(t, committed, []byte(validTOML))
			if err := os.Remove(local); err != nil {
				t.Fatal(err)
			}
			cfg := mcpConfigParity(t, ctx, root, session)
			if cfg.SchemaVersion != 1 || cfg.ConfigRevision != "r1" || cfg.Concurrency.MaxSubagents != 3 || cfg.Merge.Strategy != "squash" || cfg.Metrics.Enabled || cfg.Overrides != nil || cfg.Protected != nil {
				t.Fatalf("defaults/recovery = %+v", cfg)
			}
			t.Logf("explicit loader error and same-connection recovery: %s", tc.name)
		})
	}
	allHosts := validBothHostsTOML + validOpenCodeTOML[strings.Index(validOpenCodeTOML, "[hosts.opencode"):]
	allHosts += "\n[protected]\nmethodology_version = 2\n[[protected.paths]]\npath = \"policy/\"\nreason = \"fixture policy boundary\"\n"
	mcpWrite(t, committed, []byte(allHosts))
	cfg := mcpConfigParity(t, ctx, root, session)
	if cfg.Hosts.Claude == nil || cfg.Hosts.Codex == nil || cfg.Hosts.OpenCode == nil || cfg.Hosts.OpenCode.Roles.Architect.Variant != "xhigh" || cfg.Hosts.OpenCode.Roles.Architect.Effort != "" || cfg.Protected == nil || len(cfg.Protected.Paths) != 1 {
		t.Fatalf("host profiles/protected entries = %+v", cfg)
	}
	// Deliberately unsorted input; preserve the loader's sorted audit keys and
	// explicit bare-model override, without normalizing the Config JSON.
	mcpWrite(t, local, []byte(`[metrics]
enabled = true
[hosts.opencode.roles.architect]
model = "example/current-model"
variant = ""
[concurrency]
max_subagents = 7
`))
	cfg = mcpConfigParity(t, ctx, root, session)
	if cfg.Concurrency.MaxSubagents != 7 || !cfg.Metrics.Enabled || cfg.Hosts.OpenCode.Roles.Architect.Model != "example/current-model" || cfg.Hosts.OpenCode.Roles.Architect.EffectiveOpenCodeVariant() != "" || !reflect.DeepEqual(cfg.Overrides, []string{
		"concurrency.max_subagents", "hosts.opencode.roles.architect.model", "hosts.opencode.roles.architect.variant", "metrics.enabled",
	}) {
		t.Fatalf("fresh local values = %+v", cfg)
	}
	// Native and legacy OpenCode spelling both pass through unmodified. A local
	// native spelling replaces legacy Effort, and vice versa, in the loader.
	legacy := strings.ReplaceAll(allHosts, "variant =", "effort =")
	mcpWrite(t, committed, []byte(strings.Replace(legacy, `"r1"`, `"current-files"`, 1)))
	if err := os.Remove(local); err != nil {
		t.Fatal(err)
	}
	cfg = mcpConfigParity(t, ctx, root, session)
	if cfg.ConfigRevision != "current-files" || cfg.Hosts.OpenCode.Roles.Architect.Effort != "xhigh" || cfg.Hosts.OpenCode.Roles.Architect.Variant != "" {
		t.Fatalf("fresh legacy profile = %+v", cfg)
	}
	mcpWrite(t, local, []byte("[hosts.opencode.roles.architect]\nvariant = \"native-variant\"\n"))
	cfg = mcpConfigParity(t, ctx, root, session)
	if cfg.Hosts.OpenCode.Roles.Architect.Effort != "" || cfg.Hosts.OpenCode.Roles.Architect.Variant != "native-variant" {
		t.Fatalf("native overlay on legacy profile = %+v", cfg)
	}
	mcpWrite(t, committed, []byte(allHosts))
	mcpWrite(t, local, []byte("[hosts.opencode.roles.architect]\neffort = \"high\"\n"))
	cfg = mcpConfigParity(t, ctx, root, session)
	if cfg.Hosts.OpenCode.Roles.Architect.Effort != "high" || cfg.Hosts.OpenCode.Roles.Architect.Variant != "" || !reflect.DeepEqual(cfg.Overrides, []string{"hosts.opencode.roles.architect.effort"}) {
		t.Fatalf("legacy overlay on native profile = %+v", cfg)
	}
	if mcpTree(t, cwd) != cwdBefore {
		t.Fatal("configuration inspection changed launching repository")
	}
	closeSession()
}

func mcpConfigParity(t *testing.T, ctx context.Context, root string, session *mcp.ClientSession) *config.Config {
	t.Helper()
	cfg, err := config.Load(root)
	if err != nil {
		t.Fatal(err)
	}
	wantJSON, err := json.Marshal(map[string]any{"schema_version": 1, "configuration": cfg})
	if err != nil {
		t.Fatal(err)
	}
	result := mcpCall(t, ctx, session, root, "orch_config_effective", map[string]any{})
	if result.IsError || len(result.Content) != 1 {
		t.Fatalf("effective configuration result = %+v", result)
	}
	text, ok := result.Content[0].(*mcp.TextContent)
	want := mcpDecoded(t, wantJSON)
	if !ok || !reflect.DeepEqual(result.StructuredContent, want) || !reflect.DeepEqual(mcpDecoded(t, []byte(text.Text)), want) {
		t.Fatalf("configuration mismatch: loader=%s structured=%v content=%+v", wantJSON, result.StructuredContent, result.Content)
	}
	t.Log("config.Load = structured = JSON text; repository unchanged")
	return cfg
}
