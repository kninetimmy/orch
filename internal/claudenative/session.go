package claudenative

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"reflect"
	"slices"
	"time"

	"github.com/kninetimmy/orch/internal/manifest"
	"github.com/kninetimmy/orch/internal/metrics"
	"github.com/kninetimmy/orch/internal/nativehost"
)

type SessionOutcome string

const (
	SessionSuccessful   SessionOutcome = "successful"
	SessionFailed       SessionOutcome = "failed"
	SessionRefused      SessionOutcome = "refused"
	SessionCancelled    SessionOutcome = "cancelled"
	SessionTimedOut     SessionOutcome = "timed-out"
	SessionDisconnected SessionOutcome = "disconnected"
)

// SessionResult describes execution only; success is not verification.
// Observed holds the model the session reported. Claude Code reports no
// applied effort, so Observed.Effort stays empty (unknown).
type SessionResult struct {
	TaskID       string
	Workspace    string
	SessionID    string // set once Claude Code was launched with it
	HostVersion  string
	Started      bool // reported startup state verified; work could begin
	Requested    manifest.Selection
	Observed     metrics.Profile
	Outcome      SessionOutcome
	NativeStatus string
	Output       string // accepted only from a successful, verified session
	Observations []metrics.Observation
	Evaluation   *EvaluationBinding
	Instructions *InstructionEvidence
	Cleanup      SessionCleanup
	Error        string
}

// Session retains one approved binding. Cancel the context to interrupt.
// There is no extra input, steering, fork, tool-response or approval API.
type Session struct {
	task        Task
	boundary    nativehost.Boundary
	options     Options
	result      SessionResult
	proc        *process
	launches    int
	results     int
	sequence    int64
	events      int
	prompted    bool
	completed   bool
	interruptID string
	acked       *bool
}

// RunSession runs the approved task once. A finite caller deadline is
// mandatory. A nil Session means the binding itself was refused.
func RunSession(ctx context.Context, options Options, approved Task) (*Session, error) {
	if err := deadline(ctx); err != nil {
		return nil, err
	}
	task, boundary, err := bindTask(approved)
	if err != nil {
		return nil, err
	}
	binding := *task.Evaluation
	binding.InstructionSources = []nativehost.InstructionSource{}
	task.Evaluation = &binding
	task.Layout.SiblingWorkspaces = slices.Clone(task.Layout.SiblingWorkspaces)
	task.Layout.CredentialPaths = slices.Clone(task.Layout.CredentialPaths)
	evidence := binding
	s := &Session{task: task, boundary: boundary, options: options, result: SessionResult{TaskID: task.ID, Workspace: task.Layout.Workspace,
		Requested: task.Selection, Evaluation: &evidence, Instructions: &InstructionEvidence{SchemaVersion: 1, Status: "none-declared",
			Sources: []nativehost.InstructionSource{}, Detail: "Claude plans approve no instruction files. Safe mode disables CLAUDE.md and other customizations, and the workspace root was checked for CLAUDE.md, CLAUDE.local.md, AGENTS.md and .claude before launch. Built-in plugins stay loaded; the session reports no loaded instruction sources."}}}
	return s, s.run(ctx, false)
}

// Resume relaunches a disconnected session by its session id, only with the
// same approved task and the same canonical protected paths. It sends no new
// input. The evaluation controller never calls it.
func (s *Session) Resume(ctx context.Context, approved Task) error {
	if err := deadline(ctx); err != nil {
		return err
	}
	task, boundary, err := bindTask(approved)
	if err != nil || task.ID != s.task.ID || task.Prompt != s.task.Prompt || task.Instructions != s.task.Instructions || task.Selection != s.task.Selection ||
		!reflect.DeepEqual(*task.Evaluation, *s.task.Evaluation) || !reflect.DeepEqual(task.Layout, s.task.Layout) || !reflect.DeepEqual(boundary, s.boundary) {
		return fmt.Errorf("%w: resume requires the same approved task and protected paths", ErrTaskBoundary)
	}
	if s.result.Outcome != SessionDisconnected || s.result.SessionID == "" {
		return errors.New("claude resume requires a disconnected identified session")
	}
	return s.run(ctx, true)
}

