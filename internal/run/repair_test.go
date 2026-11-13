package run

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/kninetimmy/orch/internal/execx"
	"github.com/kninetimmy/orch/internal/execx/execxtest"
	"github.com/kninetimmy/orch/internal/ghops"
	"github.com/kninetimmy/orch/internal/gitops"
	"github.com/kninetimmy/orch/internal/guard"
	"github.com/kninetimmy/orch/internal/manifest"
	"github.com/kninetimmy/orch/internal/state"
)

func repairIssueCall(t *testing.T, body string) execxtest.Call {
	t.Helper()
	call := ghIssueViewCall(t, 1, "OPEN", body)
	call.Stdout = strings.Replace(call.Stdout, `"url":"u"`, `"url":"https://github.com/o/r/issues/1"`, 1)
	return call
}

func repairPRCall(t *testing.T, issue state.Issue, head, body string) execxtest.Call {
	t.Helper()
	call := ghPRViewCall(10, "OPEN", head)
	call.Stdout = strings.Replace(call.Stdout, `"headRefName":"b"`, `"headRefName":"`+issue.Branch+`"`, 1)
	call.Stdout = strings.Replace(call.Stdout, `"body":"pr body"`, `"body":`+jsonQuote(t, body), 1)
	// The deadlock is specifically an unchanged head whose base now conflicts.
	call.Stdout = strings.Replace(call.Stdout, `"mergeStateStatus":"CLEAN"`, `"mergeStateStatus":"DIRTY"`, 1)
	return call
}

func repairCalls(t *testing.T, issue state.Issue, head, body string) []execxtest.Call {
	t.Helper()
	calls := []execxtest.Call{ghAuth(), ghRepoViewCall("main"), repairIssueCall(t, body)}
	if issue.PRNumber == 0 {
		return append(calls, ghPRListEmptyCall(issue.Branch))
	}
	return append(calls, repairPRCall(t, issue, head, body))
}

func repairHistoryBody(t *testing.T, issue state.Issue, head string) string {
	t.Helper()
	m := baseManifest()
	for n := 1; n <= issue.ReviewCycles; n++ {
		m.Verifications = append(m.Verifications, manifest.Verification{
			Name: fmt.Sprintf("review-cycle-%d", n), Result: VerdictApprove, CommitOID: head, At: fixedNow().Format(time.RFC3339)})
	}
	body, err := manifest.Upsert("", m)
	if err != nil {
		t.Fatal(err)
	}
	return body
}

func repairRequest(t *testing.T, root, head string) RepairRequest {
	t.Helper()
	st := loadRun(t, root)
	issue := st.Run.Issues[0]
	return RepairRequest{SchemaVersion: 1, RunID: st.Run.ID, IssueNumber: 1, RepairNumber: len(issue.Repairs) + 1,
		ExpectedPhase: issue.Phase, ExpectedHeadOID: head, AuthorizedBy: "human",
		Reason: "integrate the advanced default branch; keep approved scope and original work", Statement: RepairStatement}
}

func requestBytes(t *testing.T, req any) []byte {
	t.Helper()
	data, err := json.Marshal(req)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

// Both fixtures carry real, pushed implementation commits and a conflicting,
// advanced origin/main. GitHub and every model/session call remain controlled.
func repairRepo(t *testing.T, pinned bool) (string, string, string) {
	t.Helper()
	issue := fixtureIssue("a", 1, state.PhaseDispatched)
	issue.URL = "https://github.com/o/r/issues/1"
	issue.Attempts = []state.Attempt{{Role: manifest.RoleImplementer, Selection: issue.Decision.Executor, Failed: true, Reason: "preserve history"}}
	root := setupDeliveryGitRepo(t, "r1", []state.Issue{issue})
	createBranchWorktree(t, root, issue.Branch)
	wt := filepath.Join(root, filepath.FromSlash(issue.Worktree))
	repairCommit(t, wt, "README.md", "implementation\n", "Implement approved work")
	writeWork(t, wt)
	rawGit(t, wt, "push", "-u", "origin", issue.Branch)
	head := strings.TrimSpace(rawGit(t, wt, "rev-parse", "HEAD"))
	repairCommit(t, root, "README.md", "advanced default\n", "Advance default branch")
	rawGit(t, root, "push", "origin", "main")
	if pinned {
		st := loadRun(t, root)
		i := &st.Run.Issues[0]
		i.Phase, i.PRNumber, i.PRURL = state.PhaseAwaitingMerge, 10, "https://github.com/o/r/pull/10"
		i.ReviewCycles, i.LastReviewVerdict, i.ApprovedHeadOID = 2, VerdictApprove, head
		if err := state.Save(root, st); err != nil {
			t.Fatal(err)
		}
	}
	return root, wt, head
}

func repairCommit(t *testing.T, dir, path, content, message string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, path), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	rawGit(t, dir, "add", path)
	rawGit(t, dir, "commit", "-m", message)
}

