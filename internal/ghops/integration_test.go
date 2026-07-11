package ghops

// Live tests against a real gh binary. Everything here is
// non-mutating: it reads auth state and repository resolution only.
// The mutating end-to-end pass (label taxonomy, issue lifecycle, PR
// open through merge) is a MANUAL procedure against the throwaway
// sandbox repository, per the staged test-harness decision:
//
//  1. Create a scratch repo on GitHub and clone it.
//  2. From its root: EnsureLabelTaxonomy, then CreateIssue with a
//     full Labels set, SetStatus through each transition, CreatePR
//     from a pushed branch, RequiredCI, MergePR with the viewed
//     HeadRefOid, CloseIssue — confirming each result on github.com.
//  3. Delete the scratch repo.
//
// CI never runs that pass; it requires authenticated gh and leaves
// remote state behind on failure.

import (
	"context"
	"errors"
	"os/exec"
	"testing"

	"github.com/kninetimmy/orch/internal/execx"
)

// requireGH skips unless gh is on PATH and authenticated, so the
// live tests are inert on CI and unauthenticated machines.
func requireGH(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("gh"); err != nil {
		t.Skipf("gh not on PATH: %v", err)
	}
	res, err := (execx.Local{}).Run(context.Background(), execx.Cmd{
		Name: "gh", Args: []string{"auth", "status"}, Dir: t.TempDir(), Env: ghEnv,
	})
	if err != nil || res.ExitCode != 0 {
		t.Skip("gh is not authenticated")
	}
}

func TestLiveOpen(t *testing.T) {
	requireGH(t)
	root := tempRoot(t)
	g, err := Open(context.Background(), execx.Local{}, root)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if g.Root() != root {
		t.Errorf("Root() = %q, want %q", g.Root(), root)
	}
}

func TestLiveRepoNoRemote(t *testing.T) {
	requireGH(t)
	// A bare temp dir has no git remotes, so repository resolution
	// must fail closed with the sentinel.
	root := tempRoot(t)
	g, err := Open(context.Background(), execx.Local{}, root)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if _, err := g.Repo(context.Background()); !errors.Is(err, ErrNoGitHubRepo) {
		t.Fatalf("Repo err = %v, want ErrNoGitHubRepo", err)
	}
}
