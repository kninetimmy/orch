package grant

import (
	"path"
	"path/filepath"
	"slices"
	"strings"
)

// protectedPaths is the protected-path list the grant code holds. A path is
// protected when it equals, or lies under, an entry.
var protectedPaths = []string{
	"internal/grant",
	"adapters/claude/skills/orch-delivery/GRANTS.md",
	"internal/run/gate.go",
	"internal/run/activate.go",
	"internal/run/merge.go",
	"internal/run/mergereport.go",
	"internal/run/review.go",
	"internal/run/resolveblock.go",
	"internal/run/abandon.go",
	"internal/run/resume.go",
	"internal/routing",
	"internal/state",
	"internal/manifest",
	"internal/guard",
	"internal/paths",
	"internal/lockfile",
	"adapters/opencode/src",
	"CLAUDE.md",
	"AGENTS.md",
	"ORCH-PRD.md",
	".github/workflows",
	".golangci.yml",
	"go.mod",
	"go.sum",
	".claude-plugin",
	"adapters/claude/.claude-plugin",
	"adapters/codex/.codex-plugin",
	"install.ps1",
	"install.sh",
	"LICENSE",
	".gitignore",
	".gitattributes",
	".orchestrator",
	"evaluation",
}

// adapterProtectedDirs are protected under every adapter: adapters/<any>/<dir>.
var adapterProtectedDirs = []string{"hooks", "agents"}

// Protected reports whether the repository-relative path p is protected.
// Comparison ignores case, so a case-insensitive filesystem cannot reach a
// protected file under another spelling. A path that is absolute or escapes
// the repository cannot be verified and is reported protected.
func Protected(p string) bool {
	if filepath.IsAbs(p) || filepath.VolumeName(p) != "" || strings.HasPrefix(p, "/") || strings.HasPrefix(p, `\`) {
		return true
	}
	p = strings.ToLower(path.Clean(strings.ReplaceAll(p, `\`, "/")))
	if p == ".." || strings.HasPrefix(p, "../") {
		return true
	}
	under := func(entry string) bool {
		entry = strings.ToLower(entry)
		return p == entry || strings.HasPrefix(p, entry+"/")
	}
	for _, e := range protectedPaths {
		if under(e) {
			return true
		}
	}
	segs := strings.Split(p, "/")
	return len(segs) >= 3 && segs[0] == "adapters" && slices.Contains(adapterProtectedDirs, segs[2])
}
