package opencode

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/kninetimmy/orch/internal/execx"
	"github.com/kninetimmy/orch/internal/paths"
)

// A real native child process, reached through Local/os/exec. The copied test
// executable stands in for OpenCode; no shell handles any fixture API argument.
func TestMain(m *testing.M) {
	if file := os.Getenv("ORCH_NATIVE_FIXTURE"); file != "" {
		os.Exit(nativeFixture(file))
	}
	os.Exit(m.Run())
}

type nativeState struct {
	Version string
	Bodies  map[string]string
	Loaded  []string
	Deleted int
}

type nativeCall struct {
	Executable string
	Args       []string
	Directory  string
}

func nativeFixture(file string) int {
	data, err := os.ReadFile(file)
	var state nativeState
	if err != nil || json.Unmarshal(data, &state) != nil {
		return 9
	}
	executable, _ := os.Executable()
	directory, _ := os.Getwd()
	record, _ := json.Marshal(nativeCall{executable, os.Args[1:], directory})
	log, err := os.OpenFile(file+".calls", os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return 9
	}
	_, err = log.Write(append(record, '\n'))
	closeErr := log.Close()
	if err != nil || closeErr != nil {
		return 9
	}
	if slices.Equal(os.Args[1:], []string{"--version"}) {
		fmt.Println(state.Version)
		return 0
	}
	if len(os.Args) != 4 || os.Args[1] != "api" {
		return 9
	}
	method, endpoint := os.Args[2], os.Args[3]
	if body, ok := state.Bodies[endpoint]; ok {
		fmt.Print(body)
		return 0
	}
	if method == "get" && endpoint == "/api/debug/location" {
		locations := []releaseLocation{}
		for _, directory := range state.Loaded {
			locations = append(locations, releaseLocation{Directory: directory})
		}
		fmt.Print(releaseJSON(locations))
		return 0
	}
	if method == "delete" {
		query, err := url.Parse(endpoint)
		if err != nil || query.Path != "/api/debug/location" || query.Query().Get("location[directory]") == "" {
			return 9
		}
		state.Deleted++
		state.Loaded = slices.DeleteFunc(state.Loaded, func(key string) bool { return key == query.Query().Get("location[directory]") })
		data, _ := json.Marshal(state)
		if os.WriteFile(file, data, 0o600) != nil {
			return 9
		}
		return 0
	}
	fmt.Fprint(os.Stderr, "credential-token: unsupported fixture endpoint")
	return 7
}

const npmNativeShim = "@ECHO off\r\nGOTO start\r\n:find_dp0\r\nSET dp0=%~dp0\r\nEXIT /b\r\n:start\r\nSETLOCAL\r\nCALL :find_dp0\r\n\"%dp0%\\node_modules\\@opencode\\cli\\bin\\opencode.exe\"   %*\r\n"

func fixtureWrite(t *testing.T, file string, data []byte) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(file), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(file, data, 0o600); err != nil {
		t.Fatal(err)
	}
}

func nativeInstallation(t *testing.T, name string, npm bool) (string, string) {
	t.Helper()
	prefix := filepath.Join(t.TempDir(), "npm prefix & (native)")
	selected := filepath.Join(prefix, name+".exe")
	native := selected
	if npm {
		selected = filepath.Join(prefix, name+".cmd")
		native = filepath.Join(prefix, "node_modules", "@opencode", "cli", "bin", "opencode.exe")
		fixtureWrite(t, selected, []byte(npmNativeShim))
		fixtureWrite(t, filepath.Join(prefix, "node_modules", "@opencode", "cli", "package.json"),
			[]byte(`{"name":"@opencode/cli","bin":{"opencode2":"./bin/opencode.exe","opencode":"./bin/opencode.exe"}}`))
		// PowerShell and extensionless companions must never be executed either.
		fixtureWrite(t, filepath.Join(prefix, name+".ps1"), []byte("throw 'launcher must not run'"))
		fixtureWrite(t, filepath.Join(prefix, name), []byte("launcher must not run"))
	}
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(executable)
	if err != nil {
		t.Fatal(err)
	}
	fixtureWrite(t, native, data)
	t.Setenv("PATH", prefix)
	t.Setenv("PATHEXT", ".EXE;.CMD;.BAT")
	return selected, native
}

