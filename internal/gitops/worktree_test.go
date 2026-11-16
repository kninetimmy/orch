package gitops

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kninetimmy/orch/internal/execx/execxtest"
)

var listArgs = []string{"worktree", "list", "--porcelain"}

// porcelain joins `worktree list --porcelain` lines; "" separates
// stanzas.
func porcelain(lines ...string) string {
	return strings.Join(lines, "\n") + "\n"
}

func TestListWorktrees(t *testing.T) {
	primary := tempRoot(t)
	feature := tempRoot(t)
	detached := tempRoot(t)
	g, script := openScripted(t, primary, execxtest.Call{
		Name: "git", Args: listArgs,
		Stdout: porcelain(
			"worktree "+primary,
			"HEAD 1111111111111111111111111111111111111111",
			"branch refs/heads/main",
			"",
			"worktree "+feature,
			"HEAD 2222222222222222222222222222222222222222",
			"branch refs/heads/orch/issue-4",
			"",
			"worktree "+detached,
			"HEAD 3333333333333333333333333333333333333333",
			"detached",
		),
	})
	got, err := g.ListWorktrees(context.Background())
	script.AssertExhausted()
	if err != nil {
		t.Fatalf("ListWorktrees: %v", err)
	}
	want := []Worktree{
		{Path: primary, Branch: "main", Head: "1111111111111111111111111111111111111111", Primary: true},
		{Path: feature, Branch: "orch/issue-4", Head: "2222222222222222222222222222222222222222"},
		{Path: detached, Head: "3333333333333333333333333333333333333333", Detached: true},
	}
	if len(got) != len(want) {
		t.Fatalf("got %d worktrees, want %d: %+v", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("worktree %d = %+v, want %+v", i, got[i], want[i])
		}
	}
}

func TestListWorktreesRejectsMalformedOutput(t *testing.T) {
	g, script := openScripted(t, tempRoot(t), execxtest.Call{
		Name: "git", Args: listArgs, Stdout: "HEAD before any worktree line\n",
	})
	_, err := g.ListWorktrees(context.Background())
	script.AssertExhausted()
	if err == nil || !strings.Contains(err.Error(), "unexpected line") {
		t.Fatalf("err = %v, want parse failure", err)
	}
}

func TestAddWorktree(t *testing.T) {
	root := tempRoot(t)
	path := canon(t, filepath.Join(t.TempDir(), "issue-4"))
	const head = "4444444444444444444444444444444444444444"
	g, script := openScripted(t, root,
		execxtest.Call{Name: "git", Args: []string{"rev-parse", "--verify", "--quiet", "refs/heads/orch/issue-4"}, Dir: root, Exit: 1},
		execxtest.Call{Name: "git", Args: []string{"worktree", "add", "-b", "orch/issue-4", path, "main"}, Dir: root},
		execxtest.Call{Name: "git", Args: []string{"rev-parse", "--verify", "orch/issue-4^{commit}"}, Stdout: head + "\n"},
	)
	wt, err := g.AddWorktree(context.Background(), path, "orch/issue-4", "main")
	script.AssertExhausted()
	if err != nil {
		t.Fatalf("AddWorktree: %v", err)
	}
	want := Worktree{Path: path, Branch: "orch/issue-4", Head: head}
	if *wt != want {
		t.Errorf("worktree = %+v, want %+v", *wt, want)
	}
}

func TestAddWorktreeBranchExists(t *testing.T) {
	path := filepath.Join(t.TempDir(), "issue-4")
	g, script := openScripted(t, tempRoot(t), execxtest.Call{
		// Exit 0: the branch is already there; no worktree add may follow.
		Name: "git", Args: []string{"rev-parse", "--verify", "--quiet", "refs/heads/orch/issue-4"},
	})
	_, err := g.AddWorktree(context.Background(), path, "orch/issue-4", "main")
	script.AssertExhausted()
	if !errors.Is(err, ErrBranchExists) {
		t.Fatalf("err = %v, want ErrBranchExists", err)
	}
}