func repairGuard(t *testing.T, path string, allow bool) {
	t.Helper()
	v, err := guard.NewChecker(execx.Local{}).Check(t.Context(), guard.Request{Paths: []string{path}})
	if err != nil || v.Allow != allow {
		t.Fatalf("guard(%s) = %+v, %v; want allow=%t", path, v, err, allow)
	}
}

// Repair's runner permits only read-only git commands. Any git-history rewrite,
// fetch, push, new worktree or model session is a test failure, not a fake success.
type repairReadRunner struct{ t *testing.T }

func (r repairReadRunner) Run(ctx context.Context, cmd execx.Cmd) (execx.Result, error) {
	r.t.Helper()
	if cmd.Name != "git" || len(cmd.Args) == 0 {
		r.t.Fatalf("repair launched unexpected command: %+v", cmd)
	}
	switch cmd.Args[0] {
	case "rev-parse", "symbolic-ref", "status", "rev-list", "ls-remote":
	case "worktree":
		if len(cmd.Args) < 2 || cmd.Args[1] != "list" {
			r.t.Fatalf("repair mutated worktrees: %+v", cmd)
		}
	default:
		r.t.Fatalf("repair mutated git: %+v", cmd)
	}
	return (execx.Local{}).Run(ctx, cmd)
}

func invokeRepair(t *testing.T, root string, data []byte, calls ...execxtest.Call) (*RepairResult, error) {
	t.Helper()
	script := &execxtest.Script{T: t, Calls: calls}
	out, err := Repair(t.Context(), Env{RepoRoot: root, Runner: muxRunner{git: repairReadRunner{t}, gh: script}, Now: fixedNow}, data)
	script.AssertExhausted()
	return out, err
}

func TestRepairRecoveredDivergentImplementation(t *testing.T) {
	root, wt, head := repairRepo(t, false)
	body := baseManifestBody(t)
	repairGuard(t, filepath.Join(wt, "README.md"), true)
	runVerb(t, root, Block, `{"schema_version":2,"issue_number":1,"class":"validation","detail":"retain the existing implementation?"}`,
		ghAuth(), ghSetStatusCall(1, ghops.StatusBlocked))
	repairGuard(t, filepath.Join(wt, "README.md"), false)
	resolve := ResolveBlockRequest{SchemaVersion: 1, RunID: loadRun(t, root).Run.ID, IssueNumber: 1, BlockID: 1,
		ResolvedBy: "human", Detail: "retain approved approach and criteria", Statement: ResolveBlockStatement}
	runVerb(t, root, ResolveBlock, string(requestBytes(t, resolve)))
	doc, script := resumeMux(t, root, ResumeRequest{}, ghAuth(), repairIssueCall(t, body), ghPRListEmptyCall("orch/issue-1"))
	script.AssertExhausted()
	if doc.Issues[0].PhaseAfter != state.PhaseWorktreeReady || !strings.Contains(doc.Issues[0].Reason, "orch run repair") {
		t.Fatalf("recovery/guidance: %+v", doc)
	}
	repairGuard(t, filepath.Join(wt, "README.md"), false)
	before := stateBytes(t, root)
	dispatchScript := &execxtest.Script{T: t, Calls: []execxtest.Call{ghAuth(), ghRepoViewCall("main")}}
	_, err := Dispatch(t.Context(), Env{RepoRoot: root, Runner: muxRunner{git: execx.Local{}, gh: dispatchScript}}, []byte(`{"schema_version":4,"issue_number":1}`))
	dispatchScript.AssertExhausted()
	if !errors.Is(err, gitops.ErrNotFastForward) || string(before) != string(stateBytes(t, root)) {
		t.Fatalf("ordinary dispatch no longer fails closed on divergence: %v", err)
	}
	original := loadRun(t, root).Run.Issues[0]
	req := requestBytes(t, repairRequest(t, root, head))
	result, err := invokeRepair(t, root, req, repairCalls(t, original, head, body)...)
	if err != nil {
		t.Fatal(err)
	}
	checkRepairPreserved(t, root, wt, head, original, result)
	checkRepairContainment(t, root, wt, original.Branch)
	// A restart is converged: it must not drop repaired execution back to the
	// same ff-only trap or forget its original block resolution.
	before = stateBytes(t, root)
	doc, script = resumeMux(t, root, ResumeRequest{}, ghAuth(), repairIssueCall(t, body), ghPRListEmptyCall(original.Branch))
	script.AssertExhausted()
	if doc.Issues[0].PhaseAfter != state.PhaseDispatched || string(before) != string(stateBytes(t, root)) {
		t.Fatal("restart lost repaired execution")
	}
	if _, err := invokeRepair(t, root, req); err == nil {
		t.Fatal("replayed repair authorization accepted")
	}
	repaired := integrateRepair(t, root, wt, head)
	_, posted := runVerbScript(t, root, PROpen, `{"schema_version":1,"issue_number":1,"verifications":[{"name":"tests","command":"go test ./...","result":"pass"}]}`,
		ghAuth(), ghRepoViewCall("main"), ghPRListEmptyCall(original.Branch),
		repairIssueCall(t, body), ghSetIssueBodyCall(1), ghCreatePRCall(original.Branch, original.Title, 10),
		ghSetStatusCall(1, ghops.StatusAwaitingReview))
	checkRenewedRepairGates(t, root, repaired, posted.StdinAt(4), head)
}

