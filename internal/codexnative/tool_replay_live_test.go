//go:build codex_live

package codexnative

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"net"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/kninetimmy/orch/internal/execx"
)

func TestCodexModelToolIsolationSmoke(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Fatal("model-tool validation limitation: native Windows elevated sandbox required")
	}
	installed, err := exec.LookPath("codex.exe")
	if err != nil {
		installed = filepath.Join(os.Getenv("LOCALAPPDATA"), "Programs", "OpenAI", "Codex", "bin", "codex.exe")
		if info, err := os.Stat(installed); err != nil || !info.Mode().IsRegular() {
			t.Fatal("model-tool validation limitation: installed codex.exe unavailable")
		}
	}
	layout := isolationLayout(t)
	hidden := filepath.Join(layout.ControllerState, "hidden-grading")
	metadata := filepath.Join(layout.MainCheckout, ".git", "worktrees", "synthetic-worker")
	for _, dir := range []string{hidden, metadata} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	for path, data := range map[string]string{
		filepath.Join(metadata, "commondir"):    "../..\n",
		filepath.Join(layout.Workspace, ".git"): "gitdir: " + metadata + "\n",
	} {
		if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	protected := []string{layout.MainCheckout, layout.SiblingWorkspaces[0], layout.ControllerState, hidden, layout.CredentialPaths[0], metadata, filepath.Join(layout.MainCheckout, ".git")}
	preserved := map[string][32]byte{}
	for _, dir := range append(append([]string{layout.Workspace, layout.Scratch}, protected...), "") {
		if dir == "" {
			continue
		}
		text := "preserve"
		if dir == layout.Workspace || dir == layout.Scratch {
			text = "written"
		}
		path := filepath.Join(dir, "sentinel")
		if err := os.WriteFile(path, []byte(text), 0o600); err != nil {
			t.Fatal(err)
		}
		if text == "preserve" {
			preserved[path] = sha256.Sum256([]byte(text))
		}
		picture := image.NewRGBA(image.Rect(0, 0, 2, 2))
		picture.Set(0, 0, color.RGBA{R: 90, A: 255})
		path = filepath.Join(dir, "sentinel.png")
		file, err := os.Create(path)
		if err != nil {
			t.Fatal(err)
		}
		if err := errors.Join(png.Encode(file, picture), file.Close()); err != nil {
			t.Fatal(err)
		}
		if text == "preserve" {
			data, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			preserved[path] = sha256.Sum256(data)
		}
	}
	alias := filepath.Join(layout.Workspace, "protected-alias")
	linkIsolationDirectory(t, alias, layout.MainCheckout)
	for i, dir := range protected {
		protected[i], err = isolationPath(dir, true)
		if err != nil {
			t.Fatal(err)
		}
	}
	protected = append(protected, alias)
	var forbidden []string
	for _, dir := range protected {
		for _, name := range []string{"shell-forbidden-new", "patch-forbidden-new"} {
			forbidden = append(forbidden, filepath.Join(dir, name))
		}
	}
	t.Cleanup(func() {
		if err := verifyReplayFiles(preserved, forbidden); err != nil {
			t.Error(err)
		}
	})
	t.Setenv("ORCH_CONTROLLER_CREDENTIAL", "synthetic-controller-credential")
	t.Setenv("OPENAI_API_KEY", "synthetic-api-key")
	ctx, cancel := context.WithTimeout(context.Background(), 170*time.Second) // reserve ten seconds for owned shutdown
	defer cancel()
	boundary, err := prepareIsolation(layout, false)
	if err != nil {
		t.Fatal(err)
	}
	hookConfig, hookMarker, err := replayHookSource(ctx, installed, boundary)
	if err != nil {
		t.Fatal(err)
	}
	forbidden = append(forbidden, hookMarker)
	t.Log("native metadata recognized the session-only matched hook and accepted its exact trust hash; no hook execution control")
	var observedMCP replayTool
	for _, role := range []string{"canary", "worker", "reviewer"} {
		reviewer := role == "reviewer"
		b, err := prepareIsolation(layout, reviewer)
		if err != nil {
			t.Fatal(err)
		}
		canary := &replayMCP{}
		r := &toolReplay{canary: canary}
		server := httptest.NewServer(r)
		endpoint, err := url.Parse(server.URL)
		if err != nil {
			t.Fatal(err)
		}
		positive, err := net.DialTimeout("tcp", endpoint.Host, time.Second)
		if err != nil {
			t.Fatal("replay limitation: loopback network negative control is not reachable from parent")
		}
		if err := positive.Close(); err != nil {
			t.Fatal(err)
		}
		r.plan, r.advance = modelToolReplayPlan(t, b, protected, endpoint.Host)
		if role == "canary" {
			r.plan = func(tools []replayTool) ([]replayCall, error) {
				for _, tool := range tools {
					if tool.Kind == "tool_search" {
						return []replayCall{{ID: "mcp-search", Tool: tool, Args: `{"limit":5,"query":"orch_replay_canary read_synthetic"}`, Want: "canary-discovery"}}, nil
					}
				}
				return nil, errors.New("replay limitation: configured MCP positive control not discoverable")
			}
			r.advance = func(outputs map[string]json.RawMessage) ([]replayCall, error) {
				if _, ok := outputs["mcp-positive"]; ok {
					return nil, nil
				}
				tools, err := advertisedReplayTools(outputs["mcp-search"])
				if err != nil {
					return nil, err
				}
				for _, tool := range tools {
					if strings.Contains(tool.Namespace+tool.Name, "orch_replay_canary") && strings.HasSuffix(tool.Name, "read_synthetic") {
						observedMCP = tool
					}
				}
				if observedMCP.Name == "" {
					return nil, errors.New("replay limitation: native MCP discovery did not return the configured canary")
				}
				return []replayCall{{ID: "mcp-positive", Tool: observedMCP, Args: `{}`, Want: "mcp-dispatch"}}, nil
			}
		} else {
			plan := r.plan
			r.plan = func(tools []replayTool) ([]replayCall, error) {
				calls, err := plan(tools)
				if err != nil {
					return nil, err
				}
				return append(calls, replayCall{ID: "inherited-mcp-denied", Tool: observedMCP, Args: `{}`, Want: "unavailable"}), nil
			}
		}
		func() {
			defer server.Close()
			c, err := openToolReplay(ctx, installed, b, server.URL, hookConfig)
			if err != nil {
				t.Fatal(err)
			}
			var thread struct {
				Thread struct {
					ID string `json:"id"`
				} `json:"thread"`
			}
			params := map[string]any{"cwd": b.workspace, "model": "gpt-5.5", "modelProvider": "orch_replay", "approvalPolicy": "never", "permissions": b.profile(), "ephemeral": true, "selectedCapabilityRoots": []any{}, "allowProviderModelFallback": false}
			if role == "canary" {
				params["config"] = map[string]any{"mcp_servers.orch_replay_canary.enabled": true}
			}
			if err := c.call("thread/start", params, &thread); err != nil {
				_ = c.close()
				t.Fatal(err)
			}
			var turn map[string]any
			if thread.Thread.ID == "" {
				_ = c.close()
				t.Fatal("replay limitation: missing native thread identity")
			}
			if err := c.call("turn/start", map[string]any{"threadId": thread.Thread.ID, "permissions": b.profile(), "approvalPolicy": "never", "input": []any{map[string]any{"type": "text", "text": "Run only the synthetic local replay actions."}}}, &turn); err != nil {
				_ = c.close()
				t.Fatal(err)
			}
			for {
				m, err := c.read()
				if err != nil {
					_ = c.close()
					t.Fatal(err)
				}
				var method string
				if json.Unmarshal(m.Method, &method) != nil {
					_ = c.close()
					t.Fatal("replay limitation: unsolicited native reply")
				}
				if strings.HasPrefix(method, "hook/") {
					_ = c.close()
					t.Fatal("replay limitation: disabled hook emitted a native execution event")
				}
				if method == "turn/completed" {
					var completed struct {
						Turn struct {
							Status string `json:"status"`
							Error  *struct {
								Info json.RawMessage `json:"codexErrorInfo"`
							} `json:"error"`
						} `json:"turn"`
					}
					if json.Unmarshal(m.Params, &completed) != nil || completed.Turn.Status != "completed" {
						_ = c.close()
						t.Fatalf("replay limitation: native turn failed: status=%s replay=%v requests=%d advertised=%v", completed.Turn.Status, r.err, r.requests, r.tools)
					}
					break
				}
			}
			if err := r.validate(c.close()); err != nil {
				for _, call := range r.calls {
					if validateReplayOutput(call, r.outputs[call.ID]) != nil && call.Want != "image" {
						output := r.outputs[call.ID]
						if len(output) > 2048 {
							output = output[:2048]
						}
						t.Logf("synthetic failing call %s tool=%s output=%s", call.ID, call.Tool.Name, output)
					}
				}
				t.Fatal(err)
			}
			t.Logf("native dispatched tools: role=%s tools=%v requests=%d outputs=%d; synthetic replay only", b.profile(), r.tools, r.requests, len(r.outputs))
			t.Log("actual replay child: configured installed plugin source retained; native hook and plugin inventories empty, feature gates disabled")
			if role == "canary" {
				if canary.err != nil || canary.listed == 0 || canary.called != 0 {
					t.Fatal("replay limitation: native MCP dispatch control did not retain never approval")
				}
				t.Logf("paired MCP source control: native discovered=%s.%s listed=%d called=%d; dispatcher rejected by approval never", observedMCP.Namespace, observedMCP.Name, canary.listed, canary.called)
				return
			}
			if canary.err != nil || canary.called != 0 || canary.listed != 0 {
				t.Fatal("replay limitation: disabled inherited MCP canary reached server")
			}
			for _, name := range []string{"patch-scratch-" + b.profile(), "exec-executed", "stdin-executed"} {
				if _, err := os.Stat(filepath.Join(b.scratch, name)); err != nil {
					t.Fatal("replay limitation: permitted native action did not leave its parent-verifiable marker")
				}
			}
			path := filepath.Join(b.workspace, "patch-workspace-"+b.profile())
			if reviewer {
				forbidden = append(forbidden, path)
			} else if _, err := os.Stat(path); err != nil {
				t.Fatal("replay limitation: permitted workspace patch did not execute")
			}
			if err := verifyReplayFiles(preserved, forbidden); err != nil {
				t.Fatal(err)
			}
		}()
	}
	// This replay never enables production model turns or proves live inference.
}

