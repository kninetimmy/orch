package guard

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kninetimmy/orch/internal/lockfile"
	"github.com/kninetimmy/orch/internal/manifest"
	"github.com/kninetimmy/orch/internal/paths"
	"github.com/kninetimmy/orch/internal/state"
)

// realChecker wires the production path/state/lock/head implementations
// and injects the ignore probe (real git is never run in these tests).
func realChecker(ignored func(context.Context, string, string) (bool, error)) *Checker {
	c := NewChecker(nil)
	if ignored != nil {
		c.ignored = ignored
	}
	return c
}

func mkdirAll(t *testing.T, dir string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	mkdirAll(t, filepath.Dir(path))
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// mkOrchDir creates root/.orchestrator so root reads as an orch repo.
func mkOrchDir(t *testing.T, root string) {
	t.Helper()
	mkdirAll(t, filepath.Join(root, paths.OrchestratorDir))
}

// writeWorktreeGit fabricates a linked-worktree .git pointer file plus a
// HEAD holding headContent, matching the on-disk shape row 14 reads.
func writeWorktreeGit(t *testing.T, worktreeAbs, headContent string) {
	t.Helper()
	gitMeta := filepath.Join(worktreeAbs, ".git-meta")
	writeFile(t, filepath.Join(gitMeta, "HEAD"), headContent)
	writeFile(t, filepath.Join(worktreeAbs, ".git"), "gitdir: "+gitMeta+"\n")
}

// enterDeliveryWith puts root into a Delivery run holding a single
// dispatched issue with the given worktree (repo-relative slash path)
// and branch, leaving the matching lock in place.
func enterDeliveryWith(t *testing.T, root, worktreeRel, branch string) {
	t.Helper()
	mkOrchDir(t, root)
	planned := []state.Issue{{PlanID: "a", Title: "A", Phase: state.PhasePlanned}}
	plan := state.PlanRef{Title: "t", Digest: "sha256:x", ConfigRevision: "r1"}
	if _, err := state.EnterDelivery(root, "claude", plan, planned); err != nil {
		t.Fatal(err)
	}
	st, err := state.Load(root)
	if err != nil {
		t.Fatal(err)
	}
	st.Run.Issues = []state.Issue{{
		PlanID:   "a",
		Title:    "A",
		Phase:    state.PhaseDispatched,
		Number:   3,
		Branch:   branch,
		Worktree: worktreeRel,
		Decision: &state.Decision{
			Role:      manifest.RoleImplementer,
			Executor:  manifest.Selection{Model: "m", Effort: "e"},
			Reviewer:  manifest.Selection{Model: "m", Effort: "e"},
			Rationale: "r",
		},
	}}
	if err := state.Save(root, st); err != nil {
		t.Fatal(err)
	}
}

func mustDeny(t *testing.T, v Verdict, err error, reasonHas string) {
	t.Helper()
	if err != nil {
		t.Fatalf("Check err = %v, want a Verdict deny", err)
	}
	if v.Allow {
		t.Fatalf("Allow = true, want deny")
	}
	if !strings.Contains(v.Reason, reasonHas) {
		t.Fatalf("reason = %q, want containing %q", v.Reason, reasonHas)
	}
}

func mustAllow(t *testing.T, v Verdict, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("Check err = %v, want allow", err)
	}
	if !v.Allow {
		t.Fatalf("Allow = false, reason %q", v.Reason)
	}
}

// TestOutermostRootRegression is the key correctness test: a target
// inside a worktree that itself carries a checked-out .orchestrator/ must
// resolve to the PRIMARY root (outermost), whose Delivery state matches
// the worktree — not to the worktree (innermost), whose missing state
// would read as Assist and wrongly deny.
func TestOutermostRootRegression(t *testing.T) {
	root := t.TempDir()
	worktreeRel := filepath.ToSlash(filepath.Join(paths.OrchestratorDir, "worktrees", "issue-3"))
	enterDeliveryWith(t, root, worktreeRel, "feature-3")

	worktreeAbs := filepath.Join(root, filepath.FromSlash(worktreeRel))
	// The worktree carries a committed .orchestrator/ of its own.
	mkdirAll(t, filepath.Join(worktreeAbs, paths.OrchestratorDir))
	writeWorktreeGit(t, worktreeAbs, "ref: refs/heads/feature-3\n")

	target := filepath.Join(worktreeAbs, "src", "x.go")
	v, err := realChecker(nil).Check(context.Background(), Request{Paths: []string{target}})
	mustAllow(t, v, err)
}

func TestDeliveryHeadVariants(t *testing.T) {
	cases := map[string]struct {
		head      string
		writeHead bool
		allow     bool
	}{
		"match":    {head: "ref: refs/heads/feature-3\n", writeHead: true, allow: true},
		"mismatch": {head: "ref: refs/heads/other\n", writeHead: true},
		"detached": {head: "0123456789abcdef0123456789abcdef01234567\n", writeHead: true},
		"missing":  {writeHead: false},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			root := t.TempDir()
			enterDeliveryWith(t, root, "wt", "feature-3")
			worktreeAbs := filepath.Join(root, "wt")
			if tc.writeHead {
				writeWorktreeGit(t, worktreeAbs, tc.head)
			} else {
				// A .git pointer to a gitdir with no HEAD file.
				writeFile(t, filepath.Join(worktreeAbs, ".git"), "gitdir: "+filepath.Join(worktreeAbs, ".git-meta")+"\n")
				mkdirAll(t, filepath.Join(worktreeAbs, ".git-meta"))
			}
			target := filepath.Join(worktreeAbs, "src", "x.go")
			v, err := realChecker(nil).Check(context.Background(), Request{Paths: []string{target}})
			if tc.allow {
				mustAllow(t, v, err)
				return
			}
			mustDeny(t, v, err, "not on its registered branch")
		})
	}
}

