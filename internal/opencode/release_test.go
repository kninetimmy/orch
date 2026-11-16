package opencode

import (
	"context"
	"encoding/json"
	"errors"
	"net/url"
	"os"
	"path/filepath"
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
		f.bodies[endpoint+"?"+f.query()] = f.scoped(target, "[]")
	}
	return f
}

func (f *releaseFixture) query() string {
	return url.Values{"location[directory]": {f.target}}.Encode()
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
			f.loaded = slices.DeleteFunc(f.loaded, func(directory string) bool { return directory == f.target })
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
			f.bodies["/api/shell?"+f.query()] = f.scoped(f.target, `[{"id":"shell","status":"running"}]`)
		}, "running shell or terminal", 0},
		{"running PTY", func(f *releaseFixture) {
			f.bodies["/api/pty?"+f.query()] = f.scoped(f.target, `[{"id":"pty","status":"running"}]`)
		}, "running shell or terminal", 0},
		{"exited PTY", func(f *releaseFixture) {
			f.bodies["/api/pty?"+f.query()] = f.scoped(f.target, `[{"id":"pty","status":"exited"}]`)
		}, "", 1},
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
		{"duplicate target", func(f *releaseFixture) { f.loaded = append(f.loaded, f.target) }, "duplicate target", 0},
		{"duplicate unrelated", func(f *releaseFixture) { f.loaded = append(f.loaded, f.unrelated) }, "duplicate", 0},
		{"unverifiable inventory", func(f *releaseFixture) { f.loaded = append(f.loaded, filepath.Join(f.root, "missing")) }, "identity is unavailable", 0},
		{"wrong root", func(f *releaseFixture) {
			f.bodies["/api/location?"+f.query()] = `{"directory":` + releaseJSON(f.unrelated) + `}`
		}, "roots differ", 0},
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
					if len(cmd.Args) == 3 && strings.Contains(cmd.Args[2], "location%5Bdirectory%5D") {
						t.Fatalf("unloaded target was activated by a scoped read: %+v", cmd)
					}
				}
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
