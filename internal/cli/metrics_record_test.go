package cli

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kninetimmy/orch/internal/lockfile"
	"github.com/kninetimmy/orch/internal/metrics"
	"github.com/kninetimmy/orch/internal/state"
)

func metricFixture(t *testing.T, enabled bool) (Env, metrics.Observation) {
	t.Helper()
	env, _, _ := testEnv(t)
	writeConfig(t, env.RepoRoot, validTOML+fmt.Sprintf("\n[metrics]\nenabled = %t\n", enabled))
	st, err := state.EnterDelivery(env.RepoRoot, "claude", testPlanRef(), testIssues())
	if err != nil {
		t.Fatal(err)
	}
	st.Run.Issues[0].Number = 281
	if err := state.Save(env.RepoRoot, st); err != nil {
		t.Fatal(err)
	}
	return env, metrics.Observation{
		SchemaVersion: 1, RunID: st.Run.ID, ID: "one", At: "2026-09-30T12:00:00Z",
		Source: "native", IssueNumber: 281, Role: "specialist", Attempt: "attempt-1",
		Host: "claude", Session: "session-1", Outcome: "approval",
	}
}

func invokeMetric(env Env, o metrics.Observation) (int, string, string) {
	data, err := json.Marshal(o)
	if err != nil {
		panic(err)
	}
	var out, stderr bytes.Buffer
	env.Stdin, env.Stdout, env.Stderr = bytes.NewReader(data), &out, &stderr
	code := Run([]string{"metrics", "record"}, env)
	return code, out.String(), stderr.String()
}

// Each helper invocation is a fresh process with no metrics baseline in memory.
func TestMetricsRecordProcess(t *testing.T) {
	root := os.Getenv("ORCH_TEST_METRICS_ROOT")
	if root == "" {
		return
	}
	os.Exit(Run([]string{"metrics", "record"}, Env{RepoRoot: root, Stdin: os.Stdin, Stdout: os.Stdout, Stderr: os.Stderr}))
}

func metricProcess(t *testing.T, root string, o metrics.Observation) *exec.Cmd {
	t.Helper()
	data, err := json.Marshal(o)
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(os.Args[0], "-test.run=^TestMetricsRecordProcess$")
	cmd.Env = append(os.Environ(), "ORCH_TEST_METRICS_ROOT="+root)
	cmd.Stdin = bytes.NewReader(data)
	return cmd
}

func TestMetricsRecordRestartAndConcurrentProcesses(t *testing.T) {
	env, o := metricFixture(t, true)
	statePath := filepath.Join(env.RepoRoot, filepath.FromSlash(state.Path))
	before, err := os.ReadFile(statePath)
	if err != nil {
		t.Fatal(err)
	}
	o.Outcome = ""
	zero, total := int64(0), int64(100)
	o.Sample = &metrics.CounterSample{Stream: "tokens", Mode: "cumulative", Sequence: 1, Counters: metrics.Counters{InputTokens: &zero, TotalTokens: &total}}
	for i, value := range []int64{100, 150, 150} {
		total = value
		if i > 0 {
			o.ID = "two"
			o.Sample.Sequence = 2
		}
		out, err := metricProcess(t, env.RepoRoot, o).CombinedOutput()
		if err != nil {
			t.Fatalf("process: %v: %s", err, out)
		}
		if i == 2 && !strings.Contains(string(out), `"recorded":false`) {
			t.Fatalf("replay: %s", out)
		}
	}
	// Independent sessions may arrive in any order; both must survive. Hold the
	// actual repository lock until both subprocesses have started.
	lock, err := lockfile.AcquireMutation(env.RepoRoot)
	if err != nil {
		t.Fatal(err)
	}
	var cmds []*exec.Cmd
	var outputs []*bytes.Buffer
	for i := range 2 {
		o.ID, o.Session = fmt.Sprintf("parallel-%d", i), fmt.Sprintf("session-%d", i+2)
		o.Sample.Sequence = 1
		cmd := metricProcess(t, env.RepoRoot, o)
		out := new(bytes.Buffer)
		cmd.Stdout, cmd.Stderr = out, out
		if err := cmd.Start(); err != nil {
			_ = lock.Release()
			t.Fatal(err)
		}
		cmds, outputs = append(cmds, cmd), append(outputs, out)
	}
	if err := lock.Release(); err != nil {
		t.Fatal(err)
	}
	for i, cmd := range cmds {
		if err := cmd.Wait(); err != nil {
			t.Fatalf("parallel: %v: %s", err, outputs[i])
		}
	}
	docs, err := metrics.LoadAll(env.RepoRoot)
	if err != nil {
		t.Fatal(err)
	}
	if len(docs) != 1 || len(docs[0].Observations) != 4 {
		t.Fatalf("history: %+v", docs)
	}
	deltas, err := metrics.CounterContributions(docs[0].Observations)
	if err != nil {
		t.Fatal(err)
	}
	if *deltas[0].TotalTokens != 100 || *deltas[1].TotalTokens != 50 || deltas[0].InputTokens == nil || *deltas[0].InputTokens != 0 || deltas[0].OutputTokens != nil {
		t.Fatalf("contributions: %+v", deltas)
	}
	after, err := os.ReadFile(statePath)
	if err != nil || !bytes.Equal(before, after) {
		t.Fatalf("recording changed lifecycle state: %v", err)
	}
}

