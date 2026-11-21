package run

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/kninetimmy/orch/internal/config"
	"github.com/kninetimmy/orch/internal/execx"
	"github.com/kninetimmy/orch/internal/gitops"
	"github.com/kninetimmy/orch/internal/opencode"
	"github.com/kninetimmy/orch/internal/state"
)

type checkoutHost struct {
	mu            sync.Mutex
	t             *testing.T
	root          string
	loaded        map[string]bool
	active        string
	disposalFails bool
	calls         []execx.Cmd
	deleted       []string
	partial       string
	beforeAPI     func(*url.URL)
	inventories   int
	sessions      map[string]string
	terminalCwds  map[string]string
	reactivated   []string
}

func (h *checkoutHost) Run(ctx context.Context, cmd execx.Cmd) (execx.Result, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.calls = append(h.calls, cmd)
	if cmd.Name == "git" {
		res, err := (execx.Local{}).Run(ctx, cmd)
		if len(cmd.Args) >= 3 && cmd.Args[0] == "worktree" && cmd.Args[1] == "remove" && cmd.Args[len(cmd.Args)-1] == h.partial && err == nil && res.ExitCode == 0 {
			// Model Windows Git deregistration followed by leftover path and
			// failure. Only this newly removed, owned fixture is recreated.
			if err := os.Mkdir(h.partial, 0o755); err != nil {
				h.t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(h.partial, "residue"), []byte("preserve\n"), 0o644); err != nil {
				h.t.Fatal(err)
			}
			h.partial = ""
			return execx.Result{ExitCode: 255, Stderr: "synthetic partial removal"}, nil
		}
		return res, err
	}
	if cmd.Name != "opencode2" {
		h.t.Errorf("unexpected non-Git command: %+v", cmd)
		return execx.Result{}, fmt.Errorf("unexpected fixture command")
	}
	if slices.Equal(cmd.Args, []string{"--version"}) {
		return execx.Result{Stdout: "opencode v2.3.0"}, nil
	}
	if len(cmd.Args) != 3 || cmd.Args[0] != "api" || cmd.Dir != h.root {
		h.t.Errorf("unexpected host command: %+v", cmd)
		return execx.Result{}, fmt.Errorf("unexpected fixture command")
	}
	endpoint, err := url.Parse(cmd.Args[2])
	if err != nil {
		h.t.Error(err)
		return execx.Result{}, err
	}
	if h.beforeAPI != nil {
		h.beforeAPI(endpoint)
	}
	target := endpoint.Query().Get("location[directory]")
	body := func(value any) (execx.Result, error) {
		data, err := json.Marshal(value)
		if err != nil {
			h.t.Error(err)
			return execx.Result{}, err
		}
		return execx.Result{Stdout: string(data)}, nil
	}
	switch endpoint.Path {
	case "/openapi.json":
		return execx.Result{Stdout: `{"paths":{"/api/debug/location":{"get":{"operationId":"debug.location.list"},"delete":{"operationId":"debug.location.evict","parameters":[{"name":"location","in":"query","style":"deepObject","explode":true}]}}}}`}, nil
	case "/api/debug/location":
		if cmd.Args[1] == "delete" {
			if target == "" || target == h.root || !h.loaded[target] {
				h.t.Fatalf("unsafe target disposal: %+v", cmd)
			}
			if h.disposalFails {
				return execx.Result{ExitCode: 9, Stderr: "synthetic unsupported disposal"}, nil
			}
			h.deleted = append(h.deleted, target)
			delete(h.loaded, target)
			return execx.Result{}, nil
		}
		h.inventories++
		list := []map[string]string{}
		for directory := range h.loaded {
			list = append(list, map[string]string{"directory": directory})
		}
		return body(list)
	case "/api/session/active":
		if h.active != "" {
			return execx.Result{Stdout: `{"data":{"sesBusy":{"type":"running"}}}`}, nil
		}
		return execx.Result{Stdout: `{"data":{}}`}, nil
	case "/api/session/sesBusy":
		return body(map[string]any{"data": map[string]any{"id": "sesBusy", "location": map[string]string{"directory": h.active}}})
	case "/api/experimental/job":
		return execx.Result{Stdout: `{"data":[]}`}, nil
	case "/api/session":
		sessions := []map[string]any{}
		cursor := map[string]any{}
		ids := make([]string, 0, len(h.sessions))
		for id := range h.sessions {
			ids = append(ids, id)
		}
		slices.Sort(ids)
		start := 0
		if c := endpoint.Query().Get("cursor"); c != "" {
			start, err = strconv.Atoi(c)
			if err != nil || start < 0 || start >= len(ids) {
				h.t.Fatalf("unexpected fixture cursor: %q", c)
			}
		}
		end := min(start+100, len(ids))
		if end < len(ids) {
			cursor["next"] = strconv.Itoa(end)
		}
		for _, id := range ids[start:end] {
			sessions = append(sessions, map[string]any{"id": id, "location": map[string]string{"directory": h.sessions[id]}})
		}
		return body(map[string]any{"data": sessions, "cursor": cursor})
	case "/api/location":
		return body(map[string]any{"directory": target, "project": map[string]string{"directory": target, "canonical": h.root}})
	case "/api/shell", "/api/pty":
		return body(map[string]any{"location": map[string]string{"directory": target}, "data": []any{}})
	}
	if strings.HasPrefix(endpoint.Path, "/api/experimental/session/") && strings.HasSuffix(endpoint.Path, "/terminal") {
		id := strings.TrimSuffix(strings.TrimPrefix(endpoint.Path, "/api/experimental/session/"), "/terminal")
		directory, ok := h.sessions[id]
		if !ok {
			h.t.Errorf("unknown completed fixture session: %s", id)
			return execx.Result{}, fmt.Errorf("unknown fixture session")
		}
		// Deterministic model of a host terminal-inventory read loading its
		// completed session's assigned checkout. No real host is involved.
		if !h.loaded[directory] {
			h.loaded[directory] = true
			h.reactivated = append(h.reactivated, directory)
		}
		if cwd, ok := h.terminalCwds[id]; ok {
			return body(map[string]any{"data": []map[string]string{
				{"id": "pty-" + id, "sessionID": id, "cwd": cwd, "status": "running"},
			}})
		}
		return execx.Result{Stdout: `{"data":[]}`}, nil
	}
	h.t.Errorf("unexpected host endpoint: %+v", cmd)
	return execx.Result{}, fmt.Errorf("unexpected fixture endpoint")
}

