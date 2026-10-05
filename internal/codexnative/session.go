package codexnative

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"reflect"
	"strings"
	"time"

	"github.com/kninetimmy/orch/internal/agents"
	"github.com/kninetimmy/orch/internal/manifest"
	"github.com/kninetimmy/orch/internal/metrics"
	"github.com/kninetimmy/orch/internal/paths"
)

var (
	ErrProfileMismatch = errors.New("codex reported execution profile mismatch")
	ErrTaskBoundary    = errors.New("codex approved task boundary changed")
)

type SessionOutcome string

const (
	SessionSuccessful   SessionOutcome = "successful"
	SessionFailed       SessionOutcome = "failed"
	SessionCancelled    SessionOutcome = "cancelled"
	SessionTimedOut     SessionOutcome = "timed-out"
	SessionDisconnected SessionOutcome = "disconnected"
	maxSessionEvents                   = 1024
)

// Task is one caller-approved dispatch, not a request for this package to approve
// or route work. The engine remains responsible for eligibility and approval.
// ID and Prompt identify the exact task; Selection is its routed native profile.
type Task struct {
	ID           string
	RunID        string
	IssueNumber  int
	Role         string
	Attempt      string
	ReviewCycle  int
	Selection    manifest.Selection
	Prompt       string
	Layout       IsolationPaths
	Evaluation   *EvaluationBinding
	Instructions string // evaluation-only, supplied from the declared public ROLE.md
}

// SessionResult describes execution only. Successful is neither verification
// nor review/merge approval. Observed contains native configured-profile reports,
// not proof of per-turn inference identity; absent fields remain unknown.
type SessionResult struct {
	TaskID          string
	Workspace       string
	ThreadID        string
	NativeSessionID string // native session tree, distinct from the executed thread
	TurnID          string
	Requested       manifest.Selection
	Observed        metrics.Profile
	Outcome         SessionOutcome
	NativeStatus    string
	Output          string
	Observations    []metrics.Observation
	Evaluation      *EvaluationBinding
	Eligibility     *IsolationCapabilities
	Instructions    *InstructionEvidence
	Cleanup         SessionCleanup
	Error           string
}

type SessionCleanup struct {
	InterruptionAsked     bool   `json:"interruption_asked"`
	InterruptAcknowledged *bool  `json:"interrupt_acknowledged,omitempty"`
	ShutdownObserved      *bool  `json:"shutdown_observed,omitempty"`
	Detail                string `json:"detail,omitempty"`
}

// Session retains one binding and its replay history in memory. Calls are
// sequential; cancel the execution context to interrupt. There is no arbitrary
// turn input, steering, fork, tool-response or approval API.
type Session struct {
	task         Task
	boundary     isolationBoundary // canonical implicit credential/shared-Git protections
	options      Options
	instructions string
	result       SessionResult
	samples      map[string]bool
	items        map[string]string
	sequence     int64
	events       int
	started      bool
	completed    bool
	incoming     chan sessionMessage
	pending      string
	connection   *connection
	turnReady    chan struct{} // private scripted-fixture synchronization; unset in production
}

type sessionMessage struct {
	message message
	err     error
}

// RunSession executes at most one turn. A finite caller deadline is mandatory.
// Every start and resume requires both preflights and the actual execution
// connection's controls. Unreviewed native enforcement remains unavailable.
func RunSession(ctx context.Context, options Options, approved Task) (*Session, error) {
	if err := sessionDeadline(ctx); err != nil {
		return nil, err
	}
	s, _, err := newSession(options, approved)
	if err != nil {
		return nil, err
	}
	c, cleanup, err := s.connect(ctx)
	if err != nil {
		return s, s.finish(err, nil)
	}
	defer cleanup()
	return s, s.execute(ctx, c, false)
}

// Resume revalidates the same retained task, canonical layout, native identities
// and exact profile. Only disconnected sessions are resumable. A completed turn
// is never replayed, and an unidentified submitted turn cannot be retried safely.
func (s *Session) Resume(ctx context.Context, approved Task) error {
	if err := s.checkResume(ctx, approved); err != nil {
		return err
	}
	c, cleanup, err := s.connect(ctx)
	if err != nil {
		return err // preserve the disconnected checkpoint and dirty work
	}
	defer cleanup()
	return s.execute(ctx, c, true)
}