func TestAddWorktreeInsidePrimaryNotIgnoredFailsClosed(t *testing.T) {
	root := tempRoot(t)
	path := filepath.Join(root, "wt")
	rel, err := filepath.Rel(root, path)
	if err != nil {
		t.Fatal(err)
	}
	g, script := openScripted(t, root, execxtest.Call{
		Name: "git", Args: []string{"check-ignore", "-q", "--", filepath.ToSlash(rel) + "/" + requireIgnoredProbes[0]}, Dir: root, Exit: 1,
	})
	_, err = g.AddWorktree(context.Background(), path, "orch/issue-4", "main")
	script.AssertExhausted()
	if !errors.Is(err, ErrNotIgnored) {
		t.Fatalf("err = %v, want ErrNotIgnored", err)
	}
	if !strings.Contains(err.Error(), "inside the primary checkout and not git-ignored") {
		t.Fatalf("err = %v, want isolation refusal", err)
	}
}

func TestAddWorktreeInsidePrimaryIgnoredSucceeds(t *testing.T) {
	root := tempRoot(t)
	path := canon(t, filepath.Join(root, ".orchestrator", "worktrees", "issue-4"))
	rel, err := filepath.Rel(root, path)
	if err != nil {
		t.Fatal(err)
	}
	const head = "5555555555555555555555555555555555555555"
	g, script := openScripted(t, root,
		execxtest.Call{Name: "git", Args: []string{"check-ignore", "-q", "--", filepath.ToSlash(rel) + "/" + requireIgnoredProbes[0]}, Dir: root},
		execxtest.Call{Name: "git", Args: []string{"check-ignore", "-q", "--", filepath.ToSlash(rel) + "/" + requireIgnoredProbes[1]}, Dir: root},
		execxtest.Call{Name: "git", Args: []string{"rev-parse", "--verify", "--quiet", "refs/heads/orch/issue-4"}, Dir: root, Exit: 1},
		execxtest.Call{Name: "git", Args: []string{"worktree", "add", "-b", "orch/issue-4", path, "main"}, Dir: root},
		execxtest.Call{Name: "git", Args: []string{"rev-parse", "--verify", "orch/issue-4^{commit}"}, Stdout: head + "\n"},
	)
	wt, err := g.AddWorktree(context.Background(), path, "orch/issue-4", "main")
	script.AssertExhausted()
	if err != nil {
		t.Fatalf("AddWorktree: %v", err)
	}
	want := Worktree{Path: path, Branch: "orch/issue-4", Head: head}
	if *wt != want {
		t.Errorf("worktree = %+v, want %+v", *wt, want)
	}
}

func TestAddWorktreeExistingPathFailsClosed(t *testing.T) {
	g, script := openScripted(t, tempRoot(t))
	existing := t.TempDir()
	_, err := g.AddWorktree(context.Background(), existing, "orch/issue-4", "main")
	script.AssertExhausted()
	if err == nil || !strings.Contains(err.Error(), "already exists") {
		t.Fatalf("err = %v, want clobber refusal", err)
	}
}

func TestAddDetachedWorktree(t *testing.T) {
	root := tempRoot(t)
	path := canon(t, filepath.Join(root, ".orchestrator", "worktrees", "review-issue-4"))
	rel, err := filepath.Rel(root, path)
	if err != nil {
		t.Fatal(err)
	}
	const head = "6666666666666666666666666666666666666666"
	g, script := openScripted(t, root,
		execxtest.Call{Name: "git", Args: []string{"check-ignore", "-q", "--", filepath.ToSlash(rel) + "/" + requireIgnoredProbes[0]}, Dir: root},
		execxtest.Call{Name: "git", Args: []string{"check-ignore", "-q", "--", filepath.ToSlash(rel) + "/" + requireIgnoredProbes[1]}, Dir: root},
		execxtest.Call{Name: "git", Args: []string{"worktree", "add", "--detach", path, "6666666"}, Dir: root},
		// The resolved HEAD is read back inside the new checkout, so an
		// abbreviated commit-ish comes back in full.
		execxtest.Call{Name: "git", Args: []string{"rev-parse", "--verify", "HEAD^{commit}"}, Dir: path, Stdout: head + "\n"},
	)
	wt, err := g.AddDetachedWorktree(context.Background(), path, "6666666")
	script.AssertExhausted()
	if err != nil {
		t.Fatalf("AddDetachedWorktree: %v", err)
	}
	want := Worktree{Path: path, Head: head, Detached: true}
	if *wt != want {
		t.Errorf("worktree = %+v, want %+v", *wt, want)
	}
}

