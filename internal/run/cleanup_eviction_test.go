package run

import (
	"bytes"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/kninetimmy/orch/internal/execx"
	"github.com/kninetimmy/orch/internal/gitops"
	"github.com/kninetimmy/orch/internal/state"
)

const cleanupLocationInventory = "/api/debug/location"
const cleanupRunningJobs = "/api/experimental/job?status=running"

func TestBoundMergedOpenCodeCleanupLateEviction(t *testing.T) {
	for _, tc := range []struct {
		when   string
		reads  int
		passes int
	}{
		{"post-disposal", 3, 1},
		{"late opening", 4, 1},
		{"during late activity", 5, 2},
	} {
		t.Run(tc.when, func(t *testing.T) {
			root, _, _, host, env := openCodeCheckoutFixture(t, state.PhaseMerged)
			executor, review := worktreeAbs(root, 1), reviewWorktreeAbs(root, 1)
			run := loadRun(t, root).Run
			ref := "refs/heads/" + run.Issues[0].Branch
			rawGit(t, root, "push", "origin", ref)
			lost, alsoLost, survivor := t.TempDir(), t.TempDir(), t.TempDir()
			host.loaded[lost], host.loaded[alsoLost], host.loaded[survivor] = true, true, true
			jobs, inventories := 0, 0
			ordinary := map[[2]string]int{}
			evicted, observed := false, false
			evict := func() {
				delete(host.loaded, lost)
				delete(host.loaded, alsoLost)
				evicted = true
			}
			host.beforeAPI = func(endpoint *url.URL) {
				if len(host.deleted) != 2 {
					return
				}
				if observed {
					checkCleanupLateScope(t, host, endpoint, root, survivor)
				} else {
					checkCleanupLateScope(t, host, endpoint, root, survivor, lost, alsoLost)
				}
				switch endpoint.RequestURI() {
				case cleanupLocationInventory:
					inventories++
					if (tc.when == "post-disposal" && inventories == 1) || (tc.when == "late opening" && inventories == 2) {
						evict()
					}
					observed = evicted
				case cleanupRunningJobs:
					jobs++
					if tc.when == "during late activity" && jobs == 1 {
						evict()
					}
				default:
					if observed {
						key := endpoint.Query().Get("location[directory]")
						ordinary[[2]string{key, endpoint.Path}]++
					}
				}
			}
			pushed := false
			host.beforeGit = func(cmd execx.Cmd) {
				if !slices.Contains(cmd.Args, "push") {
					return
				}
				pushed = true
				if !observed || inventories != tc.reads || jobs != tc.passes {
					t.Fatalf("push without expected fresh verification: inventories=%d jobs=%d (want %d/%d)", inventories, jobs, tc.reads, tc.passes)
				}
				for _, key := range []string{root, survivor} {
					for _, endpoint := range []string{"/api/shell", "/api/pty"} {
						if ordinary[[2]string{key, endpoint}] != 1 {
							t.Fatalf("missing or repeated fresh evidence for %s at %s: %v", endpoint, key, ordinary)
						}
					}
				}
			}
			req := fmt.Appendf(nil, `{"schema_version":1,"issue_number":1,"statement":"cleanup-issue","run_id":%q}`, run.ID)
			res, err := Cleanup(t.Context(), env, req)
			if err != nil || res == nil || res.Phase != state.PhaseCleaned || !pushed || !slices.Equal(host.deleted, []string{executor, review}) {
				t.Fatalf("cleanup with stable unrelated losses = %+v, %v; Git push=%t, disposals=%v", res, err, pushed, host.deleted)
			}
			for _, dir := range []string{executor, review} {
				if _, err := os.Lstat(dir); !os.IsNotExist(err) {
					t.Fatalf("cleaned with a remaining checkout: %v", err)
				}
			}
			if loadRun(t, root).Run.Issues[0].Phase != state.PhaseCleaned || rawGit(t, root, "ls-remote", "--heads", "origin", ref) != "" ||
				rawGit(t, root, "branch", "--list", run.Issues[0].Branch) != "" {
				t.Fatal("cleanup did not save cleaned and remove both branch refs")
			}
		})
	}
}

