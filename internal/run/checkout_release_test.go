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
	"strings"
	"testing"

	"github.com/kninetimmy/orch/internal/config"
	"github.com/kninetimmy/orch/internal/execx"
	"github.com/kninetimmy/orch/internal/state"
)

type checkoutHost struct {
	t             *testing.T
	root          string
	loaded        map[string]bool
	active        string
	disposalFails bool
	calls         []execx.Cmd
	deleted       []string
	partial       string
}

func (h *checkoutHost) Run(ctx context.Context, cmd execx.Cmd) (execx.Result, error) {
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
		h.t.Fatalf("unexpected non-Git command: %+v", cmd)
	}
	if slices.Equal(cmd.Args, []string{"--version"}) {
		return execx.Result{Stdout: "opencode v2.3.0"}, nil
	}
	if len(cmd.Args) != 3 || cmd.Args[0] != "api" || cmd.Dir != h.root {
		h.t.Fatalf("unexpected host command: %+v", cmd)
	}
	endpoint, err := url.Parse(cmd.Args[2])
	if err != nil {
		h.t.Fatal(err)
	}
	target := endpoint.Query().Get("location[directory]")
	body := func(value any) (execx.Result, error) {
		data, err := json.Marshal(value)
		if err != nil {
			h.t.Fatal(err)
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
		return execx.Result{Stdout: `{"data":[],"cursor":{}}`}, nil
	case "/api/location":
		return body(map[string]any{"directory": target, "project": map[string]string{"directory": target, "canonical": h.root}})
	case "/api/shell", "/api/pty":
		return body(map[string]any{"location": map[string]string{"directory": target}, "data": []any{}})
	}
	h.t.Fatalf("unexpected host endpoint: %+v", cmd)
	return execx.Result{}, nil
}

func openCodeCheckoutFixture(t *testing.T, phase state.Phase) (string, string, string, *checkoutHost, Env) {
	t.Helper()
	content, err := os.ReadFile(filepath.Join("..", "agents", "testdata", "opencode-mixed-provider.toml"))
	if err != nil {
		t.Fatal(err)
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
	execution, err := executionConfig(cfg, "opencode")
	if err != nil {
		t.Fatal(err)
	}
	digest, err := execution.Digest()
	if err != nil {
		t.Fatal(err)
	}
	st, err := state.EnterDelivery(root, "opencode", state.PlanRef{
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
	for _, scenario := range []string{"success", "unloaded", "active", "disposal", "partial"} {
		t.Run(scenario, func(t *testing.T) {
			root, _, _, host, env := openCodeCheckoutFixture(t, state.PhaseAbandoned)
			executor, review := worktreeAbs(root, 1), reviewWorktreeAbs(root, 1)
			branch := loadRun(t, root).Run.Issues[0].Branch
			rawGit(t, root, "push", "origin", branch)
			before := stateBytes(t, root)
			switch scenario {
			case "active":
				host.active = executor
			case "disposal":
				host.disposalFails = true
			case "unloaded":
				delete(host.loaded, executor)
				delete(host.loaded, review)
			case "partial":
				host.partial = executor
			}
			req := []byte(`{"schema_version":1,"issue_number":1,"statement":"cleanup-issue"}`)
			res, err := Cleanup(context.Background(), env, req)
			if scenario == "active" || scenario == "disposal" {
				if err == nil || res != nil || len(host.deleted) != 0 || !bytes.Equal(before, stateBytes(t, root)) {
					t.Fatalf("unsafe failed cleanup: %+v %v deleted=%v", res, err, host.deleted)
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

func TestOpenCodeInteriorLinksBeforeSideEffects(t *testing.T) {
	for _, scenario := range []string{"replace review", "cleanup executor", "cleanup review"} {
		t.Run(scenario, func(t *testing.T) {
			root, first, second, host, env := openCodeCheckoutFixture(t, state.PhaseInReview)
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
			for _, destination := range []string{root, sibling} {
				sentinel := filepath.Join(destination, "sentinel")
				if err := os.WriteFile(sentinel, []byte("preserve\n"), 0o644); err != nil {
					t.Fatal(err)
				}
				link := filepath.Join(scratch, filepath.Base(destination))
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
					if err := os.Remove(link); err != nil {
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
		})
	}
}

func TestInspectCheckoutBoundsAndRefusals(t *testing.T) {
	root := t.TempDir()
	if err := inspectCheckout(context.Background(), root); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := inspectCheckout(ctx, root); err == nil {
		t.Fatal("canceled inspection accepted")
	}
	if err := inspectCheckout(context.Background(), filepath.Join(root, "missing")); err == nil {
		t.Fatal("unavailable inspection accepted")
	}
	directory := root
	for range 65 {
		directory = filepath.Join(directory, "d")
		if err := os.Mkdir(directory, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := inspectCheckout(context.Background(), root); err == nil || !strings.Contains(err.Error(), "inspection bounds") {
		t.Fatalf("unbounded inspection accepted: %v", err)
	}
}
