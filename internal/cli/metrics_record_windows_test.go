package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"syscall"
	"testing"

	"github.com/kninetimmy/orch/internal/metrics"
)

// Windows sharing flags allow reading accepted history but deterministically
// deny its replacement, exercising the final rename failure through the CLI.
func TestMetricsRecordFailedReplacementPreservesBaseline(t *testing.T) {
	env, o := metricFixture(t, true)
	total := int64(100)
	o.Outcome = ""
	o.Sample = &metrics.CounterSample{Stream: "tokens", Mode: "cumulative", Sequence: 1, Counters: metrics.Counters{TotalTokens: &total}}
	if code, _, stderr := invokeMetric(env, o); code != ExitOK {
		t.Fatal(stderr)
	}
	path := filepath.Join(env.RepoRoot, filepath.FromSlash(metrics.Dir), o.RunID+".json")
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	name, err := syscall.UTF16PtrFromString(path)
	if err != nil {
		t.Fatal(err)
	}
	handle, err := syscall.CreateFile(name, syscall.GENERIC_READ, syscall.FILE_SHARE_READ|syscall.FILE_SHARE_WRITE, nil, syscall.OPEN_EXISTING, syscall.FILE_ATTRIBUTE_NORMAL, 0)
	if err != nil {
		t.Fatal(err)
	}
	total, o.ID, o.Sample.Sequence = 150, "two", 2
	code, _, stderr := invokeMetric(env, o)
	if err := syscall.CloseHandle(handle); err != nil {
		t.Fatal(err)
	}
	if code != ExitError {
		t.Fatalf("replacement succeeded: %s", stderr)
	}
	after, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(before, after) {
		t.Fatalf("failed replacement changed history: %v", err)
	}
	for range 2 {
		if code, _, stderr := invokeMetric(env, o); code != ExitOK {
			t.Fatal(stderr)
		}
	}
	docs, err := metrics.LoadAll(env.RepoRoot)
	if err != nil {
		t.Fatal(err)
	}
	deltas, err := metrics.CounterContributions(docs[0].Observations)
	if err != nil || len(deltas) != 2 || *deltas[1].TotalTokens != 50 {
		t.Fatalf("failed write advanced baseline: %v %+v", err, deltas)
	}
	files, err := filepath.Glob(filepath.Join(filepath.Dir(path), "*.tmp"))
	if err != nil || len(files) != 0 {
		t.Fatalf("temporary files remain: %v %v", files, err)
	}
}
