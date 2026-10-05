package evalplan

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/kninetimmy/orch/internal/evalcorpus"
	"github.com/kninetimmy/orch/internal/metrics"
)

type Blocker struct {
	Name         string `json:"name"`
	Evidence     string `json:"evidence"`
	Prerequisite string `json:"next_prerequisite"`
}

type ReportAttempt struct {
	Unit            Unit            `json:"unit"`
	Number          int             `json:"number"`
	Kind            string          `json:"kind"`
	Evidence        DigestedFile    `json:"evidence"`
	Role            string          `json:"role"`
	StartedAt       string          `json:"started_at,omitempty"`
	FinishedAt      string          `json:"finished_at,omitempty"`
	Outcome         string          `json:"outcome"`
	ExecutionSource string          `json:"execution_source"`
	Grade           string          `json:"grade"`
	Grading         *GradeResult    `json:"grading,omitempty"`
	Verification    string          `json:"verification"`
	Cleanup         Cleanup         `json:"cleanup"`
	Native          *NativeEvidence `json:"native,omitempty"`
	Eligibility     *Eligibility    `json:"eligibility,omitempty"`
	Artifacts       []DigestedFile  `json:"artifacts"`
	ReceiptWallMS   *int64          `json:"receipt_wall_ms,omitempty"`
}

type AttributedObservation struct {
	Unit         int                 `json:"unit"`
	Attempt      int                 `json:"attempt"`
	Actor        string              `json:"actor"`
	Evidence     DigestedFile        `json:"evidence"`
	Observation  metrics.Observation `json:"observation"`
	Contribution metrics.Counters    `json:"contribution"`
}

// Values from different host/source/stream definitions remain separate. Each
// native counter is independent: total is never reconstructed or added to parts.
type CounterValue struct {
	Unit             int    `json:"unit"`
	Host             string `json:"host"`
	Source           string `json:"source"`
	Stream           string `json:"stream"`
	Counter          string `json:"counter"`
	Value            int64  `json:"value"`
	CoveredAttempts  int    `json:"covered_attempts"`
	ConsumedAttempts int    `json:"consumed_attempts"`
	Complete         bool   `json:"complete"`
}

type PairValue struct {
	CaseID     string `json:"case_id"`
	Repetition int64  `json:"repetition"`
	Host       string `json:"host"`
	Source     string `json:"source"`
	Stream     string `json:"stream"`
	Counter    string `json:"counter"`
	Baseline   int64  `json:"baseline"`
	Candidate  int64  `json:"candidate"`
	Difference int64  `json:"candidate_minus_baseline"`
}

type RepeatRange struct {
	CaseID   string `json:"case_id"`
	Side     string `json:"side"`
	Host     string `json:"host"`
	Source   string `json:"source"`
	Stream   string `json:"stream"`
	Counter  string `json:"counter"`
	Minimum  int64  `json:"minimum"`
	Maximum  int64  `json:"maximum"`
	Covered  int    `json:"covered_repetitions"`
	Expected int64  `json:"expected_repetitions"`
}

type SafetyFinding struct {
	Unit        int          `json:"unit"`
	Attempt     int          `json:"attempt"`
	Side        string       `json:"side"`
	Category    string       `json:"category"`
	Evidence    DigestedFile `json:"evidence"`
	Disposition string       `json:"disposition"`
}

type UnitTiming struct {
	Unit          int    `json:"unit"`
	Start         string `json:"start"`
	End           string `json:"end"`
	ReceiptWallMS int64  `json:"receipt_wall_ms"`
}

