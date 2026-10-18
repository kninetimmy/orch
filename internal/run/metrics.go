package run

import (
	"fmt"
	"time"

	"github.com/kninetimmy/orch/internal/claudeusage"
	"github.com/kninetimmy/orch/internal/config"
	"github.com/kninetimmy/orch/internal/metrics"
	"github.com/kninetimmy/orch/internal/state"
)

// recordMetric appends a metrics event for this run when metrics are
// enabled. It runs at the very end of a verb's success path, after
// every state/GitHub mutation, so a recorded event always describes a
// completed verb; a failure is a post-mutation error (PRD §21 is
// fail-closed like everything else — disable metrics to bypass a
// broken disk).
func (c *verbCtx) recordMetric(ev metrics.Event) error {
	if !c.cfg.Metrics.Enabled {
		return nil
	}
	ev.At = c.env.nowStamp()
	if err := metrics.Append(c.env.RepoRoot, c.st.Run.ID, ev); err != nil {
		return wrapAfterMutation(fmt.Errorf("record metrics: %w", err))
	}
	return nil
}

// invalidSession stands in for a session id that is not a UUID. It is not a
// UUID either, so such a session still reports unavailable. shortcut: every
// non-UUID session of a run shares this one record; keep each apart only if a
// real host ever sends one.
const invalidSession = "invalid-session-id"

// recordArchitectSession records the invoking Claude Code session as one that
// ran a verb for a Claude-host run, when metrics are enabled.
func recordArchitectSession(env Env, cfg *config.Config, run *state.Run) error {
	if !cfg.Metrics.Enabled || run.Host != "claude" || env.ClaudeSession == "" {
		return nil
	}
	session := env.ClaudeSession
	if !claudeusage.ValidSession(session) {
		session = invalidSession
	}
	if err := metrics.AddArchitectSession(env.RepoRoot, run.ID, session); err != nil {
		return fmt.Errorf("record metrics: %w", err)
	}
	return nil
}

// recordArchitectUsage records, for a Claude-host run with metrics enabled,
// one architect-role observation per recorded session: its usage between the
// run's activation and end, or why that is unavailable.
func (c *verbCtx) recordArchitectUsage(end time.Time) error {
	run := c.st.Run
	if !c.cfg.Metrics.Enabled || run.Host != "claude" {
		return nil
	}
	sessions, err := metrics.ArchitectSessions(c.env.RepoRoot, run.ID)
	if err != nil {
		return wrapAfterMutation(fmt.Errorf("record metrics: %w", err))
	}
	for _, capture := range claudeusage.Architect(c.env.RepoRoot, sessions, run.StartedAt, end) {
		o := metrics.Observation{SchemaVersion: metrics.ObservationVersion, RunID: run.ID, ID: "claude-architect",
			At: end.Format(time.RFC3339Nano), Source: "claude-transcript", Role: "architect", Host: "claude", Session: capture.Session}
		if capture.Session != "" {
			o.ID += ":" + capture.Session
		}
		if capture.Reason != "" {
			o.Unavailable = &metrics.Unavailable{Reason: capture.Reason}
		} else {
			n := capture.Counters
			o.Sample = &metrics.CounterSample{Stream: "claude-main-assistant-usage", Mode: "delta", Sequence: 1,
				Counters: metrics.Counters{InputTokens: &n.Input, OutputTokens: &n.Output, CacheReadTokens: &n.CacheRead, CacheCreationTokens: &n.CacheCreation}}
		}
		if _, err := metrics.Record(c.env.RepoRoot, o); err != nil {
			return wrapAfterMutation(fmt.Errorf("record metrics: %w", err))
		}
	}
	return nil
}
