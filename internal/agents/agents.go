// Package agents renders the five dispatched role definitions `orch
// render-agents` writes for every enabled host. Each file starts from
// the canonical definition embedded directly from its shipped adapter;
// rendering substitutes only the routing fields that host supports.
package agents

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/kninetimmy/orch/adapters/claude"
	"github.com/kninetimmy/orch/adapters/codex"
	"github.com/kninetimmy/orch/adapters/opencode"
	"github.com/kninetimmy/orch/internal/config"
)

// Project-local generated-agent destinations. Initialization and
// configuration add both to .gitignore whether or not both hosts are
// currently enabled, so enabling the other host later stays safe.
const (
	ClaudeDir   = ".claude/agents"
	CodexDir    = ".codex/agents"
	OpenCodeDir = ".opencode/agents"
)

// roleFile pairs one hosts.<host>.roles key with the canonical file
// stem it renders. review_downgrade maps to orch-reviewer-safe: the
// safe-review-downgrade profile has no role of its own file name.
type roleFile struct {
	role string
	stem string
}

// roleFiles lists every dispatched role definition in the fixed order
// every host render produces. Architect has no definition because the
// Architect is the host session itself, never a dispatched agent.
var roleFiles = []roleFile{
	{"scout", "orch-scout"},
	{"implementer", "orch-implementer"},
	{"specialist", "orch-specialist"},
	{"reviewer", "orch-reviewer"},
	{"review_downgrade", "orch-reviewer-safe"},
}

// File is one rendered project agent definition. Path is repo-relative
// and slash-form; Content is the fully substituted canonical body.
type File struct {
	Path    string
	Content []byte
}

// Destination returns host's repo-relative generated-agent directory.
func Destination(host string) (string, error) {
	switch host {
	case "claude":
		return ClaudeDir, nil
	case "codex":
		return CodexDir, nil
	case "opencode":
		return OpenCodeDir, nil
	default:
		return "", fmt.Errorf("agents: unsupported host %q", host)
	}
}

// profileFor returns role's RoleProfile from roles. role is always one
// of roleFiles' five keys.
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

// Render produces host's five project agent definitions in roleFiles
// order. h must be the enabled host's effective configuration.
func Render(host string, h *config.Host) ([]File, error) {
	if h == nil {
		return nil, fmt.Errorf("agents.Render: %s host is nil", host)
	}
	dir, err := Destination(host)
	if err != nil {
		return nil, err
	}

	files := make([]File, 0, len(roleFiles))
	for _, rf := range roleFiles {
		profile := profileFor(h.Roles, rf.role)
		var canonical, content []byte
		var ext string
		switch host {
		case "claude":
			ext = ".md"
			canonical, err = claude.AgentDefinitions.ReadFile("agents/" + rf.stem + ext)
			if err == nil {
				content, err = substituteClaude(canonical, profile.Model)
			}
		case "codex":
			ext = ".toml"
			canonical, err = codex.AgentTOMLs.ReadFile("agents/" + rf.stem + ext)
			if err == nil {
				content, err = substituteCodex(canonical, profile.Model, profile.Effort)
			}
		case "opencode":
			ext = ".md"
			canonical, err = opencode.AgentDefinitions.ReadFile("agents/" + rf.stem + ext)
			if err == nil {
				content, err = substituteOpenCode(canonical, profile.Model, profile.EffectiveOpenCodeVariant())
			}
		}
		if err != nil {
			return nil, fmt.Errorf("render %s/%s%s: %w", host, rf.stem, ext, err)
		}
		files = append(files, File{Path: dir + "/" + rf.stem + ext, Content: content})
	}
	return files, nil
}

// developerInstructionsHeader marks where every canonical Codex agent
// TOML's substitutable header ends and its role-specific prose begins.
const developerInstructionsHeader = "\ndeveloper_instructions = \"\"\"\n"

var codexModelLine = regexp.MustCompile(`(?m)^model = "[^"]*"$`)
var codexEffortLine = regexp.MustCompile(`(?m)^model_reasoning_effort = "[^"]*"$`)

