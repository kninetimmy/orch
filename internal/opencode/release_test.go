package opencode

import (
	"context"
	"encoding/json"
	"errors"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/kninetimmy/orch/internal/execx"
)

const releaseSpec = `{"paths":{"/api/debug/location":{"get":{"operationId":"debug.location.list"},"delete":{"operationId":"debug.location.evict","parameters":[{"name":"location","in":"query","style":"deepObject","explode":true}]}}}}`

type releaseFixture struct {
	root, target, unrelated string
	loaded                  []string
	bodies                  map[string]string
	fail                    map[string]bool
	calls                   []execx.Cmd
	deletes                 int
	afterDelete             func()
	beforeReinventory       func()
	inventories             int
	targetKey               string
	executable              string
}

func releaseJSON(value any) string {
	data, err := json.Marshal(value)
	if err != nil {
		panic(err)
	}
	return string(data)
}

func newReleaseFixture(t *testing.T) *releaseFixture {
	t.Helper()
	root := t.TempDir()
	target := filepath.Join(root, "review & checkout")
	unrelated := t.TempDir()
	if err := os.Mkdir(target, 0o755); err != nil {
		t.Fatal(err)
	}
	f := &releaseFixture{root: root, target: target, unrelated: unrelated, bodies: map[string]string{}, fail: map[string]bool{}, executable: "opencode2"}
	f.loaded = []string{root, target, unrelated}
	f.bodies["/openapi.json"] = releaseSpec
	f.bodies["/api/session/active"] = `{"data":{}}`
	f.bodies["/api/experimental/job?status=running"] = `{"data":[]}`
	f.bodies["/api/session?limit=100"] = `{"data":[{"id":"sesIdle","location":{"directory":` + releaseJSON(target) + `}}],"cursor":{}}`
	f.bodies["/api/experimental/session/sesIdle/terminal"] = `{"data":[]}`
	f.bodies["/api/location?"+f.query()] = releaseJSON(map[string]any{
		"directory": target, "project": map[string]string{"directory": target, "canonical": root},
	})
	for _, endpoint := range []string{"/api/shell", "/api/pty"} {
		for _, directory := range f.loaded {
			query := url.Values{"location[directory]": {directory}}.Encode()
			f.bodies[endpoint+"?"+query] = f.scoped(directory, "[]")
		}
	}
	return f
}

func (f *releaseFixture) query() string {
	key := f.targetKey
	if key == "" {
		key = f.target
	}
	return url.Values{"location[directory]": {key}}.Encode()
}

func (f *releaseFixture) scoped(directory, data string) string {
	return `{"location":{"directory":` + releaseJSON(directory) + `},"data":` + data + `}`
}

func (f *releaseFixture) session(id, directory string) {
	f.bodies["/api/session/"+id] = `{"data":{"id":` + releaseJSON(id) + `,"location":{"directory":` + releaseJSON(directory) + `}}}`
}