func TestBoundMergedOpenCodeCleanupLateEvictionRefusals(t *testing.T) {
	const changedAgain = "changed again after an unrelated disappearance"
	for _, tc := range []struct {
		change, when, want string
	}{
		{"new key", "at loss", "new or reappearing location cache key"},
		{"reappearing key", "retry opening", "new or reappearing location cache key"},
		{"target reload", "retry opening", "target reactivated"},
		{"descendant reload", "retry opening", "unexpected loaded descendant"},
		{"missing location replaced", "at loss", "directory disappeared or changed identity"},
		{"missing location vanishes", "retry closing", "directory disappeared or changed identity"},
		{"survivor replaced", "retry closing", "another loaded location changed identity"},
		{"another loss", "retry closing", changedAgain},
		{"shell", "retry opening", "running shell or terminal"},
		{"PTY", "activity retry", "running shell or terminal"},
		{"unverifiable cwd", "retry opening", "host directory identity is unavailable"},
		{"Code Mode", "activity retry", "retry `orch_cleanup` directly"},
		{"incomplete jobs", "activity retry", "malformed or oversized background-job inventory"},
		{"another loss", "remote deletion", changedAgain},
		{"review reload", "executor removal", "target reactivated"},
		{"another loss", "review removal", changedAgain},
	} {
		t.Run(tc.when+"/"+tc.change, func(t *testing.T) {
			root, _, approved, host, env := openCodeCheckoutFixture(t, state.PhaseMerged)
			executor, review := worktreeAbs(root, 1), reviewWorktreeAbs(root, 1)
			run := loadRun(t, root).Run
			ref := "refs/heads/" + run.Issues[0].Branch
			rawGit(t, root, "push", "origin", ref)
			stateBefore := stateBytes(t, root)
			git, err := gitops.Open(t.Context(), execx.Local{}, root)
			if err != nil {
				t.Fatal(err)
			}
			listWorktrees := func() []gitops.Worktree {
				list, err := git.ListWorktrees(t.Context())
				if err != nil {
					t.Fatal(err)
				}
				return list
			}
			registrations := listWorktrees()
			dirs := []string{executor, review}
			infos := make([]os.FileInfo, len(dirs))
			for i, dir := range dirs {
				info, err := os.Stat(dir)
				if err != nil {
					t.Fatal(err)
				}
				infos[i] = info
			}
			lost, survivor := t.TempDir(), t.TempDir()
			host.loaded[lost], host.loaded[survivor] = true, true
			injected := false
			inject := func() {
				if injected {
					t.Fatal("injected the same change more than once")
				}
				injected = true
				switch tc.change {
				case "new key":
					host.loaded[lost+string(filepath.Separator)+"."] = true // Same directory, different exact key.
				case "reappearing key":
					host.loaded[lost] = true
				case "target reload":
					host.loaded[executor] = true
				case "review reload":
					host.loaded[review] = true
				case "descendant reload":
					// Use a directory already present so the fixture leaves
					// the executor's cleanliness and identity unchanged.
					host.loaded[filepath.Join(executor, ".orchestrator")] = true
				case "missing location replaced", "missing location vanishes", "survivor replaced":
					path := lost
					if tc.change == "survivor replaced" {
						path = survivor
					}
					if err := os.Rename(path, filepath.Join(t.TempDir(), "original")); err != nil {
						t.Fatal(err)
					}
					if tc.change != "missing location vanishes" {
						if err := os.Mkdir(path, 0o755); err != nil {
							t.Fatal(err)
						}
					}
				case "another loss":
					delete(host.loaded, survivor)
				case "shell", "PTY", "unverifiable cwd":
					endpoint, cwd := "/api/pty", review
					switch tc.change {
					case "shell":
						endpoint, cwd = "/api/shell", executor
					case "unverifiable cwd":
						cwd = filepath.Join(survivor, "missing")
					}
					host.ordinaryCwds = map[string]map[string]string{endpoint: {survivor: cwd}}
				case "Code Mode":
					host.jobInventory = `{"data":[{"id":"jobLate","kind":"codemode","sessionID":"sesPrimary","status":"running"}]}`
				case "incomplete jobs":
					host.jobInventory = `{"data":null}`
				}
			}
			inventories := 0
			host.beforeAPI = func(endpoint *url.URL) {
				if len(host.deleted) != 2 {
					return
				}
				if tc.when == "activity retry" && inventories < 3 {
					checkCleanupLateScope(t, host, endpoint, root, survivor, lost)
				} else {
					checkCleanupLateScope(t, host, endpoint, root, survivor)
				}
				if endpoint.RequestURI() != cleanupLocationInventory {
					return
				}
				inventories++
				switch inventories {
				case 1:
					if tc.when != "activity retry" {
						delete(host.loaded, lost)
					}
					if tc.when == "at loss" {
						inject()
					}
				case 2:
					if tc.when == "retry opening" {
						inject()
					}
				case 3:
					if tc.when == "activity retry" {
						delete(host.loaded, lost)
					}
					if tc.when == "retry closing" {
						inject()
					}
				case 4:
					if tc.when == "activity retry" {
						inject()
					}
				}
			}
			var mutations []string
			host.beforeGit = func(cmd execx.Cmd) {
				switch {
				case slices.Contains(cmd.Args, "push"):
					mutations = append(mutations, "remote deletion")
				case slices.Contains(cmd.Args, "remove"):
					switch cmd.Args[len(cmd.Args)-1] {
					case executor:
						mutations = append(mutations, "executor removal")
					case review:
						mutations = append(mutations, "review removal")
					default:
						t.Fatalf("attempted to remove another checkout: %+v", cmd)
					}
				case slices.Contains(cmd.Args, "update-ref") || slices.Contains(cmd.Args, "-D"):
					t.Fatalf("local ref mutation after unsafe evidence: %+v", cmd)
				default:
					return
				}
				if tc.when == mutations[len(mutations)-1] {
					inject()
				}
			}
			req := fmt.Appendf(nil, `{"schema_version":1,"issue_number":1,"statement":"cleanup-issue","run_id":%q}`, run.ID)
			res, err := Cleanup(t.Context(), env, req)
			if !injected || res != nil || err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("wanted %q refusal; injected=%t, result=%+v, err=%v", tc.want, injected, res, err)
			}
			if !slices.Equal(host.deleted, dirs) || !bytes.Equal(stateBefore, stateBytes(t, root)) {
				t.Fatal("refusal retried host disposal or changed the issue phase")
			}
			order := []string{"remote deletion", "executor removal", "review removal"}
			prior := slices.Index(order, tc.when) + 1
			if !slices.Equal(mutations, order[:prior]) {
				t.Fatalf("Git mutations before refusal: %v, want %v", mutations, order[:prior])
			}
			removed := dirs[:max(0, prior-1)]
			wantRegs := slices.DeleteFunc(registrations, func(w gitops.Worktree) bool { return slices.Contains(removed, w.Path) })
			if !slices.Equal(listWorktrees(), wantRegs) {
				t.Fatal("refusal changed a remaining checkout registration")
			}
			for i, dir := range dirs {
				info, err := os.Stat(dir)
				if prior >= i+2 {
					if !os.IsNotExist(err) {
						t.Fatalf("expected successful earlier checkout deletion: %s: %v", dir, err)
					}
				} else if err != nil || !os.SameFile(infos[i], info) {
					t.Fatalf("refusal did not preserve checkout identity: %s: %v", dir, err)
				}
			}
			wantRemote := ""
			if prior == 0 {
				wantRemote = approved + "\t" + ref
			}
			if strings.TrimSpace(rawGit(t, root, "rev-parse", ref)) != approved ||
				strings.TrimSpace(rawGit(t, root, "ls-remote", "--heads", "origin", ref)) != wantRemote {
				t.Fatal("refusal changed a remaining branch ref")
			}
		})
	}
}

func checkCleanupLateScope(t *testing.T, host *checkoutHost, endpoint *url.URL, surviving ...string) {
	t.Helper()
	switch endpoint.RequestURI() {
	case cleanupLocationInventory, cleanupRunningJobs:
		return
	}
	if endpoint.Path == "/api/shell" || endpoint.Path == "/api/pty" {
		if key := endpoint.Query().Get("location[directory]"); host.loaded[key] && slices.Contains(surviving, key) {
			return
		}
	}
	t.Fatalf("unexpected scoped or loading host request after release: %s", endpoint)
}
