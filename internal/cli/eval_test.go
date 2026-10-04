package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/kninetimmy/orch/internal/evalcorpus"
	"github.com/kninetimmy/orch/internal/evalplan"
	"github.com/kninetimmy/orch/internal/execx"
)

type evalFixture struct {
	repo     string
	file     string
	plan     evalplan.Proposal
	manifest evalcorpus.Manifest
	commands []execx.Cmd
}

type evalRunner struct {
	fixture *evalFixture
	t       *testing.T
}

func (r evalRunner) Run(ctx context.Context, cmd execx.Cmd) (execx.Result, error) {
	r.t.Helper()
	if cmd.Name != "git" || len(cmd.Args) < 5 || !slices.Equal(cmd.Args[:4], []string{"--no-optional-locks", "--no-pager", "-c", "core.fsmonitor=false"}) {
		r.t.Fatalf("preview invoked unexpected subprocess: %+v", cmd)
	}
	verb := cmd.Args[4]
	if !slices.Contains([]string{"cat-file", "rev-parse", "worktree"}, verb) || !slices.Contains(cmd.Env, "GIT_NO_LAZY_FETCH=1") || !slices.Contains(cmd.Env, "GIT_NO_REPLACE_OBJECTS=1") {
		r.t.Fatalf("preview invoked nonlocal/mutating subprocess: %+v", cmd)
	}
	r.fixture.commands = append(r.fixture.commands, cmd)
	return (execx.Local{}).Run(ctx, cmd)
}

func evalWrite(t *testing.T, name string, data []byte) {
	t.Helper()
	if err := os.WriteFile(name, data, 0o600); err != nil {
		t.Fatal(err)
	}
}

