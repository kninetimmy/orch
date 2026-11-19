package run

import (
	"bytes"
	"net/url"
	"os"
	"slices"
	"strings"
	"syscall"
	"testing"

	"github.com/kninetimmy/orch/internal/state"
)

func TestOpenCodeFinalWindowsDeleteAccessBeforeGit(t *testing.T) {
	for _, operation := range []string{"cleanup executor", "cleanup review", "replace review"} {
		t.Run(operation, func(t *testing.T) {
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
			before := stateBytes(t, root)
			registrations := rawGit(t, root, "worktree", "list", "--porcelain")
			var held *os.File
			lockedOnce := false
			t.Cleanup(func() {
				if held != nil {
					if err := held.Close(); err != nil {
						t.Error(err)
					}
				}
			})
			host.beforeAPI = func(endpoint *url.URL) {
				if lockedOnce || !slices.Contains(host.deleted, target) || endpoint.Path != "/api/debug/location" || endpoint.RawQuery != "" {
					return
				}
				// Introduce a real no-delete-share directory handle only at the
				// final host observation, after the scoped evictions succeeded.
				name, err := syscall.UTF16PtrFromString(target)
				if err != nil {
					t.Fatal(err)
				}
				handle, err := syscall.CreateFile(name, syscall.GENERIC_READ,
					syscall.FILE_SHARE_READ|syscall.FILE_SHARE_WRITE, nil, syscall.OPEN_EXISTING, syscall.FILE_FLAG_BACKUP_SEMANTICS, 0)
				if err != nil {
					t.Fatal(err)
				}
				held = os.NewFile(uintptr(handle), target)
				lockedOnce = true
			}
			var err error
			if operation == "replace review" {
				_, err = ReviewWorktree(t.Context(), env, []byte(reviewWorktreeJSON(1, second)))
			} else {
				_, err = Cleanup(t.Context(), env, []byte(`{"schema_version":1,"issue_number":1,"statement":"cleanup-issue"}`))
			}
			if held == nil || err == nil || !strings.Contains(err.Error(), "Windows directory delete access unavailable") || strings.Contains(err.Error(), target) {
				t.Fatalf("missing held-directory refusal or raw data exposed: %v", err)
			}
			if !bytes.Equal(before, stateBytes(t, root)) || registrations != rawGit(t, root, "worktree", "list", "--porcelain") {
				t.Fatal("Windows refusal changed phase or either registration")
			}
			detachedHeadAt(t, review, first)
			if !strings.Contains(rawGit(t, root, "ls-remote", "--heads", "origin", branch), branch) ||
				!strings.Contains(rawGit(t, root, "branch", "--list", branch), branch) {
				t.Fatal("Windows refusal deleted a branch")
			}
			for _, cmd := range host.calls {
				if cmd.Name == "git" && (slices.Contains(cmd.Args, "remove") || slices.Contains(cmd.Args, "push") ||
					slices.Contains(cmd.Args, "prune") || slices.Contains(cmd.Args, "-D")) {
					t.Fatalf("Windows access failure reached Git mutation: %+v", cmd)
				}
			}
			if err := held.Close(); err != nil {
				t.Fatal(err)
			}
			held = nil
			// The fixture releases only its own handle, without moving sessions,
			// killing a process or changing Git; normal retry must then succeed.
			if operation == "replace review" {
				_, err = ReviewWorktree(t.Context(), env, []byte(reviewWorktreeJSON(1, second)))
			} else {
				_, err = Cleanup(t.Context(), env, []byte(`{"schema_version":1,"issue_number":1,"statement":"cleanup-issue"}`))
			}
			if err != nil {
				t.Fatalf("retry after fixture handle close: %v", err)
			}
			t.Log("final Windows open-handle refusal precedes all Git mutation; retry after owned handle close succeeds")
		})
	}
}