func TestOpenCodeCleanupDoesNotReactivateReleasedCheckouts(t *testing.T) {
	for _, sequential := range []bool{true, false} {
		t.Run(fmt.Sprintf("old sequential=%t", sequential), func(t *testing.T) {
			root, _, _, host, env := openCodeCheckoutFixture(t, state.PhaseAbandoned)
			executor, review := worktreeAbs(root, 1), reviewWorktreeAbs(root, 1)
			host.sessions = map[string]string{"sesExecutor": executor, "sesReview": review}
			sessions := map[string]string{"sesExecutor": executor, "sesReview": review}
			if sequential {
				// Characterize the old engine handoff, using the single-target API.
				for _, target := range []string{executor, review} {
					if err := opencode.ReleaseLocation(t.Context(), host, root, target); err != nil {
						t.Fatal(err)
					}
				}
				if !slices.Equal(host.reactivated, []string{executor}) || !host.loaded[executor] {
					t.Fatalf("old handoff did not demonstrate re-entry: %v", host.reactivated)
				}
				t.Log("old second-target terminal preflight reopens the released executor")
			} else {
				res, err := Cleanup(t.Context(), env, []byte(`{"schema_version":1,"issue_number":1,"statement":"cleanup-issue"}`))
				if err != nil || res.Phase != state.PhaseCleaned {
					t.Fatalf("cleanup=%+v %v", res, err)
				}
				if len(host.reactivated) != 0 || host.loaded[executor] || host.loaded[review] {
					t.Fatalf("cleanup reopened a released target: %v", host.reactivated)
				}
				released := false
				for _, cmd := range host.calls {
					if cmd.Name != "opencode2" || len(cmd.Args) != 3 {
						continue
					}
					endpoint, err := url.Parse(cmd.Args[2])
					if err != nil {
						t.Fatal(err)
					}
					if cmd.Args[1] == "delete" {
						released = true
					} else if released && endpoint.Path != "/api/debug/location" {
						t.Fatalf("loading host probe after release: %+v", cmd)
					}
				}
				t.Log("batched cleanup has no loading host probes after the first release")
			}
			if len(host.sessions) != len(sessions) {
				t.Fatal("cleanup deleted completed session history")
			}
			for id, directory := range sessions {
				if host.sessions[id] != directory {
					t.Fatal("cleanup moved a completed session")
				}
			}
		})
	}
}

