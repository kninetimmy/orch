package evalplan

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"regexp"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/kninetimmy/orch/internal/codexnative"
	"github.com/kninetimmy/orch/internal/evalcorpus"
	"github.com/kninetimmy/orch/internal/metrics"
)

const privateSentinel = "CONTROLLER_ONLY_KEY_PROBE_SOLUTION_SENTINEL"

// Run the heavy real-filesystem scenario in this same test binary, without
// cmd/go's -test.testlogfile. Go 1.26 replays millions of repeated outside-module
// stat/open records during cache publication. The parent still checks every
// assertion's process exit and output; no test logger/cache policy is changed.
func controllerTestProcess(t *testing.T) bool {
	t.Helper()
	const marker = "ORCH_CONTROLLER_TEST_SCENARIO"
	if os.Getenv(marker) == t.Name() {
		t.Logf("controller scenario child: %s", t.Name())
		return false
	}
	binary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 8*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, binary, "-test.run=^"+regexp.QuoteMeta(t.Name())+"$", "-test.v", "-test.timeout=8m")
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	cmd.Dir, cmd.Env = dir, append(os.Environ(), marker+"="+t.Name())
	cmd.WaitDelay = 2 * time.Second
	var output gitOutput
	cmd.Stdout, cmd.Stderr = &output, &output
	err = cmd.Run()
	t.Logf("%s", output.String())
	if err != nil {
		t.Fatalf("controller scenario child failed: %v", err)
	}
	if !strings.Contains(output.String(), "controller scenario child: "+t.Name()) || !strings.Contains(output.String(), "--- PASS: "+t.Name()) {
		t.Fatal("exact controller scenario did not execute successfully")
	}
	return true
}

type controllerFixture struct {
	repo, inputs, root, workers, scratch, file string
	proposal                                   Proposal
	record                                     *Record
}

func writeFixture(t *testing.T, name string, data []byte) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(name), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(name, data, 0o600); err != nil {
		t.Fatal(err)
	}
}

func fixtureJSON(t *testing.T, value any) []byte {
	t.Helper()
	data, err := storedBytes(value)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func fixtureGit(t *testing.T, repo, stdin string, args ...string) string {
	t.Helper()
	command := exec.CommandContext(t.Context(), "git", args...)
	command.Dir, command.Stdin = repo, strings.NewReader(stdin)
	command.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0", "GIT_NO_LAZY_FETCH=1", "GIT_NO_REPLACE_OBJECTS=1")
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("synthetic Git fixture: %v: %s", err, output)
	}
	return strings.TrimSpace(string(output))
}

