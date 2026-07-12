package interview

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/kninetimmy/orch/internal/config"
	"github.com/kninetimmy/orch/internal/instructions"
	"github.com/kninetimmy/orch/internal/question"
)

// instructionFileFor names the root instruction file a host owns
// (contract call 4): AGENTS.md for Codex, CLAUDE.md for Claude Code.
var instructionFileFor = map[string]string{
	"claude": "CLAUDE.md",
	"codex":  "AGENTS.md",
}

// applicableInstructionFiles lists, in claude-then-codex order, the
// root instruction file name for every host cfg enables.
func applicableInstructionFiles(cfg *config.Config) []string {
	var names []string
	if cfg.Hosts.Claude != nil {
		names = append(names, instructionFileFor["claude"])
	}
	if cfg.Hosts.Codex != nil {
		names = append(names, instructionFileFor["codex"])
	}
	return names
}

// InstructionFile returns host's root instruction file name
// (instructionFileFor's exported form), or "" for an unrecognized host
// name — exported for internal/bootstrap's disable-validation
// (validateConfigure), which needs every host's file status without
// depending on interview's own host-name set.
func InstructionFile(host string) string {
	return instructionFileFor[host]
}

// baseGitignoreLines are the repo-relative ignore patterns every
// initialized repository needs, mirroring (as literal strings —
// Detect's execProber-duplication precedent, so this pre-init-safe
// package need not import internal/run/state/lockfile) the values of
// run.WorktreeContainer+"/", config.LocalOverridePath, state.Path, and
// lockfile.Path.
var baseGitignoreLines = []string{
	".orchestrator/worktrees/",
	".orchestrator/config.local.toml",
	".orchestrator/state.json",
	".orchestrator/delivery.lock",
}

// metricsGitignoreLine is the local, gitignored metrics storage
// directory PRD §21 describes ("local gitignored storage"); no other
// package names this path yet, so interview owns choosing it — a
// judgment call flagged for reviewer attention.
const metricsGitignoreLine = ".orchestrator/metrics/"

// buildSummary materializes cfg's rendered TOML and proposed
// instruction-file changes into a question.Summary (PRD §18 steps
// 7-8). A blocking instructions.Plan error (drifted, malformed, or a
// newer managed-block version than this build knows) becomes a
// Blockers entry naming the file and the exact classification detail,
// rather than aborting Next outright — the human sees the whole
// summary with the blocker named (PRD §19: stop on conflicting
// instructions), not a bare error.
func buildSummary(cfg *config.Config, repoRoot string) (question.Summary, error) {
	rendered, err := config.Render(cfg)
	if err != nil {
		return question.Summary{}, fmt.Errorf("render configuration for summary: %w", err)
	}

	files, blockers, err := planInstructionFiles(repoRoot, applicableInstructionFiles(cfg), instructions.PlanFile)
	if err != nil {
		return question.Summary{}, err
	}

	conflicts, err := instructions.Scan(repoRoot)
	if err != nil {
		return question.Summary{}, fmt.Errorf("scan for nested instruction conflicts: %w", err)
	}
	var conflictLines []string
	for _, c := range conflicts {
		rel, relErr := filepath.Rel(repoRoot, c.Path)
		if relErr != nil {
			rel = c.Path
		}
		conflictLines = append(conflictLines, fmt.Sprintf("%s: %s", filepath.ToSlash(rel), c.Report.Detail))
	}

	gitignore, err := gitignoreLines(repoRoot, cfg.Metrics.Enabled)
	if err != nil {
		return question.Summary{}, err
	}

	return question.Summary{
		ConfigTOML:     string(rendered),
		ConfigRevision: cfg.ConfigRevision,
		Files:          files,
		GitignoreLines: gitignore,
		Conflicts:      conflictLines,
		Blockers:       blockers,
	}, nil
}

// planInstructionFiles runs planFunc (instructions.PlanFile or
// instructions.PlanRemoveFile) for each name in names, turning a
// blocking Plan error into a Blockers entry rather than aborting —
// buildSummary and interview's own buildConfigureSummary
// (`orch configure`, configure.go) both share this loop: init only
// ever calls it with instructions.PlanFile, while configure calls it
// once with PlanFile (for the hosts it enables) and once more with
// PlanRemoveFile (for the hosts it disables).
func planInstructionFiles(repoRoot string, names []string, planFunc func(string) (instructions.Change, error)) ([]question.FileChange, []string, error) {
	var files []question.FileChange
	var blockers []string
	for _, name := range names {
		path := filepath.Join(repoRoot, name)
		ch, err := planFunc(path)
		if isBlockingPlanError(err) {
			blockers = append(blockers, fmt.Sprintf("%s: %v", name, err))
			continue
		}
		if err != nil {
			return nil, nil, err
		}
		files = append(files, question.FileChange{
			Path:       filepath.ToSlash(name),
			Existed:    ch.FileExisted,
			Diff:       ch.Diff,
			NewContent: ch.New,
		})
	}
	return files, blockers, nil
}

// isBlockingPlanError reports whether err is one of instructions.Plan's
// fail-closed structural errors (ErrDrifted, ErrNewerVersion,
// ErrMalformed) — the taxonomy row Plan refuses to propose a change
// for.
func isBlockingPlanError(err error) bool {
	return errors.Is(err, instructions.ErrDrifted) ||
		errors.Is(err, instructions.ErrNewerVersion) ||
		errors.Is(err, instructions.ErrMalformed)
}

// gitignoreLines returns baseGitignoreLines (plus metricsGitignoreLine
// when metricsEnabled) filtered against whatever repoRoot's .gitignore
// already contains, so the summary proposes only genuinely missing
// lines.
func gitignoreLines(repoRoot string, metricsEnabled bool) ([]string, error) {
	want := append([]string{}, baseGitignoreLines...)
	if metricsEnabled {
		want = append(want, metricsGitignoreLine)
	}

	existing, err := readGitignore(repoRoot)
	if err != nil {
		return nil, err
	}
	existingSet := make(map[string]bool, len(existing))
	for _, l := range existing {
		existingSet[l] = true
	}

	var lines []string
	for _, l := range want {
		if !existingSet[l] {
			lines = append(lines, l)
		}
	}
	return lines, nil
}

// readGitignore reads repoRoot's .gitignore into its individual lines
// (CR trimmed), or nil if the file does not exist.
func readGitignore(repoRoot string) ([]string, error) {
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

// buildComplete assembles the terminal Complete document from the
// approved summary and the detection facts the interview ran on.
// BootstrapReady never blocks on missing memhub or host CLIs — only
// git and gh are load-bearing for the bootstrap executor itself (PR
// B); the interview reports everything else as configuration, not a
// readiness gate.
func buildComplete(summary question.Summary, facts Facts) *question.Complete {
	detection := map[string]string{
		"claude_cli":     boolValue(facts.ClaudeCLI),
		"codex_cli":      boolValue(facts.CodexCLI),
		"git":            boolValue(facts.Git),
		"git_root":       facts.GitRoot,
		"gh":             boolValue(facts.Gh),
		"memhub_cli":     boolValue(facts.MemhubCLI),
		"memhub_healthy": boolValue(facts.MemhubHealthy),
		"memhub_detail":  facts.MemhubDetail,
	}

	var reasons []string
	if !facts.Git {
		reasons = append(reasons, "git was not detected")
	}
	if !facts.Gh {
		reasons = append(reasons, "gh was not detected")
	}

	return &question.Complete{
		Summary:         summary,
		Detection:       detection,
		BootstrapReady:  facts.Git && facts.Gh,
		NotReadyReasons: reasons,
	}
}