func fixtureState(t *testing.T, state nativeState) string {
	t.Helper()
	file := filepath.Join(t.TempDir(), "state.json")
	fixtureWrite(t, file, []byte(releaseJSON(state)))
	t.Setenv("ORCH_NATIVE_FIXTURE", file)
	return file
}

func fixtureCalls(t *testing.T, file, native, directory string) []nativeCall {
	t.Helper()
	data, err := os.ReadFile(file + ".calls")
	if err != nil {
		t.Fatal(err)
	}
	var calls []nativeCall
	for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		var call nativeCall
		if err := json.Unmarshal([]byte(line), &call); err != nil {
			t.Fatal(err)
		}
		a, err := os.Stat(call.Executable)
		if err != nil {
			t.Fatal(err)
		}
		b, err := os.Stat(native)
		if err != nil || !os.SameFile(a, b) || call.Directory != directory {
			t.Fatalf("wrong native identity or cwd: %+v (%v)", call, err)
		}
		calls = append(calls, call)
	}
	return calls
}

func TestWindowsNativeRuntimeBinding(t *testing.T) {
	for _, name := range []string{"opencode2", "opencode"} {
		for _, npm := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/npm=%t", name, npm), func(t *testing.T) {
				selected, native := nativeInstallation(t, name, npm)
				root := filepath.Join(t.TempDir(), "repo & (space) % ! ^")
				if err := os.Mkdir(root, 0o755); err != nil {
					t.Fatal(err)
				}
				root, err := paths.Canonical(root)
				if err != nil {
					t.Fatal(err)
				}
				query := url.Values{"location[directory]": {root}}.Encode()
				file := fixtureState(t, nativeState{Version: "opencode v2.3.0", Bodies: map[string]string{
					"/api/model?" + query:  `{"location":{"directory":` + releaseJSON(root) + `},"data":[]}`,
					"/api/skill?" + query:  `{"location":{"directory":` + releaseJSON(root) + `},"data":[]}`,
					"/api/plugin?" + query: `{"location":{"directory":` + releaseJSON(root) + `},"data":[{"id":"orch.delivery","features":{"server":true},"state":{"status":"active"}}]}`,
				}})
				found, err := exec.LookPath(name)
				if err != nil || found != selected {
					t.Fatalf("fixture must discover selected layout: %q %v", found, err)
				}
				local := execx.Local{}
				ctx := context.Background()
				runtime, err := Resolve(ctx, local, root)
				if err != nil || !filepath.IsAbs(runtime.Executable) || runtime.Warning == "" {
					t.Fatalf("resolve: %+v %v", runtime, err)
				}
				// Later PATH changes must not change the qualified runtime.
				t.Setenv("PATH", t.TempDir())
				if _, err := runtime.ReadCatalog(ctx, local, root); err != nil {
					t.Fatal(err)
				}
				if _, err := runtime.ReadSkillCatalog(ctx, local, root); err != nil {
					t.Fatal(err)
				}
				if err := runtime.CheckPlugin(ctx, local, root, "orch.delivery"); err != nil {
					t.Fatal(err)
				}
				calls := fixtureCalls(t, file, native, root)
				if len(calls) != 4 || !slices.Equal(calls[0].Args, []string{"--version"}) {
					t.Fatalf("unexpected qualification/API calls: %+v", calls)
				}
			})
		}
	}
}

