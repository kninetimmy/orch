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

// reviewWorktreeRel is the repo-relative, slash-separated path of issue
// number's disposable review checkout (see ReviewWorktree). It shares
// the git-ignored container with the executor worktrees but carries a
// "review-" prefix, so it cannot equal worktreeRel's output for this
// issue or any other: worktreeRel always yields "issue-<n>", which
// never begins with "review-". Nor can either path contain the other,
// since they are siblings — which is what keeps the review checkout
// outside every registered worktree rather than nested inside one.
//
// Nothing persists this path. Both callers — the verb that creates the
// checkout and the cleanup that removes it — derive it here, which is
// what keeps the review checkout absent from the worktree set the
// guard reads out of state.
func reviewWorktreeRel(number int) string {
	return WorktreeContainer + "/" + fmt.Sprintf("review-issue-%d", number)
}

// reviewWorktreeAbs is the canonical filesystem path for issue
// number's review checkout under repoRoot.
func reviewWorktreeAbs(repoRoot string, number int) string {
	return filepath.Join(repoRoot, filepath.FromSlash(reviewWorktreeRel(number)))
}
