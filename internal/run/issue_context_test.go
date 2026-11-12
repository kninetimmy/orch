package run

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/kninetimmy/orch/internal/execx/execxtest"
	"github.com/kninetimmy/orch/internal/lockfile"
	"github.com/kninetimmy/orch/internal/manifest"
	"github.com/kninetimmy/orch/internal/state"
)

func inspectIssueContext(t *testing.T, root string, number int) (*IssueContextDoc, error) {
	t.Helper()
	before := readinessTree(t, root)
	script := &execxtest.Script{T: t}
	doc, err := IssueContext(t.Context(), Env{RepoRoot: root, Runner: script, ClaudeSession: architectSession, Now: fixedNow}, number)
	script.AssertExhausted()
	if readinessTree(t, root) != before {
		t.Fatal("issue context changed repository names, kinds, modes, modification times or bytes")
	}
	assertNoMetricsDir(t, root)
	return doc, err
}

func assertIssueContextSources(t *testing.T, root string, doc *IssueContextDoc) {
	t.Helper()
	status, err := Status(t.Context(), Env{RepoRoot: root})
	if err != nil {
		t.Fatal(err)
	}
	if doc.SchemaVersion != 1 || doc.Mode != status.Mode || doc.Consistent != status.Consistent ||
		doc.Warning != status.Warning || !reflect.DeepEqual(doc.Lock, status.Lock) || doc.Run.ID != status.Run.ID ||
		doc.Run.Host != status.Run.Host || doc.Run.StartedAt != status.Run.StartedAt || doc.Run.Plan != status.Run.Plan ||
		doc.Run.StoppedReason != status.Run.StoppedReason || len(doc.Limitations) == 0 {
		t.Fatalf("context lost status metadata: context=%+v status=%+v", doc, status)
	}
	for _, issue := range status.Run.Issues {
		if issue.Number == doc.Issue.Number && !reflect.DeepEqual(issue, doc.Issue) {
			t.Fatalf("recorded issue changed: context=%+v status=%+v", doc.Issue, issue)
		}
	}
	nextDoc, err := inspectNext(t, root)
	if err != nil {
		t.Fatal(err)
	}
	actions := []NextAction{}
	for _, action := range nextDoc.Actions {
		if action.IssueNumber == 0 && action.PlanID == "" || action.IssueNumber == doc.Issue.Number {
			actions = append(actions, action)
		}
	}
	nextDoc.Actions = actions
	if !reflect.DeepEqual(doc.Guidance, nextDoc) {
		t.Fatalf("context invented guidance: context=%+v next=%+v", doc.Guidance, nextDoc)
	}
}

func TestIssueContextRecordedWorkAndDependencies(t *testing.T) {
	for _, phase := range []state.Phase{state.PhaseDispatched, state.PhaseMerged, state.PhaseCleaned, state.PhaseAbandoned} {
		t.Run(string(phase), func(t *testing.T) {
			a, b, c := fixtureIssue("A", 1, phase), fixtureIssue("b", 2, state.PhaseWorktreeReady), fixtureIssue("unrelated", 3, state.PhaseDispatched)
			b.DependsOn, b.Wave = []string{"a"}, 2
			b.Objective = "Keep exact approved text: café / 日本語."
			b.RequiredTests = []string{"go test ./...", "manual approved check"}
			b.TestsCIDoesNotRun = []string{"manual approved check"}
			b.Decision = &state.Decision{Role: manifest.RoleSpecialist,
				Executor: manifest.OpenCodeSelection("current/executor", "max"),
				Reviewer: manifest.OpenCodeSelection("current/reviewer", ""), Rationale: "escalated"}
			b.Attempts = []state.Attempt{{Role: manifest.RoleImplementer, Selection: manifest.OpenCodeSelection("prior/executor", "fast"), Failed: true, Reason: "requires specialist"}}
			unnumbered := state.Issue{PlanID: "uncreated", Phase: state.PhasePlanned}
			root := setupDeliveryRepoWithConfig(t, metricsEnabledConfigTOML, "r1", []state.Issue{a, b, c, unnumbered})
			doc, err := inspectIssueContext(t, root, 2)
			if err != nil {
				t.Fatal(err)
			}
			assertIssueContextSources(t, root, doc)
			want := "dispatch"
			switch phase {
			case state.PhaseDispatched:
				want = "wait"
			case state.PhaseAbandoned:
				want = "replan"
			}
			if !reflect.DeepEqual(doc.Issue, b) || !reflect.DeepEqual(doc.Dependencies, []state.Issue{a}) ||
				len(doc.Guidance.Actions) != 1 || doc.Guidance.Actions[0].Action != want {
				t.Fatalf("recorded context = %+v; want %s", doc, want)
			}
		})
	}
}

