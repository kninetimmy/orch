// Package codexusage recovers exact completed Codex child-rollout totals.
package codexusage

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"
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
	if previousTotal != nil && *previousTotal < 0 {
		return 0, false
	}
	rollout, reason := findRollout(sessionsRoot, parentThreadID, taskIdentity)
	if reason != "" {
		return 0, false
	}
	total, valid := finalTotal(rollout.previous, rollout.last)
	if !valid {
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

// NativeCounters retains presence and native definitions, including cached input
// and reasoning output. These are independent counters, not additive buckets.
type NativeCounters struct {
	InputTokens           *int64 `json:"input_tokens,omitempty"`
	CachedInputTokens     *int64 `json:"cached_input_tokens,omitempty"`
	CacheWriteInputTokens *int64 `json:"cache_write_input_tokens,omitempty"`
	OutputTokens          *int64 `json:"output_tokens,omitempty"`
	ReasoningOutputTokens *int64 `json:"reasoning_output_tokens,omitempty"`
	TotalTokens           *int64 `json:"total_tokens,omitempty"`
}

type Capture struct {
	Session  string
	Source   json.RawMessage
	At       string
	Sequence int64
	Counters NativeCounters
	Reason   string
}

// Completed captures only an exact completed child. Record position and native
// timestamp make repeated reads stable without a conversational checkpoint.
// It deliberately does not infer an execution profile or activity interval.
func Completed(sessionsRoot, parentThreadID, taskIdentity string) Capture {
	if !validAgentPath(taskIdentity) || taskIdentity == "/root" || taskIdentity == "/morpheus" {
		return Capture{Reason: "child-identity-unavailable"}
	}
	rollout, reason := findRollout(sessionsRoot, parentThreadID, taskIdentity)
	if reason != "" {
		return Capture{Reason: reason}
	}
	out := Capture{Session: rollout.meta.ID, Source: rollout.meta.Source, Sequence: rollout.sequence - 1}
	var source subagentSource
	if json.Unmarshal(out.Source, &source) != nil ||
		(source.Subagent.ThreadSpawn.AgentPath != nil && *source.Subagent.ThreadSpawn.AgentPath != taskIdentity) {
		out.Reason = "conflicting-source-task-identity"
		return out
	}
	usage, valid := finalUsage(rollout.previous, rollout.last)
	if !valid || json.Unmarshal(usage, &out.Counters) != nil {
		out.Reason = "invalid-terminal-usage"
		return out
	}
	present := false
	for _, v := range []*int64{out.Counters.InputTokens, out.Counters.CachedInputTokens,
		out.Counters.CacheWriteInputTokens, out.Counters.OutputTokens,
		out.Counters.ReasoningOutputTokens, out.Counters.TotalTokens} {
		if v != nil {
			present = true
			if *v < 0 {
				out.Reason = "invalid-terminal-usage"
				return out
			}
		}
	}
	if !present {
		out.Reason = "native-counters-absent"
	} else if json.Unmarshal(rollout.previous.Timestamp, &out.At) != nil {
		out.Reason = "native-timestamp-unavailable"
	} else if _, err := time.Parse(time.RFC3339Nano, out.At); err != nil {
		out.Reason = "native-timestamp-unavailable"
	}
	return out
}

type completedRollout struct {
	meta           sessionMeta
	previous, last record
	sequence       int64
}

func findRollout(sessionsRoot, parentThreadID, taskIdentity string) (completedRollout, string) {
	if strings.TrimSpace(parentThreadID) == "" || strings.TrimSpace(taskIdentity) == "" {
		return completedRollout{}, "child-identity-unavailable"
	}

	var matches int
	var result completedRollout
	reason := "rollout-storage-unavailable"
	err := filepath.WalkDir(sessionsRoot, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || filepath.Ext(d.Name()) != ".jsonl" {
			return nil
		}

		rollout, candidate, valid := readRollout(path, parentThreadID, taskIdentity)
		if !candidate {
			return nil
		}
		if !valid {
			reason = "identified-rollout-invalid-or-unfinished"
			return errInvalidRollout
		}

		matches++
		if matches > 1 {
			reason = "duplicate-matching-rollouts"
			return errInvalidRollout
		}
		result = rollout
		return nil
	})
	if err != nil {
		return completedRollout{}, reason
	}
	if matches != 1 {
		return completedRollout{}, "matching-child-unavailable"
	}
	return result, ""
}

type record struct {
	Timestamp json.RawMessage `json:"timestamp"`
	Type      string          `json:"type"`
	Payload   json.RawMessage `json:"payload"`
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

// readRollout parses one JSONL rollout and reports whether it is a candidate
// for the requested child and, if so, whether it is wholly valid. A rollout
// only becomes a candidate once its session metadata identifies it as that
// child; until then any read or format failure means the file is unidentified,
// so it is not this capture's rollout and is left out of the result entirely.
// Once identified, every remaining check is fail-closed.
func readRollout(path, parentThreadID, taskIdentity string) (out completedRollout, candidate, valid bool) {
	f, err := os.Open(path)
	if err != nil {
		return out, false, false
	}
	defer func() { _ = f.Close() }()

	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 64*1024), maxJSONLRecord)

	var identified bool
	for scanner.Scan() {
		line := bytes.TrimSpace(scanner.Bytes())
		if len(line) == 0 {
			continue
		}
		out.sequence++

		var current record
		if err := json.Unmarshal(line, &current); err != nil {
			return out, identified, false
		}
		if current.Type == "session_meta" {
			if identified {
				return out, true, false
			}

			var meta sessionMeta
			if err := json.Unmarshal(current.Payload, &meta); err != nil {
				return out, false, false
			}
			if !identifies(meta, parentThreadID, taskIdentity) {
				return out, false, false
			}
			if !validSessionMeta(meta) || !sourceNamesParent(meta, parentThreadID) {
				return out, true, false
			}
			identified = true
			out.meta = meta
			continue
		}
		if identified {
			out.previous = out.last
			out.last = current
		}
	}
	if err := scanner.Err(); err != nil {
		return out, identified, false
	}
	if !identified {
		return out, false, false
	}

	_, valid = finalUsage(out.previous, out.last)
	return out, true, valid
}

func finalUsage(previous, last record) (json.RawMessage, bool) {
	if last.Type != "event_msg" || previous.Type != "event_msg" {
		return nil, false
	}
	var complete event
	var token struct {
		Type string `json:"type"`
		Info struct {
			Usage json.RawMessage `json:"total_token_usage"`
		} `json:"info"`
	}
	if json.Unmarshal(last.Payload, &complete) != nil || complete.Type != "task_complete" ||
		json.Unmarshal(previous.Payload, &token) != nil || token.Type != "token_count" {
		return nil, false
	}
	return token.Info.Usage, true
}

// SampleID is independent of run associations: changing an attempt or cycle for
// the same native evidence must conflict, not count the evidence a second time.
func (c Capture) SampleID() string {
	return fmt.Sprintf("codex:%s:tokens:%d", c.Session, c.Sequence)
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
