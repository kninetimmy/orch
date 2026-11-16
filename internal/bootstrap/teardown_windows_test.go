package bootstrap

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kninetimmy/orch/internal/execx"
	"github.com/kninetimmy/orch/internal/execx/execxtest"
	"github.com/kninetimmy/orch/internal/ghops"
	"github.com/kninetimmy/orch/internal/gitops"
)

func bootstrapJunction(t *testing.T, link, destination string) {
	t.Helper()
	cmd := exec.CommandContext(t.Context(), "powershell.exe", "-NoProfile", "-NonInteractive", "-Command",
		`New-Item -ItemType Junction -Path $env:ORCH_TEST_LINK -Target $env:ORCH_TEST_TARGET -ErrorAction Stop | Out-Null`)
	cmd.Env = append(os.Environ(), "ORCH_TEST_LINK="+link, "ORCH_TEST_TARGET="+destination)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("create owned bootstrap junction: %v: %s", err, out)
	}
	t.Cleanup(func() {
		if err := os.Remove(link); err != nil && !os.IsNotExist(err) {
			t.Error(err)
		}
	})
}

func TestWindowsBootstrapTeardownRefusesJunction(t *testing.T) {
	for _, committed := range []bool{false, true} {
		t.Run(map[bool]string{false: "precommit", true: "preserved-commit"}[committed], func(t *testing.T) {
			root := newBootstrapRepo(t)
			git, err := gitops.Open(t.Context(), execx.Local{}, root)
			if err != nil {
				t.Fatal(err)
			}
			target, err := newWorktreeDir(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			wt, err := git.AddWorktree(t.Context(), target, BootstrapBranch, "HEAD")
			if err != nil {
				t.Fatal(err)
			}
			if committed {
				if err := os.WriteFile(filepath.Join(wt.Path, "commit.txt"), []byte("preserve commit\n"), 0o644); err != nil {
					t.Fatal(err)
				}
				rawGit(t, wt.Path, "add", "-A")
				rawGit(t, wt.Path, "commit", "-m", "bootstrap fixture")
			}
			head := rawGit(t, root, "rev-parse", BootstrapBranch)
			if err := os.WriteFile(filepath.Join(root, "sentinel"), []byte("preserve\n"), 0o644); err != nil {
				t.Fatal(err)
			}
			link := filepath.Join(wt.Path, "escape")
			bootstrapJunction(t, link, root)
			registrations := rawGit(t, root, "worktree", "list", "--porcelain")
			if err := removeWorktree(t.Context(), git, wt.Path); err == nil || !strings.Contains(err.Error(), "linked or unverifiable") {
				t.Fatalf("bootstrap teardown accepted junction: %v", err)
			}
			script := &execxtest.Script{T: t, Calls: []execxtest.Call{ghAuthCall(), ghSetStatusCall(1, ghops.StatusNeedsHuman)}}
			gh, err := ghops.Open(t.Context(), script, root)
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithCancel(t.Context())
			cancel()
			cause := errors.New("bootstrap fixture failure")
			err = failClosed(ctx, git, gh, 1, "https://github.com/o/r/issues/1", wt.Path, !committed, errors.Join(cause, ctx.Err()), job{title: bootstrapTitle, branch: BootstrapBranch, rerunCmd: "orch init --bootstrap"})
			script.AssertExhausted()
			if !errors.Is(err, cause) || !errors.Is(err, context.Canceled) || !strings.Contains(err.Error(), "linked or unverifiable") ||
				!strings.Contains(err.Error(), wt.Path) || !strings.Contains(err.Error(), "preserve and inspect") {
				t.Fatalf("failure lost cause, cleanup or preserved-path evidence: %v", err)
			}
			if committed && !strings.Contains(err.Error(), "carries the bootstrap commit") {
				t.Fatal("committed branch preservation guidance lost")
			}
			if rawGit(t, root, "worktree", "list", "--porcelain") != registrations ||
				rawGit(t, root, "rev-parse", BootstrapBranch) != head {
				t.Fatal("bootstrap refusal changed registration or preserved branch")
			}
			if data, err := os.ReadFile(filepath.Join(root, "sentinel")); err != nil || string(data) != "preserve\n" {
				t.Fatal("bootstrap refusal changed primary bytes")
			}
			if _, err := os.Stat(filepath.Join(root, ".git")); err != nil {
				t.Fatal("bootstrap refusal lost primary Git metadata")
			}
			// The product must refuse FIRST; only then unlink this known fixture.
			if err := os.Remove(link); err != nil {
				t.Fatal(err)
			}
			if err := removeWorktree(t.Context(), git, wt.Path); err != nil {
				t.Fatalf("link-free bootstrap retry: %v", err)
			}
			if _, err := os.Lstat(wt.Path); !os.IsNotExist(err) {
				t.Fatalf("bootstrap retry left target: %v", err)
			}
			t.Log("real bootstrap teardown refused; canceled failClosed retained cause and path, primary bytes/metadata and branch/registration; link-free retry succeeded")
		})
	}
}

func TestWindowsNewBootstrapDirectoryRefusesRedirectedTempParent(t *testing.T) {
	destination := t.TempDir()
	link := filepath.Join(t.TempDir(), "temp-alias")
	bootstrapJunction(t, link, destination)
	t.Setenv("TMP", link)
	_, err := newWorktreeDir(context.Background())
	if err == nil || !strings.Contains(err.Error(), "preserve bootstrap directory") {
		t.Fatalf("temp-parent canonicalization concealed redirection: %v", err)
	}
	entries, readErr := os.ReadDir(destination)
	if readErr != nil || len(entries) != 1 || !entries[0].IsDir() || !strings.HasPrefix(entries[0].Name(), "orch-bootstrap-") ||
		!strings.Contains(err.Error(), entries[0].Name()) {
		t.Fatalf("preserved fresh disposable directory not identified: entries=%v err=%v", entries, readErr)
	}
}

// Characterize bootstrap's old primitive, separately from the product refusal:
// Go RemoveAll does not reproduce native Git's ignored-junction traversal on
// this fixture. The product nevertheless refuses observed hazards before it.
func TestWindowsBootstrapBaselineRemoveAllJunctionFixture(t *testing.T) {
	root := newBootstrapRepo(t)
	marker := filepath.Join(root, "sentinel")
	if err := os.WriteFile(marker, []byte("preserve\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	target := t.TempDir()
	bootstrapJunction(t, filepath.Join(target, "escape"), root)
	if err := os.RemoveAll(target); err != nil {
		t.Fatal(err)
	}
	if data, err := os.ReadFile(marker); err != nil || string(data) != "preserve\n" {
		t.Fatal("baseline Go RemoveAll followed the owned junction")
	}
	if _, err := os.Stat(filepath.Join(root, ".git")); err != nil {
		t.Fatal("baseline Go RemoveAll lost primary metadata")
	}
	t.Log("owned Windows fixture: baseline Go RemoveAll preserved outside bytes/metadata; native Git exploit is not attributed to Go RemoveAll")
}