func TestMetricsRecordValidatesWithoutChangingHistory(t *testing.T) {
	env, o := metricFixture(t, true)
	if code, _, stderr := invokeMetric(env, o); code != ExitOK {
		t.Fatal(stderr)
	}
	path := filepath.Join(env.RepoRoot, filepath.FromSlash(metrics.Dir), o.RunID+".json")
	before, _ := os.ReadFile(path)
	for name, mutate := range map[string]func(*metrics.Observation){
		"run":      func(o *metrics.Observation) { o.RunID = "run-other" },
		"path":     func(o *metrics.Observation) { o.RunID = "../outside" },
		"issue":    func(o *metrics.Observation) { o.IssueNumber = 282 },
		"host":     func(o *metrics.Observation) { o.Host = "codex" },
		"id":       func(o *metrics.Observation) { o.ID = "bad\nidentifier" },
		"role":     func(o *metrics.Observation) { o.Role = "unknown" },
		"version":  func(o *metrics.Observation) { o.SchemaVersion = 999 },
		"conflict": func(o *metrics.Observation) { o.Outcome = "wrong-requirement" },
		"interval": func(o *metrics.Observation) {
			o.Outcome = ""
			o.Interval = &metrics.Interval{Kind: "verification", Start: o.At, End: "2026-09-30T11:00:00Z"}
		},
	} {
		t.Run(name, func(t *testing.T) {
			bad := o
			mutate(&bad)
			if code, _, stderr := invokeMetric(env, bad); code != ExitError || stderr == "" {
				t.Fatalf("invalid accepted: %d %s", code, stderr)
			}
			after, _ := os.ReadFile(path)
			if !bytes.Equal(before, after) {
				t.Fatal("invalid observation changed history")
			}
		})
	}
	interval := o
	interval.ID = "interval"
	interval.Outcome = ""
	interval.Interval = &metrics.Interval{Kind: "verification", Start: "2026-09-30T11:59:00Z", End: o.At}
	if code, _, stderr := invokeMetric(env, interval); code != ExitOK {
		t.Fatal(stderr)
	}
	if code, out, stderr := invokeMetric(env, interval); code != ExitOK || !strings.Contains(out, `"recorded":false`) {
		t.Fatalf("interval replay: %s %s", out, stderr)
	}
	interval.Interval.Start = "2026-09-30T11:58:00Z"
	if code, _, _ := invokeMetric(env, interval); code != ExitError {
		t.Fatal("conflicting interval accepted")
	}
}

type failingMetricOutput struct{}

func (failingMetricOutput) Write([]byte) (int, error) { return 0, errors.New("output unavailable") }

