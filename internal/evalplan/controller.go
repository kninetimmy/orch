package evalplan

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"time"

	"github.com/kninetimmy/orch/internal/evalcorpus"
	"github.com/kninetimmy/orch/internal/nativehost"
)

var (
	errStop    = errors.New("evaluation stop requested")
	errOverall = errors.New("evaluation overall cutoff reached")
	errAttempt = errors.New("evaluation attempt deadline reached")
)

// The executor seam is package-private. Only nativeWorker is present in normal
// builds; the successful scripted implementation lives entirely in _test.go.
// No exported API accepts an executor, callback, command or bypass option.
type worker interface {
	execute(context.Context, workerRequest) (workerResult, error)
}

type workerRequest struct {
	Unit         Unit
	Role         string
	Layout       nativehost.IsolationPaths
	Task         nativehost.Task
	PlanVersion  int
	Host         string
	Intervention string
	Revisions    []string
}

type workerResult struct {
	Outcome     string
	Detail      string
	Output      *string
	Native      *NativeEvidence
	Eligibility *Eligibility
}

type nativeWorker struct{ clientVersion, executable, revision string }

// Run consumes an evaluation exactly once. It retains a complete bounded
// controller outcome, including every unrun slot. Every production attempt
// requires its frozen evaluation binding and actual native gate. Run confers no
// approval, changes no Delivery state, and cannot resume interrupted execution.
func Run(ctx context.Context, storageRoot, id, clientVersion string) (*Progress, error) {
	if err := executionApproval(storageRoot, id); err != nil {
		return nil, err
	}
	return run(ctx, storageRoot, id, nativeWorker{clientVersion: clientVersion, revision: buildRevision()})
}

func run(ctx context.Context, storageRoot, id string, executor worker) (*Progress, error) {
	p, err := runController(ctx, storageRoot, id, executor)
	if p != nil && terminalState(p.State) {
		_, reportErr := RetainReport(storageRoot, id)
		err = errors.Join(err, reportErr)
	}
	return p, err
}

