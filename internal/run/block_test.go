package run

import (
	"context"
	"reflect"
	"strings"
	"testing"

	"github.com/kninetimmy/orch/internal/execx/execxtest"
	"github.com/kninetimmy/orch/internal/ghops"
	"github.com/kninetimmy/orch/internal/state"
)

func TestBlockRefusesUnrecordedHumanDecision(t *testing.T) {
	const detail = "clarify the next step"
	for _, tc := range []struct {
		name, firstClass, firstDetail string
	}{
		{"different-detail", "human-decision", "address the original question"},
		{"validation-same-detail", "validation", detail},
		{"other-same-detail", "other", detail},
		{"secret", "secret", "credential exposure suspected"},
		{"legacy", "", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			iss := fixtureIssue("a", 1, state.PhaseDispatched)
			if tc.firstClass == "" {
				iss.Phase, iss.BlockedReason = state.PhaseBlocked, "unknown original cause"
			}
			root := setupDeliveryRepoWithConfig(t, metricsEnabledConfigTOML, "r1", []state.Issue{iss})
			if tc.firstClass != "" {
				first := BlockRequest{SchemaVersion: 2, IssueNumber: 1, Class: tc.firstClass, Detail: tc.firstDetail}
				runVerb(t, root, Block, string(requestBytes(t, first)), ghAuth(), ghSetStatusCall(1, ghops.StatusBlocked))
			}
			beforeState, beforeMetrics := stateBytes(t, root), loadMetricsDocs(t, root)

			// Allow GitHub calls to succeed so the old false success is observable.
			auth := ghAuth()
			auth.CheckStdin = func(t *testing.T, _ string) { t.Error("refusal contacted GitHub") }
			env := ghEnv(root, &execxtest.Script{T: t, Calls: []execxtest.Call{auth, ghSetStatusCall(1, ghops.StatusBlocked)}})
			env.ClaudeSession = architectSession
			req := BlockRequest{SchemaVersion: 2, IssueNumber: 1, Class: "human-decision", Detail: detail}
			got, err := Block(context.Background(), env, requestBytes(t, req))
			if err == nil || got != nil {
				t.Errorf("Block = %+v, %v; want refusal (persisted blocks: %+v)", got, err, loadRun(t, root).Run.Issues[0].Blocks)
			} else {
				if !strings.Contains(err.Error(), "not recorded") || !strings.Contains(err.Error(), "must be addressed first") {
					t.Errorf("refusal did not explain what to do: %v", err)
				}
				if tc.firstClass == "" && !strings.Contains(err.Error(), "fresh plan approval") {
					t.Errorf("legacy refusal did not direct the caller back to plan approval: %v", err)
				}
			}
			if string(stateBytes(t, root)) != string(beforeState) {
				t.Error("refusal changed run state")
			}
			if !reflect.DeepEqual(loadMetricsDocs(t, root), beforeMetrics) {
				t.Error("refusal changed metrics")
			}
		})
	}
}

func TestBlockRetryAndNewDecisionAfterResolution(t *testing.T) {
	root := setupDeliveryRepoWithConfig(t, metricsEnabledConfigTOML, "r1", []state.Issue{fixtureIssue("a", 1, state.PhaseDispatched)})
	const firstJSON = `{"schema_version":2,"issue_number":1,"class":"human-decision","detail":"address the original question"}`
	first := runVerb(t, root, Block, firstJSON, ghAuth(), ghSetStatusCall(1, ghops.StatusBlocked))
	before := loadRun(t, root).Run.Issues[0]

	script := &execxtest.Script{T: t, Calls: []execxtest.Call{ghAuth(), ghSetStatusCall(1, ghops.StatusBlocked)}}
	env := ghEnv(root, script)
	env.ClaudeSession = architectSession
	retry, err := Block(context.Background(), env, []byte(firstJSON))
	if err != nil || retry == nil || *retry != *first {
		t.Fatalf("exact retry = %+v, %v; want original result %+v", retry, err, first)
	}
	script.AssertExhausted()
	if !reflect.DeepEqual(loadRun(t, root).Run.Issues[0], before) {
		t.Fatal("exact retry changed the recorded decision")
	}
	docs := loadMetricsDocs(t, root)
	if len(docs) != 1 || !reflect.DeepEqual(docs[0].ArchitectSessions, []string{architectSession}) {
		t.Fatal("accepted retry lost normal session attribution")
	}

	resolve := ResolveBlockRequest{SchemaVersion: 1, RunID: loadRun(t, root).Run.ID, IssueNumber: 1, BlockID: 1,
		ResolvedBy: "human", Detail: "continue with the approved approach", Statement: ResolveBlockStatement}
	resolveJSON := requestBytes(t, resolve)
	runVerb(t, root, ResolveBlock, string(resolveJSON))
	resolved := loadRun(t, root).Run.Issues[0]
	if len(resolved.Blocks) != 1 || resolved.Blocks[0].Resolution == nil {
		t.Fatalf("original decision was not resolved: %+v", resolved.Blocks)
	}

	const secondJSON = `{"schema_version":2,"issue_number":1,"class":"human-decision","detail":"clarify the next step"}`
	runVerb(t, root, Block, secondJSON, ghAuth(), ghSetStatusCall(1, ghops.StatusBlocked))
	got := loadRun(t, root).Run.Issues[0]
	want := state.Block{ID: 2, Cause: state.BlockHuman, Reason: "human-decision: clarify the next step"}
	if !got.DecisionPending() || len(got.Blocks) != 2 || !reflect.DeepEqual(got.Blocks[0], resolved.Blocks[0]) || got.Blocks[1] != want {
		t.Fatalf("want old resolution and new pending decision; got %+v", got.Blocks)
	}
	if _, err := ResolveBlock(context.Background(), ghEnv(root, &execxtest.Script{T: t}), resolveJSON); err == nil {
		t.Fatal("the old resolution replay was accepted for a new block")
	}
	if !reflect.DeepEqual(loadRun(t, root).Run.Issues[0], got) {
		t.Fatal("the old resolution replay changed the new block")
	}
}

func TestOtherBlockClassesKeepPendingDecision(t *testing.T) {
	for _, class := range []string{"hook", "auth", "github", "validation", "other", "secret"} {
		t.Run(class, func(t *testing.T) {
			root := setupDeliveryRepo(t, "r1", []state.Issue{fixtureIssue("a", 1, state.PhaseDispatched)})
			runVerb(t, root, Block, `{"schema_version":2,"issue_number":1,"class":"human-decision","detail":"keep the original decision"}`, ghAuth(), ghSetStatusCall(1, ghops.StatusBlocked))
			before := loadRun(t, root).Run.Issues[0]
			req := BlockRequest{SchemaVersion: 2, IssueNumber: 1, Class: class, Detail: "later failure"}
			result := runVerb(t, root, Block, string(requestBytes(t, req)), ghAuth(), ghSetStatusCall(1, ghops.StatusBlocked))
			got := loadRun(t, root).Run
			if !reflect.DeepEqual(got.Issues[0], before) {
				t.Fatalf("%s overwrote the pending issue decision: %+v", class, got.Issues[0].Blocks)
			}
			wantStop := class == "secret"
			if result.RunStopped != wantStop || (got.StoppedReason != "") != wantStop {
				t.Fatalf("%s: run_stopped = %v, stopped reason = %q", class, result.RunStopped, got.StoppedReason)
			}
		})
	}
}
