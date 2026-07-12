package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kninetimmy/orch/internal/config"
	"github.com/kninetimmy/orch/internal/lockfile"
	"github.com/kninetimmy/orch/internal/question"
)

func TestConfigureLocalBareReportGolden(t *testing.T) {
	env, stdout, stderr := testEnv(t)
	writeConfig(t, env.RepoRoot, validTOML)
	if code := Run([]string{"configure-local"}, env); code != ExitOK {
		t.Fatalf("exit = %d, want %d; stderr = %s", code, ExitOK, stderr.String())
	}
	got := strings.ReplaceAll(stdout.String(), env.RepoRoot, "<ROOT>")
	checkGolden(t, filepath.Join("testdata", "configure_local_report.golden.txt"), []byte(got))
}

func TestConfigureLocalBareUninitialized(t *testing.T) {
	env, stdout, stderr := testEnv(t)
	if code := Run([]string{"configure-local"}, env); code != ExitOK {
		t.Fatalf("exit = %d, want %d; stderr = %s", code, ExitOK, stderr.String())
	}
	if !strings.Contains(stdout.String(), "not initialized") {
		t.Errorf("stdout = %q, want the not-initialized message", stdout.String())
	}
}

func TestConfigureLocalBareNeverReadsStdin(t *testing.T) {
	env, _, stderr := testEnv(t)
	writeConfig(t, env.RepoRoot, validTOML)
	env.Stdin = blockingReader{t: t}
	if code := Run([]string{"configure-local"}, env); code != ExitOK {
		t.Fatalf("exit = %d, want %d; stderr = %s", code, ExitOK, stderr.String())
	}
}

func TestConfigureLocalMutuallyExclusiveFlagsIsUsageError(t *testing.T) {
	env, _, stderr := testEnv(t)
	if code := Run([]string{"configure-local", "--step", "--apply"}, env); code != ExitUsage {
		t.Fatalf("exit = %d, want %d", code, ExitUsage)
	}
	if !strings.Contains(stderr.String(), "usage: orch configure-local") {
		t.Errorf("stderr = %q", stderr.String())
	}
}

func TestConfigureLocalUnknownFlagIsUsageError(t *testing.T) {
	env, _, stderr := testEnv(t)
	if code := Run([]string{"configure-local", "--bogus"}, env); code != ExitUsage {
		t.Fatalf("exit = %d, want %d", code, ExitUsage)
	}
	if !strings.Contains(stderr.String(), "usage: orch configure-local") {
		t.Errorf("stderr = %q", stderr.String())
	}
}

func TestConfigureLocalStepFirstDoc(t *testing.T) {
	env, stdout, stderr := testEnv(t)
	writeConfig(t, env.RepoRoot, validTOML)
	req := question.AnswerSet{SchemaVersion: question.SchemaVersion, Answers: map[string]string{}}
	data, err := json.Marshal(req)
	if err != nil {
		t.Fatal(err)
	}
	env.Stdin = bytes.NewReader(data)
	if code := Run([]string{"configure-local", "--step"}, env); code != ExitOK {
		t.Fatalf("exit = %d, want %d; stderr = %s", code, ExitOK, stderr.String())
	}
	var doc question.Document
	if err := json.Unmarshal(stdout.Bytes(), &doc); err != nil {
		t.Fatalf("decode step document: %v\n%s", err, stdout.String())
	}
	if doc.Kind != question.DocQuestions {
		t.Fatalf("Kind = %q, want %q", doc.Kind, question.DocQuestions)
	}
	if len(doc.Questions) == 0 {
		t.Fatal("Questions is empty")
	}
}

func TestConfigureLocalStepMalformedStdinExitsError(t *testing.T) {
	env, _, stderr := testEnv(t)
	writeConfig(t, env.RepoRoot, validTOML)
	env.Stdin = bytes.NewReader([]byte("{not valid json"))
	if code := Run([]string{"configure-local", "--step"}, env); code != ExitError {
		t.Fatalf("exit = %d, want %d", code, ExitError)
	}
	if stderr.String() == "" {
		t.Error("stderr is empty, want a decode error")
	}
}

// configureLocalStepOnce sends req to `orch configure-local --step` and
// decodes the resulting Document, failing the test on any non-zero
// exit (init_test.go's stepOnce, mirrored for configure-local).
func configureLocalStepOnce(t *testing.T, env Env, req question.AnswerSet) question.Document {
	t.Helper()
	data, err := json.Marshal(req)
	if err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	env.Stdin = bytes.NewReader(data)
	env.Stdout = &stdout
	env.Stderr = &stderr
	if code := Run([]string{"configure-local", "--step"}, env); code != ExitOK {
		t.Fatalf("exit = %d, want %d; stderr = %s", code, ExitOK, stderr.String())
	}
	var doc question.Document
	if err := json.Unmarshal(stdout.Bytes(), &doc); err != nil {
		t.Fatalf("decode step document: %v\n%s", err, stdout.String())
	}
	return doc
}

