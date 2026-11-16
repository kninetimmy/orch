package gitops

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"testing"

	"github.com/kninetimmy/orch/internal/execx"
)

func ownedJunction(t *testing.T, link, destination string) {
	t.Helper()
	cmd := exec.CommandContext(t.Context(), "powershell.exe", "-NoProfile", "-NonInteractive", "-Command",
		`New-Item -ItemType Junction -Path $env:ORCH_TEST_LINK -Target $env:ORCH_TEST_TARGET -ErrorAction Stop | Out-Null`)
	cmd.Env = append(os.Environ(), "ORCH_TEST_LINK="+link, "ORCH_TEST_TARGET="+destination)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("create owned junction: %v: %s", err, out)
	}
	t.Cleanup(func() {
		if err := os.Remove(link); err != nil && !os.IsNotExist(err) {
			t.Error(err)
		}
	})
}

type removalRecorder struct {
	calls []execx.Cmd
	after func(execx.Cmd)
}

func (r *removalRecorder) Run(ctx context.Context, cmd execx.Cmd) (execx.Result, error) {
	r.calls = append(r.calls, cmd)
	result, err := (execx.Local{}).Run(ctx, cmd)
	if err == nil && result.ExitCode == 0 && r.after != nil {
		r.after(cmd)
	}
	return result, err
}

func (r *removalRecorder) assertNoRemoval(t *testing.T) {
	t.Helper()
	for _, cmd := range r.calls {
		if slices.Contains(cmd.Args, "remove") || slices.Contains(cmd.Args, "prune") {
			t.Fatalf("hazard reached destructive Git: %+v", cmd)
		}
	}
}

func TestWindowsRemoveWorktreesRefuseIgnoredJunctions(t *testing.T) {
	for _, detached := range []bool{false, true} {
		t.Run(map[bool]string{false: "executor", true: "detached"}[detached], func(t *testing.T) {
			setupGitEnv(t)
			_, root := newRepo(t)
			commitFile(t, root, ".gitignore", ".scratch/\n")
			recorder := &removalRecorder{}
			g, err := Open(t.Context(), recorder, root)
			if err != nil {
				t.Fatal(err)
			}
			sibling := filepath.Join(t.TempDir(), "sibling")
			if _, err := g.AddDetachedWorktree(t.Context(), sibling, "main"); err != nil {
				t.Fatal(err)
			}
			target := filepath.Join(t.TempDir(), "target")
			if detached {
				_, err = g.AddDetachedWorktree(t.Context(), target, "main")
			} else {
				_, err = g.AddWorktree(t.Context(), target, "orch/fixture", "main")
			}
			if err != nil {
				t.Fatal(err)
			}
			scratch := filepath.Join(target, ".scratch")
			if err := os.Mkdir(scratch, 0o755); err != nil {
				t.Fatal(err)
			}
			for _, destination := range []string{root, sibling} {
				if err := os.WriteFile(filepath.Join(destination, "sentinel"), []byte("preserve\n"), 0o644); err != nil {
					t.Fatal(err)
				}
				ownedJunction(t, filepath.Join(scratch, filepath.Base(destination)), destination)
			}
			// This is the product-level baseline exploit: cleanliness succeeds
			// despite ignored junctions, not merely a raw Git reproduction.
			if err := g.RequireClean(t.Context(), target); err != nil {
				t.Fatal(err)
			}
			registrations := rawGit(t, root, "worktree", "list", "--porcelain")
			primaryHead := rawGit(t, root, "rev-parse", "HEAD")
			siblingGit, err := os.ReadFile(filepath.Join(sibling, ".git"))
			if err != nil {
				t.Fatal(err)
			}
			recorder.calls = nil
			remove := func() error {
				if detached {
					return g.RemoveDetachedWorktree(t.Context(), target)
				}
				return g.RemoveWorktree(t.Context(), target, ExplicitConfirmation())
			}
			err = remove()
			if err == nil || !strings.Contains(err.Error(), "linked or unverifiable checkout entry") {
				t.Fatalf("ignored junction accepted or wrong refusal: %v", err)
			}
			recorder.assertNoRemoval(t)
			if rawGit(t, root, "worktree", "list", "--porcelain") != registrations ||
				rawGit(t, root, "rev-parse", "HEAD") != primaryHead {
				t.Fatal("refusal changed Git registration or primary metadata")
			}
			if data, err := os.ReadFile(filepath.Join(sibling, ".git")); err != nil || string(data) != string(siblingGit) {
				t.Fatal("refusal changed sibling Git metadata")
			}
			for _, destination := range []string{root, sibling} {
				if data, err := os.ReadFile(filepath.Join(destination, "sentinel")); err != nil || string(data) != "preserve\n" {
					t.Fatalf("outside bytes changed: %s: %v", destination, err)
				}
				// Separate, known fixture-only preparation AFTER product refusal.
				if err := os.Remove(filepath.Join(scratch, filepath.Base(destination))); err != nil {
					t.Fatal(err)
				}
			}
			if err := remove(); err != nil {
				t.Fatalf("link-free retry: %v", err)
			}
			if err := requireWorktreeAbsent(target); err != nil {
				t.Fatal(err)
			}
			t.Log("real Gitops refused before remove/prune; primary/sibling bytes and metadata/registration preserved; link-free retry removed only target")
		})
	}
}

