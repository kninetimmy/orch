package run

import (
	"bytes"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/kninetimmy/orch/internal/execx"
	"github.com/kninetimmy/orch/internal/gitops"
	"github.com/kninetimmy/orch/internal/state"
)

func TestBoundMergedOpenCodeCleanupRequiresRecordedRepository(t *testing.T) {
	const wrong = "git@github.example.invalid:fixture/other.git"
	for _, tc := range []struct{ name, want string }{
		{"already retargeted", "does not identify the recorded PR repository"},
		{"no origin", "read and deletion destinations could not be matched"},
		{"ambiguous reads", "read and deletion destinations could not be matched"},
		{"ambiguous pushes", "read and deletion destinations could not be matched"},
		{"unverifiable SSH authentication", "could not verify the approved remote branch with strict OpenSSH"},
		{"unverifiable HTTPS headers", "only well-formed Authorization and Proxy-Authorization"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root, _, approved, host, env := openCodeCheckoutFixture(t, state.PhaseMerged)
			st := loadRun(t, root)
			ref := "refs/heads/" + st.Run.Issues[0].Branch
			originalURL := strings.TrimSpace(rawGit(t, root, "remote", "get-url", "origin"))
			dir := t.TempDir()
			other := filepath.Join(dir, "other.git")
			rawGit(t, dir, "init", "--bare", other)
			t.Setenv("ORCH_CLEANUP_FIXTURE_OTHER", filepath.ToSlash(other))
			for _, destination := range []string{"origin", wrong} {
				rawGit(t, root, "push", destination, ref)
			}
			executor := worktreeAbs(root, 1)
			var indexBefore []byte
			var readIndex func() []byte
			switch tc.name {
			case "already retargeted":
				rawGit(t, root, "remote", "set-url", "origin", wrong)
				// Leave the tracked content alone but make the index stat data stale.
				t.Setenv("GIT_OPTIONAL_LOCKS", "1")
				index := strings.TrimSpace(rawGit(t, executor, "rev-parse", "--path-format=absolute", "--git-path", "index"))
				readIndex = func() []byte {
					t.Helper()
					data, err := os.ReadFile(index)
					if err != nil {
						t.Fatal(err)
					}
					return data
				}
				tracked := filepath.Join(executor, "second.txt")
				info, err := os.Stat(tracked)
				if err != nil {
					t.Fatal(err)
				}
				stale := info.ModTime().Add(-time.Hour)
				if err := os.Chtimes(tracked, stale, stale); err != nil {
					t.Fatal(err)
				}
				indexBefore = readIndex()
			case "no origin":
				rawGit(t, root, "remote", "remove", "origin")
			case "ambiguous reads":
				rawGit(t, root, "remote", "set-url", "--add", "origin", wrong)
				rawGit(t, root, "remote", "set-url", "--push", "origin", originalURL)
			case "ambiguous pushes":
				rawGit(t, root, "remote", "set-url", "--push", "origin", originalURL)
				rawGit(t, root, "remote", "set-url", "--push", "--add", "origin", wrong)
			case "unverifiable SSH authentication":
				host.sshRefusal = true
			case "unverifiable HTTPS headers":
				// SSH/file are the only allowed protocols in this fixture. The
				// matching HTTPS URL must be refused without launching a transfer.
				rawGit(t, root, "remote", "set-url", "origin", "https://github.example.invalid/fixture/orch-smoke.git")
				t.Setenv("GIT_CONFIG_COUNT", "1")
				t.Setenv("GIT_CONFIG_KEY_0", "http.extraHeader")
				t.Setenv("GIT_CONFIG_VALUE_0", "X-Orch-Test: private fixture header")
			}
			regs, before := rawGit(t, root, "worktree", "list", "--porcelain"), stateBytes(t, root)
			req := fmt.Appendf(nil, `{"schema_version":1,"issue_number":1,"statement":"cleanup-issue","run_id":%q}`, st.Run.ID)
			res, err := Cleanup(t.Context(), env, req)
			local := strings.TrimSpace(rawGit(t, root, "for-each-ref", "--format=%(objectname)", ref))
			from := strings.TrimSpace(rawGit(t, os.Getenv("ORCH_CLEANUP_FIXTURE_REMOTE"), "for-each-ref", "--format=%(objectname)", ref))
			to := strings.TrimSpace(rawGit(t, other, "for-each-ref", "--format=%(objectname)", ref))
			regsKept := regs == rawGit(t, root, "worktree", "list", "--porcelain")
			if res != nil || err == nil || !strings.Contains(err.Error(), tc.want) ||
				len(host.deleted) != 0 || !regsKept || local != approved || from != approved || to != approved ||
				!bytes.Equal(before, stateBytes(t, root)) {
				t.Fatalf("wanted %q before mutation; result=%+v err=%v evictions=%d registrations kept=%t approved refs kept (local, original, alternate)=%t,%t,%t persisted phase=%s",
					tc.want, res, err, len(host.deleted), regsKept, local == approved, from == approved, to == approved, loadRun(t, root).Run.Issues[0].Phase)
			}
			for _, checkout := range []string{worktreeAbs(root, 1), reviewWorktreeAbs(root, 1)} {
				if _, err := os.Stat(checkout); err != nil || !host.loaded[checkout] {
					t.Fatalf("refusal removed checkout or its cache: %v", err)
				}
			}
			var transfers []string
			for _, call := range host.calls {
				if call.Name == "git" && (call.Args[0] == "ls-remote" || call.Args[0] == "push") {
					transfers = append(transfers, call.Args[0])
				}
			}
			if host.sshRefusal {
				if !slices.Equal(transfers, []string{"ls-remote"}) || strings.Contains(err.Error(), "private fixture") {
					t.Fatal("unverified SSH should stop at the first lookup without projecting raw failure output")
				}
			} else if len(transfers) != 0 {
				t.Fatal("unverifiable repository reached a Git transfer")
			}
			if tc.name == "unverifiable HTTPS headers" && (strings.Contains(err.Error(), "private fixture") || strings.Contains(err.Error(), "github.example.invalid")) {
				t.Fatal("HTTPS preflight disclosed header data or the raw endpoint")
			}
			if readIndex != nil {
				if !bytes.Equal(indexBefore, readIndex()) {
					t.Fatal("destination refusal rewrote the executor index")
				}
				// A plain status must find the tree clean yet rewrite the index.
				if out := rawGit(t, executor, "status", "--porcelain=v1", "--untracked-files=all"); out != "" {
					t.Fatalf("fixture is not clean: %q", out)
				}
				if bytes.Equal(indexBefore, readIndex()) {
					t.Fatal("plain git status did not reproduce the optional index write")
				}
				t.Log("destination refusal kept index bytes; plain git status was clean but refreshed them")
			}
		})
	}
}