func modelToolReplayPlan(t *testing.T, b isolationBoundary, protected []string, network string) (func([]replayTool) ([]replayCall, error), func(map[string]json.RawMessage) ([]replayCall, error)) {
	t.Helper()
	toolsByName := map[string]replayTool{}
	encode := func(value any) string {
		data, err := json.Marshal(value)
		if err != nil {
			t.Fatal(err)
		}
		return string(data)
	}
	payload, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	quote := func(value string) string { return "'" + strings.ReplaceAll(value, "'", "''") + "'" }
	shell := filepath.Join(os.Getenv("SYSTEMROOT"), "System32", "WindowsPowerShell", "v1.0", "powershell.exe")
	fixture := func(mode string) string {
		request := replayFixtureRequest{Scratch: b.scratch, Network: network, Marker: filepath.Join(b.scratch, mode+"-executed")}
		if mode == "direct" {
			request.Marker = filepath.Join(b.scratch, "exec-executed")
		}
		for _, dir := range append([]string{b.workspace, b.scratch}, protected...) {
			probe := struct {
				Dir   string `json:"dir"`
				Read  bool   `json:"read"`
				Write bool   `json:"write"`
			}{Dir: dir, Read: dir == b.workspace || dir == b.scratch, Write: dir == b.scratch || (dir == b.workspace && !b.reviewer)}
			request.Probes = append(request.Probes, probe)
		}
		data := base64.RawURLEncoding.EncodeToString([]byte(encode(request)))
		return "& " + quote(payload) + " orch-modeltool-fixture " + mode + " " + quote(data)
	}
	plan := func(tools []replayTool) ([]replayCall, error) {
		for _, tool := range tools {
			if tool.Namespace != "" && tool.Namespace != "functions" {
				return nil, fmt.Errorf("replay limitation: unexpected namespace %s", tool.Namespace)
			}
			if !slices.Contains([]string{"apply_patch", "exec_command", "write_stdin", "view_image", "update_plan", "request_user_input"}, tool.Name) {
				return nil, fmt.Errorf("replay limitation: unexpected tool %s", tool.Name)
			}
			toolsByName[tool.Name] = tool
		}
		for _, name := range []string{"apply_patch", "exec_command", "write_stdin", "view_image"} {
			if toolsByName[name].Name == "" {
				return nil, fmt.Errorf("replay limitation: required filesystem tool %s unavailable", name)
			}
		}
		if toolsByName["apply_patch"].Kind != "custom" {
			return nil, errors.New("replay limitation: native patch grammar changed")
		}
		calls := []replayCall{}
		add := func(id, name, args, want string) {
			calls = append(calls, replayCall{ID: id, Tool: toolsByName[name], Args: args, Want: want})
		}
		patch := func(path string) string {
			return "*** Begin Patch\n*** Add File: " + filepath.ToSlash(path) + "\n+written\n*** End Patch"
		}
		workspaceWant := "patch"
		if b.reviewer {
			workspaceWant = "patch-denied"
		}
		add("patch-workspace", "apply_patch", patch(filepath.Join(b.workspace, "patch-workspace-"+b.profile())), workspaceWant)
		add("patch-scratch", "apply_patch", patch(filepath.Join(b.scratch, "patch-scratch-"+b.profile())), "patch")
		add("image-workspace", "view_image", encode(map[string]string{"path": filepath.Join(b.workspace, "sentinel.png")}), "image")
		add("image-scratch", "view_image", encode(map[string]string{"path": filepath.Join(b.scratch, "sentinel.png")}), "image")
		for i, dir := range protected {
			add(fmt.Sprintf("patch-read-denied-%d", i), "apply_patch", "*** Begin Patch\n*** Update File: "+filepath.ToSlash(filepath.Join(dir, "sentinel"))+"\n@@\n-preserve\n+changed\n*** End Patch", "patch-read-denied")
			add(fmt.Sprintf("patch-write-denied-%d", i), "apply_patch", patch(filepath.Join(dir, "patch-forbidden-new")), "patch-denied")
			add(fmt.Sprintf("image-denied-%d", i), "view_image", encode(map[string]string{"path": filepath.Join(dir, "sentinel.png")}), "image-denied")
		}
		add("exec-boundary", "exec_command", encode(map[string]any{"cmd": fixture("direct"), "shell": shell, "login": false, "yield_time_ms": 10000, "max_output_tokens": 3000}), "fixture")
		add("escalation-denied", "exec_command", encode(map[string]any{"cmd": "exit 0", "shell": shell, "login": false, "sandbox_permissions": "require_escalated", "justification": "synthetic forbidden escalation"}), "escalation-denied")
		for _, name := range []string{"spawn_agent", "request_permissions", "list_mcp_resources", "list_mcp_resource_templates", "read_mcp_resource"} {
			calls = append(calls, replayCall{ID: "unavailable-" + name, Tool: replayTool{Name: name, Kind: "function"}, Args: `{}`, Want: "unavailable"})
		}
		return calls, nil
	}
	advance := func(outputs map[string]json.RawMessage) ([]replayCall, error) {
		if _, ok := outputs["stdin-boundary"]; ok {
			return nil, nil
		}
		if _, ok := outputs["stdin-start"]; !ok {
			return []replayCall{{ID: "stdin-start", Tool: toolsByName["exec_command"], Args: encode(map[string]any{"cmd": fixture("stdin"), "shell": shell, "login": false, "tty": true, "yield_time_ms": 1000, "max_output_tokens": 3000}), Want: "running"}}, nil
		}
		var text string
		if json.Unmarshal(outputs["stdin-start"], &text) != nil {
			return nil, errors.New("replay limitation: malformed unified exec process evidence")
		}
		match := regexp.MustCompile(`Process running with session ID ([0-9]+)`).FindStringSubmatch(text)
		if len(match) != 2 {
			return nil, errors.New("replay limitation: advertised write_stdin has no positively started native process")
		}
		var session int64
		if json.Unmarshal([]byte(match[1]), &session) != nil {
			return nil, errors.New("replay limitation: malformed native process identity")
		}
		return []replayCall{{ID: "stdin-boundary", Tool: toolsByName["write_stdin"], Args: encode(map[string]any{"session_id": session, "chars": "run\n", "yield_time_ms": 10000, "max_output_tokens": 3000}), Want: "fixture"}}, nil
	}
	return plan, advance
}