func TestWindowsRemoveWorktreesRefuseRedirectedRootAndParent(t *testing.T) {
	for _, detached := range []bool{false, true} {
		for _, parent := range []bool{false, true} {
			t.Run(fmtRemovalKind(detached, parent), func(t *testing.T) {
				setupGitEnv(t)
				g, root := newRepo(t)
				container := t.TempDir()
				target := filepath.Join(container, "target")
				var err error
				if detached {
					_, err = g.AddDetachedWorktree(t.Context(), target, "main")
				} else {
					_, err = g.AddWorktree(t.Context(), target, "orch/fixture", "main")
				}
				if err != nil {
					t.Fatal(err)
				}
				link := filepath.Join(t.TempDir(), "alias")
				requested := link
				destination := target
				if parent {
					destination = container
					requested = filepath.Join(link, "target")
				}
				ownedJunction(t, link, destination)
				registrations := rawGit(t, root, "worktree", "list", "--porcelain")
				if detached {
					err = g.RemoveDetachedWorktree(t.Context(), requested)
				} else {
					err = g.RemoveWorktree(t.Context(), requested, ExplicitConfirmation())
				}
				if err == nil || !strings.Contains(err.Error(), "preserve and inspect") {
					t.Fatalf("canonicalization hid redirected requested path: %v", err)
				}
				if rawGit(t, root, "worktree", "list", "--porcelain") != registrations {
					t.Fatal("refusal changed registration")
				}
				if _, err := os.Lstat(filepath.Join(target, ".git")); err != nil {
					t.Fatal(err)
				}
			})
		}
	}
}

func fmtRemovalKind(detached, parent bool) string {
	kind, entry := "executor", "root"
	if detached {
		kind = "detached"
	}
	if parent {
		entry = "parent"
	}
	return kind + "/" + entry
}

