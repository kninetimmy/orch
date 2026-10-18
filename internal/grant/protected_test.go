package grant

import (
	"strings"
	"testing"
)

func TestProtectedFloor(t *testing.T) {
	// Every floor file name, as the issue gives it, is protected at the root
	// and at any depth, under any case.
	names := []string{
		"CLAUDE.md", "AGENTS.md", ".gitignore", ".gitattributes", "go.mod", "go.sum",
		"Cargo.toml", "Cargo.lock", "build.rs", "package.json", "package-lock.json",
		"npm-shrinkwrap.json", "pnpm-lock.yaml", "yarn.lock", "App.csproj", "App.fsproj",
		"App.vbproj", "Directory.Packages.props", "Directory.Build.props", "packages.lock.json",
		"NuGet.config", "pyproject.toml", "setup.py", "setup.cfg", "Pipfile", "Pipfile.lock",
		"poetry.lock", "uv.lock", "requirements.txt", "requirements-dev.txt", "install.sh",
		"install.ps1", "Makefile", "justfile", ".goreleaser.yml", ".goreleaser.yaml",
	}
	if len(names) != len(floorNames)+1 { // requirements*.txt has a second example
		t.Errorf("%d floor names tested for %d patterns", len(names), len(floorNames))
	}
	for _, n := range names {
		for _, p := range []string{n, "sub/dir/" + n, strings.ToUpper(n), `deep\` + n} {
			if !Protected(p, nil) {
				t.Errorf("%q not protected", p)
			}
		}
	}
	// Root-anchored floor entries cover everything under them.
	for _, p := range []string{
		".github/workflows", ".github/workflows/ci.yml", "LICENSE", "license", ".orchestrator",
		".orchestrator/config.toml", ".ORCHESTRATOR/x",
	} {
		if !Protected(p, nil) {
			t.Errorf("%q not protected", p)
		}
	}
	for _, p := range []string{
		`.\CLAUDE.md`, "./CLAUDE.md", "internal/../go.mod", "/etc/passwd", "../outside",
		// Windows aliases of protected names, and names that cannot be verified.
		"CLAUDE.md.", "CLAUDE.md ", "CLAUDE.md. .", "CLAUDE.md::$DATA", "claude.md:stream:$DATA",
		"go.mod...", "CLAUDE~1.MD", "docs/PROGRA~1/x.md", "docs/.../x.md", "docs/:x/y.md",
	} {
		if !Protected(p, nil) {
			t.Errorf("%q not protected", p)
		}
	}
	// Orch's own source entries (#397) and near misses are not on the floor.
	for _, p := range []string{
		"README.md", "go.modx", "xgo.mod", "requirements.md", "App.csproj.bak", "docs/LICENSE",
		"sub/.github/workflows/ci.yml", "sub/.orchestrator/x", ".github/dependabot.yml",
		"internal/grant/protected.go", "internal/run/merge.go", "internal/cli/grant.go",
		"internal/claudetranscript/x.go", "internal/routing/r.go", "internal/state/s.go",
		"internal/manifest/m.go", "internal/guard/g.go", "internal/paths/p.go", "internal/lockfile/l.go",
		"adapters/opencode/src/x.ts", "ORCH-PRD.md", ".golangci.yml", "evaluation/x",
		".claude-plugin/marketplace.json", "adapters/claude/.claude-plugin/plugin.json",
		"adapters/codex/.codex-plugin/plugin.json", "adapters/claude/skills/orch-delivery/GRANTS.md",
		"adapters/claude/hooks/x.json", "adapters/codex/agents/x.toml",
		"docs/a.b.md", "docs/x~y.md", "./docs/./a.md",
	} {
		if Protected(p, nil) {
			t.Errorf("%q protected", p)
		}
	}
}

func TestProtectedRepoList(t *testing.T) {
	repo := []string{"internal/grant", "internal/run/merge.go", `adapters\claude\skills\`, "Docs/Locked/"}
	for _, p := range []string{
		"internal/grant", "internal/grant/protected.go", `INTERNAL\Grant\x.go`, "internal/run/merge.go",
		"adapters/claude/skills/orch-delivery/GRANTS.md", "docs/locked/a.md", "internal/grant./x.go",
		"internal/run/merge.go...", "go.mod",
	} {
		if !Protected(p, repo) {
			t.Errorf("%q not protected", p)
		}
	}
	for _, p := range []string{
		"internal/grantx/a.go", "internal/run/merge_test.go", "internal/run", "adapters/claude/hooks/x",
		"docs/lockedx", "README.md",
	} {
		if Protected(p, repo) {
			t.Errorf("%q protected", p)
		}
	}
	// An entry that cannot be verified protects every path.
	if !Protected("README.md", []string{"docs/PROGRA~1"}) {
		t.Error("an unverifiable entry did not fail closed")
	}
}
