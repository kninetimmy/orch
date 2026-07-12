package bootstrap

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/kninetimmy/orch/internal/config"
	"github.com/kninetimmy/orch/internal/instructions"
	"github.com/kninetimmy/orch/internal/interview"
	"github.com/kninetimmy/orch/internal/question"
)

// checkConfigLoad runs the shared "config.Load succeeds" check
// validateInstallation and validateConfigure both start from: a fresh
// worktree's local overlay is naturally absent, so this proves the
// just-written committed file alone loads and validates. It returns
// the loaded *config.Config on success (validateConfigure's per-host
// check needs it), alongside a ValidationEntry describing the outcome
// and a non-nil error (already wrapping ErrValidationFailed) on
// failure.
func checkConfigLoad(dir, stamp string) (*config.Config, ValidationEntry, error) {
	cfg, err := config.Load(dir)
	if err != nil {
		return nil, ValidationEntry{Name: "config.Load", Result: "fail", Detail: err.Error(), At: stamp},
			fmt.Errorf("%w: config.Load: %v", ErrValidationFailed, err)
	}
	return cfg, ValidationEntry{Name: "config.Load", Result: "pass", At: stamp}, nil
}

// checkConfigRevision runs the shared "recomputed Revision matches
// Summary.ConfigRevision" check, re-parsing complete's own materialized
// ConfigTOML (never re-reading it from dir: this is the exact bytes
// materialize/materializeConfigure already round-tripped through
// config.Parse before the interview ever proposed them).
func checkConfigRevision(complete *question.Complete, stamp string) (ValidationEntry, error) {
	cfg, err := config.Parse([]byte(complete.Summary.ConfigTOML))
	if err != nil {
		// Unreachable in practice: see materialize's/materializeConfigure's
		// own round-trip self-check.
		return ValidationEntry{Name: "config.Revision", Result: "fail", Detail: err.Error(), At: stamp},
			fmt.Errorf("%w: re-parse rendered configuration: %v", ErrValidationFailed, err)
	}
	rev, err := config.Revision(cfg)
	if err != nil {
		return ValidationEntry{Name: "config.Revision", Result: "fail", Detail: err.Error(), At: stamp},
			fmt.Errorf("%w: recompute configuration revision: %v", ErrValidationFailed, err)
	}
	if rev != complete.Summary.ConfigRevision {
		detail := fmt.Sprintf("recomputed %s, want %s", rev, complete.Summary.ConfigRevision)
		return ValidationEntry{Name: "config.Revision", Result: "fail", Detail: detail, At: stamp},
			fmt.Errorf("%w: config.Revision %s", ErrValidationFailed, detail)
	}
	return ValidationEntry{Name: "config.Revision", Result: "pass", At: stamp}, nil
}

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

	_, loadEntry, err := checkConfigLoad(dir, stamp)
	entries = append(entries, loadEntry)
	if err != nil {
		return entries, err
	}

	revEntry, err := checkConfigRevision(complete, stamp)
	entries = append(entries, revEntry)
	if err != nil {
		return entries, err
	}

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

// validateConfigure runs `orch configure`'s own §18.13-equivalent
// validation inside the freshly written worktree at dir: config.Load
// succeeds, the recomputed Revision matches, and — a check
// validateInstallation has no reason to make, since init only ever
// installs — every host cfg enables classifies StatusCurrent while
// every host it does not classifies StatusAbsent (contract: a disabled
// host's managed block must actually be gone, not merely left in place
// by a botched removal write). Hosts are iterated via
// interview.InstructionFile so this package need not duplicate
// interview's own host-name set.
func validateConfigure(dir string, complete *question.Complete, now time.Time) ([]ValidationEntry, error) {
	stamp := now.UTC().Format(time.RFC3339)
	var entries []ValidationEntry

	cfg, loadEntry, err := checkConfigLoad(dir, stamp)
	entries = append(entries, loadEntry)
	if err != nil {
		return entries, err
	}

	revEntry, err := checkConfigRevision(complete, stamp)
	entries = append(entries, revEntry)
	if err != nil {
		return entries, err
	}

	enabled := map[string]bool{}
	for _, h := range cfg.EnabledHosts() {
		enabled[h] = true
	}

	for _, host := range []string{"claude", "codex"} {
		file := interview.InstructionFile(host)
		name := "instructions:" + file
		want := instructions.StatusAbsent
		if enabled[host] {
			want = instructions.StatusCurrent
		}

		report, err := instructions.InspectFile(filepath.Join(dir, file))
		if err != nil {
			entries = append(entries, ValidationEntry{Name: name, Result: "fail", Detail: err.Error(), At: stamp})
			return entries, fmt.Errorf("%w: %s: %v", ErrValidationFailed, name, err)
		}
		if report.Status != want {
			detail := report.Detail
			if detail == "" {
				detail = fmt.Sprintf("status %d, want %s", report.Status, statusName(want))
			}
			entries = append(entries, ValidationEntry{Name: name, Result: "fail", Detail: detail, At: stamp})
			return entries, fmt.Errorf("%w: %s is not %s: %s", ErrValidationFailed, name, statusName(want), detail)
		}
		entries = append(entries, ValidationEntry{Name: name, Result: "pass", At: stamp})
	}

	// A configure change can append gitignore lines too (writeFiles is
	// shared verbatim): enabling metrics adds its ignore line, and a
	// missing base line is re-proposed by gitignoreLines — so the same
	// §18.13 gitignore check validateInstallation runs applies here.
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

// statusName renders an instructions.Status for validateConfigure's
// error/detail messages (only StatusCurrent and StatusAbsent are ever
// passed here).
func statusName(s instructions.Status) string {
	if s == instructions.StatusCurrent {
		return "current"
	}
	return "absent"
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