// TestConfigureLocalApplyEndToEnd walks `orch configure-local --step`
// from an empty answer set to the terminal complete document over the
// CLI's own stdin/stdout boundary — changing one model along the way so
// the session is not blocked — then applies it and confirms
// config.local.toml on disk carries the new override.
func TestConfigureLocalApplyEndToEnd(t *testing.T) {
	env, _, _ := testEnv(t)
	writeConfig(t, env.RepoRoot, validTOML)

	req := question.AnswerSet{SchemaVersion: question.SchemaVersion, Answers: map[string]string{}}
	const maxSteps = 20
	complete := false
	for step := 1; step <= maxSteps && !complete; step++ {
		doc := configureLocalStepOnce(t, env, req)
		switch doc.Kind {
		case question.DocQuestions:
			for _, q := range doc.Questions {
				switch q.ID {
				case "pick.claude":
					req.Answers[q.ID] = "yes" // walk claude's role docs so scout.model below is reachable
				case "hosts.claude.roles.scout.model":
					req.Answers[q.ID] = "claude-fable-5"
				default:
					req.Answers[q.ID] = q.Default
				}
			}
		case question.DocSummary:
			if len(doc.Summary.Blockers) != 0 {
				t.Fatalf("step %d: unexpected blockers: %v", step, doc.Summary.Blockers)
			}
			req.Answers["approval"] = "approve"
		case question.DocComplete:
			complete = true
		default:
			t.Fatalf("step %d: unexpected document kind %q", step, doc.Kind)
		}
	}
	if !complete {
		t.Fatalf("did not reach a complete document within %d steps", maxSteps)
	}

	data, err := json.Marshal(req)
	if err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	env.Stdin = bytes.NewReader(data)
	env.Stdout = &stdout
	env.Stderr = &stderr
	if code := Run([]string{"configure-local", "--apply"}, env); code != ExitOK {
		t.Fatalf("exit = %d, want %d; stderr = %s", code, ExitOK, stderr.String())
	}

	var report configureLocalReport
	if err := json.Unmarshal(stdout.Bytes(), &report); err != nil {
		t.Fatalf("decode apply report: %v\n%s", err, stdout.String())
	}
	if report.SchemaVersion != configureLocalReportSchemaVersion {
		t.Errorf("SchemaVersion = %d, want %d", report.SchemaVersion, configureLocalReportSchemaVersion)
	}
	if report.Action != "written" {
		t.Errorf("Action = %q, want written", report.Action)
	}
	if len(report.Overrides) == 0 {
		t.Error("Overrides is empty, want the new override recorded")
	}

	got, err := os.ReadFile(filepath.Join(env.RepoRoot, filepath.FromSlash(config.LocalOverridePath)))
	if err != nil {
		t.Fatalf("read config.local.toml: %v", err)
	}
	if !strings.Contains(string(got), "claude-fable-5") {
		t.Errorf("config.local.toml does not carry the new override:\n%s", got)
	}
}

// TestConfigureLocalApplyNotCompleteIsError proves --apply refuses an
// answer set that has not yet re-derived to an approved configuration.
func TestConfigureLocalApplyNotCompleteIsError(t *testing.T) {
	env, _, stderr := testEnv(t)
	writeConfig(t, env.RepoRoot, validTOML)

	req := question.AnswerSet{SchemaVersion: question.SchemaVersion, Answers: map[string]string{}}
	data, err := json.Marshal(req)
	if err != nil {
		t.Fatal(err)
	}
	env.Stdin = bytes.NewReader(data)
	if code := Run([]string{"configure-local", "--apply"}, env); code != ExitError {
		t.Fatalf("exit = %d, want %d", code, ExitError)
	}
	if !strings.Contains(stderr.String(), "do not re-derive to an approved configuration") {
		t.Errorf("stderr = %q", stderr.String())
	}
}

// TestConfigureLocalRefusesDuringActiveDelivery proves both --step and
// --apply fail closed while a Delivery lock is held.
func TestConfigureLocalRefusesDuringActiveDelivery(t *testing.T) {
	for _, form := range []string{"--step", "--apply"} {
		t.Run(form, func(t *testing.T) {
			env, _, stderr := testEnv(t)
			writeConfig(t, env.RepoRoot, validTOML)
			if err := lockfile.Acquire(env.RepoRoot, lockfile.Owner{
				RunID: "r1", Host: "claude", Hostname: "h", PID: 1, AcquiredAt: time.Now(),
			}); err != nil {
				t.Fatal(err)
			}

			req := question.AnswerSet{SchemaVersion: question.SchemaVersion, Answers: map[string]string{}}
			data, err := json.Marshal(req)
			if err != nil {
				t.Fatal(err)
			}
			env.Stdin = bytes.NewReader(data)
			if code := Run([]string{"configure-local", form}, env); code != ExitError {
				t.Fatalf("exit = %d, want %d; stderr = %s", code, ExitError, stderr.String())
			}
			if !strings.Contains(stderr.String(), "delivery lock") {
				t.Errorf("stderr = %q, want the delivery-lock refusal", stderr.String())
			}
		})
	}
}