func TestMetricsRecordFailuresAndDisabled(t *testing.T) {
	env, o := metricFixture(t, false)
	if code, out, stderr := invokeMetric(env, o); code != ExitOK || !strings.Contains(out, `"enabled":false`) {
		t.Fatalf("disabled: %s %s", out, stderr)
	}
	dir := filepath.Join(env.RepoRoot, filepath.FromSlash(metrics.Dir))
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatalf("disabled metrics created storage: %v", err)
	}
	env, o = metricFixture(t, true)
	dir = filepath.Join(env.RepoRoot, filepath.FromSlash(metrics.Dir))
	// Force a real storage-path failure; retry must still be the first record.
	if err := os.WriteFile(dir, []byte("blocked directory"), 0600); err != nil {
		t.Fatal(err)
	}
	if code, _, _ := invokeMetric(env, o); code != ExitError {
		t.Fatal("storage failure accepted")
	}
	if err := os.Remove(dir); err != nil {
		t.Fatal(err)
	}
	data, _ := json.Marshal(o)
	env.Stdin, env.Stdout = bytes.NewReader(data), failingMetricOutput{}
	if code := Run([]string{"metrics", "record"}, env); code != ExitError {
		t.Fatal("output failure accepted")
	}
	if code, out, stderr := invokeMetric(env, o); code != ExitOK || !strings.Contains(out, `"recorded":false`) {
		t.Fatalf("retry: %s %s", out, stderr)
	}
	docs, err := metrics.LoadAll(env.RepoRoot)
	if err != nil || len(docs) != 1 || len(docs[0].Observations) != 1 {
		t.Fatalf("accepted history lost: %v %+v", err, docs)
	}
}

func TestMetricsRecordAssociationLoadsInsideMutationLock(t *testing.T) {
	env, o := metricFixture(t, true)
	lock, err := lockfile.AcquireMutation(env.RepoRoot)
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan int, 1)
	go func() { code, _, _ := invokeMetric(env, o); done <- code }()
	select {
	case code := <-done:
		_ = lock.Release()
		t.Fatalf("record bypassed mutation lock: %d", code)
	case <-time.After(100 * time.Millisecond):
	}
	// Simulate completion by another command holding that same boundary.
	if err := state.CompleteDelivery(env.RepoRoot); err != nil {
		_ = lock.Release()
		t.Fatal(err)
	}
	if err := lock.Release(); err != nil {
		t.Fatal(err)
	}
	if code := <-done; code != ExitError {
		t.Fatalf("stale association accepted: %d", code)
	}
	if _, err := os.Stat(filepath.Join(env.RepoRoot, filepath.FromSlash(metrics.Dir))); !os.IsNotExist(err) {
		t.Fatalf("stale run created metrics: %v", err)
	}
}

func TestMetricsReportReadsLegacyWithoutRewrite(t *testing.T) {
	env, o := metricFixture(t, true)
	dir := filepath.Join(env.RepoRoot, filepath.FromSlash(metrics.Dir))
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, o.RunID+".json")
	legacy := fmt.Sprintf(`{"schema_version":1,"run_id":%q,"events":[{"at":"old","verb":"pr-open","usage":{"total_tokens":5}}]}`, o.RunID)
	if err := os.WriteFile(path, []byte(legacy), 0600); err != nil {
		t.Fatal(err)
	}
	if code := Run([]string{"metrics"}, env); code != ExitOK {
		t.Fatalf("legacy report = %d", code)
	}
	after, err := os.ReadFile(path)
	if err != nil || string(after) != legacy {
		t.Fatalf("report rewrote legacy: %v", err)
	}
	if code, _, stderr := invokeMetric(env, o); code != ExitOK {
		t.Fatal(stderr)
	}
	docs, err := metrics.LoadAll(env.RepoRoot)
	if err != nil || docs[0].Events[0].Usage.Counters().InputTokens != nil {
		t.Fatalf("legacy absence lost: %v", err)
	}
}
