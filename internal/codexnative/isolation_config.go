package codexnative

import (
	"encoding/json"
	"fmt"
	"maps"
	"slices"
	"strings"
	"unicode/utf8"
)

// These controls cover the required surfaces, not a list of server names.
var restrictedFeatures = []string{
	"apps", "plugins", "remote_plugin", "hooks", "multi_agent", "multi_agent_v2",
	"browser_use", "browser_use_external", "computer_use", "code_mode_host",
	"workspace_dependencies", "skill_mcp_dependency_install",
}

// Diagnostic compatibility does not require these model-only controls.
var modelRestrictedFeatures = []string{
	"goals", "request_permissions_tool", "exec_permission_approvals", "image_generation",
}

// Only the fields needed for restrictions are decoded; account/configuration
// metadata and server credentials are discarded, never returned or logged.
type isolationConfig struct {
	ModelProvider string                     `json:"model_provider"`
	Features      map[string]json.RawMessage `json:"features"`
	Servers       map[string]*struct {
		Enabled *bool `json:"enabled"`
	} `json:"mcp_servers"`
	WebSearch string `json:"web_search"`
	Approval  string `json:"approval_policy"`
	Default   string `json:"default_permissions"`
	Windows   struct {
		Sandbox string `json:"sandbox"`
	} `json:"windows"`
	Environment struct {
		Inherit string                     `json:"inherit"`
		Set     map[string]json.RawMessage `json:"set"`
	} `json:"shell_environment_policy"`
	Permissions map[string]*struct {
		Extends    []string                   `json:"extends"`
		Roots      []string                   `json:"workspace_roots"`
		Filesystem map[string]json.RawMessage `json:"filesystem"`
		Network    struct {
			Enabled *bool `json:"enabled"`
		} `json:"network"`
	} `json:"permissions"`
}

func restrictionError(requirement string) error {
	return fmt.Errorf("%w: %s", ErrIsolationUnavailable, requirement)
}

func readIsolationConfig(c *connection, cwd string) (*isolationConfig, error) {
	var result struct {
		Config *isolationConfig `json:"config"`
	}
	if err := c.call("config/read", map[string]any{"cwd": cwd, "includeLayers": false}, &result); err != nil {
		return nil, err
	}
	if result.Config == nil {
		return nil, restrictionError("config/read effective configuration missing")
	}
	return result.Config, nil
}

func (config *isolationConfig) serverNames() ([]string, error) {
	if config.Servers == nil || len(config.Servers) > 256 {
		return nil, restrictionError("config/read MCP server table missing or exceeds 256 entries")
	}
	for name, server := range config.Servers {
		if name == "" || len(name) > 1024 || !utf8.ValidString(name) || strings.ContainsRune(name, '\x00') || server == nil {
			return nil, restrictionError("config/read invalid MCP server entry")
		}
	}
	return slices.Sorted(maps.Keys(config.Servers)), nil
}

func disableMCP(args, servers []string) []string {
	entries := make([]string, 0, len(servers))
	for _, name := range servers {
		entries = append(entries, quoteTOML(name)+"={enabled=false}")
	}
	return append(args, "-c", "mcp_servers={"+strings.Join(entries, ",")+"}")
}

func (config *isolationConfig) verify(b isolationBoundary, servers []string) error {
	actual, err := config.serverNames()
	if err != nil {
		return err
	}
	if !slices.Equal(actual, servers) {
		return restrictionError("inherited MCP configuration changed after discovery")
	}
	for _, name := range servers {
		if enabled := config.Servers[name].Enabled; enabled == nil || *enabled {
			return restrictionError("inherited MCP server disable missing or denied")
		}
	}
	if err := config.verifyFeatures(restrictedFeatures); err != nil {
		return err
	}
	if config.WebSearch != "disabled" {
		return restrictionError("config/read requires disabled web_search")
	}
	if config.Approval != "never" {
		return restrictionError("config/read requires approval_policy never")
	}
	if config.Windows.Sandbox != "elevated" || config.Default != b.profile() {
		return restrictionError("config/read elevated sandbox or default permission profile incompatible")
	}
	if config.Environment.Inherit != "none" || len(config.Environment.Set) != 3 {
		return restrictionError("config/read command environment not scrubbed and scratch-pinned")
	}
	for _, name := range []string{"TEMP", "TMP", "TMPDIR"} {
		var value string
		if json.Unmarshal(config.Environment.Set[name], &value) != nil || value != b.scratch {
			return restrictionError("config/read command temp environment not scratch-pinned")
		}
	}
	profile := config.Permissions[b.profile()]
	if profile == nil || len(profile.Extends) != 0 || len(profile.Roots) != 0 || profile.Network.Enabled == nil || *profile.Network.Enabled {
		return restrictionError("config/read permission profile missing, extended or network-enabled")
	}
	want := map[string]string{":root": "read", b.workspace: "write", b.scratch: "write"}
	if b.reviewer {
		want[b.workspace] = "read"
	}
	for _, path := range b.protected {
		want[path] = "deny"
	}
	for path, data := range profile.Filesystem {
		if path == "glob_scan_max_depth" {
			continue // scan metadata is not a filesystem grant
		}
		var access string
		if json.Unmarshal(data, &access) != nil || want[path] == "" || access != want[path] {
			return restrictionError("config/read filesystem boundary conflicts with approved paths")
		}
		delete(want, path)
	}
	if len(want) != 0 {
		return restrictionError("config/read filesystem boundary missing approved rules")
	}
	return nil
}

