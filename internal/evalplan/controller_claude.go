package evalplan

import (
	"context"
	"errors"
	"fmt"

	"github.com/kninetimmy/orch/internal/claudenative"
	"github.com/kninetimmy/orch/internal/metrics"
)

// claudeWorker runs each attempt of a version-3 claude plan through Claude Code
// once. It never resumes; a disconnected attempt stays incomplete.
type claudeWorker struct{ executable, revision string }

func (w claudeWorker) execute(ctx context.Context, request workerRequest) (workerResult, error) {
	if result, err := admit(w.revision, request, "claude", "The Claude worker runs only claude evaluation plans; no host fallback.", claudenative.ErrTaskBoundary); result != nil {
		return *result, err
	}
	session, err := claudenative.RunSession(ctx, claudenative.Options{Executable: w.executable}, request.Task)
	if session == nil {
		return workerResult{Outcome: "refused", Detail: fmt.Sprintf("Native evaluation binding/admission refused: %v", err)}, err
	}
	observed := session.Result()
	requested := metrics.Profile{Model: observed.Requested.Model, Effort: observed.Requested.Effort}
	// The Claude session id is the executed thread: observations name it, and
	// the controller keys cleanup acknowledgement on it.
	native := &NativeEvidence{SchemaVersion: 2, Binding: observed.Evaluation, TaskID: observed.TaskID, ThreadID: observed.SessionID,
		SessionID: observed.SessionID, Status: observed.NativeStatus, Requested: &requested, Observations: observed.Observations,
		Instructions: observed.Instructions, Cleanup: &observed.Cleanup, Failure: observed.Error}
	if observed.Observed.Model != "" {
		native.Observed = &observed.Observed
	}
	result := workerResult{Native: native, Detail: observed.Error}
	if observed.HostVersion != "" {
		result.Eligibility = &Eligibility{HostVersion: observed.HostVersion, ProfileAllowed: observed.Started, ModelToolsVerified: observed.Started}
	}
	if observed.Output != "" {
		result.Output = &observed.Output
	}
	switch {
	case errors.Is(err, claudenative.ErrProfileMismatch), errors.Is(err, claudenative.ErrTaskBoundary):
		result.Outcome = "safety-failure"
	case errors.Is(err, claudenative.ErrMalformedMessage):
		result.Outcome = "protocol-invalid"
	case errors.Is(err, claudenative.ErrUnavailable), err != nil && !observed.Started:
		result.Outcome = "refused"
	case observed.Outcome == claudenative.SessionSuccessful:
		result.Outcome = "native-completed"
	case observed.Outcome == claudenative.SessionTimedOut:
		result.Outcome = "timeout"
	case observed.Outcome == claudenative.SessionCancelled:
		result.Outcome = "interrupted"
	case observed.Outcome == claudenative.SessionDisconnected:
		result.Outcome = "disconnected"
	default:
		result.Outcome = "infrastructure-failure"
	}
	return result, err
}
