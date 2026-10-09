package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/kninetimmy/orch/internal/claudetranscript"
	"github.com/kninetimmy/orch/internal/grant"
)

// claudeHookInput is the part of Claude Code's hook stdin document the grant
// hooks use. Every field is absent on a document they cannot read.
type claudeHookInput struct {
	SessionID      string `json:"session_id"`
	TranscriptPath string `json:"transcript_path"`
	HookEventName  string `json:"hook_event_name"`
	Trigger        string `json:"trigger"`
	AgentID        string `json:"agent_id"`
}

// readClaudeHookInput reads the hook document from stdin. It reads nothing
// when stdin is a terminal (a person ran the verb by hand and would hang) and
// returns false on any read or decode failure.
func readClaudeHookInput(env Env) (claudeHookInput, bool) {
	var in claudeHookInput
	if f, ok := env.Stdin.(*os.File); ok {
		if info, err := f.Stat(); err != nil || info.Mode()&os.ModeCharDevice != 0 {
			return in, false
		}
	}
	data, err := io.ReadAll(io.LimitReader(env.Stdin, 1<<20))
	if err != nil || json.Unmarshal(data, &in) != nil {
		return claudeHookInput{}, false
	}
	return in, true
}

// activeGrantOrNil returns the active grant, or nil when there is none or the
// store cannot be read: every grant hook fails open.
func activeGrantOrNil(env Env, now time.Time) *grant.Grant {
	store, err := grant.Open(context.Background(), env.Runner, env.RepoRoot)
	if err != nil {
		return nil
	}
	g, err := store.Active(now)
	if err != nil {
		return nil
	}
	return g
}

// isHolder reports whether the hook document comes from the grant's current
// holder session. A document without a session id is never the holder.
func isHolder(g *grant.Grant, in claudeHookInput) bool {
	return in.SessionID != "" && in.SessionID == g.SessionID
}

// grantSessionContext is the text SessionStart appends while a grant is
// active, or "" when none is. It reads stdin only once a grant is known, so a
// repository without one behaves exactly as before.
func grantSessionContext(env Env, now time.Time) string {
	g := activeGrantOrNil(env, now)
	if g == nil {
		return ""
	}
	t := g.Terms
	text := fmt.Sprintf("An autonomy grant is active: %s, expiring %s (in %s). Remaining budgets: %d of %d runs, %d of %d merges.\n",
		t.ID, t.ExpiresAt.Format(time.RFC3339), t.ExpiresAt.Sub(now).Round(time.Minute),
		t.RunLimit-len(g.Runs), t.RunLimit, t.MergeLimit-len(g.Merges), t.MergeLimit)
	in, ok := readClaudeHookInput(env)
	switch {
	case !ok || in.SessionID == "":
		return text + "Whether this session is the current holder of the grant could not be determined.\n"
	case isHolder(g, in):
		return text + fmt.Sprintf("This session is the current holder of the grant. Before acting, read the latest handoff session note. Relay once your context passes %d tokens.\n", t.ContextThreshold)
	default:
		return text + "This session is not the current holder of the grant.\n"
	}
}

// hookContextCheck answers a PostToolUse or UserPromptSubmit event. In the
// holder session, once the main session's context passes the grant's
// threshold, it adds an instruction to relay at the next stopping point;
// otherwise it prints nothing. Fail-open: any failure prints nothing.
func hookContextCheck(env Env) error {
	in, ok := readClaudeHookInput(env)
	if !ok || in.AgentID != "" || (in.HookEventName != "PostToolUse" && in.HookEventName != "UserPromptSubmit") {
		return nil
	}
	g := activeGrantOrNil(env, time.Now())
	if g == nil || !isHolder(g, in) {
		return nil
	}
	tokens, ok := claudetranscript.ContextTokens(in.TranscriptPath)
	if !ok || tokens <= int64(g.Terms.ContextThreshold) {
		return nil
	}
	msg := fmt.Sprintf("Orch autonomy grant %s: this session's context (%d tokens) is past the grant's %d-token threshold. Relay at the next stopping point, meaning no subagent is in flight and no verb is half-done. Do not start new work first.",
		g.Terms.ID, tokens, g.Terms.ContextThreshold)
	return writeJSON(env.Stdout, map[string]any{"hookSpecificOutput": map[string]string{
		"hookEventName":     in.HookEventName,
		"additionalContext": msg,
	}})
}

// hookPreCompact answers a PreCompact event. In the holder session under an
// active grant it blocks automatic compaction with Claude Code's documented
// block response; manual compaction and every other session compact as usual.
// Fail-open: any failure prints nothing, which lets compaction proceed.
func hookPreCompact(env Env) error {
	in, ok := readClaudeHookInput(env)
	if !ok || in.Trigger != "auto" {
		return nil
	}
	g := activeGrantOrNil(env, time.Now())
	if g == nil || !isHolder(g, in) {
		return nil
	}
	return writeJSON(env.Stdout, map[string]string{
		"decision": "block",
		"reason":   fmt.Sprintf("Orch autonomy grant %s is active: this session must relay, not auto-compact.", g.Terms.ID),
	})
}
