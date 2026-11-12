package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/kninetimmy/orch/internal/evalcorpus"
	"github.com/kninetimmy/orch/internal/evalplan"
	"github.com/kninetimmy/orch/internal/metrics"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const mcpEvalID = "eval-aaaaaaaaaaaaaaaaaaaaaaaaaa"
const mcpEvalAt = "2026-11-11T12:00:00Z"

func mcpEvalBytes(t *testing.T, value any) []byte {
	t.Helper()
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	return append(data, '\n')
}

func mcpEvalStore(t *testing.T, path string, value any) string {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	data := mcpEvalBytes(t, value)
	evalWrite(t, path, data)
	return evalcorpus.Digest(data)
}

// Only retained typed fixtures are authored here: no controller, worker, host or
// grader executes. The public preview supplies the real frozen plan/schedule.
func mcpEvalFixture(t *testing.T) (*evalFixture, *evalplan.Evaluation, evalplan.Progress) {
	t.Helper()
	f := newEvalFixture(t)
	code, output, failure := f.run(t, "--json")
	if code != ExitOK {
		t.Fatalf("evaluation preview fixture: %d %s", code, failure)
	}
	var record evalplan.Record
	if err := json.Unmarshal([]byte(output), &record); err != nil {
		t.Fatal(err)
	}
	e := &evalplan.Evaluation{SchemaVersion: 1, ID: mcpEvalID, Repository: f.repo, PreparedAt: mcpEvalAt, Preparation: record}
	area := filepath.Join(f.plan.StorageRoot, e.ID)
	hash := mcpEvalStore(t, filepath.Join(area, "evaluation.json"), e)
	p := evalplan.Progress{SchemaVersion: 1, EvaluationID: e.ID, EvaluationSHA256: hash, PlanDigest: record.PlanDigest,
		State: "prepared", At: mcpEvalAt, Slots: []evalplan.Slot{}}
	for _, unit := range record.Preview.Schedule {
		p.Slots = append(p.Slots, evalplan.Slot{Unit: unit, Status: "unrun", Grade: "unknown", Attempts: []evalplan.AttemptRef{}})
	}
	mcpEvalStore(t, filepath.Join(area, "progress-000000.json"), p)
	a := evalplan.Approval{SchemaVersion: 1, PlanDigest: record.PlanDigest, ApprovedBy: "fixture-human", ApprovedAt: mcpEvalAt, Statement: evalplan.ApprovalStatement}
	receipt := evalplan.ApprovalRecord{SchemaVersion: 1, EvaluationID: e.ID, Approval: a, Scope: evalplan.Scope(&record)}
	mcpEvalStore(t, filepath.Join(area, "approval.json"), receipt)
	mcpEvalStore(t, filepath.Join(f.plan.StorageRoot, "approvals", evalcorpus.Digest(mcpEvalBytes(t, a)), "assertion.json"), receipt)
	return f, e, p
}

