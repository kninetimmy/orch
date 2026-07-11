package gitops

// Integration tests against real git, which CI provides on all three
// OSes; the scripted tests in the sibling files pin exact argument
// vectors, these pin actual behavior.

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kninetimmy/orch/internal/execx"
)

// setupGitEnv isolates every git invocation in the test process —
// including the ones gitops itself makes — from the developer's real
// configuration, and provides the identity commits need.
func setupGitEnv(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skipf("git not on PATH: %v", err)
	}
	cfg := filepath.Join(t.TempDir(), "gitconfig")
	config := "[user]\n\tname = Orch Test\n\temail = orch-test@example.invalid\n"
	if err := os.WriteFile(cfg, []byte(config), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GIT_CONFIG_GLOBAL", cfg)
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
}

// rawGit runs git directly for test setup and assertions, failing the
// test on any error or non-zero exit.
func rawGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	res, err := (execx.Local{}).Run(context.Background(), execx.Cmd{
		Name: "git", Args: args, Dir: dir, Env: gitEnv,
	})
	if err != nil {
		t.Fatalf("git %v: %v", args, err)
	}
	if res.ExitCode != 0 {
		t.Fatalf("git %v exited %d: %s", args, res.ExitCode, res.Stderr)
	}
	return strings.TrimSpace(res.Stdout)
}

// newRepo creates a repository on branch main with one commit and
// returns it opened.
func newRepo(t *testing.T) (*Git, string) {
	t.Helper()
	root := tempRoot(t)
	rawGit(t, root, "init", "-b", "main")
	commitFile(t, root, "README.md", "initial\n")
	g, err := Open(context.Background(), execx.Local{}, root)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	return g, root
}

func commitFile(t *testing.T, dir, name, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	rawGit(t, dir, "add", "-A")
	rawGit(t, dir, "commit", "-m", "update "+name)
}