func runController(ctx context.Context, storageRoot, id string, executor worker) (*Progress, error) {
	g, e, hash, err := openEvaluation(storageRoot, id)
	if err != nil {
		return nil, err
	}
	defer g.close()
	if _, native := executor.(nativeWorker); native {
		if err := executionApproval(storageRoot, id); err != nil {
			return nil, err
		}
		// The Codex worker never runs another host's plan, so nothing is claimed.
		if host := planHost(e.Preparation.Plan); host != "codex" {
			return nil, fmt.Errorf("no %s evaluation worker is available in this build and the Codex worker runs only Codex plans; evaluation %s remains prepared and unconsumed", host, e.ID)
		}
	}
	if _, err := Load(ctx, e.Repository, storageRoot, e.Preparation.PlanDigest); err != nil {
		return nil, err
	}
	p, err := readProgress(g, e, hash)
	if err != nil {
		return nil, err
	}
	if p.State != "prepared" || len(p.Inspection) != 0 {
		return p, fmt.Errorf("evaluation is not an untouched preparation; no takeover or replay is allowed")
	}
	if e.Preparation.Preview.Counts.MaximumAttemptsIncludingRepairs > maxControllerAttempts {
		return p, fmt.Errorf("controller attempt capacity exceeded")
	}
	size, entries, _, err := inventory(g)
	if err != nil {
		return p, err
	}
	g.budget = &writeBudget{used: size + MaxPlanBytes, entries: entries + 16} // charge every write; reserve stop capacity
	claim, err := g.createDir("execution")
	if err != nil {
		return p, fmt.Errorf("evaluation already claimed or incomplete; no takeover: %w", err)
	}
	if err := claim.publish("owner.json", struct {
		SchemaVersion int    `json:"schema_version"`
		EvaluationID  string `json:"evaluation_id"`
		PlanDigest    string `json:"plan_digest"`
		StartedAt     string `json:"started_at"`
	}{1, e.ID, e.Preparation.PlanDigest, now()}); err != nil {
		claim.close()
		return p, err
	}
	claim.close() // the permanent exclusive claim intentionally survives crashes
	p, err = appendProgress(g, p, func(next *Progress) { next.State = "running" })
	if err != nil {
		return p, err
	}
	limits := e.Preparation.Plan.Limits
	overall, cancelOverall := context.WithTimeoutCause(ctx, time.Duration(limits.OverallSeconds)*time.Second, errOverall)
	defer cancelOverall()
	execution, cancel := context.WithCancelCause(overall)
	defer cancel(nil)
	watchDone := make(chan struct{})
	watchExited := make(chan struct{})
	defer func() { close(watchDone); <-watchExited }()
	go func() {
		defer close(watchExited)
		ticker := time.NewTicker(100 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-watchDone:
				return
			case <-execution.Done():
				return
			case <-ticker.C:
				stop, err := stopped(g, e)
				if err != nil {
					cancel(fmt.Errorf("stop record safety failure: %w", err))
					return
				}
				if stop {
					cancel(errStop)
					return
				}
			}
		}
	}()

	terminal, reason := "completed", "Every frozen slot has an execution outcome; grades remain unknown."
	var runError error
	for i := range p.Slots {
		kind := "initial"
		for {
			if cause := executionCause(execution, g, e); cause != nil {
				terminal, reason = stopState(cause)
				if terminal == "safety-failure" {
					runError = cause
				}
				break
			}
			// Recheck the exact inputs/configuration/exclusions immediately before
			// preparing each attempt. Readiness remains opaque, never authorization.
			fresh, err := normalize(execution, e.Repository, storageRoot, proposal(e.Preparation.Plan), nil)
			if err != nil || !reflect.DeepEqual(fresh, &e.Preparation) {
				if execution.Err() != nil {
					terminal, reason = stopState(context.Cause(execution))
				} else {
					terminal, reason, runError = "safety-failure", "Frozen inputs or exclusions changed; progression stopped.", fmt.Errorf("attempt input revalidation failed: %v", err)
				}
				break
			}
			one := e.Preparation.Plan
			one.Cases = []PublicCase{one.Cases[slices.IndexFunc(one.Cases, func(c PublicCase) bool { return c.ID == p.Slots[i].CaseID })]}
			sources, err := loadSources(execution, e.Repository, one)
			if err != nil {
				if execution.Err() != nil {
					terminal, reason = stopState(context.Cause(execution))
				} else {
					terminal, reason, runError = "safety-failure", "Source artifacts unavailable or changed; progression stopped.", err
				}
				break
			}
			p, err = appendProgress(g, p, func(next *Progress) {
				slot := &next.Slots[i]
				slot.Status = "running"
				if kind == "repair" {
					slot.RepairsConsumed++
				} else {
					slot.AttemptsConsumed++
				}
				slot.Attempts = append(slot.Attempts, AttemptRef{Number: len(slot.Attempts) + 1, Kind: kind})
			})
			if err != nil {
				return p, err
			}
			source := sources[p.Slots[i].CaseID]
			a, record, prepErr := prepareAttempt(execution, g, e, p.Slots[i], kind, source)
			if prepErr != nil {
				a.close()
				terminal, reason, runError = "incomplete", "Attempt budget consumed; preparation artifacts preserved, execution/cleanup unknown.", prepErr
				if cause := context.Cause(execution); cause != nil {
					terminal, reason = stopState(cause)
					reason += "; preparation interrupted after consuming the attempt budget; execution/cleanup remain unknown."
					if terminal != "safety-failure" {
						runError = nil
					}
				}
				break
			}
			result, returned, interrupted, cleanupDeadline := executeAttempt(execution, g, e, a, source, record, executor)
			record.Outcome, record.Detail = result.Outcome, result.Detail
			record.ExecutionSource = "no-model-test-script"
			if _, native := executor.(nativeWorker); native {
				record.ExecutionSource = "native-eligibility-only"
				if record.SchemaVersion >= 2 {
					record.ExecutionSource = "codex-native-evaluation"
				}
			}
			record.Native, record.Eligibility = result.Native, result.Eligibility
			if validationErr := validateAttemptNative(&record, e); validationErr != nil {
				data, retainErr := storedBytes(record.Native)
				if retainErr == nil {
					retainErr = a.controller.writeFile("invalid-native.json", data)
				}
				if retainErr != nil {
					a.close()
					terminal, reason, runError = "incomplete", "Invalid native payload could not be retained within bounds; attempt remains incomplete.", errors.Join(validationErr, retainErr)
					break
				}
				record.InvalidNative = &DigestedFile{Path: "invalid-native.json", SHA256: evalcorpus.Digest(data)}
				record.Native = nil // malformed bytes remain data, never usable observations
				record.Outcome, record.Detail = "invalid-evidence", validationErr.Error()
			}
			verificationDeadline := time.Now().Add(time.Duration(limits.VerificationSeconds) * time.Second)
			if deadline, ok := execution.Deadline(); ok && deadline.Before(verificationDeadline) {
				verificationDeadline = deadline
			}
			verification, cancelVerification := context.WithDeadline(context.WithoutCancel(execution), verificationDeadline)
			if returned {
				public, packetErr := snapshot(verification, a.packet)
				scratch, scratchErr := snapshot(verification, a.scratch)
				artifactErr := errors.Join(packetErr, scratchErr)
				if artifactErr == nil {
					files, err := retainSnapshot(verification, a.controller, "worker-output", public)
					record.Artifacts = append(record.Artifacts, files...)
					artifactErr = err
					if artifactErr == nil {
						files, err := retainSnapshot(verification, a.controller, "scratch-output", scratch)
						record.Artifacts = append(record.Artifacts, files...)
						artifactErr = err
					}
				}
				if artifactErr != nil {
					if errors.Is(artifactErr, context.DeadlineExceeded) {
						record.Detail, record.Verification = artifactErr.Error(), "timed-out"
					} else {
						record.Outcome, record.Detail, record.Verification = "invalid-evidence", artifactErr.Error(), "failed"
					}
				} else {
					record.Verification = "retained-artifact-integrity-only; no semantic grade"
				}
			}
			if result.Output != nil {
				output := []byte(*result.Output)
				if err := a.controller.writeFile("output.txt", output); err != nil {
					record.Outcome, record.Detail = "invalid-evidence", err.Error()
				} else {
					record.Output = &DigestedFile{"output.txt", evalcorpus.Digest(output)}
				}
			}
			cancelVerification()
			if cleanupDeadline.IsZero() {
				cleanupDeadline = time.Now().Add(time.Duration(limits.CleanupSeconds) * time.Second)
			}
			cleanup, cancelCleanup := context.WithDeadline(context.WithoutCancel(execution), cleanupDeadline)
			var acknowledged *bool
			if record.Native != nil && record.Native.Cleanup != nil && record.Native.Cleanup.ShutdownObserved != nil {
				native := record.Native.Cleanup
				value := *native.ShutdownObserved && (!native.InterruptionAsked || native.InterruptAcknowledged != nil && *native.InterruptAcknowledged)
				acknowledged = &value
			}
			if record.Native != nil && record.Native.SchemaVersion == 2 && record.Native.ThreadID != "" && (acknowledged == nil || !*acknowledged) {
				record.Cleanup = Cleanup{Status: "preserved-unacknowledged", Detail: "Native shutdown/interruption was not acknowledged; disposable resources preserved.", WorkerReturned: returned, InterruptionAsked: interrupted}
			} else if record.Outcome == "invalid-evidence" || record.Verification == "timed-out" {
				record.Cleanup = Cleanup{Status: "preserved-unverifiable", Detail: "Invalid or bounded-out evidence; disposable work preserved.", WorkerReturned: returned, InterruptionAsked: interrupted}
			} else {
				record.Cleanup = cleanupAttempt(cleanup, a, source, returned, interrupted)
			}
			if record.Native != nil && record.Native.Cleanup != nil {
				record.Cleanup.InterruptionAsked = interrupted || record.Native.Cleanup.InterruptionAsked
			}
			record.Cleanup.NativeAcknowledged = acknowledged
			if acknowledged != nil && *acknowledged {
				record.Cleanup.Detail = "Local artifact cleanup recorded separately; direct native stdio shutdown and any requested turn interruption acknowledged. Descendant cleanup not independently observed."
			}
			cancelCleanup()
			record.FinishedAt = now()
			if err := a.controller.publish("attempt.json", &record); err != nil {
				a.close()
				return p, fmt.Errorf("attempt publication incomplete; consumed budgets retained: %w", err)
			}
			a.close()
			p, err = appendProgress(g, p, func(next *Progress) {
				slot := &next.Slots[i]
				slot.Status = record.Outcome
				slot.Attempts[len(slot.Attempts)-1].SHA256 = storedDigest(&record)
			})
			if err != nil {
				return p, err
			}
			if slices.Contains([]string{"invalid-evidence", "protocol-invalid", "safety-failure"}, record.Outcome) {
				terminal, reason, runError = "safety-failure", "Unsafe or incomplete attempt evidence/cleanup; progression stopped.", fmt.Errorf("%s: %s; cleanup: %s", record.Outcome, record.Detail, record.Cleanup.Detail)
				break
			}
			if record.Outcome == "refused" {
				terminal, reason = "refused", record.Detail
				break
			}
			if cause := executionCause(execution, g, e); cause != nil {
				terminal, reason = stopState(cause)
				break
			}
			if !returned || record.Cleanup.Status != "removed-clean" && record.Cleanup.Status != "preserved-dirty" || record.Verification == "timed-out" {
				terminal, reason, runError = "incomplete", "Bounded interruption, verification or cleanup observation unavailable; resources and consumed budgets retained.", fmt.Errorf("%s: %s; cleanup: %s", record.Outcome, record.Detail, record.Cleanup.Detail)
				break
			}
			if record.Outcome == "disconnected" || record.Outcome == "interrupted" || record.Native != nil && record.Native.SchemaVersion == 2 && record.Native.Status == "interrupted" {
				terminal, reason = "incomplete", "Unfinished execution retained; no durable native resume or automatic retry."
				break
			}
			if slices.Contains([]string{"infrastructure-failure", "timeout"}, record.Outcome) && p.Slots[i].AttemptsConsumed < limits.MaxAttemptsPerUnit {
				kind = "retry"
				continue
			}
			if record.Outcome == "task-failure" && p.Slots[i].RepairsConsumed < *limits.MaxRepairsPerUnit {
				kind = "repair"
				continue
			}
			break
		}
		if terminal != "completed" {
			break
		}
	}
	p, err = appendProgress(g, p, func(next *Progress) { next.State, next.Reason = terminal, reason })
	return p, errors.Join(runError, err)
}