func evalJSON(t *testing.T, value any) []byte {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func newEvalFixture(t *testing.T) *evalFixture {
	t.Helper()
	parent, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	f := &evalFixture{repo: filepath.Join(parent, "repo"), file: filepath.Join(parent, "artifacts", "plan.json")}
	for _, name := range []string{f.repo, filepath.Join(f.repo, ".orchestrator"), filepath.Dir(f.file), filepath.Join(parent, "storage"), filepath.Join(parent, "workers"), filepath.Join(parent, "scratch")} {
		if err := os.Mkdir(name, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	git := func(stdin string, args ...string) string {
		t.Helper()
		cmd := exec.CommandContext(t.Context(), "git", args...)
		cmd.Dir, cmd.Stdin = f.repo, strings.NewReader(stdin)
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("synthetic Git fixture: %v: %s", err, out)
		}
		return strings.TrimSpace(string(out))
	}
	git("", "init", "--quiet")
	tree := git("", "hash-object", "-w", "-t", "tree", "--stdin")
	oid := git("tree "+tree+"\nauthor Synthetic <fixture@example.invalid> 1 +0000\ncommitter Synthetic <fixture@example.invalid> 1 +0000\n\npreview fixture\n", "hash-object", "-w", "-t", "commit", "--stdin")
	git("", "update-ref", "HEAD", oid)
	evalWrite(t, filepath.Join(f.repo, ".orchestrator", "config.toml"), []byte(validTOML))
	// Deliberately invalid lifecycle documents prove preview does not read them.
	evalWrite(t, filepath.Join(f.repo, ".orchestrator", "state.json"), []byte("unchanged Delivery sentinel"))
	evalWrite(t, filepath.Join(f.repo, ".orchestrator", "delivery.lock"), []byte("unchanged lock sentinel"))
	artifact := func(name, data string) evalplan.Artifact {
		evalWrite(t, filepath.Join(filepath.Dir(f.file), name), []byte(data))
		return evalplan.Artifact{Path: name, SHA256: evalcorpus.Digest([]byte(data))}
	}
	digest := evalcorpus.Digest([]byte("synthetic public input"))
	f.manifest = evalcorpus.Manifest{Version: 1, Author: "PRIVATE_AUTHOR_SENTINEL", PreparedDate: "2026-10-03", Prerequisites: []string{"PRIVATE_PREREQUISITE_SENTINEL"}}
	for partitionIndex, partition := range []string{"development", "held-out"} {
		for roleIndex, role := range []string{"scout", "implementation", "review"} {
			for variant := range 2 {
				id := fmt.Sprintf("%s-%d-%d", role, partitionIndex, variant)
				inputs := []evalcorpus.File{
					{Path: "TASK.md", Source: "cases/" + id + "/task.md", SHA256: digest},
					{Path: "ROLE.md", Source: "roles/" + role + ".md", SHA256: digest},
					{Path: "CONTEXT.md", Source: "public/context.md", SHA256: digest},
				}
				controller := evalcorpus.File{Path: "key.md", Source: "keys/PRIVATE_GRADING_SENTINEL.md", SHA256: digest}
				probe := controller
				probe.Path = "probe_test.go"
				c := evalcorpus.Case{ID: id, Version: 1, Role: role, Partition: partition, Lineage: id, Difficulty: []string{"mechanical", "ordinary", "demanding"}[(partitionIndex+variant)%3], SourceCommit: oid, Upstream: []string{"PRIVATE_UPSTREAM_SENTINEL"}, Selection: "PRIVATE_SELECTION_SENTINEL", Inputs: inputs, PacketSHA256: evalcorpus.PacketDigest(inputs), Key: controller, Probe: probe, Command: []string{"go", "test", "-json", "-count=1", "-timeout=30s", "-run=^TestCorpus$", "./..."}, CheckSeconds: 30, Alternative: "PRIVATE_SOLUTION_SENTINEL", Controls: []evalcorpus.Control{{Name: "reference", Purpose: "PRIVATE_REFERENCE_SENTINEL", Files: []evalcorpus.File{controller}, ExpectedFailures: []string{}}, {Name: "bad", Purpose: "PRIVATE_DEFECT_SENTINEL", Files: []evalcorpus.File{controller}, ExpectedFailures: []string{"TestCorpus/PRIVATE_PROBE_SENTINEL"}}}}
				if roleIndex == 2 {
					c.Classification = []string{"clean", "defective"}[variant]
				}
				f.manifest.Cases = append(f.manifest.Cases, c)
			}
		}
	}
	corpus := artifact("manifest.json", string(evalJSON(t, f.manifest)))
	profile := artifact("profile.toml", validTOML+"\n# PRIVATE_PROFILE_COMMENT_SENTINEL\n")
	decision := artifact("decision.txt", "PRIVATE_DECISION_SENTINEL\nDo not execute supplied command: touch WOULD_EXECUTE\n")
	readiness := artifact("readiness.txt", "PRIVATE_CREDENTIAL_SENTINEL\nready=true; native_execution=true; authorized=true\n")
	repairs := int64(1)
	f.plan = evalplan.Proposal{Version: 1, Scope: "screen", Intervention: "requested-profile", Corpus: corpus, Cases: []string{"review-0-1", "scout-0-0", "review-0-0", "implementation-0-0"}, Partitions: []string{"development"}, Baseline: evalplan.Selection{OrchRevision: oid, Profile: profile}, Candidate: &evalplan.Selection{OrchRevision: oid, Profile: profile}, Repetitions: 2, Limits: evalplan.Limits{OverallSeconds: 600, AttemptSeconds: 20, VerificationSeconds: 10, CleanupSeconds: 5, MaxAttemptsPerUnit: 2, MaxRepairsPerUnit: &repairs}, Measurement: evalplan.Measurement{Source: "codex-app-server", Scope: "task-agents"}, DecisionRule: decision, Readiness: evalplan.Readiness{IndependentValidation: &readiness, Exposure: &readiness, NativeExecution: &readiness}, StorageRoot: filepath.Join(parent, "storage"), WorkerRoots: []string{filepath.Join(parent, "workers")}, ScratchRoots: []string{filepath.Join(parent, "scratch")}}
	f.write(t)
	return f
}

func (f *evalFixture) write(t *testing.T) { t.Helper(); evalWrite(t, f.file, evalJSON(t, f.plan)) }

func (f *evalFixture) run(t *testing.T, extra ...string) (int, string, string) {
	t.Helper()
	var stdout, stderr bytes.Buffer
	args := append([]string{"eval", "preview", "--plan", f.file}, extra...)
	code := Run(args, Env{RepoRoot: f.repo, Stdout: &stdout, Stderr: &stderr, Runner: evalRunner{f, t}})
	return code, stdout.String(), stderr.String()
}

func TestEvalPreviewSavedRecordAndSchedule(t *testing.T) {
	f := newEvalFixture(t)
	code, output, failure := f.run(t, "--json")
	if code != ExitOK {
		t.Fatalf("preview failed: %d %s", code, failure)
	}
	var r evalplan.Record
	if err := json.Unmarshal([]byte(output), &r); err != nil {
		t.Fatal(err)
	}
	if r.Kind != "maintainer-preparation-record" || r.Preview.ExecutionAvailable || r.Preview.ApprovalGranted || r.Preview.WorkerAccessProtection != "unverified" || len(r.Preview.ReadinessBlockers) == 0 {
		t.Fatalf("readiness became authority: %+v", r.Preview)
	}
	wantCounts := evalplan.Counts{BaselineUnits: 8, CandidateUnits: 8, MatchedPairs: 8, Units: 16, MaximumRetries: 16, MaximumRepairs: 16, MaximumAttemptsIncludingRepairs: 48, MaximumScheduledSeconds: 1680}
	if r.Preview.Counts != wantCounts {
		t.Fatalf("counts = %+v, want %+v", r.Preview.Counts, wantCounts)
	}
	if len(r.Preview.Schedule) != 16 || len(r.Plan.ExcludedCases) != 8 {
		t.Fatal("incorrect schedule/exclusions")
	}
	for pair := range 8 {
		first, second := r.Preview.Schedule[2*pair], r.Preview.Schedule[2*pair+1]
		wantSide := "baseline"
		if pair%2 == 1 {
			wantSide = "candidate"
		}
		if first.Side != wantSide || second.Side == wantSide || first.CaseID != r.Plan.Cases[pair/2].ID || first.CaseID != second.CaseID || first.Repetition != int64(pair%2+1) || first.Repetition != second.Repetition || first.Ordinal != pair*2+1 {
			t.Fatalf("bad pair %d: %+v %+v", pair, first, second)
		}
	}
	for _, check := range r.Preview.Checks {
		if slices.Contains([]string{"corpus-source-bytes", "independent-validation", "exposure", "decision-rule-semantics"}, check.Name) && check.Status != "not-performed" {
			t.Fatalf("invented check: %+v", check)
		}
	}
	stored, err := os.ReadFile(r.StorageDestination)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(stored, []byte(output)) {
		t.Fatal("JSON output disagrees with retained record")
	}
	if strings.Contains(string(stored), "PRIVATE_") || strings.Contains(string(stored), "WOULD_EXECUTE") || strings.Contains(string(stored), "ready=true") {
		t.Fatal("private artifact contents leaked into record")
	}
	code, text, failure := f.run(t)
	if code != ExitOK {
		t.Fatalf("text preview: %d %s", code, failure)
	}
	for _, value := range []any{r.Plan, r.Preview} {
		data, err := json.MarshalIndent(value, "", "  ")
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(text, string(data)) {
			t.Fatal("text/JSON facts disagree")
		}
	}
	slices.Reverse(f.plan.Cases)
	f.write(t)
	code, replay, failure := f.run(t, "--json")
	if code != ExitOK || replay != output {
		t.Fatalf("normalized replay differs: %d %s", code, failure)
	}
	for name, want := range map[string]string{"state.json": "unchanged Delivery sentinel", "delivery.lock": "unchanged lock sentinel"} {
		data, err := os.ReadFile(filepath.Join(f.repo, ".orchestrator", name))
		if err != nil || string(data) != want {
			t.Fatalf("preview touched lifecycle state: %s %v", name, err)
		}
	}
	if _, err := os.Stat(filepath.Join(f.repo, ".orchestrator", "metrics")); !os.IsNotExist(err) {
		t.Fatalf("disabled metrics storage created: %v", err)
	}
	files, err := os.ReadDir(f.plan.StorageRoot)
	if err != nil || len(files) != 1 {
		t.Fatalf("unexpected stored files: %v %v", files, err)
	}
}

func TestEvalPreviewScopes(t *testing.T) {
	for _, scope := range []string{"baseline-only", "matched", "screen"} {
		t.Run(scope, func(t *testing.T) {
			f := newEvalFixture(t)
			f.plan.Scope, f.plan.Repetitions = scope, 3
			if scope != "screen" {
				f.plan.Cases, f.plan.Partitions = []string{}, []string{"held-out", "development"}
				for _, c := range f.manifest.Cases {
					f.plan.Cases = append(f.plan.Cases, c.ID)
				}
			}
			if scope != "matched" {
				f.plan.Candidate, f.plan.Intervention = nil, "none"
			}
			f.write(t)
			code, output, failure := f.run(t, "--json")
			if code != ExitOK {
				t.Fatalf("%d %s", code, failure)
			}
			var r evalplan.Record
			if err := json.Unmarshal([]byte(output), &r); err != nil {
				t.Fatal(err)
			}
			units := int64(36)
			if scope == "matched" {
				units = 72
			}
			if scope == "screen" {
				units = 12
			}
			if r.Preview.Counts.Units != units {
				t.Fatalf("units %d want %d", r.Preview.Counts.Units, units)
			}
		})
	}
}

func TestEvalPreviewRejectsInputsAndLimits(t *testing.T) {
	for _, test := range []struct {
		name   string
		mutate func(*evalFixture)
		want   string
	}{
		{"version", func(f *evalFixture) { f.plan.Version = 2 }, "version"},
		{"duplicate-case", func(f *evalFixture) { f.plan.Cases[0] = f.plan.Cases[1] }, "duplicate case"},
		{"unknown-case", func(f *evalFixture) { f.plan.Cases[0] = "unknown" }, "unknown case"},
		{"missing-case", func(f *evalFixture) { f.plan.Cases = f.plan.Cases[:3] }, "exactly four"},
		{"screen-coverage", func(f *evalFixture) { f.plan.Cases[0] = "scout-0-1" }, "screen requires"},
		{"partition", func(f *evalFixture) { f.plan.Partitions = []string{"held-out"} }, "outside declared"},
		{"extra-partition", func(f *evalFixture) { f.plan.Partitions = []string{"development", "held-out"} }, "exactly match"},
		{"baseline-candidate", func(f *evalFixture) { f.plan.Scope = "baseline-only" }, "forbids a candidate"},
		{"revision", func(f *evalFixture) { f.plan.Baseline.OrchRevision = "main" }, "full 40"},
		{"missing-revision", func(f *evalFixture) { f.plan.Baseline.OrchRevision = strings.Repeat("f", 40) }, "unavailable locally"},
		{"missing-profile", func(f *evalFixture) { f.plan.Baseline.Profile.Path = "missing.toml" }, "read local artifact"},
		{"tampered-profile", func(f *evalFixture) { f.plan.Baseline.Profile.SHA256 = strings.Repeat("f", 64) }, "digest mismatch"},
		{"tampered-decision", func(f *evalFixture) { f.plan.DecisionRule.SHA256 = strings.Repeat("f", 64) }, "digest mismatch"},
		{"tampered-readiness", func(f *evalFixture) { f.plan.Readiness.Exposure.SHA256 = strings.Repeat("f", 64) }, "digest mismatch"},
		{"missing-overall", func(f *evalFixture) { f.plan.Limits.OverallSeconds = 0 }, "overall_seconds"},
		{"negative-attempt", func(f *evalFixture) { f.plan.Limits.AttemptSeconds = -1 }, "attempt_seconds"},
		{"missing-verification", func(f *evalFixture) { f.plan.Limits.VerificationSeconds = 0 }, "verification_seconds"},
		{"missing-cleanup", func(f *evalFixture) { f.plan.Limits.CleanupSeconds = 0 }, "cleanup_seconds"},
		{"missing-repairs", func(f *evalFixture) { f.plan.Limits.MaxRepairsPerUnit = nil }, "null"},
		{"zero-attempts", func(f *evalFixture) { f.plan.Limits.MaxAttemptsPerUnit = 0 }, "max_attempts"},
		{"overflow-attempts", func(f *evalFixture) { f.plan.Limits.MaxAttemptsPerUnit = math.MaxInt64 }, "overflow"},
		{"overflow-seconds", func(f *evalFixture) { f.plan.Limits.OverallSeconds = math.MaxInt64 }, "finite seconds"},
		{"short-overall", func(f *evalFixture) { f.plan.Limits.OverallSeconds = 30 }, "cover one attempt"},
		{"zero-repetitions", func(f *evalFixture) { f.plan.Repetitions = 0 }, "repetitions"},
		{"excess-repetitions", func(f *evalFixture) { f.plan.Repetitions = 1001 }, "repetitions"},
		{"measurement", func(f *evalFixture) { f.plan.Measurement.Scope = "unspecified" }, "measurement"},
		{"traversal", func(f *evalFixture) { f.plan.Corpus.Path = "../manifest.json" }, "traversing"},
		{"repo-storage", func(f *evalFixture) { f.plan.StorageRoot = f.repo }, "overlaps"},
		{"git-storage", func(f *evalFixture) { f.plan.StorageRoot = filepath.Join(f.repo, ".git") }, "overlaps"},
		{"worker-storage", func(f *evalFixture) { f.plan.StorageRoot = f.plan.WorkerRoots[0] }, "overlaps"},
		{"scratch-storage", func(f *evalFixture) { f.plan.StorageRoot = f.plan.ScratchRoots[0] }, "overlaps"},
		{"storage-parent", func(f *evalFixture) { f.plan.StorageRoot = filepath.Dir(f.repo) }, "overlaps"},
		{"relative-storage", func(f *evalFixture) { f.plan.StorageRoot = "storage" }, "absolute"},
		{"missing-storage", func(f *evalFixture) { f.plan.StorageRoot = filepath.Join(filepath.Dir(f.repo), "missing") }, "verify directory"},
		{"undeclared-worker", func(f *evalFixture) { f.plan.WorkerRoots = []string{} }, "at least one"},
		{"undeclared-scratch", func(f *evalFixture) { f.plan.ScratchRoots = nil }, "null"},
	} {
		t.Run(test.name, func(t *testing.T) {
			f := newEvalFixture(t)
			test.mutate(f)
			f.write(t)
			code, output, failure := f.run(t, "--json")
			if code != ExitError || output != "" || !strings.Contains(failure, test.want) {
				t.Fatalf("code=%d output=%s failure=%s, want %s", code, output, failure, test.want)
			}
		})
	}
	for name, mutate := range map[string]func([]byte) []byte{
		"unknown-field":        func(b []byte) []byte { return append([]byte(`{"readiness_claim":true,`), b[1:]...) },
		"duplicate-field":      func(b []byte) []byte { return append([]byte(`{"version":1,`), b[1:]...) },
		"case-alias":           func(b []byte) []byte { return bytes.Replace(b, []byte(`"version"`), []byte(`"Version"`), 1) },
		"trailing":             func(b []byte) []byte { return append(b, []byte(" {}")...) },
		"oversized":            func(b []byte) []byte { return append(b, bytes.Repeat([]byte(" "), evalplan.MaxPlanBytes)...) },
		"missing-repair-field": func(b []byte) []byte { return bytes.Replace(b, []byte(`,"max_repairs_per_unit":1`), nil, 1) },
	} {
		t.Run(name, func(t *testing.T) {
			f := newEvalFixture(t)
			evalWrite(t, f.file, mutate(evalJSON(t, f.plan)))
			if code, output, failure := f.run(t, "--json"); code != ExitError || output != "" || failure == "" {
				t.Fatalf("accepted malformed plan: %d %s %s", code, output, failure)
			}
		})
	}
}

func TestEvalPreviewEffectiveConfigurationAndTamperedCorpus(t *testing.T) {
	f := newEvalFixture(t)
	evalWrite(t, filepath.Join(f.repo, ".orchestrator", "config.local.toml"), []byte("[metrics]\nenabled = true\n"))
	code, output, failure := f.run(t, "--json")
	if code != ExitOK {
		t.Fatalf("%d %s", code, failure)
	}
	var record evalplan.Record
	if err := json.Unmarshal([]byte(output), &record); err != nil {
		t.Fatal(err)
	}
	if record.Plan.EffectiveConfiguration.SHA256 == record.Plan.Baseline.Configuration.SHA256 || !reflect.DeepEqual(record.Plan.EffectiveConfiguration.LocalOverrides, []string{"metrics.enabled"}) {
		t.Fatal("effective overlay identity lost")
	}
	f.manifest.Cases[0].Partition = "held-out"
	data := evalJSON(t, f.manifest)
	evalWrite(t, filepath.Join(filepath.Dir(f.file), "manifest.json"), data)
	f.plan.Corpus.SHA256 = evalcorpus.Digest(data)
	f.write(t)
	if code, _, failure := f.run(t); code != ExitError || !strings.Contains(failure, "corpus manifest") {
		t.Fatalf("invalid corpus accepted: %d %s", code, failure)
	}
}

func TestEvalUnsupportedVerbsAndUsage(t *testing.T) {
	env, _, stderr := testEnv(t)
	for _, args := range [][]string{{"eval"}, {"eval", "run"}, {"eval", "stop"}, {"eval", "status"}, {"eval", "report"}, {"eval", "preview"}, {"eval", "preview", "--plan"}, {"eval", "preview", "--plan", "a", "--plan", "b"}, {"eval", "preview", "--plan", "a", "--json", "--json"}, {"eval", "preview", "--execute"}} {
		stderr.Reset()
		if code := Run(args, env); code != ExitUsage {
			t.Fatalf("%v: exit %d", args, code)
		}
	}
	stdout := new(bytes.Buffer)
	env.Stdout = stdout
	if code := Run([]string{"help"}, env); code != ExitOK || !strings.Contains(stdout.String(), "eval preview --plan FILE") {
		t.Fatal("help does not expose preview")
	}
}

func TestEvalPreviewCLIProcess(t *testing.T) {
	f := newEvalFixture(t)
	binary := filepath.Join(filepath.Dir(f.repo), "orch-preview")
	if runtime.GOOS == "windows" {
		binary += ".exe"
	}
	build := exec.CommandContext(t.Context(), "go", "build", "-o", binary, "../../cmd/orch")
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build CLI smoke: %v: %s", err, output)
	}
	run := func(args ...string) (int, string) {
		t.Helper()
		cmd := exec.CommandContext(t.Context(), binary, args...)
		cmd.Dir = f.repo
		output, err := cmd.CombinedOutput()
		if err == nil {
			return 0, string(output)
		}
		if exit, ok := err.(*exec.ExitError); ok {
			return exit.ExitCode(), string(output)
		}
		t.Fatalf("CLI smoke spawn: %v", err)
		return -1, ""
	}
	type result struct {
		code   int
		output string
	}
	results := make(chan result, 4)
	var wg sync.WaitGroup
	for range 4 {
		wg.Go(func() {
			code, output := run("eval", "preview", "--plan", f.file, "--json")
			results <- result{code, output}
		})
	}
	wg.Wait()
	close(results)
	output := ""
	for result := range results {
		if result.code != 0 {
			t.Fatalf("concurrent CLI preview smoke: %d %s", result.code, result.output)
		}
		if output != "" && result.output != output {
			t.Fatal("concurrent CLI records disagree")
		}
		output = result.output
	}
	var r evalplan.Record
	if err := json.Unmarshal([]byte(output), &r); err != nil {
		t.Fatal(err)
	}
	if r.Preview.Counts.Units != 16 || r.Preview.Counts.MaximumAttemptsIncludingRepairs != 48 || r.Preview.ExecutionAvailable || r.Preview.ApprovalGranted {
		t.Fatal("wrong external CLI preview")
	}
	data, err := os.ReadFile(r.StorageDestination)
	if err != nil || string(data) != output {
		t.Fatalf("CLI output/storage mismatch: %v", err)
	}
	if code, output := run("eval", "run", "--plan", r.PlanDigest); code != ExitUsage || !strings.Contains(output, "explicit arguments") {
		t.Fatalf("CLI execution refusal: %d %s", code, output)
	}
	evalWrite(t, r.StorageDestination, []byte("corrupt-smoke-sentinel"))
	if code, output := run("eval", "preview", "--plan", f.file, "--json"); code != ExitError || !strings.Contains(output, "conflicts or is corrupt") {
		t.Fatalf("CLI corrupt replay refusal: %d %s", code, output)
	}
	if data, err := os.ReadFile(r.StorageDestination); err != nil || string(data) != "corrupt-smoke-sentinel" {
		t.Fatal("CLI replaced corrupt record")
	}
	t.Logf("CLI process smoke: four concurrent previews exit 0, %s, units=16, maximum_attempts_including_repairs=48; eval run exit 2; corrupt replay exit 1, preserved", r.PlanDigest)
}

func TestEvalPreviewLinkedInputsAndWorktreeExclusions(t *testing.T) {
	t.Run("hard-linked-profile", func(t *testing.T) {
		f := newEvalFixture(t)
		source := filepath.Join(filepath.Dir(f.file), f.plan.Baseline.Profile.Path)
		alias := filepath.Join(filepath.Dir(f.file), "aliased-profile.toml")
		if err := os.Link(source, alias); err != nil {
			t.Fatal(err)
		}
		f.plan.Baseline.Profile.Path = alias
		f.write(t)
		if code, _, failure := f.run(t); code != ExitError || !strings.Contains(failure, "linked file") {
			t.Fatalf("aliased input accepted: %d %s", code, failure)
		}
	})
	t.Run("registered-checkout", func(t *testing.T) {
		f := newEvalFixture(t)
		other := filepath.Join(filepath.Dir(f.repo), "other-checkout")
		cmd := exec.CommandContext(t.Context(), "git", "worktree", "add", "--detach", "--no-checkout", other, f.plan.Baseline.OrchRevision)
		cmd.Dir = f.repo
		if data, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("worktree fixture: %v %s", err, data)
		}
		f.plan.StorageRoot = other
		f.write(t)
		if code, _, failure := f.run(t); code != ExitError || !strings.Contains(failure, "overlaps") {
			t.Fatalf("other checkout storage accepted: %d %s", code, failure)
		}
	})
	t.Run("external-shared-git", func(t *testing.T) {
		f := newEvalFixture(t)
		shared := filepath.Join(filepath.Dir(f.repo), "shared-git")
		if err := os.Rename(filepath.Join(f.repo, ".git"), shared); err != nil {
			t.Fatal(err)
		}
		evalWrite(t, filepath.Join(f.repo, ".git"), []byte("gitdir: "+filepath.ToSlash(shared)+"\n"))
		cmd := exec.CommandContext(t.Context(), "git", "config", "core.worktree", f.repo)
		cmd.Dir = f.repo
		if data, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("separate Git fixture: %v %s", err, data)
		}
		f.plan.StorageRoot = shared
		f.write(t)
		if code, _, failure := f.run(t); code != ExitError || !strings.Contains(failure, "overlaps") {
			t.Fatalf("shared Git storage accepted: %d %s", code, failure)
		}
	})
	if runtime.GOOS == "windows" {
		return
	} // Junction coverage below requires no symlink privilege.
	t.Run("directory-symlink", func(t *testing.T) {
		f := newEvalFixture(t)
		alias := filepath.Join(filepath.Dir(f.repo), "storage-link")
		if err := os.Symlink(f.plan.StorageRoot, alias); err != nil {
			t.Fatal(err)
		}
		f.plan.StorageRoot = alias
		f.write(t)
		if code, _, failure := f.run(t); code != ExitError || !strings.Contains(failure, "linked/reparse") {
			t.Fatalf("linked storage accepted: %d %s", code, failure)
		}
	})
	t.Run("file-symlink", func(t *testing.T) {
		f := newEvalFixture(t)
		alias := filepath.Join(filepath.Dir(f.file), "profile-link.toml")
		if err := os.Symlink(filepath.Join(filepath.Dir(f.file), f.plan.Baseline.Profile.Path), alias); err != nil {
			t.Fatal(err)
		}
		f.plan.Baseline.Profile.Path = alias
		f.write(t)
		if code, _, failure := f.run(t); code != ExitError || !strings.Contains(failure, "linked/reparse") {
			t.Fatalf("linked profile accepted: %d %s", code, failure)
		}
	})
}