func mcpEvalExercise(t *testing.T, binary string) {
	t.Helper()
	f, e, prepared := mcpEvalFixture(t)
	root, storage := f.repo, f.plan.StorageRoot
	parent, cwd := filepath.Dir(root), t.TempDir()
	writeConfig(t, cwd, "invalid launching configuration [")
	cwdBefore := mcpTree(t, cwd)
	ctx, cancel := context.WithTimeout(t.Context(), 60*time.Second)
	defer cancel()
	input := map[string]any{"run_id": e.ID}

	// Existing launch forms still connect/discover, but do not guess a store.
	for _, args := range [][]string{nil, {"--repo", root}} {
		session, closeSession := mcpConnect(t, ctx, binary, root, root, args...)
		mcpDiscover(t, ctx, session, root)
		before := mcpTree(t, parent)
		mcpEvalError(t, mcpCall(t, ctx, session, root, "orch_eval_status", input), "--eval-storage-root")
		if mcpTree(t, parent) != before {
			t.Fatal("absent binding created storage or lifecycle records")
		}
		closeSession()
	}

	serverBefore := mcpTree(t, parent)
	session, closeSession := mcpConnect(t, ctx, binary, cwd, root, "--repo", root, "--eval-storage-root", storage)
	mcpDiscover(t, ctx, session, root)
	if mcpTree(t, parent) != serverBefore {
		t.Fatal("storage-bound initialization/discovery changed repository or storage")
	}
	r := mcpEvalParity(t, ctx, binary, f, e.ID, session)
	if r.SchemaVersion != 3 || r.Snapshot.SchemaVersion != 3 || r.Snapshot.Approval == nil || r.Snapshot.EvidenceComplete ||
		r.Snapshot.Progress.State != "prepared" || r.Snapshot.UnrunUnits != 16 || r.Snapshot.AttemptsConsumed != 0 ||
		len(r.Snapshot.Values) != 0 || len(r.Snapshot.Observations) != 0 || len(r.Snapshot.Unknowns) == 0 || len(r.Snapshot.Blockers) == 0 {
		t.Fatalf("prepared/approval/unknowns lost: %+v", r.Snapshot)
	}
	inputBefore := mcpTree(t, parent)
	for _, args := range []any{nil, json.RawMessage("null"), []any{}, true, 7, "object", map[string]any{},
		map[string]any{"run_id": ""}, map[string]any{"run_id": nil}, map[string]any{"run_id": 1}} {
		mcpEvalError(t, mcpCall(t, ctx, session, root, "orch_eval_status", args), "")
	}
	for _, field := range []string{"storage_root", "eval_storage_root", "repo", "repo_root", "repository", "unexpected"} {
		mcpEvalError(t, mcpCall(t, ctx, session, root, "orch_eval_status", map[string]any{"run_id": e.ID, field: cwd}), "")
	}
	for _, id := range []string{" ", "../" + e.ID, storage, e.ID + "/child", e.ID + `\child`, strings.ToUpper(e.ID),
		"run-delivery", "eval-" + strings.Repeat("a", 25), "eval-" + strings.Repeat("1", 26)} {
		mcpEvalError(t, mcpCall(t, ctx, session, root, "orch_eval_status", map[string]any{"run_id": id}), "invalid evaluation ID")
	}
	mcpEvalError(t, mcpCall(t, ctx, session, root, "orch_eval_status", map[string]any{"run_id": "eval-" + strings.Repeat("b", 26)}), "inspect child directory")
	if mcpTree(t, parent) != inputBefore {
		t.Fatal("invalid tool arguments changed storage")
	}

	// A complete running snapshot with an unfinished attempt is inspectable, but
	// missing execution/cleanup/counters stay unknown, never inferred from a PID.
	running := prepared
	running.Sequence, running.State, running.PreviousSHA256 = 1, "running", evalcorpus.Digest(mcpEvalBytes(t, prepared))
	running.Slots = slices.Clone(prepared.Slots)
	running.Slots[0].Status, running.Slots[0].AttemptsConsumed = "running", 1
	running.Slots[0].Attempts = []evalplan.AttemptRef{{Number: 1, Kind: "initial"}}
	area := filepath.Join(storage, e.ID)
	mcpEvalStore(t, filepath.Join(area, "progress-000001.json"), running)
	r = mcpEvalParity(t, ctx, binary, f, e.ID, session)
	if r.Snapshot.EvidenceComplete || r.Snapshot.AttemptsConsumed != 1 || r.Snapshot.CompletedAttempts != 0 || len(r.Snapshot.Progress.Inspection) == 0 ||
		r.Snapshot.Attempts[0].Outcome != "unknown" || r.Snapshot.Attempts[0].Cleanup.Status != "unknown" || len(r.Snapshot.Values) != 0 {
		t.Fatal("partial evidence became completed work or zero measurements")
	}

	// Retain one no-model refusal with private output/source bytes, a measured
	// zero, an independent total, an absent counter and unavailable observations.
	unit := running.Slots[0].Unit
	caseIndex := slices.IndexFunc(f.manifest.Cases, func(c evalcorpus.Case) bool { return c.ID == unit.CaseID })
	c := f.manifest.Cases[caseIndex]
	attemptDir := filepath.Join(area, "unit-000001-attempt-000001")
	caseHash := mcpEvalStore(t, filepath.Join(attemptDir, "case.json"), c)
	private := []byte("PRIVATE_WORKER_OUTPUT_AND_KEY_BYTES")
	if err := os.MkdirAll(filepath.Join(attemptDir, "private"), 0o700); err != nil {
		t.Fatal(err)
	}
	evalWrite(t, filepath.Join(attemptDir, "private", "key.txt"), private)
	evalWrite(t, filepath.Join(attemptDir, "output.txt"), private)
	o := metrics.Observation{SchemaVersion: 2, RunID: "run-test-only", ID: "measured-zero", At: mcpEvalAt, Source: "test-only", Host: "codex", Session: "fixture-session",
		Sample: &metrics.CounterSample{Stream: "tokens", Mode: "delta", Sequence: 1, Counters: metrics.Counters{InputTokens: new(int64(0)), TotalTokens: new(int64(7))}}}
	unavailable := o
	unavailable.ID, unavailable.Sample, unavailable.Unavailable = "missing", nil, &metrics.Unavailable{Reason: "PRIVATE_NATIVE_REASON"}
	a := evalplan.AttemptRecord{SchemaVersion: 1, EvaluationID: e.ID, PlanDigest: e.Preparation.PlanDigest, Unit: unit, Number: 1, Kind: "initial",
		PacketSHA256: c.PacketSHA256, CaseSHA256: caseHash, Packet: "fixture-packet", Scratch: "fixture-scratch", StartedAt: mcpEvalAt, FinishedAt: mcpEvalAt,
		Outcome: "refused", ExecutionSource: "no-model-test-script", Detail: "PRIVATE_CONTROLLER_REASON", Grade: "unknown",
		Verification: "retained-artifact-integrity-only; no semantic grade", Initial: []evalplan.DigestedFile{{Path: "private/key.txt", SHA256: evalcorpus.Digest(private)}},
		Artifacts: []evalplan.DigestedFile{}, Output: &evalplan.DigestedFile{Path: "output.txt", SHA256: evalcorpus.Digest(private)},
		Native:  &evalplan.NativeEvidence{Status: "PRIVATE_NATIVE_STATUS", Observations: []metrics.Observation{o, unavailable}},
		Cleanup: evalplan.Cleanup{Status: "preserved-dirty", Detail: "PRIVATE_CLEANUP_REASON"}}
	attemptHash := mcpEvalStore(t, filepath.Join(attemptDir, "attempt.json"), a)
	final := running
	final.Sequence, final.State, final.Reason, final.PreviousSHA256 = 2, "refused", "PRIVATE_PROGRESS_REASON", evalcorpus.Digest(mcpEvalBytes(t, running))
	final.Slots = slices.Clone(running.Slots)
	final.Slots[0].Status, final.Slots[0].Attempts = "refused", []evalplan.AttemptRef{{Number: 1, Kind: "initial", SHA256: attemptHash}}
	mcpEvalStore(t, filepath.Join(area, "progress-000002.json"), final)
	r = mcpEvalParity(t, ctx, binary, f, e.ID, session)
	if !r.Snapshot.EvidenceComplete || len(r.Snapshot.Values) != 2 || r.Snapshot.Values[0].Counter != "input_tokens" || r.Snapshot.Values[0].Value != 0 ||
		r.Snapshot.Values[1].Counter != "total_tokens" || r.Snapshot.Values[1].Value != 7 || r.Snapshot.Values[0].Complete ||
		len(r.Snapshot.Pairs) != 0 || r.Snapshot.Attempts[0].ReceiptWallMS == nil || *r.Snapshot.Attempts[0].ReceiptWallMS != 0 ||
		r.Snapshot.Attempts[0].Native.Observed != nil || r.Snapshot.Observations[0].Contribution.OutputTokens != nil || !strings.HasPrefix(r.Snapshot.CostPerAccepted, "undefined") {
		t.Fatal("measured zero, independent total, unknown identity/counters or incomplete cost semantics changed")
	}
	submission := evalRefusedGrade(t, f, r)
	gradeFile := filepath.Join(filepath.Dir(f.file), "submission.json")
	evalWrite(t, gradeFile, evalJSON(t, submission))
	if _, err := evalplan.SubmitGrade(root, storage, e.ID, 1, 1, gradeFile); err != nil {
		t.Fatal(err)
	}
	r = mcpEvalParity(t, ctx, binary, f, e.ID, session)
	if len(r.Snapshot.Grading.History) != 1 || r.Snapshot.Attempts[0].Grade != "invalid" || r.Snapshot.Attempts[0].Grading.AcceptedExecution {
		t.Fatal("grading assertions lost or refused execution became acceptance")
	}

	// Retained inspection needs no source artifacts or effective configuration.
	if err := os.Rename(filepath.Dir(f.file), filepath.Dir(f.file)+"-unavailable"); err != nil {
		t.Fatal(err)
	}
	mcpEvalParity(t, ctx, binary, f, e.ID, session)
	other, closeOther := mcpConnect(t, ctx, binary, root, cwd, "--eval-storage-root", storage, "--repo", cwd)
	before := mcpTree(t, parent)
	mcpEvalError(t, mcpCall(t, ctx, other, cwd, "orch_eval_status", input), "repository does not match")
	closeOther()
	if mcpTree(t, parent) != before {
		t.Fatal("cross-repository rejection changed retained bytes")
	}
	// An intact unfinished publication remains visible as incomplete, rather
	// than being repaired, swept or confused with malformed completed evidence.
	pending := filepath.Join(area, ".pending-fixture")
	evalWrite(t, pending, []byte("PRIVATE_UNPUBLISHED_BYTES"))
	if r := mcpEvalParity(t, ctx, binary, f, e.ID, session); r.Snapshot.EvidenceComplete || len(r.Snapshot.Progress.Inspection) == 0 {
		t.Fatal("pending evidence was hidden or treated as complete")
	}
	if err := os.Remove(pending); err != nil {
		t.Fatal(err)
	}
	// A durable request is data, not proof of controller acknowledgement.
	mcpEvalStore(t, filepath.Join(area, "stop", "request.json"), map[string]any{
		"schema_version": 1, "evaluation_id": e.ID, "plan_digest": e.Preparation.PlanDigest, "request": "stop",
	})
	if r := mcpEvalParity(t, ctx, binary, f, e.ID, session); !r.Snapshot.StopRequested || r.Snapshot.StopAcknowledged {
		t.Fatal("retained stop receipt became controller acknowledgement")
	}
	wrongRoots := *e
	wrongRoots.Preparation.Plan.RepositoryRoots = []string{cwd}
	wrongDigest := evalcorpus.Digest(evalJSON(t, wrongRoots.Preparation.Plan))
	wrongRoots.Preparation.PlanDigest = "sha256:" + wrongDigest
	wrongRoots.Preparation.StorageDestination = filepath.Join(storage, wrongDigest+".json")

	for _, tc := range []struct {
		name, path string
		data       []byte
		want       string
	}{
		{"partial JSON", filepath.Join(area, "progress-000002.json"), []byte(`{"schema_version":`), "EOF"},
		{"unsupported progress", filepath.Join(area, "progress-000002.json"), bytes.Replace(mcpEvalBytes(t, final), []byte(`"schema_version": 1`), []byte(`"schema_version": 99`), 1), "invalid progress identity"},
		{"unsupported evaluation", filepath.Join(area, "evaluation.json"), bytes.Replace(mcpEvalBytes(t, e), []byte(`"schema_version": 1`), []byte(`"schema_version": 99`), 1), "identity/schema mismatch"},
		{"unknown field", filepath.Join(area, "evaluation.json"), append([]byte(`{"unsupported":true,`), mcpEvalBytes(t, e)[1:]...), "unknown field"},
		{"unverifiable repository", filepath.Join(area, "evaluation.json"), bytes.Replace(mcpEvalBytes(t, e), evalJSON(t, e.Repository), evalJSON(t, filepath.Join(cwd, "missing")), 1), "repository identity"},
		{"aliased repository", filepath.Join(area, "evaluation.json"), bytes.Replace(mcpEvalBytes(t, e), evalJSON(t, e.Repository), evalJSON(t, e.Repository+string(filepath.Separator)+"."), 1), "repository identity"},
		{"frozen repository mismatch", filepath.Join(area, "evaluation.json"), mcpEvalBytes(t, wrongRoots), "frozen repository roots"},
		{"missing attempt", filepath.Join(attemptDir, "attempt.json"), nil, "attempt evidence missing or changed"},
		{"corrupt private artifact", filepath.Join(attemptDir, "private", "key.txt"), []byte("tampered"), "retained artifact missing or changed"},
		{"missing output", filepath.Join(attemptDir, "output.txt"), nil, "retained output missing or changed"},
		{"corrupt approval", filepath.Join(area, "approval.json"), []byte("{}"), "invalid retained approval"},
		{"corrupt grading", filepath.Join(storage, filepath.FromSlash(r.Snapshot.Grading.History[0].Evidence.Path)), []byte("{}"), "stored record has missing"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			original, err := os.ReadFile(tc.path)
			if err != nil {
				t.Fatal(err)
			}
			if tc.data == nil {
				if err := os.Remove(tc.path); err != nil {
					t.Fatal(err)
				}
			} else {
				evalWrite(t, tc.path, tc.data)
			}
			before := mcpTree(t, parent)
			mcpEvalError(t, mcpCall(t, ctx, session, root, "orch_eval_status", input), tc.want)
			if mcpTree(t, parent) != before {
				t.Fatal("failed inspection repaired/changed storage")
			}
			evalWrite(t, tc.path, original)
		})
	}
	// Persistent file aliases fail even when their bytes/digests match.
	alias := filepath.Join(parent, "evaluation-hardlink")
	if err := os.Link(filepath.Join(area, "evaluation.json"), alias); err != nil {
		t.Fatal(err)
	}
	before = mcpTree(t, parent)
	mcpEvalError(t, mcpCall(t, ctx, session, root, "orch_eval_status", input), "linked file")
	if mcpTree(t, parent) != before {
		t.Fatal("aliased-file inspection changed bytes")
	}
	if err := os.Remove(alias); err != nil {
		t.Fatal(err)
	}
	// A valid launch is not permanent permission to follow later aliases.
	for _, path := range []string{attemptDir, storage} {
		if err := os.Rename(path, path+"-moved"); err != nil {
			t.Fatal(err)
		}
		mcpDirectoryLink(t, path+"-moved", path)
		before := mcpTree(t, parent)
		mcpEvalError(t, mcpCall(t, ctx, session, root, "orch_eval_status", input), "")
		if mcpTree(t, parent) != before {
			t.Fatal("runtime alias rejection changed retained bytes")
		}
		if err := os.Remove(path); err != nil {
			t.Fatal(err)
		}
		if err := os.Rename(path+"-moved", path); err != nil {
			t.Fatal(err)
		}
	}
	mcpEvalParity(t, ctx, binary, f, e.ID, session)
	before = mcpTree(t, parent)
	closeSession()
	if mcpTree(t, parent) != before {
		t.Fatal("storage-bound shutdown changed repository or storage")
	}
	// Storage-only argv keeps the existing cwd repository binding.
	before = mcpTree(t, parent)
	session, closeSession = mcpConnect(t, ctx, binary, root, root, "--eval-storage-root", storage)
	mcpEvalParity(t, ctx, binary, f, e.ID, session)
	closeSession()
	if mcpTree(t, parent) != before {
		t.Fatal("storage-only server lifetime changed repository or storage")
	}
	if mcpTree(t, cwd) != cwdBefore {
		t.Fatal("evaluation inspection changed launching/unrelated repository")
	}
	mcpEvalInvalidLaunch(t, ctx, binary, f)
}

