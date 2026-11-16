//go:build !windows

package bootstrap

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/kninetimmy/orch/internal/execx"
	"github.com/kninetimmy/orch/internal/gitops"
)

func TestUnixBootstrapRemovalRetainsSymlinkBehavior(t *testing.T) {
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
	destination := t.TempDir()
	marker := filepath.Join(destination, "sentinel")
	if err := os.WriteFile(marker, []byte("preserve\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(destination, filepath.Join(wt.Path, "link")); err != nil {
		t.Fatal(err)
	}
	if err := removeWorktree(t.Context(), git, wt.Path); err != nil {
		t.Fatal(err)
	}
	if data, err := os.ReadFile(marker); err != nil || string(data) != "preserve\n" {
		t.Fatal("Unix bootstrap followed symlink")
	}
}
