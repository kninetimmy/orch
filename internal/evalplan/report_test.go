package evalplan

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/kninetimmy/orch/internal/metrics"
)

func TestEvaluationApprovalSingleUseAndFrozenScope(t *testing.T) {
	if controllerTestProcess(t) {
		return
	}
	f := newControllerFixture(t, "screen", 1)
	a := Approval{1, f.record.PlanDigest, "test-human", now(), ApprovalStatement}
	for _, change := range []func(*Approval){
		func(a *Approval) { a.SchemaVersion = 0 },
		func(a *Approval) { a.PlanDigest = "sha256:" + strings.Repeat("0", 64) },
		func(a *Approval) { a.Statement = "approve-and-enter-delivery" },
		func(a *Approval) { a.ApprovedBy = "" },
		func(a *Approval) { a.ApprovedAt = "invalid" },
		func(a *Approval) { a.ApprovedAt = time.Now().Add(-25 * time.Hour).Format(time.RFC3339Nano) },
		func(a *Approval) { a.ApprovedAt = time.Now().Add(time.Hour).Format(time.RFC3339Nano) },
	} {
		invalid := a
		change(&invalid)
		if _, err := PrepareApproved(t.Context(), f.repo, f.root, f.record.PlanDigest, invalid); err == nil {
			t.Fatal("invalid approval accepted")
		}
	}
	e, err := PrepareApproved(t.Context(), f.repo, f.root, f.record.PlanDigest, a)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := PrepareApproved(t.Context(), f.repo, f.root, f.record.PlanDigest, a); err == nil {
		t.Fatal("human assertion reused for a fresh schedule")
	}
	legacy := f.prepare(t)
	if _, err := Run(t.Context(), f.root, legacy.ID, ""); err == nil {
		t.Fatal("unapproved production preparation executed")
	}
	if r, err := Inspect(f.root, legacy.ID); err != nil || r.Snapshot.Approval != nil || r.Snapshot.EvidenceComplete {
		t.Fatalf("schema-1 inspection: %v", err)
	}
	p, err := Run(t.Context(), f.root, e.ID, "")
	if err != nil || p.State != "refused" {
		t.Fatalf("approved no-model refusal: %+v %v", p, err)
	}
	r, err := RetainReport(f.root, e.ID)
	if err != nil || r.Snapshot.Approval == nil || !reflect.DeepEqual(r.Snapshot.Approval.Scope, Scope(f.record)) || r.Snapshot.UnrunUnits != 3 || !r.Snapshot.EvidenceComplete {
		t.Fatalf("scope/refusal report: %+v %v", r, err)
	}
	if r.Snapshot.Attempts[0].ExecutionSource != "native-eligibility-only" || len(r.Snapshot.Blockers) != 5 {
		t.Fatal("native/semantic readiness invented")
	}
	for _, name := range []string{"report.txt", "report.md", "report.json", "complete.json"} {
		if _, err := os.Stat(filepath.Join(r.Destination, name)); err != nil {
			t.Fatal(err)
		}
	}
	bytes, err := os.ReadFile(filepath.Join(f.root, e.ID, "approval.json"))
	if err != nil {
		t.Fatal(err)
	}
	var receipt ApprovalRecord
	if err := json.Unmarshal(bytes, &receipt); err != nil {
		t.Fatal(err)
	}
	receipt.Approval.ApprovedBy = "tampered-human"
	writeFixture(t, filepath.Join(f.root, e.ID, "approval.json"), fixtureJSON(t, receipt))
	if _, err := Inspect(f.root, e.ID); err == nil {
		t.Fatal("unanchored modified approval accepted")
	}
}

