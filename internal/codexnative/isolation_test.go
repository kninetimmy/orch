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
	for _, version := range []string{"0.159.2", "", "future-version"} {
		if err := modelToolBoundary(version); !errors.Is(err, ErrIsolationUnavailable) {
			t.Fatalf("unverified model-tool boundary accepted: %v", err)
		}
	}
	var c connection
	c.isolation = true
	for _, method := range []string{"thread/start", "turn/start", "process/spawn", "thread/shellCommand", "windowsSandbox/setupStart", "config/value/write"} {
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
			if scenario == "incompatible-version" {
				version = "0.160.0"
			}
			write(request.ID, map[string]any{"userAgent": "Codex Desktop/" + version})
		case "initialized":
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
			if runtime.GOOS == "windows" && (!capabilities.SandboxReady || !capabilities.ProfileAllowed || !strings.Contains(err.Error(), "disabledPluginIds")) {
				t.Fatalf("readiness/profile availability bypassed specific tool refusal: %+v, %v", capabilities, err)
			}
		}
	})
	for _, scenario := range []string{"success", "missing-method", "missing-readiness", "not-ready", "denied-profile", "missing-profile", "missing-allowed", "duplicate-profile", "incompatible-version", "excessive-output"} {
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
			if scenario == "success" || scenario == "excessive-output" {
				if err != nil || !capabilities.SandboxReady || !capabilities.ProfileAllowed {
					t.Fatalf("configuration availability: %+v, %v", capabilities, err)
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
			if data, err := os.ReadFile(preserved); err != nil || string(data) != "preserve" {
				t.Fatal("failure cleanup modified existing work")
			}
			if data, err := os.ReadFile(filepath.Join(layout.Scratch, "server-closed")); err != nil || string(data) != "closed" {
				t.Fatalf("server did not close on failure: %v", err)
			}
		})
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