func replayArgs(b isolationBoundary, endpoint string) []string {
	return append(b.args(), "-c", `cli_auth_credentials_store="ephemeral"`, "-c", "chatgpt_base_url="+quoteTOML(endpoint),
		"-c", "openai_base_url="+quoteTOML(endpoint+"/v1"), "-c", `model_provider="orch_replay"`, "-c", `model="gpt-5.5"`,
		"-c", `model_providers.orch_replay={name="Synthetic loopback replay",base_url=`+quoteTOML(endpoint+"/v1")+`,wire_api="responses",requires_openai_auth=false,http_headers={Authorization="Bearer orch-synthetic-replay"},request_max_retries=0,stream_max_retries=0}`,
		"-c", `features.enable_request_compression=false`, "-c", `features.responses_websockets=false`, "-c", `features.responses_websockets_v2=false`,
		"-c", `features.goals=false`, "-c", `features.request_permissions_tool=false`, "-c", `features.exec_permission_approvals=false`, "-c", `features.image_generation=false`,
		"-c", `ephemeral=true`, "-c", "log_dir="+quoteTOML(filepath.Join(b.scratch, "native-log")), "-c", "sqlite_home="+quoteTOML(filepath.Join(b.scratch, "native-state")))
}

func openReplayChild(ctx context.Context, executable string, b isolationBoundary, args []string) (*connection, error) {
	env := serverEnvironment(os.Environ(), b.scratch)
	c, err := startWithEnv(ctx, execx.Cmd{Name: executable, Args: args, Dir: b.workspace}, env)
	if err != nil {
		return nil, err
	}
	c.isolation, c.profile = true, b.profile()
	var response struct {
		UserAgent string `json:"userAgent"`
	}
	if err := c.call("initialize", map[string]any{"clientInfo": map[string]string{"name": "orch", "version": "synthetic-tool-replay"}, "capabilities": map[string]bool{"experimentalApi": true}}, &response); err != nil {
		return nil, errors.Join(err, c.close())
	}
	if !hostVersion.MatchString(response.UserAgent) {
		return nil, errors.Join(errors.New("replay limitation: native host version missing"), c.close())
	}
	if err := c.initialized(); err != nil {
		return nil, errors.Join(err, c.close())
	}
	return c, nil
}