// Result returns an independent snapshot so caller edits cannot corrupt replay
// identity or the retained task. These types contain only JSON-safe data.
func (s *Session) Result() SessionResult {
	data, _ := json.Marshal(s.result)
	var result SessionResult
	_ = json.Unmarshal(data, &result)
	return result
}

func sessionDeadline(ctx context.Context) error {
	deadline, ok := ctx.Deadline()
	if !ok || deadline.IsZero() {
		return errors.New("codex session requires a finite caller deadline")
	}
	return ctx.Err()
}

func bindTask(task Task) (Task, isolationBoundary, string, error) {
	var instructions string
	var err error
	if task.Evaluation == nil {
		if task.Instructions != "" {
			return task, isolationBoundary{}, "", ErrTaskBoundary
		}
		instructions, err = agents.CodexInstructions(task.Role)
	} else {
		instructions = task.Instructions
		err = validateEvaluationTask(task)
	}
	if err != nil {
		return task, isolationBoundary{}, "", err
	}
	if strings.TrimSpace(task.Prompt) == "" || len(task.Prompt)+len(instructions) > maxMessageBytes/2 {
		return task, isolationBoundary{}, "", errors.New("codex session requires bounded nonempty approved task text")
	}
	if strings.TrimSpace(task.Selection.Model) == "" || strings.TrimSpace(task.Selection.Effort) == "" || task.Selection.Variant != "" || task.Selection.NoVariant {
		return task, isolationBoundary{}, "", errors.New("codex session requires an exact model and effort selection")
	}
	probe := metrics.Observation{SchemaVersion: metrics.ObservationVersion, RunID: task.RunID, ID: task.ID,
		At: time.Now().UTC().Format(time.RFC3339Nano), Source: "codex-app-server", Host: "codex", Role: metricRole(task.Role),
		IssueNumber: task.IssueNumber, Attempt: task.Attempt, ReviewCycle: task.ReviewCycle,
		Requested: &metrics.Profile{Model: task.Selection.Model, Effort: task.Selection.Effort}, Unavailable: &metrics.Unavailable{Reason: "preflight"}}
	if task.Evaluation != nil {
		probe.SchemaVersion = metrics.EvaluationObservationVersion
		identity := task.Evaluation.Identity
		probe.Evaluation = &identity
	}
	if err := probe.Validate(); err != nil {
		return task, isolationBoundary{}, "", err
	}
	b, err := prepareIsolation(task.Layout, task.Role == "scout" || metricRole(task.Role) == "reviewer")
	if err != nil {
		return task, b, "", err
	}
	task.Layout.Workspace, task.Layout.Scratch = b.workspace, b.scratch
	b.evaluation = task.Evaluation != nil
	if task.Evaluation != nil {
		if task.Evaluation.Workspace != b.workspace || task.Evaluation.Scratch != b.scratch {
			return task, b, "", fmt.Errorf("%w: evaluation packet/scratch differs", ErrTaskBoundary)
		}
		data, _ := json.Marshal(task.Evaluation)
		var binding EvaluationBinding
		_ = json.Unmarshal(data, &binding)
		task.Evaluation = &binding
	}
	// Retain canonical copies of every caller-supplied protected location, not
	// aliases or slice storage that the caller can subsequently change.
	paths := append([]string{task.Layout.MainCheckout, task.Layout.ControllerState}, task.Layout.SiblingWorkspaces...)
	paths = append(paths, task.Layout.CredentialPaths...)
	for i := range paths {
		paths[i], err = isolationPath(paths[i], false)
		if err != nil {
			return task, b, "", err
		}
	}
	task.Layout.MainCheckout, task.Layout.ControllerState = paths[0], paths[1]
	n := len(task.Layout.SiblingWorkspaces)
	task.Layout.SiblingWorkspaces = append([]string(nil), paths[2:2+n]...)
	task.Layout.CredentialPaths = append([]string(nil), paths[2+n:]...)
	return task, b, instructions, nil
}