func newControllerFixture(t *testing.T, scope string, repetitions int64) *controllerFixture {
	t.Helper()
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	f := &controllerFixture{repo: filepath.Join(base, "repo"), inputs: filepath.Join(base, "inputs"), root: filepath.Join(base, "store"), workers: filepath.Join(base, "workers"), scratch: filepath.Join(base, "scratch")}
	for _, name := range []string{f.repo, f.inputs, f.root, f.workers, f.scratch} {
		if err := os.Mkdir(name, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	fixtureGit(t, f.repo, "", "init", "--quiet")
	public := []byte("package packet\n// synthetic public history\n")
	blob := fixtureGit(t, f.repo, string(public), "hash-object", "-w", "--stdin")
	tree := fixtureGit(t, f.repo, "100644 blob "+blob+"\tpublic.go\n", "mktree")
	oid := fixtureGit(t, f.repo, "tree "+tree+"\nauthor Fixture <fixture@example.invalid> 1 +0000\ncommitter Fixture <fixture@example.invalid> 1 +0000\n\ncontroller fixture\n", "hash-object", "-w", "-t", "commit", "--stdin")
	fixtureGit(t, f.repo, "", "update-ref", "HEAD", oid)
	profile, err := os.ReadFile("../config/testdata/valid/full.toml")
	if err != nil {
		t.Fatal(err)
	}
	writeFixture(t, filepath.Join(f.repo, ".orchestrator/config.toml"), profile)
	writeFixture(t, filepath.Join(f.repo, ".orchestrator/state.json"), []byte("Delivery state sentinel"))
	writeFixture(t, filepath.Join(f.repo, ".orchestrator/delivery.lock"), []byte("Delivery lock sentinel"))
	artifact := func(name string, data []byte) Artifact {
		writeFixture(t, filepath.Join(f.inputs, filepath.FromSlash(name)), data)
		return Artifact{Path: name, SHA256: evalcorpus.Digest(data)}
	}
	publicFile := func(path, source string) evalcorpus.File {
		data := []byte("Declared public " + path + "\n")
		artifact(source, data)
		return evalcorpus.File{Path: path, Source: source, SHA256: evalcorpus.Digest(data)}
	}
	private := func(path, source string) evalcorpus.File {
		artifact(source, []byte(privateSentinel))
		return evalcorpus.File{Path: path, Source: source, SHA256: evalcorpus.Digest([]byte(privateSentinel))}
	}
	manifest := evalcorpus.Manifest{Version: 1, Author: "Synthetic fixture", PreparedDate: "2026-10-03", Prerequisites: []string{"local Git"}, Cases: []evalcorpus.Case{}}
	ids := []string{}
	for partitionIndex, partition := range []string{"development", "held-out"} {
		for _, role := range []string{"scout", "implementation", "review"} {
			for variant := range 2 {
				id := fmt.Sprintf("%s-%d-%d", role, partitionIndex, variant)
				inputs := []evalcorpus.File{publicFile("TASK.md", "cases/"+id+"/task.md"), publicFile("ROLE.md", "roles/"+role+".md"), publicFile("CONTEXT.md", "public/context.md"), {Path: "code/public.go", Source: "public.go", Commit: oid, SHA256: evalcorpus.Digest(public)}}
				c := evalcorpus.Case{ID: id, Version: 1, Role: role, Partition: partition, Lineage: id, Difficulty: []string{"mechanical", "ordinary", "demanding"}[(partitionIndex+variant)%3], SourceCommit: oid, Upstream: []string{"synthetic"}, Selection: "fixture", Inputs: inputs, PacketSHA256: evalcorpus.PacketDigest(inputs), Key: private("key.txt", "private/key.txt"), Probe: private("probe.go", "private/probe.txt"), Command: []string{"go", "test", "-json", "-count=1", "-timeout=30s", "-run=^TestCorpus$", "./..."}, CheckSeconds: 30, Alternative: "synthetic", Controls: []evalcorpus.Control{{Name: "reference", Purpose: "known-good fixture", Files: []evalcorpus.File{private("solution.go", "private/reference.txt")}, ExpectedFailures: []string{}}, {Name: "bad", Purpose: "known-bad fixture", Files: []evalcorpus.File{private("solution.go", "private/bad.txt")}, ExpectedFailures: []string{"TestCorpus/fixture"}}}}
				if role == "review" {
					c.Classification = []string{"clean", "defective"}[variant]
				}
				manifest.Cases = append(manifest.Cases, c)
				ids = append(ids, id)
			}
		}
	}
	corpus := artifact("manifest.json", fixtureJSON(t, manifest))
	profileRef := artifact("profile.toml", profile)
	decision := artifact("decision.txt", []byte("Opaque decision; no controller command execution."))
	readiness := artifact("readiness.txt", []byte("CLAIMED_READY=true; CLAIMED_APPROVED=true; these claims are opaque"))
	repairs := int64(1)
	f.proposal = Proposal{Version: 1, Scope: scope, Intervention: "none", Corpus: corpus, Cases: ids, Partitions: []string{"development", "held-out"}, Baseline: Selection{OrchRevision: oid, Profile: profileRef}, Repetitions: repetitions, Limits: Limits{OverallSeconds: 600, AttemptSeconds: 10, VerificationSeconds: 3, CleanupSeconds: 2, MaxAttemptsPerUnit: 2, MaxRepairsPerUnit: &repairs}, Measurement: Measurement{Source: "codex-app-server", Scope: "task-agents"}, DecisionRule: decision, Readiness: Readiness{IndependentValidation: &readiness, Exposure: &readiness, NativeExecution: &readiness}, StorageRoot: f.root, WorkerRoots: []string{f.workers}, ScratchRoots: []string{f.scratch}}
	if scope == "screen" {
		f.proposal.Cases, f.proposal.Partitions = []string{"scout-0-0", "implementation-0-0", "review-0-0", "review-0-1"}, []string{"development"}
	}
	if scope == "matched" {
		candidate := f.proposal.Baseline
		f.proposal.Candidate, f.proposal.Intervention = &candidate, "requested-profile"
	}
	f.file = filepath.Join(f.inputs, "proposal.json")
	f.preview(t)
	return f
}

func (f *controllerFixture) preview(t *testing.T) {
	t.Helper()
	writeFixture(t, f.file, fixtureJSON(t, f.proposal))
	var err error
	f.record, err = Preview(t.Context(), f.repo, f.file, nil)
	if err != nil {
		t.Fatal(err)
	}
}

func (f *controllerFixture) prepare(t *testing.T) *Evaluation {
	t.Helper()
	e, err := Prepare(t.Context(), f.repo, f.root, f.record.PlanDigest)
	if err != nil {
		t.Fatal(err)
	}
	return e
}

// Every successful executor is defined in tests, with no production registration.
type scriptedWorker struct {
	call func(context.Context, workerRequest) (workerResult, error)
}

func (w scriptedWorker) execute(ctx context.Context, r workerRequest) (workerResult, error) {
	return w.call(ctx, r)
}

func assertPublicPacket(t *testing.T, request workerRequest) {
	t.Helper()
	g, err := openGuarded(request.Layout.Workspace)
	if err != nil {
		t.Fatal(err)
	}
	defer g.close()
	files, err := snapshot(t.Context(), g)
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 4 {
		t.Fatalf("unexpected packet files: %v", sortedKeys(files))
	}
	for name, data := range files {
		if bytes.Contains(data, []byte(privateSentinel)) || strings.Contains(name, ".git") || strings.Contains(name, "private/") {
			t.Fatalf("controller/history material in packet: %s", name)
		}
	}
	if len(request.Layout.CredentialPaths) == 0 || request.Layout.MainCheckout == "" || request.Layout.ControllerState == "" || len(request.Layout.SiblingWorkspaces) == 0 {
		t.Fatal("incomplete protected layout")
	}
}

func readAttempt(t *testing.T, f *controllerFixture, e *Evaluation, unit, attempt int) AttemptRecord {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(f.root, e.ID, attemptName(unit, attempt), "attempt.json"))
	if err != nil {
		t.Fatal(err)
	}
	var record AttemptRecord
	if err := strictStored(data, &record); err != nil {
		t.Fatal(err)
	}
	return record
}

func TestControllerSchedulesAndArtifactSeparation(t *testing.T) {
	if controllerTestProcess(t) {
		return
	}
	for _, scope := range []string{"screen", "baseline-only", "matched"} {
		t.Run(scope, func(t *testing.T) {
			f := newControllerFixture(t, scope, 2)
			before, _ := os.ReadFile(f.record.StorageDestination)
			r, err := Load(t.Context(), f.repo, f.root, f.record.PlanDigest)
			if err != nil || !reflect.DeepEqual(r, f.record) {
				t.Fatalf("saved compatibility: %v", err)
			}
			e := f.prepare(t)
			var seen []Unit
			packets := map[string]bool{}
			p, err := run(t.Context(), f.root, e.ID, scriptedWorker{func(ctx context.Context, request workerRequest) (workerResult, error) {
				assertPublicPacket(t, request)
				if packets[request.Layout.Workspace] {
					t.Fatal("packet reused")
				}
				packets[request.Layout.Workspace] = true
				seen = append(seen, request.Unit)
				return workerResult{Outcome: "native-completed"}, nil
			}})
			if err != nil || p.State != "completed" || !slices.Equal(seen, f.record.Preview.Schedule) {
				t.Fatalf("schedule execution: %v %v", p, err)
			}
			for _, slot := range p.Slots {
				if slot.Grade != "unknown" || slot.AttemptsConsumed != 1 || slot.RepairsConsumed != 0 {
					t.Fatal("invented grade/budget")
				}
				a := readAttempt(t, f, e, slot.Ordinal, 1)
				if a.Native != nil || a.Cleanup.NativeAcknowledged != nil || a.Cleanup.Status != "removed-clean" {
					t.Fatalf("invented native evidence / failed clean cleanup: %+v", a.Cleanup)
				}
				if _, err := os.Lstat(a.Packet); !os.IsNotExist(err) {
					t.Fatalf("clean packet not removed: %v", err)
				}
			}
			status, err := Status(f.root, e.ID)
			if err != nil || status.State != "completed" || len(status.Inspection) != 0 {
				t.Fatalf("journal status: %+v %v", status, err)
			}
			if _, err := run(t.Context(), f.root, e.ID, scriptedWorker{}); err == nil {
				t.Fatal("completed evaluation replayed")
			}
			after, _ := os.ReadFile(f.record.StorageDestination)
			if !bytes.Equal(before, after) {
				t.Fatal("saved preview rewritten")
			}
			for _, name := range []string{"state.json", "delivery.lock"} {
				data, _ := os.ReadFile(filepath.Join(f.repo, ".orchestrator", name))
				if !strings.Contains(string(data), "sentinel") {
					t.Fatal("Delivery lifecycle changed")
				}
			}
		})
	}
}

func TestControllerSeparateRetryRepairAndInitialEvidence(t *testing.T) {
	if controllerTestProcess(t) {
		return
	}
	f := newControllerFixture(t, "screen", 1)
	e := f.prepare(t)
	first := e.Preparation.Preview.Schedule[0].Ordinal
	count := 0
	p, err := run(t.Context(), f.root, e.ID, scriptedWorker{func(ctx context.Context, request workerRequest) (workerResult, error) {
		assertPublicPacket(t, request)
		if request.Unit.Ordinal == first {
			count++
			if _, err := os.Stat(filepath.Join(request.Layout.Workspace, "answer.txt")); !os.IsNotExist(err) {
				t.Fatal("earlier attempt context supplied")
			}
			writeFixture(t, filepath.Join(request.Layout.Workspace, "answer.txt"), []byte(fmt.Sprintf("attempt %d", count)))
			if count == 1 {
				return workerResult{Outcome: "infrastructure-failure"}, nil
			}
			if count == 2 {
				return workerResult{Outcome: "task-failure"}, nil
			}
		}
		return workerResult{Outcome: "native-completed"}, nil
	}})
	if err != nil || p.State != "completed" || p.Slots[0].AttemptsConsumed != 2 || p.Slots[0].RepairsConsumed != 1 {
		t.Fatalf("separate budgets: %+v %v", p, err)
	}
	for i, outcome := range []string{"infrastructure-failure", "task-failure", "native-completed"} {
		a := readAttempt(t, f, e, first, i+1)
		if a.Outcome != outcome || a.Cleanup.Status != "preserved-dirty" || a.Grade != "unknown" {
			t.Fatalf("earlier attempt lost: %+v", a)
		}
		if data, err := os.ReadFile(filepath.Join(a.Packet, "answer.txt")); err != nil || string(data) != fmt.Sprintf("attempt %d", i+1) {
			t.Fatal("dirty attempt replaced")
		}
	}
	if _, err := Status(f.root, e.ID); err != nil {
		t.Fatal(err)
	}
}

func TestControllerConcurrentStartStopAndReaders(t *testing.T) {
	if controllerTestProcess(t) {
		return
	}
	f := newControllerFixture(t, "screen", 1)
	e := f.prepare(t)
	writeFixture(t, filepath.Join(f.workers, "preexisting.txt"), []byte("keep existing worker file"))
	started := make(chan struct{})
	done := make(chan struct {
		p   *Progress
		err error
	}, 1)
	go func() {
		p, err := run(t.Context(), f.root, e.ID, scriptedWorker{func(ctx context.Context, request workerRequest) (workerResult, error) {
			assertPublicPacket(t, request)
			close(started)
			<-ctx.Done()
			return workerResult{Outcome: "interrupted"}, ctx.Err()
		}})
		done <- struct {
			p   *Progress
			err error
		}{p, err}
	}()
	select {
	case <-started:
	case <-time.After(15 * time.Second):
		t.Fatal("controller did not start")
	}
	if _, err := run(t.Context(), f.root, e.ID, scriptedWorker{}); err == nil {
		t.Fatal("concurrent controller acquired evaluation")
	}
	var wg sync.WaitGroup
	failures := make(chan error, 24)
	for range 12 {
		wg.Go(func() { failures <- Stop(f.root, e.ID) })
	}
	for range 12 {
		wg.Go(func() { _, err := Status(f.root, e.ID); failures <- err })
	}
	wg.Wait()
	close(failures)
	for err := range failures {
		if err != nil {
			t.Fatal(err)
		}
	}
	select {
	case result := <-done:
		if result.err != nil || result.p.State != "stopped" || result.p.Slots[0].AttemptsConsumed != 1 || result.p.Slots[1].Status != "unrun" {
			t.Fatalf("stop outcome: %+v %v", result.p, result.err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("stop exceeded bound")
	}
	if err := Stop(f.root, e.ID); err != nil {
		t.Fatal(err)
	}
	if data, err := os.ReadFile(filepath.Join(f.workers, "preexisting.txt")); err != nil || string(data) != "keep existing worker file" {
		t.Fatal("preexisting worker file changed")
	}
	status, err := Status(f.root, e.ID)
	if err != nil || status.State != "stopped" {
		t.Fatalf("retained stop: %v", err)
	}
}

func TestControllerCutoffCancellationAndUnacknowledgedCleanup(t *testing.T) {
	if controllerTestProcess(t) {
		return
	}
	for _, mode := range []string{"cutoff", "cancellation", "unacknowledged"} {
		t.Run(mode, func(t *testing.T) {
			f := newControllerFixture(t, "screen", 1)
			zero := int64(0)
			f.proposal.Limits = Limits{OverallSeconds: 3, AttemptSeconds: 1, VerificationSeconds: 1, CleanupSeconds: 1, MaxAttemptsPerUnit: 10, MaxRepairsPerUnit: &zero}
			if mode != "cutoff" {
				f.proposal.Limits.OverallSeconds = 30
			}
			f.preview(t)
			e := f.prepare(t)
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			release := make(chan struct{})
			defer close(release)
			p, err := run(ctx, f.root, e.ID, scriptedWorker{func(attempt context.Context, request workerRequest) (workerResult, error) {
				if mode == "cancellation" {
					cancel()
				}
				if mode == "unacknowledged" {
					<-release
					return workerResult{Outcome: "native-completed"}, nil
				}
				<-attempt.Done()
				return workerResult{Outcome: "interrupted"}, attempt.Err()
			}})
			if p == nil || p.Slots[0].AttemptsConsumed < 1 || p.Slots[1].Status != "unrun" {
				t.Fatalf("budget/coverage: %+v %v", p, err)
			}
			switch mode {
			case "cutoff":
				if err != nil || p.State != "overall-cutoff" {
					t.Fatalf("cutoff: %+v %v", p, err)
				}
			case "cancellation":
				if err != nil || p.State != "stopped" {
					t.Fatalf("cancel: %+v %v", p, err)
				}
			case "unacknowledged":
				if err == nil || p.State != "incomplete" {
					t.Fatalf("unknown cleanup: %+v %v", p, err)
				}
				a := readAttempt(t, f, e, 1, 1)
				if a.Cleanup.Status != "unknown" || a.Cleanup.NativeAcknowledged != nil {
					t.Fatal("fabricated cleanup")
				}
				if _, err := os.Stat(a.Packet); err != nil {
					t.Fatal("unacknowledged worker packet removed")
				}
			}
			if _, err := Status(f.root, e.ID); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestControllerRefusalProtocolDisconnectAndBounds(t *testing.T) {
	if controllerTestProcess(t) {
		return
	}
	for _, outcome := range []string{"refused", "disconnected", "unsupported", "oversized"} {
		t.Run(outcome, func(t *testing.T) {
			f := newControllerFixture(t, "screen", 1)
			e := f.prepare(t)
			p, err := run(t.Context(), f.root, e.ID, scriptedWorker{func(ctx context.Context, request workerRequest) (workerResult, error) {
				if outcome == "oversized" {
					writeFixture(t, filepath.Join(request.Layout.Workspace, "huge.bin"), bytes.Repeat([]byte("x"), MaxArtifactBytes+1))
					return workerResult{Outcome: "native-completed"}, nil
				}
				return workerResult{Outcome: outcome}, nil
			}})
			if p == nil || p.Slots[1].Status != "unrun" {
				t.Fatal("unsafe progression")
			}
			if outcome == "refused" && (err != nil || p.State != "refused") {
				t.Fatalf("refusal: %v", err)
			}
			if outcome == "disconnected" && (err != nil || p.State != "incomplete") {
				t.Fatalf("disconnect: %v", err)
			}
			if outcome == "unsupported" || outcome == "oversized" {
				if err == nil || p.State != "safety-failure" {
					t.Fatalf("invalidity: %v", err)
				}
			}
			if _, err := Status(f.root, e.ID); err != nil {
				t.Fatal(err)
			}
			if outcome == "oversized" {
				a := readAttempt(t, f, e, 1, 1)
				if info, err := os.Stat(filepath.Join(a.Packet, "huge.bin")); err != nil || info.Size() != MaxArtifactBytes+1 {
					t.Fatal("oversized evidence silently removed")
				}
			}
		})
	}
}

func TestSavedInputTamperingFailsClosed(t *testing.T) {
	if controllerTestProcess(t) {
		return
	}
	for _, target := range []string{"schedule", "schema", "missing", "profile", "configuration", "source", "hard-link", "case-bound"} {
		t.Run(target, func(t *testing.T) {
			f := newControllerFixture(t, "screen", 1)
			switch target {
			case "schedule":
				f.record.Preview.Schedule[0].Side = "candidate"
				writeFixture(t, f.record.StorageDestination, fixtureJSON(t, f.record))
			case "schema":
				data := fixtureJSON(t, f.record)
				data = bytes.Replace(data, []byte(`"schema_version": 1`), []byte(`"schema_version": 99`), 1)
				writeFixture(t, f.record.StorageDestination, data)
			case "missing":
				data := fixtureJSON(t, f.record)
				data = bytes.Replace(data, []byte("    \"execution_available\": false,\n"), nil, 1)
				writeFixture(t, f.record.StorageDestination, data)
			case "profile":
				writeFixture(t, filepath.Join(f.inputs, "profile.toml"), []byte("changed profile sentinel"))
			case "configuration":
				data, _ := os.ReadFile(filepath.Join(f.repo, ".orchestrator/config.toml"))
				data = append(data, []byte("\n# no semantic change; render stays compatible\n")...)
				writeFixture(t, filepath.Join(f.repo, ".orchestrator/config.toml"), []byte(strings.Replace(string(data), `config_revision = "r1"`, `config_revision = "changed-revision"`, 1)))
			case "source":
				writeFixture(t, filepath.Join(f.inputs, "private/key.txt"), []byte("changed source sentinel"))
			case "hard-link":
				if err := os.Link(filepath.Join(f.inputs, "private/key.txt"), filepath.Join(f.inputs, "private/key-alias.txt")); err != nil {
					t.Fatal(err)
				}
			case "case-bound":
				for len(f.record.Plan.Cases) < 13 {
					f.record.Plan.Cases = append(f.record.Plan.Cases, f.record.Plan.Cases[0])
				}
				data, _ := json.Marshal(f.record.Plan)
				hex := evalcorpus.Digest(data)
				f.record.PlanDigest = "sha256:" + hex
				f.record.StorageDestination = filepath.Join(f.root, hex+".json")
				writeFixture(t, f.record.StorageDestination, fixtureJSON(t, f.record))
			}
			before, _ := os.ReadFile(f.record.StorageDestination)
			if target == "source" || target == "hard-link" {
				if _, err := Prepare(t.Context(), f.repo, f.root, f.record.PlanDigest); err == nil {
					t.Fatal("changed/linked source accepted")
				}
			} else if _, err := Load(t.Context(), f.repo, f.root, f.record.PlanDigest); err == nil {
				t.Fatal("tampered saved inputs accepted")
			}
			after, _ := os.ReadFile(f.record.StorageDestination)
			if !bytes.Equal(before, after) {
				t.Fatal("tampered saved file overwritten")
			}
		})
	}
}

func TestControllerInterruptedAndConflictingPublication(t *testing.T) {
	if controllerTestProcess(t) {
		return
	}
	f := newControllerFixture(t, "screen", 1)
	e := f.prepare(t)
	path := filepath.Join(f.root, e.ID)
	writeFixture(t, filepath.Join(path, ".pending-crash"), []byte(`{"partial":`))
	p, err := Status(f.root, e.ID)
	if err != nil || len(p.Inspection) == 0 || p.State != "prepared" {
		t.Fatalf("interrupted write: %+v %v", p, err)
	}
	if _, err := run(t.Context(), f.root, e.ID, scriptedWorker{}); err == nil {
		t.Fatal("incomplete preparation started")
	}
	if data, _ := os.ReadFile(filepath.Join(path, ".pending-crash")); string(data) != `{"partial":` {
		t.Fatal("partial evidence deleted")
	}
	e2 := f.prepare(t)
	if err := os.Mkdir(filepath.Join(f.root, e2.ID, "execution"), 0o700); err != nil {
		t.Fatal(err)
	}
	status, err := Status(f.root, e2.ID)
	if err != nil || len(status.Inspection) == 0 {
		t.Fatalf("crash claim outcome fabricated: %v", err)
	}
	if _, err := run(t.Context(), f.root, e2.ID, scriptedWorker{}); err == nil {
		t.Fatal("automatic crash takeover")
	}
	e3 := f.prepare(t)
	conflict := filepath.Join(f.root, e3.ID, progressName(1))
	writeFixture(t, conflict, []byte("existing conflict sentinel"))
	if _, err := run(t.Context(), f.root, e3.ID, scriptedWorker{}); err == nil {
		t.Fatal("corrupt journal replaced")
	}
	if data, _ := os.ReadFile(conflict); string(data) != "existing conflict sentinel" {
		t.Fatal("conflicting journal removed")
	}
}

func TestProductionHasNoScriptedExecutionSeam(t *testing.T) {
	if controllerTestProcess(t) {
		return
	}
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if !strings.HasSuffix(entry.Name(), ".go") || strings.HasSuffix(entry.Name(), "_test.go") {
			continue
		}
		data, err := os.ReadFile(entry.Name())
		if err != nil {
			t.Fatal(err)
		}
		if bytes.Contains(data, []byte("scriptedWorker")) {
			t.Fatal("scripted worker compiled into production")
		}
		file, err := parser.ParseFile(token.NewFileSet(), entry.Name(), data, 0)
		if err != nil {
			t.Fatal(err)
		}
		for _, declaration := range file.Decls {
			fn, ok := declaration.(*ast.FuncDecl)
			if !ok || !fn.Name.IsExported() || fn.Type.Params == nil {
				continue
			}
			for _, parameter := range fn.Type.Params.List {
				if _, ok := parameter.Type.(*ast.FuncType); ok {
					t.Fatalf("exported callback route: %s", fn.Name.Name)
				}
				if id, ok := parameter.Type.(*ast.Ident); ok && id.Name == "worker" {
					t.Fatalf("exported worker route: %s", fn.Name.Name)
				}
			}
		}
	}
	f := newControllerFixture(t, "screen", 1)
	credentials, err := credentialLocations()
	if err != nil {
		t.Fatal(err)
	}
	for _, role := range []string{"scout", "implementation", "review"} {
		result, _ := (nativeWorker{}).execute(t.Context(), workerRequest{Role: role, Layout: codexnative.IsolationPaths{Workspace: f.workers, Scratch: f.scratch, MainCheckout: f.repo, ControllerState: f.root, SiblingWorkspaces: []string{f.inputs}, CredentialPaths: credentials}})
		if result.Outcome != "refused" || result.Native != nil {
			t.Fatalf("production %s execution bypass: %+v", role, result)
		}
	}
	e := f.prepare(t)
	p, err := Run(t.Context(), f.root, e.ID, "") // missing version refuses before spawning a host; no model/native validation
	if err != nil || p.State != "refused" || p.Slots[1].Status != "unrun" {
		t.Fatalf("production core refusal: %+v %v", p, err)
	}
	t.Log("No-model core refusal is not verified native model/OS isolation; RunSession/Resume's separate gates remain unchanged.")
}

func TestControllerObservationMissingnessAndRetainedTampering(t *testing.T) {
	if controllerTestProcess(t) {
		return
	}
	f := newControllerFixture(t, "screen", 1)
	e := f.prepare(t)
	input := int64(10)
	observation := metrics.Observation{SchemaVersion: metrics.ObservationVersion, RunID: "run-scripted-fixture", ID: "sample-1", At: now(), Source: "scripted-native-fixture", Host: "codex", Session: "scripted-thread", Sample: &metrics.CounterSample{Stream: "reported-thread-total", Mode: "cumulative", Sequence: 1, Counters: metrics.Counters{InputTokens: &input}}}
	output := "available scripted output"
	p, err := run(t.Context(), f.root, e.ID, scriptedWorker{func(ctx context.Context, r workerRequest) (workerResult, error) {
		return workerResult{Outcome: "native-completed", Output: &output, Native: &NativeEvidence{ThreadID: "scripted-thread", SessionID: "scripted-session", TurnID: "scripted-turn", Observations: []metrics.Observation{observation}}}, nil
	}})
	if err != nil || p.State != "completed" {
		t.Fatalf("retained observation: %v", err)
	}
	a := readAttempt(t, f, e, 1, 1)
	if a.ExecutionSource != "no-model-test-script" || a.Grade != "unknown" || a.Native.Observed != nil || a.Native.Requested != nil || a.Native.Observations[0].Sample.Counters.TotalTokens != nil || a.Cleanup.NativeAcknowledged != nil {
		t.Fatal("missing evidence synthesized")
	}
	if _, err := Status(f.root, e.ID); err != nil {
		t.Fatal(err)
	}
	key := filepath.Join(f.root, e.ID, attemptName(1, 1), "private/key/key.txt")
	writeFixture(t, key, []byte("tampered retained key sentinel"))
	if _, err := Status(f.root, e.ID); err == nil {
		t.Fatal("retained artifact tampering accepted")
	}
	if data, _ := os.ReadFile(key); string(data) != "tampered retained key sentinel" {
		t.Fatal("tampered evidence overwritten")
	}
}

func TestGuardedControllerBoundsAliasesAndIdentity(t *testing.T) {
	if controllerTestProcess(t) {
		return
	}
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(base, "owned")
	if err := os.Mkdir(path, 0o700); err != nil {
		t.Fatal(err)
	}
	g, err := openGuarded(path)
	if err != nil {
		t.Fatal(err)
	}
	defer g.close()
	g.budget = &writeBudget{used: maxControllerBytes - 1, entries: 0}
	if err := g.writeFile("too-large", []byte("xx")); err == nil {
		t.Fatal("byte ceiling bypass")
	}
	g.budget = &writeBudget{entries: maxControllerEntries}
	if _, err := g.createDir("too-many"); err == nil {
		t.Fatal("entry ceiling bypass")
	}
	g.budget = nil
	if err := g.writeFile(strings.Repeat("level/", 33)+"deep.txt", nil); err == nil {
		t.Fatal("directory depth ceiling bypass")
	}
	for _, name := range []string{"../escape", "/absolute", `nested\alias`, "a:stream", "NUL.txt", "nested/../escape"} {
		if err := g.writeFile(name, nil); err == nil {
			t.Errorf("relative name accepted: %s", name)
		}
	}
	writeFixture(t, filepath.Join(path, "independent.txt"), []byte("preserve"))
	if err := os.Link(filepath.Join(path, "independent.txt"), filepath.Join(path, "hard-link.txt")); err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := inventory(g); err == nil {
		t.Fatal("unknown controller hard link accepted")
	}
	moved := filepath.Join(base, "moved")
	if err := os.Rename(path, moved); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(path, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := g.writeFile("after-replacement", []byte("forbidden")); err == nil {
		t.Fatal("replaced directory identity accepted")
	}
	if _, err := os.Stat(filepath.Join(moved, "after-replacement")); !os.IsNotExist(err) {
		t.Fatal("renamed root received writes")
	}
	if _, err := os.Stat(filepath.Join(path, "after-replacement")); !os.IsNotExist(err) {
		t.Fatal("replacement root received writes")
	}
}

func TestControllerDelayedCancellationSharesCleanupAllowance(t *testing.T) {
	if controllerTestProcess(t) {
		return
	}
	f := newControllerFixture(t, "screen", 1)
	e := f.prepare(t)
	sources, err := loadSources(t.Context(), f.repo, e.Preparation.Plan)
	if err != nil {
		t.Fatal(err)
	}
	g, _, _, err := openEvaluation(f.root, e.ID)
	if err != nil {
		t.Fatal(err)
	}
	defer g.close()
	unit := e.Preparation.Preview.Schedule[0]
	slot := Slot{Unit: unit, Attempts: []AttemptRef{{Number: 1, Kind: "initial"}}}
	a, _, err := prepareAttempt(t.Context(), g, e, slot, "initial", sources[unit.CaseID])
	if err != nil {
		t.Fatal(err)
	}
	defer a.close()
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	started := make(chan struct{})
	go func() { <-started; cancel() }()
	result, returned, interrupted, deadline := executeAttempt(ctx, g, e, a, sources[unit.CaseID], unit, scriptedWorker{func(ctx context.Context, r workerRequest) (workerResult, error) {
		close(started)
		<-ctx.Done()
		timer := time.NewTimer(250 * time.Millisecond)
		defer timer.Stop()
		<-timer.C
		return workerResult{Outcome: "interrupted"}, ctx.Err()
	}})
	if !returned || !interrupted || result.Outcome != "interrupted" || deadline.IsZero() {
		t.Fatalf("delayed return not observed: %+v %v %v", result, returned, interrupted)
	}
	remaining := time.Until(deadline)
	if remaining > time.Duration(e.Preparation.Plan.Limits.CleanupSeconds)*time.Second-200*time.Millisecond {
		t.Fatal("cleanup allowance reset after delayed interruption")
	}
	cleanup, cancelCleanup := context.WithDeadline(t.Context(), deadline)
	defer cancelCleanup()
	observation := cleanupAttempt(cleanup, a, sources[unit.CaseID], true, true)
	if observation.Status != "removed-clean" || observation.NativeAcknowledged != nil {
		t.Fatalf("bounded local cleanup: %+v", observation)
	}
}
