package run

import (
	"bytes"
	"fmt"
	"net/url"
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/kninetimmy/orch/internal/state"
)

func TestWindowsOpenCodeCheckoutReleasePastFirstThousand(t *testing.T) {
	for _, replace := range []bool{false, true} {
		for _, activity := range []string{"idle", "owner", "moved owner cwd"} {
			t.Run(fmt.Sprintf("replace=%t/%s", replace, activity), func(t *testing.T) {
				phase := state.PhaseAbandoned
				if replace {
					phase = state.PhaseInReview
				}
				root, first, second, host, env := openCodeCheckoutFixture(t, phase)
				executor, review := worktreeAbs(root, 1), reviewWorktreeAbs(root, 1)
				branch := loadRun(t, root).Run.Issues[0].Branch
				rawGit(t, root, "push", "origin", branch)
				before := stateBytes(t, root)
				registrations := rawGit(t, root, "worktree", "list", "--porcelain")
				local := rawGit(t, root, "rev-parse", "refs/heads/"+branch)
				remote := rawGit(t, root, "ls-remote", "--heads", "origin", branch)
				host.sessions = make(map[string]string)
				for i := 1; i <= 1001; i++ {
					host.sessions[fmt.Sprintf("ses%04d", i)] = root
				}
				const last = "ses1001"
				switch activity {
				case "owner":
					host.sessions[last] = review
					host.terminalCwds = map[string]string{last: root}
				case "moved owner cwd":
					// The saved owner is now in primary and has no active drain.
					host.terminalCwds = map[string]string{last: review}
				}

				var releaseErr error
				if replace {
					res, err := ReviewWorktree(t.Context(), env, []byte(reviewWorktreeJSON(1, second)))
					releaseErr = err
					if activity == "idle" {
						if err != nil || res.HeadOID != second || !slices.Equal(host.deleted, []string{review}) {
							t.Fatalf("replacement=%+v %v, evictions=%v", res, err, host.deleted)
						}
						detachedHeadAt(t, review, second)
						if !bytes.Equal(before, stateBytes(t, root)) {
							t.Fatal("review replacement wrote run state")
						}
					} else if res != nil {
						t.Fatalf("refusal returned a replacement: %+v", res)
					}
				} else {
					res, err := Cleanup(t.Context(), env, []byte(`{"schema_version":1,"issue_number":1,"statement":"cleanup-issue"}`))
					releaseErr = err
					if activity == "idle" {
						if err != nil || res.Phase != state.PhaseCleaned || !slices.Equal(host.deleted, []string{executor, review}) {
							t.Fatalf("cleanup=%+v %v, evictions=%v", res, err, host.deleted)
						}
						for _, dir := range []string{executor, review} {
							if _, err := os.Lstat(dir); !os.IsNotExist(err) {
								t.Fatalf("cleanup retained %s: %v", dir, err)
							}
						}
					} else if res != nil {
						t.Fatalf("refusal returned a cleanup: %+v", res)
					}
				}

				reads := make(map[string]int)
				lateOrdinary := make(map[string]bool)
				primaryQuery := url.Values{"location[directory]": {root}}.Encode()
				shell, pty := "/api/shell?"+primaryQuery, "/api/pty?"+primaryQuery
				evicting := false
				for _, cmd := range host.calls {
					if cmd.Name != "opencode2" || len(cmd.Args) != 3 {
						continue
					}
					endpoint := cmd.Args[2]
					if cmd.Args[1] == "delete" {
						evicting = true
					} else if evicting && endpoint != "/api/debug/location" {
						// Cleanup alone may recheck ordinary processes, and only
						// under the already-loaded primary key in this fixture.
						if replace || cmd.Args[1] != "get" || (endpoint != shell && endpoint != pty) {
							t.Fatalf("loading request after first eviction: %s", endpoint)
						}
						lateOrdinary[endpoint] = true
					}
					if strings.HasSuffix(endpoint, "/terminal") {
						reads[endpoint]++
					}
				}
				if !replace && activity == "idle" && (!lateOrdinary[shell] || !lateOrdinary[pty]) {
					t.Fatal("cleanup did not recheck the loaded primary shell and PTY inventories")
				}
				if len(reads) != 1001 {
					t.Fatalf("read %d terminal inventories, want 1001", len(reads))
				}
				for endpoint, count := range reads {
					if count != 1 {
						t.Fatalf("read %s %d times", endpoint, count)
					}
				}
				if activity != "idle" {
					if releaseErr == nil || !strings.Contains(releaseErr.Error(), "running persistent terminal") || len(host.deleted) != 0 {
						t.Fatalf("late activity: err=%v, evictions=%v", releaseErr, host.deleted)
					}
					if !bytes.Equal(before, stateBytes(t, root)) || registrations != rawGit(t, root, "worktree", "list", "--porcelain") ||
						local != rawGit(t, root, "rev-parse", "refs/heads/"+branch) ||
						remote != rawGit(t, root, "ls-remote", "--heads", "origin", branch) {
						t.Fatal("refusal changed run state, registration, or a branch")
					}
					detachedHeadAt(t, review, first)
				}
			})
		}
	}
}
