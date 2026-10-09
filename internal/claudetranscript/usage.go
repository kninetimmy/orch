// Package claudetranscript reads a Claude Code session transcript to learn how
// large the main session's context is. The transcript format is internal to
// Claude Code and undocumented, so this reader recognizes only the versions
// listed in supportedVersions and reports nothing for anything else.
package claudetranscript

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"os"
	"slices"
)

// supportedVersions are the Claude Code versions whose transcript format this
// reader was checked against (via testdata/usage_2.1.295.jsonl). Add a version
// only after confirming its usage entries still have this shape.
var supportedVersions = []string{"2.1.295"}

// tailBytes bounds how much of the transcript end is read: transcripts grow
// large and hooks run often. shortcut: when no main-session assistant entry
// fits in the tail the size is reported as unknown, not searched for further.
const tailBytes = 2 << 20

// entry is the part of one transcript line this reader looks at.
type entry struct {
	Type        string `json:"type"`
	IsSidechain bool   `json:"isSidechain"`
	Version     string `json:"version"`
	Message     struct {
		Usage *struct {
			Input         *int64 `json:"input_tokens"`
			CacheCreation *int64 `json:"cache_creation_input_tokens"`
			CacheRead     *int64 `json:"cache_read_input_tokens"`
		} `json:"usage"`
	} `json:"message"`
}

// ContextTokens returns the main session's current context size: the input,
// cache-creation and cache-read tokens of its latest assistant entry, output
// tokens excluded and subagent (sidechain) entries ignored. ok is false when
// the file is unreadable, holds no main-session assistant entry in its tail, or
// that latest entry is from an unrecognized version or lacks the usage fields.
func ContextTokens(path string) (tokens int64, ok bool) {
	f, err := os.Open(path)
	if err != nil {
		return 0, false
	}
	defer func() { _ = f.Close() }()
	info, err := f.Stat()
	if err != nil {
		return 0, false
	}
	start := max(info.Size()-tailBytes, 0)
	if _, err := f.Seek(start, io.SeekStart); err != nil {
		return 0, false
	}
	r := bufio.NewReader(f)
	if start > 0 {
		if _, err := r.ReadBytes('\n'); err != nil { // drop the partial first line
			return 0, false
		}
	}
	for {
		line, err := r.ReadBytes('\n')
		if t, recognized, isAssistant := parse(line); isAssistant {
			tokens, ok = t, recognized
		}
		if errors.Is(err, io.EOF) {
			return tokens, ok
		}
		if err != nil {
			return 0, false
		}
	}
}

// parse reports whether line is a main-session assistant entry and, if so,
// whether it is a recognized one and the context size it records.
func parse(line []byte) (tokens int64, recognized, isAssistant bool) {
	line = bytes.TrimSpace(line)
	if len(line) == 0 {
		return 0, false, false
	}
	var e entry
	if json.Unmarshal(line, &e) != nil || e.Type != "assistant" || e.IsSidechain {
		return 0, false, false
	}
	u := e.Message.Usage
	if !slices.Contains(supportedVersions, e.Version) || u == nil || u.Input == nil || u.CacheCreation == nil || u.CacheRead == nil {
		return 0, false, true
	}
	return *u.Input + *u.CacheCreation + *u.CacheRead, true, true
}
