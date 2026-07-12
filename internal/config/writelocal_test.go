package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestWriteLocalWritesAndOverwrites(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, ".orchestrator"), 0o755); err != nil {
		t.Fatal(err)
	}

	if err := WriteLocal(root, []byte("first\n")); err != nil {
		t.Fatalf("WriteLocal: %v", err)
	}
	got, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(LocalOverridePath)))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "first\n" {
		t.Errorf("content = %q, want %q", got, "first\n")
	}

	if err := WriteLocal(root, []byte("second\n")); err != nil {
		t.Fatalf("WriteLocal (overwrite): %v", err)
	}
	got, err = os.ReadFile(filepath.Join(root, filepath.FromSlash(LocalOverridePath)))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "second\n" {
		t.Errorf("content after overwrite = %q, want %q", got, "second\n")
	}
}

func TestRemoveLocalIdempotent(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, ".orchestrator"), 0o755); err != nil {
		t.Fatal(err)
	}

	if err := RemoveLocal(root); err != nil {
		t.Fatalf("RemoveLocal on a never-created file: %v", err)
	}

	if err := WriteLocal(root, []byte("x\n")); err != nil {
		t.Fatal(err)
	}
	if err := RemoveLocal(root); err != nil {
		t.Fatalf("RemoveLocal: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, filepath.FromSlash(LocalOverridePath))); !os.IsNotExist(err) {
		t.Errorf("file still exists after RemoveLocal (err = %v)", err)
	}

	if err := RemoveLocal(root); err != nil {
		t.Fatalf("RemoveLocal a second time (idempotent): %v", err)
	}
}