func TestWindowsNpmRejectsUnassociatedNative(t *testing.T) {
	for _, tc := range []struct {
		name   string
		change func(*testing.T, string, string)
	}{
		{"custom wrapper", func(t *testing.T, selected, _ string) {
			fixtureWrite(t, selected, []byte("@echo credential-token\r\n"))
		}},
		{"wrong package", func(t *testing.T, _, native string) {
			fixtureWrite(t, filepath.Join(filepath.Dir(filepath.Dir(native)), "package.json"), []byte(`{"name":"unrelated","bin":{"opencode2":"./bin/opencode.exe"}}`))
		}},
		{"escaping bin declaration", func(t *testing.T, _, native string) {
			fixtureWrite(t, filepath.Join(filepath.Dir(filepath.Dir(native)), "package.json"), []byte(`{"name":"@opencode/cli","bin":{"opencode2":"../../../unrelated.exe"}}`))
		}},
		{"malformed manifest", func(t *testing.T, _, native string) {
			fixtureWrite(t, filepath.Join(filepath.Dir(filepath.Dir(native)), "package.json"), []byte("credential-token"))
		}},
		{"bin for other name only", func(t *testing.T, _, native string) {
			fixtureWrite(t, filepath.Join(filepath.Dir(filepath.Dir(native)), "package.json"), []byte(`{"name":"@opencode/cli","bin":{"opencode":"./bin/opencode.exe"}}`))
		}},
		{"missing manifest", func(t *testing.T, _, native string) {
			if err := os.Remove(filepath.Join(filepath.Dir(filepath.Dir(native)), "package.json")); err != nil {
				t.Fatal(err)
			}
		}},
		{"missing native", func(t *testing.T, _, native string) {
			if err := os.Remove(native); err != nil {
				t.Fatal(err)
			}
		}},
		{"directory instead of native", func(t *testing.T, _, native string) {
			if err := os.Remove(native); err != nil {
				t.Fatal(err)
			}
			if err := os.Mkdir(native, 0o755); err != nil {
				t.Fatal(err)
			}
		}},
		{"escaping package junction", func(t *testing.T, _, native string) {
			pkg := filepath.Dir(filepath.Dir(native))
			other := filepath.Join(t.TempDir(), "unrelated-package")
			if err := os.Rename(pkg, other); err != nil {
				t.Fatal(err)
			}
			makeRuntimeJunction(t, pkg, other)
		}},
		{"escaping native directory junction", func(t *testing.T, _, native string) {
			bin := filepath.Dir(native)
			other := filepath.Join(t.TempDir(), "unrelated-bin")
			if err := os.Rename(bin, other); err != nil {
				t.Fatal(err)
			}
			makeRuntimeJunction(t, bin, other)
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			selected, native := nativeInstallation(t, "opencode2", true)
			file := fixtureState(t, nativeState{Version: "opencode v2.3.0"})
			tc.change(t, selected, native)
			_, err := Resolve(context.Background(), execx.Local{}, t.TempDir())
			if err == nil || !strings.Contains(err.Error(), "cannot verify a native Windows") || strings.Contains(err.Error(), "credential-token") {
				t.Fatalf("expected safe actionable native refusal: %v", err)
			}
			if _, err := os.Stat(file + ".calls"); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("unsupported installation executed a process: %v", err)
			}
		})
	}
}

func makeRuntimeJunction(t *testing.T, link, target string) {
	t.Helper()
	// Same fixed fixture-creation command as the path/removal tests. Generated
	// paths are environment values, never shell expressions or API/cursor data.
	powershell := filepath.Join(os.Getenv("SystemRoot"), "System32", "WindowsPowerShell", "v1.0", "powershell.exe")
	cmd := exec.CommandContext(t.Context(), powershell, "-NoProfile", "-NonInteractive", "-Command",
		`New-Item -ItemType Junction -Path $env:ORCH_TEST_LINK -Target $env:ORCH_TEST_TARGET -ErrorAction Stop | Out-Null`)
	cmd.Env = append(os.Environ(), "ORCH_TEST_LINK="+link, "ORCH_TEST_TARGET="+target)
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("required junction fixture: %v: %s", err, output)
	}
	t.Cleanup(func() {
		if err := os.Remove(link); err != nil {
			t.Error(err)
		}
	})
}

func TestWindowsNpmOldBoundaryFailsBenignMultiQuery(t *testing.T) {
	_, native := nativeInstallation(t, "opencode2", true)
	const endpoint = "/api/session?limit=1&order=desc"
	const body = `{"ok":true}`
	file := fixtureState(t, nativeState{Version: "opencode v2.3.0", Bodies: map[string]string{endpoint: body}})
	root := t.TempDir()
	local := execx.Local{}
	args := []string{"api", "get", endpoint}
	old, err := local.Run(context.Background(), execx.Cmd{Name: "opencode2", Args: args, Dir: root})
	if err == nil && old.ExitCode == 0 && old.Stdout == body {
		t.Fatal("fixture no longer discriminates the old bare-name launcher failure")
	}
	t.Logf("old bare-name boundary: started=%t exit=%d unchanged-response=%t", err == nil, old.ExitCode, old.Stdout == body)
	runtime, err := Resolve(context.Background(), local, root)
	if err != nil {
		t.Fatal(err)
	}
	current, err := local.Run(context.Background(), execx.Cmd{Name: runtime.Executable, Args: args, Dir: root})
	if err != nil || current.ExitCode != 0 || current.Stdout != body {
		t.Fatalf("native transport failed: %+v %v", current, err)
	}
	calls := fixtureCalls(t, file, native, root)
	if !slices.Equal(calls[len(calls)-1].Args, args) {
		t.Fatal("native multi-query argument changed")
	}
	t.Log("resolved native boundary: version and complete multi-query request succeeded")
}

