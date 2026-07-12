package guard

import (
	"encoding/json"
	"errors"
	"fmt"
	"sort"
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

// claudeToolPathField maps each Claude Code write tool_name guard
// understands to the tool_input field carrying its write target. It is
// the single source for both toolTargets dispatch and ClaudeTools, so
// the two cannot drift apart.
var claudeToolPathField = map[string]string{
	"Write":        "file_path",
	"Edit":         "file_path",
	"MultiEdit":    "file_path",
	"NotebookEdit": "notebook_path",
}

// ClaudeTools returns, sorted, the Claude Code tool_name values guard
// extracts a write target from. Any other tool_name is ErrUnknownTool
// (deny by default). It exists so the Claude adapter's plugin_test.go
// can pin the hooks.json PreToolUse matcher against the exact dispatch
// set by import instead of duplicating the tool list.
func ClaudeTools() []string {
	names := make([]string, 0, len(claudeToolPathField))
	for name := range claudeToolPathField {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
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

	return absTargets(ev.ToolName, ev.CWD, raw)
}

// toolTargets extracts the raw path field(s) each write tool carries,
// dispatching on claudeToolPathField. A tool_name absent from the table
// is ErrUnknownTool; a missing path field decodes to "" and is rejected
// as an empty write path by the caller.
func toolTargets(ev claudeEvent) ([]string, error) {
	field, ok := claudeToolPathField[ev.ToolName]
	if !ok {
		return nil, fmt.Errorf("%w %q; guard denies by default", ErrUnknownTool, ev.ToolName)
	}
	var in map[string]json.RawMessage
	if err := json.Unmarshal(ev.ToolInput, &in); err != nil {
		return nil, fmt.Errorf("parse %s tool_input: %w", ev.ToolName, err)
	}
	var path string
	if raw, ok := in[field]; ok {
		if err := json.Unmarshal(raw, &path); err != nil {
			return nil, fmt.Errorf("parse %s tool_input: %w", ev.ToolName, err)
		}
	}
	return []string{path}, nil
}
