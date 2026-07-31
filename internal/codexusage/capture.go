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
// problem is unavailable rather than a best-effort attribution.
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

		candidateTotal, matched, valid, err := rolloutTotal(path, parentThreadID, taskIdentity)
		if err != nil {
			return err
		}
		if !matched {
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
			ParentThreadID string `json:"parent_thread_id"`
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

// rolloutTotal parses one JSONL rollout. A candidate must be a child session
// whose three persisted parent references and agent path exactly match.
func rolloutTotal(path, parentThreadID, taskIdentity string) (int64, bool, bool, error) {
	f, err := os.Open(path)
	if err != nil {
		return 0, false, false, err
	}
	defer func() { _ = f.Close() }()

	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 64*1024), maxJSONLRecord)

	var seenMeta, matched bool
	var previous, last record
	for scanner.Scan() {
		line := bytes.TrimSpace(scanner.Bytes())
		if len(line) == 0 {
			continue
		}

		var current record
		if err := json.Unmarshal(line, &current); err != nil {
			return 0, false, false, err
		}
		if current.Type == "session_meta" {
			if seenMeta {
				return 0, false, false, errInvalidRollout
			}
			seenMeta = true

			var meta sessionMeta
			if err := json.Unmarshal(current.Payload, &meta); err != nil {
				return 0, false, false, err
			}
			matched = matches(meta, parentThreadID, taskIdentity)
			continue
		}
		if matched {
			previous = last
			last = current
		}
	}
	if err := scanner.Err(); err != nil {
		return 0, false, false, err
	}
	if !matched {
		return 0, false, false, nil
	}

	total, valid := finalTotal(previous, last)
	return total, true, valid, nil
}

func matches(meta sessionMeta, parentThreadID, taskIdentity string) bool {
	if meta.ID == "" || meta.ID == parentThreadID ||
		meta.SessionID != parentThreadID ||
		meta.ParentThreadID != parentThreadID ||
		meta.ThreadSource != "subagent" ||
		meta.AgentPath != taskIdentity {
		return false
	}
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