func (f *releaseFixture) run(t *testing.T) error {
	t.Helper()
	runner := releaseContextRunner(func(ctx context.Context, ctxCmd execx.Cmd) (execx.Result, error) {
		if deadline, ok := ctx.Deadline(); !ok || time.Until(deadline) > 2*time.Minute {
			t.Fatal("host command has no bounded deadline")
		}
		f.calls = append(f.calls, ctxCmd)
		if ctxCmd.Dir != f.root {
			t.Fatalf("host command escaped primary cwd: %+v", ctxCmd)
		}
		if slices.Equal(ctxCmd.Args, []string{"--version"}) {
			if ctxCmd.Name != f.executable {
				return execx.Result{}, errors.New("credential-token")
			}
			return execx.Result{Stdout: "opencode v2.3.0"}, nil
		}
		if ctxCmd.Name != f.executable || len(ctxCmd.Args) != 3 || ctxCmd.Args[0] != "api" {
			t.Fatalf("unexpected invocation: %+v", ctxCmd)
		}
		method, endpoint := ctxCmd.Args[1], ctxCmd.Args[2]
		if f.fail[endpoint] {
			return execx.Result{ExitCode: 7, Stdout: "credential-token", Stderr: "credential-token"}, nil
		}
		if body, ok := f.bodies[endpoint]; ok {
			return execx.Result{Stdout: body}, nil
		}
		if endpoint == "/api/debug/location" && method == "get" {
			f.inventories++
			if f.inventories > 1 && f.beforeReinventory != nil {
				f.beforeReinventory()
			}
			locations := []releaseLocation{}
			for _, directory := range f.loaded {
				locations = append(locations, releaseLocation{Directory: directory})
			}
			return execx.Result{Stdout: releaseJSON(locations)}, nil
		}
		if method == "delete" {
			if endpoint != "/api/debug/location?"+f.query() {
				t.Fatalf("not an exact encoded location deletion: %+v", ctxCmd)
			}
			f.deletes++
			key := f.target
			if f.targetKey != "" {
				key = f.targetKey
			}
			f.loaded = slices.DeleteFunc(f.loaded, func(directory string) bool { return directory == key })
			if f.afterDelete != nil {
				f.afterDelete()
			}
			return execx.Result{}, nil
		}
		t.Fatalf("unexpected endpoint: %+v", ctxCmd)
		return execx.Result{}, nil
	})
	ctx := context.Background()
	return ReleaseLocation(ctx, runner, f.root, f.target)
}

