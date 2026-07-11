package paths

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// symlink creates newname pointing at oldname, skipping the test on
// platforms where symlink creation is unavailable (Windows without
// Developer Mode or elevation).
func symlink(t *testing.T, oldname, newname string) {
	t.Helper()
	if err := os.Symlink(oldname, newname); err != nil {
		t.Skipf("cannot create symlinks here: %v", err)
	}
}

// canonical is Canonical with the error turned into a test failure.
func canonical(t *testing.T, path string) string {
	t.Helper()
	c, err := Canonical(path)
	if err != nil {
		t.Fatalf("Canonical(%s): %v", path, err)
	}
	return c
}

func TestCanonicalExistingDir(t *testing.T) {
	root := t.TempDir()
	got := canonical(t, root)
	// Canonicalizing an already-canonical path is a fixed point.
	if again := canonical(t, got); again != got {
		t.Errorf("Canonical not idempotent: %s then %s", got, again)
	}
	if !filepath.IsAbs(got) {
		t.Errorf("Canonical(%s) = %s, want absolute", root, got)
	}
}

func TestCanonicalMissingTail(t *testing.T) {
	root := t.TempDir()
	got := canonical(t, filepath.Join(root, "a", "b.txt"))
	want := filepath.Join(canonical(t, root), "a", "b.txt")
	if got != want {
		t.Errorf("Canonical = %s, want %s", got, want)
	}
}

func TestCanonicalRelative(t *testing.T) {
	root := t.TempDir()
	t.Chdir(root)
	got := canonical(t, filepath.Join("sub", "file.go"))
	want := filepath.Join(canonical(t, root), "sub", "file.go")
	if got != want {
		t.Errorf("Canonical = %s, want %s", got, want)
	}
}

func TestCanonicalResolvesSymlink(t *testing.T) {
	base := t.TempDir()
	real := filepath.Join(base, "real")
	if err := os.Mkdir(real, 0o755); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(base, "link")
	symlink(t, real, link)

	// The tail does not exist; the symlinked ancestor must still resolve.
	got := canonical(t, filepath.Join(link, "new.txt"))
	want := filepath.Join(canonical(t, real), "new.txt")
	if got != want {
		t.Errorf("Canonical = %s, want %s", got, want)
	}
}

func TestInside(t *testing.T) {
	base := t.TempDir()
	root := filepath.Join(base, "repo")
	if err := os.MkdirAll(filepath.Join(root, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	// Sibling whose name extends the root's ("repo2" vs "repo").
	if err := os.Mkdir(filepath.Join(base, "repo2"), 0o755); err != nil {
		t.Fatal(err)
	}

	cases := map[string]struct {
		path string
		want bool
	}{
		"root itself":         {root, true},
		"direct child":        {filepath.Join(root, "a.go"), true},
		"nested child":        {filepath.Join(root, "sub", "b.go"), true},
		"missing deep child":  {filepath.Join(root, "no", "such", "c.go"), true},
		"parent":              {base, false},
		"sibling name prefix": {filepath.Join(base, "repo2"), false},
		"dotdot escape":       {filepath.Join(root, "..", "repo2", "d.go"), false},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			got, err := Inside(root, tc.path)
			if err != nil {
				t.Fatalf("Inside: %v", err)
			}
			if got != tc.want {
				t.Errorf("Inside(%s, %s) = %v, want %v", root, tc.path, got, tc.want)
			}
		})
	}
}

