package run

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/kninetimmy/orch/internal/execx"
	"github.com/kninetimmy/orch/internal/state"
)

func TestBoundMergedOpenCodeCleanupRequiresRecordedRepository(t *testing.T) {
	const wrong = "git@github.example.invalid:fixture/other.git"
	for _, tc := range []struct{ name, want string }{
		{"already retargeted", "does not identify the recorded PR repository"},
		{"no origin", "read and deletion destinations could not be matched"},
		{"ambiguous reads", "read and deletion destinations could not be matched"},
		{"ambiguous pushes", "read and deletion destinations could not be matched"},
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
			switch tc.name {
			case "already retargeted":
				rawGit(t, root, "remote", "set-url", "origin", wrong)
			case "no origin":
				rawGit(t, root, "remote", "remove", "origin")
			case "ambiguous reads":
				rawGit(t, root, "remote", "set-url", "--add", "origin", wrong)
				rawGit(t, root, "remote", "set-url", "--push", "origin", originalURL)
			case "ambiguous pushes":
				rawGit(t, root, "remote", "set-url", "--push", "origin", originalURL)
				rawGit(t, root, "remote", "set-url", "--push", "--add", "origin", wrong)
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
			for _, call := range host.calls {
				if call.Name == "git" && call.Args[0] == "ls-remote" {
					t.Fatal("unverifiable repository received a Git transfer")
				}
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
		unknown          bool
	}{
		{"lookup", "/api/shell", false},
		{"lookup", "/api/pty", true},
		{"push", "/api/pty", false},
		{"push", "/api/shell", true},
		{"executor removal", "/api/pty", false},
	} {
		t.Run(fmt.Sprintf("%s%s/unknown=%t", tc.during, tc.endpoint, tc.unknown), func(t *testing.T) {
			root, reviewHead, approved, host, env := openCodeCheckoutFixture(t, state.PhaseMerged)
			executor, review := worktreeAbs(root, 1), reviewWorktreeAbs(root, 1)
			st := loadRun(t, root)
			ref := "refs/heads/" + st.Run.Issues[0].Branch
			rawGit(t, root, "push", "origin", ref)
			scratch := filepath.Join(review, "keep-scratch")
			if err := os.WriteFile(scratch, []byte("review notes"), 0o644); err != nil {
				t.Fatal(err)
			}
			before := stateBytes(t, root)
			cwd, want := review, "running shell or terminal prevents removal"
			owner := root
			if tc.unknown {
				cwd, want = filepath.Join(root, "unverifiable-cwd"), "host directory identity is unavailable"
			}
			if tc.during == "push" && !tc.unknown {
				owner = t.TempDir()
				host.loaded[owner] = true
			}
			started := false
			host.beforeGit = func(cmd execx.Cmd) {
				if len(host.deleted) != 2 || started {
					return
				}
				switch tc.during {
				case "lookup":
					started = slices.Contains(cmd.Args, "ls-remote")
				case "push":
					started = slices.Contains(cmd.Args, "push")
				case "executor removal":
					started = slices.Contains(cmd.Args, "remove") && cmd.Args[len(cmd.Args)-1] == executor
				}
				if started {
					// The owner stays outside the targets. The activity does not
					// load the review cache or appear in session/job inventories.
					host.ordinaryCwds = map[string]map[string]string{tc.endpoint: {owner: cwd}}
				}
			}
			req := fmt.Appendf(nil, `{"schema_version":1,"issue_number":1,"statement":"cleanup-issue","run_id":%q}`, st.Run.ID)
			res, err := Cleanup(t.Context(), env, req)
			if !started || res != nil || err == nil || !strings.Contains(err.Error(), want) {
				t.Fatalf("wanted %q after Git started, got started=%t result=%+v error=%v", want, started, res, err)
			}
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
				// The exact ref cannot be deleted via the old named-remote push:
				// the SSH fixture refuses the remote-specific receive program.
				rawGit(t, root, "config", "remote.origin.receivepack", "fixture-unexpected-receive-pack")
				res, err := (execx.Local{}).Run(context.Background(), execx.Cmd{Name: "git", Dir: root,
					Args: []string{"push", "--dry-run", "origin", "--delete", st.Run.Issues[0].Branch}})
				if err != nil || res.ExitCode == 0 || !strings.Contains(res.Stderr, "cleanup SSH fixture refused unexpected command") {
					t.Fatalf("named push did not select the configured receive program: %+v %v", res, err)
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
