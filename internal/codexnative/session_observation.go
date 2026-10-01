package codexnative

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"time"

	"github.com/kninetimmy/orch/internal/metrics"
)

// These camelCase counters are the installed app-server's thread totals, not
// session-log counters. Decode presence even where the native schema has a
// default: an absent cacheWriteInputTokens must not become measured zero.
type nativeCounters struct {
	Input     *int64 `json:"inputTokens"`
	Output    *int64 `json:"outputTokens"`
	CacheRead *int64 `json:"cachedInputTokens"`
	CacheNew  *int64 `json:"cacheWriteInputTokens"`
	Total     *int64 `json:"totalTokens"`
	Reasoning *int64 `json:"reasoningOutputTokens"`
}

func (s *Session) observation(key string) metrics.Observation {
	id := sha256.Sum256([]byte(s.task.ID + "\x00" + s.result.ThreadID + "\x00" + s.result.TurnID + "\x00" + key))
	requested := metrics.Profile{Model: s.task.Selection.Model, Effort: s.task.Selection.Effort}
	o := metrics.Observation{SchemaVersion: metrics.ObservationVersion, RunID: s.task.RunID,
		ID: fmt.Sprintf("codex-native:%x", id), At: time.Now().UTC().Format(time.RFC3339Nano), Source: "codex-app-server",
		IssueNumber: s.task.IssueNumber, Role: metricRole(s.task.Role), Attempt: s.task.Attempt, ReviewCycle: s.task.ReviewCycle,
		Host: "codex", Session: s.result.ThreadID, Requested: &requested}
	if s.result.Observed.Model != "" || s.result.Observed.Effort != "" {
		observed := s.result.Observed
		o.Observed = &observed
	}
	return o
}

func (s *Session) tokenUsage(data json.RawMessage) error {
	var event struct {
		Usage struct {
			Total *nativeCounters `json:"total"`
		} `json:"tokenUsage"`
	}
	if err := json.Unmarshal(data, &event); err != nil {
		return ErrMalformedMessage
	}
	counters := nativeCounters{}
	if event.Usage.Total != nil {
		counters = *event.Usage.Total
	}
	encoded, _ := json.Marshal(counters)
	// Native notifications have no event ID, sequence or timestamp. Deduplicate
	// identical totals for this turn, retaining the original receipt timestamp
	// and local sequence on reconnect. Session objects, not a second disk store,
	// own this replay history; a fresh process cannot safely reconstruct it.
	key := s.result.TurnID + ":tokens:" + string(encoded)
	if s.samples[key] {
		return nil
	}
	o := s.observation(key)
	if counters.Input == nil && counters.Output == nil && counters.CacheRead == nil && counters.CacheNew == nil && counters.Total == nil && counters.Reasoning == nil {
		o.Unavailable = &metrics.Unavailable{Reason: "native-counters-unavailable"}
	} else {
		o.Sample = &metrics.CounterSample{Stream: "codex-app-server-thread-total-token-usage", Mode: "cumulative", Sequence: s.sequence + 1,
			Counters: metrics.Counters{InputTokens: counters.Input, OutputTokens: counters.Output, CacheReadTokens: counters.CacheRead,
				CacheCreationTokens: counters.CacheNew, TotalTokens: counters.Total, ReasoningOutputTokens: counters.Reasoning}}
	}
	if err := s.appendObservation(o); err != nil {
		return err
	}
	s.samples[key] = true
	if o.Sample != nil {
		s.sequence++
	}
	return nil
}

func (s *Session) appendObservation(o metrics.Observation) error {
	if len(s.result.Observations) >= maxSessionEvents {
		return fmt.Errorf("codex session observation bound exceeded")
	}
	// Reuse the recorder's closed validation and cumulative arithmetic. This
	// package never records usage, writes metrics, or submits legacy Usage.
	// ponytail: history replay is quadratic over at most 1,024 events; index
	// only if measured session sizes need a larger bound.
	history := append(append([]metrics.Observation(nil), s.result.Observations...), o)
	if _, err := metrics.CounterContributions(history); err != nil {
		return err
	}
	s.result.Observations = history
	return nil
}

func (s *Session) terminalObservation() error {
	o := s.observation(fmt.Sprintf("terminal:%d:%s", len(s.result.Observations), s.result.Outcome))
	if s.result.Outcome == SessionFailed {
		o.Outcome = "infrastructure-failure"
	} else {
		reason := "native-session-" + string(s.result.Outcome)
		if s.result.Outcome == SessionSuccessful {
			reason = "native-completion-not-verification"
		}
		o.Unavailable = &metrics.Unavailable{Reason: reason}
	}
	return s.appendObservation(o)
}