func TestAddDetachedWorktreeExistingPathFailsClosed(t *testing.T) {
	g, script := openScripted(t, tempRoot(t))
	existing := t.TempDir()
	_, err := g.AddDetachedWorktree(context.Background(), existing, "6666666666666666666666666666666666666666")
	script.AssertExhausted() // fail closed before any git call
	if err == nil || !strings.Contains(err.Error(), "already exists") {
		t.Fatalf("err = %v, want clobber refusal", err)
	}
}

func TestAddDetachedWorktreeInsidePrimaryNotIgnoredFailsClosed(t *testing.T) {
	root := tempRoot(t)
	path := filepath.Join(root, "review")
	rel, err := filepath.Rel(root, path)
	if err != nil {
		t.Fatal(err)
	}
	g, script := openScripted(t, root, execxtest.Call{
		Name: "git", Args: []string{"check-ignore", "-q", "--", filepath.ToSlash(rel) + "/" + requireIgnoredProbes[0]}, Dir: root, Exit: 1,
	})
	_, err = g.AddDetachedWorktree(context.Background(), path, "6666666666666666666666666666666666666666")
	script.AssertExhausted() // no `worktree add` followed
	if !errors.Is(err, ErrNotIgnored) {
		t.Fatalf("err = %v, want ErrNotIgnored", err)
	}
}

func TestRemoveDetachedWorktree(t *testing.T) {
	root := tempRoot(t)
	// The scripted Git removal has no filesystem effects; model its
	// successful postcondition with an absent path.
	wt := canon(t, filepath.Join(t.TempDir(), "removed"))
	g, script := openScripted(t, root,
		execxtest.Call{Name: "git", Args: listArgs, Stdout: porcelain(
			"worktree "+root, "HEAD 1111", "branch refs/heads/main", "",
			"worktree "+wt, "HEAD 2222", "detached",
		)},
		// No cleanliness check: the checkout is disposable, and a reviewer
		// legitimately leaves test output in it.
		execxtest.Call{Name: "git", Args: []string{"worktree", "remove", "--force", wt}, Dir: root},
		execxtest.Call{Name: "git", Args: []string{"worktree", "prune"}, Dir: root},
	)
	if err := g.RemoveDetachedWorktree(context.Background(), wt); err != nil {
		t.Fatalf("RemoveDetachedWorktree: %v", err)
	}
	script.AssertExhausted()
}

func TestRemoveDetachedWorktreeUnknownPath(t *testing.T) {
	root := tempRoot(t)
	stray := canon(t, filepath.Join(t.TempDir(), "absent"))
	g, script := openScripted(t, root, execxtest.Call{
		Name: "git", Args: listArgs,
		Stdout: porcelain("worktree "+root, "HEAD 1111", "branch refs/heads/main"),
	})
	err := g.RemoveDetachedWorktree(context.Background(), stray)
	script.AssertExhausted() // no forced removal of a directory git does not know
	if !errors.Is(err, ErrUnknownWorktree) {
		t.Fatalf("err = %v, want ErrUnknownWorktree", err)
	}
}

