package run

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/kninetimmy/orch/internal/execx/execxtest"
	"github.com/kninetimmy/orch/internal/ghops"
	"github.com/kninetimmy/orch/internal/manifest"
	"github.com/kninetimmy/orch/internal/state"
)

// Exercise actual producers, persisted reloads, real git observation and the
// complete resume path, including a later generic failure on the same issue.
func TestDecisionBlocksSurviveRecovery(t *testing.T) {
	for _, kind := range []string{"architectural-ambiguity", "weak-model-failure", "human-decision", "secret", "legacy"} {
		t.Run(kind, func(t *testing.T) {
			iss := fixtureIssue("a", 1, state.PhaseDispatched)
			iss.Decision = specialistDecision()
			if kind == "legacy" {
				iss.Phase, iss.BlockedReason = state.PhaseBlocked, "old undecided reason"
			}
			root := setupDeliveryGitRepo(t, "r1", []state.Issue{iss})
			createBranchWorktree(t, root, iss.Branch)
			if kind != "legacy" {
				status := ghops.StatusBlocked
				if strings.Contains(kind, "ambiguity") || kind == "weak-model-failure" {
					status = ghops.StatusNeedsHuman
				}
				script := &execxtest.Script{T: t, Calls: []execxtest.Call{ghAuth(), ghSetStatusCall(1, status)}}
				var err error
				if status == ghops.StatusNeedsHuman {
					_, err = Escalate(context.Background(), ghEnv(root, script), []byte(`{"schema_version":2,"issue_number":1,"trigger":"`+kind+`","detail":"human decision required"}`))
				} else {
					_, err = Block(context.Background(), ghEnv(root, script), []byte(`{"schema_version":2,"issue_number":1,"class":"`+kind+`","detail":"human decision required"}`))
				}
				if err != nil {
					t.Fatal(err)
				}
				script.AssertExhausted()
			}
			assertDecisionSurvivesResume(t, root, baseManifestBody(t), false)
		})
	}
}

func assertDecisionSurvivesResume(t *testing.T, root, body string, hasPR bool) {
	t.Helper()
	original := loadRun(t, root).Run.Issues[0]
	// Even missing/closed artifacts and edited scope cannot replace a decision.
	missing := issueObservations{issueState: "CLOSED", manifestOK: pbool(false), localBranch: pbool(false), worktree: pbool(false), work: &approvedWork{objective: "edited"}}
	copyIssue := original
	applyOutcome(&copyIssue, reconcileIssue(copyIssue, missing))
	if !reflect.DeepEqual(copyIssue, original) {
		t.Fatal("unhealthy observations changed decision")
	}
	script := &execxtest.Script{T: t, Calls: []execxtest.Call{ghAuth(), ghSetStatusCall(1, ghops.StatusBlocked)}}
	if _, err := Block(context.Background(), ghEnv(root, script), []byte(`{"schema_version":2,"issue_number":1,"class":"github","detail":"later transport failure"}`)); err != nil {
		t.Fatal(err)
	}
	script.AssertExhausted()
	if got := loadRun(t, root).Run.Issues[0]; !reflect.DeepEqual(got, original) {
		t.Fatalf("generic block overwrote original decision: %+v", got)
	}
	for _, req := range []ResumeRequest{{DryRun: true}, {}, {}, {Statement: ResumeStoppedRunStatement}} {
		before := stateBytes(t, root)
		calls := []execxtest.Call{ghAuth(), ghIssueViewCall(t, 1, "OPEN", body)}
		if hasPR {
			calls = append(calls, ghPRViewCall(10, "OPEN", "head-oid-1"), ghRollupEmptyCall(10))
		} else {
			calls = append(calls, ghPRListEmptyCall("orch/issue-1"))
		}
		doc, rs := resumeMux(t, root, req, calls...)
		rs.AssertExhausted()
		if doc.Issues[0].PhaseAfter != state.PhaseBlocked || doc.Issues[0].Reason != original.BlockedReason || len(doc.Warnings) == 0 {
			t.Fatalf("decision was lost or guidance missing: %+v", doc)
		}
		if got := loadRun(t, root).Run.Issues[0]; !reflect.DeepEqual(got, original) {
			t.Fatal("resume changed decision")
		}
		if req.Statement == "" && string(stateBytes(t, root)) != string(before) {
			t.Fatal("unchanged or dry resume wrote state")
		}
	}
}

