package bootstrap

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/kninetimmy/orch/internal/question"
)

// renderRecord renders the rendered bootstrap record (contract call
// 4): human markdown sections — Detection, Configuration, Instruction
// files, .gitignore additions, and (once available) Validations —
// followed by the canonical question.Complete JSON in a fenced block.
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
func renderRecord(complete *question.Complete, validations []ValidationEntry, closesIssue int) (string, error) {
	var b strings.Builder

	b.WriteString("## Detection\n\n")
	for _, k := range sortedKeys(complete.Detection) {
		fmt.Fprintf(&b, "- %s: %s\n", k, complete.Detection[k])
	}

	b.WriteString("\n## Configuration\n\n")
	fmt.Fprintf(&b, "**Config revision:** `%s`\n\n", complete.Summary.ConfigRevision)
	b.WriteString("```toml\n")
	b.WriteString(complete.Summary.ConfigTOML)
	if !strings.HasSuffix(complete.Summary.ConfigTOML, "\n") {
		b.WriteByte('\n')
	}
	b.WriteString("```\n")

	if complete.Summary.ConfigDiff != "" {
		b.WriteString("\n## Configuration diff\n\n")
		b.WriteString("```diff\n")
		b.WriteString(complete.Summary.ConfigDiff)
		if !strings.HasSuffix(complete.Summary.ConfigDiff, "\n") {
			b.WriteByte('\n')
		}
		b.WriteString("```\n")
	}

	b.WriteString("\n## Instruction files\n\n")
	if len(complete.Summary.Files) == 0 {
		b.WriteString("_none_\n")
	}
	for _, f := range complete.Summary.Files {
		fmt.Fprintf(&b, "- `%s` (existed: %t)\n", f.Path, f.Existed)
	}

	b.WriteString("\n## .gitignore additions\n\n")
	if len(complete.Summary.GitignoreLines) == 0 {
		b.WriteString("_none_\n")
	}
	for _, l := range complete.Summary.GitignoreLines {
		fmt.Fprintf(&b, "- `%s`\n", l)
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

	data, err := json.MarshalIndent(complete, "", "  ")
	if err != nil {
		return "", fmt.Errorf("render bootstrap record: encode complete document: %w", err)
	}
	b.WriteString("\n```json\n")
	b.Write(data)
	b.WriteByte('\n')
	b.WriteString("```\n")

	return b.String(), nil
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
