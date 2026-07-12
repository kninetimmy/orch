package bootstrap

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/kninetimmy/orch/internal/config"
	"github.com/kninetimmy/orch/internal/question"
)

// writeFiles writes complete's materialized artifacts into the
// worktree at dir, verbatim and LF (PRD §18 step 12): the committed
// configuration, every proposed instruction-file change, and the
// .gitignore additions.
func writeFiles(dir string, complete *question.Complete) error {
	cfgPath := filepath.Join(dir, filepath.FromSlash(config.Path))
	if err := os.MkdirAll(filepath.Dir(cfgPath), 0o755); err != nil {
		return fmt.Errorf("create directory for %s: %w", config.Path, err)
	}
	if err := os.WriteFile(cfgPath, []byte(complete.Summary.ConfigTOML), 0o644); err != nil {
		return fmt.Errorf("write %s: %w", config.Path, err)
	}

	for _, f := range complete.Summary.Files {
		path := filepath.Join(dir, filepath.FromSlash(f.Path))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return fmt.Errorf("create directory for %s: %w", f.Path, err)
		}
		if err := os.WriteFile(path, []byte(f.NewContent), 0o644); err != nil {
			return fmt.Errorf("write %s: %w", f.Path, err)
		}
	}

	return appendGitignore(dir, complete.Summary.GitignoreLines)
}

// appendGitignore appends lines to dir's .gitignore, creating the file
// if absent and adding a trailing newline first if the existing file
// lacks one — every line ends up on its own line, LF only. A nil or
// empty lines is a no-op: it leaves an absent .gitignore absent rather
// than create an empty one.
func appendGitignore(dir string, lines []string) error {
	if len(lines) == 0 {
		return nil
	}
	path := filepath.Join(dir, ".gitignore")
	data, err := os.ReadFile(path)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("read .gitignore: %w", err)
	}

	var b strings.Builder
	b.Write(data)
	if len(data) > 0 && !strings.HasSuffix(string(data), "\n") {
		b.WriteByte('\n')
	}
	for _, l := range lines {
		b.WriteString(l)
		b.WriteByte('\n')
	}
	if err := os.WriteFile(path, []byte(b.String()), 0o644); err != nil {
		return fmt.Errorf("write .gitignore: %w", err)
	}
	return nil
}
