package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/kninetimmy/orch/internal/evalcorpus"
	"github.com/kninetimmy/orch/internal/evalplan"
)

func TestEvalControllerCLIProcess(t *testing.T) {
	f := newEvalFixture(t)
	// Complete the existing synthetic source declarations. No frozen corpus,
	// host credentials, live model trial or executable grader is needed.
	for _, c := range f.manifest.Cases {
		files := append(append([]evalcorpus.File{}, c.Inputs...), c.Key, c.Probe)
		for _, control := range c.Controls {
			files = append(files, control.Files...)
		}
		for _, file := range files {
			name := filepath.Join(filepath.Dir(f.file), filepath.FromSlash(file.Source))
			if err := os.MkdirAll(filepath.Dir(name), 0o700); err != nil {
				t.Fatal(err)
			}
			evalWrite(t, name, []byte("synthetic public input"))
		}
	}
	binary := filepath.Join(filepath.Dir(f.repo), "orch-eval-cli")
	if runtime.GOOS == "windows" {
		binary += ".exe"
	}
	build := exec.CommandContext(t.Context(), "go", "build", "-o", binary, "../../cmd/orch")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build public CLI: %v: %s", err, out)
	}
	git, err := exec.LookPath("git")
	if err != nil {
		t.Fatal(err)
	}
	run := func(args ...string) (int, string, string) {
		t.Helper()
		ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
		defer cancel()
		cmd := exec.CommandContext(ctx, binary, args...)
		cmd.Dir = f.repo
		// Keep only local Git on PATH: native refusal must not launch installed
		// host software or consult its credentials in this public-process check.
		for _, entry := range os.Environ() {
			if !strings.HasPrefix(strings.ToUpper(entry), "PATH=") {
				cmd.Env = append(cmd.Env, entry)
			}
		}
		cmd.Env = append(cmd.Env, "PATH="+filepath.Dir(git))
		cmd.WaitDelay = 2 * time.Second
		var stdout, stderr bytes.Buffer
		cmd.Stdout, cmd.Stderr = &stdout, &stderr
		err := cmd.Run()
		if err == nil {
			return 0, stdout.String(), stderr.String()
		}
		if exit, ok := err.(*exec.ExitError); ok {
			return exit.ExitCode(), stdout.String(), stderr.String()
		}
		t.Fatalf("public CLI spawn: %v", err)
		return -1, "", ""
	}
	code, out, failure := run("eval", "preview", "--plan", f.file, "--json")
	if code != ExitOK {
		t.Fatalf("preview: %d %s", code, failure)
	}
	var record evalplan.Record
	if err := json.Unmarshal([]byte(out), &record); err != nil {
		t.Fatal(err)
	}
	root := record.Plan.StorageRoot
	invoke := []string{"eval", "run", "--plan", record.PlanDigest, "--storage-root", root, "--json"}
	if code, out, failure := run(invoke...); code != ExitError || !strings.Contains(failure, "human approval") || !strings.Contains(out, record.PlanDigest) {
		t.Fatalf("approval gate: %d %s %s", code, out, failure)
	}
	for _, args := range [][]string{
		{"eval", "run", "--plan", record.PlanDigest},
		{"eval", "run", "--plan", record.PlanDigest, "--storage-root", root, "--json", "--json"},
		{"eval", "status", "--run", "bad", "--storage-root", root, "--plan", record.PlanDigest},
		{"eval", "report", "--run", "bad", "--storage-root", root},
		{"eval", "report", "--run", "bad", "--storage-root", root, "--format", "html"},
		{"eval", "stop", "--run", "bad", "--storage-root", root, "--execute"},
	} {
		if code, _, _ := run(args...); code != ExitUsage {
			t.Fatalf("strict arguments %v: %d", args, code)
		}
	}
	approval := filepath.Join(filepath.Dir(f.file), "approval.json")
	valid := evalplan.Approval{SchemaVersion: 1, PlanDigest: record.PlanDigest, ApprovedBy: "test-human", ApprovedAt: time.Now().UTC().Format(time.RFC3339Nano), Statement: evalplan.ApprovalStatement}
	approved := append(append([]string{}, invoke...), "--approval", approval)
	for _, change := range []func(*evalplan.Approval){
		func(a *evalplan.Approval) { a.PlanDigest = "sha256:" + strings.Repeat("0", 64) },
		func(a *evalplan.Approval) { a.Statement = "approve-and-enter-delivery" },
		func(a *evalplan.Approval) { a.ApprovedBy = "" },
		func(a *evalplan.Approval) { a.ApprovedAt = time.Now().Add(-25 * time.Hour).Format(time.RFC3339Nano) },
		func(a *evalplan.Approval) { a.ApprovedAt = time.Now().Add(time.Hour).Format(time.RFC3339Nano) },
	} {
		bad := valid
		change(&bad)
		evalWrite(t, approval, evalJSON(t, bad))
		if code, _, failure := run(approved...); code != ExitError || !strings.Contains(failure, "approval") {
			t.Fatalf("invalid approval: %d %s", code, failure)
		}
	}
	for _, bad := range []string{`{"schema_version":1,"schema_version":1}`, `{"schema_version":1}`, `{"schema_version":1,"unknown":true}`, `null`} {
		evalWrite(t, approval, []byte(bad))
		if code, _, failure := run(approved...); code != ExitError || !strings.Contains(failure, "approval JSON") {
			t.Fatalf("strict approval JSON: %d %s", code, failure)
		}
	}
	evalWrite(t, approval, evalJSON(t, valid))
	if err := os.Link(approval, approval+".linked"); err != nil {
		t.Fatal(err)
	}
	if code, _, failure := run(approved...); code != ExitError || !strings.Contains(failure, "linked file") {
		t.Fatalf("linked approval: %d %s", code, failure)
	}
	if err := os.Remove(approval + ".linked"); err != nil {
		t.Fatal(err)
	}
	profile := filepath.Join(filepath.Dir(f.file), f.plan.Baseline.Profile.Path)
	original, err := os.ReadFile(profile)
	if err != nil {
		t.Fatal(err)
	}
	evalWrite(t, profile, append(append([]byte{}, original...), []byte("\n# changed pinned input\n")...))
	if code, _, failure := run(approved...); code != ExitError || !strings.Contains(failure, "revalidation") {
		t.Fatalf("saved-plan revalidation: %d %s", code, failure)
	}
	evalWrite(t, profile, original)
	code, out, failure = run(approved...)
	if code != ExitError || !strings.Contains(failure, "refused") {
		t.Fatalf("approved production refusal: %d %s", code, failure)
	}
	decoder := json.NewDecoder(strings.NewReader(out))
	var scope evalplan.ApprovalScope
	var retained evalplan.Report
	if err := decoder.Decode(&scope); err != nil {
		t.Fatal(err)
	}
	if err := decoder.Decode(&retained); err != nil {
		t.Fatal(err)
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		t.Fatal("unexpected run JSON document")
	}
	if !reflect.DeepEqual(scope, retained.Snapshot.Scope) || retained.Snapshot.Progress.State != "refused" || retained.Snapshot.UnrunUnits != 15 || retained.Snapshot.AttemptsConsumed != 1 || retained.Snapshot.RepairsConsumed != 0 || len(retained.Snapshot.Observations) != 0 || retained.Snapshot.Approval == nil {
		t.Fatal("scope/refusal/schedule/missingness changed")
	}
	if code, _, failure := run(approved...); code != ExitError || !strings.Contains(failure, "already consumed") {
		t.Fatalf("approval replay: %d %s", code, failure)
	}
	id := retained.Snapshot.EvaluationID
	// Retained operations must work even after the original source disappears.
	missing := filepath.Dir(f.file) + "-unavailable"
	if err := os.Rename(filepath.Dir(f.file), missing); err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := os.Rename(missing, filepath.Dir(f.file)); err != nil {
			t.Error(err)
		}
	}()
	statusArgs := []string{"eval", "status", "--run", id, "--storage-root", root, "--json"}
	if code, out, failure := run(statusArgs...); code != ExitOK || !strings.Contains(out, id) {
		t.Fatalf("retained status: %d %s", code, failure)
	}
	for _, format := range []string{"text", "markdown", "json"} {
		code, out, failure := run("eval", "report", "--run", id, "--storage-root", root, "--format", format)
		if code != ExitOK {
			t.Fatalf("retained report %s: %d %s", format, code, failure)
		}
		start, end := strings.Index(out, "{\n"), strings.LastIndex(out, "}")+1
		var parity evalplan.Report
		if err := json.Unmarshal([]byte(out[start:end]), &parity); err != nil || !reflect.DeepEqual(parity, retained) {
			t.Fatalf("public report parity %s: %v", format, err)
		}
		if strings.Contains(out, "PRIVATE_") {
			t.Fatal("private artifact prose leaked")
		}
	}
	stopArgs := []string{"eval", "stop", "--run", id, "--storage-root", root, "--json"}
	var stopped evalplan.Report
	for range 2 {
		code, out, failure := run(stopArgs...)
		if code != ExitOK {
			t.Fatalf("stop receipt: %d %s", code, failure)
		}
		if err := json.Unmarshal([]byte(out), &stopped); err != nil || !stopped.Snapshot.StopRequested || stopped.Snapshot.StopAcknowledged || stopped.Snapshot.Progress.State != "refused" {
			t.Fatal("request receipt became termination acknowledgement")
		}
	}
	if code, _, failure := run("eval", "report", "--run", id, "--storage-root", root, "--format", "json"); code != ExitOK {
		t.Fatalf("later report: %d %s", code, failure)
	}
	if stopped.Destination == retained.Destination {
		t.Fatal("later evidence reused earlier snapshot")
	}
	if _, err := os.Stat(filepath.Join(retained.Destination, "report.json")); err != nil {
		t.Fatal("earlier report removed")
	}
	evalWrite(t, filepath.Join(stopped.Destination, "report.json"), []byte("report-tamper-sentinel"))
	if code, _, failure := run("eval", "report", "--run", id, "--storage-root", root, "--format", "json"); code != ExitError || !strings.Contains(failure, "conflict") {
		t.Fatalf("report tampering: %d %s", code, failure)
	}
	if code, _, failure := run(statusArgs...); code != ExitOK {
		t.Fatalf("report poisoning status: %d %s", code, failure)
	}
	progress := filepath.Join(root, id, "progress-000000.json")
	evalWrite(t, progress, []byte("progress-tamper-sentinel"))
	if code, _, _ := run(statusArgs...); code != ExitError {
		t.Fatal("corrupt chain accepted")
	}
	if data, err := os.ReadFile(progress); err != nil || string(data) != "progress-tamper-sentinel" {
		t.Fatal("corrupt evidence overwritten")
	}
	t.Log("Compiled public CLI: strict inputs/approvals, saved-plan revalidation, production refusal, retained status/stop/report parity, single-use approval and immutable conflict preservation; no host/model execution.")
}
