package bootstrap

import (
	"fmt"
	"sort"
	"strings"

	"github.com/kninetimmy/orch/internal/config"
	"github.com/kninetimmy/orch/internal/question"
)

// renderRecord renders approved init/configure choices and their evidence.
// Before this change the record duplicated question.Complete as JSON; now
// instruction edits, protected-path reasons and conflicts have readable
// sections. The structured Complete document and re-materialization remain.
// There is deliberately no orch:manifest region: the free text here is
// already constrained (detection values are boolean strings/paths,
// configuration is orch-rendered TOML) and nothing ever parses this
// body back (unlike internal/manifest's Delivery audit records).
//
// validations is nil when the record is rendered for the issue body
// (validation has not run yet, since it happens inside the worktree
// Stage 1 creates after the issue) and populated when re-rendered for
// the PR body. closesIssue is the issue number to link with a "Closes
// #<n>" line, or 0 to omit it — an issue cannot close itself, so the
// issue body always passes 0; the PR body passes the real number.
func renderRecord(complete *question.Complete, answers map[string]string, title string, validations []ValidationEntry, closesIssue int) (string, error) {
	cfg, err := config.Parse([]byte(complete.Summary.ConfigTOML))
	if err != nil {
		return "", fmt.Errorf("render bootstrap record: parse approved configuration: %w", err)
	}
	var b strings.Builder

	fmt.Fprintf(&b, "## What\n\n%s.\n\n", title)
	var sections []string
	if _, configure := answers["pick.hosts"]; !configure {
		sections = append(sections, "host enablement", "role profiles", "settings")
	} else {
		for _, section := range []struct{ key, label string }{
			{"pick.hosts", "host enablement"},
			{"pick.roles.claude", "Claude role profiles"},
			{"pick.roles.codex", "Codex role profiles"},
			{"pick.roles.opencode", "OpenCode role profiles"},
			{"pick.settings", "settings"},
		} {
			if answers[section.key] == "yes" {
				sections = append(sections, section.label)
			}
		}
	}
	if answer, ok := answers["protected.paths"]; ok && strings.TrimSpace(answer) != "defer" {
		sections = append(sections, "grant-protected paths")
	}
	if len(sections) > 0 {
		fmt.Fprintf(&b, "Selected interview sections: %s.\n\n", strings.Join(sections, ", "))
	}
	if len(complete.Summary.Files) > 0 {
		b.WriteString("Instruction-file changes are shown below.\n\n")
	}
	b.WriteString("## Why\n\nApply the choices explicitly approved in the Orch configuration interview. ")
	b.WriteString("The proposed configuration and edits below are derived from those answers.\n")

	b.WriteString("\n## Detection\n\n")
	for _, k := range sortedKeys(complete.Detection) {
		fmt.Fprintf(&b, "- %s: %s\n", k, complete.Detection[k])
	}

	b.WriteString("\n## Configuration\n\n")
	fmt.Fprintf(&b, "**Config revision:** `%s`\n\n", complete.Summary.ConfigRevision)
	writeCodeBlock(&b, "toml", complete.Summary.ConfigTOML)

	if complete.Summary.ConfigDiff != "" {
		b.WriteString("\n## Configuration diff\n\n")
		writeCodeBlock(&b, "diff", complete.Summary.ConfigDiff)
	}

	if cfg.Protected != nil {
		b.WriteString("\n## Grant-protected paths\n\n")
		fmt.Fprintf(&b, "**Survey methodology:** %d\n\n", cfg.Protected.MethodologyVersion)
		if len(cfg.Protected.Paths) == 0 {
			b.WriteString("No entries beyond the built-in floor.\n")
		} else {
			b.WriteString("| Path | Reason |\n| --- | --- |\n")
			escapeCell := strings.NewReplacer("\\", "\\\\", "|", "\\|")
			for _, entry := range cfg.Protected.Paths {
				fmt.Fprintf(&b, "| %s | %s |\n", escapeCell.Replace(entry.Path), escapeCell.Replace(entry.Reason))
			}
		}
	}

	b.WriteString("\n## Instruction files\n\n")
	if len(complete.Summary.Files) == 0 {
		b.WriteString("_none_\n")
	}
	for _, f := range complete.Summary.Files {
		fmt.Fprintf(&b, "- `%s` (existed: %t)\n", f.Path, f.Existed)
		if f.Diff != "" {
			b.WriteByte('\n')
			writeCodeBlock(&b, "diff", f.Diff)
		} else if !f.Existed {
			b.WriteByte('\n')
			writeCodeBlock(&b, "md", f.NewContent)
		}
	}

	b.WriteString("\n## .gitignore additions\n\n")
	if len(complete.Summary.GitignoreLines) == 0 {
		b.WriteString("_none_\n")
	}
	for _, l := range complete.Summary.GitignoreLines {
		fmt.Fprintf(&b, "- `%s`\n", l)
	}

	b.WriteString("\n## Conflicts\n\n")
	if len(complete.Summary.Conflicts) == 0 {
		b.WriteString("_none_\n")
	}
	for _, conflict := range complete.Summary.Conflicts {
		fmt.Fprintf(&b, "- %s\n", conflict)
	}

	if len(validations) > 0 {
		b.WriteString("\n## Validations\n\n")
		for _, v := range validations {
			if v.Detail != "" {
				fmt.Fprintf(&b, "- %s: %s (%s)\n", v.Name, v.Result, v.Detail)
			} else {
				fmt.Fprintf(&b, "- %s: %s\n", v.Name, v.Result)
			}
		}
	}

	if closesIssue > 0 {
		fmt.Fprintf(&b, "\nCloses #%d\n", closesIssue)
	}

	return b.String(), nil
}

// A new instruction file may itself contain Markdown fences.
func writeCodeBlock(b *strings.Builder, language, content string) {
	fence := "```"
	for strings.Contains(content, fence) {
		fence += "`"
	}
	fmt.Fprintf(b, "%s%s\n%s", fence, language, content)
	if !strings.HasSuffix(content, "\n") {
		b.WriteByte('\n')
	}
	fmt.Fprintf(b, "%s\n", fence)
}

// sortedKeys returns m's keys sorted, so the rendered Detection
// section is byte-stable.
func sortedKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
