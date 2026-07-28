package metrics

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// GitignoreLine is Dir expressed as the .gitignore directory pattern
// internal/interview proposes for it: Dir plus a trailing slash.
const GitignoreLine = Dir + "/"

// TrapActive reports whether repoRoot is in the metrics-ignore trap: an
// enabled metrics configuration while GitignoreLine is absent from
// repoRoot's .gitignore. In that state, Append's writes under Dir show
// up as untracked files that gitops.RequireClean counts, and nothing
// in RequireClean's own error names metrics as the cause — the
// activation, run-completion, and committed-configure RequireClean
// gates all trip on it with no clue what to fix. enabled is the
// caller's already-resolved cfg.Metrics.Enabled: this package stays
// policy-free and never reads *config.Config itself.
//
// This is the module's single implementation of that predicate: `orch
// configure-local`'s summary blocker, `orch doctor`'s own check, and
// every RequireClean-failure explanation below call it rather than
// re-deriving it.
func TrapActive(enabled bool, repoRoot string) (bool, error) {
	if !enabled {
		return false, nil
	}
	lines, err := gitignoreLines(repoRoot)
	if err != nil {
		return false, err
	}
	for _, l := range lines {
		if l == GitignoreLine {
			return false, nil
		}
	}
	return true, nil
}

// ExplainTrap returns the parenthetical cause-and-remedy text a
// RequireClean-failure caller appends to its own error when repoRoot is
// in the metrics-ignore trap (TrapActive(enabled, repoRoot)), or "" when
// it is not — including when the trap check itself fails, since
// RequireClean's own error already says enough there. The one wording
// lives here so activation, run completion, and orch configure's own
// RequireClean gate never drift apart.
func ExplainTrap(enabled bool, repoRoot string) string {
	trapped, err := TrapActive(enabled, repoRoot)
	if err != nil || !trapped {
		return ""
	}
	return fmt.Sprintf(" (cause: metrics is enabled but %s is not yet in .gitignore, so the metrics directory shows as untracked; remedy: run `orch configure` to add the .gitignore line, or disable metrics)", GitignoreLine)
}

// gitignoreLines reads repoRoot's .gitignore into individual lines (CR
// trimmed, mirroring internal/interview's own readGitignore), or nil if
// the file does not exist.
func gitignoreLines(repoRoot string) ([]string, error) {
	data, err := os.ReadFile(filepath.Join(repoRoot, ".gitignore"))
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read .gitignore: %w", err)
	}
	lines := strings.Split(string(data), "\n")
	for i, l := range lines {
		lines[i] = strings.TrimRight(l, "\r")
	}
	return lines, nil
}