func openToolReplay(ctx context.Context, executable string, b isolationBoundary, endpoint, hookConfig string) (*connection, error) {
	args := append(replayArgs(b, endpoint), "-c", hookConfig, "-c", `mcp_servers.orch_replay_canary={url=`+quoteTOML(endpoint+"/mcp")+`,enabled=true}`)
	// The first child performs bounded read-only MCP discovery with ephemeral auth.
	c, err := openReplayChild(ctx, executable, b, args)
	if err != nil {
		return nil, err
	}
	config, readErr := readIsolationConfig(c, b.workspace)
	if err := errors.Join(readErr, c.close()); err != nil {
		return nil, err
	}
	servers, err := config.serverNames()
	if err != nil {
		return nil, err
	}
	restricted := append(disableMCP(args, servers), "-c", "mcp_servers.orch_replay_canary.url="+quoteTOML(endpoint+"/mcp"))
	c, err = openReplayChild(ctx, executable, b, restricted)
	if err != nil {
		return nil, err
	}
	config, err = readIsolationConfig(c, b.workspace)
	if err == nil {
		err = config.verify(b, servers)
	}
	if err == nil {
		err = verifyRestrictedFeatures(c)
	}
	if err == nil {
		err = replayDisabledCapabilities(c, b)
	}
	var readiness struct {
		Status string `json:"status"`
	}
	if err == nil {
		err = c.call("windowsSandbox/readiness", struct{}{}, &readiness)
	}
	if err == nil && readiness.Status != "ready" {
		err = errors.New("replay limitation: existing elevated sandbox unavailable; no provisioning allowed")
	}
	if err != nil {
		return nil, errors.Join(err, c.close())
	}
	c.session = true // test-only native replay; production modelToolBoundary is unchanged
	return c, nil
}

