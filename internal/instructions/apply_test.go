package instructions

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestApplyInstall(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "AGENTS.md")
	if err := writeTestFile(t, path, "human notes\n"); err != nil {
		t.Fatal(err)
	}
	ch, err := PlanFile(path)
	if err != nil {
		t.Fatalf("PlanFile: %v", err)
	}
	if err := Apply(path, ch); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != ch.New {
		t.Errorf("file content = %q, want %q", got, ch.New)
	}
}

func TestApplyNoopDoesNotWrite(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "AGENTS.md")
	current := mustRender(t, CurrentVersion)
	if err := writeTestFile(t, path, current); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	modBefore := info.ModTime()

	ch := mustPlan(t, current)
	if ch.Action != ActionNone {
		t.Fatalf("Action = %v, want ActionNone", ch.Action)
	}
	if err := Apply(path, ch); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	info, err = os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if !info.ModTime().Equal(modBefore) {
		t.Error("Apply rewrote a file with Old == New")
	}
}

// TestWriteAtomicFailureNoTargetCreated points the temp-file directory
// at a location that cannot hold one (a file, not a directory) so
// os.CreateTemp fails immediately, and asserts the target is never
// created.
func TestWriteAtomicFailureNoTargetCreated(t *testing.T) {
	dir := t.TempDir()
	notADir := filepath.Join(dir, "not-a-dir")
	if err := writeTestFile(t, notADir, ""); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(notADir, "AGENTS.md")

	// target's directory (notADir) is actually a file, so
	// os.CreateTemp(notADir, ...) must fail before writing anything.
	if err := write(target, []byte("new content")); err == nil {
		t.Fatal("write() succeeded against a directory that is actually a file")
	}
	if _, statErr := os.Stat(target); statErr == nil {
		t.Error("write() created a file despite failing")
	}
}

