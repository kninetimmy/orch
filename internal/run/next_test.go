package run

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kninetimmy/orch/internal/execx/execxtest"
	"github.com/kninetimmy/orch/internal/lockfile"
	"github.com/kninetimmy/orch/internal/state"
)

// Every successful and failed inspection uses a forbidden-command runner and
// a real Claude session with metrics enabled where applicable. In particular,
// Next must never call loadVerb's session-attribution write.
func inspectNext(t *testing.T, root string) (*NextDoc, error) {
	t.Helper()
	script := &execxtest.Script{T: t}
	doc, err := Next(t.Context(), Env{RepoRoot: root, Runner: script, ClaudeSession: architectSession, Now: fixedNow})
	script.AssertExhausted()
	assertNoMetricsDir(t, root)
	return doc, err
}

func TestNextAssist(t *testing.T) {
	root := setupRepo(t, metricsEnabledConfigTOML)
	doc, err := inspectNext(t, root)
	if err != nil {
		t.Fatal(err)
	}
	if doc.SchemaVersion != 1 || doc.Mode != state.ModeAssist || doc.RunID != "" || len(doc.Actions) != 1 ||
		doc.Actions[0].Action != "plan" || doc.Actions[0].ApprovalRequired == "" || len(doc.Limitations) == 0 {
		t.Fatalf("Assist guidance = %+v", doc)
	}
	if _, err := os.Stat(filepath.Join(root, state.Path)); !os.IsNotExist(err) {
		t.Fatalf("inspection created state: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, ".orchestrator", "config.toml"), []byte("bad TOML ["), 0o644); err != nil {
		t.Fatal(err)
	}
	doc, err = inspectNext(t, root)
	if err != nil || doc.Actions[0].Action != "recover" || len(doc.Actions[0].Blockers) == 0 {
		t.Fatalf("invalid config: doc=%+v err=%v", doc, err)
	}
	// Status retains its separate config-free schema-4 inspection contract.
	status, err := Status(t.Context(), Env{RepoRoot: root})
	if err != nil || status.SchemaVersion != 4 || !status.Consistent {
		t.Fatalf("status: doc=%+v err=%v", status, err)
	}
}

func TestNextDeliveryPhases(t *testing.T) {
	for _, tc := range []struct {
		phase   state.Phase
		verdict string
		action  string
		approve bool
	}{
		{state.PhasePlanned, "", "recover", false},
		{state.PhaseIssueCreated, "", "recover", false},
		{state.PhaseWorktreeReady, "", "dispatch", false},
		{state.PhaseDispatched, "", "implement", false},
		{state.PhasePROpen, "", "review", false},
		{state.PhaseInReview, "", "review", false},
		{state.PhaseInReview, VerdictRequestChanges, "fix-and-review", false},
		{state.PhaseInReview, VerdictApprove, "merge-report", false},
		{state.PhaseAwaitingMerge, VerdictApprove, "merge", true},
		{state.PhaseMerged, VerdictApprove, "cleanup", true},
		{state.PhaseAbandoned, "", "cleanup", true},
		{state.PhaseBlocked, "", "resume", false},
		{state.PhaseCleaned, "", "complete", false},
	} {
		t.Run(string(tc.phase)+"/"+tc.verdict, func(t *testing.T) {
			issue := fixtureIssue("a", 1, tc.phase)
			issue.LastReviewVerdict = tc.verdict
			root := setupDeliveryRepoWithConfig(t, metricsEnabledConfigTOML, "r1", []state.Issue{issue})
			before := string(stateBytes(t, root))
			lockBefore, err := os.ReadFile(filepath.Join(root, lockfile.Path))
			if err != nil {
				t.Fatal(err)
			}
			doc, err := inspectNext(t, root)
			if err != nil {
				t.Fatal(err)
			}
			if doc.Mode != state.ModeDelivery || doc.RunID == "" || len(doc.Actions) != 1 {
				t.Fatalf("guidance = %+v", doc)
			}
			a := doc.Actions[0]
			if a.Action != tc.action || a.Reason == "" || len(a.Prerequisites) == 0 || (a.ApprovalRequired != "") != tc.approve {
				t.Fatalf("action = %+v, want %s approval=%t", a, tc.action, tc.approve)
			}
			if tc.action != "complete" && (a.PlanID != "a" || a.IssueNumber != 1 || a.Phase != tc.phase) {
				t.Fatalf("issue identity lost: %+v", a)
			}
			if tc.action == "merge" && (!strings.Contains(a.ApprovalRequired, MergeApprovalStatement) || !strings.Contains(a.ApprovalRequired, "PR #10 at head head-oid")) {
				t.Fatalf("merge approval is not pinned: %+v", a)
			}
			lockAfter, err := os.ReadFile(filepath.Join(root, lockfile.Path))
			if err != nil || before != string(stateBytes(t, root)) || string(lockBefore) != string(lockAfter) {
				t.Fatalf("inspection changed state/lock: %v", err)
			}
		})
	}
}

func TestNextDependenciesAndApprovedWork(t *testing.T) {
	for _, phase := range []state.Phase{state.PhaseDispatched, state.PhaseMerged, state.PhaseCleaned, state.PhaseAbandoned} {
		t.Run(string(phase), func(t *testing.T) {
			a := fixtureIssue("A", 1, phase)
			b := fixtureIssue("b", 2, state.PhaseWorktreeReady)
			b.DependsOn = []string{"a"} // Same case-insensitive rule as dispatch.
			root := setupDeliveryRepo(t, "r1", []state.Issue{a, b})
			doc, err := inspectNext(t, root)
			if err != nil {
				t.Fatal(err)
			}
			got := doc.Actions[1]
			want := "dispatch"
			switch phase {
			case state.PhaseDispatched:
				want = "wait"
			case state.PhaseAbandoned:
				want = "replan"
				if got.ApprovalRequired == "" {
					t.Fatal("abandoned dependency omitted human plan gate")
				}
			}
			if got.Action != want || (len(got.Blockers) > 0) != (want != "dispatch") {
				t.Fatalf("dependency action = %+v, want %s", got, want)
			}
		})
	}
	t.Run("all waits", func(t *testing.T) {
		a, b, c := fixtureIssue("a", 1, state.PhaseDispatched), fixtureIssue("b", 2, state.PhaseDispatched), fixtureIssue("c", 3, state.PhaseWorktreeReady)
		c.DependsOn = []string{"a", "b"}
		root := setupDeliveryRepo(t, "r1", []state.Issue{a, b, c})
		doc, err := inspectNext(t, root)
		if err != nil || len(doc.Actions[2].Blockers) != 2 || doc.Actions[2].Action != "wait" {
			t.Fatalf("guidance=%+v err=%v", doc, err)
		}
	})
	for _, phase := range []state.Phase{state.PhaseDispatched, state.PhasePROpen, state.PhaseInReview, state.PhaseAwaitingMerge} {
		t.Run("abandoned dependency after "+string(phase), func(t *testing.T) {
			a, b := fixtureIssue("a", 1, state.PhaseAbandoned), fixtureIssue("b", 2, phase)
			b.DependsOn = []string{"a"}
			root := setupDeliveryRepo(t, "r1", []state.Issue{a, b})
			doc, err := inspectNext(t, root)
			if err != nil || doc.Actions[1].Action != "replan" || doc.Actions[1].ApprovalRequired == "" {
				t.Fatalf("guidance=%+v err=%v", doc, err)
			}
		})
	}
	for _, phase := range []state.Phase{state.PhaseWorktreeReady, state.PhaseDispatched, state.PhaseInReview, state.PhaseAwaitingMerge} {
		for _, missing := range []string{"objective", "criteria", "tests", "routing"} {
			if phase != state.PhaseWorktreeReady && missing == "routing" {
				continue // state.Load already rejects this invalid shape.
			}
			t.Run(string(phase)+"/missing "+missing, func(t *testing.T) {
				issue := fixtureIssue("a", 1, phase)
				switch missing {
				case "objective":
					issue.Objective = ""
				case "criteria":
					issue.AcceptanceCriteria = nil
				case "tests":
					issue.RequiredTests = nil
				case "routing":
					issue.Decision = nil
				}
				root := setupDeliveryRepo(t, "r1", []state.Issue{issue})
				doc, err := inspectNext(t, root)
				if err != nil || doc.Actions[0].Action != "replan" || doc.Actions[0].ApprovalRequired == "" || len(doc.Actions[0].Blockers) == 0 {
					t.Fatalf("guidance=%+v err=%v", doc, err)
				}
			})
		}
	}
}

func TestNextDecisionVersusRecovery(t *testing.T) {
	for _, cause := range []string{"", state.BlockOperational, state.BlockHuman, state.BlockWrong, state.BlockArchitect, state.BlockSecret} {
		for _, resolved := range []bool{false, true} {
			if cause == "" && resolved {
				continue
			}
			t.Run(fmt.Sprintf("%s/resolved=%t", cause, resolved), func(t *testing.T) {
				issue := fixtureIssue("a", 1, state.PhaseBlocked)
				issue.Blocks = nil
				if cause != "" {
					b := state.Block{ID: 1, Cause: cause, Reason: issue.BlockedReason}
					if resolved {
						b.Resolution = &state.BlockResolution{ResolvedBy: "human", Detail: "scope unchanged", At: "2026-11-11T12:00:00Z", Statement: ResolveBlockStatement}
					}
					issue.Blocks = []state.Block{b}
				}
				root := setupDeliveryRepo(t, "r1", []state.Issue{issue})
				doc, err := inspectNext(t, root)
				if err != nil {
					t.Fatal(err)
				}
				want := "resume"
				if cause == "" {
					want = "replan"
				} else if issue.DecisionPending() {
					want = "resolve-block"
				}
				got := doc.Actions[0]
				if got.Action != want || len(got.Blockers) == 0 || (got.ApprovalRequired != "") != issue.DecisionPending() {
					t.Fatalf("action=%+v want=%s", got, want)
				}
			})
		}
	}
}

func TestNextRunBlockers(t *testing.T) {
	for _, name := range []string{"revision drift", "effective drift", "invalid config", "schema 4", "schema 5", "contract", "stop", "missing lock", "mismatched lock", "empty run"} {
		t.Run(name, func(t *testing.T) {
			root := setupDeliveryRepoWithConfig(t, metricsEnabledConfigTOML, "r1", []state.Issue{fixtureIssue("a", 1, state.PhaseCleaned)})
			st := loadRun(t, root)
			want := "restore-or-replan"
			switch name {
			case "revision drift":
				st.Run.Plan.ConfigRevision = "other"
			case "effective drift":
				if err := os.WriteFile(filepath.Join(root, ".orchestrator", "config.local.toml"), []byte("[concurrency]\nmax_subagents = 2\n"), 0o644); err != nil {
					t.Fatal(err)
				}
			case "invalid config":
				if err := os.WriteFile(filepath.Join(root, ".orchestrator", "config.toml"), []byte("invalid ["), 0o644); err != nil {
					t.Fatal(err)
				}
			case "schema 4":
				st.SchemaVersion = 4
			case "schema 5":
				st.SchemaVersion = 5
			case "contract":
				st.Run.Plan.ContractVersion = 999
			case "stop":
				st.Run.StoppedReason = "secret remediation required"
				want = "recover-stopped-run"
			case "missing lock":
				if err := os.Remove(filepath.Join(root, lockfile.Path)); err != nil {
					t.Fatal(err)
				}
				want = "recover"
			case "mismatched lock":
				st.Run.ID = "other-run"
				want = "recover"
			case "empty run":
				st.Run.Issues = nil
				want = "recover"
			}
			if err := state.Save(root, st); err != nil {
				t.Fatal(err)
			}
			before := string(stateBytes(t, root))
			doc, err := inspectNext(t, root)
			if err != nil || len(doc.Actions) != 1 || doc.Actions[0].Action != want || len(doc.Actions[0].Blockers) == 0 {
				t.Fatalf("guidance=%+v err=%v want=%s", doc, err, want)
			}
			if name == "stop" && !strings.Contains(doc.Actions[0].ApprovalRequired, ResumeStoppedRunStatement) {
				t.Fatal("stopped run omitted explicit recovery statement")
			}
			if before != string(stateBytes(t, root)) {
				t.Fatal("inspection changed state")
			}
		})
	}
	root := setupRepo(t, testConfigTOML)
	if err := lockfile.Acquire(root, lockfile.Owner{RunID: "orphan"}); err != nil {
		t.Fatal(err)
	}
	doc, err := inspectNext(t, root)
	if err != nil || doc.Actions[0].Action != "recover" || len(doc.Actions[0].Blockers) == 0 {
		t.Fatalf("orphaned lock: guidance=%+v err=%v", doc, err)
	}
}

func TestNextCorruptOrUnsupportedStateFails(t *testing.T) {
	for _, name := range []string{"state JSON", "lock JSON", "state schema", "lock schema", "mode", "phase", "block cause", "missing plan id", "duplicate plan id", "duplicate number", "missing dependency"} {
		t.Run(name, func(t *testing.T) {
			root := setupDeliveryRepo(t, "r1", []state.Issue{fixtureIssue("a", 1, state.PhaseBlocked)})
			st := loadRun(t, root)
			path := filepath.Join(root, state.Path)
			data := []byte("{broken")
			switch name {
			case "lock JSON":
				path = filepath.Join(root, lockfile.Path)
			case "lock schema":
				path = filepath.Join(root, lockfile.Path)
				data = []byte(`{"schema_version":999}`)
			case "state schema":
				st.SchemaVersion = 999
			case "mode":
				st.Mode = "future-mode"
			case "phase":
				st.Run.Issues[0].Phase = "future-phase"
			case "block cause":
				st.Run.Issues[0].Blocks[0].Cause = "future-cause"
			case "missing plan id":
				st.Run.Issues[0].PlanID = ""
			case "duplicate plan id":
				st.Run.Issues = append(st.Run.Issues, fixtureIssue("A", 2, state.PhaseWorktreeReady))
			case "duplicate number":
				st.Run.Issues = append(st.Run.Issues, fixtureIssue("b", 1, state.PhaseWorktreeReady))
			case "missing dependency":
				st.Run.Issues[0].DependsOn = []string{"missing"}
			}
			if name != "state JSON" && name != "lock JSON" && name != "lock schema" {
				var err error
				data, err = json.Marshal(st)
				if err != nil {
					t.Fatal(err)
				}
			}
			if err := os.WriteFile(path, data, 0o644); err != nil {
				t.Fatal(err)
			}
			doc, err := inspectNext(t, root)
			if err == nil || doc != nil {
				t.Fatalf("guidance=%+v err=%v", doc, err)
			}
			got, err := os.ReadFile(path)
			if err != nil || string(got) != string(data) {
				t.Fatalf("inspection changed corrupt state: %v", err)
			}
		})
	}
}