func TestOpenCodeCleanupFinalReactivationRefusesBeforeGit(t *testing.T) {
	for _, kind := range []string{"executor", "review"} {
		t.Run(kind, func(t *testing.T) {
			root, first, _, host, env := openCodeCheckoutFixture(t, state.PhaseAbandoned)
			executor, review := worktreeAbs(root, 1), reviewWorktreeAbs(root, 1)
			target := executor
			if kind == "review" {
				target = review
			}
			branch := loadRun(t, root).Run.Issues[0].Branch
			rawGit(t, root, "push", "origin", branch)
			before := stateBytes(t, root)
			registrations := rawGit(t, root, "worktree", "list", "--porcelain")
			host.beforeAPI = func(endpoint *url.URL) {
				if len(host.deleted) == 2 && endpoint.Path == "/api/debug/location" && endpoint.RawQuery == "" {
					host.loaded[target] = true // External client, not a cleanup probe.
				}
			}
			res, err := Cleanup(t.Context(), env, []byte(`{"schema_version":1,"issue_number":1,"statement":"cleanup-issue"}`))
			if err == nil || res != nil || !strings.Contains(err.Error(), "reactivated") || strings.Contains(err.Error(), target) {
				t.Fatalf("reactivation accepted or wrong refusal: %+v %v", res, err)
			}
			if !bytes.Equal(before, stateBytes(t, root)) || registrations != rawGit(t, root, "worktree", "list", "--porcelain") {
				t.Fatal("refusal changed state or either Git registration")
			}
			detachedHeadAt(t, review, first)
			if !strings.Contains(rawGit(t, root, "ls-remote", "--heads", "origin", branch), branch) ||
				!strings.Contains(rawGit(t, root, "branch", "--list", branch), branch) {
				t.Fatal("refusal deleted a branch")
			}
			for _, cmd := range host.calls {
				if cmd.Name == "git" && (slices.Contains(cmd.Args, "remove") || slices.Contains(cmd.Args, "push") ||
					slices.Contains(cmd.Args, "prune") || slices.Contains(cmd.Args, "-D")) {
					t.Fatalf("reactivation reached Git mutation: %+v", cmd)
				}
			}
			t.Log("final reactivation preserves both registrations, branches and phase")
		})
	}
}

func openCodeCheckoutFixture(t *testing.T, phase state.Phase) (string, string, string, *checkoutHost, Env) {
	t.Helper()
	return checkoutFixture(t, phase, "opencode")
}

