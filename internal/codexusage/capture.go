// Package codexusage recovers exact completed Codex child-rollout totals.
package codexusage

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

const maxJSONLRecord = 8 * 1024 * 1024

var errInvalidRollout = errors.New("invalid Codex rollout")

// TotalTokens returns a completed child rollout's exact total only when
// sessionsRoot contains exactly one valid rollout for parentThreadID and
// taskIdentity. When previousTotal is set, it returns the exact non-negative
// difference from that earlier cumulative total. Every persistence or format
// problem in a rollout that identifies itself as that child is unavailable
// rather than a best-effort attribution; a rollout that does not identify
// itself as that child is another session's business and cannot affect the
// result, however malformed it is.
func TotalTokens(sessionsRoot, parentThreadID, taskIdentity string, previousTotal *int64) (int64, bool) {
	if strings.TrimSpace(parentThreadID) == "" || strings.TrimSpace(taskIdentity) == "" {
		return 0, false
	}
	if previousTotal != nil && *previousTotal < 0 {
		return 0, false
	}

	var matches int
	var total int64
	err := filepath.WalkDir(sessionsRoot, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || filepath.Ext(d.Name()) != ".jsonl" {
			return nil
		}

		candidateTotal, candidate, valid := rolloutTotal(path, parentThreadID, taskIdentity)
		if !candidate {
			return nil
		}
		if !valid {
			return errInvalidRollout
		}

		matches++
		if matches > 1 {
			return errInvalidRollout
		}
		total = candidateTotal
		return nil
	})
	if err != nil || matches != 1 {
		return 0, false
	}
	if previousTotal != nil {
		if total < *previousTotal {
			return 0, false
		}
		return total - *previousTotal, true
	}
	return total, true
}

type record struct {
	Type    string          `json:"type"`
	Payload json.RawMessage `json:"payload"`
}

type sessionMeta struct {
	ID             string          `json:"id"`
	SessionID      string          `json:"session_id"`
	ParentThreadID string          `json:"parent_thread_id"`
	ThreadSource   string          `json:"thread_source"`
	AgentPath      string          `json:"agent_path"`
	Source         json.RawMessage `json:"source"`
}

type subagentSource struct {
	Subagent struct {
		ThreadSpawn struct {
			ParentThreadID string  `json:"parent_thread_id"`
			Depth          *int32  `json:"depth"`
			AgentPath      *string `json:"agent_path"`
			AgentNickname  *string `json:"agent_nickname"`
			AgentRole      *string `json:"agent_role"`
			AgentType      *string `json:"agent_type"`
		} `json:"thread_spawn"`
	} `json:"subagent"`
}

type event struct {
	Type string `json:"type"`
	Info struct {
		TotalTokenUsage struct {
			TotalTokens *int64 `json:"total_tokens"`
		} `json:"total_token_usage"`
	} `json:"info"`
}

// rolloutTotal parses one JSONL rollout and reports whether it is a candidate
// for the requested child and, if so, whether it is wholly valid. A rollout
// only becomes a candidate once its session metadata identifies it as that
// child; until then any read or format failure means the file is unidentified,
// so it is not this capture's rollout and is left out of the result entirely.
// Once identified, every remaining check is fail-closed.
func rolloutTotal(path, parentThreadID, taskIdentity string) (int64, bool, bool) {
	f, err := os.Open(path)
	if err != nil {
		return 0, false, false
	}
	defer func() { _ = f.Close() }()

	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 64*1024), maxJSONLRecord)

	var identified bool
	var previous, last record
	for scanner.Scan() {
		line := bytes.TrimSpace(scanner.Bytes())
		if len(line) == 0 {
			continue
		}

		var current record
		if err := json.Unmarshal(line, &current); err != nil {
			return 0, identified, false
		}
		if current.Type == "session_meta" {
			if identified {
				return 0, true, false
			}

			var meta sessionMeta
			if err := json.Unmarshal(current.Payload, &meta); err != nil {
				return 0, false, false
			}
			if !identifies(meta, parentThreadID, taskIdentity) {
				return 0, false, false
			}
			if !validSessionMeta(meta) || !sourceNamesParent(meta, parentThreadID) {
				return 0, true, false
			}
			identified = true
			continue
		}
		if identified {
			previous = last
			last = current
		}
	}
	if err := scanner.Err(); err != nil {
		return 0, identified, false
	}
	if !identified {
		return 0, false, false
	}

	total, valid := finalTotal(previous, last)
	return total, true, valid
}