func newSession(options Options, task Task) (*Session, isolationBoundary, error) {
	task, boundary, instructions, err := bindTask(task)
	if err != nil {
		return nil, boundary, err
	}
	dir, err := isolationPath(options.Dir, true)
	if err != nil || dir != task.Layout.Workspace {
		return nil, boundary, fmt.Errorf("%w: host cwd differs from approved workspace", ErrTaskBoundary)
	}
	options.Dir = dir
	s := &Session{task: task, boundary: boundary, options: options, instructions: instructions, samples: map[string]bool{}, items: map[string]string{},
		result: SessionResult{TaskID: task.ID, Workspace: dir, Requested: task.Selection, Evaluation: task.Evaluation}}
	return s, boundary, nil
}

func metricRole(role string) string {
	if role == "review_downgrade" {
		return "reviewer"
	}
	return role
}

func (s *Session) checkResume(ctx context.Context, approved Task) error {
	if err := sessionDeadline(ctx); err != nil {
		return err
	}
	bound, boundary, instructions, err := bindTask(approved)
	if err != nil || !reflect.DeepEqual(bound, s.task) || !reflect.DeepEqual(boundary.protected, s.boundary.protected) || instructions != s.instructions {
		return fmt.Errorf("%w: resume requires the same approved binding", ErrTaskBoundary)
	}
	if s.result.Outcome != SessionDisconnected || s.completed || s.result.ThreadID == "" || s.result.NativeSessionID == "" || s.result.TurnID == "" {
		return errors.New("codex resume requires a disconnected identified unfinished turn")
	}
	return nil
}

func (s *Session) connect(ctx context.Context) (*connection, func(), error) {
	if s.task.Evaluation == nil {
		if _, err := Preflight(ctx, s.options, s.task.Selection); err != nil {
			return nil, nil, err
		}
		if _, err := IsolationPreflight(ctx, s.options, s.task.Layout, s.task.Role == "scout" || metricRole(s.task.Role) == "reviewer"); err != nil {
			return nil, nil, err
		}
	}
	b, err := prepareIsolation(s.task.Layout, s.task.Role == "scout" || metricRole(s.task.Role) == "reviewer")
	if err != nil {
		return nil, nil, err
	}
	if !reflect.DeepEqual(b.protected, s.boundary.protected) {
		return nil, nil, fmt.Errorf("%w: implicit credential or shared Git paths changed", ErrTaskBoundary)
	}
	b.evaluation = s.task.Evaluation != nil
	release, err := s.holdInstructions()
	if err != nil {
		return nil, nil, err
	}
	retained := false
	defer func() {
		if !retained {
			release()
		}
	}()
	if b.evaluation {
		// Every evaluation child, including metadata and discovery, receives the
		// same scrubbed environment and context restrictions before it starts.
		if _, err := preflight(ctx, s.options, s.task.Selection, &b); err != nil {
			return nil, nil, err
		}
		if _, err := isolationPreflight(ctx, s.options, b); err != nil {
			return nil, nil, err
		}
	}
	processCtx, cleanup := sessionContext(ctx)
	c, capabilities, err := openIsolation(processCtx, s.options, b)
	if err == nil {
		_, err = inspectCatalog(c, Capabilities{HostVersion: capabilities.HostVersion}, s.task.Selection)
	}
	if err == nil {
		err = modelToolBoundary(c, b, capabilities) // recheck the actual connection
	}
	if err == nil && b.evaluation {
		err = verifyEvaluationConfig(c, b)
	}
	capabilities.ModelToolsVerified = err == nil
	if s.task.Evaluation != nil {
		s.result.Eligibility = &capabilities
	}
	if err != nil {
		if c != nil {
			err = errors.Join(err, c.close())
		}
		cleanup()
		return nil, nil, err
	}
	c.session = true
	retained = true
	return c, func() { cleanup(); release() }, nil
}