func mcpEvalParity(t *testing.T, ctx context.Context, binary string, f *evalFixture, id string, session *mcp.ClientSession) *evalplan.Report {
	t.Helper()
	before := mcpTree(t, filepath.Dir(f.repo))
	r, err := evalplan.Inspect(f.plan.StorageRoot, id)
	if err != nil {
		t.Fatal(err)
	}
	want := mcpDecoded(t, mcpEvalBytes(t, r))
	var stdout, stderr bytes.Buffer
	cmd := mcpProcess(ctx, binary, f.repo, "eval", "status", "--storage-root", f.plan.StorageRoot, "--run", id, "--json")
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil || stderr.Len() != 0 || !reflect.DeepEqual(mcpDecoded(t, stdout.Bytes()), want) {
		t.Fatalf("CLI/shared evaluation parity: %v stderr=%s stdout=%s", err, stderr.String(), stdout.String())
	}
	result := mcpCall(t, ctx, session, f.repo, "orch_eval_status", map[string]any{"run_id": id})
	if result.IsError || len(result.Content) != 1 {
		t.Fatalf("evaluation result = %+v", result)
	}
	text, ok := result.Content[0].(*mcp.TextContent)
	if !ok || !reflect.DeepEqual(result.StructuredContent, want) || !reflect.DeepEqual(mcpDecoded(t, []byte(text.Text)), want) {
		t.Fatalf("evaluation shared/structured/text mismatch: %+v", result)
	}
	if strings.Contains(text.Text, "PRIVATE_") || !strings.HasPrefix(r.Snapshot.ProcessLiveness, "unknown") || r.SnapshotSHA256 != evalcorpus.Digest(mcpEvalBytes(t, r.Snapshot)) {
		t.Fatal("private prose leaked, liveness inferred or snapshot hash changed")
	}
	if mcpTree(t, filepath.Dir(f.repo)) != before {
		t.Fatal("CLI/shared/MCP evaluation inspection created/changed repository, storage, reports, approvals or locks")
	}
	t.Logf("eval %s sequence=%d: CLI = shared report = structured = JSON text; hashes/privacy/unknowns and all repository/storage bytes unchanged", r.Snapshot.Progress.State, r.Snapshot.Progress.Sequence)
	return r
}