// Snapshot contains only public plan metadata, typed observations and guarded
// references. Free-form reasons, native errors and worker output stay in evidence.
type Snapshot struct {
	SchemaVersion     int                     `json:"schema_version"`
	EvaluationID      string                  `json:"evaluation_id"`
	EvaluationSHA256  string                  `json:"evaluation_sha256"`
	Repository        string                  `json:"repository"`
	PreparedAt        string                  `json:"prepared_at"`
	Scope             ApprovalScope           `json:"scope"`
	Approval          *ApprovalRecord         `json:"approval,omitempty"`
	ApprovalStatus    string                  `json:"approval_status"`
	Progress          Progress                `json:"progress"`
	Evidence          []DigestedFile          `json:"evidence"`
	EvidenceComplete  bool                    `json:"evidence_complete"`
	StopRequested     bool                    `json:"stop_requested"`
	StopAcknowledged  bool                    `json:"controller_stop_acknowledged"`
	ProcessLiveness   string                  `json:"process_liveness"`
	AttemptsConsumed  int64                   `json:"attempts_consumed"`
	RepairsConsumed   int64                   `json:"repairs_consumed"`
	CompletedAttempts int                     `json:"completed_attempt_records"`
	UnrunUnits        int                     `json:"unrun_units"`
	Attempts          []ReportAttempt         `json:"attempts"`
	Observations      []AttributedObservation `json:"observations"`
	MeasurementStatus string                  `json:"measurement_status"`
	Values            []CounterValue          `json:"counter_values"`
	Pairs             []PairValue             `json:"matched_values"`
	Ranges            []RepeatRange           `json:"repeat_ranges"`
	UnitTimings       []UnitTiming            `json:"unit_receipt_wall_spans"`
	Blockers          []Blocker               `json:"blockers"`
	Safety            []SafetyFinding         `json:"safety_findings"`
	Decision          string                  `json:"decision"`
	CostPerAccepted   string                  `json:"cost_per_accepted_outcome"`
	Unknowns          []string                `json:"unknowns"`
	Reproduction      [][]string              `json:"reproduction_argv"`
	Grading           *GradingSummary         `json:"grading,omitempty"`
}

type Report struct {
	SchemaVersion  int      `json:"schema_version"`
	SnapshotSHA256 string   `json:"snapshot_sha256"`
	Destination    string   `json:"destination"`
	Snapshot       Snapshot `json:"snapshot"`
}

func terminalState(state string) bool {
	return state != "prepared" && state != "running"
}

func blockers(e *Evaluation) []Blocker {
	return []Blocker{
		{"independent-semantic-validation", "Readiness references are opaque; grading-journal validation and judgments are attributed assertions, not authenticated semantic proof.", "Fresh reviewers must independently check pinned sources, semantic mappings, control reproductions and unresolved disputes."},
		{"exposure", "Readiness references do not establish held-out exposure or training familiarity.", "Review and retain the frozen exposure record before a later trial."},
		{"worker-access-enforcement", "Guarded storage identity and local permissions do not prove worker read/write denial.", "Implement and independently verify protected runtime access under a separate approved change."},
		{"native-model-tool-validation", "Every production controller attempt refuses; RunSession and Session.Resume retain their model-turn gates for every role.", "Complete separately approved native model-tool validation and a reviewed refusal change; configuration diagnostics cannot satisfy this prerequisite."},
		{"decision-and-measurement", "The decision rule is opaque; model-trial and controller/grader/human-work coverage remain unestablished.", "Validate decision semantics and all claimed measurement coverage before a later finite trial."},
	}
}

