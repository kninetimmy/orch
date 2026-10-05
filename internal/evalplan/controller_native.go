package evalplan

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"reflect"
	"runtime/debug"

	"github.com/kninetimmy/orch/internal/codexnative"
	"github.com/kninetimmy/orch/internal/manifest"
	"github.com/kninetimmy/orch/internal/metrics"
)

func buildRevision() string {
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return ""
	}
	var revision string
	clean := false
	for _, setting := range info.Settings {
		switch setting.Key {
		case "vcs.revision":
			revision = setting.Value
		case "vcs.modified":
			clean = setting.Value == "false"
		}
	}
	if !clean || !oidPattern.MatchString(revision) {
		return ""
	}
	return revision
}

// This mapping is evaluation policy, not Delivery routing or a fallback.
func evaluationProfile(plan Plan, unit Unit, role string) (string, PinnedSelection, manifest.Selection, error) {
	side := plan.Baseline
	if unit.Side == "candidate" {
		if plan.Candidate == nil {
			return "", side, manifest.Selection{}, fmt.Errorf("candidate profile is absent")
		}
		side = *plan.Candidate
	} else if unit.Side != "baseline" {
		return "", side, manifest.Selection{}, fmt.Errorf("unknown evaluation side")
	}
	profiles, ok := side.Configuration.Profiles["codex"]
	if !ok {
		return "", side, manifest.Selection{}, fmt.Errorf("selected profile has no Codex roles; no host fallback")
	}
	nativeRole, profile := "", profiles.Scout
	switch role {
	case "scout":
		nativeRole = "scout"
	case "implementation":
		nativeRole, profile = "implementer", profiles.Implementer
	case "review":
		nativeRole, profile = "reviewer", profiles.Reviewer
	default:
		return "", side, manifest.Selection{}, fmt.Errorf("unsupported public evaluation role")
	}
	return nativeRole, side, manifest.Selection{Model: profile.Model, Effort: profile.Effort, Variant: profile.Variant}, nil
}

func evaluationTask(e *Evaluation, record AttemptRecord, source caseSource, layout codexnative.IsolationPaths) (codexnative.Task, error) {
	role, side, selection, err := evaluationProfile(e.Preparation.Plan, record.Unit, source.definition.Role)
	if err != nil {
		return codexnative.Task{}, err
	}
	prompt, instructions := string(source.public["TASK.md"]), string(source.public["ROLE.md"])
	instructions += "\nRead CONTEXT.md and the supplied public files. Execute only TASK.md under this approved bounded evaluation. Do not access other packets, repositories, history, controller records or credentials. Do not delegate or perform Git/GitHub lifecycle actions. Return your output and exact verification evidence; completion is not a semantic grade."
	binding := &codexnative.EvaluationBinding{Identity: metrics.EvaluationIdentity{ID: e.ID, PlanDigest: e.Preparation.PlanDigest,
		Unit: record.Unit.Ordinal, CaseID: record.Unit.CaseID, CaseVersion: record.Unit.CaseVersion, CaseSHA256: record.CaseSHA256,
		PacketSHA256: record.PacketSHA256, Repetition: record.Unit.Repetition, Side: record.Unit.Side, Attempt: record.Number, Kind: record.Kind, Role: role},
		OrchRevision: side.OrchRevision, ProfileSHA256: side.Profile.SHA256, Selection: selection, Workspace: layout.Workspace, Scratch: layout.Scratch,
		PromptSHA256: fmt.Sprintf("%x", sha256.Sum256([]byte(prompt))), InstructionsSHA256: fmt.Sprintf("%x", sha256.Sum256([]byte(instructions))), InstructionSources: []codexnative.InstructionSource{}}
	if e.Preparation.Plan.Instructions == nil {
		return codexnative.Task{}, fmt.Errorf("evaluation lacks declared instruction inputs")
	}
	for _, artifact := range *e.Preparation.Plan.Instructions {
		binding.InstructionSources = append(binding.InstructionSources, codexnative.InstructionSource{Path: artifact.Path, SHA256: artifact.SHA256})
	}
	return codexnative.Task{ID: binding.TaskID(), Role: role, Selection: selection, Prompt: prompt, Instructions: instructions, Layout: layout, Evaluation: binding}, nil
}