// These inventory calls stay test-only; production transport admission is unchanged.
func replayMetadata(c *connection, method string, params, result any) error {
	if method != "hooks/list" && method != "plugin/installed" {
		return errors.New("replay limitation: unsupported metadata method")
	}
	c.sequence++
	id := fmt.Sprintf("orch-replay-metadata-%d", c.sequence)
	if err := c.send(map[string]any{"id": id, "method": method, "params": params}); err != nil {
		return err
	}
	for {
		m, err := c.read()
		if err != nil {
			return err
		}
		if len(m.Method) == 0 {
			return decodeResponse(m, id, method, result)
		}
	}
}

type replayHookInventory struct {
	Data []struct {
		Cwd   string `json:"cwd"`
		Hooks []struct {
			Key         string `json:"key"`
			Source      string `json:"source"`
			CurrentHash string `json:"currentHash"`
			TrustStatus string `json:"trustStatus"`
			Enabled     *bool  `json:"enabled"`
			IsManaged   *bool  `json:"isManaged"`
			Matcher     string `json:"matcher"`
			EventName   string `json:"eventName"`
			HandlerType string `json:"handlerType"`
			Command     string `json:"command"`
		} `json:"hooks"`
		Errors   []json.RawMessage `json:"errors"`
		Warnings []json.RawMessage `json:"warnings"`
	} `json:"data"`
}