// Inspect is read-only. The validated chain is frozen at the observed sequence;
// later immutable records never change that snapshot or imply a live process.
func Inspect(storageRoot, id string) (*Report, error) {
	g, e, hash, err := openEvaluation(storageRoot, id)
	if err != nil {
		return nil, err
	}
	defer g.close()
	p, err := readProgress(g, e, hash)
	if err != nil {
		return nil, err
	}
	s := Snapshot{SchemaVersion: 2, EvaluationID: id, EvaluationSHA256: hash, Repository: e.Repository, PreparedAt: e.PreparedAt,
		Scope: Scope(&e.Preparation), ApprovalStatus: "unknown; no retained evaluation approval", Progress: *p,
		Evidence: []DigestedFile{{"evaluation.json", hash}}, EvidenceComplete: terminalState(p.State) && p.State != "incomplete",
		ProcessLiveness: "unknown; retained progress is not a liveness observation", Attempts: []ReportAttempt{},
		Observations: []AttributedObservation{}, Values: []CounterValue{}, Pairs: []PairValue{}, Ranges: []RepeatRange{}, UnitTimings: []UnitTiming{},
		Blockers: blockers(e), Safety: []SafetyFinding{}, Decision: "inconclusive; decision semantics and complete eligible comparisons unavailable",
		CostPerAccepted: "undefined; no validated nonzero accepted denominator", Unknowns: []string{
			"Absent, unsupported, disputed or invalidated semantic judgments remain unknown; asserted evaluator identities are not authenticated.",
			"Human clarifications/corrections/escalations and work durations, exposure observations: unknown, never zero events.",
			"Observed inference identity and native shutdown acknowledgement: unknown unless explicitly retained.",
			"Controller/grader/root coverage and whole-Orch cost: unknown; task-agent subtotals cannot establish whole-Orch cost.",
			"Receipt/wall timestamps are controller observations; active-agent time exists only in explicitly reported intervals.",
			"Evidence is preserved without profile adoption, Delivery/configuration/merge authority or durable execution resume.",
		}, Reproduction: [][]string{
			{"orch", "eval", "status", "--storage-root", e.Preparation.Plan.StorageRoot, "--run", id, "--json"},
			{"orch", "eval", "report", "--storage-root", e.Preparation.Plan.StorageRoot, "--run", id, "--format", "json"},
		}}
	// Never echo arbitrary retained controller/worker prose in any summary format.
	s.Progress.Reason = ""
	for _, note := range p.Inspection {
		if note != "Durable stop request retained." {
			s.EvidenceComplete = false
		}
	}
	if a, err := readApproval(g, e); err == nil {
		s.Approval, s.ApprovalStatus = a, "retained single-use human assertion; never native eligibility"
		data, err := g.read("approval.json", maxRecordBytes)
		if err != nil {
			return nil, err
		}
		s.Evidence = append(s.Evidence, DigestedFile{"approval.json", evalcorpus.Digest(data)})
	} else if !errors.Is(err, errNoApproval) {
		return nil, fmt.Errorf("invalid retained approval: %w", err)
	}
	for n := 0; n <= p.Sequence; n++ {
		data, err := g.read(progressName(n), maxRecordBytes)
		if err != nil {
			return nil, err
		}
		s.Evidence = append(s.Evidence, DigestedFile{progressName(n), evalcorpus.Digest(data)})
	}
	s.StopRequested, err = stopped(g, e)
	if err != nil {
		return nil, err
	}
	s.StopAcknowledged = s.StopRequested && p.State == "stopped"
	if s.StopRequested {
		data, err := g.read("stop/request.json", MaxPlanBytes)
		if err != nil {
			return nil, err
		}
		s.Evidence = append(s.Evidence, DigestedFile{"stop/request.json", evalcorpus.Digest(data)})
	}
	for _, slot := range p.Slots {
		var firstStart, lastFinish string
		finished := len(slot.Attempts) > 0 && slot.Status != "running"
		s.AttemptsConsumed += slot.AttemptsConsumed
		s.RepairsConsumed += slot.RepairsConsumed
		if slot.Status == "unrun" {
			s.UnrunUnits++
		}
		role := e.Preparation.Plan.Cases[slices.IndexFunc(e.Preparation.Plan.Cases, func(c PublicCase) bool { return c.ID == slot.CaseID })].Role
		for _, ref := range slot.Attempts {
			name := attemptName(slot.Ordinal, ref.Number)
			item := ReportAttempt{Unit: slot.Unit, Number: ref.Number, Kind: ref.Kind,
				Evidence: DigestedFile{name + "/attempt.json", ref.SHA256}, Role: role, Outcome: "unknown", ExecutionSource: "unknown",
				Grade: "unknown", Verification: "unknown", Cleanup: Cleanup{Status: "unknown", Detail: "see retained evidence"}, Artifacts: []DigestedFile{}}
			if ref.SHA256 != "" {
				data, err := g.read(item.Evidence.Path, maxRecordBytes)
				if err != nil || evalcorpus.Digest(data) != ref.SHA256 {
					return nil, fmt.Errorf("attempt changed during snapshot: %v", err)
				}
				var a AttemptRecord
				if err := strictStored(data, &a); err != nil {
					return nil, err
				}
				item.StartedAt, item.FinishedAt = a.StartedAt, a.FinishedAt
				if ref.Number == 1 {
					firstStart = a.StartedAt
				}
				lastFinish = a.FinishedAt
				item.Outcome, item.ExecutionSource, item.Eligibility = a.Outcome, a.ExecutionSource, a.Eligibility
				if slices.Contains([]string{"unknown", "timed-out", "failed", "retained-artifact-integrity-only; no semantic grade"}, a.Verification) {
					item.Verification = a.Verification
				}
				item.Cleanup = a.Cleanup
				item.Cleanup.Detail = "see retained attempt evidence; local cleanup is not native acknowledgement"
				if !slices.Contains([]string{"removed-clean", "preserved-dirty"}, a.Cleanup.Status) {
					s.EvidenceComplete = false
					if !slices.Contains([]string{"unknown", "preserved-unverifiable", "preserved-unacknowledged", "timed-out"}, a.Cleanup.Status) {
						item.Cleanup.Status = "unknown"
					}
				}
				item.Native = publicNative(a.Native)
				for _, file := range append(slices.Clone(a.Initial), a.Artifacts...) {
					item.Artifacts = append(item.Artifacts, DigestedFile{name + "/" + file.Path, file.SHA256})
				}
				for _, file := range []*DigestedFile{a.Output, a.InvalidNative} {
					if file != nil {
						item.Artifacts = append(item.Artifacts, DigestedFile{name + "/" + file.Path, file.SHA256})
					}
				}
				item.Artifacts = append(item.Artifacts, DigestedFile{name + "/case.json", a.CaseSHA256})
				if start, err := time.Parse(time.RFC3339Nano, a.StartedAt); err == nil {
					if end, err := time.Parse(time.RFC3339Nano, a.FinishedAt); err == nil && !end.Before(start) {
						ms := end.Sub(start).Milliseconds()
						item.ReceiptWallMS = &ms
					}
				}
				if item.Native != nil {
					for _, o := range item.Native.Observations {
						actor := o.Role
						if actor == "" {
							actor = "unknown"
						}
						s.Observations = append(s.Observations, AttributedObservation{Unit: slot.Ordinal, Attempt: ref.Number, Actor: actor, Evidence: item.Evidence, Observation: o})
					}
				}
				s.CompletedAttempts++
				if slices.Contains([]string{"invalid-evidence", "protocol-invalid", "safety-failure"}, a.Outcome) {
					s.Safety = append(s.Safety, SafetyFinding{slot.Ordinal, ref.Number, slot.Side, a.Outcome, item.Evidence, "disqualifying when attributable; never averaged into savings"})
				}
			} else {
				finished = false
			}
			s.Attempts = append(s.Attempts, item)
		}
		if start, err := time.Parse(time.RFC3339Nano, firstStart); finished && err == nil {
			if end, err := time.Parse(time.RFC3339Nano, lastFinish); err == nil && !end.Before(start) {
				s.UnitTimings = append(s.UnitTimings, UnitTiming{slot.Ordinal, firstStart, lastFinish, end.Sub(start).Milliseconds()})
			}
		}
	}
	if p.State == "safety-failure" && len(s.Safety) == 0 {
		for _, evidence := range s.Evidence {
			if evidence.Path == progressName(p.Sequence) {
				s.Safety = append(s.Safety, SafetyFinding{Category: "controller-safety-failure", Evidence: evidence, Disposition: "disqualifying when attributable; attribution otherwise unknown"})
			}
		}
	}
	if len(s.Safety) != 0 {
		s.Decision = "inconclusive with disqualifying safety findings; no profile adoption"
	}
	if err := inspectGrading(g, e, hash, p, &s); err != nil {
		return nil, err
	}
	measure(&s)
	sha := storedDigest(s)
	return &Report{2, sha, filepath.Join(e.Preparation.Plan.StorageRoot, "reports", id, sha), s}, g.check()
}