func TestInsideSymlinkCannotEscape(t *testing.T) {
	base := t.TempDir()
	root := filepath.Join(base, "repo")
	outside := filepath.Join(base, "outside")
	for _, dir := range []string{root, outside} {
		if err := os.Mkdir(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	symlink(t, outside, filepath.Join(root, "link"))

	got, err := Inside(root, filepath.Join(root, "link", "e.go"))
	if err != nil {
		t.Fatalf("Inside: %v", err)
	}
	if got {
		t.Error("Inside = true for a symlink that points outside the root")
	}
}

func TestInsideCaseFolding(t *testing.T) {
	root := filepath.FromSlash("/Repo")
	path := filepath.FromSlash("/repo/sub/f.go")
	if !inside(root, path, true) {
		t.Error("inside(fold=true) = false, want true for case-differing paths")
	}
	// On Windows filepath.Rel itself folds case, so the unfolded
	// comparison is only observable elsewhere (notably macOS, where
	// Rel is case-sensitive but the default filesystem is not —
	// which is what the explicit fold is for).
	if runtime.GOOS != "windows" && inside(root, path, false) {
		t.Error("inside(fold=false) = true, want false for case-differing paths")
	}
}

func TestFindRoot(t *testing.T) {
	root := t.TempDir()
	deep := filepath.Join(root, "a", "b")
	if err := os.MkdirAll(deep, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(root, OrchestratorDir), 0o755); err != nil {
		t.Fatal(err)
	}

	got, err := FindRoot(deep)
	if err != nil {
		t.Fatalf("FindRoot: %v", err)
	}
	if want := canonical(t, root); got != want {
		t.Errorf("FindRoot = %s, want %s", got, want)
	}
}

func TestFindRootNotFound(t *testing.T) {
	got, err := FindRoot(t.TempDir())
	if err == nil {
		// A real ancestor of the temp dir happens to be initialized;
		// the not-found case cannot be exercised on this machine.
		t.Skipf("ancestor %s contains %s", got, OrchestratorDir)
	}
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("FindRoot error = %v, want ErrNotFound", err)
	}
}

func TestFindRootRejectsNonDirectory(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, OrchestratorDir), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := FindRoot(root)
	if err == nil || !strings.Contains(err.Error(), "not a real directory") {
		t.Errorf("FindRoot error = %v, want 'not a real directory'", err)
	}
}

// TestFindOutermostRootNested confirms the outermost .orchestrator ancestor
// wins when roots nest, the opposite of FindRoot's innermost selection.
func TestFindOutermostRootNested(t *testing.T) {
	outer := t.TempDir()
	inner := filepath.Join(outer, "a", "inner")
	deep := filepath.Join(inner, "b", "c")
	if err := os.MkdirAll(deep, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, r := range []string{outer, inner} {
		if err := os.Mkdir(filepath.Join(r, OrchestratorDir), 0o755); err != nil {
			t.Fatal(err)
		}
	}

	got, err := FindOutermostRoot(deep)
	if err != nil {
		t.Fatalf("FindOutermostRoot: %v", err)
	}
	if want := canonical(t, outer); got != want {
		t.Errorf("FindOutermostRoot = %s, want outer %s", got, want)
	}
	// FindRoot picks the innermost, proving the two differ here.
	if inRoot, err := FindRoot(deep); err != nil || inRoot != canonical(t, inner) {
		t.Errorf("FindRoot = %s, %v; want inner %s", inRoot, err, canonical(t, inner))
	}
}

// TestFindOutermostRootWorktreeShaped mirrors the guard regression: a
// worktree checkout under the primary root carries its own committed
// .orchestrator/, but the outermost root is still the primary checkout.
func TestFindOutermostRootWorktreeShaped(t *testing.T) {
	root := t.TempDir()
	worktree := filepath.Join(root, OrchestratorDir, "worktrees", "issue-3")
	target := filepath.Join(worktree, "src")
	if err := os.MkdirAll(target, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, r := range []string{root, worktree} {
		if err := os.MkdirAll(filepath.Join(r, OrchestratorDir), 0o755); err != nil {
			t.Fatal(err)
		}
	}

	got, err := FindOutermostRoot(filepath.Join(target, "x.go"))
	if err != nil {
		t.Fatalf("FindOutermostRoot: %v", err)
	}
	if want := canonical(t, root); got != want {
		t.Errorf("FindOutermostRoot = %s, want primary root %s", got, want)
	}
}

func TestFindOutermostRootNotFound(t *testing.T) {
	got, err := FindOutermostRoot(t.TempDir())
	if err == nil {
		t.Skipf("ancestor %s contains %s", got, OrchestratorDir)
	}
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("FindOutermostRoot error = %v, want ErrNotFound", err)
	}
}

func TestFindOutermostRootRejectsNonDirectory(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, OrchestratorDir), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := FindOutermostRoot(root)
	if err == nil || !strings.Contains(err.Error(), "not a real directory") {
		t.Errorf("FindOutermostRoot error = %v, want 'not a real directory'", err)
	}
}
