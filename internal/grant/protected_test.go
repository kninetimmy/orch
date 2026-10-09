package grant

import (
	"slices"
	"strings"
	"testing"
)

func TestProtected(t *testing.T) {
	// Every list entry, as given in the issue, is protected itself; entries
	// naming a directory also protect what lies under them.
	entries := []string{
		"internal/grant/", "adapters/claude/skills/orch-delivery/GRANTS.md",
		"internal/run/gate.go", "internal/run/activate.go", "internal/run/merge.go",
		"internal/run/mergereport.go", "internal/run/review.go", "internal/run/resolveblock.go",
		"internal/run/abandon.go", "internal/run/resume.go", "internal/routing/", "internal/state/",
		"internal/manifest/", "internal/guard/", "internal/paths/", "internal/lockfile/",
		"adapters/opencode/src/", "CLAUDE.md", "AGENTS.md", "ORCH-PRD.md", ".github/workflows/",
		".golangci.yml", "go.mod", "go.sum", ".claude-plugin/", "adapters/claude/.claude-plugin/",
		"adapters/codex/.codex-plugin/", "install.ps1", "install.sh", "LICENSE", ".gitignore",
		".gitattributes", ".orchestrator/", "evaluation/",
		"adapters/claude/hooks/", "adapters/claude/agents/", "adapters/codex/hooks/",
		"adapters/codex/agents/", "adapters/opencode/hooks/", "adapters/opencode/agents/",
		"adapters/new-host/hooks/",
	}
	for _, e := range protectedPaths {
		if !slices.Contains(entries, e) && !slices.Contains(entries, e+"/") {
			t.Errorf("list entry %q has no test", e)
		}
	}
	for _, e := range entries {
		if !Protected(e) {
			t.Errorf("%q not protected", e)
		}
		if dir, ok := strings.CutSuffix(e, "/"); ok && (!Protected(dir) || !Protected(dir+"/sub/file.go")) {
			t.Errorf("directory %q or its contents not protected", e)
		}
	}
	for _, p := range []string{
		`internal\grant\grant.go`, "./CLAUDE.md", "claude.md", "internal/../go.mod",
		"/etc/passwd", "../outside",
	} {
		if !Protected(p) {
			t.Errorf("%q not protected", p)
		}
	}
	for _, p := range []string{
		"README.md", "internal/run/plan.go", "internal/run/gate_test.go", "internal/grantx/a.go",
		"internal/run", "adapters/claude/skills/orch-delivery/SKILL.md", "adapters/hooks",
		"adapters/claude/README.md", "docs/CLAUDE.md", "go.modx", "evaluations/x", "cmd/orch/main.go",
		"adapters/opencode/smoke.mjs",
	} {
		if Protected(p) {
			t.Errorf("%q protected", p)
		}
	}
}