func publicNative(n *NativeEvidence) *NativeEvidence {
	if n == nil {
		return nil
	}
	copy := *n
	copy.Status = "unknown; see retained native evidence"
	for _, id := range []*string{&copy.ThreadID, &copy.SessionID, &copy.TurnID} {
		if !identifierPattern.MatchString(*id) {
			*id = ""
		}
	}
	copy.Observations = slices.Clone(n.Observations)
	for i := range copy.Observations {
		if copy.Observations[i].Unavailable != nil {
			copy.Observations[i].Unavailable = &metrics.Unavailable{Reason: "see-retained-attempt-evidence"}
		}
	}
	return &copy
}

var counterNames = []string{"input_tokens", "output_tokens", "cache_read_tokens", "cache_creation_tokens", "total_tokens", "reasoning_output_tokens"}

func counterFields(c metrics.Counters) []*int64 {
	return []*int64{c.InputTokens, c.OutputTokens, c.CacheReadTokens, c.CacheCreationTokens, c.TotalTokens, c.ReasoningOutputTokens}
}

func measure(s *Snapshot) {
	observations := make([]metrics.Observation, len(s.Observations))
	for i, o := range s.Observations {
		observations[i] = o.Observation
	}
	deltas, err := metrics.CounterContributions(observations)
	if err != nil {
		s.MeasurementStatus = "unknown; cross-attempt observation conflict or ordering failure; inspect raw referenced evidence"
		return // valid underlying schema-1 evidence remains inspectable; no invented allocation
	}
	s.MeasurementStatus = "partial attributed observations; absent counters/actors/attempts remain unknown"
	type key struct {
		unit                          int
		host, source, stream, counter string
	}
	values := map[key]*CounterValue{}
	covered := map[key]map[int]bool{}
	for i, delta := range deltas {
		o := &s.Observations[i]
		o.Contribution = delta
		if o.Observation.Sample == nil {
			continue
		}
		for field, value := range counterFields(delta) {
			if value == nil {
				continue
			}
			k := key{o.Unit, o.Observation.Host, o.Observation.Source, o.Observation.Sample.Stream, counterNames[field]}
			if values[k] == nil {
				values[k] = &CounterValue{Unit: o.Unit, Host: k.host, Source: k.source, Stream: k.stream, Counter: k.counter}
				covered[k] = map[int]bool{}
			}
			v := values[k]
			v.Value, err = checkedAdd(v.Value, *value)
			if err != nil {
				s.Values, s.Pairs, s.Ranges = []CounterValue{}, []PairValue{}, []RepeatRange{}
				s.MeasurementStatus = "unknown; attributed counter subtotal overflow"
				return
			}
			covered[k][o.Attempt] = true
		}
	}
	for k, value := range values {
		slot := s.Progress.Slots[k.unit-1]
		value.CoveredAttempts, value.ConsumedAttempts = len(covered[k]), len(slot.Attempts)
		value.Complete = value.CoveredAttempts == value.ConsumedAttempts && slot.Status != "running" && slot.Status != "unrun" &&
			!slices.Contains([]string{"invalid-evidence", "protocol-invalid", "safety-failure", "refused"}, slot.Status)
		s.Values = append(s.Values, *value)
	}
	slices.SortFunc(s.Values, func(a, b CounterValue) int {
		return strings.Compare(fmt.Sprintf("%06d/%s/%s/%s/%s", a.Unit, a.Host, a.Source, a.Stream, a.Counter), fmt.Sprintf("%06d/%s/%s/%s/%s", b.Unit, b.Host, b.Source, b.Stream, b.Counter))
	})
	type rangeKey struct{ caseID, side, host, source, stream, counter string }
	ranges := map[rangeKey]*RepeatRange{}
	candidates := map[string]int{}
	for _, slot := range s.Progress.Slots {
		if slot.Side == "candidate" {
			candidates[fmt.Sprintf("%s/%d", slot.CaseID, slot.Repetition)] = slot.Ordinal
		}
	}
	for _, v := range s.Values {
		if !v.Complete {
			continue
		}
		slot := s.Progress.Slots[v.Unit-1]
		k := rangeKey{slot.CaseID, slot.Side, v.Host, v.Source, v.Stream, v.Counter}
		if ranges[k] == nil {
			ranges[k] = &RepeatRange{slot.CaseID, slot.Side, v.Host, v.Source, v.Stream, v.Counter, v.Value, v.Value, 0, s.Scope.Preparation.Plan.Repetitions}
		}
		r := ranges[k]
		r.Minimum, r.Maximum, r.Covered = min(r.Minimum, v.Value), max(r.Maximum, v.Value), r.Covered+1
		if slot.Side != "baseline" {
			continue
		}
		ordinal := candidates[fmt.Sprintf("%s/%d", slot.CaseID, slot.Repetition)]
		if candidate := values[key{ordinal, v.Host, v.Source, v.Stream, v.Counter}]; candidate != nil && candidate.Complete {
			s.Pairs = append(s.Pairs, PairValue{slot.CaseID, slot.Repetition, v.Host, v.Source, v.Stream, v.Counter, v.Value, candidate.Value, candidate.Value - v.Value})
		}
	}
	for _, r := range ranges {
		s.Ranges = append(s.Ranges, *r)
	}
	slices.SortFunc(s.Ranges, func(a, b RepeatRange) int {
		return strings.Compare(a.CaseID+"/"+a.Side+"/"+a.Host+"/"+a.Source+"/"+a.Stream+"/"+a.Counter, b.CaseID+"/"+b.Side+"/"+b.Host+"/"+b.Source+"/"+b.Stream+"/"+b.Counter)
	})
}