// Keep the host alive for a bounded native interrupt after caller cancellation.
// The caller's deadline still bounds execution; this grace only bounds cleanup.
func sessionContext(ctx context.Context) (context.Context, func()) {
	deadline, _ := ctx.Deadline()
	processCtx, cancel := context.WithDeadline(context.WithoutCancel(ctx), deadline.Add(shutdownTimeout))
	stop := context.AfterFunc(ctx, func() {
		timer := time.NewTimer(shutdownTimeout)
		defer timer.Stop()
		select {
		case <-processCtx.Done():
		case <-timer.C:
			cancel()
		}
	})
	return processCtx, func() { stop(); cancel() }
}

type nativeTurn struct {
	ID     string            `json:"id"`
	Status string            `json:"status"`
	Items  []json.RawMessage `json:"items"`
}

type nativeSettings struct {
	Provider string  `json:"modelProvider"`
	Model    *string `json:"model"`
	Effort   *string `json:"reasoningEffort"`
	Cwd      string  `json:"cwd"`
	Approval string  `json:"approvalPolicy"`
	Profile  *struct {
		ID      string  `json:"id"`
		Extends *string `json:"extends"`
	} `json:"activePermissionProfile"`
}

type nativeThread struct {
	nativeSettings
	ID      string       `json:"id"`
	Session string       `json:"sessionId"`
	Parent  *string      `json:"parentThreadId"`
	Turns   []nativeTurn `json:"turns"`
}

type nativeThreadResponse struct {
	nativeSettings
	Thread             nativeThread `json:"thread"`
	TurnsCursor        *string      `json:"turnsBackwardsCursor"`
	ItemsCursor        *string      `json:"itemsBackwardsCursor"`
	WorkspaceRoots     []string     `json:"runtimeWorkspaceRoots"`
	InstructionSources []string     `json:"instructionSources"`
}

func (s *Session) execute(ctx context.Context, c *connection, resume bool) (err error) {
	s.result.Cleanup = SessionCleanup{}
	s.connection, s.pending = c, ""
	s.incoming = make(chan sessionMessage, 1)
	incoming := s.incoming
	readerDone := make(chan struct{})
	defer close(readerDone)
	go func() {
		for {
			m, readErr := c.read()
			select {
			case incoming <- sessionMessage{m, readErr}:
			case <-readerDone:
				return
			}
			if readErr != nil {
				return
			}
		}
	}()
	defer func() {
		var cleanupErr error
		if err != nil && !s.completed {
			s.result.Cleanup.InterruptionAsked = s.result.TurnID != ""
			if interruptErr := s.interrupt(); interruptErr != nil {
				if s.result.TurnID != "" {
					acknowledged := false
					s.result.Cleanup.InterruptAcknowledged = &acknowledged
				}
				cleanupErr = fmt.Errorf("codex native interruption cleanup: %w", interruptErr)
			} else if s.result.TurnID != "" {
				acknowledged := true
				s.result.Cleanup.InterruptAcknowledged = &acknowledged
			}
		}
		closeErr := c.close()
		shutdown := closeErr == nil
		s.result.Cleanup.ShutdownObserved = &shutdown
		if closeErr != nil {
			cleanupErr = errors.Join(cleanupErr, fmt.Errorf("codex native shutdown cleanup: %w", closeErr))
		}
		if s.task.Evaluation != nil {
			err = errors.Join(err, s.checkInstructions())
		}
		s.connection = nil
		err = s.finish(err, cleanupErr)
	}()
	params := map[string]any{"cwd": s.task.Layout.Workspace, "model": s.task.Selection.Model, "modelProvider": "openai", "runtimeWorkspaceRoots": []any{},
		"config": map[string]string{"model_reasoning_effort": s.task.Selection.Effort}, "approvalPolicy": "never",
		"permissions": c.profile, "developerInstructions": s.instructions + "\nExecute only the supplied approved task. Do not delegate, expand the task or request elevated permissions. Completion is not verification or merge approval."}
	if s.task.Evaluation != nil {
		if err := s.checkInstructions(); err != nil {
			return err
		}
	}
	method := "thread/start"
	if resume {
		method = "thread/resume"
		params["threadId"] = s.result.ThreadID
		params["excludeTurns"] = false
	} else {
		params["allowProviderModelFallback"] = false
		params["environments"] = []any{}
		params["selectedCapabilityRoots"] = []any{}
		params["dynamicTools"] = []any{}
		params["ephemeral"] = false
	}
	var response nativeThreadResponse
	if err := s.call(ctx, method, params, &response); err != nil {
		return err
	}
	if err := s.acceptThread(response, resume); err != nil {
		return err
	}
	if !resume {
		s.started = true // even an uncertain turn/start response must not be replayed
		var result struct {
			Turn nativeTurn `json:"turn"`
		}
		params := map[string]any{"threadId": s.result.ThreadID, "cwd": s.task.Layout.Workspace, "model": s.task.Selection.Model,
			"effort": s.task.Selection.Effort, "approvalPolicy": "never", "permissions": c.profile, "environments": []any{},
			"input": []any{map[string]any{"type": "text", "text": s.task.Prompt}}}
		if err := s.call(ctx, "turn/start", params, &result); err != nil {
			return err
		}
		if err := s.acceptTurn(result.Turn, false); err != nil {
			return err
		}
	}
	for !s.completed {
		m, err := s.receive(ctx)
		if err != nil {
			return err
		}
		if len(m.Method) == 0 {
			return fmt.Errorf("%w: unsolicited session response", ErrMalformedMessage)
		}
		if err := s.notification(m); err != nil {
			return err
		}
	}
	return nil
}

