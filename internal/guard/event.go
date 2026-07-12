package guard

import (
	"fmt"
	"path/filepath"
)

// absTargets resolves each raw write-target path a decoder extracted
// from a PreToolUse event into an absolute path, shared by every host
// decoder (PathsFromClaudeEvent, PathsFromCodexEvent) so the
// resolution rule cannot drift between hosts. A relative path is joined
// against cwd; a relative path with no cwd in the payload, or an empty
// path, is an internal failure — the decoder cannot verify the target,
// so it must not be silently allowed through, and it maps to the hook
// protocol's blocking exit rather than a deny.
func absTargets(toolName, cwd string, raw []string) ([]string, error) {
	out := make([]string, 0, len(raw))
	for _, p := range raw {
		if p == "" {
			return nil, fmt.Errorf("PreToolUse %s: empty write path", toolName)
		}
		if !filepath.IsAbs(p) {
			if cwd == "" {
				return nil, fmt.Errorf("PreToolUse %s: relative path %q with no cwd in payload", toolName, p)
			}
			p = filepath.Join(cwd, p)
		}
		out = append(out, p)
	}
	return out, nil
}