func TestIntegrationOpen(t *testing.T) {
	setupGitEnv(t)
	g, root := newRepo(t)
	if g.Root() != root {
		t.Errorf("Root() = %q, want %q", g.Root(), root)
	}

	if _, err := Open(context.Background(), execx.Local{}, t.TempDir()); !errors.Is(err, ErrNotARepo) {
		t.Errorf("Open(non-repo) err = %v, want ErrNotARepo", err)
	}

	sub := filepath.Join(root, "subdir")
	if err := os.Mkdir(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := Open(context.Background(), execx.Local{}, sub); err == nil || !strings.Contains(err.Error(), "top level") {
		t.Errorf("Open(subdir) err = %v, want top-level mismatch", err)
	}
}

func TestIntegrationRequireClean(t *testing.T) {
	setupGitEnv(t)
	g, root := newRepo(t)
	ctx := context.Background()

	if err := g.RequireClean(ctx, ""); err != nil {
		t.Fatalf("clean repo: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "README.md"), []byte("changed\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := g.RequireClean(ctx, ""); !errors.Is(err, ErrNotClean) {
		t.Errorf("modified tracked file: err = %v, want ErrNotClean", err)
	}
	rawGit(t, root, "checkout", "--", "README.md")
	if err := os.WriteFile(filepath.Join(root, "untracked.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := g.RequireClean(ctx, ""); !errors.Is(err, ErrNotClean) {
		t.Errorf("untracked file: err = %v, want ErrNotClean", err)
	}
}

func TestIntegrationChecks(t *testing.T) {
	setupGitEnv(t)
	g, root := newRepo(t)
	ctx := context.Background()

	branch, err := g.CurrentBranch(ctx)
	if err != nil || branch != "main" {
		t.Fatalf("CurrentBranch = %q, %v; want main, nil", branch, err)
	}
	if err := g.RequireNotOn(ctx, "main"); !errors.Is(err, ErrProtectedBranch) {
		t.Errorf("on main: err = %v, want ErrProtectedBranch", err)
	}
	head := rawGit(t, root, "rev-parse", "HEAD")
	got, err := g.RevParse(ctx, "main")
	if err != nil || got != head {
		t.Errorf("RevParse = %q, %v; want %q, nil", got, err, head)
	}
	rawGit(t, root, "checkout", "--detach")
	if _, err := g.CurrentBranch(ctx); !errors.Is(err, ErrDetachedHead) {
		t.Errorf("detached: err = %v, want ErrDetachedHead", err)
	}
}

func TestIntegrationWorktreeLifecycle(t *testing.T) {
	setupGitEnv(t)
	g, _ := newRepo(t)
	ctx := context.Background()
	wtPath := filepath.Join(t.TempDir(), "issue-4")

	wt, err := g.AddWorktree(ctx, wtPath, "orch/issue-4", "main")
	if err != nil {
		t.Fatalf("AddWorktree: %v", err)
	}
	if wt.Branch != "orch/issue-4" || wt.Head == "" {
		t.Errorf("worktree = %+v, want branch orch/issue-4 with a head", wt)
	}
	list, err := g.ListWorktrees(ctx)
	if err != nil || len(list) != 2 {
		t.Fatalf("ListWorktrees = %+v, %v; want 2 entries", list, err)
	}
	if !list[0].Primary || list[0].Branch != "main" {
		t.Errorf("first entry = %+v, want primary on main", list[0])
	}
	if list[1].Branch != "orch/issue-4" {
		t.Errorf("second entry = %+v, want orch/issue-4", list[1])
	}

	if _, err := g.AddWorktree(ctx, filepath.Join(t.TempDir(), "again"), "orch/issue-4", "main"); !errors.Is(err, ErrBranchExists) {
		t.Errorf("duplicate branch: err = %v, want ErrBranchExists", err)
	}

	// Preservation: unconfirmed and dirty removals both leave the
	// worktree in place (PRD §15).
	if err := g.RemoveWorktree(ctx, wt.Path, Confirmation{}); !errors.Is(err, ErrNotConfirmed) {
		t.Fatalf("unconfirmed: err = %v, want ErrNotConfirmed", err)
	}
	if _, err := os.Stat(wt.Path); err != nil {
		t.Fatal("worktree vanished after unconfirmed removal attempt")
	}
	if err := os.WriteFile(filepath.Join(wt.Path, "wip.go"), []byte("package wip\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := g.RemoveWorktree(ctx, wt.Path, ExplicitConfirmation()); !errors.Is(err, ErrNotClean) {
		t.Fatalf("dirty: err = %v, want ErrNotClean", err)
	}
	if _, err := os.Stat(wt.Path); err != nil {
		t.Fatal("worktree vanished after dirty removal attempt")
	}

	// Clean and confirmed: gone.
	if err := os.Remove(filepath.Join(wt.Path, "wip.go")); err != nil {
		t.Fatal(err)
	}
	if err := g.RemoveWorktree(ctx, wt.Path, ExplicitConfirmation()); err != nil {
		t.Fatalf("RemoveWorktree: %v", err)
	}
	if _, err := os.Stat(wt.Path); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("worktree still present after confirmed removal: %v", err)
	}
	list, err = g.ListWorktrees(ctx)
	if err != nil || len(list) != 1 {
		t.Errorf("ListWorktrees after removal = %+v, %v; want 1 entry", list, err)
	}
}

func TestIntegrationBranchDeletion(t *testing.T) {
	setupGitEnv(t)
	g, _ := newRepo(t)
	ctx := context.Background()
	wtPath := filepath.Join(t.TempDir(), "issue-5")

	wt, err := g.AddWorktree(ctx, wtPath, "orch/issue-5", "main")
	if err != nil {
		t.Fatalf("AddWorktree: %v", err)
	}
	commitFile(t, wt.Path, "feature.go", "package feature\n")

	// The branch is checked out: force deletion refuses.
	if err := g.ForceDeleteBranch(ctx, "orch/issue-5", ExplicitConfirmation()); err == nil || !strings.Contains(err.Error(), "checked out") {
		t.Fatalf("checked out: err = %v, want refusal", err)
	}
	if err := g.RemoveWorktree(ctx, wt.Path, ExplicitConfirmation()); err != nil {
		t.Fatalf("RemoveWorktree: %v", err)
	}

	// Unmerged: -d preserved, -D (post-verification path) deletes.
	if err := g.DeleteBranch(ctx, "orch/issue-5", ExplicitConfirmation()); !errors.Is(err, ErrBranchNotMerged) {
		t.Fatalf("unmerged: err = %v, want ErrBranchNotMerged", err)
	}
	if err := g.ForceDeleteBranch(ctx, "orch/issue-5", ExplicitConfirmation()); err != nil {
		t.Fatalf("ForceDeleteBranch: %v", err)
	}
	if err := g.DeleteBranch(ctx, "orch/issue-5", ExplicitConfirmation()); err == nil || !strings.Contains(err.Error(), "does not exist") {
		t.Errorf("deleted branch: err = %v, want missing-branch error", err)
	}
}

func TestIntegrationRemoteFlow(t *testing.T) {
	setupGitEnv(t)
	g, root := newRepo(t)
	ctx := context.Background()

	origin := filepath.Join(t.TempDir(), "origin.git")
	rawGit(t, filepath.Dir(origin), "init", "--bare", origin)
	rawGit(t, root, "remote", "add", "origin", origin)
	rawGit(t, root, "push", "origin", "main")

	wt, err := g.AddWorktree(ctx, filepath.Join(t.TempDir(), "issue-6"), "orch/issue-6", "main")
	if err != nil {
		t.Fatalf("AddWorktree: %v", err)
	}
	commitFile(t, wt.Path, "feature.go", "package feature\n")
	featureHead := rawGit(t, wt.Path, "rev-parse", "HEAD")
	rawGit(t, wt.Path, "push", "origin", "orch/issue-6")

	// Simulate the squash-merge landing on the remote: origin/main
	// advances past the local primary checkout.
	rawGit(t, wt.Path, "push", "origin", "orch/issue-6:main")

	// PRD §12 step 18: delete the merged remote branch.
	if err := g.DeleteRemoteBranch(ctx, "origin", "orch/issue-6", ExplicitConfirmation()); err != nil {
		t.Fatalf("DeleteRemoteBranch: %v", err)
	}
	if out := rawGit(t, root, "ls-remote", "--heads", "origin", "orch/issue-6"); out != "" {
		t.Errorf("remote branch survived deletion: %q", out)
	}

	// PRD §12 step 20: fast-forward the primary checkout.
	if err := g.FastForward(ctx, "origin", "main"); err != nil {
		t.Fatalf("FastForward: %v", err)
	}
	if head := rawGit(t, root, "rev-parse", "HEAD"); head != featureHead {
		t.Errorf("HEAD = %s, want %s after fast-forward", head, featureHead)
	}

	// Diverge: a commit on local main plus a different commit pushed
	// to origin main from the worktree.
	commitFile(t, root, "local.txt", "local\n")
	localHead := rawGit(t, root, "rev-parse", "HEAD")
	commitFile(t, wt.Path, "remote.txt", "remote\n")
	rawGit(t, wt.Path, "push", "origin", "orch/issue-6:main")

	if err := g.FastForward(ctx, "origin", "main"); !errors.Is(err, ErrNotFastForward) {
		t.Fatalf("diverged: err = %v, want ErrNotFastForward", err)
	}
	if head := rawGit(t, root, "rev-parse", "HEAD"); head != localHead {
		t.Errorf("HEAD moved to %s on refused fast-forward, want %s", head, localHead)
	}
}

func TestIntegrationPushFastForwardInRemoteExists(t *testing.T) {
	setupGitEnv(t)
	g, root := newRepo(t)
	ctx := context.Background()

	origin := filepath.Join(t.TempDir(), "origin.git")
	rawGit(t, filepath.Dir(origin), "init", "--bare", origin)
	rawGit(t, root, "remote", "add", "origin", origin)
	rawGit(t, root, "push", "origin", "main")
	rawGit(t, root, "fetch", "origin")

	wt, err := g.AddWorktree(ctx, filepath.Join(t.TempDir(), "issue-8"), "orch/issue-8", "main")
	if err != nil {
		t.Fatalf("AddWorktree: %v", err)
	}

	// A pre-dispatch branch sits at origin/main: FastForwardIn is a no-op
	// and CommitsAhead is 0 (no empty PR).
	if err := g.FastForwardIn(ctx, wt.Path, "origin/main"); err != nil {
		t.Fatalf("FastForwardIn (at tip): %v", err)
	}
	ahead, err := g.CommitsAhead(ctx, wt.Path, "origin/main", "orch/issue-8")
	if err != nil || ahead != 0 {
		t.Fatalf("CommitsAhead (no work) = %d, %v; want 0, nil", ahead, err)
	}

	// Add work, push it, and confirm the remote branch and the ahead count.
	commitFile(t, wt.Path, "feature.go", "package feature\n")
	if err := g.Push(ctx, wt.Path, "origin", "orch/issue-8"); err != nil {
		t.Fatalf("Push: %v", err)
	}
	exists, err := g.RemoteBranchExists(ctx, "origin", "orch/issue-8")
	if err != nil || !exists {
		t.Fatalf("RemoteBranchExists after push = %v, %v; want true, nil", exists, err)
	}
	ahead, err = g.CommitsAhead(ctx, wt.Path, "origin/main", "orch/issue-8")
	if err != nil || ahead != 1 {
		t.Fatalf("CommitsAhead (one commit) = %d, %v; want 1, nil", ahead, err)
	}

	gone, err := g.RemoteBranchExists(ctx, "origin", "orch/never")
	if err != nil || gone {
		t.Fatalf("RemoteBranchExists(absent) = %v, %v; want false, nil", gone, err)
	}
}

func TestIntegrationAddWorktreeInsidePrimary(t *testing.T) {
	setupGitEnv(t)
	g, root := newRepo(t)
	ctx := context.Background()

	// Not ignored: refused, parent stays clean.
	notIgnored := filepath.Join(root, "scratch", "issue-9")
	if _, err := g.AddWorktree(ctx, notIgnored, "orch/issue-9", "main"); !errors.Is(err, ErrNotIgnored) {
		t.Fatalf("not ignored: err = %v, want ErrNotIgnored", err)
	}
	if err := g.RequireClean(ctx, ""); err != nil {
		t.Fatalf("primary dirtied by a refused AddWorktree: %v", err)
	}

	// Ignored: succeeds, and the ignored directory never shows up as
	// dirty in the primary checkout's status.
	if err := os.WriteFile(filepath.Join(root, ".gitignore"), []byte(".orchestrator/worktrees/\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	commitFile(t, root, ".gitignore", ".orchestrator/worktrees/\n")

	inside := filepath.Join(root, ".orchestrator", "worktrees", "issue-9")
	wt, err := g.AddWorktree(ctx, inside, "orch/issue-9", "main")
	if err != nil {
		t.Fatalf("ignored inside-primary AddWorktree: %v", err)
	}
	if wt.Branch != "orch/issue-9" {
		t.Errorf("worktree = %+v, want branch orch/issue-9", wt)
	}
	if err := g.RequireClean(ctx, ""); err != nil {
		t.Fatalf("primary not clean with an ignored inside-primary worktree present: %v", err)
	}
}

func TestIntegrationWithBaseWorktree(t *testing.T) {
	setupGitEnv(t)
	g, _ := newRepo(t)
	ctx := context.Background()

	wt, err := g.AddWorktree(ctx, filepath.Join(t.TempDir(), "issue-7"), "orch/issue-7", "main")
	if err != nil {
		t.Fatalf("AddWorktree: %v", err)
	}
	commitFile(t, wt.Path, "README.md", "feature version\n")

	// fn sees the base branch's content, not the feature branch's,
	// and may leave untracked artifacts behind (PRD §16).
	var baseDir string
	sentinel := errors.New("reproduced on base")
	err = g.WithBaseWorktree(ctx, "main", func(dir string) error {
		baseDir = dir
		content, err := os.ReadFile(filepath.Join(dir, "README.md"))
		if err != nil {
			return err
		}
		if string(content) != "initial\n" {
			t.Errorf("base content = %q, want the main version", content)
		}
		if err := os.WriteFile(filepath.Join(dir, "build-artifact.o"), []byte("junk"), 0o644); err != nil {
			return err
		}
		return sentinel
	})
	if !errors.Is(err, sentinel) {
		t.Fatalf("err = %v, want fn's error", err)
	}
	if _, err := os.Stat(baseDir); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("base worktree %s survived cleanup: %v", baseDir, err)
	}
}
