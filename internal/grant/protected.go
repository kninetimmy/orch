package grant

import (
	"path"
	"path/filepath"
	"regexp"
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
	"internal/run/grantgate.go",
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

// shortName matches an 8.3 short-name segment such as CLAUDE~1.MD.
var shortName = regexp.MustCompile(`~[0-9]`)

// Protected reports whether the repository-relative path p is protected.
// Comparison ignores case, so a case-insensitive filesystem cannot reach a
// protected file under another spelling. A path that is absolute or escapes
// the repository cannot be verified and is reported protected.
//
// Windows aliases are resolved the way Windows resolves them: a segment's
// trailing dots and spaces and any ":stream" suffix are dropped, so a
// committed CLAUDE.md. or CLAUDE.md::$DATA is CLAUDE.md. Before #353 they
// were compared as written. A segment that is all dots and spaces, or looks
// like an 8.3 short name (which can alias any long name), cannot be verified
// and is reported protected.
func Protected(p string) bool {
	if filepath.IsAbs(p) || filepath.VolumeName(p) != "" || strings.HasPrefix(p, "/") || strings.HasPrefix(p, `\`) {
		return true
	}
	segs := strings.Split(strings.ReplaceAll(p, `\`, "/"), "/")
	for i, seg := range segs {
		if seg == "." || seg == ".." {
			continue
		}
		name, _, _ := strings.Cut(seg, ":")
		name = strings.TrimRight(name, ". ")
		if (name == "" && seg != "") || shortName.MatchString(name) {
			return true
		}
		segs[i] = name
	}
	p = strings.ToLower(path.Clean(strings.Join(segs, "/")))
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
	segs = strings.Split(p, "/")
	return len(segs) >= 3 && segs[0] == "adapters" && slices.Contains(adapterProtectedDirs, segs[2])
}