func TestBoundMergedOpenCodeCleanupRefusesDirtyExecutor(t *testing.T) {
	for _, change := range []string{"modified", "staged", "untracked"} {
		t.Run(change, func(t *testing.T) {
			root, _, _, host, env := openCodeCheckoutFixture(t, state.PhaseMerged)
			executor := worktreeAbs(root, 1)
			file := "second.txt"
			if change == "untracked" {
				file = "untracked.txt"
			}
			if err := os.WriteFile(filepath.Join(executor, file), []byte("uncommitted work\n"), 0o644); err != nil {
				t.Fatal(err)
			}
			if change == "staged" {
				rawGit(t, executor, "add", "--", file)
			}
			st := loadRun(t, root)
			before := stateBytes(t, root)
			req := fmt.Appendf(nil, `{"schema_version":1,"issue_number":1,"statement":"cleanup-issue","run_id":%q}`, st.Run.ID)
			res, err := Cleanup(t.Context(), env, req)
			if res != nil || !errors.Is(err, gitops.ErrNotClean) || !strings.Contains(err.Error(), file) {
				t.Fatalf("wanted refusal naming %s; result=%+v err=%v", file, res, err)
			}
			if len(host.deleted) != 0 || !bytes.Equal(before, stateBytes(t, root)) {
				t.Fatal("dirty executor refusal evicted a cache or changed run state")
			}
		})
	}
}

