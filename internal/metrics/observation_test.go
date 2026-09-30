package metrics

import (
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func ptr(v int64) *int64 { return &v }

func sample(id string, seq, total int64) Observation {
	return Observation{
		SchemaVersion: ObservationVersion, RunID: "run-test", ID: id,
		At: "2026-09-30T12:00:00Z", Source: "native", Host: "codex", Session: "s1",
		Sample: &CounterSample{Stream: "tokens", Mode: "cumulative", Sequence: seq, Counters: Counters{TotalTokens: ptr(total)}},
	}
}

func TestObservationReplayAndBaselines(t *testing.T) {
	root := t.TempDir()
	for _, o := range []Observation{sample("a", 1, 100), sample("b", 2, 150), sample("b", 2, 150), sample("c", 3, 150)} {
		if _, err := Record(root, o); err != nil {
			t.Fatal(err)
		}
	}
	newSession := sample("d", 1, 20)
	newSession.Session = "s2"
	newSession.Sample.Counters.InputTokens = ptr(0)
	newSession.Requested = &Profile{Model: "requested", Effort: "high"}
	newSession.Observed = &Profile{Model: "actual"}
	if _, err := Record(root, newSession); err != nil {
		t.Fatal(err)
	}
	docs, err := LoadAll(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(docs[0].Observations) != 4 {
		t.Fatalf("observations = %+v", docs[0].Observations)
	}
	deltas, err := CounterContributions(docs[0].Observations)
	if err != nil {
		t.Fatal(err)
	}
	for i, want := range []int64{100, 50, 0, 20} {
		if *deltas[i].TotalTokens != want {
			t.Errorf("delta %d = %v", i, deltas[i])
		}
	}
	got := docs[0].Observations[3]
	if got.Sample.Counters.InputTokens == nil || *got.Sample.Counters.InputTokens != 0 || got.Sample.Counters.OutputTokens != nil || got.Observed.Effort != "" || !reflect.DeepEqual(got, newSession) {
		t.Fatalf("presence/profile lost: %+v", got)
	}
}

func TestObservationRejectionsPreserveHistory(t *testing.T) {
	root := t.TempDir()
	if _, err := Record(root, sample("a", 1, 100)); err != nil {
		t.Fatal(err)
	}
	if _, err := Record(root, sample("b", 2, 150)); err != nil {
		t.Fatal(err)
	}
	path := docPath(root, "run-test")
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	cases := map[string]Observation{
		"identity":   sample("b", 2, 151),
		"sequence":   sample("c", 1, 200),
		"regression": sample("c", 3, 149),
		"negative":   sample("c", 3, -1),
	}
	mode := sample("c", 3, 200)
	mode.Sample.Mode = "delta"
	cases["mode"] = mode
	oldTime := sample("c", 3, 200)
	oldTime.At = "2026-09-30T11:59:59Z"
	cases["time"] = oldTime
	for name, o := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := Record(root, o); err == nil {
				t.Fatal("accepted invalid sample")
			}
			after, err := os.ReadFile(path)
			if err != nil || string(after) != string(before) {
				t.Fatalf("history changed: %v", err)
			}
		})
	}
	if _, err := Record(root, sample("c", 3, 200)); err != nil {
		t.Fatalf("rejections advanced baseline: %v", err)
	}
}

