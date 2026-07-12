package gitops

import (
	"context"
	"errors"
	"testing"

	"github.com/kninetimmy/orch/internal/execx/execxtest"
)

func TestCommitAll(t *testing.T) {
	root := tempRoot(t)
	g, script := openScripted(t, root,
		execxtest.Call{Name: "git", Args: []string{"add", "-A"}, Dir: root},
		execxtest.Call{Name: "git", Args: []string{"diff", "--cached", "--quiet"}, Dir: root, Exit: 1},
		execxtest.Call{Name: "git", Args: []string{"commit", "-m", "Bootstrap orch configuration"}, Dir: root},
	)
	if err := g.CommitAll(context.Background(), root, "Bootstrap orch configuration"); err != nil {
		t.Fatalf("CommitAll: %v", err)
	}
	script.AssertExhausted()
}

func TestCommitAllNothingStaged(t *testing.T) {
	root := tempRoot(t)
	g, script := openScripted(t, root,
		execxtest.Call{Name: "git", Args: []string{"add", "-A"}, Dir: root},
		execxtest.Call{Name: "git", Args: []string{"diff", "--cached", "--quiet"}, Dir: root, Exit: 0},
	)
	err := g.CommitAll(context.Background(), root, "message")
	script.AssertExhausted()
	if !errors.Is(err, ErrNothingToCommit) {
		t.Fatalf("err = %v, want ErrNothingToCommit", err)
	}
}

func TestCommitAllAddFails(t *testing.T) {
	root := tempRoot(t)
	g, script := openScripted(t, root,
		execxtest.Call{Name: "git", Args: []string{"add", "-A"}, Dir: root, Exit: 1, Stderr: "fatal: bad object"},
	)
	err := g.CommitAll(context.Background(), root, "message")
	script.AssertExhausted()
	if err == nil {
		t.Fatal("CommitAll succeeded, want error")
	}
}

func TestCommitAllDiffCheckUnexpectedExit(t *testing.T) {
	root := tempRoot(t)
	g, script := openScripted(t, root,
		execxtest.Call{Name: "git", Args: []string{"add", "-A"}, Dir: root},
		execxtest.Call{Name: "git", Args: []string{"diff", "--cached", "--quiet"}, Dir: root, Exit: 128, Stderr: "fatal: ambiguous argument"},
	)
	err := g.CommitAll(context.Background(), root, "message")
	script.AssertExhausted()
	if err == nil {
		t.Fatal("CommitAll succeeded, want error")
	}
}

func TestCommitAllCommitFails(t *testing.T) {
	root := tempRoot(t)
	g, script := openScripted(t, root,
		execxtest.Call{Name: "git", Args: []string{"add", "-A"}, Dir: root},
		execxtest.Call{Name: "git", Args: []string{"diff", "--cached", "--quiet"}, Dir: root, Exit: 1},
		execxtest.Call{Name: "git", Args: []string{"commit", "-m", "message"}, Dir: root, Exit: 1, Stderr: "fatal: unable to write commit"},
	)
	err := g.CommitAll(context.Background(), root, "message")
	script.AssertExhausted()
	if err == nil {
		t.Fatal("CommitAll succeeded, want error")
	}
}