func TestBoundMergedOpenCodeCleanupPreservesDriftedRefs(t *testing.T) {
	for _, tc := range []struct{ name, want string }{
		{"executor already advanced", "executor HEAD differs from the approved PR head"},
		{"local branch advanced without checkout", "local branch differs from the approved PR head"},
		{"remote branch already advanced", "remote branch differs from the approved PR head"},
		{"remote branch advances at push", "approved remote branch deletion was refused"},
		{"remote read and push disagree", "remote read and deletion destinations could not be matched"},
		{"executor branch in use by bisect", "rebase, am or bisect state is present or unverifiable"},
		{"unrelated worktree in use by bisect", "rebase, am or bisect state is present or unverifiable"},
		{"local branch advances at deletion", "cannot lock ref"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root, _, approved, host, env := openCodeCheckoutFixture(t, state.PhaseMerged)
			executor, review := worktreeAbs(root, 1), reviewWorktreeAbs(root, 1)
			st := loadRun(t, root)
			branch := st.Run.Issues[0].Branch
			ref := "refs/heads/" + branch
			rawGit(t, root, "push", "origin", branch)
			rawGit(t, executor, "commit", "--allow-empty", "-m", "unapproved work")
			advanced := strings.TrimSpace(rawGit(t, executor, "rev-parse", "HEAD"))
			if advanced == approved || strings.TrimSpace(rawGit(t, executor, "status", "--porcelain")) != "" {
				t.Fatal("fixture must have a clean, advanced executor")
			}
			localHead, remoteHead := approved, approved
			expectsRemoved := tc.name == "local branch advances at deletion"
			switch tc.name {
			case "executor already advanced":
				localHead = advanced
			case "local branch advanced without checkout":
				localHead = advanced
				rawGit(t, root, "worktree", "remove", executor)
				delete(host.loaded, executor)
			case "remote read and push disagree":
				// Keep a real but different deletion destination. No mutation
				// there is safe even if the read endpoint reports the expected ref.
				dir := t.TempDir()
				rawGit(t, dir, "init", "--bare", "other.git")
				rawGit(t, root, "config", "remote.origin.pushurl", filepath.Join(dir, "other.git"))
				rawGit(t, executor, "reset", "--hard", approved)
			case "executor branch in use by bisect":
				rawGit(t, executor, "reset", "--hard", approved)
				rawGit(t, executor, "bisect", "start")
			case "unrelated worktree in use by bisect":
				rawGit(t, executor, "reset", "--hard", approved)
				unrelated := filepath.Join(root, WorktreeContainer, "unrelated")
				rawGit(t, root, "worktree", "add", "--detach", unrelated, approved)
				rawGit(t, unrelated, "bisect", "start")
			case "local branch advances at deletion":
				rawGit(t, executor, "reset", "--hard", approved)
				localHead, remoteHead = advanced, ""
				host.beforeGit = func(cmd execx.Cmd) {
					if cmd.Args[0] == "update-ref" {
						rawGit(t, root, "update-ref", ref, advanced, approved)
						host.beforeGit = nil
					}
				}
			default:
				rawGit(t, executor, "reset", "--hard", approved) // commit remains available to the disposable remote
				remoteHead = advanced
				pushAdvanced := func() { rawGit(t, root, "push", "origin", advanced+":"+ref) }
				if tc.name == "remote branch already advanced" {
					pushAdvanced()
				} else {
					host.beforeGit = func(cmd execx.Cmd) {
						if cmd.Args[0] == "push" {
							pushAdvanced()
							host.beforeGit = nil
						}
					}
				}
			}
			before := stateBytes(t, root)
			registrations := rawGit(t, root, "worktree", "list", "--porcelain")
			request := fmt.Appendf(nil, `{"schema_version":1,"issue_number":1,"statement":"cleanup-issue","run_id":%q}`, st.Run.ID)
			res, err := Cleanup(t.Context(), env, request)
			if err == nil || res != nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("wanted %q refusal, got %+v %v", tc.want, res, err)
			}
			if !expectsRemoved && tc.name != "remote branch advances at push" && (len(host.deleted) != 0 || !host.loaded[review]) {
				t.Fatal("known ref drift reached cache eviction")
			}
			if !bytes.Equal(stateBytes(t, root), before) {
				t.Fatal("refusal changed merged phase")
			}
			if expectsRemoved {
				// The late ref update is refused even after legitimate earlier
				// deletions; those are not rolled back or reported as cleaned.
				for _, path := range []string{executor, review} {
					if _, err := os.Stat(path); !os.IsNotExist(err) {
						t.Fatalf("earlier deletion not observed at %s: %v", path, err)
					}
				}
			} else {
				if rawGit(t, root, "worktree", "list", "--porcelain") != registrations {
					t.Fatal("refusal changed worktree registration")
				}
				if _, err := os.Stat(review); err != nil {
					t.Fatal(err)
				}
				if tc.name != "local branch advanced without checkout" &&
					strings.TrimSpace(rawGit(t, executor, "rev-parse", "HEAD")) != localHead {
					t.Fatal("refusal changed executor HEAD")
				}
			}
			remoteRef := ""
			if remoteHead != "" {
				remoteRef = remoteHead + "\t" + ref
			}
			if strings.TrimSpace(rawGit(t, root, "rev-parse", ref)) != localHead ||
				strings.TrimSpace(rawGit(t, root, "ls-remote", "--heads", "origin", ref)) != remoteRef {
				t.Fatal("refusal changed a branch ref")
			}
		})
	}
}

