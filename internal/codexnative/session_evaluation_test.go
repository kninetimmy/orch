package codexnative

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/kninetimmy/orch/internal/metrics"
)

func evaluationSessionTask(t *testing.T) (Options, Task) {
	t.Helper()
	options, task := sessionTask(t)
	task.ID, task.RunID, task.IssueNumber, task.Attempt, task.Role = "", "", 0, "", "implementer"
	task.Instructions = "Public ROLE.md instructions."
	task.Evaluation = &EvaluationBinding{Identity: metrics.EvaluationIdentity{ID: "eval-aaaaaaaaaaaaaaaaaaaaaaaaaa", PlanDigest: "sha256:" + strings.Repeat("a", 64),
		Unit: 1, CaseID: "public-case", CaseVersion: 1, CaseSHA256: strings.Repeat("b", 64), PacketSHA256: strings.Repeat("c", 64), Repetition: 1, Side: "baseline", Attempt: 1, Kind: "initial", Role: task.Role},
		OrchRevision: strings.Repeat("a", 40), ProfileSHA256: strings.Repeat("b", 64), Selection: task.Selection,
		Workspace: task.Layout.Workspace, Scratch: task.Layout.Scratch, PromptSHA256: textSHA256(task.Prompt), InstructionsSHA256: textSHA256(task.Instructions), InstructionSources: []InstructionSource{}}
	// Native binding requires canonical paths, including macOS temporary aliases.
	var err error
	task.Evaluation.Workspace, err = isolationPath(task.Layout.Workspace, true)
	if err != nil {
		t.Fatal(err)
	}
	task.Evaluation.Scratch, err = isolationPath(task.Layout.Scratch, true)
	if err != nil {
		t.Fatal(err)
	}
	task.Layout.Workspace, task.Layout.Scratch, options.Dir = task.Evaluation.Workspace, task.Evaluation.Scratch, task.Evaluation.Workspace
	task.ID = task.Evaluation.TaskID()
	return options, task
}

func TestEvaluationSessionBindingAndContext(t *testing.T) {
	t.Setenv("CODEX_HOME", t.TempDir()) // synthetic instructions, never installed Codex/authentication
	options, task := evaluationSessionTask(t)
	if _, _, err := newSession(options, task); err != nil {
		t.Fatal(err)
	}
	for _, change := range []func(*Task){
		func(t *Task) { t.RunID = "run-fabricated" },
		func(t *Task) { t.IssueNumber = 323 },
		func(t *Task) { t.ID = "another-attempt" },
		func(t *Task) { t.Prompt += "private key" },
		func(t *Task) { t.Instructions += "private grade" },
		func(t *Task) { t.Selection.Effort = "high" },
		func(t *Task) { t.Role = "reviewer" },
	} {
		bad := task
		change(&bad)
		if _, _, err := newSession(options, bad); err == nil {
			t.Fatal("changed evaluation task accepted")
		}
	}
	home, err := nativeInstructionHome()
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(home, "AGENTS.md")
	if err := os.WriteFile(path, []byte("Generic approved collaboration instructions."), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := newSession(options, task); err == nil {
		t.Fatal("undeclared global instructions accepted")
	}
	task.Evaluation.InstructionSources = []InstructionSource{{Path: path, SHA256: textSHA256("Generic approved collaboration instructions.")}}
	if _, _, err := newSession(options, task); err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS == "windows" {
		held, err := holdInstructionFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("changed"), 0o600); err == nil {
			t.Fatal("held instruction file permitted ordinary write")
		}
		if err := held.Close(); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(path, []byte("changed"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := newSession(options, task); err == nil {
		t.Fatal("instruction hash drift accepted")
	}
	if err := os.WriteFile(path, []byte("Generic approved collaboration instructions."), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, "AGENTS.override.md"), []byte("undeclared private override"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := newSession(options, task); err == nil {
		t.Fatal("undeclared override accepted")
	}
}

func TestEvaluationSessionScriptedEvidence(t *testing.T) {
	t.Setenv("CODEX_HOME", t.TempDir())
	for _, scenario := range []string{"evaluation-complete", "evaluation-private-source"} {
		t.Run(scenario, func(t *testing.T) {
			options, task := evaluationSessionTask(t)
			s, _, err := newSession(options, task)
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
			defer cancel()
			c, cleanup := scriptedSessionConnection(t, ctx, s, scenario)
			defer cleanup()
			err = s.execute(ctx, c, false)
			result := s.Result()
			if scenario == "evaluation-private-source" {
				if !errors.Is(err, ErrTaskBoundary) || result.Instructions.Status != "mismatch" || result.TurnID != "" {
					t.Fatalf("undeclared loaded context admitted: %+v %v", result, err)
				}
				return
			}
			if err != nil || result.Outcome != SessionSuccessful || result.Output == "" || result.NativeSessionID != "tree-session-294" || result.ThreadID != "thread-294" || result.TurnID != "turn-294" || result.Instructions.Status != "verified-sources-and-hashes" || result.Cleanup.ShutdownObserved == nil || !*result.Cleanup.ShutdownObserved {
				t.Fatalf("evaluation native evidence: %+v %v", result, err)
			}
			if len(result.Observations) != 3 {
				t.Fatalf("native replay was counted twice: %+v", result.Observations)
			}
			deltas, err := metrics.CounterContributions(result.Observations)
			if err != nil || *deltas[0].InputTokens != 100 || *deltas[1].InputTokens != 20 || *deltas[0].OutputTokens != 0 || deltas[1].OutputTokens != nil || deltas[0].CacheCreationTokens != nil {
				t.Fatalf("evaluation counters: %+v %v", deltas, err)
			}
			for _, o := range result.Observations {
				if o.SchemaVersion != 3 || o.RunID != "" || o.IssueNumber != 0 || o.Attempt != "" || o.Evaluation == nil || *o.Evaluation != task.Evaluation.Identity {
					t.Fatal("evaluation mixed into Delivery identity")
				}
			}
		})
	}
}
