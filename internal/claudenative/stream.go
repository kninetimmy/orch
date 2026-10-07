package claudenative

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/kninetimmy/orch/internal/metrics"
	"github.com/kninetimmy/orch/internal/nativehost"
)

const (
	maxEvents       = 1 << 16
	maxObservations = 1024
	usageStream     = "claude-code-session-model-usage"
)

// streamEvent is the subset of stream-json output this bridge reads. Pointer
// and slice fields keep "absent" distinct from a reported empty value.
type streamEvent struct {
	Type      string `json:"type"`
	Subtype   string `json:"subtype"`
	SessionID string `json:"session_id"`
	// system/init
	Cwd            *string           `json:"cwd"`
	Tools          []string          `json:"tools"`
	MCPServers     []json.RawMessage `json:"mcp_servers"`
	Model          *string           `json:"model"`
	PermissionMode *string           `json:"permissionMode"`
	APIKeySource   *string           `json:"apiKeySource"`
	Version        string            `json:"claude_code_version"`
	Plugins        []struct {
		Path   string `json:"path"`
		Source string `json:"source"`
	} `json:"plugins"`
	// assistant
	Message *struct {
		Model   string `json:"model"`
		Content []struct {
			Type string `json:"type"`
			Name string `json:"name"`
		} `json:"content"`
	} `json:"message"`
	ParentToolUseID *string `json:"parent_tool_use_id"`
	// result
	IsError    bool                  `json:"is_error"`
	Result     *string               `json:"result"`
	ModelUsage map[string]modelUsage `json:"modelUsage"`
	// control_response
	Response *struct {
		Subtype   string `json:"subtype"`
		RequestID string `json:"request_id"`
	} `json:"response"`
}

// modelUsage is the session's running per-model total, which on resume
// already includes the earlier invocation's usage. Absent counters stay nil.
type modelUsage struct {
	Input         *int64 `json:"inputTokens"`
	Output        *int64 `json:"outputTokens"`
	CacheRead     *int64 `json:"cacheReadInputTokens"`
	CacheCreation *int64 `json:"cacheCreationInputTokens"`
	Thinking      *int64 `json:"thinkingTokens"`
}

func (s *Session) event(line []byte) error {
	s.events++
	if s.events > maxEvents {
		return fmt.Errorf("%w: event bound exceeded", ErrMalformedMessage)
	}
	var e streamEvent
	if json.Unmarshal(line, &e) != nil || e.Type == "" {
		return fmt.Errorf("%w: invalid stream-json event", ErrMalformedMessage)
	}
	if e.SessionID != "" && e.SessionID != s.result.SessionID {
		return fmt.Errorf("%w: event for another session", ErrTaskBoundary)
	}
	switch e.Type {
	case "system":
		if e.Subtype == "init" {
			return s.acceptInit(e)
		}
		if strings.HasPrefix(e.Subtype, "hook") {
			return fmt.Errorf("%w: a hook ran despite safe mode", ErrTaskBoundary)
		}
	case "control_request":
		return fmt.Errorf("%w: unexpected permission or control request from Claude Code", ErrTaskBoundary)
	case "control_response":
		if e.Response != nil && s.interruptID != "" && e.Response.RequestID == s.interruptID {
			acknowledged := e.Response.Subtype == "success"
			s.acked = &acknowledged
		}
	case "assistant", "user", "result":
		if !s.result.Started {
			return fmt.Errorf("%w: session output before its verified startup state", ErrUnavailable)
		}
		if e.SessionID == "" {
			return fmt.Errorf("%w: %s event without session id", ErrMalformedMessage, e.Type)
		}
		if e.ParentToolUseID != nil {
			return fmt.Errorf("%w: delegated subagent output", ErrTaskBoundary)
		}
		switch e.Type {
		case "assistant":
			return s.assistant(e)
		case "result":
			return s.resultEvent(e)
		}
	}
	return nil // rate limits, permission denials and other events cannot authorize work
}

// acceptInit verifies the reported startup state. A model other than the one
// requested is a safety failure; every other difference refuses the attempt.
func (s *Session) acceptInit(e streamEvent) error {
	if s.result.Started {
		return fmt.Errorf("%w: repeated startup state", ErrMalformedMessage)
	}
	if e.SessionID != s.result.SessionID {
		return fmt.Errorf("%w: startup state names no or another session", ErrUnavailable)
	}
	if len(e.Version) <= 64 {
		s.result.HostVersion = e.Version
	}
	if e.Model == nil || *e.Model == "" {
		return fmt.Errorf("%w: startup state reports no model", ErrUnavailable)
	}
	s.result.Observed.Model = *e.Model
	if *e.Model != s.task.Selection.Model {
		return fmt.Errorf("%w: startup model differs from the requested model", ErrProfileMismatch)
	}
	if e.Cwd == nil {
		return fmt.Errorf("%w: startup state reports no working directory", ErrUnavailable)
	}
	if cwd, err := nativehost.CanonicalPath(*e.Cwd, true); err != nil || cwd != s.task.Layout.Workspace {
		return fmt.Errorf("%w: startup working directory differs", ErrUnavailable)
	}
	want, got := roleTools(s.task.Role), slices.Clone(e.Tools)
	slices.Sort(want)
	slices.Sort(got)
	if e.Tools == nil || !slices.Equal(want, got) {
		return fmt.Errorf("%w: startup tool set differs", ErrUnavailable)
	}
	if e.MCPServers == nil || len(e.MCPServers) != 0 {
		return fmt.Errorf("%w: startup state reports MCP servers", ErrUnavailable)
	}
	if e.Plugins == nil {
		return fmt.Errorf("%w: startup state reports no plugin list", ErrUnavailable)
	}
	for _, plugin := range e.Plugins {
		if plugin.Path != "builtin" || !strings.HasSuffix(plugin.Source, "@builtin") {
			return fmt.Errorf("%w: startup state reports a plugin that is not built in", ErrUnavailable)
		}
	}
	if e.APIKeySource == nil || *e.APIKeySource != "none" {
		return fmt.Errorf("%w: authentication source is not the subscription login", ErrUnavailable)
	}
	if e.PermissionMode == nil || *e.PermissionMode != "dontAsk" {
		return fmt.Errorf("%w: startup permission mode differs", ErrUnavailable)
	}
	s.result.Started = true
	// The requested effort is passed and recorded; nothing reports the applied one.
	o := s.observation(fmt.Sprintf("applied-effort:%d", s.launches))
	o.Unavailable = &metrics.Unavailable{Reason: "applied-effort-not-reported"}
	return s.appendObservation(o)
}

