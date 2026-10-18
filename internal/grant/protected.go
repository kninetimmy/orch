package grant

import (
	"path"
	"path/filepath"
	"regexp"
	"strings"
)

// floorNames is the floor every repository gets, matched against a path's
// file name at any depth: instruction files, git attributes, dependency
// manifests and lockfiles, and install, build or release scripts, so a grant
// can never merge a new dependency without the human. Entries are lowercase
// path.Match patterns.
//
// Before #397 the floor was one compiled list written for Orch's own source
// (protectedPaths, root-anchored) plus adapterProtectedDirs (hooks, agents
// and skills under every adapter): internal/grant, the merge and gate engine
// files under internal/run, internal/cli/grant.go and hookgrant.go,
// internal/claudetranscript, routing, state, manifest, guard, paths,
// lockfile, adapters/opencode/src, ORCH-PRD.md, .golangci.yml, evaluation,
// .claude-plugin, adapters/claude/.claude-plugin and
// adapters/codex/.codex-plugin. Those entries left the binary; a repository
// protects its own source through the protected section of its committed
// .orchestrator/config.toml. CLAUDE.md, AGENTS.md, go.mod, go.sum,
// install.sh, install.ps1, .gitignore and .gitattributes were root-only then
// and match at any depth now.
var floorNames = []string{
	"claude.md", "agents.md", ".gitignore", ".gitattributes",
	"go.mod", "go.sum",
	"cargo.toml", "cargo.lock", "build.rs",
	"package.json", "package-lock.json", "npm-shrinkwrap.json", "pnpm-lock.yaml", "yarn.lock",
	"*.csproj", "*.fsproj", "*.vbproj", "directory.packages.props", "directory.build.props",
	"packages.lock.json", "nuget.config",
	"pyproject.toml", "setup.py", "setup.cfg", "pipfile", "pipfile.lock", "poetry.lock", "uv.lock",
	"requirements*.txt",
	"install.sh", "install.ps1", "makefile", "justfile", ".goreleaser.yml", ".goreleaser.yaml",
}

// floorRoots is the floor anchored at the repository root. A path is
// protected when it equals, or lies under, an entry.
var floorRoots = []string{".github/workflows", "license", ".orchestrator"}

// shortName matches an 8.3 short-name segment such as CLAUDE~1.MD.
var shortName = regexp.MustCompile(`~[0-9]`)

// Protected reports whether the repository-relative path p is protected:
// by the floor every repository gets (floorNames, floorRoots), or by repo,
// the paths listed in the protected section of the repository's committed
// configuration. A repo entry protects that path and, when it names a
// directory, everything under it. Comparison ignores case, so a
// case-insensitive filesystem cannot reach a protected file under another
// spelling. A path that is absolute or escapes the repository cannot be
// verified and is reported protected.
//
// Windows aliases are resolved the way Windows resolves them: a segment's
// trailing dots and spaces and any ":stream" suffix are dropped, so a
// committed CLAUDE.md. or CLAUDE.md::$DATA is CLAUDE.md. Before #353 they
// were compared as written. A segment that is all dots and spaces, or looks
// like an 8.3 short name (which can alias any long name), cannot be verified
// and is reported protected. These rules apply to every path p and to every
// repo entry alike.
func Protected(p string, repo []string) bool {
	p, ok := normalize(p)
	if !ok {
		return true
	}
	under := func(entry string) bool {
		return p == entry || strings.HasPrefix(p, entry+"/")
	}
	name := path.Base(p)
	for _, pattern := range floorNames {
		if m, _ := path.Match(pattern, name); m {
			return true
		}
	}
	for _, e := range floorRoots {
		if under(e) {
			return true
		}
	}
	for _, e := range repo {
		// An entry that cannot be verified fails closed: it protects every path.
		if e, ok := normalize(e); !ok || under(e) {
			return true
		}
	}
	return false
}

// normalize resolves p to its lowercase, cleaned, slash-separated form, or
// reports false when p cannot be verified as a path inside the repository.
func normalize(p string) (string, bool) {
	if filepath.IsAbs(p) || filepath.VolumeName(p) != "" || strings.HasPrefix(p, "/") || strings.HasPrefix(p, `\`) {
		return "", false
	}
	segs := strings.Split(strings.ReplaceAll(p, `\`, "/"), "/")
	for i, seg := range segs {
		if seg == "." || seg == ".." {
			continue
		}
		name, _, _ := strings.Cut(seg, ":")
		name = strings.TrimRight(name, ". ")
		if (name == "" && seg != "") || shortName.MatchString(name) {
			return "", false
		}
		segs[i] = name
	}
	p = strings.ToLower(path.Clean(strings.Join(segs, "/")))
	if p == ".." || strings.HasPrefix(p, "../") {
		return "", false
	}
	return p, true
}