// Result returns an independent snapshot.
func (s *Session) Result() SessionResult {
	data, _ := json.Marshal(s.result)
	var result SessionResult
	_ = json.Unmarshal(data, &result)
	return result
}

func deadline(ctx context.Context) error {
	if d, ok := ctx.Deadline(); !ok || d.IsZero() {
		return errors.New("claude session requires a finite caller deadline")
	}
	return ctx.Err()
}

func (s *Session) run(ctx context.Context, resume bool) error {
	s.result.Started, s.result.NativeStatus, s.result.Output, s.result.Cleanup = false, "", "", SessionCleanup{}
	s.completed, s.interruptID, s.acked, s.prompted = false, "", nil, resume // a resumed turn gets no new input
	err := s.launch(ctx, resume)
	var cleanupErr error
	if s.proc != nil {
		if err == nil {
			err = s.converse(ctx)
		}
		err, cleanupErr = s.shutdown(err)
		s.proc = nil
	}
	return s.finish(err, cleanupErr)
}

// launch refuses before starting Claude Code unless the workspace is clean,
// the executable resolves and every required flag is present.
func (s *Session) launch(ctx context.Context, resume bool) error {
	if err := checkWorkspace(s.task.Layout.Workspace); err != nil {
		return err
	}
	executable := s.options.Executable
	if executable == "" {
		executable = "claude"
	}
	path, err := exec.LookPath(executable)
	if err != nil {
		return fmt.Errorf("%w: claude executable not found; install it or adjust PATH: %w", ErrUnavailable, err)
	}
	if err := checkCapabilities(ctx, path, s.task.Layout.Scratch); err != nil {
		return err
	}
	id := s.result.SessionID
	if !resume {
		id = newSessionID()
	}
	args, err := launchArgs(s.task, id, resume)
	if err != nil {
		return err
	}
	if err := checkWorkspace(s.task.Layout.Workspace); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	p, err := startProcess(path, args, s.task.Layout.Workspace, environment(os.Environ(), s.task.Layout.Scratch, resume))
	if err != nil {
		return fmt.Errorf("%w: start Claude Code: %w", ErrUnavailable, err)
	}
	s.proc, s.result.SessionID = p, id
	s.launches++
	return nil
}

