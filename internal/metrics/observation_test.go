package metrics

import (
	"encoding/json"
	"errors"
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

func TestEvaluationObservationIsolationAndCounters(t *testing.T) {
	identity := EvaluationIdentity{ID: "eval-aaaaaaaaaaaaaaaaaaaaaaaaaa", PlanDigest: "sha256:" + strings.Repeat("a", 64), Unit: 1,
		CaseID: "public-case", CaseVersion: 2, CaseSHA256: strings.Repeat("b", 64), PacketSHA256: strings.Repeat("c", 64), Repetition: 1,
		Side: "baseline", Attempt: 1, Kind: "initial", Role: "implementer"}
	zero, ten := int64(0), int64(10)
	a := Observation{SchemaVersion: EvaluationObservationVersion, Evaluation: &identity, ID: "first", At: "2026-10-05T12:00:00Z",
		Source: "codex-app-server", Host: "codex", Role: "implementer", Session: "thread",
		Sample: &CounterSample{Stream: "thread-total", Mode: "cumulative", Sequence: 1, Counters: Counters{InputTokens: &zero}}}
	b := a
	b.ID = "second"
	b.Sample = &CounterSample{Stream: "thread-total", Mode: "cumulative", Sequence: 2, Counters: Counters{InputTokens: &ten, OutputTokens: &zero}}
	other := identity
	other.Attempt, other.Kind = 2, "retry"
	c := b
	c.ID, c.Evaluation = "first", &other
	c.Sample = &CounterSample{Stream: "thread-total", Mode: "cumulative", Sequence: 1, Counters: Counters{InputTokens: &ten}}
	d := c
	d.ID, d.Source = "other-source", "session-log"
	deltas, err := CounterContributions([]Observation{a, b, c, d})
	if err != nil || *deltas[0].InputTokens != 0 || deltas[0].OutputTokens != nil || *deltas[1].InputTokens != 10 || *deltas[1].OutputTokens != 0 || deltas[1].TotalTokens != nil || *deltas[2].InputTokens != 10 || *deltas[3].InputTokens != 10 {
		t.Fatalf("evaluation presence/scope/source arithmetic: %+v %v", deltas, err)
	}
	root := t.TempDir()
	if _, err := Record(root, a); err == nil {
		t.Fatal("evaluation entered Delivery history")
	}
	if _, err := os.Stat(filepath.Join(root, ".orchestrator", "metrics")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("evaluation created Delivery storage")
	}
	if _, err := CounterContributions([]Observation{a, a}); err == nil {
		t.Fatal("duplicate evaluation sample accepted")
	}
	changed := identity
	changed.CaseVersion++
	b.Evaluation = &changed
	if _, err := CounterContributions([]Observation{a, b}); err == nil {
		t.Fatal("attempt identity drift accepted")
	}
	for _, change := range []func(*Observation){
		func(o *Observation) { o.RunID = "run-fabricated" },
		func(o *Observation) { o.IssueNumber = 323 },
		func(o *Observation) { o.Attempt = "implementation-1" },
		func(o *Observation) { o.SchemaVersion = 2 },
		func(o *Observation) { o.Evaluation = nil },
		func(o *Observation) { o.Role = "reviewer" },
	} {
		bad := a
		change(&bad)
		if err := bad.Validate(); err == nil {
			t.Fatal("mixed/unbound evaluation observation accepted")
		}
	}
	data, err := json.Marshal(a)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ParseObservation(data); err != nil {
		t.Fatal(err)
	}
	data = []byte(strings.Replace(string(data), `"schema_version":3`, `"schema_version":2`, 1))
	if _, err := ParseObservation(data); err == nil {
		t.Fatal("new identity silently reinterpreted as schema 2")
	}
}

func TestObservationVersionedMissingnessAndReasoning(t *testing.T) {
	o := sample("missing", 1, 0)
	o.Sample, o.Session = nil, ""
	o.Unavailable = &Unavailable{Reason: "matching-child-unavailable"}
	root := t.TempDir()
	if recorded, err := Record(root, o); err != nil || !recorded {
		t.Fatalf("missingness: %t %v", recorded, err)
	}
	if recorded, err := Record(root, o); err != nil || recorded {
		t.Fatalf("missingness replay: %t %v", recorded, err)
	}
	deltas, err := CounterContributions([]Observation{o})
	if err != nil || deltas[0] != (Counters{}) {
		t.Fatalf("missingness became a counter: %+v %v", deltas, err)
	}
	for _, mutate := range []func(*Observation){
		func(o *Observation) { o.SchemaVersion = 1 },
		func(o *Observation) { o.Sample = sample("s", 1, 0).Sample },
		func(o *Observation) { o.Outcome = "infrastructure-failure" },
		func(o *Observation) { o.Unavailable = &Unavailable{} },
	} {
		bad := o
		mutate(&bad)
		if err := bad.Validate(); err == nil {
			t.Fatalf("accepted invalid missingness: %+v", bad)
		}
	}
	legacy := sample("old", 1, 0)
	legacy.SchemaVersion = 1
	data, err := json.Marshal(legacy)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ParseObservation(data); err != nil {
		t.Fatalf("legacy shape rejected: %v", err)
	}
	for _, bad := range []string{
		strings.Replace(string(data), `"schema_version":1`, `"schema_version":1,"unavailable":null`, 1),
		strings.Replace(string(data), `"total_tokens":0`, `"total_tokens":0,"reasoning_output_tokens":null`, 1),
		strings.Replace(string(data), `"schema_version":1`, `"schema_version":2,"unknown":null`, 1),
	} {
		if _, err := ParseObservation([]byte(bad)); err == nil {
			t.Fatalf("accepted extended/unknown legacy shape: %s", bad)
		}
	}
	var usage Usage
	if json.Unmarshal([]byte(`{"reasoning_output_tokens":null}`), &usage) == nil {
		t.Fatal("legacy usage silently accepted native reasoning counter")
	}
	a, b := sample("a", 1, 0), sample("b", 2, 0)
	a.Sample.Counters.ReasoningOutputTokens = ptr(5)
	b.Sample.Counters.ReasoningOutputTokens = ptr(4)
	if _, err := CounterContributions([]Observation{a, b}); err == nil {
		t.Fatal("accepted regressing reasoning counter")
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