func checkoutFixture(t *testing.T, phase state.Phase, hostName string) (string, string, string, *checkoutHost, Env) {
	t.Helper()
	content, err := os.ReadFile(filepath.Join("..", "agents", "testdata", "opencode-mixed-provider.toml"))
	if err != nil {
		t.Fatal(err)
	}
	if hostName != "opencode" {
		content = []byte(testConfigTOMLBothHosts)
	}
	root := newLifecycleRepoWithConfig(t, string(content))
	first := strings.TrimSpace(rawGit(t, root, "rev-parse", "HEAD"))
	if err := os.WriteFile(filepath.Join(root, "second.txt"), []byte("second\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	rawGit(t, root, "add", "-A")
	rawGit(t, root, "commit", "-m", "second")
	second := strings.TrimSpace(rawGit(t, root, "rev-parse", "HEAD"))
	cfg, err := config.Load(root)
	if err != nil {
		t.Fatal(err)
	}
	execution, err := executionConfig(cfg, hostName)
	if err != nil {
		t.Fatal(err)
	}
	digest, err := execution.Digest()
	if err != nil {
		t.Fatal(err)
	}
	st, err := state.EnterDelivery(root, hostName, state.PlanRef{
		Title: "Synthetic test", Digest: "synthetic", ApprovedBy: "test", ApprovedAt: fixedNow(),
		ConfigRevision: cfg.ConfigRevision, ContractVersion: ContractVersion, ExecutionDigest: digest,
	}, []state.Issue{{PlanID: "synthetic", Phase: state.PhasePlanned}})
	if err != nil {
		t.Fatal(err)
	}
	st.Run.Issues = []state.Issue{fixtureIssue("synthetic", 1, phase)}
	if err := state.Save(root, st); err != nil {
		t.Fatal(err)
	}
	executor, review := worktreeAbs(root, 1), reviewWorktreeAbs(root, 1)
	rawGit(t, root, "worktree", "add", "-b", st.Run.Issues[0].Branch, executor, second)
	rawGit(t, root, "worktree", "add", "--detach", review, first)
	host := &checkoutHost{t: t, root: root, loaded: map[string]bool{root: true, executor: true, review: true}}
	return root, first, second, host, Env{RepoRoot: root, Runner: host, Now: fixedNow}
}

func TestOpenCodeReviewCheckoutRelease(t *testing.T) {
	for _, failure := range []string{"", "active", "disposal"} {
		t.Run(failure, func(t *testing.T) {
			root, first, second, host, env := openCodeCheckoutFixture(t, state.PhaseInReview)
			review := reviewWorktreeAbs(root, 1)
			before := stateBytes(t, root)
			if failure == "active" {
				host.active = review
			}
			host.disposalFails = failure == "disposal"
			res, err := ReviewWorktree(context.Background(), env, []byte(reviewWorktreeJSON(1, second)))
			if failure != "" {
				if err == nil || res != nil || len(host.deleted) != 0 {
					t.Fatalf("unsafe refused replacement: result=%+v err=%v deleted=%v", res, err, host.deleted)
				}
				detachedHeadAt(t, review, first)
			} else {
				if err != nil || res.HeadOID != second || !slices.Equal(host.deleted, []string{review}) {
					t.Fatalf("replacement result=%+v err=%v deleted=%v", res, err, host.deleted)
				}
				detachedHeadAt(t, review, second)
			}
			if !bytes.Equal(before, stateBytes(t, root)) {
				t.Fatal("review replacement wrote run state")
			}
		})
	}
}

func TestOpenCodeCleanupReleaseAndPartialRetry(t *testing.T) {
	for _, scenario := range []string{"success", "unloaded", "active", "review active", "disposal", "partial",
		"executor descendant", "review descendant", "executor alias", "review alias"} {
		t.Run(scenario, func(t *testing.T) {
			root, _, _, host, env := openCodeCheckoutFixture(t, state.PhaseAbandoned)
			executor, review := worktreeAbs(root, 1), reviewWorktreeAbs(root, 1)
			branch := loadRun(t, root).Run.Issues[0].Branch
			rawGit(t, root, "push", "origin", branch)
			before := stateBytes(t, root)
			registrations := rawGit(t, root, "worktree", "list", "--porcelain")
			wantRefusal := ""
			switch scenario {
			case "active", "review active":
				host.active = executor
				if scenario == "review active" {
					host.active = review
				}
				wantRefusal = "active session"
			case "disposal":
				host.disposalFails = true
				wantRefusal = "unavailable or unsupported"
			case "unloaded":
				delete(host.loaded, executor)
				delete(host.loaded, review)
			case "partial":
				host.partial = executor
			case "executor descendant", "review descendant", "executor alias", "review alias":
				target := executor
				if strings.HasPrefix(scenario, "review") {
					target = review
				}
				key := target + string(filepath.Separator) + "."
				wantRefusal = "ambiguous target cache aliases"
				if strings.HasSuffix(scenario, "descendant") {
					key = filepath.Join(target, "loaded-descendant")
					if err := os.Mkdir(key, 0o755); err != nil {
						t.Fatal(err)
					}
					wantRefusal = "unexpected loaded descendant"
				}
				host.loaded[key] = true
			}
			req := []byte(`{"schema_version":1,"issue_number":1,"statement":"cleanup-issue"}`)
			res, err := Cleanup(context.Background(), env, req)
			if wantRefusal != "" {
				if err == nil || !strings.Contains(err.Error(), wantRefusal) || res != nil || len(host.deleted) != 0 || !bytes.Equal(before, stateBytes(t, root)) {
					t.Fatalf("unsafe failed cleanup: %+v %v deleted=%v", res, err, host.deleted)
				}
				if registrations != rawGit(t, root, "worktree", "list", "--porcelain") ||
					!strings.Contains(rawGit(t, root, "branch", "--list", branch), branch) {
					t.Fatal("host refusal changed registration or local branch")
				}
				if !strings.Contains(rawGit(t, root, "ls-remote", "--heads", "origin", branch), branch) {
					t.Fatal("host refusal deleted remote branch")
				}
				for _, directory := range []string{executor, review} {
					if _, err := os.Stat(directory); err != nil {
						t.Fatal("host refusal removed checkout")
					}
				}
				return
			}
			if scenario == "partial" {
				if err == nil || res != nil || !bytes.Equal(before, stateBytes(t, root)) {
					t.Fatalf("partial cleanup=%+v %v", res, err)
				}
				deletions := len(host.deleted)
				_, err = Cleanup(context.Background(), env, req)
				if err == nil || !strings.Contains(err.Error(), "unregistered") || len(host.deleted) != deletions {
					t.Fatalf("lost-registration retry must preserve residue without host disposal: %v", err)
				}
				marker := filepath.Join(executor, "residue")
				if content, err := os.ReadFile(marker); err != nil || string(content) != "preserve\n" {
					t.Fatal("partial-removal residue changed")
				}
				// Separate, inspected fixture-only repair, not product recovery.
				if err := os.Remove(marker); err != nil {
					t.Fatal(err)
				}
				if err := os.Remove(executor); err != nil {
					t.Fatal(err)
				}
				res, err = Cleanup(context.Background(), env, req)
			}
			if err != nil || res.Phase != state.PhaseCleaned {
				t.Fatalf("cleanup=%+v %v", res, err)
			}
			if scenario != "unloaded" && !slices.Equal(host.deleted, []string{executor, review}) {
				t.Fatalf("wrong disposal targets/order: %v", host.deleted)
			}
			if scenario == "unloaded" && len(host.deleted) != 0 {
				t.Fatal("already unloaded targets were evicted")
			}
			for _, directory := range []string{executor, review} {
				if _, err := os.Lstat(directory); !os.IsNotExist(err) {
					t.Fatalf("cleaned with remaining checkout: %s (%v)", directory, err)
				}
			}
			if !host.loaded[root] {
				t.Fatal("primary location was evicted")
			}
		})
	}
}

func TestOpenCodeCheckoutReleaseEviction(t *testing.T) {
	for _, operation := range []string{"replace review", "cleanup executor", "cleanup review"} {
		for _, scenario := range []string{"stable", "fresh active", "second change", "final change"} {
			t.Run(operation+"/"+scenario, func(t *testing.T) {
				phase := state.PhaseAbandoned
				if operation == "replace review" {
					phase = state.PhaseInReview
				}
				root, first, second, host, env := openCodeCheckoutFixture(t, phase)
				executor, review := worktreeAbs(root, 1), reviewWorktreeAbs(root, 1)
				target := review
				if operation == "cleanup executor" {
					target = executor
				}
				branch := loadRun(t, root).Run.Issues[0].Branch
				rawGit(t, root, "push", "origin", branch)
				unrelated, other := t.TempDir(), t.TempDir()
				host.loaded[unrelated], host.loaded[other] = true, true
				unrelatedInfo, err := os.Stat(unrelated)
				if err != nil {
					t.Fatal(err)
				}
				before := stateBytes(t, root)
				registrations := rawGit(t, root, "worktree", "list", "--porcelain")
				triggerInventory := 0
				evictionCall := 0
				host.beforeAPI = func(endpoint *url.URL) {
					if triggerInventory == 0 && endpoint.Path == "/api/session/active" {
						delete(host.loaded, unrelated)
						triggerInventory = host.inventories
						evictionCall = len(host.calls) - 1
					}
					if triggerInventory == 0 {
						return
					}
					if scenario == "fresh active" && host.inventories == triggerInventory+1 {
						host.active = target
					}
					if endpoint.Path == "/api/debug/location" && endpoint.RawQuery == "" {
						if scenario == "second change" && host.inventories == triggerInventory+1 ||
							scenario == "final change" && slices.Contains(host.deleted, target) {
							delete(host.loaded, other)
						}
					}
				}
				if operation == "replace review" {
					result, releaseErr := ReviewWorktree(t.Context(), env, []byte(reviewWorktreeJSON(1, second)))
					err = releaseErr
					if scenario == "stable" {
						if err != nil || result.HeadOID != second || !slices.Equal(host.deleted, []string{review}) {
							t.Fatalf("replacement=%+v %v deleted=%v", result, err, host.deleted)
						}
						detachedHeadAt(t, review, second)
						if !bytes.Equal(before, stateBytes(t, root)) {
							t.Fatal("replacement wrote run state")
						}
					} else if result != nil {
						t.Fatalf("refused replacement returned success: %+v", result)
					}
				} else {
					result, releaseErr := Cleanup(t.Context(), env, []byte(`{"schema_version":1,"issue_number":1,"statement":"cleanup-issue"}`))
					err = releaseErr
					if scenario == "stable" {
						if err != nil || result.Phase != state.PhaseCleaned || !slices.Equal(host.deleted, []string{executor, review}) {
							t.Fatalf("cleanup=%+v %v deleted=%v", result, err, host.deleted)
						}
						for _, directory := range []string{executor, review} {
							if _, err := os.Lstat(directory); !os.IsNotExist(err) {
								t.Fatalf("cleanup retained checkout: %s: %v", directory, err)
							}
						}
						if strings.TrimSpace(rawGit(t, root, "ls-remote", "--heads", "origin", branch)) != "" ||
							strings.Contains(rawGit(t, root, "branch", "--list", branch), branch) {
							t.Fatal("successful cleanup retained a branch")
						}
					} else if result != nil {
						t.Fatalf("refused cleanup returned success: %+v", result)
					}
				}
				if triggerInventory == 0 {
					t.Fatal("fixture did not evict during the requested checkout release")
				}
				currentInfo, statErr := os.Stat(unrelated)
				if statErr != nil || !os.SameFile(unrelatedInfo, currentInfo) {
					t.Fatal("cache eviction changed its physical directory")
				}
				for i, cmd := range host.calls {
					if i >= evictionCall && cmd.Name == "opencode2" && len(cmd.Args) == 3 {
						endpoint, err := url.Parse(cmd.Args[2])
						if err != nil {
							t.Fatal(err)
						}
						if endpoint.Query().Get("location[directory]") == unrelated {
							t.Fatalf("evicted key received a scoped request: %+v", cmd)
						}
					}
				}
				if scenario == "stable" {
					if !host.loaded[root] || !host.loaded[other] {
						t.Fatal("stable release lost another location")
					}
					t.Log("stable eviction permits normal lifecycle; only exact target keys disposed")
					return
				}
				want := map[string]string{"fresh active": "active session", "second change": "another loaded location disappeared", "final change": "another loaded location disappeared"}[scenario]
				if err == nil || !strings.Contains(err.Error(), want) {
					t.Fatalf("release=%v, want %q", err, want)
				}
				if !bytes.Equal(before, stateBytes(t, root)) || rawGit(t, root, "worktree", "list", "--porcelain") != registrations {
					t.Fatal("refusal changed issue state or checkout registration")
				}
				detachedHeadAt(t, review, first)
				if !strings.Contains(rawGit(t, root, "ls-remote", "--heads", "origin", branch), branch) ||
					!strings.Contains(rawGit(t, root, "branch", "--list", branch), branch) {
					t.Fatal("refusal deleted a branch")
				}
				for _, cmd := range host.calls {
					if cmd.Name == "git" && (slices.Contains(cmd.Args, "remove") || slices.Contains(cmd.Args, "push") ||
						slices.Contains(cmd.Args, "prune") || slices.Contains(cmd.Args, "-D")) {
						t.Fatalf("host refusal reached Git mutation: %+v", cmd)
					}
				}
				wantDeleted := []string{}
				if scenario == "final change" {
					if operation == "replace review" {
						wantDeleted = append(wantDeleted, review)
					} else {
						wantDeleted = append(wantDeleted, executor, review)
					}
					if host.inventories != triggerInventory+3 {
						t.Fatal("failed final observation was retried")
					}
				}
				if !slices.Equal(host.deleted, wantDeleted) {
					t.Fatalf("unexpected disposal/retry: %v, want %v", host.deleted, wantDeleted)
				}
				t.Log("refusal preserves branches, both checkouts/registration and state; no Git mutation or disposal retry")
			})
		}
	}
}

func TestOpenCodeCheckoutOwnershipBeforeHost(t *testing.T) {
	for _, scenario := range []string{"dirty executor", "executor branch", "review branch", "unregistered review", "state path", "confirmation"} {
		t.Run(scenario, func(t *testing.T) {
			root, _, second, host, env := openCodeCheckoutFixture(t, state.PhaseInReview)
			review := reviewWorktreeAbs(root, 1)
			st := loadRun(t, root)
			st.Run.Issues[0].Phase = state.PhaseAbandoned
			verb := Cleanup
			req := []byte(`{"schema_version":1,"issue_number":1,"statement":"cleanup-issue"}`)
			switch scenario {
			case "dirty executor":
				if err := os.WriteFile(filepath.Join(worktreeAbs(root, 1), "untracked"), []byte("preserve"), 0o644); err != nil {
					t.Fatal(err)
				}
			case "executor branch":
				rawGit(t, worktreeAbs(root, 1), "checkout", "-b", "other")
			case "review branch":
				rawGit(t, review, "checkout", "-b", "review-work")
				verb = func(ctx context.Context, env Env, req []byte) (*CleanupResult, error) {
					_, err := ReviewWorktree(ctx, env, req)
					return nil, err
				}
				st.Run.Issues[0].Phase = state.PhaseInReview
				req = []byte(reviewWorktreeJSON(1, second))
			case "unregistered review":
				rawGit(t, root, "worktree", "remove", review)
				if err := os.Mkdir(review, 0o755); err != nil {
					t.Fatal(err)
				}
				// Executor is already absent as well: no host call should occur.
				rawGit(t, root, "worktree", "remove", worktreeAbs(root, 1))
			case "state path":
				st.Run.Issues[0].Worktree = ".orchestrator/worktrees/issue-999"
			case "confirmation":
				req = []byte(`{"schema_version":1,"issue_number":1,"statement":"no"}`)
			}
			if err := state.Save(root, st); err != nil {
				t.Fatal(err)
			}
			before := stateBytes(t, root)
			if _, err := verb(context.Background(), env, req); err == nil {
				t.Fatal("unsafe checkout was accepted")
			}
			if !bytes.Equal(before, stateBytes(t, root)) {
				t.Fatal("refused checkout changed state")
			}
			for _, cmd := range host.calls {
				if cmd.Name != "git" {
					t.Fatalf("%s reached host before ownership/removal checks: %+v", scenario, cmd)
				}
			}
		})
	}
}

func TestOpenCodeAbsentCheckoutsNeedNoHost(t *testing.T) {
	root, _, _, host, env := openCodeCheckoutFixture(t, state.PhaseAbandoned)
	for _, directory := range []string{worktreeAbs(root, 1), reviewWorktreeAbs(root, 1)} {
		rawGit(t, root, "worktree", "remove", directory)
		delete(host.loaded, directory)
	}
	res, err := Cleanup(context.Background(), env, []byte(`{"schema_version":1,"issue_number":1,"statement":"cleanup-issue"}`))
	if err != nil || res.Phase != state.PhaseCleaned {
		t.Fatalf("absent cleanup=%+v %v", res, err)
	}
	for _, cmd := range host.calls {
		if cmd.Name != "git" {
			t.Fatalf("absent checkout reached host: %s %v", cmd.Name, cmd.Args)
		}
	}
	if len(host.deleted) != 0 {
		t.Fatal(fmt.Sprint(host.deleted))
	}
}

func TestCheckoutInteriorLinksBeforeSideEffects(t *testing.T) {
	hosts := []string{"opencode"}
	if runtime.GOOS == "windows" {
		hosts = append(hosts, "claude", "codex")
	}
	for _, hostName := range hosts {
		for _, scenario := range []string{"replace review", "cleanup executor", "cleanup review"} {
			t.Run(hostName+"/"+scenario, func(t *testing.T) {
				root, first, second, host, env := checkoutFixture(t, state.PhaseInReview, hostName)
				executor, review := worktreeAbs(root, 1), reviewWorktreeAbs(root, 1)
				sibling := filepath.Join(root, WorktreeContainer, "sibling")
				rawGit(t, root, "worktree", "add", "--detach", sibling, first)
				branch := loadRun(t, root).Run.Issues[0].Branch
				rawGit(t, root, "push", "origin", branch)
				target := review
				if scenario == "cleanup executor" {
					target = executor
				}
				// Ignored links pass executor cleanliness, but may redirect native
				// Git removal into the primary or another checkout.
				if err := os.WriteFile(filepath.Join(root, ".git", "info", "exclude"), []byte(".scratch/\n"), 0o644); err != nil {
					t.Fatal(err)
				}
				scratch := filepath.Join(target, ".scratch")
				if err := os.Mkdir(scratch, 0o755); err != nil {
					t.Fatal(err)
				}
				var links []string
				for _, destination := range []string{root, sibling} {
					sentinel := filepath.Join(destination, "sentinel")
					if err := os.WriteFile(sentinel, []byte("preserve\n"), 0o644); err != nil {
						t.Fatal(err)
					}
					link := filepath.Join(scratch, filepath.Base(destination))
					links = append(links, link)
					if runtime.GOOS == "windows" {
						// Match paths' junction fixtures: paths are environment
						// values, never interpolated into a shell expression.
						cmd := exec.CommandContext(t.Context(), "powershell.exe", "-NoProfile", "-NonInteractive", "-Command",
							`New-Item -ItemType Junction -Path $env:ORCH_TEST_LINK -Target $env:ORCH_TEST_TARGET -ErrorAction Stop | Out-Null`)
						cmd.Env = append(os.Environ(), "ORCH_TEST_LINK="+link, "ORCH_TEST_TARGET="+destination)
						if out, err := cmd.CombinedOutput(); err != nil {
							t.Fatalf("create owned junction: %v: %s", err, out)
						}
					} else if err := os.Symlink(destination, link); err != nil {
						t.Fatal(err)
					}
					// Never send these adversarial fixtures to Git, even at test
					// teardown. Remove only the links this test just created.
					t.Cleanup(func() {
						if err := os.Remove(link); err != nil && !os.IsNotExist(err) {
							t.Error(err)
						}
					})
				}
				if scenario != "replace review" {
					st := loadRun(t, root)
					st.Run.Issues[0].Phase = state.PhaseAbandoned
					if err := state.Save(root, st); err != nil {
						t.Fatal(err)
					}
				}
				before := stateBytes(t, root)
				registrations := rawGit(t, root, "worktree", "list", "--porcelain")
				var err error
				if scenario == "replace review" {
					_, err = ReviewWorktree(context.Background(), env, []byte(reviewWorktreeJSON(1, second)))
				} else {
					_, err = Cleanup(context.Background(), env, []byte(`{"schema_version":1,"issue_number":1,"statement":"cleanup-issue"}`))
				}
				if err == nil || !strings.Contains(err.Error(), "linked or unverifiable checkout entry") {
					t.Fatalf("interior link accepted or wrong refusal: %v", err)
				}
				if !bytes.Equal(before, stateBytes(t, root)) || rawGit(t, root, "worktree", "list", "--porcelain") != registrations {
					t.Fatal("refusal changed issue state or registration")
				}
				if !strings.Contains(rawGit(t, root, "ls-remote", "--heads", "origin", branch), branch) {
					t.Fatal("refusal deleted remote branch")
				}
				for _, cmd := range host.calls {
					if cmd.Name != "git" || slices.Contains(cmd.Args, "remove") || slices.Contains(cmd.Args, "push") || slices.Contains(cmd.Args, "prune") {
						t.Fatalf("refusal reached host or Git mutation: %+v", cmd)
					}
				}
				for _, directory := range []string{root, sibling} {
					if data, err := os.ReadFile(filepath.Join(directory, "sentinel")); err != nil || string(data) != "preserve\n" {
						t.Fatalf("sentinel changed: %s: %v", directory, err)
					}
					if _, err := os.Lstat(filepath.Join(directory, ".git")); err != nil {
						t.Fatal(err)
					}
				}
				detachedHeadAt(t, review, first)
				t.Log("no host calls or Git mutations; primary/sibling sentinels, registrations and issue state preserved")
				// Separate, inspected fixture-only preparation AFTER refusal.
				for _, link := range links {
					if err := os.Remove(link); err != nil {
						t.Fatal(err)
					}
				}
				if scenario == "replace review" {
					result, err := ReviewWorktree(t.Context(), env, []byte(reviewWorktreeJSON(1, second)))
					if err != nil || result.HeadOID != second {
						t.Fatalf("link-free replacement retry: %+v %v", result, err)
					}
					detachedHeadAt(t, review, second)
				} else {
					result, err := Cleanup(t.Context(), env, []byte(`{"schema_version":1,"issue_number":1,"statement":"cleanup-issue"}`))
					if err != nil || result.Phase != state.PhaseCleaned {
						t.Fatalf("link-free cleanup retry: %+v %v", result, err)
					}
				}
				for _, directory := range []string{root, sibling} {
					if data, err := os.ReadFile(filepath.Join(directory, "sentinel")); err != nil || string(data) != "preserve\n" {
						t.Fatalf("normal retry changed outside bytes: %s: %v", directory, err)
					}
				}
				if hostName != "opencode" {
					for _, cmd := range host.calls {
						if cmd.Name != "git" {
							t.Fatalf("non-OpenCode retry reached host: %+v", cmd)
						}
					}
				}
				t.Log("link-free lifecycle retry succeeds and preserves outside bytes")
			})
		}
	}
}

func TestInspectCheckoutBoundsAndRefusals(t *testing.T) {
	root := t.TempDir()
	if err := gitops.InspectWorktree(context.Background(), root); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := gitops.InspectWorktree(ctx, root); err == nil {
		t.Fatal("canceled inspection accepted")
	}
	if err := gitops.InspectWorktree(context.Background(), filepath.Join(root, "missing")); err == nil {
		t.Fatal("unavailable inspection accepted")
	}
	directory := root
	for range 65 {
		directory = filepath.Join(directory, "d")
		if err := os.Mkdir(directory, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := gitops.InspectWorktree(context.Background(), root); err == nil || !strings.Contains(err.Error(), "inspection bounds") {
		t.Fatalf("unbounded inspection accepted: %v", err)
	}
}
