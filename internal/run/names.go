package run

import (
	"fmt"
	"path/filepath"
	"regexp"
	"strings"
)

// WorktreeContainer is the repo-relative directory activation creates
// per-issue worktrees under. It must be listed in .gitignore (F1):
// gitops.RequireIgnored enforces that before activation touches git.
const WorktreeContainer = ".orchestrator/worktrees"

var slugNonAlnum = regexp.MustCompile(`[^a-z0-9]+`)

// slugify lowercases s, collapses every run of non-[a-z0-9] characters
// to a single "-", trims leading/trailing "-", and caps the result at
// 40 characters (trimming any trailing "-" the cut leaves). An empty
// result becomes "issue".
func slugify(s string) string {
	slug := slugNonAlnum.ReplaceAllString(strings.ToLower(s), "-")
	slug = strings.Trim(slug, "-")
	if len(slug) > 40 {
		slug = strings.TrimRight(slug[:40], "-")
	}
	if slug == "" {
		slug = "issue"
	}
	return slug
}

// branchName is the per-issue feature branch name (PRD §12).
func branchName(number int, title string) string {
	return fmt.Sprintf("orch/issue-%d-%s", number, slugify(title))
}

// worktreeRel is the repo-relative, slash-separated worktree path for
// issue number, the form state.Issue.Worktree persists.
func worktreeRel(number int) string {
	return WorktreeContainer + "/" + fmt.Sprintf("issue-%d", number)
}

// worktreeAbs is the canonical filesystem path for issue number's
// worktree under repoRoot, the form gitops.AddWorktree takes.
func worktreeAbs(repoRoot string, number int) string {
	return filepath.Join(repoRoot, filepath.FromSlash(worktreeRel(number)))
}