func (s *Session) call(ctx context.Context, method string, params, result any) error {
	id, err := s.connection.request(method, params)
	if err != nil {
		return err
	}
	s.pending = id
	for {
		m, err := s.receive(ctx)
		if err != nil {
			return err
		}
		if len(m.Method) != 0 {
			if err := s.notification(m); err != nil {
				return err
			}
			continue
		}
		err = decodeResponse(m, id, method, result)
		if err == nil {
			s.pending = ""
		}
		return err
	}
}

func (s *Session) receive(ctx context.Context) (message, error) {
	select {
	case <-ctx.Done():
		return message{}, ctx.Err()
	case incoming := <-s.incoming:
		return incoming.message, incoming.err
	}
}

func (s *Session) settings(settings nativeSettings) error {
	if settings.Provider != "" && settings.Provider != "openai" {
		return fmt.Errorf("%w: reported provider differs", ErrTaskBoundary)
	}
	if settings.Model != nil {
		s.result.Observed.Model = *settings.Model
	}
	if settings.Effort != nil {
		s.result.Observed.Effort = *settings.Effort
	}
	if (settings.Model != nil && *settings.Model != s.task.Selection.Model) || (settings.Effort != nil && *settings.Effort != s.task.Selection.Effort) {
		return ErrProfileMismatch
	}
	if settings.Cwd != "" {
		cwd, err := isolationPath(settings.Cwd, true)
		if err != nil || cwd != s.task.Layout.Workspace {
			return fmt.Errorf("%w: reported workspace differs", ErrTaskBoundary)
		}
	}
	if settings.Approval != "" && settings.Approval != "never" {
		return fmt.Errorf("%w: reported permission escalation policy", ErrTaskBoundary)
	}
	if settings.Profile != nil && (settings.Profile.ID != s.connection.profile || settings.Profile.Extends != nil) {
		return fmt.Errorf("%w: reported native permission profile differs", ErrTaskBoundary)
	}
	return nil
}