// converse sends the one user message once the startup state is verified, or
// after initWait if Claude Code reports it only on receiving input; then the
// first event must still be that verified startup state.
func (s *Session) converse(ctx context.Context) error {
	wait := time.NewTimer(initWait)
	defer wait.Stop()
	waitC := wait.C
	if s.prompted {
		waitC = nil
	}
	exited := s.proc.exited
	var drain <-chan time.Time
	for !s.completed {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-exited:
			// Read what the process wrote, but a descendant holding stdout open
			// cannot keep an exited session waiting for its deadline.
			exited, drain = nil, time.After(shutdownTimeout)
		case <-drain:
			return ErrProcessExit
		case <-waitC:
			waitC = nil
			if err := s.prompt(); err != nil {
				return err
			}
		case line, ok := <-s.proc.lines:
			if !ok {
				return s.proc.closedError()
			}
			if err := s.event(line); err != nil {
				return err
			}
			if s.result.Started && !s.prompted {
				waitC = nil
				if err := s.prompt(); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

// prompt sends the role instructions and the task as one user message. Free
// text never enters the argument vector.
func (s *Session) prompt() error {
	s.prompted = true
	return s.proc.send(map[string]any{"type": "user", "parent_tool_use_id": nil, "message": map[string]any{"role": "user",
		"content": []any{map[string]string{"type": "text", "text": s.task.Instructions}, map[string]string{"type": "text", "text": s.task.Prompt}}}})
}

// shutdown interrupts unfinished work, closes stdin, waits a bounded time for
// exit, then kills the whole process tree so nothing from the attempt keeps
// running. Session evidence found during the interrupt joins err.
func (s *Session) shutdown(err error) (error, error) {
	p := s.proc
	var cleanupErr error
	// A session whose process already exited has nothing to interrupt.
	if err != nil && !s.completed && s.prompted && !errors.Is(err, ErrProcessExit) && p.running() {
		s.result.Cleanup.InterruptionAsked = true
		acknowledged, eventErr, interruptErr := s.interrupt()
		s.result.Cleanup.InterruptAcknowledged = &acknowledged
		err = errors.Join(err, eventErr)
		if interruptErr != nil {
			cleanupErr = fmt.Errorf("claude interrupt: %w", interruptErr)
		}
	}
	_ = p.stdin.Close()
	exited := p.wait(shutdownTimeout)
	s.result.Cleanup.ShutdownObserved = &exited
	if !exited {
		cleanupErr = errors.Join(cleanupErr, errors.New("claude code did not exit after stdin closed; process tree killed"))
	}
	if killErr := p.tree.kill(); killErr != nil {
		cleanupErr = errors.Join(cleanupErr, fmt.Errorf("claude process tree kill: %w", killErr))
	}
	if !exited && !p.wait(shutdownTimeout) {
		cleanupErr = errors.Join(cleanupErr, errors.New("claude code did not exit after kill"))
	}
	p.tree.close()
	p.stop()
	if cleanupErr != nil {
		s.result.Cleanup.Detail = cleanupErr.Error()
	}
	return err, cleanupErr
}

// interrupt sends the stream-json interrupt control request and waits a
// bounded time for its control_response and the turn's result.
func (s *Session) interrupt() (acknowledged bool, eventErr, err error) {
	s.interruptID = "orch-interrupt-" + newSessionID()
	if err := s.proc.send(map[string]any{"type": "control_request", "request_id": s.interruptID, "request": map[string]string{"subtype": "interrupt"}}); err != nil {
		return false, nil, err
	}
	timer := time.NewTimer(shutdownTimeout)
	defer timer.Stop()
	for s.acked == nil || !s.completed {
		select {
		case <-timer.C:
			if s.acked != nil {
				return *s.acked, eventErr, nil // acknowledged; the turn's result did not follow in time
			}
			return false, eventErr, errors.New("interrupt not acknowledged in time")
		case line, ok := <-s.proc.lines:
			if !ok {
				if s.acked != nil {
					return *s.acked, eventErr, nil
				}
				return false, eventErr, errors.New("claude code exited before acknowledging the interrupt")
			}
			eventErr = errors.Join(eventErr, s.event(line))
		}
	}
	return *s.acked, eventErr, nil
}

func (s *Session) finish(err, cleanupErr error) error {
	switch {
	case errors.Is(err, ErrProfileMismatch), errors.Is(err, ErrTaskBoundary), errors.Is(err, ErrMalformedMessage):
		s.result.Outcome = SessionFailed
	case errors.Is(err, ErrUnavailable):
		s.result.Outcome = SessionRefused
	case errors.Is(err, context.DeadlineExceeded):
		s.result.Outcome = SessionTimedOut
	case errors.Is(err, context.Canceled):
		s.result.Outcome = SessionCancelled
	case errors.Is(err, ErrProcessExit):
		s.result.Outcome = SessionDisconnected
	case err != nil:
		s.result.Outcome = SessionFailed
	case s.result.NativeStatus == "completed":
		s.result.Outcome = SessionSuccessful
	default:
		s.result.Outcome = SessionFailed
		err = fmt.Errorf("claude turn ended with status %q", s.result.NativeStatus)
	}
	if s.result.Outcome != SessionSuccessful {
		s.result.Output = "" // no output is accepted from a refused, failed or unfinished session
	}
	combined := errors.Join(err, cleanupErr, s.terminalObservation())
	s.result.Error = ""
	if combined != nil {
		s.result.Error = combined.Error()
	}
	return combined
}