func (w nativeWorker) execute(ctx context.Context, request workerRequest) (workerResult, error) {
	refuse := func(detail string) (workerResult, error) {
		return workerResult{Outcome: "refused", Detail: detail}, nil
	}
	if request.PlanVersion != 2 || request.Task.Evaluation == nil {
		return refuse("Version-1 evaluation evidence remains readable; execution requires a version-2 frozen plan declaring approved instruction inputs.")
	}
	if !oidPattern.MatchString(w.revision) || request.Task.Evaluation.OrchRevision != w.revision || request.Intervention != "none" && request.Intervention != "requested-profile" {
		return refuse("Native evaluation requires the exact clean embedded controller revision and none/requested-profile intervention; historical revision execution is unsupported.")
	}
	if len(request.Revisions) == 0 {
		return refuse("Selected controller revisions are unknown.")
	}
	for _, revision := range request.Revisions {
		if revision != w.revision {
			return refuse("Every selected Orch revision must match this clean controller build; no historical executor is available.")
		}
	}
	role := map[string]string{"scout": "scout", "implementation": "implementer", "review": "reviewer"}[request.Role]
	if role == "" || role != request.Task.Role || !reflect.DeepEqual(request.Task.Layout, request.Layout) || request.Unit.Ordinal != request.Task.Evaluation.Identity.Unit || request.Unit.CaseID != request.Task.Evaluation.Identity.CaseID || request.Unit.CaseVersion != request.Task.Evaluation.Identity.CaseVersion || request.Unit.Side != request.Task.Evaluation.Identity.Side || request.Unit.Repetition != request.Task.Evaluation.Identity.Repetition {
		return workerResult{Outcome: "safety-failure", Detail: "Evaluation request differs from its native binding."}, codexnative.ErrTaskBoundary
	}
	options := codexnative.Options{Executable: w.executable, Dir: request.Layout.Workspace, ClientVersion: w.clientVersion}
	session, err := codexnative.RunSession(ctx, options, request.Task)
	if session == nil {
		result, _ := refuse(fmt.Sprintf("Native evaluation binding/admission refused: %v", err))
		return result, err
	}
	observed := session.Result()
	requested := metrics.Profile{Model: observed.Requested.Model, Effort: observed.Requested.Effort}
	native := &NativeEvidence{SchemaVersion: 2, Binding: observed.Evaluation, TaskID: observed.TaskID,
		ThreadID: observed.ThreadID, SessionID: observed.NativeSessionID, TurnID: observed.TurnID, Status: observed.NativeStatus,
		Requested: &requested, Observations: observed.Observations, Instructions: observed.Instructions, Cleanup: &observed.Cleanup, Failure: observed.Error}
	if observed.Observed.Model != "" || observed.Observed.Effort != "" {
		native.Observed = &observed.Observed
	}
	result := workerResult{Native: native}
	if caps := observed.Eligibility; caps != nil {
		result.Eligibility = &Eligibility{HostVersion: caps.HostVersion, SandboxReady: caps.SandboxReady, ProfileAllowed: caps.ProfileAllowed, ToolsDisabled: caps.ToolsDisabled, ModelToolsVerified: caps.ModelToolsVerified}
		if caps.ToolsDisabled {
			count := caps.DisabledMCP
			result.Eligibility.DisabledMCP = &count
		}
	}
	if observed.Output != "" {
		result.Output = &observed.Output
	}
	result.Detail = observed.Error
	switch {
	case errors.Is(err, codexnative.ErrProfileMismatch), errors.Is(err, codexnative.ErrTaskBoundary):
		result.Outcome = "safety-failure"
	case errors.Is(err, codexnative.ErrMalformedMessage):
		result.Outcome = "protocol-invalid"
	case errors.Is(err, codexnative.ErrIsolationUnavailable):
		result.Outcome = "refused"
	case err != nil && observed.ThreadID == "":
		result.Outcome = "refused"
	case observed.Outcome == codexnative.SessionSuccessful:
		result.Outcome = "native-completed"
	case observed.Outcome == codexnative.SessionTimedOut:
		result.Outcome = "timeout"
	case observed.Outcome == codexnative.SessionCancelled:
		result.Outcome = "interrupted"
	case observed.Outcome == codexnative.SessionDisconnected:
		result.Outcome = "disconnected"
	default:
		result.Outcome = "infrastructure-failure"
	}
	return result, err
}

