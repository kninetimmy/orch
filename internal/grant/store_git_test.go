package grant

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/kninetimmy/orch/internal/execx"
)

func git(t *testing.T, dir string, args ...string) string {
	t.Helper()
	res, err := (execx.Local{}).Run(context.Background(), execx.Cmd{Name: "git", Args: args, Dir: dir})
	if err != nil || res.ExitCode != 0 {
		t.Fatalf("git %v: %v %s", args, err, res.Stderr)
	}
	return res.Stdout
}

// A grant created from the primary checkout is the grant every linked
// worktree sees, and no grant operation shows up in any `git status`.
func TestStoreSharedAcrossWorktreesAndInvisibleToGit(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skipf("git not on PATH: %v", err)
	}
	cfg := filepath.Join(t.TempDir(), "gitconfig")
	if err := os.WriteFile(cfg, []byte("[user]\n\tname = Orch Test\n\temail = orch-test@example.invalid\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GIT_CONFIG_GLOBAL", cfg)
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	main, wt := filepath.Join(base, "main"), filepath.Join(base, "wt")
	if err := os.Mkdir(main, 0o755); err != nil {
		t.Fatal(err)
	}
	git(t, main, "init", "-b", "main")
	if err := os.WriteFile(filepath.Join(main, "README.md"), []byte("x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	git(t, main, "add", ".")
	git(t, main, "commit", "-m", "init")
	git(t, main, "worktree", "add", "-b", "feature", wt)
	status := func() [2]string {
		return [2]string{git(t, main, "status", "--porcelain"), git(t, wt, "status", "--porcelain")}
	}
	before := status()

	ctx := context.Background()
	fromMain, err := Open(ctx, execx.Local{}, main)
	if err != nil {
		t.Fatal(err)
	}
	fromWT, err := Open(ctx, execx.Local{}, wt)
	if err != nil {
		t.Fatal(err)
	}
	if fromMain.Dir() != fromWT.Dir() {
		t.Fatalf("stores differ: %s vs %s", fromMain.Dir(), fromWT.Dir())
	}
	g, err := fromMain.Create(approved(t, proposal()), session, now)
	if err != nil {
		t.Fatal(err)
	}
	if got, err := fromWT.Active(now); err != nil || got == nil || got.Terms.ID != g.Terms.ID {
		t.Fatalf("worktree sees %+v, %v", got, err)
	}
	if _, err := fromWT.RecordRun(g.Terms.ID, "run-1", now); err != nil {
		t.Fatal(err)
	}
	if got, err := fromMain.Active(now); err != nil || len(got.Runs) != 1 {
		t.Fatalf("main sees %+v, %v", got, err)
	}
	if after := status(); after != before {
		t.Fatalf("git status changed: %q -> %q", before, after)
	}
	if r, err := fromWT.Revoke(now); err != nil || r == nil {
		t.Fatalf("revoke from worktree: %+v, %v", r, err)
	}
	if got, err := fromMain.Active(now); err != nil || got != nil {
		t.Fatalf("main still sees %+v, %v", got, err)
	}
	if after := status(); after != before {
		t.Fatalf("git status changed: %q -> %q", before, after)
	}
}
