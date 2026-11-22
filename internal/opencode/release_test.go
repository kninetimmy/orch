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
	"strconv"
	"strings"
	"sync"
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
	beforeCall              func(context.Context, execx.Cmd) error
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
	var originalDeadline time.Time
	var recorded sync.Mutex
	runner := releaseContextRunner(func(ctx context.Context, ctxCmd execx.Cmd) (execx.Result, error) {
		deadline, ok := ctx.Deadline()
		if !ok || time.Until(deadline) > 2*time.Minute {
			t.Error("host command has no bounded deadline")
		}
		recorded.Lock()
		if originalDeadline.IsZero() {
			originalDeadline = deadline
		} else if !originalDeadline.Equal(deadline) {
			t.Error("release reset its original deadline")
		}
		f.calls = append(f.calls, ctxCmd)
		recorded.Unlock()
		if ctxCmd.Dir != f.root {
			t.Errorf("host command escaped primary cwd: %+v", ctxCmd)
			return execx.Result{}, errors.New("wrong fixture cwd")
		}
		if f.beforeCall != nil {
			if err := f.beforeCall(ctx, ctxCmd); err != nil {
				return execx.Result{}, err
			}
		}
		if slices.Equal(ctxCmd.Args, []string{"--version"}) {
			if ctxCmd.Name != f.executable {
				return execx.Result{}, errors.New("credential-token")
			}
			return execx.Result{Stdout: "opencode v2.3.0"}, nil
		}
		if ctxCmd.Name != f.executable || len(ctxCmd.Args) != 3 || ctxCmd.Args[0] != "api" {
			t.Errorf("unexpected invocation: %+v", ctxCmd)
			return execx.Result{}, errors.New("unexpected fixture invocation")
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
		t.Errorf("unexpected endpoint: %+v", ctxCmd)
		return execx.Result{}, errors.New("unexpected fixture endpoint")
	})
	return ReleaseLocation(context.Background(), runner, f.root, f.target)
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
		{"background shell primary", func(f *releaseFixture) {
			f.bodies["/api/experimental/job?status=running"] = `{"data":[{"id":"job","kind":"shell","status":"running","sessionID":"sesOwner"}]}`
			f.session("sesOwner", f.root)
		}, "", 1},
		{"background image target", func(f *releaseFixture) {
			f.bodies["/api/experimental/job?status=running"] = `{"data":[{"id":"job","kind":"image","status":"running","sessionID":"sesOwner"}]}`
			f.session("sesOwner", f.target)
		}, "unfinished background job", 0},
		{"background image primary", func(f *releaseFixture) {
			f.bodies["/api/experimental/job?status=running"] = `{"data":[{"id":"job","kind":"image","status":"running","sessionID":"sesOwner"}]}`
			f.session("sesOwner", f.root)
		}, "", 1},
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
		{"Code Mode owner in target", func(f *releaseFixture) {
			f.bodies["/api/experimental/job?status=running"] = `{"data":[{"id":"credential-token","kind":"codemode","status":"running","sessionID":"sescredential-token","title":"credential-token"}]}`
			f.session("sescredential-token", f.target)
		}, "running Code Mode job prevents removal: its owning session is in a target checkout", 0},
		{"Code Mode owner in primary", func(f *releaseFixture) {
			f.bodies["/api/experimental/job?status=running"] = `{"data":[{"id":"credential-token","kind":"codemode","status":"running","sessionID":"sescredential-token","title":"credential-token: uses only primary"}]}`
			f.session("sescredential-token", f.root)
		}, "running Code Mode job prevents removal: its working directories cannot be verified", 0},
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
			f.bodies["/api/experimental/job?status=running"] = `{"data":[{"id":"job","kind":"credential-token","status":"running","sessionID":"sesOwner"}]}`
			f.session("sesOwner", f.root)
		}, "unsupported background-job evidence", 0},
		{"malformed job inventory", func(f *releaseFixture) {
			f.bodies["/api/experimental/job?status=running"] = `{"data":null}`
		}, "malformed or oversized background-job inventory", 0},
		{"Code Mode job missing ID", func(f *releaseFixture) {
			f.bodies["/api/experimental/job?status=running"] = `{"data":[{"kind":"codemode","status":"running","sessionID":"sesOwner","title":"credential-token"}]}`
			f.session("sesOwner", f.root)
		}, "malformed background-job evidence", 0},
		{"Code Mode job wrong status", func(f *releaseFixture) {
			f.bodies["/api/experimental/job?status=running"] = `{"data":[{"id":"job","kind":"codemode","status":"credential-token","sessionID":"sesOwner"}]}`
			f.session("sesOwner", f.root)
		}, "malformed background-job evidence", 0},
		{"Code Mode owner identifier invalid", func(f *releaseFixture) {
			f.bodies["/api/experimental/job?status=running"] = `{"data":[{"id":"job","kind":"codemode","status":"running","sessionID":"credential-token"}]}`
		}, "malformed activity session identifier", 0},
		{"Code Mode owner identity mismatch", func(f *releaseFixture) {
			f.bodies["/api/experimental/job?status=running"] = `{"data":[{"id":"job","kind":"codemode","status":"running","sessionID":"sesOwner"}]}`
			f.bodies["/api/session/sesOwner"] = `{"data":{"id":"sescredential-token","location":{"directory":` + releaseJSON(f.target) + `}}}`
		}, "activity session identity mismatch", 0},
		{"Code Mode owner location unverifiable", func(f *releaseFixture) {
			f.bodies["/api/experimental/job?status=running"] = `{"data":[{"id":"job","kind":"codemode","status":"running","sessionID":"sesOwner"}]}`
			f.session("sesOwner", filepath.Join(f.root, "credential-token-missing"))
		}, "host directory identity is unavailable", 0},
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
		}, "", 1},
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
					if tc.name == "alias disappeared before disposal" && key == alias {
						for _, cmd := range f.calls {
							if len(cmd.Args) == 3 && strings.Contains(cmd.Args[2], "?"+url.Values{"location[directory]": {alias}}.Encode()) {
								t.Fatalf("disappeared key received a scoped request: %+v", cmd)
							}
						}
						continue
					}
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