func TestIssueContextPhasesAndMissingWork(t *testing.T) {
	for _, phase := range []state.Phase{state.PhasePlanned, state.PhaseIssueCreated, state.PhaseDispatched, state.PhasePROpen,
		state.PhaseInReview, state.PhaseAwaitingMerge, state.PhaseMerged, state.PhaseAbandoned, state.PhaseCleaned} {
		t.Run(string(phase), func(t *testing.T) {
			root := setupDeliveryRepoWithConfig(t, metricsEnabledConfigTOML, "r1", []state.Issue{fixtureIssue("a", 1, phase)})
			doc, err := inspectIssueContext(t, root, 1)
			if err != nil {
				t.Fatal(err)
			}
			assertIssueContextSources(t, root, doc)
			if doc.Issue.TestsCIDoesNotRun != nil || len(doc.Dependencies) != 0 || len(doc.Guidance.Actions) != 1 {
				t.Fatalf("invented declarations/dependencies or lost guidance: %+v", doc)
			}
		})
	}
	for _, missing := range []string{"objective", "criteria", "tests", "routing"} {
		t.Run("missing "+missing, func(t *testing.T) {
			issue := fixtureIssue("a", 1, state.PhaseWorktreeReady)
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
			root := setupDeliveryRepoWithConfig(t, metricsEnabledConfigTOML, "r1", []state.Issue{issue})
			doc, err := inspectIssueContext(t, root, 1)
			if err != nil {
				t.Fatal(err)
			}
			assertIssueContextSources(t, root, doc)
			if !reflect.DeepEqual(doc.Issue, issue) || doc.Guidance.Actions[0].Action != "replan" || doc.Guidance.Actions[0].ApprovalRequired == "" {
				t.Fatalf("missing %s fabricated or made actionable: %+v", missing, doc)
			}
		})
	}
}

func TestIssueContextBlocks(t *testing.T) {
	for _, cause := range []string{"", state.BlockOperational, state.BlockHuman, state.BlockWrong, state.BlockArchitect, state.BlockSecret} {
		t.Run(cause, func(t *testing.T) {
			issue := fixtureIssue("a", 1, state.PhaseBlocked)
			issue.Blocks = nil
			if cause != "" {
				issue.Blocks = []state.Block{{ID: 1, Cause: cause, Reason: issue.BlockedReason}}
			}
			root := setupDeliveryRepoWithConfig(t, metricsEnabledConfigTOML, "r1", []state.Issue{issue})
			doc, err := inspectIssueContext(t, root, 1)
			if err != nil {
				t.Fatal(err)
			}
			assertIssueContextSources(t, root, doc)
			if !reflect.DeepEqual(doc.Issue.Blocks, issue.Blocks) || len(doc.Guidance.Actions[0].Blockers) == 0 {
				t.Fatalf("lost unresolved block: %+v", doc)
			}
			if cause != "" && cause != state.BlockOperational {
				st := loadRun(t, root)
				st.Run.Issues[0].Blocks[0].Resolution = &state.BlockResolution{ResolvedBy: "human", Detail: "scope unchanged", At: "2026-11-11T12:00:00Z", Statement: ResolveBlockStatement}
				if err := state.Save(root, st); err != nil {
					t.Fatal(err)
				}
				doc, err = inspectIssueContext(t, root, 1)
				if err != nil {
					t.Fatal(err)
				}
				assertIssueContextSources(t, root, doc)
				if doc.Issue.Blocks[0].Resolution == nil || doc.Guidance.Actions[0].Action != "resume" {
					t.Fatalf("resolution lost or treated as recovery: %+v", doc)
				}
			}
		})
	}
}

func TestIssueContextRunBlockers(t *testing.T) {
	for _, name := range []string{"revision drift", "effective drift", "invalid config", "schema 4", "schema 5", "contract drift", "stopped", "missing lock", "mismatched lock", "all cleaned"} {
		t.Run(name, func(t *testing.T) {
			root := setupDeliveryRepoWithConfig(t, metricsEnabledConfigTOML, "r1", []state.Issue{fixtureIssue("a", 1, state.PhaseCleaned)})
			st := loadRun(t, root)
			want := "restore-or-replan"
			switch name {
			case "revision drift":
				st.Run.Plan.ConfigRevision = "changed"
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
			case "contract drift":
				st.Run.Plan.ContractVersion = 999
			case "stopped":
				st.Run.StoppedReason = "secret remediation"
				want = "recover-stopped-run"
			case "missing lock":
				if err := os.Remove(filepath.Join(root, lockfile.Path)); err != nil {
					t.Fatal(err)
				}
				want = "recover"
			case "mismatched lock":
				st.Run.ID = "other-run"
				want = "recover"
			case "all cleaned":
				want = "complete"
			}
			if err := state.Save(root, st); err != nil {
				t.Fatal(err)
			}
			doc, err := inspectIssueContext(t, root, 1)
			if err != nil {
				t.Fatal(err)
			}
			assertIssueContextSources(t, root, doc)
			if len(doc.Guidance.Actions) != 1 || doc.Guidance.Actions[0].Action != want || doc.Guidance.Actions[0].IssueNumber != 0 {
				t.Fatalf("lost run-level advice: %+v; want %s", doc, want)
			}
		})
	}
}