func (s *Session) assistant(e streamEvent) error {
	if e.Message == nil || e.Message.Model == "" {
		return fmt.Errorf("%w: assistant message without model", ErrMalformedMessage)
	}
	if e.Message.Model != s.task.Selection.Model {
		s.result.Observed.Model = e.Message.Model
		return fmt.Errorf("%w: assistant message model differs from the requested model", ErrProfileMismatch)
	}
	tools := roleTools(s.task.Role)
	for _, block := range e.Message.Content {
		if block.Type == "tool_use" && !slices.Contains(tools, block.Name) {
			return fmt.Errorf("%w: unapproved tool use", ErrTaskBoundary)
		}
	}
	return nil
}

// resultEvent records the session's running per-model usage, then fails the
// session if that usage names any other model.
func (s *Session) resultEvent(e streamEvent) error {
	s.results++
	o := s.observation(fmt.Sprintf("result:%d", s.results))
	usage, reported := e.ModelUsage[s.task.Selection.Model]
	switch {
	case e.Subtype == "error_during_execution":
		o.Unavailable = &metrics.Unavailable{Reason: "native-counters-unreliable-after-error"}
	case !reported || usage.Input == nil && usage.Output == nil && usage.CacheRead == nil && usage.CacheCreation == nil && usage.Thinking == nil:
		o.Unavailable = &metrics.Unavailable{Reason: "native-counters-unavailable"}
	default:
		s.sequence++
		o.Sample = &metrics.CounterSample{Stream: usageStream, Mode: "cumulative", Sequence: s.sequence, Counters: metrics.Counters{
			InputTokens: usage.Input, OutputTokens: usage.Output, CacheReadTokens: usage.CacheRead,
			CacheCreationTokens: usage.CacheCreation, ReasoningOutputTokens: usage.Thinking}}
	}
	if err := s.appendObservation(o); err != nil {
		return err
	}
	s.completed = true // the turn has ended; nothing is left to interrupt
	for model := range e.ModelUsage {
		if model != s.task.Selection.Model {
			s.result.Observed.Model = model
			return fmt.Errorf("%w: final per-model usage names another model", ErrProfileMismatch)
		}
	}
	switch {
	case s.interruptID != "":
		s.result.NativeStatus = "interrupted"
	case e.Subtype == "success" && !e.IsError && e.Result != nil:
		if len(*e.Result) > maxTextBytes {
			return fmt.Errorf("%w: result exceeds bound", ErrMalformedMessage)
		}
		s.result.NativeStatus, s.result.Output = "completed", *e.Result
	default:
		s.result.NativeStatus = "failed"
	}
	return nil
}

func (s *Session) observation(key string) metrics.Observation {
	id := sha256.Sum256([]byte(s.task.ID + "\x00" + s.result.SessionID + "\x00" + key))
	requested := metricProfile(s.task.Selection)
	identity := s.task.Evaluation.Identity
	o := metrics.Observation{SchemaVersion: metrics.EvaluationObservationVersion, Evaluation: &identity, ID: fmt.Sprintf("claude-native:%x", id),
		At: time.Now().UTC().Format(time.RFC3339Nano), Source: "claude-code-stream-json", Role: s.task.Role, Host: "claude",
		Session: s.result.SessionID, Requested: &requested}
	if s.result.Observed.Model != "" {
		observed := metrics.Profile{Model: s.result.Observed.Model}
		o.Observed = &observed
	}
	return o
}

func (s *Session) appendObservation(o metrics.Observation) error {
	if len(s.result.Observations) >= maxObservations {
		return fmt.Errorf("%w: observation bound exceeded", ErrMalformedMessage)
	}
	// Reuse the recorder's closed validation and cumulative arithmetic.
	history := append(slices.Clone(s.result.Observations), o)
	if _, err := metrics.CounterContributions(history); err != nil {
		return fmt.Errorf("%w: %w", ErrMalformedMessage, err)
	}
	s.result.Observations = history
	return nil
}

func (s *Session) terminalObservation() error {
	o := s.observation(fmt.Sprintf("terminal:%d:%s", len(s.result.Observations), s.result.Outcome))
	switch s.result.Outcome {
	case SessionFailed:
		o.Outcome = "infrastructure-failure"
	case SessionSuccessful:
		o.Unavailable = &metrics.Unavailable{Reason: "native-completion-not-verification"}
	default:
		o.Unavailable = &metrics.Unavailable{Reason: "native-session-" + string(s.result.Outcome)}
	}
	return s.appendObservation(o)
}