func (s *Session) acceptThread(response nativeThreadResponse, resume bool) error {
	t := response.Thread
	if t.ID == "" || t.Session == "" || t.Parent != nil || t.Cwd == "" || response.Cwd == "" || response.Approval == "" || response.Profile == nil || response.Provider != "openai" || response.WorkspaceRoots == nil || len(response.WorkspaceRoots) != 0 {
		return fmt.Errorf("%w: incomplete or delegated native thread binding", ErrTaskBoundary)
	}
	if (s.result.ThreadID != "" && t.ID != s.result.ThreadID) || (resume && t.Session != s.result.NativeSessionID) {
		return fmt.Errorf("%w: resume changed native identity", ErrTaskBoundary)
	}
	s.result.ThreadID, s.result.NativeSessionID = t.ID, t.Session
	for _, id := range []string{t.ID, t.Session} {
		if err := s.nativeIdentity(id); err != nil {
			return err
		}
	}
	if err := s.settings(response.nativeSettings); err != nil {
		return err
	}
	if err := s.settings(t.nativeSettings); err != nil {
		return err
	}
	if s.task.Evaluation != nil {
		if err := s.acceptInstructions(response.InstructionSources); err != nil {
			return err
		}
	}
	if response.TurnsCursor != nil || response.ItemsCursor != nil || (!resume && len(t.Turns) != 0) {
		return fmt.Errorf("%w: unexpected or incomplete task history", ErrTaskBoundary)
	}
	if resume {
		if len(t.Turns) != 1 || t.Turns[0].ID != s.result.TurnID {
			return fmt.Errorf("%w: resume did not identify the same single turn", ErrTaskBoundary)
		}
		return s.acceptTurn(t.Turns[0], t.Turns[0].Status != "inProgress")
	}
	return nil
}

func (s *Session) acceptTurn(turn nativeTurn, terminal bool) error {
	if !s.started || turn.ID == "" || (s.result.TurnID != "" && turn.ID != s.result.TurnID) {
		return fmt.Errorf("%w: unexpected native turn identity", ErrTaskBoundary)
	}
	s.result.TurnID = turn.ID
	if err := s.nativeIdentity(turn.ID); err != nil {
		return err
	}
	for _, item := range turn.Items {
		if err := s.item(item, terminal); err != nil {
			return err
		}
	}
	if !terminal {
		if turn.Status != "inProgress" && (!s.completed || turn.Status != s.result.NativeStatus) {
			return fmt.Errorf("%w: invalid started turn status", ErrMalformedMessage)
		}
		if s.turnReady != nil {
			close(s.turnReady)
			s.turnReady = nil
		}
		return nil
	}
	if s.completed && turn.Status != s.result.NativeStatus {
		return fmt.Errorf("%w: conflicting completed turn", ErrMalformedMessage)
	}
	switch turn.Status {
	case "completed", "failed", "interrupted":
		s.completed, s.result.NativeStatus = true, turn.Status
	default:
		return fmt.Errorf("%w: invalid completed turn status", ErrMalformedMessage)
	}
	return nil
}