func TestObservationOverflowAndIntervals(t *testing.T) {
	o := sample("a", 1, math.MaxInt64)
	o.Sample.Mode = "delta"
	next := sample("b", 2, 1)
	next.Sample.Mode = "delta"
	if _, err := CounterContributions([]Observation{o, next}); err == nil || !strings.Contains(err.Error(), "overflow") {
		t.Fatalf("overflow = %v", err)
	}
	for _, kind := range []string{"active-agent", "verification", "ci-waiting", "human-waiting"} {
		i := sample("i", 1, 0)
		i.Sample = nil
		i.Interval = &Interval{Kind: kind, Start: "2026-09-30T11:00:00Z", End: i.At}
		if err := i.Validate(); err != nil {
			t.Fatal(err)
		}
		i.Interval.Start = "2026-09-30T12:00:01Z"
		if err := i.Validate(); err == nil {
			t.Fatal("accepted reversed interval")
		}
		i.Interval.Start = "1000-01-01T00:00:00Z"
		if err := i.Validate(); err == nil {
			t.Fatal("accepted overflowing duration")
		}
	}
	for _, kind := range []string{"implementation-failure", "infrastructure-failure", "evidence-correction", "wrong-requirement", "escalation", "approval"} {
		o := sample("o", 1, 0)
		o.Sample = nil
		o.Outcome = kind
		if err := o.Validate(); err != nil {
			t.Fatal(err)
		}
	}
	for _, input := range []string{
		`{"schema_version":999}`, `{"schema_version":1,"extra":true}`,
		`{} {}`, `{} garbage`,
		`{"schema_version":1,"sample":{"counters":{"total_tokens":9223372036854775808}}}`,
	} {
		if _, err := ParseObservation([]byte(input)); err == nil {
			t.Errorf("accepted %s", input)
		}
	}
}

func TestLegacyPresenceAndUpgrade(t *testing.T) {
	root := t.TempDir()
	legacy := `{"schema_version":1,"run_id":"run-test","events":[{"at":"old","verb":"pr-open","usage":{"input_tokens":0,"total_tokens":42}}]}`
	writeDoc(t, root, "run-test", legacy)
	docs, err := LoadAll(root)
	if err != nil {
		t.Fatal(err)
	}
	c := docs[0].Events[0].Usage.Counters()
	if c.InputTokens == nil || *c.InputTokens != 0 || c.OutputTokens != nil || *c.TotalTokens != 42 {
		t.Fatalf("legacy presence: %+v", c)
	}
	data, _ := os.ReadFile(docPath(root, "run-test"))
	if string(data) != legacy {
		t.Fatal("read rewrote schema 1")
	}
	if _, err := Record(root, sample("a", 1, 100)); err != nil {
		t.Fatal(err)
	}
	docs, err = LoadAll(root)
	if err != nil {
		t.Fatal(err)
	}
	c = docs[0].Events[0].Usage.Counters()
	if docs[0].SchemaVersion != 2 || c.InputTokens == nil || c.OutputTokens != nil {
		t.Fatal("upgrade changed legacy presence")
	}
	deltas, err := CounterContributions(docs[0].Observations)
	if err != nil || *deltas[0].TotalTokens != 100 {
		t.Fatalf("legacy was used as baseline: %v %v", deltas, err)
	}
	if err := Append(root, "run-test", Event{At: "later", Verb: "review"}); err != nil {
		t.Fatal(err)
	}
	docs, err = LoadAll(root)
	if err != nil || len(docs[0].Observations) != 1 || len(docs[0].Events) != 2 {
		t.Fatalf("append lost observations: %v", err)
	}
	var u Usage
	if err := json.Unmarshal([]byte(`{"mystery":1}`), &u); err == nil {
		t.Fatal("legacy decoder lost strictness")
	}
}

func TestAtomicWriteFailurePreservesAcceptedDocument(t *testing.T) {
	root := t.TempDir()
	if _, err := Record(root, sample("a", 1, 100)); err != nil {
		t.Fatal(err)
	}
	path := docPath(root, "run-test")
	before, _ := os.ReadFile(path)
	// A missing staging directory deterministically fails the real writer's
	// CreateTemp before replacing an accepted document, even when run as root.
	if err := write(path, filepath.Join(root, "missing"), Document{}); err == nil {
		t.Fatal("write succeeded")
	}
	after, _ := os.ReadFile(path)
	if string(before) != string(after) {
		t.Fatal("failed write changed history")
	}
	// Exercise rename failure and temporary-file cleanup separately.
	if err := write(root, dirPath(root), Document{}); err == nil {
		t.Fatal("rename over directory succeeded")
	}
	files, err := filepath.Glob(filepath.Join(dirPath(root), "*.tmp"))
	if err != nil || len(files) != 0 {
		t.Fatalf("temporary files remain: %v %v", files, err)
	}
	if _, err := Record(root, sample("b", 2, 150)); err != nil {
		t.Fatal(err)
	}
}