// TestWriteAtomicFailurePreservesExistingFile forces the rename step
// to fail (the destination is a directory, not a file) and asserts
// the pre-existing directory survives untouched with no leftover temp
// file.
func TestWriteAtomicFailurePreservesExistingFile(t *testing.T) {
	dir := t.TempDir()
	// path is a directory, not a file: write() should fail when it
	// tries to rename its temp file over a directory.
	path := filepath.Join(dir, "AGENTS.md")
	if err := os.Mkdir(path, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := write(path, []byte("new content")); err == nil {
		t.Fatal("write() succeeded renaming over a directory")
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if !info.IsDir() {
		t.Error("write() replaced the directory")
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if strings.Contains(e.Name(), ".tmp") {
			t.Errorf("leftover temp file after a failed write: %s", e.Name())
		}
	}
}

// TestApplyRemoveMatrix exercises ApplyRemove's confirmed/unconfirmed
// x DeleteWholeFile true/false x ActionNone matrix.
func TestApplyRemoveMatrix(t *testing.T) {
	current := mustRender(t, CurrentVersion)

	t.Run("ActionNone is a no-op regardless of confirmation", func(t *testing.T) {
		for _, confirm := range []Confirmation{{}, ExplicitConfirmation()} {
			dir := t.TempDir()
			path := filepath.Join(dir, "AGENTS.md")
			content := "human notes\n"
			if err := writeTestFile(t, path, content); err != nil {
				t.Fatal(err)
			}
			ch, err := PlanRemoveFile(path)
			if err != nil {
				t.Fatalf("PlanRemoveFile: %v", err)
			}
			if ch.Action != ActionNone {
				t.Fatalf("Action = %v, want ActionNone", ch.Action)
			}
			result, err := ApplyRemove(path, ch, confirm)
			if err != nil {
				t.Fatalf("ApplyRemove: %v", err)
			}
			if result != RemovedNothing {
				t.Errorf("result = %v, want RemovedNothing", result)
			}
			got, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if string(got) != content {
				t.Errorf("file content changed: %q", got)
			}
		}
	})

	t.Run("DeleteWholeFile true, confirmed: file removed", func(t *testing.T) {
		dir := t.TempDir()
		path := filepath.Join(dir, "AGENTS.md")
		if err := writeTestFile(t, path, current); err != nil {
			t.Fatal(err)
		}
		ch, err := PlanRemoveFile(path)
		if err != nil {
			t.Fatalf("PlanRemoveFile: %v", err)
		}
		if !ch.DeleteWholeFile {
			t.Fatal("test setup: expected DeleteWholeFile")
		}
		result, err := ApplyRemove(path, ch, ExplicitConfirmation())
		if err != nil {
			t.Fatalf("ApplyRemove: %v", err)
		}
		if result != RemovedWholeFile {
			t.Errorf("result = %v, want RemovedWholeFile", result)
		}
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Errorf("file still exists after RemovedWholeFile: %v", err)
		}
	})

	t.Run("DeleteWholeFile true, unconfirmed: soft-degrades to block-only", func(t *testing.T) {
		dir := t.TempDir()
		path := filepath.Join(dir, "AGENTS.md")
		if err := writeTestFile(t, path, current); err != nil {
			t.Fatal(err)
		}
		ch, err := PlanRemoveFile(path)
		if err != nil {
			t.Fatalf("PlanRemoveFile: %v", err)
		}
		result, err := ApplyRemove(path, ch, Confirmation{})
		if err != nil {
			t.Fatalf("ApplyRemove: %v", err)
		}
		if result != RemovedBlockOnly {
			t.Errorf("result = %v, want RemovedBlockOnly", result)
		}
		got, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("file was removed despite no confirmation: %v", err)
		}
		if string(got) != ch.New {
			t.Errorf("file content = %q, want %q", got, ch.New)
		}
	})

	t.Run("DeleteWholeFile false: block-only removal regardless of confirmation", func(t *testing.T) {
		for _, confirm := range []Confirmation{{}, ExplicitConfirmation()} {
			dir := t.TempDir()
			path := filepath.Join(dir, "AGENTS.md")
			body := "intro\n\n" + current + "\n\noutro\n"
			if err := writeTestFile(t, path, body); err != nil {
				t.Fatal(err)
			}
			ch, err := PlanRemoveFile(path)
			if err != nil {
				t.Fatalf("PlanRemoveFile: %v", err)
			}
			if ch.DeleteWholeFile {
				t.Fatal("test setup: expected DeleteWholeFile == false")
			}
			result, err := ApplyRemove(path, ch, confirm)
			if err != nil {
				t.Fatalf("ApplyRemove: %v", err)
			}
			if result != RemovedBlockOnly {
				t.Errorf("result = %v, want RemovedBlockOnly", result)
			}
			got, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if string(got) != ch.New {
				t.Errorf("file content = %q, want %q", got, ch.New)
			}
		}
	})
}

func TestRemoveConvenience(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "CLAUDE.md")
	current := mustRender(t, CurrentVersion)
	if err := writeTestFile(t, path, current); err != nil {
		t.Fatal(err)
	}
	ch, result, err := Remove(path, ExplicitConfirmation())
	if err != nil {
		t.Fatalf("Remove: %v", err)
	}
	if ch.Action != ActionRemove {
		t.Errorf("Action = %v, want ActionRemove", ch.Action)
	}
	if result != RemovedWholeFile {
		t.Errorf("result = %v, want RemovedWholeFile", result)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Errorf("file still exists: %v", err)
	}
}

func TestConfirmationZeroValueFailsClosed(t *testing.T) {
	var c Confirmation
	dir := t.TempDir()
	path := filepath.Join(dir, "AGENTS.md")
	current := mustRender(t, CurrentVersion)
	if err := writeTestFile(t, path, current); err != nil {
		t.Fatal(err)
	}
	ch, err := PlanRemoveFile(path)
	if err != nil {
		t.Fatalf("PlanRemoveFile: %v", err)
	}
	result, err := ApplyRemove(path, ch, c)
	if err != nil {
		t.Fatalf("ApplyRemove: %v", err)
	}
	if result == RemovedWholeFile {
		t.Error("zero-value Confirmation authorized a whole-file delete")
	}
}
