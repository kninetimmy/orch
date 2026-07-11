package instructions

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// Conflict is a nested AGENTS.md/CLAUDE.md file — anywhere under root
// other than root's own direct pair — whose managed region is not
// StatusAbsent. Scan reports conflicts; it never edits them.
type Conflict struct {
	Path   string
	Report Report
}

// targetNames are the file base names Scan inspects, matched
// case-insensitively.
var targetNames = []string{"AGENTS.md", "CLAUDE.md"}

// Scan walks root looking for AGENTS.md/CLAUDE.md files whose managed
// region is not absent, outside of root's own direct pair (those are
// the files the caller manages directly — they are not "nested"
// conflicts). It skips every directory whose base name starts with
// "." (covers .git, .orchestrator, .memhub, .github uniformly) except
// root itself, which is never skipped regardless of its own name.
// filepath.WalkDir never follows symlinks on its own; Scan reinforces
// that by never opening a symlinked entry, directory or file, through
// its target. Any directory or file Scan cannot read is a hard error
// naming the path — a nested conflict that silently went unseen would
// be worse than failing loudly.
func Scan(root string) ([]Conflict, error) {
	root = filepath.Clean(root)
	var conflicts []Conflict
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return fmt.Errorf("scan %s: %w", path, err)
		}
		if d.IsDir() {
			if path != root && strings.HasPrefix(d.Name(), ".") {
				return filepath.SkipDir
			}
			return nil
		}
		// WalkDir reports a symlink (to a file or a directory) as a
		// non-directory leaf regardless of its target, and never
		// descends into it on its own. Skip it here without opening
		// it, so a symlinked file is never read through and a
		// symlinked directory is never traversed.
		if d.Type()&fs.ModeSymlink != 0 {
			return nil
		}
		if !isTargetName(d.Name()) {
			return nil
		}
		if isRootPair(root, path) {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return fmt.Errorf("scan %s: %w", path, err)
		}
		if report := Inspect(string(data)); report.Status != StatusAbsent {
			conflicts = append(conflicts, Conflict{Path: path, Report: report})
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return conflicts, nil
}

// isTargetName reports whether name case-insensitively matches one of
// targetNames.
func isTargetName(name string) bool {
	for _, n := range targetNames {
		if strings.EqualFold(name, n) {
			return true
		}
	}
	return false
}

// isRootPair reports whether path is a direct child of root — root's
// own AGENTS.md/CLAUDE.md pair, excluded from nested-conflict
// reporting.
func isRootPair(root, path string) bool {
	return filepath.Dir(path) == root
}