// TestDeliveryNotYetExisting confirms a target that does not exist yet is
// still matched to its worktree (Canonical's deepest-ancestor behavior).
func TestDeliveryNotYetExisting(t *testing.T) {
	root := t.TempDir()
	enterDeliveryWith(t, root, "wt", "feature-3")
	worktreeAbs := filepath.Join(root, "wt")
	writeWorktreeGit(t, worktreeAbs, "ref: refs/heads/feature-3\n")

	target := filepath.Join(worktreeAbs, "brand", "new", "file.go")
	v, err := realChecker(nil).Check(context.Background(), Request{Paths: []string{target}})
	mustAllow(t, v, err)
}

func TestDeliveryOutsideWorktree(t *testing.T) {
	root := t.TempDir()
	enterDeliveryWith(t, root, "wt", "feature-3")
	target := filepath.Join(root, "src", "x.go")
	v, err := realChecker(nil).Check(context.Background(), Request{Paths: []string{target}})
	mustDeny(t, v, err, "outside every registered worktree")
}

func TestGitSegmentDenies(t *testing.T) {
	root := t.TempDir()
	mkOrchDir(t, root)
	target := filepath.Join(root, ".git", "config")
	v, err := realChecker(nil).Check(context.Background(), Request{Paths: []string{target}})
	mustDeny(t, v, err, "git internals")
}

func TestAssistMissingStateSelfPromotion(t *testing.T) {
	root := t.TempDir()
	mkOrchDir(t, root) // no state.json → assist
	target := filepath.Join(root, paths.OrchestratorDir, "state.json")
	v, err := realChecker(nil).Check(context.Background(), Request{Paths: []string{target}})
	mustDeny(t, v, err, "orchestrator internals")
}

func TestCorruptStateErrors(t *testing.T) {
	root := t.TempDir()
	mkOrchDir(t, root)
	writeFile(t, filepath.Join(root, filepath.FromSlash(state.Path)), "{ not json")
	target := filepath.Join(root, "src", "x.go")
	_, err := realChecker(nil).Check(context.Background(), Request{Paths: []string{target}})
	if err == nil {
		t.Fatal("corrupt state did not produce an operational error")
	}
}

func TestVersionMismatchStateErrors(t *testing.T) {
	root := t.TempDir()
	mkOrchDir(t, root)
	writeFile(t, filepath.Join(root, filepath.FromSlash(state.Path)), `{"schema_version":99,"mode":"assist"}`)
	target := filepath.Join(root, "src", "x.go")
	_, err := realChecker(nil).Check(context.Background(), Request{Paths: []string{target}})
	if err == nil {
		t.Fatal("version-mismatch state did not produce an operational error")
	}
}

func TestOrphanedLockErrors(t *testing.T) {
	root := t.TempDir()
	mkOrchDir(t, root) // assist (no state.json)
	if err := lockfile.Acquire(root, lockfile.Owner{RunID: "run-x", Host: "claude"}); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(root, "src", "x.go")
	_, err := realChecker(nil).Check(context.Background(), Request{Paths: []string{target}})
	if err == nil || !strings.Contains(err.Error(), "orch abort") {
		t.Fatalf("err = %v, want an orphaned-lock error", err)
	}
}

// TestAssistExistingFileTarget pins the root walk to the target's parent
// directory: probing <existing-file>/.orchestrator returns ENOTDIR on
// Unix (not fs.ErrNotExist), so walking from the file itself would deny
// every edit of an existing file as "cannot verify repository root".
func TestAssistExistingFileTarget(t *testing.T) {
	root := t.TempDir()
	mkOrchDir(t, root) // assist
	target := filepath.Join(root, "src", "x.go")
	writeFile(t, target, "package src\n")
	probe := func(context.Context, string, string) (bool, error) { return false, nil }
	v, err := realChecker(probe).Check(context.Background(), Request{Paths: []string{target}})
	mustDeny(t, v, err, "read-only for repository files")
}

func TestOutsideRepoAllows(t *testing.T) {
	outside := t.TempDir()
	target := filepath.Join(outside, "x.go")
	if _, err := paths.FindOutermostRoot(target); err == nil {
		t.Skip("temp dir has an .orchestrator ancestor; outside-repo case not exercisable here")
	}
	v, err := realChecker(nil).Check(context.Background(), Request{Paths: []string{target}})
	mustAllow(t, v, err)
}

func TestAssistIgnoreProbe(t *testing.T) {
	cases := map[string]struct {
		probe func(context.Context, string, string) (bool, error)
		allow bool
	}{
		"ignored allows": {
			probe: func(context.Context, string, string) (bool, error) { return true, nil },
			allow: true,
		},
		"tracked denies": {
			probe: func(context.Context, string, string) (bool, error) { return false, nil },
		},
		"probe error denies": {
			probe: func(context.Context, string, string) (bool, error) { return false, errors.New("git missing") },
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			root := t.TempDir()
			mkOrchDir(t, root) // assist
			target := filepath.Join(root, "src", "x.go")
			v, err := realChecker(tc.probe).Check(context.Background(), Request{Paths: []string{target}})
			if tc.allow {
				mustAllow(t, v, err)
				return
			}
			if err != nil {
				t.Fatalf("Check err = %v, want a Verdict deny", err)
			}
			if v.Allow {
				t.Fatal("Allow = true, want deny")
			}
		})
	}
}
