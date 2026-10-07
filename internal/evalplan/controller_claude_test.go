package evalplan

import (
	"bufio"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/kninetimmy/orch/internal/config"
	"github.com/kninetimmy/orch/internal/evalcorpus"
	"github.com/kninetimmy/orch/internal/nativehost"
)

// claudeScenario points the scripted Claude Code at a scenario through its
// config home, which the worker passes to the child and protects as Claude's own.
func claudeScenario(t *testing.T, scenario string) string {
	t.Helper()
	home, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	writeFixture(t, filepath.Join(home, "fixture-scenario"), []byte(scenario))
	t.Setenv("CLAUDE_CONFIG_DIR", home)
	return home
}

func claudeLaunches(t *testing.T, home string) [][]string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(home, "launches.jsonl"))
	if err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	var launches [][]string
	scanner := bufio.NewScanner(strings.NewReader(string(data)))
	for scanner.Scan() {
		var l struct{ Args []string }
		if err := json.Unmarshal(scanner.Bytes(), &l); err != nil {
			t.Fatal(err)
		}
		if l.Args[0] == "--print" {
			launches = append(launches, l.Args)
		}
	}
	return launches
}

func claudeWorkerFixture(t *testing.T) (workerRequest, claudeWorker) {
	t.Helper()
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	layout := nativehost.IsolationPaths{Workspace: filepath.Join(base, "packet"), Scratch: filepath.Join(base, "scratch"), MainCheckout: filepath.Join(base, "main"), ControllerState: filepath.Join(base, "controller"), SiblingWorkspaces: []string{filepath.Join(base, "earlier-private-evidence")}, CredentialPaths: []string{filepath.Join(base, "credentials")}}
	for _, dir := range append([]string{layout.Workspace, layout.Scratch, layout.MainCheckout, layout.ControllerState}, layout.SiblingWorkspaces...) {
		if err := os.Mkdir(dir, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	profileBytes, err := os.ReadFile("../config/testdata/valid/full.toml")
	if err != nil {
		t.Fatal(err)
	}
	cfg, err := config.Parse(profileBytes)
	if err != nil {
		t.Fatal(err)
	}
	profiles, err := configuration(cfg)
	if err != nil {
		t.Fatal(err)
	}
	instructions, protected := []Artifact{}, layout.SiblingWorkspaces
	e := &Evaluation{ID: "eval-aaaaaaaaaaaaaaaaaaaaaaaaaa", Preparation: Record{PlanDigest: "sha256:" + strings.Repeat("a", 64), Plan: Plan{Version: 3, Host: "claude", Instructions: &instructions, ProtectedRoots: &protected,
		Baseline: PinnedSelection{Selection: Selection{OrchRevision: strings.Repeat("a", 40), Profile: Artifact{SHA256: evalcorpus.Digest(profileBytes)}}, Configuration: profiles},
		Cases:    []PublicCase{{Role: "implementation"}}}}}
	unit := Unit{Ordinal: 1, CaseID: "public-case", CaseVersion: 1, Repetition: 1, Side: "baseline"}
	source := caseSource{definition: evalcorpus.Case{Role: "implementation"}, public: map[string][]byte{"TASK.md": []byte("Declared synthetic public task."), "ROLE.md": []byte("Declared synthetic public role.")}}
	for name, bytes := range source.public {
		writeFixture(t, filepath.Join(layout.Workspace, name), bytes)
	}
	record := AttemptRecord{Unit: unit, Number: 1, Kind: "initial", CaseSHA256: strings.Repeat("b", 64), PacketSHA256: strings.Repeat("c", 64)}
	task, err := evaluationTask(e, record, source, layout)
	if err != nil {
		t.Fatal(err)
	}
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	request := workerRequest{Unit: unit, Role: "implementation", Layout: layout, Task: task, PlanVersion: 3, Host: "claude", Intervention: "none", Revisions: []string{e.Preparation.Plan.Baseline.OrchRevision}}
	return request, claudeWorker{executable: executable, revision: e.Preparation.Plan.Baseline.OrchRevision}
}

func TestClaudeWorkerOutcomes(t *testing.T) {
	for _, test := range []struct {
		scenario, outcome string
		timeout           time.Duration
	}{
		{"complete", "native-completed", 20 * time.Second},
		{"model-init", "safety-failure", 20 * time.Second},
		{"model-usage", "safety-failure", 20 * time.Second},
		{"extra-tool", "refused", 20 * time.Second},
		{"apikey", "refused", 20 * time.Second},
		{"missing-flag", "refused", 20 * time.Second},
		{"slow", "timeout", 2 * time.Second},
		{"disconnect", "disconnected", 20 * time.Second},
	} {
		t.Run(test.scenario, func(t *testing.T) {
			claudeScenario(t, test.scenario)
			request, worker := claudeWorkerFixture(t)
			ctx, cancel := context.WithTimeout(t.Context(), test.timeout)
			defer cancel()
			result, err := worker.execute(ctx, request)
			if result.Outcome != test.outcome || result.Native == nil || result.Native.Requested.Model != "claude-sonnet-5" || result.Native.Requested.Effort != "xhigh" {
				t.Fatalf("outcome %+v %v", result, err)
			}
			if (result.Output != nil) != (test.outcome == "native-completed") {
				t.Fatalf("output accepted from a %s attempt", test.outcome)
			}
			if test.outcome == "safety-failure" && (result.Native.Observed == nil || result.Native.Observed.Model != "claude-substitute-model") {
				t.Fatalf("observed model not recorded: %+v", result.Native.Observed)
			}
			if test.scenario == "slow" {
				c := result.Native.Cleanup
				if !c.InterruptionAsked || c.InterruptAcknowledged == nil || !*c.InterruptAcknowledged || c.ShutdownObserved == nil || !*c.ShutdownObserved {
					t.Fatalf("interrupt evidence %+v", c)
				}
			}
		})
	}
	claudeScenario(t, "complete")
	request, worker := claudeWorkerFixture(t)
	for _, change := range []func(*workerRequest){
		func(r *workerRequest) { r.Host = "codex" },
		func(r *workerRequest) { r.Unit.CaseVersion++ },
		func(r *workerRequest) { r.Revisions = append(r.Revisions, strings.Repeat("0", 40)) },
	} {
		bad := request
		change(&bad)
		if result, _ := worker.execute(t.Context(), bad); result.Outcome != "refused" && result.Outcome != "safety-failure" {
			t.Fatalf("mismatched request admitted: %+v", result)
		}
	}
}

func TestClaudeEvaluationControllerRetainsExecution(t *testing.T) {
	if controllerTestProcess(t) {
		return
	}
	home := claudeScenario(t, "complete")
	f := newControllerFixture(t, "screen", 1)
	instructions, protected := []Artifact{}, []string{}
	f.proposal.Version, f.proposal.Host, f.proposal.Instructions, f.proposal.ProtectedRoots = 3, "claude", &instructions, &protected
	f.preview(t)
	e, err := PrepareApproved(t.Context(), f.repo, f.root, f.record.PlanDigest, Approval{1, f.record.PlanDigest, "test-human", now(), ApprovalStatement})
	if err != nil {
		t.Fatal(err)
	}
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	worker := claudeWorker{executable: executable, revision: e.Preparation.Plan.Baseline.OrchRevision}
	// A Claude worker never runs a Codex plan; nothing is claimed.
	codexPlan := newControllerFixture(t, "screen", 1)
	codexPlan.proposal.Version, codexPlan.proposal.Host, codexPlan.proposal.Instructions, codexPlan.proposal.ProtectedRoots = 3, "codex", &instructions, &protected
	codexPlan.preview(t)
	codex, err := PrepareApproved(t.Context(), codexPlan.repo, codexPlan.root, codexPlan.record.PlanDigest, Approval{1, codexPlan.record.PlanDigest, "test-human", now(), ApprovalStatement})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := run(t.Context(), codexPlan.root, codex.ID, worker); err == nil || !strings.Contains(err.Error(), "the claude worker runs only claude plans") {
		t.Fatalf("Claude worker admitted a codex plan: %v", err)
	}
	p, err := run(t.Context(), f.root, e.ID, worker)
	if err != nil || p.State != "completed" {
		t.Fatalf("claude frozen schedule did not finish: %+v %v", p, err)
	}
	launches := claudeLaunches(t, home)
	if len(launches) != 4 {
		t.Fatalf("want one Claude Code launch per attempt, got %d", len(launches))
	}
	r, err := RetainReport(f.root, e.ID)
	if err != nil {
		t.Fatal(err)
	}
	uuid := regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)
	sessions := []string{}
	for _, a := range r.Snapshot.Attempts {
		n := a.Native
		if n == nil || a.ExecutionSource != "claude-native-evaluation" || a.Outcome != "native-completed" || a.Grade != "unknown" || !uuid.MatchString(n.SessionID) || n.ThreadID != n.SessionID ||
			a.Cleanup.NativeAcknowledged == nil || !*a.Cleanup.NativeAcknowledged || n.Instructions == nil || n.Instructions.Status != "none-declared" {
			t.Fatalf("retained claude evidence: %+v", a)
		}
		sessions = append(sessions, n.SessionID)
		for _, o := range n.Observations {
			if o.Host != "claude" || o.Session != n.SessionID {
				t.Fatalf("observation %+v", o)
			}
		}
		// The stand-in leaves an empty "claude" directory in the scratch, as
		// Claude Code does; an attempt that changed nothing else is removed clean.
		if want := map[bool]string{true: "preserved-dirty", false: "removed-clean"}[n.Binding.Identity.Role == "implementer"]; a.Cleanup.Status != want {
			t.Fatalf("%s cleanup %s: %s", n.Binding.Identity.Role, a.Cleanup.Status, a.Cleanup.Detail)
		}
	}
	for _, args := range launches {
		if i := slices.Index(args, "--session-id"); i < 0 || !slices.Contains(sessions, args[i+1]) || slices.Contains(args, "--resume") {
			t.Fatalf("launch args %v", args)
		}
	}
	for _, v := range r.Snapshot.Values {
		if v.Counter == "total_tokens" {
			t.Fatalf("unreported total synthesized: %+v", v)
		}
	}
	retained := readAttempt(t, f, e, 1, 1)
	if err := validateAttemptNative(&retained, e); err != nil {
		t.Fatalf("retained claude attempt invalid: %v", err)
	}
	for name, change := range map[string]func(*AttemptRecord){
		"observation-session": func(a *AttemptRecord) { a.Native.Observations[0].Session = "00000000-0000-4000-8000-000000000000" },
		"observation-profile": func(a *AttemptRecord) { a.Native.Observations[0].Requested.Model = "claude-other" },
		"observation-host":    func(a *AttemptRecord) { a.Native.Observations[0].Host = "codex" },
		"second-session":      func(a *AttemptRecord) { a.Native.SessionID = "00000000-0000-4000-8000-000000000000" },
	} {
		bad := readAttempt(t, f, e, 1, 1)
		change(&bad)
		if err := validateAttemptNative(&bad, e); err == nil {
			t.Fatalf("%s accepted", name)
		}
	}
}

func TestClaudeEvaluationNeverResumes(t *testing.T) {
	if controllerTestProcess(t) {
		return
	}
	home := claudeScenario(t, "disconnect")
	f := newControllerFixture(t, "screen", 1)
	instructions, protected := []Artifact{}, []string{}
	f.proposal.Version, f.proposal.Host, f.proposal.Instructions, f.proposal.ProtectedRoots = 3, "claude", &instructions, &protected
	f.preview(t)
	e, err := PrepareApproved(t.Context(), f.repo, f.root, f.record.PlanDigest, Approval{1, f.record.PlanDigest, "test-human", now(), ApprovalStatement})
	if err != nil {
		t.Fatal(err)
	}
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	p, err := run(t.Context(), f.root, e.ID, claudeWorker{executable: executable, revision: e.Preparation.Plan.Baseline.OrchRevision})
	if err != nil || p.State != "incomplete" || p.Slots[0].Status != "disconnected" || p.Slots[1].Status != "unrun" {
		t.Fatalf("disconnected attempt: %+v %v", p, err)
	}
	if launches := claudeLaunches(t, home); len(launches) != 1 || slices.Contains(launches[0], "--resume") {
		t.Fatalf("evaluation resumed or retried a disconnected session: %v", launches)
	}
}
