package bootstrap

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/kninetimmy/orch/internal/config"
	"github.com/kninetimmy/orch/internal/instructions"
	"github.com/kninetimmy/orch/internal/question"
)

// validateInstallation runs the §18.13 mechanical validation inside
// the freshly written worktree at dir, recording one ValidationEntry
// per check, stamped with now: config.Load succeeds (the local
// overlay is naturally absent in a fresh worktree), the recomputed
// config.Revision matches Summary.ConfigRevision, every written
// instruction file classifies as instructions.StatusCurrent, and
// every proposed .gitignore line is present in the worktree's
// .gitignore. It returns on the first failing check — every entry up
// to and including the failure is returned alongside the wrapped
// ErrValidationFailed, so the caller can still render what passed.
func validateInstallation(dir string, complete *question.Complete, now time.Time) ([]ValidationEntry, error) {
	stamp := now.UTC().Format(time.RFC3339)
	var entries []ValidationEntry

	if _, err := config.Load(dir); err != nil {
		entries = append(entries, ValidationEntry{Name: "config.Load", Result: "fail", Detail: err.Error(), At: stamp})
		return entries, fmt.Errorf("%w: config.Load: %v", ErrValidationFailed, err)
	}
	entries = append(entries, ValidationEntry{Name: "config.Load", Result: "pass", At: stamp})

	cfg, err := config.Parse([]byte(complete.Summary.ConfigTOML))
	if err != nil {
		// Unreachable in practice: materialize already round-trips this
		// exact TOML through Parse before Next ever emits it.
		return entries, fmt.Errorf("%w: re-parse rendered configuration: %v", ErrValidationFailed, err)
	}
	rev, err := config.Revision(cfg)
	if err != nil {
		return entries, fmt.Errorf("%w: recompute configuration revision: %v", ErrValidationFailed, err)
	}
	if rev != complete.Summary.ConfigRevision {
		entries = append(entries, ValidationEntry{Name: "config.Revision", Result: "fail", Detail: fmt.Sprintf("recomputed %s, want %s", rev, complete.Summary.ConfigRevision), At: stamp})
		return entries, fmt.Errorf("%w: config.Revision recomputed %s, want %s", ErrValidationFailed, rev, complete.Summary.ConfigRevision)
	}
	entries = append(entries, ValidationEntry{Name: "config.Revision", Result: "pass", At: stamp})

	for _, f := range complete.Summary.Files {
		name := "instructions:" + f.Path
		path := filepath.Join(dir, filepath.FromSlash(f.Path))
		report, err := instructions.InspectFile(path)
		if err != nil {
			entries = append(entries, ValidationEntry{Name: name, Result: "fail", Detail: err.Error(), At: stamp})
			return entries, fmt.Errorf("%w: %s: %v", ErrValidationFailed, name, err)
		}
		if report.Status != instructions.StatusCurrent {
			detail := report.Detail
			if detail == "" {
				detail = fmt.Sprintf("status %d, want current", report.Status)
			}
			entries = append(entries, ValidationEntry{Name: name, Result: "fail", Detail: detail, At: stamp})
			return entries, fmt.Errorf("%w: %s is not current: %s", ErrValidationFailed, name, detail)
		}
		entries = append(entries, ValidationEntry{Name: name, Result: "pass", At: stamp})
	}

	gitignoreData, err := os.ReadFile(filepath.Join(dir, ".gitignore"))
	if err != nil && !os.IsNotExist(err) {
		entries = append(entries, ValidationEntry{Name: "gitignore", Result: "fail", Detail: err.Error(), At: stamp})
		return entries, fmt.Errorf("%w: gitignore: %v", ErrValidationFailed, err)
	}
	missing := missingLines(string(gitignoreData), complete.Summary.GitignoreLines)
	if len(missing) > 0 {
		detail := "missing: " + strings.Join(missing, ", ")
		entries = append(entries, ValidationEntry{Name: "gitignore", Result: "fail", Detail: detail, At: stamp})
		return entries, fmt.Errorf("%w: gitignore is missing %s", ErrValidationFailed, strings.Join(missing, ", "))
	}
	entries = append(entries, ValidationEntry{Name: "gitignore", Result: "pass", At: stamp})

	return entries, nil
}

// missingLines returns the entries of want not present as an exact
// line (CR trimmed) in content.
func missingLines(content string, want []string) []string {
	present := map[string]bool{}
	for _, l := range strings.Split(content, "\n") {
		present[strings.TrimRight(l, "\r")] = true
	}
	var missing []string
	for _, l := range want {
		if !present[l] {
			missing = append(missing, l)
		}
	}
	return missing
}