func TestReleaseLocation(t *testing.T) {
	for _, tc := range []struct {
		name    string
		arrange func(*releaseFixture)
		want    string
		deletes int
	}{
		{"idle loaded target", func(*releaseFixture) {}, "", 1},
		{"resolved fallback command", func(f *releaseFixture) { f.executable = "opencode" }, "", 1},
		{"already unloaded", func(f *releaseFixture) { f.loaded = []string{f.root, f.unrelated} }, "", 0},
		{"active target", func(f *releaseFixture) {
			f.bodies["/api/session/active"] = `{"data":{"sesBusy":{"type":"running"}}}`
			f.session("sesBusy", f.target)
		}, "active session", 0},
		{"active unrelated", func(f *releaseFixture) {
			f.bodies["/api/session/active"] = `{"data":{"sesBusy":{"type":"running"}}}`
			f.session("sesBusy", f.unrelated)
		}, "", 1},
		{"unloaded but active target", func(f *releaseFixture) {
			f.loaded = []string{f.root, f.unrelated}
			f.bodies["/api/session/active"] = `{"data":{"sesBusy":{"type":"running"}}}`
			f.session("sesBusy", f.target)
		}, "active session", 0},
		{"background shell target", func(f *releaseFixture) {
			f.bodies["/api/experimental/job?status=running"] = `{"data":[{"id":"job","kind":"shell","status":"running","sessionID":"sesOwner"}]}`
			f.session("sesOwner", f.target)
		}, "unfinished background job", 0},
		{"background child target owner elsewhere", func(f *releaseFixture) {
			f.bodies["/api/experimental/job?status=running"] = `{"data":[{"id":"job","kind":"subagent","status":"running","sessionID":"sesOwner","childSessionID":"sesChild"}]}`
			f.session("sesOwner", f.root)
			f.session("sesChild", f.target)
		}, "unfinished background job", 0},
		{"background owner target child elsewhere", func(f *releaseFixture) {
			f.bodies["/api/experimental/job?status=running"] = `{"data":[{"id":"job","kind":"subagent","status":"running","sessionID":"sesOwner","childSessionID":"sesChild"}]}`
			f.session("sesOwner", f.target)
			f.session("sesChild", f.unrelated)
		}, "unfinished background job", 0},
		{"background unrelated", func(f *releaseFixture) {
			f.bodies["/api/experimental/job?status=running"] = `{"data":[{"id":"job","kind":"subagent","status":"running","sessionID":"sesOwner","childSessionID":"sesChild"}]}`
			f.session("sesOwner", f.root)
			f.session("sesChild", f.unrelated)
		}, "", 1},
		{"running shell", func(f *releaseFixture) {
			f.bodies["/api/shell?"+f.query()] = f.scoped(f.target, `[{"id":"shell","status":"running","cwd":`+releaseJSON(f.target)+`}]`)
		}, "running shell or terminal", 0},
		{"running PTY", func(f *releaseFixture) {
			f.bodies["/api/pty?"+f.query()] = f.scoped(f.target, `[{"id":"pty","status":"running","cwd":`+releaseJSON(f.target)+`}]`)
		}, "running shell or terminal", 0},
		{"exited PTY", func(f *releaseFixture) {
			f.bodies["/api/pty?"+f.query()] = f.scoped(f.target, `[{"id":"pty","status":"exited","cwd":`+releaseJSON(f.target)+`}]`)
		}, "", 1},
		{"primary shell target cwd", func(f *releaseFixture) {
			query := url.Values{"location[directory]": {f.root}}.Encode()
			f.bodies["/api/shell?"+query] = f.scoped(f.root, `[{"id":"shell","status":"running","cwd":`+releaseJSON(f.target)+`}]`)
		}, "running shell or terminal", 0},
		{"unrelated PTY target cwd", func(f *releaseFixture) {
			query := url.Values{"location[directory]": {f.unrelated}}.Encode()
			f.bodies["/api/pty?"+query] = f.scoped(f.unrelated, `[{"id":"pty","status":"running","cwd":`+releaseJSON(f.target)+`}]`)
		}, "running shell or terminal", 0},
		{"unloaded target with primary PTY", func(f *releaseFixture) {
			f.loaded = []string{f.root, f.unrelated}
			query := url.Values{"location[directory]": {f.root}}.Encode()
			f.bodies["/api/pty?"+query] = f.scoped(f.root, `[{"id":"pty","status":"running","cwd":`+releaseJSON(f.target)+`}]`)
		}, "running shell or terminal", 0},
		{"target owner unrelated cwd", func(f *releaseFixture) {
			f.bodies["/api/shell?"+f.query()] = f.scoped(f.target, `[{"id":"shell","status":"running","cwd":`+releaseJSON(f.unrelated)+`}]`)
		}, "running shell or terminal", 0},
		{"unrelated ordinary activity", func(f *releaseFixture) {
			query := url.Values{"location[directory]": {f.unrelated}}.Encode()
			for _, endpoint := range []string{"/api/shell", "/api/pty"} {
				f.bodies[endpoint+"?"+query] = f.scoped(f.unrelated, `[{"id":"process","status":"running","cwd":`+releaseJSON(f.unrelated)+`}]`)
			}
		}, "", 1},
		{"missing ordinary cwd", func(f *releaseFixture) {
			f.bodies["/api/shell?"+f.query()] = f.scoped(f.target, `[{"id":"shell","status":"running"}]`)
		}, "malformed", 0},
		{"relative ordinary cwd", func(f *releaseFixture) {
			f.bodies["/api/pty?"+f.query()] = f.scoped(f.target, `[{"id":"pty","status":"running","cwd":"relative"}]`)
		}, "malformed", 0},
		{"unverifiable ordinary cwd", func(f *releaseFixture) {
			f.bodies["/api/pty?"+f.query()] = f.scoped(f.target, `[{"id":"pty","status":"running","cwd":`+releaseJSON(filepath.Join(f.root, "missing"))+`}]`)
		}, "identity is unavailable", 0},
		{"oversized ordinary shells", func(f *releaseFixture) {
			f.bodies["/api/shell?"+f.query()] = f.scoped(f.target, "["+strings.Repeat(`{},`, 1000)+`{}]`)
		}, "oversized", 0},
		{"oversized ordinary PTYs", func(f *releaseFixture) {
			f.bodies["/api/pty?"+f.query()] = f.scoped(f.target, "["+strings.Repeat(`{},`, 1000)+`{}]`)
		}, "oversized", 0},
		{"unrelated process inventory unavailable", func(f *releaseFixture) {
			query := url.Values{"location[directory]": {f.unrelated}}.Encode()
			f.fail["/api/pty?"+query] = true
		}, "unavailable", 0},
		{"persistent terminal after session moved", func(f *releaseFixture) {
			f.bodies["/api/session?limit=100"] = `{"data":[{"id":"sesIdle","location":{"directory":` + releaseJSON(f.unrelated) + `}}],"cursor":{}}`
			f.bodies["/api/experimental/session/sesIdle/terminal"] = `{"data":[{"id":"pty","sessionID":"sesIdle","cwd":` + releaseJSON(f.target) + `,"status":"running"}]}`
		}, "running persistent terminal", 0},
		{"persistent terminal target owner different cwd", func(f *releaseFixture) {
			f.bodies["/api/experimental/session/sesIdle/terminal"] = `{"data":[{"id":"pty","sessionID":"sesIdle","cwd":` + releaseJSON(f.unrelated) + `,"status":"running"}]}`
		}, "running persistent terminal", 0},
		{"persistent unrelated", func(f *releaseFixture) {
			f.bodies["/api/session?limit=100"] = `{"data":[{"id":"sesIdle","location":{"directory":` + releaseJSON(f.unrelated) + `}}],"cursor":{}}`
			f.bodies["/api/experimental/session/sesIdle/terminal"] = `{"data":[{"id":"pty","sessionID":"sesIdle","cwd":` + releaseJSON(f.unrelated) + `,"status":"running"}]}`
		}, "", 1},
		{"exited persistent terminal", func(f *releaseFixture) {
			f.bodies["/api/experimental/session/sesIdle/terminal"] = `{"data":[{"id":"pty","sessionID":"sesIdle","cwd":` + releaseJSON(f.target) + `,"status":"exited"}]}`
		}, "", 1},
		{"unexpected descendant", func(f *releaseFixture) {
			child := filepath.Join(f.target, "nested")
			if err := os.Mkdir(child, 0o755); err != nil {
				t.Fatal(err)
			}
			f.loaded = append(f.loaded, child)
		}, "unexpected loaded descendant", 0},
		{"duplicate target", func(f *releaseFixture) { f.loaded = append(f.loaded, f.target) }, "duplicate location cache key", 0},
		{"duplicate unrelated", func(f *releaseFixture) { f.loaded = append(f.loaded, f.unrelated) }, "duplicate", 0},
		{"unverifiable inventory", func(f *releaseFixture) { f.loaded = append(f.loaded, filepath.Join(f.root, "missing")) }, "identity is unavailable", 0},
		{"wrong root", func(f *releaseFixture) {
			f.bodies["/api/location?"+f.query()] = `{"directory":` + releaseJSON(f.unrelated) + `}`
		}, "cache key differs", 0},
		{"wrong project", func(f *releaseFixture) {
			f.bodies["/api/location?"+f.query()] = releaseJSON(map[string]any{"directory": f.target, "project": map[string]string{"directory": f.target, "canonical": f.unrelated}})
		}, "roots differ", 0},
		{"wrong shell root", func(f *releaseFixture) { f.bodies["/api/shell?"+f.query()] = f.scoped(f.unrelated, "[]") }, "wrong-root", 0},
		{"disposal unavailable", func(f *releaseFixture) { f.fail["/api/debug/location?"+f.query()] = true }, "unavailable or unsupported", 0},
		{"disposal still loaded", func(f *releaseFixture) { f.afterDelete = func() { f.loaded = append(f.loaded, f.target) } }, "did not unload", 1},
		{"unrelated disappeared", func(f *releaseFixture) { f.afterDelete = func() { f.loaded = []string{f.root} } }, "another loaded location disappeared", 1},
		{"post-disposal unavailable", func(f *releaseFixture) { f.afterDelete = func() { f.fail["/api/debug/location"] = true } }, "unavailable", 1},
		{"scoped disposal unsupported", func(f *releaseFixture) { f.bodies["/openapi.json"] = `{}` }, "unsupported", 0},
		{"wrong query style", func(f *releaseFixture) {
			f.bodies["/openapi.json"] = strings.ReplaceAll(releaseSpec, "deepObject", "form")
		}, "unsupported", 0},
		{"malformed inventory", func(f *releaseFixture) { f.bodies["/api/debug/location"] = `null` }, "malformed", 0},
		{"relative inventory key", func(f *releaseFixture) { f.loaded = append(f.loaded, "relative") }, "not absolute", 0},
		{"oversized inventory", func(f *releaseFixture) {
			f.bodies["/api/debug/location"] = "[" + strings.Repeat(`{},`, 1024) + "{}]"
		}, "oversized", 0},
		{"missing active data", func(f *releaseFixture) { f.bodies["/api/session/active"] = `{}` }, "malformed", 0},
		{"unknown active state", func(f *releaseFixture) { f.bodies["/api/session/active"] = `{"data":{"sesBusy":{"type":"idle"}}}` }, "unsupported", 0},
		{"unknown job", func(f *releaseFixture) {
			f.bodies["/api/experimental/job?status=running"] = `{"data":[{"id":"job","kind":"future","status":"running"}]}`
		}, "unsupported", 0},
		{"session unavailable", func(f *releaseFixture) {
			f.bodies["/api/session/active"] = `{"data":{"sesBusy":{"type":"running"}}}`
			f.fail["/api/session/sesBusy"] = true
		}, "unavailable", 0},
		{"session identifiers are not diagnostics", func(f *releaseFixture) {
			f.bodies["/api/session/active"] = `{"data":{"sescredential-token":{"type":"running"}}}`
			f.fail["/api/session/sescredential-token"] = true
		}, "unavailable", 0},
		{"missing session cursor", func(f *releaseFixture) { f.bodies["/api/session?limit=100"] = `{"data":[]}` }, "malformed", 0},
		{"persistent owner mismatch", func(f *releaseFixture) {
			f.bodies["/api/experimental/session/sesIdle/terminal"] = `{"data":[{"id":"pty","sessionID":"sesOther","cwd":"x","status":"running"}]}`
		}, "unsupported", 0},
		{"persistent evidence unavailable", func(f *releaseFixture) { f.fail["/api/experimental/session/sesIdle/terminal"] = true }, "unavailable", 0},
		{"unsupported PTY state", func(f *releaseFixture) {
			f.bodies["/api/pty?"+f.query()] = f.scoped(f.target, `[{"id":"pty","status":"unknown"}]`)
		}, "malformed", 0},
		{"null PTY array", func(f *releaseFixture) { f.bodies["/api/pty?"+f.query()] = f.scoped(f.target, "null") }, "malformed", 0},
		{"invalid JSON", func(f *releaseFixture) { f.bodies["/api/session/active"] = "credential-token" }, "malformed", 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newReleaseFixture(t)
			tc.arrange(f)
			err := f.run(t)
			if (tc.want == "" && err != nil) || (tc.want != "" && (err == nil || !strings.Contains(err.Error(), tc.want))) {
				t.Fatalf("release = %v, want %q", err, tc.want)
			}
			if err != nil && strings.Contains(err.Error(), "credential-token") {
				t.Fatalf("raw host output disclosed: %v", err)
			}
			if f.deletes != tc.deletes {
				t.Fatalf("deletes=%d want %d", f.deletes, tc.deletes)
			}
			if tc.name == "already unloaded" {
				for _, cmd := range f.calls {
					if len(cmd.Args) == 3 && strings.HasSuffix(cmd.Args[2], "?"+f.query()) {
						t.Fatalf("unloaded target was activated by a scoped read: %+v", cmd)
					}
				}
			}
		})
	}
}