func TestReleaseLocationEvictionRecheck(t *testing.T) {
	deadlineDiagnostic := "unavailable"
	if runtime.GOOS == "windows" {
		deadlineDiagnostic = "deadline exceeded"
	}
	for _, tc := range []struct {
		name    string
		arrange func(*testing.T, *releaseFixture, string)
		fresh   func(*testing.T, *releaseFixture, string)
		want    string
		deletes int
	}{
		{name: "stable loaded", deletes: 1},
		{name: "stable unloaded", arrange: func(_ *testing.T, f *releaseFixture, _ string) {
			f.loaded = slices.DeleteFunc(f.loaded, func(key string) bool { return key == f.target })
		}},
		{name: "foreground", want: "active session", fresh: func(_ *testing.T, f *releaseFixture, _ string) {
			f.bodies["/api/session/active"] = `{"data":{"sesBusy":{"type":"running"}}}`
			f.session("sesBusy", f.target)
		}},
		{name: "background child", want: "unfinished background job", fresh: func(_ *testing.T, f *releaseFixture, _ string) {
			f.bodies["/api/experimental/job?status=running"] = `{"data":[{"id":"job","kind":"subagent","status":"running","sessionID":"sesOwner","childSessionID":"sesChild"}]}`
			f.session("sesOwner", f.root)
			f.session("sesChild", f.target)
		}},
		{name: "persistent owner", want: "running persistent terminal", fresh: func(_ *testing.T, f *releaseFixture, _ string) {
			f.bodies["/api/experimental/session/sesIdle/terminal"] = `{"data":[{"id":"pty","sessionID":"sesIdle","cwd":` + releaseJSON(f.root) + `,"status":"running"}]}`
		}},
		{name: "persistent cwd", want: "running persistent terminal", fresh: func(_ *testing.T, f *releaseFixture, _ string) {
			f.bodies["/api/session?limit=100"] = `{"data":[{"id":"sesIdle","location":{"directory":` + releaseJSON(f.root) + `}}],"cursor":{}}`
			f.bodies["/api/experimental/session/sesIdle/terminal"] = `{"data":[{"id":"pty","sessionID":"sesIdle","cwd":` + releaseJSON(f.target) + `,"status":"running"}]}`
		}},
		{name: "ordinary owner", want: "running shell or terminal", fresh: func(_ *testing.T, f *releaseFixture, _ string) {
			f.bodies["/api/shell?"+f.query()] = f.scoped(f.target, `[{"id":"shell","status":"running","cwd":`+releaseJSON(f.root)+`}]`)
		}},
		{name: "ordinary alias cwd", want: "running shell or terminal", fresh: func(_ *testing.T, f *releaseFixture, alias string) {
			key := url.Values{"location[directory]": {alias}}.Encode()
			f.bodies["/api/pty?"+key] = f.scoped(alias, `[{"id":"pty","status":"running","cwd":`+releaseJSON(f.target)+`}]`)
		}},
		{name: "target roots", want: "roots differ", fresh: func(_ *testing.T, f *releaseFixture, _ string) {
			f.bodies["/api/location?"+f.query()] = releaseJSON(map[string]any{
				"directory": f.target, "project": map[string]string{"directory": f.target, "canonical": f.target},
			})
		}},
		{name: "target exact key", want: "cache key differs", fresh: func(_ *testing.T, f *releaseFixture, _ string) {
			f.bodies["/api/location?"+f.query()] = releaseJSON(map[string]string{"directory": f.target + string(filepath.Separator) + "."})
		}},
		{name: "ordinary exact key", want: "wrong-root", fresh: func(_ *testing.T, f *releaseFixture, alias string) {
			key := url.Values{"location[directory]": {alias}}.Encode()
			f.bodies["/api/pty?"+key] = f.scoped(f.root, "[]")
		}},
		{name: "malformed activity", want: "malformed", fresh: func(_ *testing.T, f *releaseFixture, _ string) {
			f.bodies["/api/session/active"] = `{}`
		}},
		{name: "malformed jobs", want: "malformed", fresh: func(_ *testing.T, f *releaseFixture, _ string) {
			f.bodies["/api/experimental/job?status=running"] = `{}`
		}},
		{name: "malformed terminals", want: "malformed", fresh: func(_ *testing.T, f *releaseFixture, _ string) {
			f.bodies["/api/experimental/session/sesIdle/terminal"] = `{}`
		}},
		{name: "malformed ordinary", want: "malformed", fresh: func(_ *testing.T, f *releaseFixture, alias string) {
			key := url.Values{"location[directory]": {alias}}.Encode()
			f.bodies["/api/shell?"+key] = f.scoped(alias, "null")
		}},
		{name: "unavailable evidence", want: "unavailable", fresh: func(_ *testing.T, f *releaseFixture, _ string) {
			f.fail["/api/session/active"] = true
		}},
		{name: "malformed fresh inventory", want: "malformed", fresh: func(_ *testing.T, f *releaseFixture, _ string) {
			f.bodies["/api/debug/location"] = `null`
		}},
		{name: "deadline exhausted", want: deadlineDiagnostic},
		{name: "multiple disappearances", want: "another loaded location disappeared", arrange: func(_ *testing.T, f *releaseFixture, alias string) {
			f.beforeReinventory = func() {
				f.loaded = slices.DeleteFunc(f.loaded, func(key string) bool { return key == alias })
			}
		}},
		{name: "second disappearance", want: "another loaded location disappeared", fresh: func(_ *testing.T, f *releaseFixture, alias string) {
			f.beforeReinventory = func() {
				f.loaded = slices.DeleteFunc(f.loaded, func(key string) bool { return key == alias })
			}
		}},
		{name: "missing key reappears", want: "inventory changed again", fresh: func(_ *testing.T, f *releaseFixture, _ string) {
			f.loaded = append(f.loaded, f.unrelated)
		}},
		{name: "new key appears", want: "inventory changed again", fresh: func(_ *testing.T, f *releaseFixture, alias string) {
			f.loaded = append(f.loaded, alias+string(filepath.Separator)+".")
		}},
		{name: "replacement alias is not preservation", want: "beyond one unrelated disappearance", arrange: func(_ *testing.T, f *releaseFixture, _ string) {
			f.beforeReinventory = func() {
				f.loaded = append(f.loaded, f.unrelated+string(filepath.Separator)+".")
				f.beforeReinventory = nil
			}
		}},
		{name: "target reentry", want: "cache key loaded or changed", arrange: func(_ *testing.T, f *releaseFixture, _ string) {
			f.loaded = slices.DeleteFunc(f.loaded, func(key string) bool { return key == f.target })
		}, fresh: func(_ *testing.T, f *releaseFixture, _ string) {
			f.loaded = append(f.loaded, f.target)
		}},
		{name: "target key changes", want: "cache key loaded or changed", fresh: func(t *testing.T, f *releaseFixture, _ string) {
			f.loaded[slices.Index(f.loaded, f.target)] = releaseAlias(t, f.target)
		}},
		{name: "target unloads on recheck", want: "inventory changed again", fresh: func(_ *testing.T, f *releaseFixture, _ string) {
			f.loaded = slices.DeleteFunc(f.loaded, func(key string) bool { return key == f.target })
		}},
		{name: "final exact key lost", want: "another loaded location disappeared", deletes: 1, arrange: func(_ *testing.T, f *releaseFixture, alias string) {
			f.afterDelete = func() {
				f.loaded = slices.DeleteFunc(f.loaded, func(key string) bool { return key == alias })
			}
		}},
		{name: "final target reentry", want: "did not unload", deletes: 1, arrange: func(_ *testing.T, f *releaseFixture, _ string) {
			f.afterDelete = func() { f.loaded = append(f.loaded, f.target) }
		}},
		{name: "final descendant", want: "unexpected loaded descendant", deletes: 1, arrange: func(t *testing.T, f *releaseFixture, _ string) {
			f.afterDelete = func() {
				child := filepath.Join(f.target, "child")
				if err := os.Mkdir(child, 0o755); err != nil {
					t.Fatal(err)
				}
				f.loaded = append(f.loaded, child)
			}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newReleaseFixture(t)
			alias := releaseAlias(t, f.root)
			f.loaded = append(f.loaded, alias)
			for _, endpoint := range []string{"/api/shell", "/api/pty"} {
				key := url.Values{"location[directory]": {alias}}.Encode()
				f.bodies[endpoint+"?"+key] = f.scoped(alias, "[]")
			}
			if tc.arrange != nil {
				tc.arrange(t, f, alias)
			}
			evicted, fresh := false, false
			f.beforeCall = func(_ context.Context, cmd execx.Cmd) error {
				if len(cmd.Args) != 3 {
					return nil
				}
				// Evict naturally during global probing, before ordinary scoped
				// reads. Only the synthetic host cache changes, not the directory.
				if !evicted && cmd.Args[2] == "/api/experimental/job?status=running" {
					f.loaded = slices.DeleteFunc(f.loaded, func(key string) bool { return key == f.unrelated })
					evicted = true
				}
				if f.inventories == 2 && !fresh && cmd.Args[2] == "/api/session/active" {
					fresh = true
					if tc.name == "deadline exhausted" {
						return context.DeadlineExceeded
					}
					if tc.fresh != nil {
						tc.fresh(t, f, alias)
					}
				}
				return nil
			}
			err := f.run(t)
			if (tc.want == "" && err != nil) || (tc.want != "" && (err == nil || !strings.Contains(err.Error(), tc.want))) || f.deletes != tc.deletes {
				t.Fatalf("release=%v deletes=%d, want %q deletes=%d", err, f.deletes, tc.want, tc.deletes)
			}
			count := func(endpoint string) int {
				total := 0
				for _, cmd := range f.calls {
					if len(cmd.Args) == 3 && cmd.Args[2] == endpoint {
						total++
					}
				}
				return total
			}
			for _, cmd := range f.calls {
				if len(cmd.Args) == 3 {
					endpoint, err := url.Parse(cmd.Args[2])
					if err != nil {
						t.Fatal(err)
					}
					key := endpoint.Query().Get("location[directory]")
					if key == f.unrelated || tc.name == "stable unloaded" && key == f.target {
						t.Fatalf("unloaded key received a scoped request: %+v", cmd)
					}
				}
			}
			if f.inventories > 4 || tc.want == "" && f.inventories != 4 || tc.deletes == 1 && f.inventories != 4 {
				t.Fatalf("unexpected inventory retries: %d", f.inventories)
			}
			if tc.want == "" {
				for endpoint, want := range map[string]int{
					"/api/session/active": 4, "/api/experimental/job?status=running": 4,
					"/api/session?limit=100": 2, "/api/experimental/session/sesIdle/terminal": 2,
				} {
					if got := count(endpoint); got != want {
						t.Fatalf("fresh evidence %s reads=%d want=%d", endpoint, got, want)
					}
				}
				keys := []string{f.root, alias}
				roots := 0
				if tc.deletes == 1 {
					keys = append(keys, f.target)
					roots = 2
				}
				if got := count("/api/location?" + f.query()); got != roots {
					t.Fatalf("target root reads=%d want=%d", got, roots)
				}
				for _, key := range keys {
					for _, endpoint := range []string{"/api/shell", "/api/pty"} {
						if count(endpoint+"?"+url.Values{"location[directory]": {key}}.Encode()) != 1 {
							t.Fatalf("current exact key lacks independent ordinary evidence: %s %s", endpoint, key)
						}
					}
				}
			}
		})
	}
}

