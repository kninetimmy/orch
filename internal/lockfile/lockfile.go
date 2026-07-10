// Package lockfile implements the exclusive cross-host Delivery lock
// (PRD §14): one active Delivery coordinator per repository across both
// hosts. The lock is the existence of .orchestrator/delivery.lock,
// created with O_EXCL so acquisition is atomic on every supported OS.
//
// The lock deliberately outlives the acquiring process: a Delivery run
// spans many short-lived orch invocations plus a host agent session, so
// the recorded PID is advisory and its death is never treated as
// staleness. There is no automatic takeover; recovery is always the
// explicit human action `orch abort` (PRD §15). A lock file that exists
// but cannot be read still denies acquisition (fail closed).
package lockfile

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"time"
)

// Path is the repo-relative location of the Delivery lock file.
const Path = ".orchestrator/delivery.lock"

// SchemaVersion is the lock-file schema this build reads and writes.
const SchemaVersion = 1

// ErrHeld reports that the Delivery lock is already held. Callers test
// for it with errors.Is.
var ErrHeld = errors.New("delivery lock is already held")

// Owner identifies who acquired the lock. Hostname and PID exist for
// audit and diagnostics only: the acquiring process is expected to exit
// while its Delivery run legitimately continues.
type Owner struct {
	SchemaVersion int       `json:"schema_version"`
	RunID         string    `json:"run_id"`
	Host          string    `json:"host"` // "claude" or "codex"
	Hostname      string    `json:"hostname"`
	PID           int       `json:"pid"`
	AcquiredAt    time.Time `json:"acquired_at"`
}

func lockPath(repoRoot string) string {
	return filepath.Join(repoRoot, filepath.FromSlash(Path))
}

// Acquire atomically creates the lock file recording o. If the lock
// already exists — even unreadable — it returns an error wrapping
// ErrHeld and changes nothing.
func Acquire(repoRoot string, o Owner) error {
	f, err := os.OpenFile(lockPath(repoRoot), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if errors.Is(err, fs.ErrExist) {
		return heldError(repoRoot)
	}
	if err != nil {
		return fmt.Errorf("create %s: %w", Path, err)
	}
	o.SchemaVersion = SchemaVersion
	data, err := json.MarshalIndent(o, "", "  ")
	if err == nil {
		_, err = f.Write(append(data, '\n'))
	}
	if err == nil {
		err = f.Sync()
	}
	if closeErr := f.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		_ = os.Remove(lockPath(repoRoot)) // best effort; a leftover corrupt lock still fails closed
		return fmt.Errorf("write %s: %w", Path, err)
	}
	return nil
}

func heldError(repoRoot string) error {
	cur, err := Inspect(repoRoot)
	if err != nil {
		return fmt.Errorf("%w and unreadable (%v); run `orch abort` to clear it", ErrHeld, err)
	}
	if cur == nil {
		// The holder released between our failed create and this read.
		return fmt.Errorf("%w (released concurrently; retry)", ErrHeld)
	}
	return fmt.Errorf("%w by %s on %s (pid %d, acquired %s)",
		ErrHeld, cur.Host, cur.Hostname, cur.PID, cur.AcquiredAt.Format(time.RFC3339))
}

// Inspect returns the current owner, (nil, nil) when no lock exists, or
// an error when the lock exists but cannot be trusted. Callers must
// treat an error as "cannot verify, deny" (fail closed).
func Inspect(repoRoot string) (*Owner, error) {
	data, err := os.ReadFile(lockPath(repoRoot))
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", Path, err)
	}
	var o Owner
	if err := json.Unmarshal(data, &o); err != nil {
		return nil, fmt.Errorf("parse %s: %v (run `orch abort` to clear a broken lock)", Path, err)
	}
	if o.SchemaVersion != SchemaVersion {
		return nil, fmt.Errorf("%s: unsupported schema_version %d (this build understands %d)", Path, o.SchemaVersion, SchemaVersion)
	}
	return &o, nil
}

// Release removes the lock file. A missing lock is not an error, so
// Release is idempotent.
func Release(repoRoot string) error {
	err := os.Remove(lockPath(repoRoot))
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("remove %s: %w", Path, err)
	}
	return nil
}
