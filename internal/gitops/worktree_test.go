package gitops

import (
	"context"
	"errors"
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

func TestAddWorktreeInsidePrimaryFailsClosed(t *testing.T) {
	root := tempRoot(t)
	g, script := openScripted(t, root)
	_, err := g.AddWorktree(context.Background(), filepath.Join(root, "wt"), "orch/issue-4", "main")
	script.AssertExhausted() // no git call was made
	if err == nil || !strings.Contains(err.Error(), "inside the primary checkout") {
		t.Fatalf("err = %v, want isolation refusal", err)
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
	stray := tempRoot(t)
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
	wt := tempRoot(t)
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