func TestRepairUnchangedPinnedPR(t *testing.T) {
	root, wt, head := repairRepo(t, true)
	original := loadRun(t, root).Run.Issues[0]
	body := repairHistoryBody(t, original, head)
	repairGuard(t, filepath.Join(wt, "README.md"), false)
	before := stateBytes(t, root)
	doc, script := resumeMux(t, root, ResumeRequest{}, ghAuth(), repairIssueCall(t, body), repairPRCall(t, original, head, body))
	script.AssertExhausted()
	if doc.Issues[0].PhaseAfter != state.PhaseAwaitingMerge || string(before) != string(stateBytes(t, root)) ||
		!strings.Contains(doc.Issues[0].Reason, "orch run repair") {
		t.Fatalf("resume unexpectedly authorizes pinned repair: %+v", doc)
	}
	result, err := invokeRepair(t, root, requestBytes(t, repairRequest(t, root, head)), repairCalls(t, original, head, body)...)
	if err != nil {
		t.Fatal(err)
	}
	checkRepairPreserved(t, root, wt, head, original, result)
	checkRepairContainment(t, root, wt, original.Branch)
	before = stateBytes(t, root)
	doc, script = resumeMux(t, root, ResumeRequest{}, ghAuth(), repairIssueCall(t, body), repairPRCall(t, original, head, body), ghRollupEmptyCall(10))
	script.AssertExhausted()
	if doc.Issues[0].PhaseAfter != state.PhaseInReview || string(before) != string(stateBytes(t, root)) {
		t.Fatal("restart restored invalidated approval")
	}
	repaired := integrateRepair(t, root, wt, head)
	checkRenewedRepairGates(t, root, repaired, body, head)
}

func checkRepairPreserved(t *testing.T, root, wt, head string, original state.Issue, result *RepairResult) {
	t.Helper()
	got := loadRun(t, root).Run.Issues[0]
	want := original
	want.Phase, want.ApprovedHeadOID, want.LastReviewVerdict = state.PhaseDispatched, "", ""
	if original.PRNumber > 0 {
		want.Phase = state.PhaseInReview
	}
	want.Repairs = got.Repairs
	if !reflect.DeepEqual(want, got) || len(got.Repairs) != 1 || got.Repairs[0].AuthorizedBy != "human" ||
		got.Repairs[0].HeadOID != head || got.Repairs[0].ReviewCycles != original.ReviewCycles ||
		result.Execution.SchemaVersion != DispatchSchemaVersion || !reflect.DeepEqual(result.Execution, dispatchResult(&got)) {
		t.Fatalf("repair lost work/history/routing or failed to invalidate approvals:\n%+v\n%+v", got, result)
	}
	if strings.TrimSpace(rawGit(t, wt, "rev-parse", "HEAD")) != head || strings.TrimSpace(rawGit(t, wt, "status", "--porcelain")) != "" ||
		strings.TrimSpace(rawGit(t, root, "ls-remote", "origin", "refs/heads/"+original.Branch)) != head+"\trefs/heads/"+original.Branch {
		t.Fatal("repair moved git or changed preserved work")
	}
}

func checkRepairContainment(t *testing.T, root, wt, branch string) {
	t.Helper()
	repairGuard(t, filepath.Join(wt, "README.md"), true)
	repairGuard(t, filepath.Join(root, "README.md"), false)
	repairGuard(t, filepath.Join(root, ".orchestrator", "state.json"), false)
	repairGuard(t, filepath.Join(wt, ".git"), false)
	repairGuard(t, filepath.Join(root, ".orchestrator", "worktrees", "unregistered", "code.go"), false)
	rawGit(t, wt, "switch", "-c", "wrong-branch")
	repairGuard(t, filepath.Join(wt, "README.md"), false)
	rawGit(t, wt, "switch", branch)
}

