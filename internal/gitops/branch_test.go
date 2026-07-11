package gitops

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/kninetimmy/orch/internal/execx/execxtest"
)

func verifyBranchCall(branch string, exists bool) execxtest.Call {
	exit := 1
	if exists {
		exit = 0
	}
	return execxtest.Call{
		Name: "git", Args: []string{"rev-parse", "--verify", "--quiet", "refs/heads/" + branch}, Exit: exit,
	}
}

func TestFetch(t *testing.T) {
	root := tempRoot(t)
	g, script := openScripted(t, root, execxtest.Call{
		Name: "git", Args: []string{"fetch", "origin", "main"}, Dir: root,
	})
	if err := g.Fetch(context.Background(), "origin", "main"); err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	script.AssertExhausted()
}

func TestDeleteBranchUnconfirmed(t *testing.T) {
	g, script := openScripted(t, tempRoot(t))
	err := g.DeleteBranch(context.Background(), "orch/issue-4", Confirmation{})
	script.AssertExhausted() // fail closed before any git call
	if !errors.Is(err, ErrNotConfirmed) {
		t.Fatalf("err = %v, want ErrNotConfirmed", err)
	}
}

func TestDeleteBranchMissing(t *testing.T) {
	g, script := openScripted(t, tempRoot(t), verifyBranchCall("gone", false))
	err := g.DeleteBranch(context.Background(), "gone", ExplicitConfirmation())
	script.AssertExhausted()
	if err == nil || !strings.Contains(err.Error(), "does not exist") {
		t.Fatalf("err = %v, want missing-branch error", err)
	}
}

func TestDeleteBranchUnmergedPreserved(t *testing.T) {
	g, script := openScripted(t, tempRoot(t),
		verifyBranchCall("orch/issue-4", true),
		execxtest.Call{Name: "git", Args: []string{"merge-base", "--is-ancestor", "refs/heads/orch/issue-4", "HEAD"}, Exit: 1},
	)
	err := g.DeleteBranch(context.Background(), "orch/issue-4", ExplicitConfirmation())
	script.AssertExhausted() // no `branch -d` followed
	if !errors.Is(err, ErrBranchNotMerged) {
		t.Fatalf("err = %v, want ErrBranchNotMerged", err)
	}
}

func TestDeleteBranch(t *testing.T) {
	g, script := openScripted(t, tempRoot(t),
		verifyBranchCall("orch/issue-4", true),
		execxtest.Call{Name: "git", Args: []string{"merge-base", "--is-ancestor", "refs/heads/orch/issue-4", "HEAD"}},
		execxtest.Call{Name: "git", Args: []string{"branch", "-d", "orch/issue-4"}},
	)
	if err := g.DeleteBranch(context.Background(), "orch/issue-4", ExplicitConfirmation()); err != nil {
		t.Fatalf("DeleteBranch: %v", err)
	}
	script.AssertExhausted()
}

func TestForceDeleteBranchCheckedOutRefused(t *testing.T) {
	root := tempRoot(t)
	wt := tempRoot(t)
	g, script := openScripted(t, root,
		verifyBranchCall("orch/issue-4", true),
		execxtest.Call{Name: "git", Args: listArgs, Stdout: porcelain(
			"worktree "+root, "HEAD 1111", "branch refs/heads/main", "",
			"worktree "+wt, "HEAD 2222", "branch refs/heads/orch/issue-4",
		)},
	)
	err := g.ForceDeleteBranch(context.Background(), "orch/issue-4", ExplicitConfirmation())
	script.AssertExhausted() // no `branch -D` followed
	if err == nil || !strings.Contains(err.Error(), "checked out") {
		t.Fatalf("err = %v, want checked-out refusal", err)
	}
}

func TestForceDeleteBranch(t *testing.T) {
	root := tempRoot(t)
	g, script := openScripted(t, root,
		verifyBranchCall("orch/issue-4", true),
		execxtest.Call{Name: "git", Args: listArgs, Stdout: porcelain(
			"worktree "+root, "HEAD 1111", "branch refs/heads/main",
		)},
		execxtest.Call{Name: "git", Args: []string{"branch", "-D", "orch/issue-4"}},
	)
	if err := g.ForceDeleteBranch(context.Background(), "orch/issue-4", ExplicitConfirmation()); err != nil {
		t.Fatalf("ForceDeleteBranch: %v", err)
	}
	script.AssertExhausted()
}

