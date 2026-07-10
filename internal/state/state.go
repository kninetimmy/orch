// Package state persists the Assist/Delivery operating mode (PRD §7)
// in .orchestrator/state.json, machine-local and gitignored. A missing
// file means Assist; a file that cannot be read, parsed, or recognized
// is an error so callers fail closed (PRD §15).
package state

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"time"
)

// Path is the repo-relative location of the state file.
const Path = ".orchestrator/state.json"

// SchemaVersion is the state-file schema this build reads and writes.
const SchemaVersion = 1

// Mode is the operating mode (PRD §7).
type Mode string

const (
	ModeAssist   Mode = "assist"
	ModeDelivery Mode = "delivery"
)

// Run describes the active Delivery run.
type Run struct {
	ID        string    `json:"id"`
	Host      string    `json:"host"` // "claude" or "codex"
	StartedAt time.Time `json:"started_at"`
}

// State is the persisted operating state.
type State struct {
	SchemaVersion int       `json:"schema_version"`
	Mode          Mode      `json:"mode"`
	Run           *Run      `json:"run,omitempty"` // non-nil iff Mode is delivery
	UpdatedAt     time.Time `json:"updated_at"`
}

func statePath(repoRoot string) string {
	return filepath.Join(repoRoot, filepath.FromSlash(Path))
}

// Load reads the state under repoRoot. A missing file is Assist, not an
// error. Anything unreadable or inconsistent is an error naming the
// file so the caller can deny and the human can act.
func Load(repoRoot string) (*State, error) {
	data, err := os.ReadFile(statePath(repoRoot))
	if errors.Is(err, fs.ErrNotExist) {
		return &State{SchemaVersion: SchemaVersion, Mode: ModeAssist}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", Path, err)
	}

	var st State
	if err := json.Unmarshal(data, &st); err != nil {
		return nil, fmt.Errorf("parse %s: %v (run `orch abort` to reset to assist)", Path, err)
	}
	if st.SchemaVersion != SchemaVersion {
		return nil, fmt.Errorf("%s: unsupported schema_version %d (this build understands %d)", Path, st.SchemaVersion, SchemaVersion)
	}
	switch st.Mode {
	case ModeAssist:
		if st.Run != nil {
			return nil, fmt.Errorf("%s: assist mode with a recorded run (run `orch abort` to reset)", Path)
		}
	case ModeDelivery:
		if st.Run == nil {
			return nil, fmt.Errorf("%s: delivery mode without a recorded run (run `orch abort` to reset)", Path)
		}
	default:
		return nil, fmt.Errorf("%s: unknown mode %q (run `orch abort` to reset)", Path, st.Mode)
	}
	return &st, nil
}

// write atomically replaces the state file: temp file in the same
// directory, sync, then rename (which replaces on Windows too).
func write(repoRoot string, st *State) error {
	data, err := json.MarshalIndent(st, "", "  ")
	if err != nil {
		return fmt.Errorf("encode %s: %w", Path, err)
	}
	dir := filepath.Dir(statePath(repoRoot))
	f, err := os.CreateTemp(dir, "state-*.tmp")
	if err != nil {
		return fmt.Errorf("write %s: %w", Path, err)
	}
	tmp := f.Name()
	_, err = f.Write(append(data, '\n'))
	if err == nil {
		err = f.Sync()
	}
	if closeErr := f.Close(); err == nil {
		err = closeErr
	}
	if err == nil {
		err = os.Rename(tmp, statePath(repoRoot))
	}
	if err != nil {
		_ = os.Remove(tmp) // best effort; the real state file is untouched
		return fmt.Errorf("write %s: %w", Path, err)
	}
	return nil
}
