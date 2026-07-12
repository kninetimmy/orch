package cli

import (
	"bytes"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kninetimmy/orch/internal/lockfile"
	"github.com/kninetimmy/orch/internal/question"
)

func TestConfigureBareReportGolden(t *testing.T) {
	env, stdout, stderr := testEnv(t)
	writeConfig(t, env.RepoRoot, validTOML)
	if code := Run([]string{"configure"}, env); code != ExitOK {
		t.Fatalf("exit = %d, want %d; stderr = %s", code, ExitOK, stderr.String())
	}
	checkGolden(t, filepath.Join("testdata", "configure_report.golden.txt"), stdout.Bytes())
}

func TestConfigureBareUninitialized(t *testing.T) {
	env, stdout, stderr := testEnv(t)
	if code := Run([]string{"configure"}, env); code != ExitOK {
		t.Fatalf("exit = %d, want %d; stderr = %s", code, ExitOK, stderr.String())
	}
	if !strings.Contains(stdout.String(), "not initialized") {
		t.Errorf("stdout = %q, want the not-initialized message", stdout.String())
	}
}

func TestConfigureBareNeverReadsStdin(t *testing.T) {
	env, _, stderr := testEnv(t)
	writeConfig(t, env.RepoRoot, validTOML)
	env.Stdin = blockingReader{t: t}
	if code := Run([]string{"configure"}, env); code != ExitOK {
		t.Fatalf("exit = %d, want %d; stderr = %s", code, ExitOK, stderr.String())
	}
}

func TestConfigureMutuallyExclusiveFlagsIsUsageError(t *testing.T) {
	env, _, stderr := testEnv(t)
	if code := Run([]string{"configure", "--step", "--deliver"}, env); code != ExitUsage {
		t.Fatalf("exit = %d, want %d", code, ExitUsage)
	}
	if !strings.Contains(stderr.String(), "usage: orch configure") {
		t.Errorf("stderr = %q", stderr.String())
	}
}

func TestConfigureUnknownFlagIsUsageError(t *testing.T) {
	env, _, stderr := testEnv(t)
	if code := Run([]string{"configure", "--bogus"}, env); code != ExitUsage {
		t.Fatalf("exit = %d, want %d", code, ExitUsage)
	}
	if !strings.Contains(stderr.String(), "usage: orch configure") {
		t.Errorf("stderr = %q", stderr.String())
	}
}

func TestConfigureTrailingArgumentIsUsageError(t *testing.T) {
	env, _, stderr := testEnv(t)
	if code := Run([]string{"configure", "extra"}, env); code != ExitUsage {
		t.Fatalf("exit = %d, want %d", code, ExitUsage)
	}
	if !strings.Contains(stderr.String(), "usage: orch configure") {
		t.Errorf("stderr = %q", stderr.String())
	}
}

func TestConfigureStepFirstDoc(t *testing.T) {
	env, stdout, stderr := testEnv(t)
	writeConfig(t, env.RepoRoot, validTOML)
	req := question.AnswerSet{SchemaVersion: question.SchemaVersion, Answers: map[string]string{}}
	data, err := json.Marshal(req)
	if err != nil {
		t.Fatal(err)
	}
	env.Stdin = bytes.NewReader(data)
	if code := Run([]string{"configure", "--step"}, env); code != ExitOK {
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

func TestConfigureStepMalformedStdinExitsError(t *testing.T) {
	env, _, stderr := testEnv(t)
	writeConfig(t, env.RepoRoot, validTOML)
	env.Stdin = bytes.NewReader([]byte("{not valid json"))
	if code := Run([]string{"configure", "--step"}, env); code != ExitError {
		t.Fatalf("exit = %d, want %d", code, ExitError)
	}
	if stderr.String() == "" {
		t.Error("stderr is empty, want a decode error")
	}
}

func TestConfigureStepUninitializedExitsError(t *testing.T) {
	env, _, stderr := testEnv(t)
	req := question.AnswerSet{SchemaVersion: question.SchemaVersion, Answers: map[string]string{}}
	data, err := json.Marshal(req)
	if err != nil {
		t.Fatal(err)
	}
	env.Stdin = bytes.NewReader(data)
	if code := Run([]string{"configure", "--step"}, env); code != ExitError {
		t.Fatalf("exit = %d, want %d", code, ExitError)
	}
	if !strings.Contains(stderr.String(), "not initialized") {
		t.Errorf("stderr = %q, want config.ErrNotInitialized's message", stderr.String())
	}
}

// TestConfigureRefusesDuringActiveDelivery proves --step fails closed
// while a Delivery lock is held (the `orch configure-local` precedent,
// reused by requireNoActiveDelivery for both commands).
func TestConfigureRefusesDuringActiveDelivery(t *testing.T) {
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
	if code := Run([]string{"configure", "--step"}, env); code != ExitError {
		t.Fatalf("exit = %d, want %d; stderr = %s", code, ExitError, stderr.String())
	}
	if !strings.Contains(stderr.String(), "delivery lock") {
		t.Errorf("stderr = %q, want the delivery-lock refusal", stderr.String())
	}
}

// TestConfigureBareWarnsDuringActiveDelivery proves the bare report
// still exits 0 while a Delivery lock is held, but surfaces a warning
// line.
func TestConfigureBareWarnsDuringActiveDelivery(t *testing.T) {
	env, stdout, stderr := testEnv(t)
	writeConfig(t, env.RepoRoot, validTOML)
	if err := lockfile.Acquire(env.RepoRoot, lockfile.Owner{
		RunID: "r1", Host: "claude", Hostname: "h", PID: 1, AcquiredAt: time.Now(),
	}); err != nil {
		t.Fatal(err)
	}

	if code := Run([]string{"configure"}, env); code != ExitOK {
		t.Fatalf("exit = %d, want %d; stderr = %s", code, ExitOK, stderr.String())
	}
	if !strings.Contains(stdout.String(), "warning:") || !strings.Contains(stdout.String(), "delivery lock") {
		t.Errorf("stdout = %q, want a delivery-lock warning line", stdout.String())
	}
}