func TestReportMatchedCountersRetriesRepairsAndPrivacy(t *testing.T) {
	if controllerTestProcess(t) {
		return
	}
	f := newControllerFixture(t, "screen", 2)
	candidate := f.proposal.Baseline
	f.proposal.Candidate, f.proposal.Intervention = &candidate, "requested-profile"
	f.preview(t)
	e := f.prepare(t)
	sequence := int64(0)
	cumulative := int64(0)
	first := true
	p, err := run(t.Context(), f.root, e.ID, scriptedWorker{func(ctx context.Context, request workerRequest) (workerResult, error) {
		sequence++
		cumulative += 10
		obs := metrics.Observation{SchemaVersion: metrics.ObservationVersion, RunID: "run-test-only", ID: fmtID(sequence), At: now(), Source: "test-only-counter", Host: "codex", Session: "test-only-session", Sample: &metrics.CounterSample{Stream: "native-total", Mode: "cumulative", Sequence: sequence, Counters: metrics.Counters{InputTokens: &cumulative}}}
		// Snapshot the pointer-backed sample: changing later counters must not change retained facts.
		value := cumulative
		obs.Sample.Counters.InputTokens = &value
		unavailable := obs
		unavailable.ID, unavailable.Sample = fmtID(sequence)+"-missing", nil
		unavailable.Unavailable = &metrics.Unavailable{Reason: privateSentinel}
		outcome := "native-completed"
		if request.Unit.Ordinal == 1 && first {
			outcome, first = "infrastructure-failure", false
		} else if request.Unit.Ordinal == 1 && sequence == 2 {
			outcome = "task-failure"
		}
		output := privateSentinel
		return workerResult{Outcome: outcome, Detail: privateSentinel, Output: &output, Native: &NativeEvidence{Status: privateSentinel, Observations: []metrics.Observation{obs, unavailable}}}, nil
	}})
	if err != nil || p.State != "completed" {
		t.Fatalf("simulated matched controller: %+v %v", p, err)
	}
	r, err := RetainReport(f.root, e.ID)
	if err != nil {
		t.Fatal(err)
	}
	if r.Snapshot.AttemptsConsumed != 17 || r.Snapshot.RepairsConsumed != 1 || len(r.Snapshot.Attempts) != 18 || len(r.Snapshot.Pairs) != 8 || len(r.Snapshot.Ranges) != 8 {
		t.Fatalf("budgets/pairs/ranges: attempts=%d repairs=%d records=%d pairs=%d ranges=%d", r.Snapshot.AttemptsConsumed, r.Snapshot.RepairsConsumed, len(r.Snapshot.Attempts), len(r.Snapshot.Pairs), len(r.Snapshot.Ranges))
	}
	if r.Snapshot.Values[0].Value != 30 || !r.Snapshot.Values[0].Complete || r.Snapshot.Values[1].Value != 10 || r.Snapshot.Values[0].Counter != "input_tokens" {
		t.Fatal("cumulative deduplication/failed resources lost")
	}
	for _, a := range r.Snapshot.Attempts {
		if a.Native.Observed != nil || a.Native.Requested != nil || a.Grade != "unknown" || a.ReceiptWallMS == nil || a.ExecutionSource != "no-model-test-script" {
			t.Fatal("simulated identity/grade/time evidence changed")
		}
	}
	for _, format := range []string{"text", "markdown", "json"} {
		var out bytes.Buffer
		if err := RenderReport(&out, r, format); err != nil {
			t.Fatal(err)
		}
		if strings.Contains(out.String(), privateSentinel) || !strings.Contains(out.String(), `"input_tokens"`) || strings.Contains(out.String(), `"total_tokens"`) {
			t.Fatal("private prose leaked or total synthesized")
		}
		start := strings.Index(out.String(), "{\n")
		end := strings.LastIndex(out.String(), "}") + 1
		var same Report
		if err := json.Unmarshal([]byte(out.String()[start:end]), &same); err != nil || !reflect.DeepEqual(&same, r) {
			t.Fatalf("format parity %s: %v", format, err)
		}
	}
	t.Log("Successful controller execution is no-model-test-script only; no native isolation, semantic grading or measured baseline evidence.")
}

func fmtID(n int64) string { return time.Unix(n, 0).UTC().Format("150405") }

