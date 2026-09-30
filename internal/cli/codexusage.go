package cli

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/kninetimmy/orch/internal/codexusage"
	"github.com/kninetimmy/orch/internal/metrics"
)

// codexSubagentUsageRequest identifies one completed Codex child rollout.
// Both values come from the host: CODEX_THREAD_ID in the parent session and
// the canonical task identity returned by spawn_agent.
type codexSubagentUsageRequest struct {
	ParentThreadID      string `json:"parent_thread_id"`
	TaskIdentity        string `json:"task_identity"`
	PreviousTotalTokens *int64 `json:"previous_total_tokens,omitempty"`
}

// codexSubagentUsageResponse omits TotalTokens when the persisted rollout
// cannot be attributed exactly.
type codexSubagentUsageResponse struct {
	TotalTokens *int64 `json:"total_tokens,omitempty"`
}

// runCodexSubagentUsage keeps the legacy total/empty response and selects the
// observation shape only for explicit v2 requests. Missing optional host
// persistence remains unavailable data, not a command failure.
func runCodexSubagentUsage(env Env) error {
	stdin := env.Stdin
	if stdin == nil {
		stdin = strings.NewReader("")
	}
	data, err := io.ReadAll(stdin)
	if err != nil {
		return fmt.Errorf("read Codex capture request: %w", err)
	}
	var header map[string]json.RawMessage
	if json.Unmarshal(data, &header) != nil {
		return usageError(hookUsage)
	}
	if version, exists := header["schema_version"]; exists {
		if string(version) != "2" {
			return usageError("Codex capture supports schema_version 2 or the unversioned legacy request")
		}
		return runCodexObservation(env, data)
	}
	var req codexSubagentUsageRequest
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&req); err != nil {
		return usageError(hookUsage)
	}
	if err := dec.Decode(&struct{}{}); err != io.EOF {
		return usageError(hookUsage)
	}
	if strings.TrimSpace(req.ParentThreadID) == "" || strings.TrimSpace(req.TaskIdentity) == "" {
		return usageError(hookUsage)
	}

	out := codexSubagentUsageResponse{}
	if sessionsRoot, ok := codexSessionsRoot(); ok {
		if total, ok := codexusage.TotalTokens(sessionsRoot, req.ParentThreadID, req.TaskIdentity, req.PreviousTotalTokens); ok {
			out.TotalTokens = &total
		}
	}
	data, err = json.Marshal(out)
	if err != nil {
		return fmt.Errorf("encode Codex subagent usage: %w", err)
	}
	_, err = fmt.Fprintf(env.Stdout, "%s\n", data)
	return err
}

type codexObservationRequest struct {
	SchemaVersion  int    `json:"schema_version"`
	ParentThreadID string `json:"parent_thread_id"`
	TaskIdentity   string `json:"task_identity"`
	RunID          string `json:"run_id"`
	IssueNumber    int    `json:"issue_number,omitempty"`
	Role           string `json:"role"`
	Attempt        string `json:"attempt,omitempty"`
	ReviewCycle    int    `json:"review_cycle,omitempty"`
	UnavailableID  string `json:"unavailable_id"`
	UnavailableAt  string `json:"unavailable_at"`
}

type codexObservationResponse struct {
	SchemaVersion int                 `json:"schema_version"`
	Observation   metrics.Observation `json:"observation"`
	NativeSource  json.RawMessage     `json:"native_source,omitempty"`
}

// Capture is read-only. The caller submits the returned observation unchanged
// through metrics record, which owns run association, serialization and replay.
func runCodexObservation(env Env, data []byte) error {
	var req codexObservationRequest
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&req); err != nil {
		return usageError(hookUsage)
	}
	if err := dec.Decode(&struct{}{}); err != io.EOF {
		return usageError(hookUsage)
	}
	if req.Role == "" || strings.TrimSpace(req.ParentThreadID) == "" || strings.TrimSpace(req.TaskIdentity) == "" {
		return usageError(hookUsage)
	}
	if (req.Role == "implementer" || req.Role == "specialist" || req.Role == "reviewer") &&
		(req.IssueNumber <= 0 || req.Attempt == "" || (req.Role == "reviewer" && req.ReviewCycle <= 0)) {
		return usageError("Codex executor/reviewer capture requires issue_number and attempt; reviewer also requires review_cycle")
	}
	o := metrics.Observation{SchemaVersion: 2, RunID: req.RunID, ID: req.UnavailableID,
		At: req.UnavailableAt, Source: "codex-session-log", Host: "codex",
		IssueNumber: req.IssueNumber, Role: req.Role, Attempt: req.Attempt, ReviewCycle: req.ReviewCycle,
		Unavailable: &metrics.Unavailable{Reason: "rollout-storage-unavailable"}}
	if err := o.Validate(); err != nil {
		return err
	}
	var capture codexusage.Capture
	if root, ok := codexSessionsRoot(); ok {
		capture = codexusage.Completed(root, req.ParentThreadID, req.TaskIdentity)
	} else {
		capture.Reason = "rollout-storage-unavailable"
	}
	if capture.Reason != "" {
		o.Unavailable.Reason = capture.Reason
	} else {
		o.ID, o.At, o.Session = capture.SampleID(), capture.At, capture.Session
		o.Unavailable = nil
		c := capture.Counters
		o.Sample = &metrics.CounterSample{Stream: "codex-total-token-usage", Mode: "cumulative", Sequence: capture.Sequence,
			Counters: metrics.Counters{InputTokens: c.InputTokens, OutputTokens: c.OutputTokens,
				CacheReadTokens: c.CachedInputTokens, CacheCreationTokens: c.CacheWriteInputTokens,
				ReasoningOutputTokens: c.ReasoningOutputTokens, TotalTokens: c.TotalTokens}}
	}
	if err := o.Validate(); err != nil {
		// The request was already validated. Invalid native identity must stay
		// unavailable, just like other malformed rollout evidence.
		o.ID, o.At, o.Session = req.UnavailableID, req.UnavailableAt, ""
		o.Sample = nil
		o.Unavailable = &metrics.Unavailable{Reason: "invalid-native-identity"}
	}
	return json.NewEncoder(env.Stdout).Encode(codexObservationResponse{SchemaVersion: 2, Observation: o, NativeSource: capture.Source})
}

func codexSessionsRoot() (string, bool) {
	if home := os.Getenv("CODEX_HOME"); home != "" {
		return filepath.Join(home, "sessions"), true
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", false
	}
	return filepath.Join(home, ".codex", "sessions"), true
}
