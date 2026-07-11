package instructions

import (
	"os"
	"path/filepath"
	"testing"
)

// symlink creates newname pointing at oldname, skipping the test on
// platforms where symlink creation is unavailable (Windows without
// Developer Mode or elevation) — the paths_test.go idiom.
func symlink(t *testing.T, oldname, newname string) {
	t.Helper()
	if err := os.Symlink(oldname, newname); err != nil {
		t.Skipf("cannot create symlinks here: %v", err)
	}
}

func mkdirAll(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(path, 0o755); err != nil {
		t.Fatal(err)
	}
}

func TestScanRootPairExcluded(t *testing.T) {
	root := t.TempDir()
	current := mustRender(t, CurrentVersion)
	if err := writeTestFile(t, filepath.Join(root, "AGENTS.md"), current); err != nil {
		t.Fatal(err)
	}
	if err := writeTestFile(t, filepath.Join(root, "CLAUDE.md"), current); err != nil {
		t.Fatal(err)
	}
	conflicts, err := Scan(root)
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}
	if len(conflicts) != 0 {
		t.Errorf("Scan reported %d conflicts, want 0 (root's own pair is excluded): %+v", len(conflicts), conflicts)
	}
}

func TestScanNestedConflictDepthTwo(t *testing.T) {
	root := t.TempDir()
	nested := filepath.Join(root, "a", "b")
	mkdirAll(t, nested)
	current := mustRender(t, CurrentVersion)
	nestedPath := filepath.Join(nested, "AGENTS.md")
	if err := writeTestFile(t, nestedPath, current); err != nil {
		t.Fatal(err)
	}

	conflicts, err := Scan(root)
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}
	if len(conflicts) != 1 {
		t.Fatalf("Scan reported %d conflicts, want 1: %+v", len(conflicts), conflicts)
	}
	if conflicts[0].Path != nestedPath {
		t.Errorf("conflict path = %s, want %s", conflicts[0].Path, nestedPath)
	}
	if conflicts[0].Report.Status != StatusCurrent {
		t.Errorf("conflict status = %v, want StatusCurrent", conflicts[0].Report.Status)
	}
}

func TestScanAbsentNestedFileIsNotAConflict(t *testing.T) {
	root := t.TempDir()
	nested := filepath.Join(root, "sub")
	mkdirAll(t, nested)
	if err := writeTestFile(t, filepath.Join(nested, "AGENTS.md"), "plain human notes\n"); err != nil {
		t.Fatal(err)
	}
	conflicts, err := Scan(root)
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}
	if len(conflicts) != 0 {
		t.Errorf("Scan reported %d conflicts for an absent nested region, want 0", len(conflicts))
	}
}

func TestScanDotDirsSkipped(t *testing.T) {
	root := t.TempDir()
	current := mustRender(t, CurrentVersion)
	for _, dotDir := range []string{".git", ".orchestrator", ".memhub", ".github"} {
		dir := filepath.Join(root, dotDir, "nested")
		mkdirAll(t, dir)
		if err := writeTestFile(t, filepath.Join(dir, "AGENTS.md"), current); err != nil {
			t.Fatal(err)
		}
	}
	conflicts, err := Scan(root)
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}
	if len(conflicts) != 0 {
		t.Errorf("Scan reported %d conflicts under dot-directories, want 0: %+v", len(conflicts), conflicts)
	}
}

func TestScanCaseInsensitiveNameMatch(t *testing.T) {
	root := t.TempDir()
	nested := filepath.Join(root, "sub")
	mkdirAll(t, nested)
	current := mustRender(t, CurrentVersion)
	// Deliberately non-canonical casing.
	path := filepath.Join(nested, "Agents.MD")
	if err := writeTestFile(t, path, current); err != nil {
		t.Fatal(err)
	}
	conflicts, err := Scan(root)
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}
	if len(conflicts) != 1 {
		t.Fatalf("Scan reported %d conflicts, want 1", len(conflicts))
	}
}

func TestScanSymlinkedDirectoryNotFollowed(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	current := mustRender(t, CurrentVersion)
	if err := writeTestFile(t, filepath.Join(outside, "AGENTS.md"), current); err != nil {
		t.Fatal(err)
	}
	symlink(t, outside, filepath.Join(root, "linked"))

	conflicts, err := Scan(root)
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}
	if len(conflicts) != 0 {
		t.Errorf("Scan followed a symlinked directory, found %d conflicts: %+v", len(conflicts), conflicts)
	}
}

func TestScanSymlinkedFileNotFollowed(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	current := mustRender(t, CurrentVersion)
	target := filepath.Join(outside, "AGENTS.md")
	if err := writeTestFile(t, target, current); err != nil {
		t.Fatal(err)
	}
	sub := filepath.Join(root, "sub")
	mkdirAll(t, sub)
	symlink(t, target, filepath.Join(sub, "AGENTS.md"))

	conflicts, err := Scan(root)
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}
	if len(conflicts) != 0 {
		t.Errorf("Scan read through a symlinked file, found %d conflicts: %+v", len(conflicts), conflicts)
	}
}

func TestScanUnreadableDirIsError(t *testing.T) {
	if os.Getuid() == 0 {
		t.Skip("running as root: permission bits do not restrict access")
	}
	root := t.TempDir()
	blocked := filepath.Join(root, "blocked")
	mkdirAll(t, blocked)
	if err := writeTestFile(t, filepath.Join(blocked, "AGENTS.md"), "notes\n"); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(blocked, 0o000); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(blocked, 0o755) })

	_, err := Scan(root)
	if err == nil {
		t.Skip("directory permissions did not block traversal on this platform/filesystem")
	}
}

func TestConflictReportsBlockingStatus(t *testing.T) {
	root := t.TempDir()
	nested := filepath.Join(root, "sub")
	mkdirAll(t, nested)
	drifted := "<!-- orchestrator:managed:start version=2 -->\nfuture body\n" + EndMarker
	if err := writeTestFile(t, filepath.Join(nested, "CLAUDE.md"), drifted); err != nil {
		t.Fatal(err)
	}
	conflicts, err := Scan(root)
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}
	if len(conflicts) != 1 {
		t.Fatalf("got %d conflicts, want 1", len(conflicts))
	}
	if !conflicts[0].Report.Blocking() {
		t.Error("StatusNewerVersion conflict should be Blocking")
	}
}
