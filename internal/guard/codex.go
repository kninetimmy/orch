package guard

import (
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
)

// ErrPatchEnvelope reports an apply_patch tool_input whose command
// envelope guard cannot parse into at least one write target: missing
// "*** Begin Patch"/"*** End Patch" framing, a directive line guard does
// not recognize, or a directive with an empty path. Like ErrUnknownTool,
// it is a deny (fail closed), not an internal failure: an upstream
// envelope format guard's parser does not yet understand must degrade to
// a denial the agent can report, never a silent allow.
var ErrPatchEnvelope = errors.New("malformed apply_patch envelope")

// codexEvent is the subset of a Codex CLI PreToolUse hook payload guard
// reads. It is decoded WITHOUT DisallowUnknownFields, for the same
// reason as claudeEvent: host payloads carry many fields guard does not
// consume, unlike orch's own schema-versioned documents that reject
// unknown fields on principle.
type codexEvent struct {
	ToolName  string          `json:"tool_name"`
	ToolInput json.RawMessage `json:"tool_input"`
	CWD       string          `json:"cwd"`
}

// codexInterceptedTools is the single source of truth for the Codex CLI
// tool_name values guard extracts a write target from. Codex CLI routes
// every file mutation — create, update, delete, rename — through
// "apply_patch"; "Bash" and "mcp__<server>__<tool>" carry no parsed path
// list and are ErrUnknownTool.
var codexInterceptedTools = map[string]bool{
	"apply_patch": true,
}

// CodexTools returns, sorted, the Codex CLI tool_name values guard
// extracts a write target from. It exists so the Codex adapter's
// hooks.json PreToolUse matcher can be pinned against the exact dispatch
// set by import instead of duplicating the tool list — the same purpose
// ClaudeTools serves for the Claude adapter.
func CodexTools() []string {
	names := make([]string, 0, len(codexInterceptedTools))
	for name := range codexInterceptedTools {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// PathsFromCodexEvent decodes a raw PreToolUse event and returns the
// absolute write targets it declares. Only tool_name "apply_patch"
// carries a write target: its tool_input.command is the raw patch
// envelope string, parsed by parseApplyPatchPaths. A relative target is
// resolved against the payload's cwd; a relative target with no cwd is
// an internal failure. An unrecognized tool_name — including "Bash" and
// any "mcp__*" tool — returns an error wrapping ErrUnknownTool so the
// caller can deny by default; inspecting Bash commands for write targets
// is deliberately out of scope here (task 27).
func PathsFromCodexEvent(payload []byte) ([]string, error) {
	var ev codexEvent
	if err := json.Unmarshal(payload, &ev); err != nil {
		return nil, fmt.Errorf("parse PreToolUse event: %w", err)
	}

	if !codexInterceptedTools[ev.ToolName] {
		return nil, fmt.Errorf("%w %q; guard denies by default", ErrUnknownTool, ev.ToolName)
	}

	var in struct {
		Command string `json:"command"`
	}
	if err := json.Unmarshal(ev.ToolInput, &in); err != nil {
		return nil, fmt.Errorf("parse %s tool_input: %w", ev.ToolName, err)
	}

	raw, err := parseApplyPatchPaths(in.Command)
	if err != nil {
		return nil, err
	}

	return absTargets(ev.ToolName, ev.CWD, raw)
}

// applyPatchDirectives lists the envelope directive prefixes
// parseApplyPatchPaths treats as write-target lines, keeping the
// recognized-directive set in one place alongside the loop that reads
// it. Any other "*** "-prefixed line (bar the Begin/End framing) is an
// unrecognized directive and fails closed.
var applyPatchDirectives = []string{
	"*** Add File: ",
	"*** Update File: ",
	"*** Delete File: ",
	"*** Move to: ",
}

// parseApplyPatchPaths line-scans a raw apply_patch envelope string and
// returns every write-target path it declares, in envelope order.
// Duplicates (e.g. an Update File immediately followed by its own Move
// to) are returned as-is — Checker evaluates each path independently, so
// there is nothing to dedupe.
//
// The envelope must open with "*** Begin Patch" (the first non-empty
// line) and contain "*** End Patch"; either missing is ErrPatchEnvelope.
// Recognized directive lines are "*** Add File: <p>", "*** Update File:
// <p>", "*** Delete File: <p>", and "*** Move to: <p>" — Move collects
// its destination in addition to the Update path it renames, since both
// are write targets. Hunk body lines (@@ headers, +/-/context lines) do
// not start with "*** " and are skipped, never parsed as paths. Any
// other line starting with "*** " (other than Begin/End Patch) is an
// unrecognized directive: an upstream format addition must degrade to a
// deny, never a silent allow, so it is ErrPatchEnvelope. An empty path
// after a directive's colon is likewise ErrPatchEnvelope, as is an
// envelope that yields zero paths. Each line has at most one trailing
// "\r" trimmed before matching, tolerating a CRLF envelope; path text
// itself is not trimmed beyond the single space the directive syntax
// specifies after the colon.
func parseApplyPatchPaths(envelope string) ([]string, error) {
	lines := strings.Split(envelope, "\n")
	var paths []string
	sawFirstNonEmpty := false
	sawEnd := false

	for _, raw := range lines {
		line := strings.TrimSuffix(raw, "\r")

		if !sawFirstNonEmpty {
			if line == "" {
				continue
			}
			sawFirstNonEmpty = true
			if line != "*** Begin Patch" {
				return nil, fmt.Errorf("%w: envelope must open with \"*** Begin Patch\", got %q", ErrPatchEnvelope, line)
			}
			continue
		}

		if !strings.HasPrefix(line, "*** ") {
			continue // hunk body: @@ header, +/-/context line, or blank
		}
		if line == "*** End Patch" {
			sawEnd = true
			continue
		}

		matched := false
		for _, prefix := range applyPatchDirectives {
			if !strings.HasPrefix(line, prefix) {
				continue
			}
			matched = true
			p := strings.TrimPrefix(line, prefix)
			if p == "" {
				return nil, fmt.Errorf("%w: empty path after %q", ErrPatchEnvelope, strings.TrimSuffix(prefix, ": "))
			}
			paths = append(paths, p)
			break
		}
		if !matched {
			return nil, fmt.Errorf("%w: unrecognized directive %q", ErrPatchEnvelope, line)
		}
	}

	if !sawFirstNonEmpty {
		return nil, fmt.Errorf("%w: empty envelope", ErrPatchEnvelope)
	}
	if !sawEnd {
		return nil, fmt.Errorf("%w: missing \"*** End Patch\"", ErrPatchEnvelope)
	}
	if len(paths) == 0 {
		return nil, fmt.Errorf("%w: no write targets found", ErrPatchEnvelope)
	}
	return paths, nil
}