func TestWindowsNativeResolutionUnsupportedAndCanceled(t *testing.T) {
	selected, native := nativeInstallation(t, "opencode2", true)
	file := fixtureState(t, nativeState{Version: "opencode v2.3.0"})
	for _, candidate := range []string{
		"relative.cmd", filepath.Join(t.TempDir(), "missing.exe"),
		filepath.Join(filepath.Dir(selected), "opencode2.ps1"),
	} {
		local := execx.Local{LookPath: func(string) (string, error) { return candidate, nil }}
		if _, err := Resolve(context.Background(), local, t.TempDir()); err == nil {
			t.Fatalf("unsupported native candidate accepted: %s", candidate)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := Resolve(ctx, execx.Local{}, t.TempDir()); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation not preserved: %v", err)
	}
	if _, err := os.Stat(file + ".calls"); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("unsupported/canceled resolution executed %s: %v", native, err)
	}
}

func TestWindowsNpmProductionPagination(t *testing.T) {
	for _, outcome := range []string{"complete", "malformed", "unavailable", "repeated", "nonadvancing"} {
		t.Run(outcome, func(t *testing.T) {
			_, native := nativeInstallation(t, "opencode2", true)
			f := newReleaseFixture(t)
			cursor := "opaque + / = & % ! ^ (cursor)"
			next := "/api/session?" + url.Values{"cursor": {cursor}, "limit": {"100"}}.Encode()
			f.bodies["/api/session?limit=100"] = `{"data":[{"id":"sesIdle","location":{"directory":` + releaseJSON(f.root) + `}}],"cursor":{"next":` + releaseJSON(cursor) + `}}`
			f.bodies[next] = `{"data":[{"id":"sesSecond","location":{"directory":` + releaseJSON(f.root) + `}}],"cursor":{}}`
			f.bodies["/api/experimental/session/sesSecond/terminal"] = `{"data":[]}`
			switch outcome {
			case "malformed":
				f.bodies[next] = `{"data":[]}`
			case "unavailable":
				delete(f.bodies, next)
			case "repeated":
				f.bodies[next] = f.bodies["/api/session?limit=100"]
			case "nonadvancing":
				f.bodies[next] = `{"data":[{"id":"sesSecond","location":{"directory":` + releaseJSON(f.root) + `}}],"cursor":{"next":` + releaseJSON(cursor) + `}}`
			}
			file := fixtureState(t, nativeState{Version: "opencode v2.3.0", Bodies: f.bodies, Loaded: f.loaded})
			err := ReleaseLocation(context.Background(), execx.Local{}, f.root, f.target)
			if (outcome == "complete" && err != nil) || (outcome != "complete" && err == nil) || (err != nil && strings.Contains(err.Error(), "credential-token")) {
				t.Fatalf("%s: %v", outcome, err)
			}
			calls := fixtureCalls(t, file, native, f.root)
			if !slices.ContainsFunc(calls, func(call nativeCall) bool { return slices.Equal(call.Args, []string{"api", "get", next}) }) {
				t.Fatal("opaque cursor/multi-parameter request did not arrive unchanged")
			}
			data, err := os.ReadFile(file)
			if err != nil {
				t.Fatal(err)
			}
			var state nativeState
			if err := json.Unmarshal(data, &state); err != nil {
				t.Fatal(err)
			}
			want := 0
			if outcome == "complete" {
				want = 1
			}
			if state.Deleted != want {
				t.Fatalf("disposals=%d want %d", state.Deleted, want)
			}
		})
	}
}