func replayHookSource(ctx context.Context, executable string, b isolationBoundary) (string, string, error) {
	marker := filepath.Join(b.scratch, "hook-forbidden-executed")
	command := `powershell.exe -NoProfile -NonInteractive -Command "Set-Content -LiteralPath '` + strings.ReplaceAll(marker, "'", "''") + `' -Value executed"`
	base := `PreToolUse=[{matcher=".*",hooks=[{type="command",command=` + quoteTOML(command) + `,timeout=1}]}]`
	config := "hooks={" + base + "}"
	var key, hash string
	for _, trusted := range []bool{false, true} {
		args := append(replayArgs(b, "http://127.0.0.1:1"), "-c", "features.hooks=true", "-c", config)
		c, err := openReplayChild(ctx, executable, b, args)
		if err != nil {
			return "", marker, err
		}
		var inventory replayHookInventory
		err = replayMetadata(c, "hooks/list", map[string]any{"cwds": []string{b.workspace}}, &inventory)
		if err = errors.Join(err, c.close()); err != nil {
			return "", marker, err
		}
		if len(inventory.Data) != 1 || inventory.Data[0].Cwd != b.workspace || inventory.Data[0].Hooks == nil || inventory.Data[0].Errors == nil || len(inventory.Data[0].Errors) != 0 {
			return "", marker, errors.New("replay limitation: hook source inventory missing, ambiguous or failed")
		}
		found := 0
		for _, hook := range inventory.Data[0].Hooks {
			if hook.Command != command {
				continue // inherited metadata is never logged or executed
			}
			found++
			if hook.Key == "" || !strings.HasPrefix(hook.CurrentHash, "sha256:") || hook.Source != "sessionFlags" || hook.EventName != "preToolUse" || hook.Matcher != ".*" || hook.HandlerType != "command" || hook.IsManaged == nil || *hook.IsManaged {
				return "", marker, errors.New("replay limitation: configured hook source identity unsupported")
			}
			if trusted && (hook.Key != key || hook.CurrentHash != hash || hook.TrustStatus != "trusted" || hook.Enabled == nil || !*hook.Enabled) {
				return "", marker, errors.New("replay limitation: session-only hook trust not accepted")
			}
			key, hash = hook.Key, hook.CurrentHash
		}
		if found != 1 {
			return "", marker, errors.New("replay limitation: configured hook canary not uniquely recognized")
		}
		config = "hooks={" + base + ",state={" + quoteTOML(key) + "={trusted_hash=" + quoteTOML(hash) + "}}}"
	}
	return config, marker, verifyReplayFiles(nil, []string{marker})
}