// TestRemoveDetachedWorktreeBranchRefused proves the gate that stands in
// for RemoveWorktree's Confirmation: a worktree holding a branch — where
// committed work lives — never reaches the forced removal.
func TestRemoveDetachedWorktreeBranchRefused(t *testing.T) {
	root := tempRoot(t)
	wt := tempRoot(t)
	g, script := openScripted(t, root, execxtest.Call{
		Name: "git", Args: listArgs, Stdout: porcelain(
			"worktree "+root, "HEAD 1111", "branch refs/heads/main", "",
			"worktree "+wt, "HEAD 2222", "branch refs/heads/orch/issue-4",
		),
	})
	err := g.RemoveDetachedWorktree(context.Background(), wt)
	script.AssertExhausted()
	if err == nil || !strings.Contains(err.Error(), "orch/issue-4") {
		t.Fatalf("err = %v, want a refusal naming the branch", err)
	}
}

// TestRemoveDetachedWorktreePrimaryRefused pins the primary check ahead
// of the detached check: a primary checkout left on a detached HEAD (the
// shape a mis-targeted checkout leaves behind) is still refused.
func TestRemoveDetachedWorktreePrimaryRefused(t *testing.T) {
	root := tempRoot(t)
	g, script := openScripted(t, root, execxtest.Call{
		Name: "git", Args: listArgs,
		Stdout: porcelain("worktree "+root, "HEAD 1111", "detached"),
	})
	err := g.RemoveDetachedWorktree(context.Background(), root)
	script.AssertExhausted()
	if err == nil || !strings.Contains(err.Error(), "primary checkout") {
		t.Fatalf("err = %v, want primary-checkout refusal", err)
	}
}

func TestRemoveWorktreeUnconfirmed(t *testing.T) {
	g, script := openScripted(t, tempRoot(t))
	err := g.RemoveWorktree(context.Background(), t.TempDir(), Confirmation{})
	script.AssertExhausted() // fail closed before any git call
	if !errors.Is(err, ErrNotConfirmed) {
		t.Fatalf("err = %v, want ErrNotConfirmed", err)
	}
}

func TestRemoveWorktreeUnknownPath(t *testing.T) {
	root := tempRoot(t)
	stray := canon(t, filepath.Join(t.TempDir(), "absent"))
	g, script := openScripted(t, root, execxtest.Call{
		Name: "git", Args: listArgs,
		Stdout: porcelain("worktree "+root, "HEAD 1111", "branch refs/heads/main"),
	})
	err := g.RemoveWorktree(context.Background(), stray, ExplicitConfirmation())
	script.AssertExhausted()
	if !errors.Is(err, ErrUnknownWorktree) {
		t.Fatalf("err = %v, want ErrUnknownWorktree", err)
	}
}

func TestRemoveWorktreePrimaryRefused(t *testing.T) {
	root := tempRoot(t)
	g, script := openScripted(t, root, execxtest.Call{
		Name: "git", Args: listArgs,
		Stdout: porcelain("worktree "+root, "HEAD 1111", "branch refs/heads/main"),
	})
	err := g.RemoveWorktree(context.Background(), root, ExplicitConfirmation())
	script.AssertExhausted()
	if err == nil || !strings.Contains(err.Error(), "primary checkout") {
		t.Fatalf("err = %v, want primary-checkout refusal", err)
	}
}

func TestRemoveWorktreeDirtyPreserved(t *testing.T) {
	root := tempRoot(t)
	wt := tempRoot(t)
	g, script := openScripted(t, root,
		execxtest.Call{Name: "git", Args: listArgs, Stdout: porcelain(
			"worktree "+root, "HEAD 1111", "branch refs/heads/main", "",
			"worktree "+wt, "HEAD 2222", "branch refs/heads/orch/issue-4",
		)},
		execxtest.Call{Name: "git", Args: statusArgs, Dir: wt, Stdout: "?? partial-work.go\n"},
	)
	err := g.RemoveWorktree(context.Background(), wt, ExplicitConfirmation())
	script.AssertExhausted() // no `worktree remove` followed
	if !errors.Is(err, ErrNotClean) {
		t.Fatalf("err = %v, want ErrNotClean", err)
	}
}