func integrateRepair(t *testing.T, root, wt, originalHead string) string {
	t.Helper()
	res, err := (execx.Local{}).Run(t.Context(), execx.Cmd{Name: "git", Dir: wt, Args: []string{"merge", "--no-commit", "origin/main"}})
	if err != nil || res.ExitCode == 0 || !strings.Contains(res.Stdout, "CONFLICT") {
		t.Fatalf("fixture did not reproduce a real integration conflict: %+v %v", res, err)
	}
	repairCommit(t, wt, "README.md", "implementation\nadvanced default\n", "Integrate advanced default without discarding work")
	rawGit(t, wt, "merge-base", "--is-ancestor", originalHead, "HEAD")
	rawGit(t, wt, "merge-base", "--is-ancestor", "origin/main", "HEAD")
	if content, err := os.ReadFile(filepath.Join(wt, "feature.go")); err != nil || string(content) != "package feature\n" {
		t.Fatalf("original implementation lost: %s %v", content, err)
	}
	rawGit(t, wt, "push", "origin", loadRun(t, root).Run.Issues[0].Branch)
	return strings.TrimSpace(rawGit(t, wt, "rev-parse", "HEAD"))
}

func deniedRepairVerb[T any](t *testing.T, root string, fn func(context.Context, Env, []byte) (T, error), req []byte, calls ...execxtest.Call) {
	t.Helper()
	before := stateBytes(t, root)
	script := &execxtest.Script{T: t, Calls: calls}
	_, err := fn(t.Context(), Env{RepoRoot: root, Runner: muxRunner{git: execx.Local{}, gh: script}, Now: fixedNow}, req)
	script.AssertExhausted()
	if err == nil || string(before) != string(stateBytes(t, root)) {
		t.Fatalf("invalid request succeeded or changed state: %v", err)
	}
}

func checkRenewedRepairGates(t *testing.T, root, head, body, oldHead string) {
	t.Helper()
	issue := loadRun(t, root).Run.Issues[0]
	mergeReq := func(oid string, at time.Time) []byte {
		return requestBytes(t, MergeRequest{SchemaVersion: 1, IssueNumber: 1, Approval: MergeApproval{
			PRNumber: 10, HeadOID: oid, ApprovedBy: "human", ApprovedAt: at, Statement: MergeApprovalStatement, RepairNumber: 1}})
	}
	oldApproval := mergeReq(oldHead, fixedNow().Add(-time.Minute))
	deniedRepairVerb(t, root, Merge, oldApproval)
	deniedRepairVerb(t, root, MergeReport, []byte(`{"schema_version":1,"issue_number":1}`))
	review := ReviewRequest{SchemaVersion: ReviewSchemaVersion, IssueNumber: 1, ReviewedHeadOID: head,
		Verdict: VerdictApprove, Summary: "independent review after integration", Reviewer: issue.Decision.Reviewer,
		Judgments: []CriterionJudgment{{Criterion: 1, Judgment: JudgmentSatisfied, Reason: "verified on repaired head"}}}
	deniedRepairVerb(t, root, Review, requestBytes(t, review), ghAuth(), repairPRCall(t, issue, head, body))
	review.RepairNumber = 1
	deniedRepairVerb(t, root, Review, requestBytes(t, review), ghAuth(), repairPRCall(t, issue, head, body))
	review.Verifications = []VerificationInput{{Name: "tests", Command: "go test ./...", Result: "pass"}}
	review.Reviewer = issue.Decision.Executor
	deniedRepairVerb(t, root, Review, requestBytes(t, review))
	review.Reviewer = issue.Decision.Reviewer
	review.ReviewedHeadOID = oldHead
	deniedRepairVerb(t, root, Review, requestBytes(t, review), ghAuth(), repairPRCall(t, issue, head, body))
	review.ReviewedHeadOID = head
	_, recorded := runVerbScript(t, root, Review, string(requestBytes(t, review)),
		ghAuth(), repairPRCall(t, issue, head, body), repairIssueCall(t, body), ghSetIssueBodyCall(1), ghSetPRBodyCall(10))
	reviewBody := recorded.StdinAt(3)
	for n := 1; n <= issue.ReviewCycles; n++ {
		if findVerification(t, reviewBody, fmt.Sprintf("review-cycle-%d", n)).CommitOID != oldHead {
			t.Fatal("repair replaced original review audit history")
		}
	}
	v := findVerification(t, reviewBody, "tests")
	if v.CommitOID != head || latestRepair(&loadRun(t, root).Run.Issues[0]).VerifiedHeadOID != head ||
		loadRun(t, root).Run.Issues[0].ReviewCycles != issue.ReviewCycles+1 {
		t.Fatal("new review/verification not bound to repaired head")
	}
	// Head-specific evidence cannot authorize another unreviewed push.
	deniedRepairVerb(t, root, MergeReport, []byte(`{"schema_version":1,"issue_number":1}`),
		ghAuth(), repairPRCall(t, issue, strings.Repeat("f", 40), reviewBody))
	deniedRepairVerb(t, root, MergeReport, []byte(`{"schema_version":1,"issue_number":1}`),
		append([]execxtest.Call{ghAuth(), repairPRCall(t, issue, head, reviewBody)}, ciCalls("pending")...)...)
	report := runVerb(t, root, MergeReport, `{"schema_version":1,"issue_number":1}`,
		append(append([]execxtest.Call{ghAuth(), repairPRCall(t, issue, head, reviewBody)}, ciCalls("pass")...),
			ghSetStatusCall(1, ghops.StatusNeedsHuman))...)
	if report.HeadOID != head || report.RepairNumber != 1 {
		t.Fatal("new merge report did not pin repaired head")
	}
	// Even with a renewed gate and an unchanged head, an approval from before
	// the repair is stale. A new timestamp is required, not just the same OID.
	deniedRepairVerb(t, root, Merge, mergeReq(head, fixedNow().Add(-time.Minute)))
	deniedRepairVerb(t, root, Merge, oldApproval)
	// Even an old approval with a future/skewed timestamp cannot acknowledge
	// an integration repair that did not exist when it was asserted.
	deniedRepairVerb(t, root, Merge, requestBytes(t, MergeRequest{SchemaVersion: 1, IssueNumber: 1,
		Approval: MergeApproval{PRNumber: 10, HeadOID: head, ApprovedBy: "human", ApprovedAt: fixedNow().Add(time.Hour), Statement: MergeApprovalStatement}}))
	deniedRepairVerb(t, root, Merge, mergeReq(head, fixedNow().Add(time.Minute)),
		append([]execxtest.Call{ghAuth(), repairPRCall(t, issue, head, reviewBody)}, ciCalls("pending")...)...)
	runVerb(t, root, Merge, string(mergeReq(head, fixedNow().Add(time.Minute))),
		append(append([]execxtest.Call{ghAuth(), repairPRCall(t, issue, head, reviewBody)}, ciCalls("pass")...),
			ghMergePRCall(10, head), ghPRViewCall(10, "MERGED", head),
			ghSetStatusCall(1, ghops.StatusDelivered), repairIssueCall(t, reviewBody), ghCloseIssueCall(1), ghSetIssueBodyCall(1))...)
	wantPhase(t, root, 1, state.PhaseMerged)
}

