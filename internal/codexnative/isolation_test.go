package codexnative

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/BurntSushi/toml"
)

func isolationLayout(t *testing.T) IsolationPaths {
	t.Helper()
	root := t.TempDir()
	var dirs []string
	for _, name := range []string{"workspace", "scratch", "main", "controller", "sibling", "credentials"} {
		dir := filepath.Join(root, name)
		if err := os.Mkdir(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		dirs = append(dirs, dir)
	}
	return IsolationPaths{Workspace: dirs[0], Scratch: dirs[1], MainCheckout: dirs[2], ControllerState: dirs[3], SiblingWorkspaces: dirs[4:5], CredentialPaths: dirs[5:6]}
}

func TestIsolationPathsAndProfiles(t *testing.T) {
	layout := isolationLayout(t)
	layout.Workspace += string(filepath.Separator) + "."
	b, err := prepareIsolation(layout, false)
	if err != nil {
		t.Fatal(err)
	}
	second, err := prepareIsolation(layout, false)
	if err != nil || b.profile() == second.profile() {
		t.Fatal("isolation profile reused an inherited name")
	}
	wantWorkspace, err := filepath.EvalSymlinks(layout.Workspace)
	if err != nil || b.workspace != wantWorkspace {
		t.Fatalf("noncanonical workspace %q; expected %q, %v", b.workspace, wantWorkspace, err)
	}
	for _, reviewer := range []bool{false, true} {
		b.reviewer = reviewer
		args := b.args()
		var config map[string]any
		if _, err := toml.Decode(strings.Join(configOverrides(args), "\n"), &config); err != nil {
			t.Fatal(err)
		}
		profile := config["permissions"].(map[string]any)[b.profile()].(map[string]any)
		fs := profile["filesystem"].(map[string]any)
		want := "write"
		if reviewer {
			want = "read"
		}
		if fs[":root"] != "read" || fs[b.workspace] != want || fs[b.scratch] != "write" || len(fs) != len(b.protected)+3 || profile["network"].(map[string]any)["enabled"] != false || len(profile) != 2 {
			t.Fatalf("unexpected profile rules: %v", profile)
		}
		for _, path := range b.protected {
			if fs[path] != "deny" {
				t.Fatalf("protected path lacks denial: %s", path)
			}
		}
		if config["windows"].(map[string]any)["sandbox"] != "elevated" || config["default_permissions"] != b.profile() {
			t.Fatal("native sandbox/profile not pinned")
		}
		environment := config["shell_environment_policy"].(map[string]any)
		set := environment["set"].(map[string]any)
		if environment["inherit"] != "none" || len(set) != 3 || set["TEMP"] != b.scratch || set["TMP"] != b.scratch || set["TMPDIR"] != b.scratch {
			t.Fatal("every native shell tool must receive only scratch-pinned temp overrides")
		}
	}
}

func configOverrides(args []string) []string {
	var overrides []string
	for i := 3; i < len(args); i += 2 {
		if args[i] != "-c" || i+1 == len(args) {
			panic("invalid test app-server argv")
		}
		overrides = append(overrides, args[i+1])
	}
	return overrides
}

func TestIsolationRejectsUnsafeLayouts(t *testing.T) {
	layout := isolationLayout(t)
	for _, test := range []struct {
		name string
		edit func(*IsolationPaths)
	}{
		{"relative", func(p *IsolationPaths) { p.Workspace = "workspace" }},
		{"missing", func(p *IsolationPaths) { p.Scratch = filepath.Join(p.Scratch, "missing") }},
		{"scratch-equal", func(p *IsolationPaths) { p.Scratch = p.Workspace }},
		{"scratch-under-workspace", func(p *IsolationPaths) { p.Scratch = filepath.Join(p.Workspace, "nested") }},
		{"main-parent", func(p *IsolationPaths) { p.MainCheckout = filepath.Dir(p.Workspace) }},
		{"protected-under-workspace", func(p *IsolationPaths) { p.ControllerState = filepath.Join(p.Workspace, "controller") }},
		{"sibling-equal", func(p *IsolationPaths) { p.SiblingWorkspaces = []string{p.Workspace} }},
		{"escaped", func(p *IsolationPaths) { p.Workspace = filepath.Join(p.Workspace, "..", "main") }},
		{"glob", func(p *IsolationPaths) { p.CredentialPaths = []string{p.CredentialPaths[0] + "*"} }},
		{"missing-credentials", func(p *IsolationPaths) { p.CredentialPaths = nil }},
		{"empty-controller", func(p *IsolationPaths) { p.ControllerState = "" }},
	} {
		t.Run(test.name, func(t *testing.T) {
			copy := layout
			test.edit(&copy)
			if _, err := prepareIsolation(copy, false); !errors.Is(err, ErrIsolationUnavailable) {
				t.Fatalf("unsafe layout accepted: %v", err)
			}
		})
	}
	if runtime.GOOS == "windows" {
		for _, path := range []string{strings.ToLower(layout.Workspace), layout.Workspace + ".", layout.Workspace + " ", layout.Workspace + ":stream", `\\?\` + layout.Workspace, `\\server\share`} {
			copy := layout
			copy.MainCheckout = path
			if _, err := prepareIsolation(copy, false); !errors.Is(err, ErrIsolationUnavailable) {
				t.Fatalf("Windows alias accepted: %q: %v", path, err)
			}
		}
	}
}

func linkIsolationDirectory(t *testing.T, alias, target string) {
	t.Helper()
	if runtime.GOOS == "windows" {
		cmd := exec.Command(filepath.Join(os.Getenv("SYSTEMROOT"), "System32", "cmd.exe"), "/c", "mklink", "/J", alias, target)
		if output, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("task-owned junction fixture: %v: %s", err, output)
		}
	} else if err := os.Symlink(target, alias); err != nil {
		t.Fatal(err)
	}
}

func TestIsolationPathAliasesAndSharedGit(t *testing.T) {
	layout := isolationLayout(t)
	alias := filepath.Join(filepath.Dir(layout.Workspace), "alias")
	linkIsolationDirectory(t, alias, layout.Workspace)
	copy := layout
	copy.MainCheckout = alias
	if _, err := prepareIsolation(copy, false); !errors.Is(err, ErrIsolationUnavailable) {
		canonical, canonicalErr := isolationPath(alias, true)
		t.Fatalf("aliased protected path accepted: %v; alias=%q target=%q canonical=%q error=%v", err, alias, layout.Workspace, canonical, canonicalErr)
	}
	metadata := filepath.Join(layout.MainCheckout, ".git", "worktrees", "worker")
	if err := os.MkdirAll(metadata, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(layout.Workspace, ".git"), []byte("gitdir: "+metadata+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(metadata, "commondir"), []byte("../..\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	b, err := prepareIsolation(layout, false)
	if err != nil {
		t.Fatal(err)
	}
	canonicalMetadata, err := isolationPath(metadata, true)
	if err != nil {
		t.Fatal(err)
	}
	canonicalCommon, err := isolationPath(filepath.Join(layout.MainCheckout, ".git"), true)
	if err != nil || !strings.Contains(strings.Join(b.protected, "\n"), canonicalMetadata) || !strings.Contains(strings.Join(b.protected, "\n"), canonicalCommon) {
		t.Fatal("shared Git metadata did not receive explicit denials")
	}
	if err := os.WriteFile(filepath.Join(layout.Workspace, ".git"), []byte("invalid\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := prepareIsolation(layout, false); !errors.Is(err, ErrIsolationUnavailable) {
		t.Fatalf("invalid Git pointer accepted: %v", err)
	}
}

func TestIsolationEnvironmentsAndModelRefusal(t *testing.T) {
	t.Setenv("ORCH_CONTROLLER_CREDENTIAL", "synthetic-secret")
	t.Setenv("OPENAI_API_KEY", "synthetic-api-key")
	server := serverEnvironment([]string{"PATH=system-path", "USERPROFILE=trusted-host", "CODEX_HOME=trusted-auth", "GH_TOKEN=synthetic", "OTHER_CREDENTIAL=synthetic"}, "scratch")
	if strings.Contains(strings.Join(server, "\n"), "synthetic") || !strings.Contains(strings.Join(server, "\n"), "CODEX_HOME=trusted-auth") {
		t.Fatal("trusted host authentication/environment separation failed")
	}
	b := isolationBoundary{scratch: "scratch"}
	env := b.commandEnvironment()
	for _, name := range []string{"ORCH_CONTROLLER_CREDENTIAL", "OPENAI_API_KEY", "HOME", "USERPROFILE", "CODEX_HOME", "GH_TOKEN", "GITHUB_TOKEN"} {
		value, present := env[name]
		if !present || value != nil {
			t.Fatalf("tool environment did not explicitly unset %s", name)
		}
	}
	for _, version := range []string{"0.159.2", "0.160.0", "77.88.99", "", "future-version"} {
		if err := modelToolBoundary(nil, b, IsolationCapabilities{HostVersion: version}); !errors.Is(err, ErrIsolationUnavailable) {
			t.Fatalf("unverified model-tool boundary accepted: %v", err)
		}
	}
	var c connection
	c.isolation = true
	for _, method := range []string{"thread/start", "thread/resume", "turn/start", "turn/interrupt", "command/exec", "process/spawn", "thread/shellCommand", "windowsSandbox/setupStart", "config/value/write", "config/batchWrite", "mcpServer/tool/call", "app/tool/call", "approval/respond"} {
		if err := c.call(method, nil, nil); err == nil {
			t.Fatalf("isolation connection allowed %s", method)
		}
	}
}

func scriptedIsolationServer() {
	var config map[string]any
	if _, err := toml.Decode(strings.Join(configOverrides(os.Args[1:]), "\n"), &config); err != nil {
		os.Exit(40)
	}
	if os.Getenv("ORCH_CONTROLLER_CREDENTIAL") != "" || os.Getenv("OPENAI_API_KEY") != "" {
		os.Exit(41)
	}
	scratch := os.Getenv("TMPDIR")
	write := func(id string, result any) {
		if err := json.NewEncoder(os.Stdout).Encode(map[string]any{"id": id, "result": result}); err != nil {
			os.Exit(42)
		}
	}
	scenario := ""
	configReads := 0
	scanner := bufio.NewScanner(os.Stdin)
	for scanner.Scan() {
		var request struct {
			ID     string          `json:"id"`
			Method string          `json:"method"`
			Params json.RawMessage `json:"params"`
		}
		if json.Unmarshal(scanner.Bytes(), &request) != nil {
			os.Exit(43)
		}
		switch request.Method {
		case "initialize":
			var params struct {
				ClientInfo struct {
					Version string `json:"version"`
				} `json:"clientInfo"`
				Capabilities struct {
					Experimental bool `json:"experimentalApi"`
				} `json:"capabilities"`
			}
			if json.Unmarshal(request.Params, &params) != nil || !params.Capabilities.Experimental {
				os.Exit(44)
			}
			scenario = params.ClientInfo.Version
			version := "0.159.2"
			if parts := strings.Split(scenario, ":"); len(parts) == 3 && parts[0] == "session" {
				write(request.ID, map[string]any{"userAgent": "orch/0.160.0 (scripted)", "platformFamily": "windows", "platformOs": "windows"})
				_ = json.NewEncoder(os.Stdout).Encode(map[string]any{"method": "account/updated", "params": map[string]string{"authMode": "chatgpt"}})
				scriptedSessionRequests(parts[2], parts[1], scanner)
				return
			}
			if base, observed, ok := strings.Cut(scenario, "@"); ok {
				scenario, version = base, observed
			}
			write(request.ID, map[string]any{"userAgent": "Codex Desktop/" + version, "extraMetadata": "harmless"})
		case "initialized":
		case "config/read":
			configReads++
			var params struct {
				Cwd    string `json:"cwd"`
				Layers bool   `json:"includeLayers"`
			}
			cwd, cwdErr := os.Getwd()
			if json.Unmarshal(request.Params, &params) != nil || cwdErr != nil || !strings.EqualFold(params.Cwd, cwd) || params.Layers {
				os.Exit(47)
			}
			if scenario == "missing-config-method" {
				_ = json.NewEncoder(os.Stdout).Encode(map[string]any{"id": request.ID, "error": map[string]any{"code": -32601, "message": "DO-NOT-LEAK"}})
				continue
			}
			// Emulate user/project table merging: empty tables preserve entries.
			servers := map[string]any{
				"user-server":                      map[string]any{"enabled": true, "secret": "DO-NOT-LEAK"},
				"project.server with \"quotes\" 雪": map[string]any{"enabled": true},
				"already-disabled":                 map[string]any{"enabled": false},
			}
			overrides, restricted := config["mcp_servers"].(map[string]any)
			for name, value := range overrides {
				servers[name] = value
			}
			config["mcp_servers"] = servers
			if enabled, ok := config["features"].(map[string]any)["multi_agent_v2"].(bool); ok {
				config["features"].(map[string]any)["multi_agent_v2"] = map[string]any{"enabled": enabled, "additionalMetadata": 42}
			}
			if restricted {
				switch scenario {
				case "missing-config":
					write(request.ID, map[string]any{"origins": "harmless"})
					continue
				case "missing-mcp":
					delete(config, "mcp_servers")
				case "enabled-mcp":
					servers["user-server"] = map[string]any{"enabled": true}
				case "missing-mcp-enabled":
					servers["user-server"] = map[string]any{"secret": "DO-NOT-LEAK"}
				case "new-mcp":
					servers["late-server"] = map[string]any{"enabled": false}
				case "denied-apps":
					config["features"].(map[string]any)["apps"] = true
				case "missing-hooks":
					delete(config["features"].(map[string]any), "hooks")
				case "malformed-control":
					config["features"].(map[string]any)["plugins"] = 1
				case "malformed-config":
					config["windows"] = 1
				case "missing-multi-agent-enabled":
					config["features"].(map[string]any)["multi_agent_v2"] = map[string]any{"metadata": false}
				case "search-enabled":
					config["web_search"] = "live"
				case "approval-enabled":
					config["approval_policy"] = "on-request"
				case "network-enabled":
					config["permissions"].(map[string]any)[config["default_permissions"].(string)].(map[string]any)["network"] = map[string]any{"enabled": true}
				case "filesystem-conflict":
					config["permissions"].(map[string]any)[config["default_permissions"].(string)].(map[string]any)["filesystem"].(map[string]any)["/unapproved"] = "write"
				case "inherited-environment":
					config["shell_environment_policy"].(map[string]any)["set"] = map[string]any{"secret": "DO-NOT-LEAK"}
				case "missing-model-control":
					delete(config["features"].(map[string]any), "image_generation")
				case "changed-model-control":
					if configReads > 1 {
						config["features"].(map[string]any)["request_permissions_tool"] = true
					}
				case "wrong-provider":
					config["model_provider"] = "other"
				}
			}
			write(request.ID, map[string]any{"config": config, "origins": "ignored metadata", "extra": true})
		case "experimentalFeature/list":
			if scenario == "missing-feature-method" {
				_ = json.NewEncoder(os.Stdout).Encode(map[string]any{"id": request.ID, "error": map[string]any{"code": -32601, "message": "DO-NOT-LEAK"}})
				continue
			}
			var params struct {
				Cursor *string `json:"cursor"`
			}
			if json.Unmarshal(request.Params, &params) != nil {
				os.Exit(48)
			}
			var data []any
			for i, name := range append(append([]string(nil), restrictedFeatures...), modelRestrictedFeatures...) {
				if (params.Cursor == nil && i >= 6) || (params.Cursor != nil && i < 6) || (scenario == "unsupported-feature" && name == "plugins") {
					continue
				}
				if scenario == "unsupported-model-control" && name == "goals" {
					continue
				}
				entry := map[string]any{"name": name, "enabled": false, "metadata": "harmless"}
				if scenario == "conflicting-feature" && name == "hooks" {
					entry["enabled"] = true
				}
				if scenario == "missing-feature-enabled" && name == "plugins" {
					delete(entry, "enabled")
				}
				data = append(data, entry)
			}
			data = append(data, map[string]any{"name": "unrelated-feature", "metadata": "ignored"})
			var next *string
			if params.Cursor == nil || scenario == "repeated-feature-cursor" {
				value := "second-page"
				next = &value
			}
			write(request.ID, map[string]any{"data": data, "nextCursor": next})
		case "hooks/list":
			if scenario == "missing-inventory-method" {
				_ = json.NewEncoder(os.Stdout).Encode(map[string]any{"id": request.ID, "error": map[string]any{"code": -32601, "message": "DO-NOT-LEAK"}})
				continue
			}
			cwd, _ := os.Getwd()
			entry := map[string]any{"cwd": cwd, "hooks": []any{}, "errors": []any{}, "warnings": []any{}}
			switch scenario {
			case "missing-hook-inventory":
				delete(entry, "hooks")
			case "enabled-hook":
				entry["hooks"] = []any{map[string]any{"command": "DO-NOT-LEAK"}}
			case "hook-errors":
				entry["errors"] = []any{"DO-NOT-LEAK"}
			case "hook-warnings":
				entry["warnings"] = []any{"DO-NOT-LEAK"}
			case "hook-wrong-cwd":
				entry["cwd"] = filepath.Dir(cwd)
			}
			write(request.ID, map[string]any{"data": []any{entry}})
		case "plugin/installed":
			inventory := map[string]any{"marketplaces": []any{}, "marketplaceLoadErrors": []any{}}
			switch scenario {
			case "missing-plugin-inventory":
				delete(inventory, "marketplaces")
			case "enabled-plugin":
				inventory["marketplaces"] = []any{map[string]any{"name": "DO-NOT-LEAK"}}
			case "plugin-errors":
				inventory["marketplaceLoadErrors"] = []any{"DO-NOT-LEAK"}
			}
			write(request.ID, inventory)
		case "windowsSandbox/readiness":
			switch scenario {
			case "missing-method":
				_ = json.NewEncoder(os.Stdout).Encode(map[string]any{"id": request.ID, "error": map[string]any{"code": -32601, "message": "DO-NOT-LEAK"}})
			case "missing-readiness":
				write(request.ID, map[string]any{})
			case "not-ready":
				write(request.ID, map[string]any{"status": "notConfigured"})
			default:
				write(request.ID, map[string]any{"status": "ready"})
			}
		case "permissionProfile/list":
			var data any = []any{map[string]any{"id": config["default_permissions"], "allowed": true}}
			switch scenario {
			case "denied-profile":
				data = []any{map[string]any{"id": config["default_permissions"], "allowed": false}}
			case "missing-profile":
				data = []any{}
			case "missing-allowed":
				data = []any{map[string]any{"id": config["default_permissions"]}}
			case "duplicate-profile":
				data = []any{map[string]any{"id": config["default_permissions"], "allowed": true}, map[string]any{"id": config["default_permissions"], "allowed": true}}
			}
			write(request.ID, map[string]any{"data": data})
		case "command/exec":
			if scenario != "success" && scenario != "excessive-output" {
				os.Exit(49) // a refused control must stop before commands
			}
			if scenario == "excessive-output" {
				write(request.ID, map[string]any{"exitCode": 0, "stdout": strings.Repeat("x", maxMessageBytes), "stderr": ""})
				continue
			}
			write(request.ID, map[string]any{"exitCode": 0, "stdout": "", "stderr": ""})
		default:
			os.Exit(45) // forbids thread/model, setup, login and unsandboxed APIs
		}
	}
	if err := os.WriteFile(filepath.Join(scratch, "server-closed"), []byte("closed"), 0o600); err != nil {
		os.Exit(46)
	}
}

func TestIsolationCapabilitiesAndFailureCleanup(t *testing.T) {
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("ORCH_CONTROLLER_CREDENTIAL", "synthetic-secret")
	t.Run("public-fail-closed", func(t *testing.T) {
		for _, reviewer := range []bool{false, true} {
			layout := isolationLayout(t)
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			capabilities, err := IsolationPreflight(ctx, Options{Executable: executable, Dir: layout.Workspace, ClientVersion: "success"}, layout, reviewer)
			cancel()
			if !errors.Is(err, ErrIsolationUnavailable) {
				t.Fatalf("public isolation preflight accepted model execution: %v", err)
			}
			if runtime.GOOS == "windows" && (!capabilities.SandboxReady || !capabilities.ProfileAllowed || !capabilities.ToolsDisabled || !strings.Contains(err.Error(), "disabledPluginIds")) {
				t.Fatalf("readiness/profile availability bypassed specific tool refusal: %+v, %v", capabilities, err)
			}
		}
	})
	for _, scenario := range []string{"success", "success@0.160.0", "success@77.88.99-beta.2", "missing-method", "missing-method@77.88.99", "missing-readiness", "not-ready", "denied-profile", "missing-profile", "missing-allowed", "duplicate-profile", "excessive-output", "missing-config-method", "missing-config", "missing-mcp", "enabled-mcp", "missing-mcp-enabled", "new-mcp", "denied-apps", "missing-hooks", "malformed-control", "malformed-config", "missing-multi-agent-enabled", "search-enabled", "approval-enabled", "network-enabled", "filesystem-conflict", "inherited-environment", "missing-feature-method", "unsupported-feature", "conflicting-feature", "missing-feature-enabled", "repeated-feature-cursor", "success@not-a-version"} {
		t.Run(scenario, func(t *testing.T) {
			layout := isolationLayout(t)
			preserved := filepath.Join(layout.Workspace, "existing-work")
			if err := os.WriteFile(preserved, []byte("preserve"), 0o600); err != nil {
				t.Fatal(err)
			}
			b, err := prepareIsolation(layout, false)
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			c, capabilities, err := openIsolation(ctx, Options{Executable: executable, Dir: layout.Workspace, ClientVersion: scenario}, b)
			if (strings.HasPrefix(scenario, "success") && scenario != "success@not-a-version") || scenario == "excessive-output" {
				if err != nil || !capabilities.SandboxReady || !capabilities.ProfileAllowed || !capabilities.ToolsDisabled || capabilities.DisabledMCP != 3 {
					t.Fatalf("configuration availability: %+v, %v", capabilities, err)
				}
				_, version, versionChanged := strings.Cut(scenario, "@")
				if versionChanged && capabilities.HostVersion != version {
					t.Fatal("observed host version was not retained")
				}
				if scenario != "excessive-output" {
					if code, err := c.diagnosticCommand(b, []string{"fixture"}, false); err != nil || code != 0 {
						t.Fatalf("compatible diagnostic command refused: %v", err)
					}
				}
				if scenario == "excessive-output" {
					_, err = c.diagnosticCommand(b, []string{"fixture"}, false)
					if !errors.Is(err, ErrMalformedMessage) {
						t.Fatalf("unbounded command output accepted: %v", err)
					}
				}
				if err := c.close(); err != nil {
					t.Fatal(err)
				}
				select {
				case <-c.exited:
				default:
					t.Fatal("owned app-server was not reaped")
				}
			} else if err == nil || c != nil || strings.Contains(err.Error(), "DO-NOT-LEAK") {
				t.Fatalf("missing/denied capability was accepted: %+v, %v", capabilities, err)
			}
			if strings.HasPrefix(scenario, "missing-method") && !strings.Contains(err.Error(), "windowsSandbox/readiness rejected") {
				t.Fatalf("version hid the specific missing capability: %v", err)
			}
			if data, err := os.ReadFile(preserved); err != nil || string(data) != "preserve" {
				t.Fatal("failure cleanup modified existing work")
			}
			if data, err := os.ReadFile(filepath.Join(layout.Scratch, "server-closed")); err != nil || string(data) != "closed" {
				t.Fatalf("server did not close on failure: %v", err)
			}
		})
	}
}

func TestModelToolAdmission(t *testing.T) {
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	for _, reviewer := range []bool{false, true} {
		for _, scenario := range []string{"success", "missing-model-control", "changed-model-control", "unsupported-model-control", "wrong-provider", "missing-inventory-method", "missing-hook-inventory", "enabled-hook", "hook-errors", "hook-warnings", "hook-wrong-cwd", "missing-plugin-inventory", "enabled-plugin", "plugin-errors"} {
			t.Run(fmt.Sprintf("reviewer=%v/%s", reviewer, scenario), func(t *testing.T) {
				layout := isolationLayout(t)
				b, err := prepareIsolation(layout, reviewer)
				if err != nil {
					t.Fatal(err)
				}
				ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
				defer cancel()
				options := Options{Executable: executable, Dir: layout.Workspace, ClientVersion: scenario + "@0.160.0"}
				c, caps, err := openIsolation(ctx, options, b)
				if err != nil {
					t.Fatal(err) // diagnostic compatibility remains independent
				}
				defer func() {
					if err := c.close(); err != nil {
						t.Error(err)
					}
				}()
				err = modelToolBoundary(c, b, caps)
				if (scenario == "success") != (err == nil) || (err != nil && strings.Contains(err.Error(), "DO-NOT-LEAK")) {
					t.Fatalf("actual-connection admission: %+v %v", caps, err)
				}
				if _, err := c.request("turn/start", nil); err == nil || c.session {
					t.Fatal("isolation evidence enabled a metadata-only model turn")
				}
				if scenario == "success" {
					changed := b
					changed.nonce = "changed"
					if err := modelToolBoundary(c, changed, caps); !errors.Is(err, ErrIsolationUnavailable) {
						t.Fatal("another boundary reused connection evidence")
					}
					caps.ToolsDisabled = false
					if err := modelToolBoundary(c, b, caps); !errors.Is(err, ErrIsolationUnavailable) {
						t.Fatal("version/profile alone granted admission")
					}
				}
			})
		}
	}
	if runtime.GOOS == "windows" {
		for _, reviewer := range []bool{false, true} {
			layout := isolationLayout(t)
			ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
			caps, err := IsolationPreflight(ctx, Options{Executable: executable, Dir: layout.Workspace, ClientVersion: "success@0.160.0"}, layout, reviewer)
			cancel()
			if err != nil || !caps.ModelToolsVerified {
				t.Fatalf("eligible public isolation preflight: %+v %v", caps, err)
			}
		}
	}
}

// The same test executable is the synthetic native-sandbox payload. It only
// touches explicitly supplied test-owned fixtures; it has no model or network.
func isolationFixture(args []string) int {
	if len(args) != 2 {
		return 20
	}
	var err error
	switch args[0] {
	case "read":
		_, err = os.ReadFile(args[1])
	case "write":
		err = os.WriteFile(args[1], []byte("written"), 0o600)
	case "cancel":
		if err := os.WriteFile(args[1]+".started", []byte("started"), 0o600); err != nil {
			return 21
		}
		executable, executableErr := os.Executable()
		if executableErr != nil {
			return 25
		}
		child := exec.Command(executable, "orch-isolation-fixture", "delayed-child", args[1]+".child")
		child.Stdout, child.Stderr = os.Stdout, os.Stderr
		if err := child.Start(); err != nil {
			return 26
		}
		_, _ = fmt.Fprintln(os.Stdout, "synthetic command started")
		if err := child.Wait(); err != nil {
			return 27
		}
		err = os.WriteFile(args[1], []byte("not canceled"), 0o600)
	case "delayed-child":
		if err := os.WriteFile(args[1]+".started", []byte("started"), 0o600); err != nil {
			return 28
		}
		time.Sleep(5 * time.Second)
		err = os.WriteFile(args[1], []byte("not canceled"), 0o600)
	case "environment":
		for _, name := range []string{"ORCH_CONTROLLER_CREDENTIAL", "OPENAI_API_KEY", "GH_TOKEN", "GITHUB_TOKEN", "CODEX_HOME", "HOME", "USERPROFILE"} {
			if os.Getenv(name) != "" {
				return 22
			}
		}
		if os.Getenv("TEMP") != args[1] || os.Getenv("TMPDIR") != args[1] {
			return 23
		}
	default:
		return 24
	}
	if errors.Is(err, os.ErrPermission) {
		return 13
	}
	if err != nil {
		return 14
	}
	return 0
}

func TestIsolationFixtureCancellationWriteError(t *testing.T) {
	// The child succeeds, then the final marker write fails because its target
	// is a directory. The cancel case must report that error, not the shadowed
	// executable lookup's nil error.
	target := filepath.Join(t.TempDir(), "marker-directory")
	if err := os.Mkdir(target, 0o755); err != nil {
		t.Fatal(err)
	}
	code := isolationFixture([]string{"cancel", target})
	if code != 13 && code != 14 {
		t.Fatalf("final cancellation marker write failure returned %d, want a filesystem error", code)
	}
	if data, err := os.ReadFile(target + ".child"); err != nil || string(data) != "not canceled" {
		t.Fatalf("child did not finish before final marker failure: %v", err)
	}
}