func TestBoundMergedOpenCodeCleanupCanResumeAfterRemoteDeletion(t *testing.T) {
	root, _, _, host, env := openCodeCheckoutFixture(t, state.PhaseMerged)
	st := loadRun(t, root)
	issue := st.Run.Issues[0]
	// origin never had the branch, as after automatic GitHub branch deletion.
	req := fmt.Appendf(nil, `{"schema_version":1,"issue_number":1,"statement":"cleanup-issue","run_id":%q}`, st.Run.ID)
	res, err := Cleanup(t.Context(), env, req)
	if err != nil || res == nil || res.Phase != state.PhaseCleaned {
		t.Fatalf("cleanup = %+v, %v", res, err)
	}
	if len(host.deleted) != 2 || loadRun(t, root).Run.Issues[0].Phase != state.PhaseCleaned {
		t.Fatal("cleanup did not dispose both owned caches and save cleaned")
	}
	late := false
	var probes int
	for _, cmd := range host.calls {
		if cmd.Name != "opencode2" || len(cmd.Args) != 3 {
			continue
		}
		if cmd.Args[1] == "delete" {
			late = true
		}
		if late && cmd.Args[2] == "/api/experimental/job?status=running" {
			probes++
		}
	}
	if probes != 2 {
		t.Fatalf("wanted the global job probe after absent remote lookup and executor removal, got %d", probes)
	}
	for _, path := range []string{worktreeAbs(root, 1), reviewWorktreeAbs(root, 1)} {
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Fatalf("cleanup left checkout %s: %v", path, err)
		}
	}
	if rawGit(t, root, "branch", "--list", issue.Branch) != "" {
		t.Fatal("cleanup retained local branch")
	}
}

