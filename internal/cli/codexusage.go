package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/kninetimmy/orch/internal/codexusage"
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

// runCodexSubagentUsage reads the Codex adapter's JSON request and always
// reports either one exact total or an empty object. Rollout persistence is an
// optional host facility, so unavailable data is not a command failure.
func runCodexSubagentUsage(env Env) error {
	stdin := env.Stdin
	if stdin == nil {
		stdin = strings.NewReader("")
	}
	var req codexSubagentUsageRequest
	dec := json.NewDecoder(stdin)
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
	data, err := json.Marshal(out)
	if err != nil {
		return fmt.Errorf("encode Codex subagent usage: %w", err)
	}
	_, err = fmt.Fprintf(env.Stdout, "%s\n", data)
	return err
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
