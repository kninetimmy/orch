package ghops

import (
	"context"
	"errors"
	"testing"

	"github.com/kninetimmy/orch/internal/execx/execxtest"
	"github.com/kninetimmy/orch/internal/paths"
)

// ghTestEnv mirrors ghEnv so transcripts pin the exact fail-closed
// environment every invocation must carry.
var ghTestEnv = []string{
	"GH_PROMPT_DISABLED=1",
	"GH_NO_UPDATE_NOTIFIER=1",
	"GH_SPINNER_DISABLED=1",
	"NO_COLOR=1",
	"CLICOLOR=0",
}

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

// openScripted opens a GH rooted at root against a Script whose first
// call answers Open's auth-status probe; calls holds the transcript
// the test itself exercises.
func openScripted(t *testing.T, root string, calls ...execxtest.Call) (*GH, *execxtest.Script) {
	t.Helper()
	script := &execxtest.Script{T: t, Calls: append([]execxtest.Call{{
		Name: "gh",
		Args: []string{"auth", "status"},
		Dir:  root,
		Env:  ghTestEnv,
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

func TestOpenNotAuthenticated(t *testing.T) {
	root := tempRoot(t)
	script := &execxtest.Script{T: t, Calls: []execxtest.Call{{
		Name:   "gh",
		Args:   []string{"auth", "status"},
		Dir:    root,
		Env:    ghTestEnv,
		Stderr: "You are not logged into any GitHub hosts.",
		Exit:   1,
	}}}
	_, err := Open(context.Background(), script, root)
	if !errors.Is(err, ErrNotAuthenticated) {
		t.Fatalf("err = %v, want ErrNotAuthenticated", err)
	}
}

func TestOpenRunnerError(t *testing.T) {
	root := tempRoot(t)
	sentinel := errors.New("spawn failed")
	script := &execxtest.Script{T: t, Calls: []execxtest.Call{{
		Name: "gh",
		Args: []string{"auth", "status"},
		Err:  sentinel,
	}}}
	_, err := Open(context.Background(), script, root)
	if !errors.Is(err, sentinel) {
		t.Fatalf("err = %v, want wrapped runner error", err)
	}
}
