package gitops

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/kninetimmy/orch/internal/execx/execxtest"
	"github.com/kninetimmy/orch/internal/paths"
)

func canon(t *testing.T, path string) string {
	t.Helper()
	c, err := paths.Canonical(path)
	if err != nil {
		t.Fatalf("Canonical(%s): %v", path, err)
	}
	return c
}

func tempRoot(t *testing.T) string {
	t.Helper()
	return canon(t, t.TempDir())
}

// openScripted opens a Git rooted at root against a Script whose
// first call answers Open's rev-parse probe; calls holds the
// transcript the test itself exercises.
func openScripted(t *testing.T, root string, calls ...execxtest.Call) (*Git, *execxtest.Script) {
	t.Helper()
	script := &execxtest.Script{T: t, Calls: append([]execxtest.Call{{
		Name:   "git",
		Args:   []string{"rev-parse", "--show-toplevel"},
		Dir:    root,
		Env:    []string{"GIT_TERMINAL_PROMPT=0", "LC_ALL=C"},
		Stdout: root + "\n",
	}}, calls...)}
	g, err := Open(context.Background(), script, root)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	return g, script
}

func TestOpen(t *testing.T) {
	root := tempRoot(t)
	g, script := openScripted(t, root)
	script.AssertExhausted()
	if g.Root() != root {
		t.Errorf("Root() = %q, want %q", g.Root(), root)
	}
}

func TestOpenNotARepo(t *testing.T) {
	root := tempRoot(t)
	script := &execxtest.Script{T: t, Calls: []execxtest.Call{{
		Name:   "git",
		Args:   []string{"rev-parse", "--show-toplevel"},
		Dir:    root,
		Stderr: "fatal: not a git repository",
		Exit:   128,
	}}}
	_, err := Open(context.Background(), script, root)
	if !errors.Is(err, ErrNotARepo) {
		t.Fatalf("err = %v, want ErrNotARepo", err)
	}
}

func TestOpenTopLevelMismatch(t *testing.T) {
	top := tempRoot(t)
	sub := tempRoot(t)
	script := &execxtest.Script{T: t, Calls: []execxtest.Call{{
		Name:   "git",
		Args:   []string{"rev-parse", "--show-toplevel"},
		Dir:    sub,
		Stdout: top + "\n",
	}}}
	_, err := Open(context.Background(), script, sub)
	if err == nil || !strings.Contains(err.Error(), "top level") {
		t.Fatalf("err = %v, want top-level mismatch", err)
	}
}

func TestOpenRunnerError(t *testing.T) {
	root := tempRoot(t)
	sentinel := errors.New("spawn failed")
	script := &execxtest.Script{T: t, Calls: []execxtest.Call{{
		Name: "git",
		Args: []string{"rev-parse", "--show-toplevel"},
		Err:  sentinel,
	}}}
	_, err := Open(context.Background(), script, root)
	if !errors.Is(err, sentinel) {
		t.Fatalf("err = %v, want wrapped runner error", err)
	}
}