func TestRepairRejectsStaleMalformedAndGatedRequests(t *testing.T) {
	root, wt, head := repairRepo(t, true)
	req := repairRequest(t, root, head)
	for name, mutate := range map[string]func(*RepairRequest){
		"schema":            func(r *RepairRequest) { r.SchemaVersion = 99 },
		"run":               func(r *RepairRequest) { r.RunID = "other-run" },
		"issue":             func(r *RepairRequest) { r.IssueNumber = 2 },
		"zero issue":        func(r *RepairRequest) { r.IssueNumber = 0 },
		"phase":             func(r *RepairRequest) { r.ExpectedPhase = state.PhaseWorktreeReady },
		"writable phase":    func(r *RepairRequest) { r.ExpectedPhase = state.PhaseInReview },
		"replay number":     func(r *RepairRequest) { r.RepairNumber = 2 },
		"head abbreviation": func(r *RepairRequest) { r.ExpectedHeadOID = head[:8] },
		"head expression":   func(r *RepairRequest) { r.ExpectedHeadOID = "HEAD" },
		"actor":             func(r *RepairRequest) { r.AuthorizedBy = " " },
		"reason":            func(r *RepairRequest) { r.Reason = " " },
		"statement":         func(r *RepairRequest) { r.Statement = MergeApprovalStatement },
	} {
		t.Run(name, func(t *testing.T) {
			bad := req
			mutate(&bad)
			deniedRepairVerb(t, root, Repair, requestBytes(t, bad))
		})
	}
	for _, data := range [][]byte{
		[]byte(`{`), append(requestBytes(t, req), []byte(` {}`)...),
		[]byte(strings.TrimSuffix(string(requestBytes(t, req)), "}") + `,"scope":"new"}`),
	} {
		deniedRepairVerb(t, root, Repair, data)
	}
	for _, kind := range []string{"stopped", "config revision", "effective config", "schema 5", "missing routing", "missing work", "unresolved decision", "legacy", "historical unresolved decision", "dependency unmet", "dependency abandoned", "dependency missing", "ambiguous identity", "initial dispatch"} {
		t.Run(kind, func(t *testing.T) {
			original := loadRun(t, root)
			st, err := cloneState(original)
			if err != nil {
				t.Fatal(err)
			}
			i := &st.Run.Issues[0]
			switch kind {
			case "stopped":
				st.Run.StoppedReason = "secret"
			case "config revision":
				st.Run.Plan.ConfigRevision = "other"
			case "effective config":
				st.Run.Plan.ExecutionDigest = "sha256:other"
			case "schema 5":
				st.SchemaVersion = 5
			case "missing routing":
				i.Phase, i.PRNumber, i.ApprovedHeadOID, i.Decision = state.PhaseWorktreeReady, 0, "", nil
			case "missing work":
				i.Objective = ""
			case "unresolved decision":
				i.SetBlock(state.BlockHuman, "decision required")
			case "legacy":
				i.Phase, i.BlockedReason = state.PhaseBlocked, "unknown original block"
			case "historical unresolved decision":
				i.Blocks = []state.Block{{ID: 1, Cause: state.BlockHuman, Reason: "original unresolved decision"}}
			case "dependency unmet", "dependency abandoned":
				d := fixtureIssue("dep", 2, state.PhaseDispatched)
				if kind == "dependency abandoned" {
					d.Phase = state.PhaseAbandoned
				}
				i.DependsOn = []string{"dep"}
				st.Run.Issues = append(st.Run.Issues, d)
			case "dependency missing":
				i.DependsOn = []string{"missing"}
			case "ambiguous identity":
				d := fixtureIssue("a", 2, state.PhaseDispatched)
				st.Run.Issues = append(st.Run.Issues, d)
			case "initial dispatch":
				i.Phase, i.PRNumber, i.ApprovedHeadOID = state.PhaseWorktreeReady, 0, ""
			}
			if err := state.Save(root, st); err != nil {
				t.Fatal(err)
			}
			bad := req
			if i.Phase == state.PhaseWorktreeReady {
				bad.ExpectedPhase = i.Phase
			}
			deniedRepairVerb(t, root, Repair, requestBytes(t, bad))
			if err := state.Save(root, original); err != nil {
				t.Fatal(err)
			}
		})
	}
	if strings.TrimSpace(rawGit(t, wt, "rev-parse", "HEAD")) != head {
		t.Fatal("invalid requests moved HEAD")
	}
}