// All formats carry exactly the same typed facts. JSON sections intentionally
// avoid three separate lossy narrative/measurement implementations.
func RenderReport(w io.Writer, r *Report, format string) error {
	data, err := storedBytes(r)
	if err != nil {
		return err
	}
	switch format {
	case "json":
		_, err = w.Write(data)
	case "text":
		_, err = fmt.Fprintf(w, "Evaluation %s: %s\nEvidence complete: %t; %s.\nSnapshot: %s\nReport destination (publication verified separately): %s\n\n%s", r.Snapshot.EvaluationID, r.Snapshot.Progress.State, r.Snapshot.EvidenceComplete, gradingHeading(&r.Snapshot), r.SnapshotSHA256, r.Destination, data)
	case "markdown":
		heading := gradingHeading(&r.Snapshot)
		heading = strings.ToUpper(heading[:1]) + heading[1:]
		_, err = fmt.Fprintf(w, "# Evaluation %s\n\nState: **%s**. Evidence complete: **%t**. %s.\n\nSnapshot: `%s`\n\nReport destination (publication verified separately): `%s`\n\n```json\n%s```\n", r.Snapshot.EvaluationID, r.Snapshot.Progress.State, r.Snapshot.EvidenceComplete, heading, r.SnapshotSHA256, r.Destination, data)
	default:
		return fmt.Errorf("require report format text, markdown or json")
	}
	return err
}

