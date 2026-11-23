package run

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kninetimmy/orch/internal/execx"
	"github.com/kninetimmy/orch/internal/state"
)

func TestBoundMergedOpenCodeCleanupPreservesDriftedRefs(t *testing.T) {
	for _, tc := range []struct{ name, want string }{
		{"executor already advanced", "executor HEAD differs from the approved PR head"},
		{"local branch advanced without checkout", "local branch differs from the approved PR head"},
		{"remote branch already advanced", "remote branch differs from the approved PR head"},
		{"remote branch advances at push", "stale info"},
		{"remote read and push disagree", "remote read and deletion destinations could not be matched"},
		{"executor branch in use by bisect", "rebase, am or bisect state is present or unverifiable"},
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