func TestReportCounterScopesAndPartialCoverage(t *testing.T) {
	s := Snapshot{Progress: Progress{Slots: []Slot{
		{Unit: Unit{Ordinal: 1, CaseID: "case", Repetition: 1, Side: "baseline"}, Status: "native-completed", Attempts: []AttemptRef{{Number: 1}, {Number: 2}}},
		{Unit: Unit{Ordinal: 2, CaseID: "case", Repetition: 1, Side: "candidate"}, Status: "task-failure", Attempts: []AttemptRef{{Number: 1}, {Number: 2}}},
	}}, Scope: ApprovalScope{Preparation: Record{Plan: Plan{Repetitions: 1}}}, Values: []CounterValue{}, Pairs: []PairValue{}, Ranges: []RepeatRange{}}
	for i, n := range []int64{10, 18, 23, 7} {
		value := n
		o := metrics.Observation{SchemaVersion: 2, RunID: "run-scopes", ID: fmtID(int64(i + 1)), At: time.Unix(int64(i+1), 0).UTC().Format(time.RFC3339Nano), Host: "codex", Source: "app-server", Session: "session", Sample: &metrics.CounterSample{Stream: "tokens", Mode: "cumulative", Sequence: int64(i + 1), Counters: metrics.Counters{InputTokens: &value}}}
		unit, attempt := 1, i+1
		if i >= 2 {
			unit, attempt = 2, 1
		}
		if i == 3 {
			o.Source, o.Session, o.Sample.Sequence = "session-log", "other-session", 1
		}
		s.Observations = append(s.Observations, AttributedObservation{Unit: unit, Attempt: attempt, Actor: "unknown", Observation: o})
	}
	measure(&s)
	if len(s.Values) != 3 || s.Values[0].Value != 18 || !s.Values[0].Complete || s.Values[1].Value != 5 || s.Values[1].Complete || s.Values[2].Value != 7 || s.Values[2].Source != "session-log" || len(s.Pairs) != 0 {
		t.Fatalf("coverage/source/delta semantics: %+v", s.Values)
	}
	if *s.Observations[1].Contribution.InputTokens != 8 || s.Observations[1].Contribution.TotalTokens != nil {
		t.Fatal("cumulative contribution or missing total changed")
	}
	s.Observations = append(s.Observations, s.Observations[0])
	s.Values, s.Pairs, s.Ranges = []CounterValue{}, []PairValue{}, []RepeatRange{}
	measure(&s)
	if !strings.HasPrefix(s.MeasurementStatus, "unknown") || len(s.Values) != 0 {
		t.Fatal("conflicting cross-attempt observations became complete cost")
	}
}