func validSessionMeta(meta sessionMeta) bool {
	sourceParent, threadSpawn, valid := parseSessionSource(meta.Source)
	if meta.ID == "" || !valid {
		return false
	}
	if !threadSpawn {
		return true
	}
	return meta.ThreadSource == "subagent" &&
		meta.SessionID != "" &&
		meta.ParentThreadID != "" &&
		meta.AgentPath != "" &&
		meta.ID != meta.SessionID &&
		meta.SessionID == meta.ParentThreadID &&
		meta.ParentThreadID == sourceParent
}

func parseSessionSource(raw json.RawMessage) (string, bool, bool) {
	if len(raw) == 0 {
		return "", false, true
	}

	var source any
	if err := json.Unmarshal(raw, &source); err != nil {
		return "", false, false
	}
	switch source := source.(type) {
	case string:
		switch source {
		case "custom", "internal", "subagent":
			return "", false, false
		default:
			return "", false, true
		}
	case map[string]any:
		if len(source) != 1 {
			return "", false, false
		}
		for kind, payload := range source {
			switch kind {
			case "custom":
				_, valid := payload.(string)
				return "", false, valid
			case "internal":
				internal, valid := payload.(string)
				return "", false, valid && internal == "memory_consolidation"
			case "subagent":
				switch payload := payload.(type) {
				case string:
					return "", false, payload == "review" ||
						payload == "compact" ||
						payload == "memory_consolidation"
				case map[string]any:
					if len(payload) != 1 {
						return "", false, false
					}
					if other, ok := payload["other"]; ok {
						_, valid := other.(string)
						return "", false, valid
					}
					spawn, ok := payload["thread_spawn"].(map[string]any)
					if !ok {
						return "", false, false
					}
					_, hasAgentRole := spawn["agent_role"]
					_, hasAgentType := spawn["agent_type"]
					if hasAgentRole && hasAgentType {
						return "", false, false
					}

					var typed subagentSource
					if err := json.Unmarshal(raw, &typed); err != nil {
						return "", false, false
					}
					threadSpawn := typed.Subagent.ThreadSpawn
					if threadSpawn.ParentThreadID == "" || threadSpawn.Depth == nil {
						return "", false, false
					}
					if threadSpawn.AgentPath != nil && !validAgentPath(*threadSpawn.AgentPath) {
						return "", false, false
					}
					return threadSpawn.ParentThreadID, true, true
				}
			}
		}
	}
	return "", false, false
}

func validAgentPath(path string) bool {
	if path == "/morpheus" || path == "/root" {
		return true
	}
	if !strings.HasPrefix(path, "/root/") || strings.HasSuffix(path, "/") {
		return false
	}
	for _, segment := range strings.Split(strings.TrimPrefix(path, "/root/"), "/") {
		if segment == "" || segment == "root" || segment == "." || segment == ".." {
			return false
		}
		for _, char := range segment {
			if (char < 'a' || char > 'z') &&
				(char < '0' || char > '9') &&
				char != '_' {
				return false
			}
		}
	}
	return true
}

// identifies reports whether a rollout's own id, its two persisted parent
// references and its agent path name it as the requested child. This is the
// only question a rollout has to answer before it can affect the result; the
// remaining parent reference in its spawn source is validated afterwards.
func identifies(meta sessionMeta, parentThreadID, taskIdentity string) bool {
	return meta.ID != "" &&
		meta.ID != parentThreadID &&
		meta.SessionID == parentThreadID &&
		meta.ParentThreadID == parentThreadID &&
		meta.ThreadSource == "subagent" &&
		meta.AgentPath == taskIdentity
}

// sourceNamesParent reports whether an identified rollout's spawn source agrees
// with the parent references it identified itself by.
func sourceNamesParent(meta sessionMeta, parentThreadID string) bool {
	var source subagentSource
	if err := json.Unmarshal(meta.Source, &source); err != nil {
		return false
	}
	return source.Subagent.ThreadSpawn.ParentThreadID == parentThreadID
}

// finalTotal accepts only the terminal pair Codex currently persists: the
// final exact token_count followed by task_complete.
func finalTotal(previous, last record) (int64, bool) {
	if last.Type != "event_msg" || previous.Type != "event_msg" {
		return 0, false
	}

	var complete event
	if err := json.Unmarshal(last.Payload, &complete); err != nil || complete.Type != "task_complete" {
		return 0, false
	}

	var token event
	if err := json.Unmarshal(previous.Payload, &token); err != nil || token.Type != "token_count" {
		return 0, false
	}
	if token.Info.TotalTokenUsage.TotalTokens == nil || *token.Info.TotalTokenUsage.TotalTokens < 0 {
		return 0, false
	}
	return *token.Info.TotalTokenUsage.TotalTokens, true
}