func validateAttemptNative(a *AttemptRecord, e *Evaluation) error {
	if a.SchemaVersion != e.Preparation.Plan.Version {
		return fmt.Errorf("attempt evidence version differs from frozen plan")
	}
	if a.SchemaVersion == 2 {
		if a.Native != nil && a.Native.SchemaVersion != 2 || a.ExecutionSource == "codex-native-evaluation" && a.Outcome == "native-completed" && (a.Native == nil || a.Native.Binding == nil) {
			return fmt.Errorf("version-2 native execution requires versioned evaluation evidence")
		}
	} else if a.Native != nil && a.Native.SchemaVersion != 0 {
		return fmt.Errorf("legacy attempt cannot contain new native evaluation evidence")
	}
	if err := validateNative(a.Native); err != nil {
		return err
	}
	if a.Native == nil || a.Native.SchemaVersion == 0 {
		return nil
	} // original retained wire contract
	b := a.Native.Binding
	role := ""
	for _, c := range e.Preparation.Plan.Cases {
		if c.ID == a.Unit.CaseID {
			role = c.Role
		}
	}
	nativeRole, side, selection, err := evaluationProfile(e.Preparation.Plan, a.Unit, role)
	if err != nil {
		return err
	}
	want := metrics.EvaluationIdentity{ID: e.ID, PlanDigest: e.Preparation.PlanDigest, Unit: a.Unit.Ordinal, CaseID: a.Unit.CaseID,
		CaseVersion: a.Unit.CaseVersion, CaseSHA256: a.CaseSHA256, PacketSHA256: a.PacketSHA256, Repetition: a.Unit.Repetition,
		Side: a.Unit.Side, Attempt: a.Number, Kind: a.Kind, Role: nativeRole}
	if b == nil || b.Identity != want || b.Workspace != a.Packet || b.Scratch != a.Scratch || b.Selection != selection || b.OrchRevision != side.OrchRevision || b.ProfileSHA256 != side.Profile.SHA256 ||
		a.Native.TaskID != b.TaskID() || a.Native.Requested == nil || a.Native.Requested.Model != selection.Model || a.Native.Requested.Effort != selection.Effort || a.Native.Requested.Variant != nil {
		return fmt.Errorf("native evidence differs from originating evaluation attempt/profile")
	}
	if e.Preparation.Plan.Instructions == nil || len(b.InstructionSources) != len(*e.Preparation.Plan.Instructions) {
		return fmt.Errorf("native instruction declarations differ from frozen approval")
	}
	for i, source := range b.InstructionSources {
		artifact := (*e.Preparation.Plan.Instructions)[i]
		if source.Path != artifact.Path || source.SHA256 != artifact.SHA256 {
			return fmt.Errorf("native instruction artifact differs from frozen approval")
		}
	}
	for _, o := range a.Native.Observations {
		if o.Evaluation == nil || *o.Evaluation != want || o.Session != a.Native.ThreadID || o.Requested == nil || *o.Requested != *a.Native.Requested {
			return fmt.Errorf("native observation differs from originating evaluation attempt/session/profile")
		}
	}
	return nil
}