func TestRepairRejectsUnhealthyArtifactsWithoutMutation(t *testing.T) {
	for _, kind := range []string{"issue closed", "issue identity", "issue scope", "issue audit missing", "routing", "PR closed", "PR merged", "PR identity", "PR branch", "PR base", "PR head", "PR scope", "PR audit missing", "local head", "missing worktree", "missing branch", "missing remote branch", "wrong branch", "detached", "wrong path", "foreign gitdir", "dirty"} {
		t.Run(kind, func(t *testing.T) {
			root, wt, head := repairRepo(t, true)
			issue := loadRun(t, root).Run.Issues[0]
			body := baseManifestBody(t)
			req := repairRequest(t, root, head)
			calls := repairCalls(t, issue, head, body)
			modManifest := func(change func(*manifest.Manifest)) string {
				m := baseManifest()
				change(&m)
				b, err := manifest.Upsert("", m)
				if err != nil {
					t.Fatal(err)
				}
				return b
			}
			switch kind {
			case "issue closed":
				calls[2].Stdout = strings.Replace(calls[2].Stdout, `"state":"OPEN"`, `"state":"CLOSED"`, 1)
				calls = calls[:3]
			case "issue identity":
				calls[2].Stdout = strings.ReplaceAll(calls[2].Stdout, "o/r/issues/1", "other/repo/issues/1")
				calls = calls[:3]
			case "issue scope":
				calls[2] = repairIssueCall(t, modManifest(func(m *manifest.Manifest) { m.Objective = "new scope" }))
				calls = calls[:3]
			case "issue audit missing":
				calls[2] = repairIssueCall(t, "no managed record")
				calls = calls[:3]
			case "routing":
				calls[2] = repairIssueCall(t, modManifest(func(m *manifest.Manifest) { m.RoutingRationale = "unapproved route" }))
				calls = calls[:3]
			case "PR closed", "PR merged":
				value := "CLOSED"
				if kind == "PR merged" {
					value = "MERGED"
				}
				calls[3].Stdout = strings.Replace(calls[3].Stdout, `"state":"OPEN"`, `"state":"`+value+`"`, 1)
			case "PR identity":
				calls[3].Stdout = strings.ReplaceAll(calls[3].Stdout, "o/r/pull/10", "other/repo/pull/10")
			case "PR branch":
				calls[3].Stdout = strings.ReplaceAll(calls[3].Stdout, issue.Branch, "other-branch")
			case "PR base":
				calls[3].Stdout = strings.ReplaceAll(calls[3].Stdout, `"baseRefName":"main"`, `"baseRefName":"other"`)
			case "PR head":
				calls[3] = repairPRCall(t, issue, strings.Repeat("f", 40), body)
			case "PR scope":
				calls[3] = repairPRCall(t, issue, head, modManifest(func(m *manifest.Manifest) { m.AcceptanceCriteria = []string{"replacement"} }))
			case "PR audit missing":
				calls[3] = repairPRCall(t, issue, head, "missing")
			case "local head":
				repairCommit(t, wt, "more.txt", "more\n", "Unobserved local commit")
			case "missing worktree":
				rawGit(t, root, "worktree", "remove", wt)
			case "missing branch":
				rawGit(t, root, "worktree", "remove", wt)
				rawGit(t, root, "branch", "-D", issue.Branch)
			case "missing remote branch":
				rawGit(t, root, "push", "origin", "--delete", issue.Branch)
			case "wrong branch":
				rawGit(t, wt, "switch", "-c", "wrong")
			case "detached":
				rawGit(t, wt, "switch", "--detach")
			case "wrong path":
				rawGit(t, root, "worktree", "move", wt, wt+"-moved")
			case "foreign gitdir":
				foreign := newLifecycleRepo(t)
				createBranchWorktree(t, foreign, "foreign")
				content, err := os.ReadFile(filepath.Join(foreign, ".orchestrator", "worktrees", "issue-1", ".git"))
				if err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(wt, ".git"), content, 0o644); err != nil {
					t.Fatal(err)
				}
			case "dirty":
				if err := os.WriteFile(filepath.Join(wt, "dirty"), []byte("preserve"), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			deniedRepairVerb(t, root, Repair, requestBytes(t, req), calls...)
			repairGuard(t, filepath.Join(wt, "README.md"), false)
		})
	}
}

func TestRepairReviewAuditFailureCannotRepin(t *testing.T) {
	root, _, head := repairRepo(t, true)
	issue := loadRun(t, root).Run.Issues[0]
	body := repairHistoryBody(t, issue, head)
	if _, err := invokeRepair(t, root, requestBytes(t, repairRequest(t, root, head)), repairCalls(t, issue, head, body)...); err != nil {
		t.Fatal(err)
	}
	review := ReviewRequest{SchemaVersion: ReviewSchemaVersion, IssueNumber: 1, RepairNumber: 1, ReviewedHeadOID: head,
		Verdict: VerdictApprove, Summary: "new independent review at unchanged head", Reviewer: issue.Decision.Reviewer,
		Judgments:     []CriterionJudgment{{Criterion: 1, Judgment: JudgmentSatisfied, Reason: "checked"}},
		Verifications: []VerificationInput{{Name: "tests", Result: "pass"}}}
	failure := ghSetPRBodyCall(10)
	failure.Exit, failure.Stderr = 1, "controlled audit write failure"
	script := &execxtest.Script{T: t, Calls: []execxtest.Call{ghAuth(), repairPRCall(t, issue, head, body),
		repairIssueCall(t, body), ghSetIssueBodyCall(1), failure}}
	if _, err := Review(t.Context(), Env{RepoRoot: root, Runner: script, Now: fixedNow}, requestBytes(t, review)); err == nil ||
		!strings.Contains(err.Error(), failure.Stderr) {
		t.Fatalf("missing partial-write error: %v", err)
	}
	script.AssertExhausted()
	got := loadRun(t, root).Run.Issues[0]
	if latestRepair(&got).VerifiedHeadOID != "" {
		t.Fatal("failed audit write enabled repair verification")
	}
	deniedRepairVerb(t, root, MergeReport, []byte(`{"schema_version":1,"issue_number":1}`))
	// Same-head recovery still needs a new review acknowledgement, evidence,
	// report and approval; old timestamps cannot be rescued by an identical OID.
	_, posted := runVerbScript(t, root, Review, string(requestBytes(t, review)),
		ghAuth(), repairPRCall(t, issue, head, body), repairIssueCall(t, script.StdinAt(3)), ghSetIssueBodyCall(1), ghSetPRBodyCall(10))
	report := runVerb(t, root, MergeReport, `{"schema_version":1,"issue_number":1}`, ghAuth(),
		repairPRCall(t, issue, head, posted.StdinAt(3)), ghRollupEmptyCall(10), ghSetStatusCall(1, ghops.StatusNeedsHuman))
	if report.HeadOID != head {
		t.Fatal("same-head report lost pin")
	}
	old := MergeRequest{SchemaVersion: 1, IssueNumber: 1, Approval: MergeApproval{PRNumber: 10, HeadOID: head,
		ApprovedBy: "human", ApprovedAt: fixedNow(), Statement: MergeApprovalStatement}}
	deniedRepairVerb(t, root, Merge, requestBytes(t, old))
	// A replay of the original repair request cannot authorize this new gate.
	stale := repairRequest(t, root, head)
	stale.RepairNumber = 1
	deniedRepairVerb(t, root, Repair, requestBytes(t, stale))
	old.Approval.RepairNumber, old.Approval.ApprovedAt = 1, fixedNow().Add(time.Minute)
	runVerb(t, root, Merge, string(requestBytes(t, old)), ghAuth(), repairPRCall(t, issue, head, posted.StdinAt(3)),
		ghRollupEmptyCall(10), ghMergePRCall(10, head), ghPRViewCall(10, "MERGED", head),
		ghSetStatusCall(1, ghops.StatusDelivered), repairIssueCall(t, posted.StdinAt(3)), ghCloseIssueCall(1), ghSetIssueBodyCall(1))
	wantPhase(t, root, 1, state.PhaseMerged)
}

func TestRepairPrePRRejectsOrphanAndMissingImplementation(t *testing.T) {
	for _, orphan := range []bool{false, true} {
		t.Run(fmt.Sprint(orphan), func(t *testing.T) {
			issue := fixtureIssue("a", 1, state.PhaseWorktreeReady)
			issue.URL = "https://github.com/o/r/issues/1"
			issue.Blocks = []state.Block{{ID: 1, Cause: state.BlockOperational, Reason: "recovered"}}
			root := setupDeliveryGitRepo(t, "r1", []state.Issue{issue})
			createBranchWorktree(t, root, issue.Branch)
			head := strings.TrimSpace(rawGit(t, root, "rev-parse", issue.Branch))
			calls := repairCalls(t, issue, head, baseManifestBody(t))
			if orphan {
				calls[3] = prListForBranchCall(issue.Branch, 10, head)
			}
			deniedRepairVerb(t, root, Repair, requestBytes(t, repairRequest(t, root, head)), calls...)
			repairGuard(t, filepath.Join(root, issue.Worktree, "README.md"), false)
		})
	}
}

func TestRepairReceiptValidationFailsClosed(t *testing.T) {
	root, _, head := repairRepo(t, true)
	issue := loadRun(t, root).Run.Issues[0]
	if _, err := invokeRepair(t, root, requestBytes(t, repairRequest(t, root, head)), repairCalls(t, issue, head, baseManifestBody(t))...); err != nil {
		t.Fatal(err)
	}
	for name, change := range map[string]func(*state.IntegrationRepair){
		"number":                  func(r *state.IntegrationRepair) { r.Number = 2 },
		"phase":                   func(r *state.IntegrationRepair) { r.PhaseBefore = state.PhaseDispatched },
		"head":                    func(r *state.IntegrationRepair) { r.HeadOID = "HEAD" },
		"actor":                   func(r *state.IntegrationRepair) { r.AuthorizedBy = " " },
		"reason":                  func(r *state.IntegrationRepair) { r.Reason = "" },
		"time":                    func(r *state.IntegrationRepair) { r.At = "yesterday" },
		"cycles":                  func(r *state.IntegrationRepair) { r.ReviewCycles = 99 },
		"unreviewed verification": func(r *state.IntegrationRepair) { r.VerifiedHeadOID = head },
	} {
		t.Run(name, func(t *testing.T) {
			st := loadRun(t, root)
			before := stateBytes(t, root)
			change(&st.Run.Issues[0].Repairs[0])
			if err := state.Save(root, st); err == nil || string(before) != string(stateBytes(t, root)) {
				t.Fatalf("invalid repair receipt saved: %v", err)
			}
		})
	}
}

func TestRepairGuidanceRemainsReadOnly(t *testing.T) {
	for _, pinned := range []bool{false, true} {
		t.Run(fmt.Sprint(pinned), func(t *testing.T) {
			root, _, head := repairRepo(t, pinned)
			st := loadRun(t, root)
			if !pinned {
				st.Run.Issues[0].SetBlock(state.BlockOperational, "recovered")
				st.Run.Issues[0].Phase, st.Run.Issues[0].BlockedReason = state.PhaseWorktreeReady, ""
				if err := state.Save(root, st); err != nil {
					t.Fatal(err)
				}
			}
			before := stateBytes(t, root)
			doc, err := Next(t.Context(), ghEnv(root, &execxtest.Script{T: t}))
			if err != nil || !strings.Contains(strings.Join(doc.Actions[0].Prerequisites, " "), "orch run repair") ||
				string(before) != string(stateBytes(t, root)) {
				t.Fatalf("repair guidance missing or mutating: %+v %v", doc, err)
			}
			if head == "" {
				t.Fatal("fixture head absent")
			}
		})
	}
}
