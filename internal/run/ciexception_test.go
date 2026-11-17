package run

import (
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/kninetimmy/orch/internal/execx/execxtest"
	"github.com/kninetimmy/orch/internal/manifest"
	"github.com/kninetimmy/orch/internal/state"
)

func TestManualCIAuditSurvivesBodyCapRotation(t *testing.T) {
	m := baseManifest()
	m.Verifications = []manifest.Verification{
		{Name: "ordinary-evidence", Result: "pass", Detail: strings.Repeat("x", 22000)},
		{Name: "ci-exception-1", Result: CIExceptionStatement, Detail: strings.Repeat("reason and CI evidence ", 500), CommitOID: readinessHead, At: "2026-07-11T12:00:00Z"},
		{Name: "merged-acknowledgment-2", Result: MergedAcknowledgmentStatement, Detail: strings.Repeat("reason and merged CI ", 500), CommitOID: readinessHead, At: "2026-07-11T12:00:01Z"},
	}
	body, err := upsertCapped("preserved prose\n", m)
	if err != nil {
		t.Fatal(err)
	}
	got, err := manifest.Parse(body)
	if err != nil {
		t.Fatal(err)
	}
	if len(body) > bodyCapHeadroom || got.Verifications[0].Detail != "" || !reflect.DeepEqual(got.Verifications[1:], m.Verifications[1:]) {
		t.Fatal("body-cap rotation changed/dropped manual decisions or CI evidence")
	}
	m.Verifications = append(m.Verifications, manifest.Verification{Name: "ci-exception-3", Result: CIExceptionStatement, Detail: strings.Repeat("x", bodyCapHeadroom)})
	if _, err := upsertCapped("preserved prose\n", m); !errors.Is(err, ErrBodyTooLarge) {
		t.Fatalf("oversized protected history err=%v, want refusal instead of audit loss", err)
	}
}

func TestOrdinaryMergedRetryRefusesDifferentHead(t *testing.T) {
	issue := fixtureIssue("a", 1, state.PhaseAwaitingMerge)
	issue.ApprovedHeadOID = "head-oid-2"
	root := setupDeliveryRepo(t, "r1", []state.Issue{issue})
	before := stateBytes(t, root)
	script := &execxtest.Script{T: t, Calls: []execxtest.Call{ghAuth(), ghPRViewCall(10, "MERGED", "different-head")}}
	_, err := Merge(t.Context(), ghEnv(root, script), mergeRequest(MergeApprovalStatement, "owner"))
	script.AssertExhausted()
	if !errors.Is(err, ErrMergeApproval) || string(stateBytes(t, root)) != string(before) {
		t.Fatalf("mismatched merged head adopted: err=%v", err)
	}
}

func TestMergeConfirmationRequiresPinnedHead(t *testing.T) {
	issue := fixtureIssue("a", 1, state.PhaseAwaitingMerge)
	issue.ApprovedHeadOID = "head-oid-2"
	root := setupDeliveryRepo(t, "r1", []state.Issue{issue})
	before := stateBytes(t, root)
	script := &execxtest.Script{T: t, Calls: []execxtest.Call{
		ghAuth(), ghPRViewCall(10, "OPEN", "head-oid-2"), ghRollupEmptyCall(10),
		ghMergePRCall(10, "head-oid-2"), ghPRViewCall(10, "MERGED", "different-head"),
	}}
	_, err := Merge(t.Context(), ghEnv(root, script), mergeRequest(MergeApprovalStatement, "owner"))
	script.AssertExhausted()
	if err == nil || !strings.Contains(err.Error(), "expected MERGED at approved head") || string(stateBytes(t, root)) != string(before) {
		t.Fatalf("mismatched post-merge confirmation advanced delivery: err=%v", err)
	}
}