func executionCause(ctx context.Context, g *guardedDir, e *Evaluation) error {
	if cause := context.Cause(ctx); cause != nil {
		return cause
	}
	stop, err := stopped(g, e)
	if err != nil {
		return fmt.Errorf("stop record safety failure: %w", err)
	}
	if stop {
		return errStop
	}
	return nil
}

func stopState(cause error) (string, string) {
	switch {
	case errors.Is(cause, errOverall):
		return "overall-cutoff", cause.Error()
	case errors.Is(cause, errStop), errors.Is(cause, context.Canceled), errors.Is(cause, context.DeadlineExceeded):
		return "stopped", cause.Error()
	default:
		return "safety-failure", cause.Error()
	}
}

func executeAttempt(ctx context.Context, g *guardedDir, e *Evaluation, a *preparedAttempt, source caseSource, record AttemptRecord, executor worker) (workerResult, bool, bool, time.Time) {
	layout, err := a.layout(e)
	if err != nil {
		return workerResult{Outcome: "safety-failure", Detail: err.Error()}, true, false, time.Time{}
	}
	if cause := executionCause(ctx, g, e); cause != nil {
		return workerResult{Outcome: "interrupted", Detail: cause.Error()}, true, false, time.Time{}
	}
	request := workerRequest{Unit: record.Unit, Role: source.definition.Role, Layout: layout, PlanVersion: e.Preparation.Plan.Version, Host: planHost(e.Preparation.Plan), Intervention: e.Preparation.Plan.Intervention}
	request.Revisions = []string{e.Preparation.Plan.Baseline.OrchRevision}
	if e.Preparation.Plan.Candidate != nil {
		request.Revisions = append(request.Revisions, e.Preparation.Plan.Candidate.OrchRevision)
	}
	if request.PlanVersion >= 2 {
		request.Task, err = evaluationTask(e, record, source, layout)
		if err != nil {
			return workerResult{Outcome: "refused", Detail: err.Error()}, true, false, time.Time{}
		}
	}
	limits := e.Preparation.Plan.Limits
	attempt, cancel := context.WithTimeoutCause(ctx, time.Duration(limits.AttemptSeconds)*time.Second, errAttempt)
	defer cancel()
	type reply struct {
		result workerResult
		err    error
	}
	done := make(chan reply, 1)
	go func() {
		result, err := executor.execute(attempt, request)
		done <- reply{result, err}
	}()
	var response reply
	returned, interrupted := false, false
	var cleanupDeadline time.Time
	select {
	case response = <-done:
		returned = true
	case <-attempt.Done():
		interrupted = true
		cleanupDeadline = time.Now().Add(time.Duration(limits.CleanupSeconds) * time.Second)
		// Cancellation requests interruption. Lack of a bounded return is unknown,
		// never a native acknowledgement or permission to delete dirty work.
		grace := time.NewTimer(time.Duration(limits.CleanupSeconds) * time.Second)
		defer grace.Stop()
		select {
		case response = <-done:
			returned = true
		case <-grace.C:
		}
	}
	if cause := context.Cause(attempt); cause != nil {
		outcome := "interrupted"
		if errors.Is(cause, errAttempt) || errors.Is(cause, errOverall) || errors.Is(cause, context.DeadlineExceeded) {
			outcome = "timeout"
		}
		response.result.Outcome, response.result.Detail = outcome, cause.Error()
	}
	if response.result.Outcome == "" && response.err != nil {
		response.result.Outcome, response.result.Detail = "infrastructure-failure", response.err.Error()
	}
	if !slices.Contains(outcomes, response.result.Outcome) {
		response.result.Outcome, response.result.Detail = "protocol-invalid", "Worker returned a missing or unsupported execution outcome."
	}
	return response.result, returned, interrupted, cleanupDeadline
}
