package cli

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kninetimmy/orch/internal/question"
)

// update regenerates golden files instead of comparing against them
// (internal/interview's -update convention).
var update = flag.Bool("update", false, "regenerate golden files")

// initLookPath resolves claude, codex, git, and gh — but never memhub,
// so Detect's fake `memhub status` probe (fakeRunner has no case for
// it) is never exercised by these tests.
func initLookPath(name string) (string, error) {
	switch name {
	case "claude", "codex", "git", "gh":
		return "/fake/" + name, nil
	default:
		return "", fmt.Errorf("%s not found", name)
	}
}

// initEnv returns a testEnv wired so Detect finds both host CLIs, git
// (rooted at RepoRoot), and gh — the fixture every init test not
// specifically probing detection gaps starts from.
func initEnv(t *testing.T) (Env, *bytes.Buffer, *bytes.Buffer) {
	t.Helper()
	env, stdout, stderr := testEnv(t)
	env.LookPath = initLookPath
	env.Runner = fakeRunner{toplevel: env.RepoRoot}
	return env, stdout, stderr
}

func TestInitBareDetectionReportGolden(t *testing.T) {
	env, stdout, stderr := initEnv(t)
	if code := Run([]string{"init"}, env); code != ExitOK {
		t.Fatalf("exit = %d, want %d; stderr = %s", code, ExitOK, stderr.String())
	}
	got := strings.ReplaceAll(stdout.String(), env.RepoRoot, "<ROOT>")
	checkGolden(t, filepath.Join("testdata", "init_report.golden.txt"), []byte(got))
}

func TestInitBareNeverReadsStdin(t *testing.T) {
	env, _, stderr := initEnv(t)
	env.Stdin = blockingReader{t: t}
	if code := Run([]string{"init"}, env); code != ExitOK {
		t.Fatalf("exit = %d, want %d; stderr = %s", code, ExitOK, stderr.String())
	}
}

func TestInitBareAlreadyInitializedStillExitsOK(t *testing.T) {
	env, stdout, stderr := initEnv(t)
	writeConfig(t, env.RepoRoot, validTOML)
	if code := Run([]string{"init"}, env); code != ExitOK {
		t.Fatalf("exit = %d, want %d; stderr = %s", code, ExitOK, stderr.String())
	}
	if !strings.Contains(stdout.String(), "already initialized") || !strings.Contains(stdout.String(), "orch configure") {
		t.Errorf("stdout = %q, want the already-initialized note pointing at orch configure", stdout.String())
	}
}

func TestInitMutuallyExclusiveFlagsIsUsageError(t *testing.T) {
	env, _, stderr := initEnv(t)
	if code := Run([]string{"init", "--step", "--bootstrap"}, env); code != ExitUsage {
		t.Fatalf("exit = %d, want %d", code, ExitUsage)
	}
	if !strings.Contains(stderr.String(), "usage: orch init") {
		t.Errorf("stderr = %q", stderr.String())
	}
}

func TestInitTrailingArgumentIsUsageError(t *testing.T) {
	env, _, stderr := initEnv(t)
	if code := Run([]string{"init", "extra"}, env); code != ExitUsage {
		t.Fatalf("exit = %d, want %d", code, ExitUsage)
	}
	if !strings.Contains(stderr.String(), "usage: orch init") {
		t.Errorf("stderr = %q", stderr.String())
	}
}

func TestInitStepMalformedStdinExitsError(t *testing.T) {
	env, _, stderr := initEnv(t)
	env.Stdin = bytes.NewReader([]byte("{not valid json"))
	if code := Run([]string{"init", "--step"}, env); code != ExitError {
		t.Fatalf("exit = %d, want %d", code, ExitError)
	}
	if stderr.String() == "" {
		t.Error("stderr is empty, want a decode error")
	}
}

func TestInitStepUnknownAnswerExitsErrorWithMessage(t *testing.T) {
	env, _, stderr := initEnv(t)
	req := question.AnswerSet{SchemaVersion: question.SchemaVersion, Answers: map[string]string{"bogus.key": "x"}}
	data, err := json.Marshal(req)
	if err != nil {
		t.Fatal(err)
	}
	env.Stdin = bytes.NewReader(data)
	if code := Run([]string{"init", "--step"}, env); code != ExitError {
		t.Fatalf("exit = %d, want %d", code, ExitError)
	}
	if !strings.Contains(stderr.String(), "answer does not match a currently applicable question") {
		t.Errorf("stderr = %q, want ErrUnknownAnswer's message", stderr.String())
	}
}

func TestInitStepAlreadyInitializedExitsError(t *testing.T) {
	env, _, stderr := initEnv(t)
	writeConfig(t, env.RepoRoot, validTOML)
	req := question.AnswerSet{SchemaVersion: question.SchemaVersion, Answers: map[string]string{}}
	data, err := json.Marshal(req)
	if err != nil {
		t.Fatal(err)
	}
	env.Stdin = bytes.NewReader(data)
	if code := Run([]string{"init", "--step"}, env); code != ExitError {
		t.Fatalf("exit = %d, want %d", code, ExitError)
	}
	if !strings.Contains(stderr.String(), "already initialized") || !strings.Contains(stderr.String(), "orch configure") {
		t.Errorf("stderr = %q, want the already-initialized message naming orch configure", stderr.String())
	}
}