func replayDisabledCapabilities(c *connection, b isolationBoundary) error {
	var hooks replayHookInventory
	if err := replayMetadata(c, "hooks/list", map[string]any{"cwds": []string{b.workspace}}, &hooks); err != nil {
		return err
	}
	if len(hooks.Data) != 1 || hooks.Data[0].Cwd != b.workspace || hooks.Data[0].Hooks == nil || len(hooks.Data[0].Hooks) != 0 || hooks.Data[0].Errors == nil || len(hooks.Data[0].Errors) != 0 || hooks.Data[0].Warnings == nil || len(hooks.Data[0].Warnings) != 0 {
		return errors.New("replay limitation: disabled native hooks not unambiguously unavailable")
	}
	var plugins struct {
		Marketplaces []json.RawMessage `json:"marketplaces"`
		Errors       []json.RawMessage `json:"marketplaceLoadErrors"`
	}
	if err := replayMetadata(c, "plugin/installed", map[string]any{"cwds": []string{b.workspace}}, &plugins); err != nil {
		return err
	}
	if plugins.Marketplaces == nil || len(plugins.Marketplaces) != 0 || plugins.Errors == nil || len(plugins.Errors) != 0 {
		return errors.New("replay limitation: disabled native plugin inventory missing, nonempty or failed")
	}
	return replayInstalledPluginSource(c, b.workspace)
}

func replayInstalledPluginSource(c *connection, cwd string) error {
	var response struct {
		Config struct {
			Plugins map[string]struct {
				Enabled *bool `json:"enabled"`
			} `json:"plugins"`
		} `json:"config"`
	}
	if err := c.call("config/read", map[string]any{"cwd": cwd, "includeLayers": false}, &response); err != nil {
		return err
	}
	const identity = "unified-computer-use@openai-bundled"
	enabled := response.Config.Plugins[identity].Enabled
	if enabled == nil || !*enabled {
		return errors.New("replay limitation: configured installed plugin source fixture unavailable")
	}
	codexRoot := os.Getenv("CODEX_HOME")
	if codexRoot == "" {
		userDir, err := os.UserHomeDir()
		if err != nil {
			return err
		}
		codexRoot = filepath.Join(userDir, ".codex")
	}
	cacheRoot, err := isolationPath(filepath.Join(codexRoot, "plugins", "cache"), true)
	if err != nil {
		return err
	}
	base := filepath.Join(cacheRoot, "openai-bundled", "unified-computer-use")
	versions, err := os.ReadDir(base)
	if err != nil || len(versions) != 1 || !versions[0].IsDir() {
		return errors.New("replay limitation: installed plugin version source absent or ambiguous")
	}
	root, err := isolationPath(filepath.Join(base, versions[0].Name()), true)
	if err != nil || !strings.HasPrefix(strings.ToLower(root), strings.ToLower(cacheRoot)+string(filepath.Separator)) {
		return errors.New("replay limitation: installed plugin source escapes its cache")
	}
	manifestPath := filepath.Join(root, ".codex-plugin", "plugin.json")
	manifestPath, err = filepath.EvalSymlinks(manifestPath)
	if err != nil || !strings.HasPrefix(strings.ToLower(manifestPath), strings.ToLower(root)+string(filepath.Separator)) {
		return errors.New("replay limitation: plugin manifest source escapes its installation")
	}
	data, err := os.ReadFile(manifestPath)
	var manifest struct {
		Name string `json:"name"`
	}
	if err != nil || len(data) > 16384 || json.Unmarshal(data, &manifest) != nil || manifest.Name != "unified-computer-use" {
		return errors.New("replay limitation: installed plugin manifest identity unsupported")
	}
	mcpPath, err := filepath.EvalSymlinks(filepath.Join(root, ".mcp.json"))
	if err != nil || !strings.HasPrefix(strings.ToLower(mcpPath), strings.ToLower(root)+string(filepath.Separator)) {
		return errors.New("replay limitation: plugin MCP source escapes its installation")
	}
	info, err := os.Stat(mcpPath)
	if err != nil || !info.Mode().IsRegular() || info.Size() == 0 {
		return errors.New("replay limitation: installed plugin has no declared MCP source")
	}
	// Only manifest identity and source presence are read; MCP credentials/content stay unread.
	return nil
}
