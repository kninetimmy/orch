package config

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
)

// WriteLocal atomically writes data to repoRoot's config.local.toml: a
// temp file in the same directory, synced, then renamed over the
// destination (internal/state.write's shape, which also replaces
// atomically on Windows). On any failure the existing file, if any, is
// left untouched and the temp file is removed on a best-effort basis.
func WriteLocal(repoRoot string, data []byte) error {
	path := filepath.Join(repoRoot, filepath.FromSlash(LocalOverridePath))
	dir := filepath.Dir(path)
	f, err := os.CreateTemp(dir, "config.local-*.tmp")
	if err != nil {
		return fmt.Errorf("write %s: %w", LocalOverridePath, err)
	}
	tmp := f.Name()
	_, err = f.Write(data)
	if err == nil {
		err = f.Sync()
	}
	if closeErr := f.Close(); err == nil {
		err = closeErr
	}
	if err == nil {
		err = os.Rename(tmp, path)
	}
	if err != nil {
		_ = os.Remove(tmp) // best effort; the real file is untouched
		return fmt.Errorf("write %s: %w", LocalOverridePath, err)
	}
	return nil
}

// RemoveLocal deletes repoRoot's config.local.toml. A missing file is
// not an error, so RemoveLocal is idempotent — a re-applied "clear
// every override" apply must succeed even if a prior attempt already
// removed the file.
func RemoveLocal(repoRoot string) error {
	path := filepath.Join(repoRoot, filepath.FromSlash(LocalOverridePath))
	err := os.Remove(path)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("remove %s: %w", LocalOverridePath, err)
	}
	return nil
}