func TestBoundCleanupHoldsActivityDuringGit(t *testing.T) {
	for _, tc := range []struct {
		during, endpoint string
		unknown, absent  bool
	}{
		{during: "lookup", endpoint: "/api/shell"},
		{during: "lookup", endpoint: "/api/pty", unknown: true},
		{during: "push", endpoint: "/api/pty"},
		{during: "push", endpoint: "/api/shell", unknown: true},
		{during: "lookup", endpoint: "codemode"},
		{during: "push", endpoint: "codemode"},
		{during: "lookup", endpoint: "codemode", absent: true},
		{during: "push", endpoint: "codemode", absent: true},
		{during: "executor removal", endpoint: "/api/pty"},
	} {
		t.Run(fmt.Sprintf("%s%s/unknown=%t/absent=%t", tc.during, tc.endpoint, tc.unknown, tc.absent), func(t *testing.T) {
			root, reviewHead, approved, host, env := openCodeCheckoutFixture(t, state.PhaseMerged)
			executor, review := worktreeAbs(root, 1), reviewWorktreeAbs(root, 1)
			st := loadRun(t, root)
			ref := "refs/heads/" + st.Run.Issues[0].Branch
			rawGit(t, root, "push", "origin", ref)
			scratch := filepath.Join(review, "keep-scratch")
			if tc.absent {
				for _, checkout := range []string{executor, review} {
					rawGit(t, root, "worktree", "remove", checkout)
					delete(host.loaded, checkout)
				}
			} else if err := os.WriteFile(scratch, []byte("review notes"), 0o644); err != nil {
				t.Fatal(err)
			}
			before := stateBytes(t, root)
			registrations := rawGit(t, root, "worktree", "list", "--porcelain")
			cwd, want := review, "running shell or terminal prevents removal"
			owner := root
			const primaryOwner = "sesPrimary"
			const jobs = "/api/experimental/job?status=running"
			lateJobProbe := false
			if tc.endpoint == "codemode" {
				want = "running Code Mode job prevents removal: its working directories cannot be verified"
			}
			if tc.unknown {
				cwd, want = filepath.Join(root, "unverifiable-cwd"), "host directory identity is unavailable"
			}
			if tc.during == "push" && tc.endpoint == "/api/pty" {
				owner = t.TempDir()
				host.loaded[owner] = true
			}
			started := false
			host.beforeAPI = func(endpoint *url.URL) {
				if !tc.absent && len(host.deleted) != 2 {
					return
				}
				switch endpoint.Path {
				case "/api/debug/location", "/api/shell", "/api/pty":
					if key := endpoint.Query().Get("location[directory]"); key != "" && (key == executor || key == review) {
						t.Fatalf("scoped read of released target: %s", endpoint)
					}
				case "/api/experimental/job":
					if endpoint.RequestURI() != jobs {
						t.Fatalf("unexpected late job request: %s", endpoint)
					}
					lateJobProbe = lateJobProbe || started
				default:
					t.Fatalf("unexpected potentially loading request after both evictions: %s", endpoint)
				}
			}
			lookups := 0
			host.beforeGit = func(cmd execx.Cmd) {
				if started || !tc.absent && len(host.deleted) != 2 {
					return
				}
				switch tc.during {
				case "lookup":
					if slices.Contains(cmd.Args, "ls-remote") {
						lookups++
						// Without evictions to distinguish the two lookups, skip
						// the first (ref preflight) on a ref-only retry.
						started = !tc.absent || lookups == 2
					}
				case "push":
					started = slices.Contains(cmd.Args, "push")
				case "executor removal":
					started = slices.Contains(cmd.Args, "remove") && cmd.Args[len(cmd.Args)-1] == executor
				}
				if started {
					if tc.endpoint == "codemode" {
						host.sessions = map[string]string{primaryOwner: root}
						host.jobInventory = `{"data":[{"id":"jobLate","kind":"codemode","status":"running","sessionID":"` + primaryOwner + `"}]}`
					} else {
						// The owner stays outside the targets. Ordinary activity
						// does not load the review or appear in job inventories.
						host.ordinaryCwds = map[string]map[string]string{tc.endpoint: {owner: cwd}}
					}
				}
			}
			req := fmt.Appendf(nil, `{"schema_version":1,"issue_number":1,"statement":"cleanup-issue","run_id":%q}`, st.Run.ID)
			res, err := Cleanup(t.Context(), env, req)
			if !started || res != nil || err == nil || !strings.Contains(err.Error(), want) {
				t.Fatalf("wanted %q after Git started, got started=%t result=%+v error=%v evictions=%d persisted phase=%s",
					want, started, res, err, len(host.deleted), loadRun(t, root).Run.Issues[0].Phase)
			}
			if tc.endpoint == "codemode" && !lateJobProbe {
				t.Fatal("late hold did not use the global job inventory")
			}
			if tc.endpoint == "codemode" && (!strings.Contains(err.Error(), "retry `orch_cleanup` directly") || strings.Contains(err.Error(), "standalone")) {
				t.Fatalf("late bound hold did not recommend a bound retry: %v", err)
			}
			if tc.absent {
				for _, checkout := range []string{executor, review} {
					if _, err := os.Lstat(checkout); !os.IsNotExist(err) {
						t.Fatalf("refusal changed checkout absence: %v", err)
					}
				}
			} else {
				if content, err := os.ReadFile(scratch); err != nil || string(content) != "review notes" {
					t.Fatalf("late hold lost review scratch: %q %v", content, err)
				}
				detachedHeadAt(t, review, reviewHead)
				if tc.during == "executor removal" {
					if _, err := os.Stat(executor); !os.IsNotExist(err) {
						t.Fatalf("successful earlier executor removal was not observed: %v", err)
					}
				} else if strings.TrimSpace(rawGit(t, executor, "rev-parse", "HEAD")) != approved {
					t.Fatal("late hold changed executor checkout")
				}
			}
			if tc.during != "executor removal" && rawGit(t, root, "worktree", "list", "--porcelain") != registrations {
				t.Fatal("late hold changed worktree registrations")
			}
			remote := strings.TrimSpace(rawGit(t, root, "ls-remote", "--heads", "origin", ref))
			if tc.during == "lookup" && remote != approved+"	"+ref || tc.during != "lookup" && remote != "" {
				t.Fatalf("unexpected remote ref after %s: %q", tc.during, remote)
			}
			if !bytes.Equal(before, stateBytes(t, root)) || strings.TrimSpace(rawGit(t, root, "rev-parse", ref)) != approved {
				t.Fatal("refused cleanup changed merged phase or local ref")
			}
		})
	}
}