// Windows casing aliases reproduce the real V2 cache inventory. A Unix symlink
// supplies the same distinct-key / same-physical-directory condition in CI.
func releaseAlias(t *testing.T, directory string) string {
	t.Helper()
	alias := strings.ToUpper(directory)
	if runtime.GOOS != "windows" {
		alias = filepath.Join(t.TempDir(), "alias")
		if err := os.Symlink(directory, alias); err != nil {
			t.Fatal(err)
		}
	}
	a, err := os.Stat(directory)
	if err != nil {
		t.Fatal(err)
	}
	b, err := os.Stat(alias)
	if err != nil || alias == directory || !os.SameFile(a, b) {
		t.Fatalf("fixture needs distinct keys for one physical directory: %v", err)
	}
	return alias
}

func TestReleaseLocationAliases(t *testing.T) {
	for _, tc := range []struct {
		name    string
		owner   string
		arrange func(*releaseFixture, string)
		want    string
		deletes int
	}{
		{"unrelated aliases", "unrelated", func(*releaseFixture, string) {}, "", 1},
		{"primary aliases", "primary", func(*releaseFixture, string) {}, "", 1},
		{"unloaded target", "unrelated", func(f *releaseFixture, _ string) {
			f.loaded = slices.DeleteFunc(f.loaded, func(key string) bool { return key == f.target })
		}, "", 0},
		{"alias shell target cwd", "unrelated", func(f *releaseFixture, alias string) {
			key := url.Values{"location[directory]": {alias}}.Encode()
			f.bodies["/api/shell?"+key] = f.scoped(alias, `[{"id":"shell","status":"running","cwd":`+releaseJSON(f.target)+`}]`)
		}, "running shell or terminal", 0},
		{"alias PTY target cwd", "unrelated", func(f *releaseFixture, alias string) {
			key := url.Values{"location[directory]": {alias}}.Encode()
			f.bodies["/api/pty?"+key] = f.scoped(alias, `[{"id":"pty","status":"running","cwd":`+releaseJSON(f.target)+`}]`)
		}, "running shell or terminal", 0},
		{"original owner target cwd", "unrelated", func(f *releaseFixture, _ string) {
			key := url.Values{"location[directory]": {f.unrelated}}.Encode()
			f.bodies["/api/pty?"+key] = f.scoped(f.unrelated, `[{"id":"pty","status":"running","cwd":`+releaseJSON(f.target)+`}]`)
		}, "running shell or terminal", 0},
		{"alias shell wrong cache same identity", "unrelated", func(f *releaseFixture, alias string) {
			key := url.Values{"location[directory]": {alias}}.Encode()
			f.bodies["/api/shell?"+key] = f.scoped(f.unrelated, "[]")
		}, "wrong-root", 0},
		{"original PTY wrong cache same identity", "unrelated", func(f *releaseFixture, alias string) {
			key := url.Values{"location[directory]": {f.unrelated}}.Encode()
			f.bodies["/api/pty?"+key] = f.scoped(alias, "[]")
		}, "wrong-root", 0},
		{"alias inventory unavailable", "unrelated", func(f *releaseFixture, alias string) {
			key := url.Values{"location[directory]": {alias}}.Encode()
			f.fail["/api/pty?"+key] = true
		}, "unavailable", 0},
		{"alias disappeared", "unrelated", func(f *releaseFixture, alias string) {
			f.afterDelete = func() {
				f.loaded = slices.DeleteFunc(f.loaded, func(key string) bool { return key == alias })
			}
		}, "another loaded location disappeared", 1},
		{"original disappeared alias remains", "unrelated", func(f *releaseFixture, _ string) {
			f.afterDelete = func() {
				f.loaded = slices.DeleteFunc(f.loaded, func(key string) bool { return key == f.unrelated })
			}
		}, "another loaded location disappeared", 1},
		{"alias replaced by another key same identity", "unrelated", func(f *releaseFixture, alias string) {
			replacement := alias + string(filepath.Separator) + "."
			f.afterDelete = func() {
				f.loaded[slices.Index(f.loaded, alias)] = replacement
			}
		}, "another loaded location disappeared", 1},
		{"alias disappeared before disposal", "unrelated", func(f *releaseFixture, alias string) {
			f.beforeReinventory = func() {
				f.loaded = slices.DeleteFunc(f.loaded, func(key string) bool { return key == alias })
				f.beforeReinventory = nil
			}
		}, "another loaded location disappeared", 0},
		{"ambiguous target aliases", "target", func(*releaseFixture, string) {}, "ambiguous target cache aliases", 0},
		{"target alias owner unrelated cwd", "target", func(f *releaseFixture, alias string) {
			key := url.Values{"location[directory]": {alias}}.Encode()
			f.bodies["/api/shell?"+key] = f.scoped(alias, `[{"id":"shell","status":"running","cwd":`+releaseJSON(f.unrelated)+`}]`)
		}, "ambiguous target cache aliases", 0},
		{"single attributable target alias", "target", func(f *releaseFixture, alias string) {
			f.loaded = slices.DeleteFunc(f.loaded, func(key string) bool { return key == f.target })
			f.targetKey = alias
			f.bodies["/api/location?"+f.query()] = releaseJSON(map[string]any{
				"directory": alias, "project": map[string]string{"directory": f.target, "canonical": f.root},
			})
		}, "", 1},
		{"single target alias owner unrelated cwd", "target", func(f *releaseFixture, alias string) {
			f.loaded = slices.DeleteFunc(f.loaded, func(key string) bool { return key == f.target })
			f.targetKey = alias
			f.bodies["/api/location?"+f.query()] = releaseJSON(map[string]any{
				"directory": alias, "project": map[string]string{"directory": f.target, "canonical": f.root},
			})
			f.bodies["/api/pty?"+f.query()] = f.scoped(alias, `[{"id":"pty","status":"running","cwd":`+releaseJSON(f.unrelated)+`}]`)
		}, "running shell or terminal", 0},
		{"active session target alias", "target", func(f *releaseFixture, alias string) {
			f.loaded = slices.DeleteFunc(f.loaded, func(key string) bool { return key == alias })
			f.bodies["/api/session/active"] = `{"data":{"sesBusy":{"type":"running"}}}`
			f.session("sesBusy", alias)
		}, "active session", 0},
		{"job child target alias", "target", func(f *releaseFixture, alias string) {
			f.loaded = slices.DeleteFunc(f.loaded, func(key string) bool { return key == alias })
			f.bodies["/api/experimental/job?status=running"] = `{"data":[{"id":"job","kind":"subagent","status":"running","sessionID":"sesOwner","childSessionID":"sesChild"}]}`
			f.session("sesOwner", f.root)
			f.session("sesChild", alias)
		}, "unfinished background job", 0},
		{"persistent terminal target alias cwd", "target", func(f *releaseFixture, alias string) {
			f.loaded = slices.DeleteFunc(f.loaded, func(key string) bool { return key == alias })
			f.bodies["/api/experimental/session/sesIdle/terminal"] = `{"data":[{"id":"pty","sessionID":"sesIdle","cwd":` + releaseJSON(alias) + `,"status":"running"}]}`
		}, "running persistent terminal", 0},
		{"target alias wrong cache same identity", "target", func(f *releaseFixture, alias string) {
			f.loaded = slices.DeleteFunc(f.loaded, func(key string) bool { return key == f.target })
			f.targetKey = alias
			f.bodies["/api/location?"+f.query()] = releaseJSON(map[string]any{
				"directory": f.target, "project": map[string]string{"directory": f.target, "canonical": f.root},
			})
		}, "cache key differs", 0},
		{"target key changes before disposal", "target", func(f *releaseFixture, alias string) {
			f.loaded = slices.DeleteFunc(f.loaded, func(key string) bool { return key == alias })
			f.beforeReinventory = func() {
				f.loaded[slices.Index(f.loaded, f.target)] = alias
				f.beforeReinventory = nil
			}
		}, "cache key loaded or changed", 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newReleaseFixture(t)
			owner := map[string]string{"primary": f.root, "target": f.target, "unrelated": f.unrelated}[tc.owner]
			alias := releaseAlias(t, owner)
			f.loaded = append(f.loaded, alias)
			for _, endpoint := range []string{"/api/shell", "/api/pty"} {
				key := url.Values{"location[directory]": {alias}}.Encode()
				f.bodies[endpoint+"?"+key] = f.scoped(alias, "[]")
			}
			tc.arrange(f, alias)
			before := slices.Clone(f.loaded)
			err := f.run(t)
			if (tc.want == "" && err != nil) || (tc.want != "" && (err == nil || !strings.Contains(err.Error(), tc.want))) || f.deletes != tc.deletes {
				t.Fatalf("release=%v deletes=%d, want %q deletes=%d", err, f.deletes, tc.want, tc.deletes)
			}
			if tc.want == "" {
				for _, key := range before {
					for _, endpoint := range []string{"/api/shell", "/api/pty"} {
						query := endpoint + "?" + url.Values{"location[directory]": {key}}.Encode()
						if !slices.ContainsFunc(f.calls, func(cmd execx.Cmd) bool {
							return slices.Equal(cmd.Args, []string{"api", "get", query})
						}) {
							t.Fatalf("cache key was not independently queried: %s", query)
						}
					}
				}
			}
			if tc.name == "unloaded target" || strings.HasPrefix(tc.name, "ambiguous target") {
				for _, cmd := range f.calls {
					if len(cmd.Args) == 3 && strings.Contains(cmd.Args[2], "location%5Bdirectory%5D="+url.QueryEscape(f.target)) {
						t.Fatalf("unloaded or ambiguous target received a scoped request: %+v", cmd)
					}
				}
			}
		})
	}
}