func TestForceDeleteBranchUnconfirmed(t *testing.T) {
	g, script := openScripted(t, tempRoot(t))
	err := g.ForceDeleteBranch(context.Background(), "orch/issue-4", Confirmation{})
	script.AssertExhausted()
	if !errors.Is(err, ErrNotConfirmed) {
		t.Fatalf("err = %v, want ErrNotConfirmed", err)
	}
}

func TestDeleteRemoteBranch(t *testing.T) {
	root := tempRoot(t)
	g, script := openScripted(t, root, execxtest.Call{
		Name: "git", Args: []string{"push", "origin", "--delete", "orch/issue-4"}, Dir: root,
	})
	if err := g.DeleteRemoteBranch(context.Background(), "origin", "orch/issue-4", ExplicitConfirmation()); err != nil {
		t.Fatalf("DeleteRemoteBranch: %v", err)
	}
	script.AssertExhausted()
}

func TestDeleteRemoteBranchUnconfirmed(t *testing.T) {
	g, script := openScripted(t, tempRoot(t))
	err := g.DeleteRemoteBranch(context.Background(), "origin", "orch/issue-4", Confirmation{})
	script.AssertExhausted()
	if !errors.Is(err, ErrNotConfirmed) {
		t.Fatalf("err = %v, want ErrNotConfirmed", err)
	}
}

func currentBranchCall(branch string) execxtest.Call {
	return execxtest.Call{Name: "git", Args: []string{"symbolic-ref", "--short", "HEAD"}, Stdout: branch + "\n"}
}

func TestFastForwardWrongBranch(t *testing.T) {
	g, script := openScripted(t, tempRoot(t), currentBranchCall("orch/issue-4"))
	err := g.FastForward(context.Background(), "origin", "main")
	script.AssertExhausted() // stopped before fetch
	if err == nil || !strings.Contains(err.Error(), "refusing to fast-forward") {
		t.Fatalf("err = %v, want wrong-branch refusal", err)
	}
}

func TestFastForwardDirty(t *testing.T) {
	g, script := openScripted(t, tempRoot(t),
		currentBranchCall("main"),
		execxtest.Call{Name: "git", Args: statusArgs, Stdout: "?? junk\n"},
	)
	err := g.FastForward(context.Background(), "origin", "main")
	script.AssertExhausted()
	if !errors.Is(err, ErrNotClean) {
		t.Fatalf("err = %v, want ErrNotClean", err)
	}
}

func TestFastForwardDiverged(t *testing.T) {
	g, script := openScripted(t, tempRoot(t),
		currentBranchCall("main"),
		execxtest.Call{Name: "git", Args: statusArgs},
		execxtest.Call{Name: "git", Args: []string{"fetch", "origin", "main"}},
		execxtest.Call{Name: "git", Args: []string{"merge-base", "--is-ancestor", "HEAD", "FETCH_HEAD"}, Exit: 1},
	)
	err := g.FastForward(context.Background(), "origin", "main")
	script.AssertExhausted() // no merge followed
	if !errors.Is(err, ErrNotFastForward) {
		t.Fatalf("err = %v, want ErrNotFastForward", err)
	}
}

func TestFastForward(t *testing.T) {
	g, script := openScripted(t, tempRoot(t),
		currentBranchCall("main"),
		execxtest.Call{Name: "git", Args: statusArgs},
		execxtest.Call{Name: "git", Args: []string{"fetch", "origin", "main"}},
		execxtest.Call{Name: "git", Args: []string{"merge-base", "--is-ancestor", "HEAD", "FETCH_HEAD"}},
		execxtest.Call{Name: "git", Args: []string{"merge", "--ff-only", "FETCH_HEAD"}},
	)
	if err := g.FastForward(context.Background(), "origin", "main"); err != nil {
		t.Fatalf("FastForward: %v", err)
	}
	script.AssertExhausted()
}