// stepOnce sends req to `orch init --step` and decodes the resulting
// Document, failing the test on any non-zero exit.
func stepOnce(t *testing.T, env Env, req question.AnswerSet) question.Document {
	t.Helper()
	data, err := json.Marshal(req)
	if err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	env.Stdin = bytes.NewReader(data)
	env.Stdout = &stdout
	env.Stderr = &stderr
	if code := Run([]string{"init", "--step"}, env); code != ExitOK {
		t.Fatalf("exit = %d, want %d; stderr = %s", code, ExitOK, stderr.String())
	}
	var doc question.Document
	if err := json.Unmarshal(stdout.Bytes(), &doc); err != nil {
		t.Fatalf("decode step document: %v\n%s", err, stdout.String())
	}
	return doc
}

// TestInitStepEndToEndReachesComplete walks `orch init --step` from an
// empty answer set to the terminal complete document over the CLI's
// own stdin/stdout boundary, answering every question with its
// default and approving the summary — the CLI-level analogue of
// internal/interview's own golden-transcript walk
// (TestGoldenTranscriptBothHosts), proving the decode-stdin ->
// Detect -> Next -> encode-stdout wiring round-trips correctly.
//
// It does not byte-compare against internal/interview's own golden
// transcript files: those pin Facts.GitRoot to the literal placeholder
// "/repo", decoupled from the real repoRoot the interview package's
// own tests pass to Next directly. cli/init.go, per spec, threads
// facts.GitRoot itself into Next (falling back to Env.RepoRoot only
// when git is absent), so a real, on-disk root is load-bearing here —
// buildSummary reads CLAUDE.md/AGENTS.md/.gitignore from it. A CLI
// test cannot reuse "/repo" without breaking that file I/O, so this
// walk instead proves the same live behavior end to end.
func TestInitStepEndToEndReachesComplete(t *testing.T) {
	env, _, _ := initEnv(t)
	req := question.AnswerSet{SchemaVersion: question.SchemaVersion, Answers: map[string]string{}}

	const maxSteps = 20
	for step := 1; step <= maxSteps; step++ {
		doc := stepOnce(t, env, req)
		if doc.SchemaVersion != question.SchemaVersion {
			t.Fatalf("step %d: schema_version = %d, want %d", step, doc.SchemaVersion, question.SchemaVersion)
		}
		switch doc.Kind {
		case question.DocQuestions:
			for _, q := range doc.Questions {
				if q.Default == "" {
					t.Fatalf("step %d: question %s has no default", step, q.ID)
				}
				req.Answers[q.ID] = q.Default
			}
		case question.DocSummary:
			if len(doc.Summary.Blockers) != 0 {
				t.Fatalf("step %d: unexpected blockers: %v", step, doc.Summary.Blockers)
			}
			req.Answers["approval"] = "approve"
		case question.DocComplete:
			if !doc.Complete.BootstrapReady {
				t.Fatalf("step %d: BootstrapReady = false, want true (git and gh were both detected): %+v", step, doc.Complete)
			}
			if len(doc.Complete.Summary.Files) != 2 {
				t.Errorf("step %d: Summary.Files = %v, want CLAUDE.md and AGENTS.md (both hosts enabled)", step, doc.Complete.Summary.Files)
			}
			return
		default:
			t.Fatalf("step %d: unexpected document kind %q", step, doc.Kind)
		}
	}
	t.Fatalf("did not reach a complete document within %d steps", maxSteps)
}

// TestInitBootstrapAlreadyInitializedExitsError proves --bootstrap
// wiring: a fully-walked, valid answer set re-derives to a ready
// Complete document, but a committed configuration already on disk
// makes internal/bootstrap.Execute fail closed before any mutation —
// exercised here through the CLI boundary (decode stdin, call
// Execute, exit 1 with its message on stderr).
func TestInitBootstrapAlreadyInitializedExitsError(t *testing.T) {
	env, _, _ := initEnv(t)
	req := question.AnswerSet{SchemaVersion: question.SchemaVersion, Answers: map[string]string{}}
	for step := 1; step <= 20; step++ {
		doc := stepOnce(t, env, req)
		switch doc.Kind {
		case question.DocQuestions:
			for _, q := range doc.Questions {
				req.Answers[q.ID] = q.Default
			}
		case question.DocSummary:
			req.Answers["approval"] = "approve"
		case question.DocComplete:
			step = 999 // done walking
		default:
			t.Fatalf("unexpected document kind %q", doc.Kind)
		}
		if step == 999 {
			break
		}
	}

	writeConfig(t, env.RepoRoot, validTOML)
	data, err := json.Marshal(req)
	if err != nil {
		t.Fatal(err)
	}
	env.Stdin = bytes.NewReader(data)
	var stdout, stderr bytes.Buffer
	env.Stdout = &stdout
	env.Stderr = &stderr
	if code := Run([]string{"init", "--bootstrap"}, env); code != ExitError {
		t.Fatalf("exit = %d, want %d; stdout = %s stderr = %s", code, ExitError, stdout.String(), stderr.String())
	}
	if !strings.Contains(stderr.String(), "already initialized") {
		t.Errorf("stderr = %q, want the already-initialized message", stderr.String())
	}
}

// checkGolden compares got against the golden file at path, rewriting
// it first when -update is passed (internal/interview's convention).
func checkGolden(t *testing.T, path string, got []byte) {
	t.Helper()
	if *update {
		if err := os.WriteFile(path, got, 0o644); err != nil {
			t.Fatalf("write golden: %v", err)
		}
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read golden (run `go test ./internal/cli -update`): %v", err)
	}
	if string(got) != string(want) {
		t.Errorf("does not match %s\n--- got ---\n%s\n--- want ---\n%s", path, got, want)
	}
}