func TestIssueContextErrors(t *testing.T) {
	root := setupRepo(t, metricsEnabledConfigTOML)
	for _, number := range []int{0, -1, 1} {
		doc, err := inspectIssueContext(t, root, number)
		want := ErrNoDeliveryRun
		if number <= 0 {
			want = ErrBadRequest
		}
		if doc != nil || !errors.Is(err, want) {
			t.Fatalf("issue %d: doc=%+v err=%v; want %v", number, doc, err, want)
		}
	}
	for _, name := range []string{"unknown", "empty run", "duplicate number", "duplicate plan id", "missing plan id", "missing dependency", "corrupt state", "corrupt lock", "unsupported state", "unreadable state"} {
		t.Run(name, func(t *testing.T) {
			root := setupDeliveryRepoWithConfig(t, metricsEnabledConfigTOML, "r1", []state.Issue{fixtureIssue("a", 1, state.PhaseWorktreeReady)})
			st := loadRun(t, root)
			number, path := 1, filepath.Join(root, state.Path)
			switch name {
			case "unknown":
				number = 99
			case "empty run":
				st.Run.Issues = nil
			case "duplicate number":
				st.Run.Issues = append(st.Run.Issues, fixtureIssue("b", 1, state.PhaseWorktreeReady))
				st.Run.StoppedReason = "even a stopped run cannot disambiguate this number"
			case "duplicate plan id":
				st.Run.Issues = append(st.Run.Issues, fixtureIssue("A", 2, state.PhaseWorktreeReady))
			case "missing plan id":
				st.Run.Issues[0].PlanID = ""
			case "missing dependency":
				st.Run.Issues[0].DependsOn = []string{"missing"}
			case "corrupt lock":
				path = filepath.Join(root, lockfile.Path)
			case "unsupported state":
				st.SchemaVersion = 999
			}
			data, err := json.Marshal(st)
			if err != nil {
				t.Fatal(err)
			}
			if strings.HasPrefix(name, "corrupt") {
				data = []byte("{broken")
			}
			if err := os.WriteFile(path, data, 0o644); err != nil {
				t.Fatal(err)
			}
			if name == "unreadable state" {
				if err := os.Remove(path); err != nil {
					t.Fatal(err)
				}
				if err := os.Mkdir(path, 0o755); err != nil {
					t.Fatal(err)
				}
			}
			doc, err := inspectIssueContext(t, root, number)
			if err == nil || doc != nil {
				t.Fatalf("%s produced context: %+v err=%v", name, doc, err)
			}
			if name == "duplicate number" || name == "unknown" || name == "empty run" {
				if !errors.Is(err, ErrUnknownIssue) {
					t.Fatalf("identity error = %v", err)
				}
			}
		})
	}
}

func TestIssueContextGuidanceUsesStatusSnapshot(t *testing.T) {
	root := setupDeliveryRepoWithConfig(t, metricsEnabledConfigTOML, "r1", []state.Issue{fixtureIssue("a", 1, state.PhaseDispatched)})
	st, status, err := inspectStatus(Env{RepoRoot: root})
	if err != nil {
		t.Fatal(err)
	}
	changed := loadRun(t, root)
	changed.Run.ID = "replacement-run"
	changed.Run.Issues[0].Phase = state.PhaseWorktreeReady
	changed.Run.Issues[0].Decision = specialistDecision()
	if err := state.Save(root, changed); err != nil {
		t.Fatal(err)
	}
	before := readinessTree(t, root)
	doc, err := next(Env{RepoRoot: root}, st, status)
	if err != nil || doc.RunID != status.Run.ID || doc.Actions[0].Phase != status.Run.Issues[0].Phase || doc.Actions[0].Action != "implement" ||
		status.Run.Issues[0].Decision.Role != manifest.RoleImplementer || readinessTree(t, root) != before {
		t.Fatalf("guidance reloaded a different run/phase/routing: status=%+v next=%+v err=%v", status, doc, err)
	}
}
