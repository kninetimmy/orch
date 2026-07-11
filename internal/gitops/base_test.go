package gitops

import (
	"context"
	"errors"
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/kninetimmy/orch/internal/execx"
)

// recordingRunner answers git calls by pattern instead of exact argv,
// because WithBaseWorktree generates its own temp path that a fixed
// transcript cannot predict.
type recordingRunner struct {
	root      string
	calls     [][]string
	removeErr error
}

func (r *recordingRunner) Run(_ context.Context, c execx.Cmd) (execx.Result, error) {
	r.calls = append(r.calls, c.Args)
	switch {
	case slices.Equal(c.Args, []string{"rev-parse", "--show-toplevel"}):
		return execx.Result{Stdout: r.root + "\n"}, nil
	case len(c.Args) > 1 && c.Args[0] == "worktree" && c.Args[1] == "remove":
		if r.removeErr != nil {
			return execx.Result{}, r.removeErr
		}
		// Delete the directory like real git would, so the test can
		// assert the disposable checkout is gone (and the system temp
		// directory stays clean).
		return execx.Result{}, os.RemoveAll(c.Args[len(c.Args)-1])
	default:
		return execx.Result{}, nil
	}
}

func openRecording(t *testing.T, removeErr error) (*Git, *recordingRunner) {
	t.Helper()
	r := &recordingRunner{root: tempRoot(t), removeErr: removeErr}
	g, err := Open(context.Background(), r, r.root)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	return g, r
}

func TestWithBaseWorktree(t *testing.T) {
	g, r := openRecording(t, nil)
	var got string
	err := g.WithBaseWorktree(context.Background(), "origin/main", func(dir string) error {
		got = dir
		return nil
	})
	if err != nil {
		t.Fatalf("WithBaseWorktree: %v", err)
	}
	if got == "" {
		t.Fatal("fn was not called")
	}
	if _, err := os.Stat(got); err == nil {
		t.Errorf("temp worktree directory %s still exists", got)
	}

	// The transcript must be: open probe, detached add of the ref at
	// the generated dir, forced remove of that same dir.
	if len(r.calls) != 3 {
		t.Fatalf("got %d git calls, want 3: %v", len(r.calls), r.calls)
	}
	wantAdd := []string{"worktree", "add", "--detach", got, "origin/main"}
	if !slices.Equal(r.calls[1], wantAdd) {
		t.Errorf("add call = %v, want %v", r.calls[1], wantAdd)
	}
	wantRemove := []string{"worktree", "remove", "--force", got}
	if !slices.Equal(r.calls[2], wantRemove) {
		t.Errorf("remove call = %v, want %v", r.calls[2], wantRemove)
	}
}

func TestWithBaseWorktreeFnErrorStillCleansUp(t *testing.T) {
	g, r := openRecording(t, nil)
	sentinel := errors.New("reproduction failed to build")
	err := g.WithBaseWorktree(context.Background(), "main", func(string) error { return sentinel })
	if !errors.Is(err, sentinel) {
		t.Fatalf("err = %v, want fn's error", err)
	}
	last := r.calls[len(r.calls)-1]
	if len(last) < 3 || last[0] != "worktree" || last[1] != "remove" {
		t.Errorf("last call = %v, want forced worktree remove", last)
	}
}

func TestWithBaseWorktreeCleanupFailureNotSwallowed(t *testing.T) {
	removeErr := errors.New("remove refused")
	g, _ := openRecording(t, removeErr)
	sentinel := errors.New("fn error")
	err := g.WithBaseWorktree(context.Background(), "main", func(string) error { return sentinel })
	if !errors.Is(err, sentinel) {
		t.Errorf("err = %v, want fn's error preserved", err)
	}
	if !errors.Is(err, removeErr) {
		t.Errorf("err = %v, want cleanup error joined", err)
	}
	if !strings.Contains(err.Error(), "clean up base worktree") {
		t.Errorf("err = %v, want cleanup context", err)
	}
}