func TestResolveBlockRequiresExactDecisionAndPreservesGates(t *testing.T) {
	iss := fixtureIssue("a", 1, state.PhaseDispatched)
	iss.SetBlock(state.BlockHuman, "choose the existing approach")
	root := setupDeliveryGitRepo(t, "r1", []state.Issue{iss})
	createBranchWorktree(t, root, iss.Branch)
	st := loadRun(t, root)
	req := ResolveBlockRequest{SchemaVersion: 1, RunID: st.Run.ID, IssueNumber: 1, BlockID: 1, ResolvedBy: "human", Detail: "use the approved approach; criteria unchanged", Statement: ResolveBlockStatement}
	for _, mutate := range []func(*ResolveBlockRequest){
		func(r *ResolveBlockRequest) { r.SchemaVersion = 99 },
		func(r *ResolveBlockRequest) { r.RunID = "another-run" },
		func(r *ResolveBlockRequest) { r.IssueNumber = 2 },
		func(r *ResolveBlockRequest) { r.IssueNumber = 0 },
		func(r *ResolveBlockRequest) { r.BlockID = 2 },
		func(r *ResolveBlockRequest) { r.Statement = "change-criteria" },
		func(r *ResolveBlockRequest) { r.ResolvedBy = " " },
	} {
		bad := req
		mutate(&bad)
		before := stateBytes(t, root)
		data, _ := json.Marshal(bad)
		if _, err := ResolveBlock(context.Background(), ghEnv(root, &execxtest.Script{T: t}), data); err == nil {
			t.Fatal("accepted invalid resolution")
		}
		if string(before) != string(stateBytes(t, root)) {
			t.Fatal("invalid resolution mutated state")
		}
	}
	data, _ := json.Marshal(req)
	st.Run.StoppedReason = "separate run stop"
	if err := state.Save(root, st); err != nil {
		t.Fatal(err)
	}
	if _, err := ResolveBlock(context.Background(), ghEnv(root, &execxtest.Script{T: t}), data); !errors.Is(err, ErrRunStopped) {
		t.Fatalf("stop bypass: %v", err)
	}
	st.Run.StoppedReason = ""
	if err := state.Save(root, st); err != nil {
		t.Fatal(err)
	}
	if _, err := ResolveBlock(context.Background(), ghEnv(root, &execxtest.Script{T: t}), data); err != nil {
		t.Fatal(err)
	}
	got := loadRun(t, root).Run.Issues[0]
	if got.Phase != state.PhaseBlocked || got.Blocks[0].Resolution == nil || got.Blocks[0].Reason != iss.BlockedReason {
		t.Fatalf("resolution not recorded against original: %+v", got)
	}
	doc, script := resumeMux(t, root, ResumeRequest{}, ghAuth(), ghIssueViewCall(t, 1, "OPEN", baseManifestBody(t)), ghPRListEmptyCall(iss.Branch))
	script.AssertExhausted()
	if doc.Issues[0].PhaseAfter != state.PhaseWorktreeReady {
		t.Fatalf("resolved issue not recovered: %+v", doc)
	}
	got = loadRun(t, root).Run.Issues[0]
	if got.Blocks[0].Resolution == nil || got.ApprovedHeadOID != "" {
		t.Fatal("history lost or merge authorized")
	}
	// A new, even identical decision cannot reuse the prior resolution.
	got.SetBlock(state.BlockHuman, iss.BlockedReason)
	if !got.DecisionPending() || got.Blocks[1].ID != 2 {
		t.Fatal("resolution leaked into another block")
	}
}

func TestResolutionCannotAdoptEditedCriteria(t *testing.T) {
	iss := fixtureIssue("a", 1, state.PhaseDispatched)
	iss.SetBlock(state.BlockWrong, "criterion disputed")
	root := setupDeliveryGitRepo(t, "r1", []state.Issue{iss})
	createBranchWorktree(t, root, iss.Branch)
	req := ResolveBlockRequest{SchemaVersion: 1, RunID: loadRun(t, root).Run.ID, IssueNumber: 1, BlockID: 1, ResolvedBy: "human", Detail: "original criterion is correct", Statement: ResolveBlockStatement}
	data, _ := json.Marshal(req)
	if _, err := ResolveBlock(context.Background(), ghEnv(root, &execxtest.Script{T: t}), data); err != nil {
		t.Fatal(err)
	}
	m := baseManifest()
	m.AcceptanceCriteria = []string{"replacement criterion"}
	body, err := manifest.Upsert("", m)
	if err != nil {
		t.Fatal(err)
	}
	doc, script := resumeMux(t, root, ResumeRequest{}, ghAuth(), ghIssueViewCall(t, 1, "OPEN", body), ghPRListEmptyCall(iss.Branch))
	script.AssertExhausted()
	got := loadRun(t, root).Run.Issues[0]
	if !got.DecisionPending() || !strings.Contains(doc.Issues[0].Reason, "plan approval") || !reflect.DeepEqual(got.AcceptanceCriteria, iss.AcceptanceCriteria) {
		t.Fatalf("edited criteria escaped approval: %+v", got)
	}
}

func TestRecoveryVersionsPreserveEvidence(t *testing.T) {
	for _, version := range []int{4, 5, 99} {
		iss := fixtureIssue("a", 1, state.PhaseBlocked)
		iss.Blocks = nil
		root := setupDeliveryRepo(t, "r1", []state.Issue{iss})
		st := loadRun(t, root)
		st.SchemaVersion = version
		data, err := json.Marshal(st)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(root, filepath.FromSlash(state.Path)), data, 0o644); err != nil {
			t.Fatal(err)
		}
		env := ghEnv(root, &execxtest.Script{T: t})
		if version != 99 {
			doc, err := Status(context.Background(), env)
			if err != nil || doc.Run.Issues[0].BlockedReason != iss.BlockedReason {
				t.Fatalf("legacy inspection: %v %+v", err, doc)
			}
		}
		if _, err := Resume(context.Background(), env, ResumeRequest{}); err == nil {
			t.Fatal("unsupported recovery succeeded")
		}
		if string(data) != string(stateBytes(t, root)) {
			t.Fatal("version refusal discarded evidence")
		}
	}
	for _, version := range []int{1, 99} {
		data, _ := json.Marshal(BlockRequest{SchemaVersion: version, IssueNumber: 1, Class: "other", Detail: "x"})
		if _, err := Block(context.Background(), Env{}, data); !errors.Is(err, ErrBadRequest) {
			t.Fatalf("block version: %v", err)
		}
	}
}