func TestReleaseLocationPreservesPhysicalIdentity(t *testing.T) {
	for _, phase := range []string{"before disposal", "after disposal"} {
		t.Run(phase, func(t *testing.T) {
			f := newReleaseFixture(t)
			alias := releaseAlias(t, f.unrelated)
			f.loaded = append(f.loaded, alias)
			for _, endpoint := range []string{"/api/shell", "/api/pty"} {
				key := url.Values{"location[directory]": {alias}}.Encode()
				f.bodies[endpoint+"?"+key] = f.scoped(alias, "[]")
			}
			old := filepath.Join(t.TempDir(), "old")
			replace := func() {
				if err := os.Rename(f.unrelated, old); err != nil {
					t.Fatal(err)
				}
				if err := os.Mkdir(f.unrelated, 0o755); err != nil {
					t.Fatal(err)
				}
			}
			wantDeletes := 1
			if phase == "before disposal" {
				wantDeletes = 0
				f.beforeReinventory = func() { replace(); f.beforeReinventory = nil }
			} else {
				f.afterDelete = replace
			}
			err := f.run(t)
			if err == nil || !strings.Contains(err.Error(), "changed identity") || f.deletes != wantDeletes {
				t.Fatalf("replaced physical directory accepted: release=%v deletes=%d", err, f.deletes)
			}
		})
	}
}