func (s *Session) notification(m message) error {
	s.events++
	if s.events > maxSessionEvents {
		return errors.New("codex session event bound exceeded")
	}
	var method string
	_ = json.Unmarshal(m.Method, &method) // envelope was validated by connection.read
	var event struct {
		ThreadID string          `json:"threadId"`
		TurnID   string          `json:"turnId"`
		Turn     nativeTurn      `json:"turn"`
		Item     json.RawMessage `json:"item"`
	}
	if json.Unmarshal(m.Params, &event) != nil {
		return fmt.Errorf("%w: invalid native notification", ErrMalformedMessage)
	}
	if event.ThreadID != "" && s.result.ThreadID == "" && !s.started && s.pending != "" {
		s.result.ThreadID = event.ThreadID // provisional; thread response must agree
	}
	if event.ThreadID != "" && event.ThreadID != s.result.ThreadID {
		return fmt.Errorf("%w: notification for another task thread", ErrTaskBoundary)
	}
	if event.TurnID != "" && (s.result.TurnID == "" || event.TurnID != s.result.TurnID) {
		return fmt.Errorf("%w: notification for another task turn", ErrTaskBoundary)
	}
	switch method {
	case "hook/started", "hook/completed":
		return restrictionError("disabled native hook reported execution")
	case "turn/started", "turn/completed":
		if event.ThreadID == "" {
			return ErrTaskBoundary
		}
		return s.acceptTurn(event.Turn, method == "turn/completed")
	case "thread/settings/updated":
		var update struct {
			Settings nativeSettings `json:"threadSettings"`
		}
		if json.Unmarshal(m.Params, &update) != nil || event.ThreadID == "" {
			return ErrMalformedMessage
		}
		// This notification names the field effort, unlike thread responses.
		var effort struct {
			Settings struct {
				Effort *string `json:"effort"`
			} `json:"threadSettings"`
		}
		if json.Unmarshal(m.Params, &effort) != nil {
			return ErrMalformedMessage
		}
		update.Settings.Effort = effort.Settings.Effort
		if update.Settings.Cwd == "" || update.Settings.Approval == "" || update.Settings.Profile == nil {
			return fmt.Errorf("%w: incomplete reported permission settings", ErrTaskBoundary)
		}
		return s.settings(update.Settings)
	case "model/rerouted":
		var reroute struct {
			Model string `json:"toModel"`
		}
		if json.Unmarshal(m.Params, &reroute) != nil || reroute.Model == "" || event.ThreadID == "" || event.TurnID == "" {
			return ErrMalformedMessage
		}
		s.result.Observed.Model = reroute.Model
		return ErrProfileMismatch // even a reroute back to the same name stops work
	case "thread/tokenUsage/updated":
		if event.ThreadID == "" || event.TurnID == "" {
			return ErrTaskBoundary
		}
		return s.tokenUsage(m.Params)
	case "item/started", "item/completed":
		if event.ThreadID == "" || event.TurnID == "" {
			return ErrTaskBoundary
		}
		return s.item(event.Item, method == "item/completed")
	case "thread/started":
		var started struct {
			Thread nativeThread `json:"thread"`
		}
		if json.Unmarshal(m.Params, &started) != nil || started.Thread.ID == "" || started.Thread.Parent != nil {
			return fmt.Errorf("%w: unapproved additional thread", ErrTaskBoundary)
		}
		if s.result.ThreadID == "" && !s.started && s.pending != "" {
			s.result.ThreadID = started.Thread.ID
		}
		if started.Thread.ID != s.result.ThreadID {
			return fmt.Errorf("%w: unapproved additional thread", ErrTaskBoundary)
		}
		return s.settings(started.Thread.nativeSettings)
	}
	return nil // other notifications cannot satisfy a request or authorize work
}

func (s *Session) item(data json.RawMessage, completed bool) error {
	var item struct {
		ID      string `json:"id"`
		Type    string `json:"type"`
		Text    string `json:"text"`
		Phase   string `json:"phase"`
		Cwd     string `json:"cwd"`
		Changes []struct {
			Path string `json:"path"`
		} `json:"changes"`
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
	}
	if json.Unmarshal(data, &item) != nil || item.ID == "" || item.Type == "" {
		return ErrMalformedMessage
	}
	switch item.Type {
	case "agentMessage", "reasoning", "plan", "contextCompaction", "sleep":
	case "userMessage":
		if len(item.Content) != 1 || item.Content[0].Type != "text" || item.Content[0].Text != s.task.Prompt {
			return fmt.Errorf("%w: additional task input", ErrTaskBoundary)
		}
	case "commandExecution":
		if err := s.taskPath(item.Cwd); err != nil {
			return err
		}
	case "fileChange":
		if s.task.Role == "scout" || metricRole(s.task.Role) == "reviewer" {
			return fmt.Errorf("%w: read-only role attempted file changes", ErrTaskBoundary)
		}
		if len(item.Changes) == 0 {
			return ErrMalformedMessage
		}
		for _, change := range item.Changes {
			path := change.Path
			if path == "" {
				return ErrMalformedMessage
			}
			if !filepath.IsAbs(path) {
				path = filepath.Join(s.task.Layout.Workspace, path)
			}
			if err := s.taskPath(path); err != nil {
				return err
			}
		}
	default:
		return fmt.Errorf("%w: unapproved delegation, tool or task expansion", ErrTaskBoundary)
	}
	if !completed || item.Type != "agentMessage" || (item.Phase != "" && item.Phase != "final_answer") {
		return nil
	}
	if old, ok := s.items[item.ID]; ok {
		if old != item.Text {
			return fmt.Errorf("%w: conflicting completed output", ErrMalformedMessage)
		}
		return nil
	}
	if len(s.result.Output)+len(item.Text)+1 > maxMessageBytes || len(s.items) >= maxSessionEvents {
		return errors.New("codex session output bound exceeded")
	}
	s.items[item.ID] = item.Text
	if s.result.Output != "" {
		s.result.Output += "\n"
	}
	s.result.Output += item.Text
	return nil
}

