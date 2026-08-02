package agents

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/kninetimmy/orch/internal/config"
)

// ClaudeAgentsSubdir is the subdirectory of the installed Claude
// plugin root that holds the agent definitions, as the plugin lays
// them out (adapters/claude/agents/*.md).
const ClaudeAgentsSubdir = "agents"

// claudeModel returns the model pinned in a Claude agent definition's
// leading `---` frontmatter. Every value in that frontmatter is a
// single-line scalar (adapters/claude/plugin_test.go's parseFrontmatter
// relies on the same fact), so a split on the line's first ":" is
// exact. It fails closed rather than defaulting: no frontmatter, no
// model line, and an empty model are each an error, because a
// definition whose model cannot be read is not evidence that the
// installed pin matches anything.
func claudeModel(data []byte) (string, error) {
	lines := strings.Split(string(data), "\n")
	if len(lines) == 0 || strings.TrimSpace(lines[0]) != "---" {
		return "", errors.New("does not start with a --- frontmatter delimiter")
	}
	for _, line := range lines[1:] {
		if strings.TrimSpace(line) == "---" {
			break
		}
		key, value, ok := strings.Cut(line, ":")
		if !ok || strings.TrimSpace(key) != "model" {
			continue
		}
		model := strings.TrimSpace(value)
		if model == "" {
			return "", errors.New("frontmatter model is empty")
		}
		return model, nil
	}
	return "", errors.New("frontmatter pins no model")
}

// CheckClaude compares the model h gives each of the five roles that
// ship a Claude agent definition against the model the installed
// definition for that role pins in its frontmatter, and returns an
// error naming every role whose two models differ. installRoot is the
// install path the Claude CLI's own plugin listing reports for the
// Orch Claude plugin; the definitions live in its ClaudeAgentsSubdir.
// The architect has no agent definition (the Architect is the host
// session itself, never a dispatched agent) and so is not compared.
//
// This is a read-only report: unlike the Codex side, nothing here
// renders or writes a Claude agent file, so a mismatch is reconciled
// by updating the installed plugin or the configuration, not by a
// render verb. It fails closed — an empty installRoot, an absent
// agents directory, an unreadable definition, and a definition
// carrying no model are each an error, never a pass.
func CheckClaude(h *config.Host, installRoot string) error {
	if h == nil {
		return errors.New("agents.CheckClaude: claude host is nil")
	}
	if installRoot == "" {
		return errors.New("the `claude plugin list --json` entry for the Orch Claude plugin carries no install path, so the installed agent definitions cannot be located")
	}

	dir := filepath.Join(installRoot, ClaudeAgentsSubdir)
	if _, err := os.Stat(dir); err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return fmt.Errorf("the installed agents directory %s is absent", dir)
		}
		return fmt.Errorf("read the installed agents directory %s: %w", dir, err)
	}

	var problems []string
	for _, rf := range roleFiles {
		path := filepath.Join(dir, rf.stem+".md")
		data, err := os.ReadFile(path)
		if err != nil {
			problems = append(problems, fmt.Sprintf("%s: unreadable definition: %v", rf.role, err))
			continue
		}
		installed, err := claudeModel(data)
		if err != nil {
			problems = append(problems, fmt.Sprintf("%s: %s: %v", rf.role, path, err))
			continue
		}
		if configured := profileFor(h.Roles, rf.role).Model; configured != installed {
			problems = append(problems, fmt.Sprintf("%s: configured %q, installed %s pins %q", rf.role, configured, path, installed))
		}
	}
	if len(problems) > 0 {
		return fmt.Errorf("%s; update the installed plugin or change hosts.claude.roles so the two agree, then restart Claude Code", strings.Join(problems, "; "))
	}
	return nil
}
