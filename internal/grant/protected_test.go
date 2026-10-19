package grant

import (
	"os"
	"path/filepath"
	"slices"
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
		".mcp.json", "opencode.json", "opencode.jsonc",
		"go.work", "go.work.sum", "Gemfile", "Gemfile.lock", "pom.xml", "build.gradle",
		"build.gradle.kts", "settings.gradle", "settings.gradle.kts", "gradlew", "gradlew.bat",
		"gradle-wrapper.properties", "gradle-wrapper.jar", "composer.json", "composer.lock",
		"bun.lock", "bun.lockb", "Directory.Build.targets", "global.json", "Package.swift",
		"Package.resolved", "mix.exs", "mix.lock", "vcpkg.json", ".npmrc", ".yarnrc.yml",
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
	// A harness directory is protected as any segment, at any depth, under any case.
	if len(floorDirs) != 3 {
		t.Errorf("%d floor directories tested for %d entries", 3, len(floorDirs))
	}
	for _, d := range []string{".claude", ".codex", ".opencode"} {
		for _, p := range []string{
			d, d + "/x", "pkg/web/" + d + "/skills/x/SKILL.md", strings.ToUpper(d) + "/x", `deep\` + d + `\x`,
		} {
			if !Protected(p, nil) {
				t.Errorf("%q not protected", p)
			}
		}
	}
	for _, p := range []string{".claude/settings.json", ".codex/config.toml", ".opencode/agents/a.md", ".claude./x"} {
		if !Protected(p, nil) {
			t.Errorf("%q not protected", p)
		}
	}
	// Root-anchored floor entries cover everything under them.
	for _, p := range []string{
		".github/workflows", ".github/workflows/ci.yml", "LICENSE", "license", ".orchestrator",
		".orchestrator/config.toml", ".ORCHESTRATOR/x", ".github/actions", ".github/actions/setup/action.yml",
		".GitHub/Actions/x",
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
		// Near misses of the #412 entries.
		"adapters/claude/skills/x/SKILL.md", "internal/gemfile.go", "docs/.github/actions/x.yml",
		".claudex/x", "x.claude/y", "docs/claude/x", ".mcp.json.bak", "opencode.json5", "go.workx",
		".github/actionsx/a.yml", "pkg/.codex-plugin/plugin.json",
		// The survey methodology's examples of what the floor does not cover.
		"build.sbt", "Podfile", "CMakeLists.txt", "pip.conf", "scripts/release.sh", "release.ps1",
		".gitlab-ci.yml", "LICENSE.md", "LICENSE.txt",
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

// TestSurveyDocNamesEveryFloorEntry keeps the survey methodology's description
// of the floor from drifting: the Claude orch-setup reference must name every
// floor entry, or a survey following it would skip paths the floor lacks.
func TestSurveyDocNamesEveryFloorEntry(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "..", "adapters", "claude", "skills", "orch-setup", "PROTECTED_PATHS_SURVEY.md"))
	if err != nil {
		t.Fatal(err)
	}
	// Only the floor's own section counts, so a mention elsewhere cannot mask an omission.
	_, doc, ok := strings.Cut(strings.ToLower(string(data)), "\n## what the list is\n")
	if !ok {
		t.Fatal(`survey methodology has no "What the list is" section`)
	}
	doc, _, _ = strings.Cut(doc, "\n## ")
	for _, entry := range slices.Concat(floorNames, floorDirs, floorRoots) {
		if !strings.Contains(doc, "`"+entry+"`") {
			t.Errorf("survey methodology does not name floor entry %q", entry)
		}
	}
}