func TestWindowsBaseWorktreeRefusesHazardAfterCancellation(t *testing.T) {
	setupGitEnv(t)
	_, root := newRepo(t)
	// Keep even a preserved generated base path inside this test's owned temp.
	t.Setenv("TMP", t.TempDir())
	recorder := &removalRecorder{}
	g, err := Open(t.Context(), recorder, root)
	if err != nil {
		t.Fatal(err)
	}
	sentinel := errors.New("base callback failed")
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	var target, link, registrations string
	err = g.WithBaseWorktree(ctx, "main", func(dir string) error {
		target = dir
		if err := os.WriteFile(filepath.Join(root, "sentinel"), []byte("preserve\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		link = filepath.Join(dir, "escape")
		ownedJunction(t, link, root)
		registrations = rawGit(t, root, "worktree", "list", "--porcelain")
		recorder.calls = nil
		cancel()
		return errors.Join(sentinel, ctx.Err())
	})
	if !errors.Is(err, sentinel) || !errors.Is(err, context.Canceled) ||
		!strings.Contains(err.Error(), target) || !strings.Contains(err.Error(), "linked or unverifiable") {
		t.Fatalf("callback/cancellation/cleanup/path evidence lost: %v", err)
	}
	recorder.assertNoRemoval(t)
	if rawGit(t, root, "worktree", "list", "--porcelain") != registrations {
		t.Fatal("unsafe base teardown changed registration")
	}
	if data, err := os.ReadFile(filepath.Join(root, "sentinel")); err != nil || string(data) != "preserve\n" {
		t.Fatal("unsafe base teardown changed primary bytes")
	}
	if err := os.Remove(link); err != nil {
		t.Fatal(err)
	}
	if err := g.RemoveDetachedWorktree(t.Context(), target); err != nil {
		t.Fatal(err)
	}
	t.Log("canceled callback and hazardous cleanup errors retained; generated base path named and preserved; inspected fixture retry succeeds")
}

type reparseInfo struct {
	os.FileInfo
	attributes any
}

func (i reparseInfo) Sys() any { return i.attributes }

func TestWindowsInspectionRefusesUnsupportedReparseAttributes(t *testing.T) {
	info, err := os.Lstat(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if !ordinaryWorktreeEntry(info) {
		t.Fatal("ordinary directory refused")
	}
	attrs := *info.Sys().(*syscall.Win32FileAttributeData)
	attrs.FileAttributes |= syscall.FILE_ATTRIBUTE_REPARSE_POINT
	for _, evidence := range []any{nil, (*syscall.Win32FileAttributeData)(nil), &attrs} {
		if ordinaryWorktreeEntry(reparseInfo{info, evidence}) {
			t.Fatal("unavailable or unsupported reparse evidence accepted as ordinary directory")
		}
	}
}

func TestWindowsRemovalRechecksAfterGitPreconditions(t *testing.T) {
	for _, detached := range []bool{false, true} {
		t.Run(map[bool]string{false: "executor-status", true: "detached-list"}[detached], func(t *testing.T) {
			setupGitEnv(t)
			_, root := newRepo(t)
			commitFile(t, root, ".gitignore", ".scratch/\n")
			r := &removalRecorder{}
			g, err := Open(t.Context(), r, root)
			if err != nil {
				t.Fatal(err)
			}
			target := filepath.Join(t.TempDir(), "target")
			if detached {
				_, err = g.AddDetachedWorktree(t.Context(), target, "main")
			} else {
				_, err = g.AddWorktree(t.Context(), target, "orch/fixture", "main")
			}
			if err != nil {
				t.Fatal(err)
			}
			probe := "status"
			if detached {
				probe = "worktree"
			}
			r.calls = nil
			r.after = func(cmd execx.Cmd) {
				if len(cmd.Args) != 0 && cmd.Args[0] == probe {
					r.after = nil
					scratch := filepath.Join(target, ".scratch")
					if err := os.Mkdir(scratch, 0o755); err != nil {
						t.Fatal(err)
					}
					ownedJunction(t, filepath.Join(scratch, "escape"), root)
				}
			}
			if detached {
				err = g.RemoveDetachedWorktree(t.Context(), target)
			} else {
				err = g.RemoveWorktree(t.Context(), target, ExplicitConfirmation())
			}
			if err == nil || !strings.Contains(err.Error(), "linked or unverifiable") || r.after != nil {
				t.Fatalf("hazard introduced during Git probe bypassed reinspection: %v", err)
			}
			r.assertNoRemoval(t)
			if _, err := os.Stat(filepath.Join(root, ".git")); err != nil {
				t.Fatal(err)
			}
			if _, err := os.Stat(filepath.Join(target, ".git")); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestWindowsRemovalInspectionAbsenceCancellationAndInvalidPaths(t *testing.T) {
	root := t.TempDir()
	for _, target := range []string{root, filepath.Join(root, "absent")} {
		if err := CheckWorktreeRemoval(t.Context(), target); err != nil {
			t.Fatalf("ordinary/absent path refused: %v", err)
		}
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if err := CheckWorktreeRemoval(ctx, root); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation lost: %v", err)
	}
	for _, target := range []string{root + `\..\absent`, filepath.Join(root, "invalid\x00path")} {
		if err := CheckWorktreeRemoval(t.Context(), target); err == nil {
			t.Fatalf("incomplete or cleaned path evidence accepted: %q: %v", target, err)
		}
	}
}