// substituteCodex replaces only model and model_reasoning_effort in
// canonical's header, leaving every other byte untouched.
func substituteCodex(canonical []byte, model, effort string) ([]byte, error) {
	s := string(canonical)
	idx := strings.Index(s, developerInstructionsHeader)
	if idx < 0 {
		return nil, errors.New("canonical TOML has no developer_instructions block")
	}
	header, rest := s[:idx], s[idx:]

	header, err := replaceOneLine(header, codexModelLine, fmt.Sprintf("model = %q", model))
	if err != nil {
		return nil, fmt.Errorf("model: %w", err)
	}
	header, err = replaceOneLine(header, codexEffortLine, fmt.Sprintf("model_reasoning_effort = %q", effort))
	if err != nil {
		return nil, fmt.Errorf("model_reasoning_effort: %w", err)
	}
	return []byte(header + rest), nil
}

const claudeFrontmatterStart = "---\n"
const claudeFrontmatterEnd = "\n---\n"

var claudeModelLine = regexp.MustCompile(`(?m)^model: [^\r\n]*$`)

// substituteClaude replaces only model in canonical's frontmatter.
// This adapter does not pin Claude effort in project definitions; it
// continues to travel as the Delivery skill's prompt cue. The unchanged
// canonical value remains byte-identical; every override is quoted so
// YAML cannot reinterpret it or let it inject another frontmatter field.
func substituteClaude(canonical []byte, model string) ([]byte, error) {
	s := string(canonical)
	if !strings.HasPrefix(s, claudeFrontmatterStart) {
		return nil, errors.New("canonical Markdown has no leading frontmatter")
	}
	end := strings.Index(s[len(claudeFrontmatterStart):], claudeFrontmatterEnd)
	if end < 0 {
		return nil, errors.New("canonical Markdown has no closing frontmatter delimiter")
	}
	end += len(claudeFrontmatterStart)
	header, rest := s[:end], s[end:]
	value := strconv.Quote(model)
	if claudeModelLine.FindString(header) == "model: "+model {
		value = model
	}
	header, err := replaceOneLine(header, claudeModelLine, "model: "+value)
	if err != nil {
		return nil, fmt.Errorf("model: %w", err)
	}
	return []byte(header + rest), nil
}

// substituteOpenCode replaces only model in native V2 frontmatter. OpenCode
// carries a selected model-specific variant as #variant and leaves a
// no-variant provider/model reference bare.
func substituteOpenCode(canonical []byte, model, variant string) ([]byte, error) {
	if strings.Contains(model, "#") {
		return nil, fmt.Errorf("model %q already contains an OpenCode variant", model)
	}
	if variant != "" {
		model += "#" + variant
	}
	return substituteClaude(canonical, model)
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

// Write atomically writes files into their project directories,
// creating those directories if absent and overwriting existing files.
func Write(repoRoot string, files []File) error {
	for _, f := range files {
		path := filepath.Join(repoRoot, filepath.FromSlash(f.Path))
		dir := filepath.Dir(path)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return fmt.Errorf("create %s: %w", filepath.ToSlash(filepath.Dir(f.Path)), err)
		}
		if err := writeAtomic(path, dir, f.Content); err != nil {
			return err
		}
	}
	return nil
}

// Stale reports every definition for host that is absent, unreadable,
// or byte-different from Render's current output. Read errors are joined
// after every expected path has been checked so callers can name all of
// them at once.
func Stale(repoRoot, host string, h *config.Host) ([]string, error) {
	files, err := Render(host, h)
	if err != nil {
		return nil, err
	}

	var stale []string
	var readErrs []error
	for _, f := range files {
		got, err := os.ReadFile(filepath.Join(repoRoot, filepath.FromSlash(f.Path)))
		switch {
		case errors.Is(err, fs.ErrNotExist):
			stale = append(stale, f.Path)
		case err != nil:
			stale = append(stale, f.Path)
			readErrs = append(readErrs, fmt.Errorf("read %s: %w", f.Path, err))
		case !bytes.Equal(got, f.Content):
			stale = append(stale, f.Path)
		}
	}
	return stale, errors.Join(readErrs...)
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
