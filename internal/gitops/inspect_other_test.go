//go:build !windows

package gitops

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestGenericUnixRemovalRetainsSymlinkBehavior(t *testing.T) {
	for _, kind := range []string{"executor", "detached", "base"} {
		t.Run(kind, func(t *testing.T) {
			setupGitEnv(t)
			g, _ := newRepo(t)
			destination := t.TempDir()
			marker := filepath.Join(destination, "sentinel")
			if err := os.WriteFile(marker, []byte("preserve\n"), 0o644); err != nil {
				t.Fatal(err)
			}
			plant := func(dir string) {
				t.Helper()
				if err := os.Mkdir(filepath.Join(dir, ".scratch"), 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(destination, filepath.Join(dir, ".scratch", "link")); err != nil {
					t.Fatal(err)
				}
			}
			ctx := context.Background()
			if kind == "base" {
				if err := g.WithBaseWorktree(ctx, "main", func(dir string) error { plant(dir); return nil }); err != nil {
					t.Fatal(err)
				}
			} else {
				commitFile(t, g.Root(), ".gitignore", ".scratch/\n")
				target := filepath.Join(t.TempDir(), "checkout")
				var err error
				if kind == "detached" {
					_, err = g.AddDetachedWorktree(ctx, target, "main")
				} else {
					_, err = g.AddWorktree(ctx, target, "orch/fixture", "main")
				}
				if err != nil {
					t.Fatal(err)
				}
				plant(target)
				if kind == "detached" {
					err = g.RemoveDetachedWorktree(ctx, target)
				} else {
					err = g.RemoveWorktree(ctx, target, ExplicitConfirmation())
				}
				if err != nil {
					t.Fatal(err)
				}
			}
			if data, err := os.ReadFile(marker); err != nil || string(data) != "preserve\n" {
				t.Fatal("Unix removal followed symlink or changed outside bytes")
			}
		})
	}
}