func TestReleaseLocationEvictionIdentity(t *testing.T) {
	for _, directory := range []string{"missing key", "unloaded target"} {
		for _, phase := range []string{"at disappearance", "during recheck", "before DELETE"} {
			for _, replace := range []bool{false, true} {
				t.Run(directory+"/"+phase+"/replace="+strconv.FormatBool(replace), func(t *testing.T) {
					f := newReleaseFixture(t)
					f.loaded = slices.DeleteFunc(f.loaded, func(key string) bool { return key == f.target })
					path := f.unrelated
					if directory == "unloaded target" {
						path = f.target
					}
					change := func() {
						if err := os.Rename(path, filepath.Join(t.TempDir(), "old")); err != nil {
							t.Fatal(err)
						}
						if replace {
							if err := os.Mkdir(path, 0o755); err != nil {
								t.Fatal(err)
							}
						}
					}
					f.beforeReinventory = func() {
						if f.inventories == 2 {
							f.loaded = slices.DeleteFunc(f.loaded, func(key string) bool { return key == f.unrelated })
							if phase == "at disappearance" {
								change()
							}
						}
					}
					changed := false
					f.beforeCall = func(_ context.Context, cmd execx.Cmd) error {
						if len(cmd.Args) == 3 && !changed &&
							(phase == "during recheck" && f.inventories == 2 && cmd.Args[2] == "/api/session/active" ||
								phase == "before DELETE" && cmd.Args[2] == "/api/pty?"+url.Values{"location[directory]": {f.root}}.Encode()) {
							changed = true
							change()
						}
						return nil
					}
					if err := f.run(t); err == nil || !strings.Contains(err.Error(), "changed identity") || f.deletes != 0 {
						t.Fatalf("unverifiable/replaced %s accepted: err=%v deletes=%d", directory, err, f.deletes)
					}
				})
			}
		}
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
