package ghops

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/kninetimmy/orch/internal/execx/execxtest"
)

var repoViewArgs = []string{"repo", "view", "--json", "nameWithOwner,defaultBranchRef,url"}

func TestRepo(t *testing.T) {
	root := tempRoot(t)
	g, script := openScripted(t, root, execxtest.Call{
		Name:   "gh",
		Args:   repoViewArgs,
		Dir:    root,
		Env:    ghTestEnv,
		Stdout: `{"nameWithOwner":"octo/orch","defaultBranchRef":{"name":"main"},"url":"https://github.com/octo/orch"}` + "\n",
	})
	repo, err := g.Repo(context.Background())
	if err != nil {
		t.Fatalf("Repo: %v", err)
	}
	script.AssertExhausted()
	want := Repo{NameWithOwner: "octo/orch", DefaultBranch: "main", URL: "https://github.com/octo/orch"}
	if repo != want {
		t.Errorf("Repo = %+v, want %+v", repo, want)
	}
}

func TestRepoNoRemote(t *testing.T) {
	root := tempRoot(t)
	g, script := openScripted(t, root, execxtest.Call{
		Name:   "gh",
		Args:   repoViewArgs,
		Dir:    root,
		Env:    ghTestEnv,
		Stderr: "none of the git remotes configured for this repository point to a known GitHub host",
		Exit:   1,
	})
	_, err := g.Repo(context.Background())
	script.AssertExhausted()
	if !errors.Is(err, ErrNoGitHubRepo) {
		t.Fatalf("err = %v, want ErrNoGitHubRepo", err)
	}
	if !strings.Contains(err.Error(), "known GitHub host") {
		t.Errorf("err = %v, want gh's stderr preserved", err)
	}
}

func TestRepoBadJSON(t *testing.T) {
	root := tempRoot(t)
	g, script := openScripted(t, root, execxtest.Call{
		Name:   "gh",
		Args:   repoViewArgs,
		Dir:    root,
		Env:    ghTestEnv,
		Stdout: "not json",
	})
	_, err := g.Repo(context.Background())
	script.AssertExhausted()
	if err == nil || !strings.Contains(err.Error(), "unparsable JSON") {
		t.Fatalf("err = %v, want unparsable JSON error", err)
	}
}

func TestRepoMissingNameWithOwner(t *testing.T) {
	root := tempRoot(t)
	g, script := openScripted(t, root, execxtest.Call{
		Name:   "gh",
		Args:   repoViewArgs,
		Dir:    root,
		Env:    ghTestEnv,
		Stdout: `{"nameWithOwner":"","defaultBranchRef":{"name":"main"},"url":""}`,
	})
	_, err := g.Repo(context.Background())
	script.AssertExhausted()
	if err == nil || !strings.Contains(err.Error(), "nameWithOwner") {
		t.Fatalf("err = %v, want missing nameWithOwner error", err)
	}
}