func (s *Session) nativeIdentity(id string) error {
	o := s.observation("identity-validation")
	o.Session = id
	o.Unavailable = &metrics.Unavailable{Reason: "identity-validation"}
	if err := o.Validate(); err != nil {
		return fmt.Errorf("%w: invalid native identity", ErrMalformedMessage)
	}
	return nil
}

func (s *Session) taskPath(path string) error {
	canonical, err := isolationPath(path, false)
	if err != nil {
		return fmt.Errorf("%w: unverifiable tool path", ErrTaskBoundary)
	}
	for _, root := range []string{s.task.Layout.Workspace, s.task.Layout.Scratch} {
		inside, err := paths.Inside(root, canonical)
		if err != nil {
			return fmt.Errorf("%w: unverifiable tool containment", ErrTaskBoundary)
		}
		if inside {
			return nil
		}
	}
	return fmt.Errorf("%w: tool path outside approved task", ErrTaskBoundary)
}

func (s *Session) interrupt() error {
	if s.result.TurnID == "" {
		return nil // no known native turn can be addressed safely
	}
	ctx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
	c := s.connection
	stop := context.AfterFunc(ctx, func() {
		// Bound a blocked interrupt write as well as its acknowledgement read.
		_ = c.stdin.Close()
		_ = c.stdout.Close()
		_ = c.cmd.Process.Kill()
	})
	defer func() { stop(); cancel() }()
	id, err := s.connection.request("turn/interrupt", map[string]string{"threadId": s.result.ThreadID, "turnId": s.result.TurnID})
	if err != nil {
		return err
	}
	acked := false
	for !acked || !s.completed {
		m, err := s.receive(ctx)
		if err != nil {
			return err
		}
		if len(m.Method) != 0 {
			if err := s.notification(m); err != nil {
				return err
			}
			continue
		}
		var responseID string
		_ = json.Unmarshal(m.ID, &responseID)
		if s.pending != "" && responseID == s.pending {
			var ignored json.RawMessage
			if err := decodeResponse(m, s.pending, "interrupted-request", &ignored); err != nil {
				return err
			}
			s.pending = ""
			continue
		}
		var result struct{}
		if err := decodeResponse(m, id, "turn/interrupt", &result); err != nil || acked {
			return errors.Join(ErrMalformedMessage, err)
		}
		acked = true
	}
	return nil
}

func (s *Session) finish(err, cleanupErr error) error {
	switch {
	case errors.Is(err, ErrProfileMismatch), errors.Is(err, ErrTaskBoundary), errors.Is(err, ErrMalformedMessage):
		s.result.Outcome = SessionFailed
	case errors.Is(err, context.DeadlineExceeded):
		s.result.Outcome = SessionTimedOut
	case errors.Is(err, context.Canceled):
		s.result.Outcome = SessionCancelled
	case errors.Is(err, ErrTimeout):
		s.result.Outcome = SessionTimedOut
	case errors.Is(err, ErrProcessExit), errors.Is(err, ErrTransportInterrupted):
		s.result.Outcome = SessionDisconnected
	case err != nil, s.result.NativeStatus == "failed":
		s.result.Outcome = SessionFailed
		if err == nil {
			err = errors.New("codex native turn failed")
		}
	case s.result.NativeStatus == "interrupted":
		s.result.Outcome = SessionCancelled
	case s.completed:
		s.result.Outcome = SessionSuccessful
	default:
		s.result.Outcome = SessionFailed
		err = errors.New("codex session ended without a terminal turn")
	}
	if cleanupErr != nil {
		s.result.Cleanup.Detail = cleanupErr.Error()
	}
	combined := errors.Join(err, cleanupErr, s.terminalObservation())
	s.result.Error = ""
	if combined != nil {
		s.result.Error = combined.Error()
	}
	return combined
}
