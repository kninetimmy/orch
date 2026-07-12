package config

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/BurntSushi/toml"
)

// Path is the repo-relative location of the committed configuration.
const Path = ".orchestrator/config.toml"

// LocalOverridePath is the repo-relative location of the machine-local
// override file. When present, Load layers it onto the committed
// configuration, restricted to a closed set of preference keys
// (PRD §17; see overlay.go).
const LocalOverridePath = ".orchestrator/config.local.toml"

// ErrNotInitialized reports a missing committed configuration file.
var ErrNotInitialized = errors.New("not initialized: " + Path + " not found (run `orch init` to set up this repository)")

// Load reads, parses, and validates the committed configuration under
// repoRoot, then merges in any machine-local overlay from
// config.local.toml (PRD §17). It never returns a partial Config: on
// any parse or validation failure the returned Config is nil.
func Load(repoRoot string) (*Config, error) {
	committed, err := loadCommitted(repoRoot)
	if err != nil {
		return nil, err
	}
	return applyLocalOverride(repoRoot, committed)
}

// loadCommitted reads the committed configuration file under repoRoot
// and calls Parse on its bytes; it does not consult
// config.local.toml.
func loadCommitted(repoRoot string) (*Config, error) {
	data, err := os.ReadFile(filepath.Join(repoRoot, filepath.FromSlash(Path)))
	if errors.Is(err, fs.ErrNotExist) {
		return nil, ErrNotInitialized
	}
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", Path, err)
	}

	cfg, err := Parse(data)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", Path, err)
	}
	return cfg, nil
}

// applyDefaults fills PRD-specified defaults for keys the file omits.
// Only keys with an explicit PRD default get one; everything else must
// be stated in the file (fail closed).
func applyDefaults(cfg *Config, md toml.MetaData) {
	if !md.IsDefined("concurrency", "max_subagents") {
		cfg.Concurrency.MaxSubagents = 3
	}
	if !md.IsDefined("merge", "strategy") {
		cfg.Merge.Strategy = "squash"
	}
}

// HasLocalOverride reports whether the machine-local override file
// exists under repoRoot.
func HasLocalOverride(repoRoot string) bool {
	_, err := os.Stat(filepath.Join(repoRoot, filepath.FromSlash(LocalOverridePath)))
	return err == nil
}