func (config *isolationConfig) verifyFeatures(features []string) error {
	for _, feature := range features {
		var enabled *bool
		data := config.Features[feature]
		if feature == "multi_agent_v2" && len(data) > 0 && data[0] == '{' {
			var multi struct {
				Enabled *bool `json:"enabled"`
			}
			if json.Unmarshal(data, &multi) == nil {
				enabled = multi.Enabled
			}
		} else {
			if json.Unmarshal(data, &enabled) != nil {
				enabled = nil
			}
		}
		if enabled == nil || *enabled {
			return restrictionError("config/read requires disabled features." + feature)
		}
	}
	return nil
}

// A raw config echo alone cannot prove that a feature control is supported.
// This read-only list reports enablement in the actual server's loaded config.
func verifyRestrictedFeatures(c *connection, additional ...string) error {
	required := append(append([]string(nil), restrictedFeatures...), additional...)
	seen := map[string]bool{}
	cursors := map[string]bool{}
	var cursor *string
	for page := 0; page < 16; page++ {
		var result struct {
			Data []struct {
				Name    string `json:"name"`
				Enabled *bool  `json:"enabled"`
			} `json:"data"`
			NextCursor *string `json:"nextCursor"`
		}
		if err := c.call("experimentalFeature/list", map[string]any{"cursor": cursor, "limit": 100}, &result); err != nil {
			return err
		}
		if result.Data == nil {
			return restrictionError("experimentalFeature/list data missing")
		}
		for _, feature := range result.Data {
			if !slices.Contains(required, feature.Name) {
				continue
			}
			if seen[feature.Name] || feature.Enabled == nil || *feature.Enabled {
				return restrictionError("experimentalFeature/list requires unambiguous disabled " + feature.Name)
			}
			seen[feature.Name] = true
		}
		if result.NextCursor == nil {
			for _, name := range required {
				if !seen[name] {
					return restrictionError("experimentalFeature/list unsupported required feature " + name)
				}
			}
			return nil
		}
		cursor = result.NextCursor
		if *cursor == "" || len(*cursor) > 1024 || cursors[*cursor] {
			return restrictionError("experimentalFeature/list invalid or repeated cursor")
		}
		cursors[*cursor] = true
	}
	return restrictionError("experimentalFeature/list exceeds 16 pages")
}

// Native inventories supplement loaded feature support; a configuration echo
// alone cannot establish that inherited hook/plugin execution is excluded.
func verifyDisabledCapabilities(c *connection, b isolationBoundary) error {
	var hooks struct {
		Data []struct {
			Cwd      string            `json:"cwd"`
			Hooks    []json.RawMessage `json:"hooks"`
			Errors   []json.RawMessage `json:"errors"`
			Warnings []json.RawMessage `json:"warnings"`
		} `json:"data"`
	}
	if err := c.call("hooks/list", map[string]any{"cwds": []string{b.workspace}}, &hooks); err != nil {
		return err
	}
	if len(hooks.Data) != 1 || hooks.Data[0].Cwd != b.workspace || hooks.Data[0].Hooks == nil || len(hooks.Data[0].Hooks) != 0 || hooks.Data[0].Errors == nil || len(hooks.Data[0].Errors) != 0 || hooks.Data[0].Warnings == nil || len(hooks.Data[0].Warnings) != 0 {
		return restrictionError("disabled native hooks not unambiguously unavailable")
	}
	var plugins struct {
		Marketplaces []json.RawMessage `json:"marketplaces"`
		Errors       []json.RawMessage `json:"marketplaceLoadErrors"`
	}
	if err := c.call("plugin/installed", map[string]any{"cwds": []string{b.workspace}}, &plugins); err != nil {
		return err
	}
	if plugins.Marketplaces == nil || len(plugins.Marketplaces) != 0 || plugins.Errors == nil || len(plugins.Errors) != 0 {
		return restrictionError("disabled native plugin inventory missing, nonempty or failed")
	}
	return nil
}