func mcpEvalError(t *testing.T, result *mcp.CallToolResult, want string) {
	t.Helper()
	if !result.IsError || result.StructuredContent != nil || len(result.Content) != 1 {
		t.Fatalf("expected evaluation error %q, got %+v", want, result)
	}
	text, ok := result.Content[0].(*mcp.TextContent)
	if !ok || text.Text == "" || !strings.Contains(text.Text, want) {
		t.Fatalf("expected evaluation error %q, got %+v", want, result.Content)
	}
}

func mcpEvalInvalidLaunch(t *testing.T, ctx context.Context, binary string, f *evalFixture) {
	t.Helper()
	storage, root := f.plan.StorageRoot, f.repo
	alias := filepath.Join(filepath.Dir(root), "storage-alias")
	mcpDirectoryLink(t, storage, alias)
	for _, args := range [][]string{
		{"--eval-storage-root"}, {"--eval-storage-root", ""}, {"--eval-storage-root", storage, "--eval-storage-root", storage},
		{"--repo", root, "--repo", root}, {"--eval-storage-root", storage, "extra"},
		{"--eval-storage-root", "relative"}, {"--eval-storage-root", filepath.Join(storage, "missing")},
		{"--eval-storage-root", filepath.Join(storage, mcpEvalID, "evaluation.json")},
		{"--eval-storage-root", storage + string(filepath.Separator) + "."},
		{"--eval-storage-root", storage + string(filepath.Separator) + ".."},
		{"--eval-storage-root", `\\server\share`}, {"--eval-storage-root", `\\?\C:\device`},
		{"--eval-storage-root", filepath.Join(storage, "con")}, {"--eval-storage-root", storage + ":stream"},
		{"--eval-storage-root", alias},
	} {
		before := mcpTree(t, filepath.Dir(root))
		cmd := mcpProcess(ctx, binary, root, append([]string{"mcp"}, args...)...)
		var stdout, stderr bytes.Buffer
		cmd.Stdout, cmd.Stderr = &stdout, &stderr
		if err := cmd.Run(); err == nil || stdout.Len() != 0 || stderr.Len() == 0 {
			t.Fatalf("unsafe evaluation launch %v: err=%v stdout=%q stderr=%q", args, err, stdout.String(), stderr.String())
		}
		if mcpTree(t, filepath.Dir(root)) != before {
			t.Fatal("invalid launch created/changed repository or storage")
		}
	}
	t.Log("explicit storage opt-in, strict run IDs/arguments, cross-repository/alias/corrupt evidence rejection and clean real-stdio shutdown verified with no executables on PATH")
}