func TestRemoveWorktree(t *testing.T) {
	root := tempRoot(t)
	wt := canon(t, filepath.Join(t.TempDir(), "removed"))
	g, script := openScripted(t, root,
		execxtest.Call{Name: "git", Args: listArgs, Stdout: porcelain(
			"worktree "+root, "HEAD 1111", "branch refs/heads/main", "",
			"worktree "+wt, "HEAD 2222", "branch refs/heads/orch/issue-4",
		)},
		execxtest.Call{Name: "git", Args: statusArgs, Dir: wt},
		execxtest.Call{Name: "git", Args: []string{"worktree", "remove", wt}, Dir: root},
		execxtest.Call{Name: "git", Args: []string{"worktree", "prune"}, Dir: root},
	)
	if err := g.RemoveWorktree(context.Background(), wt, ExplicitConfirmation()); err != nil {
		t.Fatalf("RemoveWorktree: %v", err)
	}
	script.AssertExhausted()
}

func TestRemoveWorktreesRequireAbsentPathAfterGitSuccess(t *testing.T) {
	for _, detached := range []bool{false, true} {
		name := "executor"
		if detached {
			name = "review"
		}
		t.Run(name, func(t *testing.T) {
			root := tempRoot(t)
			wt := tempRoot(t)
			entry := "branch refs/heads/orch/issue-4"
			remove := []string{"worktree", "remove", wt}
			if detached {
				entry = "detached"
				remove = []string{"worktree", "remove", "--force", wt}
			}
			calls := []execxtest.Call{{
				Name: "git", Args: listArgs, Stdout: porcelain(
					"worktree "+root, "HEAD 1111", "branch refs/heads/main", "",
					"worktree "+wt, "HEAD 2222", entry,
				),
			}}
			if !detached {
				calls = append(calls, execxtest.Call{Name: "git", Args: statusArgs, Dir: wt})
			}
			calls = append(calls, execxtest.Call{Name: "git", Args: remove, Dir: root})
			g, script := openScripted(t, root, calls...)
			var err error
			if detached {
				err = g.RemoveDetachedWorktree(context.Background(), wt)
			} else {
				err = g.RemoveWorktree(context.Background(), wt, ExplicitConfirmation())
			}
			script.AssertExhausted() // no pruning or fallback deletion
			if err == nil || errors.Is(err, ErrUnknownWorktree) {
				t.Fatalf("err = %v, want non-tolerable residue error after Git success", err)
			}
			for _, want := range []string{wt, "preserve", "retry"} {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("error %q does not contain %q", err, want)
				}
			}
			if _, err := os.Lstat(wt); err != nil {
				t.Fatalf("Git-success residue was not preserved: %v", err)
			}
		})
	}
}

func TestRequireWorktreeAbsent(t *testing.T) {
	root := tempRoot(t)
	file := filepath.Join(root, "residue.txt")
	if err := os.WriteFile(file, []byte("keep\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := requireWorktreeAbsent(filepath.Join(root, "absent")); err != nil {
		t.Fatalf("absent path: %v", err)
	}
	for _, path := range []string{root, file} {
		if err := requireWorktreeAbsent(path); err == nil || !strings.Contains(err.Error(), path) {
			t.Errorf("existing path %s: %v, want named residue error", path, err)
		}
	}
	// NUL is invalid on every supported OS, unlike permission fixtures
	// which can succeed under elevated users or Windows ACLs.
	err := requireWorktreeAbsent(filepath.Join(root, "invalid\x00path"))
	var pathErr *os.PathError
	if !errors.As(err, &pathErr) || !strings.Contains(err.Error(), "inspect worktree path") {
		t.Fatalf("inspection failure = %v, want wrapped filesystem error", err)
	}
}

func TestPruneWorktrees(t *testing.T) {
	root := tempRoot(t)
	g, script := openScripted(t, root, execxtest.Call{
		Name: "git", Args: []string{"worktree", "prune"}, Dir: root,
	})
	if err := g.PruneWorktrees(context.Background()); err != nil {
		t.Fatalf("PruneWorktrees: %v", err)
	}
	script.AssertExhausted()
}
