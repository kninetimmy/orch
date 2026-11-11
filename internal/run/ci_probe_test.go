package run

import (
	"strings"
	"testing"

	"github.com/kninetimmy/orch/internal/execx"
	"github.com/kninetimmy/orch/internal/execx/execxtest"
	"github.com/kninetimmy/orch/internal/state"
)

// All lifecycle consumers must stop on the same failed probe, before audit,
// phase, pin, metrics, merge or grant-spend mutations. Readiness is covered by
// TestReadinessCIAndProbeFailures and the real CLI/MCP stdio regression.
func TestRequiredCIProbeFailureStopsLifecycle(t *testing.T) {
	for _, name := range []string{"ci", "merge-report", "human merge", "grant merge", "resume pr-open", "resume in-review"} {
		t.Run(name, func(t *testing.T) {
			issue := fixtureIssue("a", 1, state.PhaseInReview)
			issue.LastReviewVerdict, issue.ReviewCycles = VerdictApprove, 1
			if name == "human merge" || name == "grant merge" {
				issue.Phase, issue.ApprovedHeadOID = state.PhaseAwaitingMerge, "head-oid-2"
			}
			if name == "resume pr-open" {
				issue.Phase = state.PhasePROpen
			}
			var root string
			approver := "alice"
			switch {
			case name == "grant merge":
				r, g := grantMergeRun(t, grantConfigTOML, nil, nil, "grant")
				root, approver = r, "grant:"+g.Terms.ID
			case strings.HasPrefix(name, "resume"):
				root = setupDeliveryGitRepo(t, "r1", []state.Issue{issue})
			default:
				root = setupDeliveryRepoWithConfig(t, metricsEnabledConfigTOML, "r1", []state.Issue{issue})
			}
			calls := []execxtest.Call{ghAuth()}
			if strings.HasPrefix(name, "resume") {
				calls = append(calls, ghIssueViewCall(t, 1, "OPEN", baseManifestBody(t)))
			}
			if name != "ci" {
				calls = append(calls, ghPRViewCall(10, "OPEN", "head-oid-2"))
			}
			probes := ciCalls("pass")
			probes[1].Stdout, probes[1].Exit, probes[1].Stderr = "", 1, "error connecting to api.github.com"
			calls = append(calls, probes...)
			script := &execxtest.Script{T: t, Calls: calls}
			env := Env{RepoRoot: root, Runner: muxRunner{git: execx.Local{}, gh: script}, Now: fixedNow}
			before := readinessTree(t, root)
			var err error
			switch name {
			case "ci":
				_, err = CI(t.Context(), env, []byte(`{"schema_version":1,"issue_number":1}`))
			case "merge-report":
				_, err = MergeReport(t.Context(), env, []byte(`{"schema_version":1,"issue_number":1}`))
			case "human merge":
				_, err = Merge(t.Context(), env, mergeRequest(MergeApprovalStatement, approver))
			case "grant merge":
				_, err = Merge(t.Context(), env, mergeRequest(GrantMergeApprovalStatement, approver))
			default:
				_, err = Resume(t.Context(), env, ResumeRequest{})
			}
			if err == nil || !strings.Contains(err.Error(), "exited 1") || !strings.Contains(err.Error(), probes[1].Stderr) {
				t.Fatalf("err = %v, want explicit required-CI probe failure", err)
			}
			script.AssertExhausted()
			if readinessTree(t, root) != before {
				t.Fatal("failed required-CI probe changed repository contents or metadata")
			}
			assertNoMetricsDir(t, root)
		})
	}
}
