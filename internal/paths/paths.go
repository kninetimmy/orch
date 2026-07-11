// Package paths provides the safe-path and containment primitives
// (PRD §6, §15) the orchestration core builds on: canonical absolute
// paths with symlinks resolved, segment-aware containment checks for
// worktree enforcement, and discovery of the repository root that
// holds .orchestrator/. Every helper fails closed: a path that cannot
// be verified yields an error, never a permissive default.
package paths

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// OrchestratorDir is the name of the repo-root directory Orch owns.
const OrchestratorDir = ".orchestrator"

// ErrNotFound reports that no ancestor directory contains
// .orchestrator/. Callers test for it with errors.Is.
var ErrNotFound = errors.New("no " + OrchestratorDir + " directory found")

// foldCase reports whether path comparison ignores case on this OS.
// Windows and macOS default filesystems are case-insensitive;
// per-volume overrides (case-sensitive APFS, case-insensitive ext4)
// are not detected.
var foldCase = runtime.GOOS == "windows" || runtime.GOOS == "darwin"

// Canonical returns path as an absolute, symlink-resolved, cleaned
// path. The target need not exist: the deepest existing ancestor is
// resolved and the remaining components are appended lexically, so a
// write destination can be checked before anything creates it. Any
// failure other than non-existence (for example permission denied)
// is an error so callers fail closed.
func Canonical(path string) (string, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", fmt.Errorf("canonicalize %s: %w", path, err)
	}
	// Abs cleans the path, so the components popped into rest below
	// never contain "." or "..".
	dir := abs
	var rest []string
	for {
		resolved, err := filepath.EvalSymlinks(dir)
		if err == nil {
			for i := len(rest) - 1; i >= 0; i-- {
				resolved = filepath.Join(resolved, rest[i])
			}
			return filepath.Clean(resolved), nil
		}
		if !errors.Is(err, fs.ErrNotExist) {
			return "", fmt.Errorf("canonicalize %s: %w", path, err)
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			// Even the volume root failed to resolve.
			return "", fmt.Errorf("canonicalize %s: %w", path, err)
		}
		rest = append(rest, filepath.Base(dir))
		dir = parent
	}
}

// Inside reports whether path is contained in root; path equal to
// root counts as inside. Both arguments are canonicalized first, so
// a symlink cannot smuggle a path out of root and ".." segments
// cannot fake containment. The comparison is segment-aware: /repo-2
// is not inside /repo. An unverifiable path is an error, and callers
// must treat it as "deny".
func Inside(root, path string) (bool, error) {
	canonRoot, err := Canonical(root)
	if err != nil {
		return false, err
	}
	canonPath, err := Canonical(path)
	if err != nil {
		return false, err
	}
	return inside(canonRoot, canonPath, foldCase), nil
}

// inside compares two canonical paths. Case folding uses ToLower as
// an approximation of the Windows/macOS folding rules; both sides
// fold identically so containment stays consistent.
func inside(root, path string, fold bool) bool {
	if fold {
		root = strings.ToLower(root)
		path = strings.ToLower(path)
	}
	rel, err := filepath.Rel(root, path)
	if err != nil {
		// Different volumes or otherwise incomparable: not inside.
		return false
	}
	if rel == "." {
		return true
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

// FindRoot walks from startDir toward the filesystem root and returns
// the first directory containing an .orchestrator directory. It
// returns an error wrapping ErrNotFound when no ancestor qualifies,
// and fails closed on anything it cannot trust: an unreadable
// ancestor, or an .orchestrator entry that is not a real directory
// (a symlink does not count).
func FindRoot(startDir string) (string, error) {
	dir, err := Canonical(startDir)
	if err != nil {
		return "", err
	}
	for {
		candidate := filepath.Join(dir, OrchestratorDir)
		info, err := os.Lstat(candidate)
		switch {
		case err == nil && info.IsDir():
			return dir, nil
		case err == nil:
			return "", fmt.Errorf("%s exists but is not a real directory", candidate)
		case !errors.Is(err, fs.ErrNotExist):
			return "", fmt.Errorf("find repo root: %w", err)
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", fmt.Errorf("%w in %s or any parent", ErrNotFound, startDir)
		}
		dir = parent
	}
}

// FindOutermostRoot walks from startDir to the filesystem root and
// returns the outermost ancestor that contains an .orchestrator
// directory — the opposite selection from FindRoot, which stops at the
// innermost. The guard (PRD §23) needs the outermost hit: a Delivery
// worktree checkout carries the committed .orchestrator/config.toml, so
// an innermost search would resolve a path under
// <root>/.orchestrator/worktrees/issue-N/ to the worktree itself, whose
// missing machine-local state.json would read as Assist and wrongly deny
// every executor write. The same fail-closed rules as FindRoot apply: an
// unreadable ancestor, or an .orchestrator entry that is not a real
// directory (a symlink does not count), is an error; ErrNotFound wraps
// when no ancestor qualifies.
func FindOutermostRoot(startDir string) (string, error) {
	dir, err := Canonical(startDir)
	if err != nil {
		return "", err
	}
	outermost := ""
	for {
		candidate := filepath.Join(dir, OrchestratorDir)
		info, err := os.Lstat(candidate)
		switch {
		case err == nil && info.IsDir():
			outermost = dir
		case err == nil:
			return "", fmt.Errorf("%s exists but is not a real directory", candidate)
		case !errors.Is(err, fs.ErrNotExist):
			return "", fmt.Errorf("find repo root: %w", err)
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	if outermost == "" {
		return "", fmt.Errorf("%w in %s or any parent", ErrNotFound, startDir)
	}
	return outermost, nil
}
