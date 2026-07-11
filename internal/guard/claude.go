package guard

import (
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
)

// ErrUnknownTool reports a PreToolUse event whose tool_name guard does
// not extract a write target from. It is a deny (fail closed; adapters
// scope their hook matchers), not an internal failure — callers test for
// it with errors.Is and answer the hook with a denial rather than the
// blocking error exit.
var ErrUnknownTool = errors.New("unrecognized tool_name")

// claudeEvent is the subset of a Claude Code PreToolUse hook payload
// guard reads. It is decoded WITHOUT DisallowUnknownFields: host
// payloads carry many fields guard does not consume, unlike orch's own
// schema-versioned documents that reject unknown fields on principle.
type claudeEvent struct {
	ToolName  string          `json:"tool_name"`
	ToolInput json.RawMessage `json:"tool_input"`
	CWD       string          `json:"cwd"`
}

// PathsFromClaudeEvent decodes a raw PreToolUse event and returns the
// absolute write targets it declares. Targets are taken by tool_name:
// Write/Edit/MultiEdit from tool_input.file_path, NotebookEdit from
// tool_input.notebook_path. A relative target is resolved against the
// payload's cwd; a relative target with no cwd is an internal failure.
// An unrecognized tool_name returns an error wrapping ErrUnknownTool so
// the caller can deny by default.
func PathsFromClaudeEvent(payload []byte) ([]string, error) {
	var ev claudeEvent
	if err := json.Unmarshal(payload, &ev); err != nil {
		return nil, fmt.Errorf("parse PreToolUse event: %w", err)
	}

	raw, err := toolTargets(ev)
	if err != nil {
		return nil, err
	}

	out := make([]string, 0, len(raw))
	for _, p := range raw {
		if p == "" {
			return nil, fmt.Errorf("PreToolUse %s: empty write path", ev.ToolName)
		}
		if !filepath.IsAbs(p) {
			if ev.CWD == "" {
				return nil, fmt.Errorf("PreToolUse %s: relative path %q with no cwd in payload", ev.ToolName, p)
			}
			p = filepath.Join(ev.CWD, p)
		}
		out = append(out, p)
	}
	return out, nil
}

// toolTargets extracts the raw path field(s) each write tool carries.
func toolTargets(ev claudeEvent) ([]string, error) {
	switch ev.ToolName {
	case "Write", "Edit", "MultiEdit":
		var in struct {
			FilePath string `json:"file_path"`
		}
		if err := json.Unmarshal(ev.ToolInput, &in); err != nil {
			return nil, fmt.Errorf("parse %s tool_input: %w", ev.ToolName, err)
		}
		return []string{in.FilePath}, nil
	case "NotebookEdit":
		var in struct {
			NotebookPath string `json:"notebook_path"`
		}
		if err := json.Unmarshal(ev.ToolInput, &in); err != nil {
			return nil, fmt.Errorf("parse %s tool_input: %w", ev.ToolName, err)
		}
		return []string{in.NotebookPath}, nil
	default:
		return nil, fmt.Errorf("%w %q; guard denies by default", ErrUnknownTool, ev.ToolName)
	}
}
