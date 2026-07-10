package config

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/BurntSushi/toml"
)

// Path is the repo-relative location of the committed configuration.
const Path = ".orchestrator/config.toml"

// LocalOverridePath is the repo-relative location of the machine-local
// override file. It is detected but not yet applied (PRD §17).
const LocalOverridePath = ".orchestrator/config.local.toml"

// ErrNotInitialized reports a missing committed configuration file.
var ErrNotInitialized = errors.New("not initialized: " + Path + " not found (orch init is not yet implemented)")

// Load reads, parses, and validates the committed configuration under
// repoRoot. It never returns a partial Config: on any parse or
// validation failure the returned Config is nil.
func Load(repoRoot string) (*Config, error) {
	data, err := os.ReadFile(filepath.Join(repoRoot, filepath.FromSlash(Path)))
	if errors.Is(err, fs.ErrNotExist) {
		return nil, ErrNotInitialized
	}
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", Path, err)
	}

	var cfg Config
	md, err := toml.Decode(string(data), &cfg)
	if err != nil {
		return nil, fmt.Errorf("parse %s: %w", Path, err)
	}
	if undecoded := md.Undecoded(); len(undecoded) > 0 {
		keys := make([]string, len(undecoded))
		for i, k := range undecoded {
			keys[i] = k.String()
		}
		return nil, fmt.Errorf("%s: unknown keys: %s", Path, strings.Join(keys, ", "))
	}

	applyDefaults(&cfg, md)
	if err := cfg.validate(); err != nil {
		return nil, fmt.Errorf("%s: %w", Path, err)
	}
	return &cfg, nil
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
