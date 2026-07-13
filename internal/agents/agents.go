// Package agents renders the five Codex agent TOMLs `orch
// render-agents` writes into <repo>/.codex/agents/ (PRD §22): each
// file's model and model_reasoning_effort are substituted from the
// effective configuration's hosts.codex.roles, but the surrounding
// body — name, description, developer_instructions — is the canonical
// shipped text embedded from adapters/codex/agents/*.toml
// (adapters/codex/embed.go's AgentTOMLs). That embed is the one
// source of truth: this package never carries its own copy of the
// prose, so the shipped plugin files and this package's rendered
// output cannot silently diverge from one another.
package agents

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/kninetimmy/orch/adapters/codex"
	"github.com/kninetimmy/orch/internal/config"
)

// Dir is the repo-relative, slash-form destination directory `orch
// render-agents` writes into. It is a machine-local artifact (like
// config.local.toml), not a committed one: a repository's own
// .gitignore, not this package, decides whether it is tracked.
const Dir = ".codex/agents"

// roleFile pairs one hosts.codex.roles key with the canonical TOML
// file stem it renders. review_downgrade maps to orch-reviewer-safe:
// the §10 safe-review-downgrade profile has no role of its own file
// name (see adapters/codex/README.md).
type roleFile struct {
	role string
	stem string
}

// roleFiles lists the five agent files this package renders, in the
// same fixed order every run produces them.
var roleFiles = []roleFile{
	{"scout", "orch-scout"},
	{"implementer", "orch-implementer"},
	{"specialist", "orch-specialist"},
	{"reviewer", "orch-reviewer"},
	{"review_downgrade", "orch-reviewer-safe"},
}

// File is one rendered agent TOML: Name is the file stem (e.g.
// "orch-scout", no extension), Content its fully substituted bytes.
type File struct {
	Name    string
	Content []byte
}

// profileFor returns role's RoleProfile from roles. role is always one
// of roleFiles' five keys; architect has no agent file (the Architect
// is the host session itself, never a dispatched agent).
func profileFor(roles config.Roles, role string) config.RoleProfile {
	switch role {
	case "scout":
		return roles.Scout
	case "implementer":
		return roles.Implementer
	case "specialist":
		return roles.Specialist
	case "reviewer":
		return roles.Reviewer
	case "review_downgrade":
		return roles.ReviewDowngrade
	default:
		// Unreachable: roleFiles is the only caller and is closed.
		return config.RoleProfile{}
	}
}

// Render produces the five agent TOMLs for h's roles, in roleFiles
// order. h must not be nil — the caller (`orch render-agents`) fails
// closed before calling Render when hosts.codex is not enabled.
func Render(h *config.Host) ([]File, error) {
	if h == nil {
		return nil, errors.New("agents.Render: codex host is nil")
	}
	files := make([]File, 0, len(roleFiles))
	for _, rf := range roleFiles {
		canonical, err := codex.AgentTOMLs.ReadFile("agents/" + rf.stem + ".toml")
		if err != nil {
			return nil, fmt.Errorf("read canonical %s.toml: %w", rf.stem, err)
		}
		profile := profileFor(h.Roles, rf.role)
		content, err := substitute(canonical, profile.Model, profile.Effort)
		if err != nil {
			return nil, fmt.Errorf("render %s.toml: %w", rf.stem, err)
		}
		files = append(files, File{Name: rf.stem, Content: content})
	}
	return files, nil
}

// developerInstructionsHeader marks where every canonical agent TOML's
// substitutable header (name, description, model,
// model_reasoning_effort) ends and its role-specific prose begins.
// substitute never rewrites anything at or past this marker, so a
// coincidental mention of a model name inside the prose is never
// touched.
const developerInstructionsHeader = "\ndeveloper_instructions = \"\"\"\n"

var modelLine = regexp.MustCompile(`(?m)^model = "[^"]*"$`)
var effortLine = regexp.MustCompile(`(?m)^model_reasoning_effort = "[^"]*"$`)

// substitute replaces canonical's model and model_reasoning_effort
// values with model and effort, leaving every other byte — including
// the entire developer_instructions body — untouched. It fails closed
// if canonical does not have the expected shape (missing
// developer_instructions marker, or not exactly one model /
// model_reasoning_effort line in the header), rather than silently
// rewriting the wrong line or leaving a value unsubstituted.
func substitute(canonical []byte, model, effort string) ([]byte, error) {
	s := string(canonical)
	idx := strings.Index(s, developerInstructionsHeader)
	if idx < 0 {
		return nil, errors.New("canonical TOML has no developer_instructions block")
	}
	header, rest := s[:idx], s[idx:]

	header, err := replaceOneLine(header, modelLine, fmt.Sprintf("model = %q", model))
	if err != nil {
		return nil, fmt.Errorf("model: %w", err)
	}
	header, err = replaceOneLine(header, effortLine, fmt.Sprintf("model_reasoning_effort = %q", effort))
	if err != nil {
		return nil, fmt.Errorf("model_reasoning_effort: %w", err)
	}
	return []byte(header + rest), nil
}

// replaceOneLine replaces pattern's single match in s with replacement,
// failing closed if pattern matches zero or more than one line.
func replaceOneLine(s string, pattern *regexp.Regexp, replacement string) (string, error) {
	n := len(pattern.FindAllStringIndex(s, -1))
	if n != 1 {
		return "", fmt.Errorf("expected exactly one match for %s in header, found %d", pattern.String(), n)
	}
	return pattern.ReplaceAllLiteralString(s, replacement), nil
}

// Write atomically writes each of files into repoRoot's Dir, creating
// the directory if absent and overwriting any existing file: a temp
// file in the same directory, synced, then renamed over the
// destination (internal/config.WriteLocal's shape, which also replaces
// atomically on Windows). On any single file's failure the files
// written before it are left in place; the failing file's own prior
// content, if any, is left untouched and its temp file removed on a
// best-effort basis.
func Write(repoRoot string, files []File) error {
	dir := filepath.Join(repoRoot, filepath.FromSlash(Dir))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("create %s: %w", Dir, err)
	}
	for _, f := range files {
		path := filepath.Join(dir, f.Name+".toml")
		if err := writeAtomic(path, dir, f.Content); err != nil {
			return err
		}
	}
	return nil
}

func writeAtomic(path, dir string, data []byte) error {
	f, err := os.CreateTemp(dir, "agent-*.tmp")
	if err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	tmp := f.Name()
	_, err = f.Write(data)
	if err == nil {
		err = f.Sync()
	}
	if closeErr := f.Close(); err == nil {
		err = closeErr
	}
	if err == nil {
		err = os.Rename(tmp, path)
	}
	if err != nil {
		_ = os.Remove(tmp) // best effort; the prior file, if any, is untouched
		return fmt.Errorf("write %s: %w", path, err)
	}
	return nil
}