func TestReportConcurrentStopReadAndPublicationPreservation(t *testing.T) {
	if controllerTestProcess(t) {
		return
	}
	f := newControllerFixture(t, "screen", 1)
	e := f.prepare(t)
	initial, err := RetainReport(f.root, e.ID)
	if err != nil || initial.Snapshot.EvidenceComplete {
		t.Fatalf("prepared snapshot: %v", err)
	}
	started := make(chan struct{})
	done := make(chan error, 1)
	go func() {
		_, err := run(t.Context(), f.root, e.ID, scriptedWorker{func(ctx context.Context, request workerRequest) (workerResult, error) {
			close(started)
			<-ctx.Done()
			return workerResult{Outcome: "interrupted"}, ctx.Err()
		}})
		done <- err
	}()
	select {
	case <-started:
	case <-time.After(15 * time.Second):
		t.Fatal("controller did not start")
	}
	var wg sync.WaitGroup
	failures := make(chan error, 8)
	for range 2 {
		wg.Go(func() { failures <- Stop(f.root, e.ID) })
		wg.Go(func() { _, err := Status(f.root, e.ID); failures <- err })
		wg.Go(func() { _, err := Inspect(f.root, e.ID); failures <- err })
		wg.Go(func() { _, err := RetainReport(f.root, e.ID); failures <- err })
	}
	wg.Wait()
	close(failures)
	for err := range failures {
		if err != nil {
			t.Fatal(err)
		}
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(15 * time.Second):
		t.Fatal("stop cleanup exceeded bound")
	}
	final, err := RetainReport(f.root, e.ID)
	if err != nil || !final.Snapshot.StopRequested || !final.Snapshot.StopAcknowledged || final.Snapshot.Progress.State != "stopped" || final.Destination == initial.Destination {
		t.Fatalf("stop/publication evidence: %+v %v", final, err)
	}
	if _, err := os.Stat(filepath.Join(initial.Destination, "report.json")); err != nil {
		t.Fatal("earlier snapshot overwritten")
	}
	var readers sync.WaitGroup
	for range 4 {
		readers.Go(func() {
			if _, err := RetainReport(f.root, e.ID); err != nil {
				t.Error(err)
			}
		})
	}
	readers.Wait()
	writeFixture(t, filepath.Join(final.Destination, "report.txt"), []byte("corrupt-report-sentinel"))
	if _, err := RetainReport(f.root, e.ID); err == nil {
		t.Fatal("corrupt report silently replaced")
	}
	if bytes, err := os.ReadFile(filepath.Join(final.Destination, "report.txt")); err != nil || string(bytes) != "corrupt-report-sentinel" {
		t.Fatal("report conflict replaced")
	}
	if _, err := Status(f.root, e.ID); err != nil {
		t.Fatal("report corruption poisoned controller status")
	}
	if err := Stop(f.root, e.ID); err != nil {
		t.Fatal("report corruption spent stop reserve")
	}
}

func TestReportInterruptedPublicationAndMissingObservations(t *testing.T) {
	if controllerTestProcess(t) {
		return
	}
	f := newControllerFixture(t, "screen", 1)
	e := f.prepare(t)
	r, err := Inspect(f.root, e.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(r.Destination, 0o700); err != nil {
		t.Fatal(err)
	}
	writeFixture(t, filepath.Join(r.Destination, ".pending-fixture"), []byte("unpublished"))
	if _, err := RetainReport(f.root, e.ID); err == nil {
		t.Fatal("interrupted report publisher taken over")
	}
	if _, err := Status(f.root, e.ID); err != nil {
		t.Fatal("interrupted report poisoned valid evidence")
	}
	if err := Stop(f.root, e.ID); err != nil {
		t.Fatal(err)
	}
	newReport, err := RetainReport(f.root, e.ID)
	if err != nil || newReport.Destination == r.Destination || newReport.Snapshot.StopAcknowledged || newReport.Snapshot.EvidenceComplete {
		t.Fatalf("incomplete snapshot/stop receipt: %+v %v", newReport, err)
	}
	if len(newReport.Snapshot.Observations) != 0 || len(newReport.Snapshot.Values) != 0 || !strings.HasPrefix(newReport.Snapshot.CostPerAccepted, "undefined") {
		t.Fatal("unknown usage/acceptance converted into zero")
	}
	// Future capacity failures remain confined to the report namespace.
	tooLarge := filepath.Join(filepath.Dir(newReport.Destination), "oversized-output")
	file, err := os.Create(tooLarge)
	if err != nil {
		t.Fatal(err)
	}
	if err := file.Truncate(maxRecordBytes + 1); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	writeFixture(t, filepath.Join(f.root, e.ID, ".pending-later-snapshot"), []byte("unpublished controller observation"))
	if _, err := RetainReport(f.root, e.ID); err == nil {
		t.Fatal("report namespace capacity failure accepted")
	}
	if err := Stop(f.root, e.ID); err != nil {
		t.Fatal("report capacity consumed stop reserve")
	}
	other := f.prepare(t)
	if _, err := RetainReport(f.root, other.ID); err != nil {
		t.Fatal("different evaluation namespace interfered")
	}
	if _, err := Status(f.root, e.ID); err != nil {
		t.Fatal(err)
	}
}