func TestReleaseActivityPagination(t *testing.T) {
	f := newReleaseFixture(t)
	f.bodies["/api/session?limit=100"] = `{"data":[{"id":"sesIdle","location":{"directory":` + releaseJSON(f.root) + `}}],"cursor":{"next":"page & 2"}}`
	next := "/api/session?" + url.Values{"limit": {"100"}, "cursor": {"page & 2"}}.Encode()
	f.bodies[next] = `{"data":[{"id":"sesSecond","location":{"directory":` + releaseJSON(f.target) + `}}],"cursor":{}}`
	f.bodies["/api/experimental/session/sesSecond/terminal"] = `{"data":[{"id":"pty","sessionID":"sesSecond","cwd":` + releaseJSON(f.target) + `,"status":"running"}]}`
	if err := f.run(t); err == nil || !strings.Contains(err.Error(), "persistent terminal") || f.deletes != 0 {
		t.Fatalf("later-page activity accepted: %v", err)
	}
	f.bodies[next] = f.bodies["/api/session?limit=100"]
	if err := f.run(t); err == nil || !strings.Contains(err.Error(), "repeated session") {
		t.Fatalf("repeated pagination accepted: %v", err)
	}
}

func TestReleaseLocationBoundedAndCanceled(t *testing.T) {
	runner := releaseContextRunner(func(ctx context.Context, cmd execx.Cmd) (execx.Result, error) {
		deadline, ok := ctx.Deadline()
		if !ok || time.Until(deadline) > 2*time.Minute {
			t.Fatal("release has no finite activity/disposal deadline")
		}
		return execx.Result{}, ctx.Err()
	})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := ReleaseLocation(ctx, runner, t.TempDir(), t.TempDir()); err == nil {
		t.Fatal("canceled release succeeded")
	}
}

type releaseContextRunner func(context.Context, execx.Cmd) (execx.Result, error)

func (f releaseContextRunner) Run(ctx context.Context, cmd execx.Cmd) (execx.Result, error) {
	return f(ctx, cmd)
}