func TestBoundCleanupPinsRemoteAcrossReconfiguration(t *testing.T) {
	for _, scenario := range []string{"push URL changes", "origin renamed", "URL rewrite changes", "observed change before eviction"} {
		t.Run(scenario, func(t *testing.T) {
			root, _, approved, host, env := openCodeCheckoutFixture(t, state.PhaseMerged)
			st := loadRun(t, root)
			ref := "refs/heads/" + st.Run.Issues[0].Branch
			original := os.Getenv("ORCH_CLEANUP_FIXTURE_REMOTE")
			originalURL := strings.TrimSpace(rawGit(t, root, "remote", "get-url", "origin"))
			dir := t.TempDir()
			rawGit(t, dir, "init", "--bare", "other.git")
			other := filepath.Join(dir, "other.git")
			for _, destination := range []string{original, other} {
				rawGit(t, root, "push", destination, ref, approved+":refs/heads/keep")
			}
			rawGit(t, root, "tag", "-a", "unpublished", "-m", "keep local", approved)
			// The pinned command must retain inherited Git configuration, while
			// never applying an automatic tag push to a deletion.
			t.Setenv("GIT_CONFIG_COUNT", "1")
			t.Setenv("GIT_CONFIG_KEY_0", "push.followTags")
			t.Setenv("GIT_CONFIG_VALUE_0", "true")
			if scenario == "URL rewrite changes" {
				rawGit(t, root, "remote", "set-url", "origin", "orch-fixture://first")
				rawGit(t, root, "config", "url."+originalURL+".insteadOf", "orch-fixture://first")
			}
			if scenario == "push URL changes" {
				// Both old named-remote operations selected repository-specific
				// programs. The bound operations use Git's default programs.
				rawGit(t, root, "config", "remote.origin.uploadpack", "fixture-unexpected-upload-pack")
				rawGit(t, root, "config", "remote.origin.receivepack", "fixture-unexpected-receive-pack")
				for _, args := range [][]string{{"ls-remote", "--heads", "origin", ref}, {"push", "--dry-run", "origin", ":" + ref}} {
					res, err := (execx.Local{}).Run(t.Context(), execx.Cmd{Name: "git", Dir: root, Args: args})
					if err != nil || res.ExitCode == 0 || !strings.Contains(res.Stderr, "cleanup SSH fixture refused unexpected command") {
						t.Fatalf("named %s did not select the configured program: %+v %v", args[0], res, err)
					}
				}
			}
			changed := false
			if scenario == "observed change before eviction" {
				host.beforeVersion = func() {
					rawGit(t, root, "remote", "set-url", "origin", other)
					changed = true
				}
			}
			host.beforeGit = func(cmd execx.Cmd) {
				if changed || !slices.Contains(cmd.Args, "push") {
					return
				}
				changed = true
				switch scenario {
				case "origin renamed":
					rawGit(t, root, "remote", "rename", "origin", "old-origin")
					rawGit(t, root, "remote", "add", "origin", other)
				case "URL rewrite changes":
					rawGit(t, root, "config", "url."+other+".insteadOf", originalURL)
					rawGit(t, root, "config", "url."+other+".pushInsteadOf", originalURL)
				case "push URL changes":
					rawGit(t, root, "remote", "set-url", "--push", "origin", other)
				default:
					t.Fatal("known drift reached push")
				}
			}
			before := stateBytes(t, root)
			req := fmt.Appendf(nil, `{"schema_version":1,"issue_number":1,"statement":"cleanup-issue","run_id":%q}`, st.Run.ID)
			res, err := Cleanup(t.Context(), env, req)
			wantOriginal := ""
			if scenario == "observed change before eviction" {
				if !changed || err == nil || !strings.Contains(err.Error(), "remote destination changed") ||
					res != nil || len(host.deleted) != 0 || !bytes.Equal(before, stateBytes(t, root)) {
					t.Fatalf("known destination change was not refused before eviction: %+v %v", res, err)
				}
				wantOriginal = approved
			} else if !changed || err != nil || res == nil || res.Phase != state.PhaseCleaned {
				t.Fatalf("cleanup did not finish against the bound destination: changed=%t result=%+v err=%v", changed, res, err)
			}
			// Read each bare repository directly, independent of the current rewrites.
			readRef := func(dir, ref string) string {
				return strings.TrimSpace(rawGit(t, dir, "for-each-ref", "--format=%(objectname)", ref))
			}
			if readRef(original, ref) != wantOriginal {
				t.Fatal("unexpected state of the approved ref in the original remote")
			}
			if readRef(other, ref) != approved {
				t.Fatal("alternate remote lost the identically named ref at the same OID")
			}
			for _, destination := range []string{original, other} {
				if readRef(destination, "refs/heads/keep") != approved || readRef(destination, "refs/tags/unpublished") != "" {
					t.Fatal("cleanup changed an unrelated ref")
				}
			}
		})
	}
}