// RetainReport publishes one immutable bundle in a sibling namespace, so report
// bytes/pending files never spend controller/stop capacity or poison Status.
// Each snapshot gets an exclusive claim; concurrent identical writers only read
// its completion manifest. A crashed publisher is inspectable, never taken over.
func RetainReport(storageRoot, id string) (*Report, error) {
	r, err := Inspect(storageRoot, id)
	if err != nil {
		return nil, err
	}
	root, err := openGuarded(storageRoot)
	if err != nil {
		return nil, err
	}
	defer root.close()
	reports, err := directoryForPublication(root, "reports")
	if err != nil {
		return nil, err
	}
	defer reports.close()
	area, err := directoryForPublication(reports, id)
	if err != nil {
		return nil, err
	}
	defer area.close()
	// Replay of a completed bundle remains available even after capacity is spent
	// or a different publication was interrupted.
	if existing, err := area.child(r.SnapshotSHA256); err == nil {
		defer existing.close()
		return r, waitForBundle(existing, r)
	} else if !errors.Is(err, fs.ErrNotExist) {
		return nil, err
	}
	var claim *guardedDir
	for tries := 0; tries < 50; tries++ {
		claim, err = area.createDir("publication")
		if !errors.Is(err, fs.ErrExist) {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if err != nil {
		return nil, fmt.Errorf("report publisher active or interrupted; no takeover: %w", err)
	}
	result, publishErr := retainBundle(area, r)
	// This invocation removes only its own empty temporary claim, after checking
	// its anchored identity. A crash leaves the claim inspectable and fails closed.
	checkErr := claim.check()
	claim.close()
	var cleanupErr error
	if checkErr == nil {
		cleanupErr = area.root.Remove("publication")
	}
	return result, errors.Join(publishErr, checkErr, cleanupErr, area.check())
}

func retainBundle(area *guardedDir, r *Report) (*Report, error) {
	size, entries, _, err := inventory(area)
	if err != nil {
		return nil, fmt.Errorf("report namespace capacity/identity failure: %w", err)
	}
	// A single exclusive publisher per snapshot bounds races without a global
	// registry or changing Delivery locks. Refuse new snapshots conservatively.
	if size > maxControllerBytes-4*maxRecordBytes || entries > maxControllerEntries-16 {
		return nil, fmt.Errorf("report namespace capacity exceeded; earlier evidence and stop capacity preserved")
	}
	bundle, err := area.createDir(r.SnapshotSHA256)
	if errors.Is(err, fs.ErrExist) {
		bundle, err = area.child(r.SnapshotSHA256)
		if err != nil {
			return nil, err
		}
		defer bundle.close()
		return r, waitForBundle(bundle, r)
	}
	if err != nil {
		return nil, err
	}
	defer bundle.close()
	bundle.budget = &writeBudget{used: maxControllerBytes - 4*maxRecordBytes, entries: maxControllerEntries - 16}
	files := []DigestedFile{}
	for _, format := range []string{"text", "markdown", "json"} {
		var data bytes.Buffer
		if err := RenderReport(&data, r, format); err != nil {
			return nil, err
		}
		name := map[string]string{"text": "report.txt", "markdown": "report.md", "json": "report.json"}[format]
		if err := bundle.publishBytes(name, data.Bytes()); err != nil {
			return nil, fmt.Errorf("report publication incomplete; artifacts preserved: %w", err)
		}
		files = append(files, DigestedFile{name, evalcorpus.Digest(data.Bytes())})
	}
	if err := bundle.publish("complete.json", files); err != nil {
		return nil, fmt.Errorf("report publication incomplete: %w", err)
	}
	return r, verifyBundle(bundle, r)
}

func waitForBundle(bundle *guardedDir, r *Report) error {
	for tries := 0; tries < 50; tries++ {
		if err := verifyBundle(bundle, r); err == nil {
			return nil
		} else if !errors.Is(err, fs.ErrNotExist) {
			return fmt.Errorf("retained report conflict/corruption; preserved: %w", err)
		}
		time.Sleep(20 * time.Millisecond)
	}
	return fmt.Errorf("report publication incomplete; exclusive bundle preserved for inspection")
}

func verifyBundle(g *guardedDir, r *Report) error {
	data, err := g.read("complete.json", MaxPlanBytes)
	if err != nil {
		return err
	}
	var files []DigestedFile
	if err := strictStored(data, &files); err != nil || len(files) != 3 {
		return fmt.Errorf("invalid report completion manifest: %v", err)
	}
	for i, format := range []string{"text", "markdown", "json"} {
		var expected bytes.Buffer
		if err := RenderReport(&expected, r, format); err != nil {
			return err
		}
		name := []string{"report.txt", "report.md", "report.json"}[i]
		if files[i] != (DigestedFile{name, evalcorpus.Digest(expected.Bytes())}) {
			return fmt.Errorf("report manifest identity mismatch")
		}
		stored, err := g.read(name, maxRecordBytes)
		if err != nil || !bytes.Equal(stored, expected.Bytes()) {
			return fmt.Errorf("report bytes missing or conflicting: %s: %w", name, err)
		}
	}
	return g.check()
}
