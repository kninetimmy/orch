package run

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/kninetimmy/orch/internal/execx"
	"github.com/kninetimmy/orch/internal/manifest"
	"github.com/kninetimmy/orch/internal/state"
)

const (
	riskRepoURL = "https://github.com/o/r"
	riskHead    = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	riskNewHead = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
)

type riskServer struct {
	t             *testing.T
	now           time.Time
	head, prState string
	issueBody     string
	prBody        string
	decision      map[string]any
	viewerID      string
	viewerRole    string
	viewerBypass  string
	adminRole     string
	canAdminister bool
	classic       bool
	inherited     bool
	extraRule     string
	strict        bool
	checks        []map[string]any
	incomplete    bool
	failMirror    bool
	mergeCalls    int
}

func (g *riskServer) json(v any) execx.Result {
	return execx.Result{Stdout: string(requestBytes(g.t, v))}
}

func (g *riskServer) Run(_ context.Context, cmd execx.Cmd) (execx.Result, error) {
	a := cmd.Args
	if cmd.Name != "gh" || len(a) < 2 {
		g.t.Fatalf("unexpected command %s %q", cmd.Name, a)
	}
	switch a[0] + " " + a[1] {
	case "auth status":
		return execx.Result{}, nil
	case "repo view":
		return g.json(map[string]any{"id": "R_repo", "url": riskRepoURL, "nameWithOwner": "o/r", "defaultBranchRef": map[string]any{"name": "main"}}), nil
	case "pr view":
		return g.json(map[string]any{"number": 10, "url": riskRepoURL + "/pull/10", "state": g.prState, "body": g.prBody,
			"baseRefName": "main", "headRefName": "orch/issue-1", "headRefOid": g.head}), nil
	case "issue view":
		status := "OPEN"
		if g.prState == "MERGED" {
			status = "CLOSED"
		}
		return g.json(map[string]any{"number": 1, "url": riskRepoURL + "/issues/1", "state": status, "body": g.issueBody}), nil
	case "issue edit":
		if cmd.Stdin != nil {
			data, err := io.ReadAll(cmd.Stdin)
			if err != nil {
				g.t.Fatal(err)
			}
			g.issueBody = string(data)
		}
		return execx.Result{}, nil
	case "api graphql":
		return g.graphql(cmd), nil
	}
	switch a[len(a)-1] {
	case "repos/o/r/collaborators/automation/permission":
		return g.json(riskPermission("automation", 11, "write", g.viewerRole)), nil
	case "repos/o/r/collaborators/admin/permission":
		return g.json(riskPermission("admin", 22, g.adminRole, g.adminRole)), nil
	case "repos/o/r/branches/main":
		return g.json(map[string]any{"name": "main", "protected": true, "protection": map[string]any{
			"enabled": g.classic, "required_status_checks": map[string]any{"enforcement_level": "off", "contexts": []any{}, "checks": []any{}},
		}}), nil
	case "repos/o/r/rules/branches/main?per_page=100":
		source := "Repository"
		if g.inherited {
			source = "Organization"
		}
		page := []map[string]any{{"type": "required_status_checks", "ruleset_id": 9, "ruleset_source": "o/r", "ruleset_source_type": source,
			"parameters": map[string]any{"strict_required_status_checks_policy": g.strict, "do_not_enforce_on_create": false,
				"required_status_checks": []map[string]any{{"context": "build", "integration_id": 15368}}}}}
		if g.extraRule != "" {
			page = append(page, map[string]any{"type": g.extraRule, "ruleset_id": 10, "ruleset_source": "o/r", "ruleset_source_type": source})
		}
		return g.json([]any{page}), nil
	case "repos/o/r/rulesets/9?includes_parents=false":
		return g.json(map[string]any{"id": 9, "target": "branch", "source_type": "Repository",
			"source": "o/r", "enforcement": "active", "current_user_can_bypass": g.viewerBypass, "updated_at": fixedNow()}), nil
	}
	g.t.Fatalf("unexpected gh arguments %q", a)
	return execx.Result{}, nil
}

func riskPermission(login string, id int, permission, role string) map[string]any {
	return map[string]any{"permission": permission, "role_name": role, "user": map[string]any{"login": login, "id": id, "type": "User"}}
}

func (g *riskServer) graphql(cmd execx.Cmd) execx.Result {
	var in struct {
		OperationName string
		Variables     map[string]json.RawMessage
	}
	if err := json.NewDecoder(cmd.Stdin).Decode(&in); err != nil {
		g.t.Fatal(err)
	}
	wrap := func(x any) execx.Result { return g.json(map[string]any{"data": x}) }
	vars := in.Variables
	switch in.OperationName {
	case "OrchRiskSnapshot":
		if string(vars["repositoryId"]) != `"R_repo"` || string(vars["issue"]) != "1" || string(vars["pr"]) != "10" ||
			string(vars["admin"]) != `"U_admin"` {
			g.t.Fatalf("crossed snapshot query: %+v", vars)
		}
		pr := map[string]any{"number": 10, "id": "PR_issue", "url": riskRepoURL + "/pull/10", "state": g.prState,
			"headRefName": "orch/issue-1", "headRefOid": g.head, "baseRefName": "main", "body": g.prBody,
			"headRepository": map[string]any{"id": "R_repo"}, "baseRepository": map[string]any{"id": "R_repo"}}
		if string(vars["ci"]) == "true" {
			pr["headRef"] = map[string]any{"target": map[string]any{"oid": g.head, "statusCheckRollup": map[string]any{
				"commit": map[string]any{"oid": g.head}, "contexts": map[string]any{"totalCount": len(g.checks), "nodes": g.checks,
					"pageInfo": map[string]any{"hasNextPage": g.incomplete}},
			}}}
		}
		return wrap(map[string]any{"viewer": map[string]any{"id": g.viewerID, "databaseId": 11, "login": "automation"},
			"admin": map[string]any{"id": "U_admin", "databaseId": 22, "login": "admin"},
			"repository": map[string]any{"id": "R_repo", "nameWithOwner": "o/r", "url": riskRepoURL, "viewerCanAdminister": g.canAdminister,
				"viewerPermission": "WRITE", "defaultBranchRef": map[string]any{"name": "main"}, "pullRequest": pr,
				"issue": map[string]any{"id": "I_issue", "number": 1, "url": riskRepoURL + "/issues/1", "state": "OPEN", "body": g.issueBody}}})
	case "OrchRiskDecision":
		return wrap(map[string]any{"viewer": map[string]any{"id": g.viewerID}, "node": g.decision})
	case "OrchRiskAudit":
		var issue, pr map[string]string
		if json.Unmarshal(vars["issue"], &issue) != nil || json.Unmarshal(vars["pr"], &pr) != nil ||
			issue["id"] != "I_issue" || pr["pullRequestId"] != "PR_issue" {
			g.t.Fatalf("crossed audit mutation: %+v", vars)
		}
		g.issueBody = issue["body"]
		if g.failMirror {
			g.failMirror = false
			return g.json(map[string]any{"errors": []any{map[string]any{"message": "PR mirror unavailable"}}})
		}
		g.prBody = pr["body"]
		return wrap(map[string]any{"issue": map[string]any{"issue": map[string]any{"id": "I_issue", "body": g.issueBody, "state": "OPEN"}},
			"pr": map[string]any{"pullRequest": map[string]any{"id": "PR_issue", "body": g.prBody, "state": g.prState,
				"headRefOid": g.head, "baseRefName": "main"}}})
	case "OrchRiskMerge":
		var input map[string]string
		if json.Unmarshal(vars["input"], &input) != nil || input["pullRequestId"] != "PR_issue" ||
			input["expectedHeadOid"] != g.head || input["mergeMethod"] != "SQUASH" {
			g.t.Fatalf("merge not pinned to verified PR/head: %+v", vars)
		}
		g.mergeCalls++
		g.prState = "MERGED"
		return wrap(map[string]any{"mergePullRequest": map[string]any{"pullRequest": map[string]any{"id": "PR_issue",
			"state": "MERGED", "headRefOid": g.head, "repository": map[string]any{"id": "R_repo"}}}})
	default:
		g.t.Fatalf("unexpected GraphQL operation %s", in.OperationName)
		return execx.Result{}
	}
}

func (g *riskServer) green() {
	g.checks = []map[string]any{{"kind": "CheckRun", "name": "build", "id": "CR_build", "status": "COMPLETED", "conclusion": "SUCCESS",
		"completedAt": g.now.Add(-time.Hour), "checkSuite": map[string]any{"app": map[string]any{"databaseId": 15368},
			"repository": map[string]any{"id": "R_repo"}, "commit": map[string]any{"oid": g.head}}}}
}

func (g *riskServer) submit(challenge string) {
	g.now = g.now.Add(time.Minute)
	g.decision = map[string]any{"id": "PRR_admin", "url": riskRepoURL + "/pull/10#pullrequestreview-99",
		"state": "COMMENTED", "body": riskStatement + challenge + "\n\nRetry is cheap and the affected surface is bounded.",
		"createdAt": g.now, "submittedAt": g.now, "editor": nil, "lastEditedAt": nil, "includesCreatedEdit": false,
		"isMinimized": false, "viewerDidAuthor": false, "createdViaEmail": false, "isCopilotCodeReview": false,
		"author": map[string]any{"id": "U_admin", "databaseId": 22, "login": "admin"},
		"commit": map[string]any{"oid": g.head}, "pullRequest": map[string]any{"id": "PR_issue"}, "repository": map[string]any{"id": "R_repo"}}
}

func newRiskFixture(t *testing.T, enabled bool) (Env, *riskServer, ReviewRequest, AcceptRiskRequest) {
	t.Helper()
	toml := testConfigTOML
	if enabled {
		toml += "\n[residual_risk]\nadmin_user_id = \"U_admin\"\n"
	}
	root := setupRepo(t, toml)
	iss := fixtureIssue("r", 1, state.PhasePROpen)
	iss.URL = riskRepoURL + "/issues/1"
	enterDeliveryAt(t, root, "r1", []state.Issue{iss})
	st := loadRun(t, root)
	st.Run.Plan.GitHubRepositoryID = "R_repo"
	if err := state.Save(root, st); err != nil {
		t.Fatal(err)
	}
	body := baseManifestBody(t)
	g := &riskServer{t: t, now: fixedNow(), head: riskHead, prState: "OPEN", issueBody: body, prBody: body,
		viewerID: "U_automation", viewerRole: "write", viewerBypass: "never", adminRole: "admin", strict: true}
	g.green()
	env := Env{RepoRoot: root, Runner: g, Now: func() time.Time { return g.now }}
	return env, g, ReviewRequest{SchemaVersion: ReviewSchemaVersion, IssueNumber: 1, ReviewedHeadOID: riskHead, Verdict: VerdictRiskHold,
			Summary:  "All evidence complete; one uncertain display refresh requires a decision.",
			Reviewer: iss.Decision.Reviewer, Judgments: []CriterionJudgment{{1, JudgmentSatisfied, "Regression ran on this head."}},
			Verifications: []VerificationInput{{Name: "suite", Command: iss.RequiredTests[0], Result: "pass", Detail: "20 tests passed"}},
			RiskHold:      &RiskHoldInput{"Two simultaneous refreshes", "Stale display", "Refresh again", true}},
		AcceptRiskRequest{AcceptRiskSchemaVersion, st.Run.ID, 1, 1, "PRR_admin"}
}

func holdRisk(t *testing.T, env Env, g *riskServer, req ReviewRequest) *ReviewResult {
	t.Helper()
	hold, err := Review(t.Context(), env, requestBytes(t, req))
	if err != nil || hold.Phase != state.PhaseBlocked || hold.Verdict != VerdictRiskHold || hold.RiskChallenge == "" {
		t.Fatalf("hold = %+v, %v", hold, err)
	}
	g.submit(hold.RiskChallenge)
	return hold
}

func TestResidualRiskSameHeadThroughSeparateMergeGate(t *testing.T) {
	env, g, req, accept := newRiskFixture(t, true)
	g.failMirror = true
	if _, err := Review(t.Context(), env, requestBytes(t, req)); err == nil {
		t.Fatal("partial review unexpectedly finalized")
	}
	wantPhase(t, env.RepoRoot, 1, state.PhasePROpen)
	if doc, err := Next(t.Context(), env); err != nil || doc.Actions[0].Action != "retry-risk-review" {
		t.Fatalf("partial review next = %+v, %v", doc, err)
	}
	hold := holdRisk(t, env, g, req)
	if hold.ReviewCycles != 1 || hold.RiskBlockID != accept.BlockID {
		t.Fatalf("retry duplicated review or block: %+v", hold)
	}
	doc, err := Next(t.Context(), env)
	if err != nil || doc.Actions[0].Action != "accept-risk" {
		t.Fatalf("held next = %+v, %v", doc, err)
	}
	held := loadRun(t, env.RepoRoot)
	if resumed, err := Resume(t.Context(), env, ResumeRequest{}); err != nil || resumed.Issues[0].PhaseAfter != state.PhaseBlocked {
		t.Fatalf("generic resume = %+v, %v", resumed, err)
	}
	resolve := ResolveBlockRequest{1, held.Run.ID, 1, 1, "admin", "looks good", ResolveBlockStatement}
	if _, err := ResolveBlock(t.Context(), env, requestBytes(t, resolve)); !errors.Is(err, ErrBadApproval) {
		t.Fatalf("generic resolve: %v", err)
	}
	approve := req
	approve.RiskHold, approve.Verdict = nil, VerdictApprove
	if _, err := Review(t.Context(), env, requestBytes(t, approve)); !errors.Is(err, ErrBadApproval) {
		t.Fatalf("same-head generic review while held = %v", err)
	}
	g.failMirror = true
	if _, err := AcceptRisk(t.Context(), env, requestBytes(t, accept)); err == nil {
		t.Fatal("partial acceptance unexpectedly finalized")
	}
	wantPhase(t, env.RepoRoot, 1, state.PhaseBlocked)
	first := findVerification(t, g.issueBody, "risk-acceptance-1")
	g.checks[0]["conclusion"] = "SKIPPED"
	if _, err := AcceptRisk(t.Context(), env, requestBytes(t, accept)); !errors.Is(err, ErrCINotReady) {
		t.Fatalf("partial acceptance incorrectly reused stale CI: %v", err)
	}
	g.checks[0]["conclusion"] = "SUCCESS"
	g.now = g.now.Add(time.Minute)
	accepted, err := AcceptRisk(t.Context(), env, requestBytes(t, accept))
	if err != nil || accepted.Phase != state.PhaseInReview || accepted.Receipt != first ||
		findVerification(t, g.prBody, first.Name) != first || !strings.Contains(first.Detail, "CR_build") ||
		!strings.Contains(first.Detail, "Retry is cheap") || !strings.Contains(first.Detail, "U_admin") {
		t.Fatalf("acceptance did not converge with CI/actor/reason: %+v, %v", accepted, err)
	}
	after := loadRun(t, env.RepoRoot)
	is, before := after.Run.Issues[0], held.Run.Issues[0]
	if after.Run.ID != held.Run.ID || is.PRNumber != before.PRNumber || is.Worktree != before.Worktree || is.Branch != before.Branch ||
		is.ReviewCycles != before.ReviewCycles || is.LastReviewVerdict != VerdictRiskHold || is.ReviewedHeadOID != riskHead || is.ApprovedHeadOID != "" {
		t.Fatalf("acceptance changed artifacts, verdict or review cycle: %+v", is)
	}
	if _, err := AcceptRisk(t.Context(), env, requestBytes(t, accept)); !errors.Is(err, ErrWrongPhase) {
		t.Fatalf("replayed acceptance = %v", err)
	}
	reportReq := MergeReportRequest{SchemaVersion: MergeReportSchemaVersion, IssueNumber: 1}
	if _, err := MergeReport(t.Context(), env, requestBytes(t, reportReq)); err == nil {
		t.Fatal("acceptance was treated as independent approval")
	}
	if _, err := Review(t.Context(), env, requestBytes(t, approve)); err == nil {
		t.Fatal("fresh reviewer omitted acknowledgment")
	}
	approve.RiskFollowUp = &RiskFollowUp{hold.RiskBlockID, riskAcknowledged, "I independently reviewed this head and the scoped receipt."}
	g.now = g.now.Add(time.Minute)
	if approved, err := Review(t.Context(), env, requestBytes(t, approve)); err != nil || approved.ReviewCycles != 2 {
		t.Fatalf("fresh review = %+v, %v", approved, err)
	}
	if _, err := Review(t.Context(), env, requestBytes(t, approve)); err == nil {
		t.Fatal("a superseding review reused the decision")
	}
	reportReq.CIException = &HumanCIException{}
	if _, err := MergeReport(t.Context(), env, requestBytes(t, reportReq)); !errors.Is(err, ErrBadApproval) {
		t.Fatalf("manual CI report = %v", err)
	}
	reportReq.CIException = nil
	report, err := MergeReport(t.Context(), env, requestBytes(t, reportReq))
	if err != nil || report.HeadOID != riskHead || report.CI.Total != 1 || report.RiskMergeReportAt == nil {
		t.Fatalf("report = %+v, %v", report, err)
	}
	merge := MergeRequest{SchemaVersion: MergeSchemaVersion, IssueNumber: 1, Approval: MergeApproval{
		PRNumber: 10, HeadOID: riskHead, ApprovedBy: "human", ApprovedAt: g.now, Statement: MergeApprovalStatement}}
	refuseMerge := func(label string) {
		t.Helper()
		if _, err := Merge(t.Context(), env, requestBytes(t, merge)); err == nil || g.mergeCalls != 0 {
			t.Fatalf("%s progressed merge: %v; calls %d", label, err, g.mergeCalls)
		}
		wantPhase(t, env.RepoRoot, 1, state.PhaseAwaitingMerge)
	}
	refuseMerge("old approval")
	g.now = g.now.Add(time.Minute)
	merge.Approval.ApprovedAt = g.now
	merge.Approval.Statement = GrantMergeApprovalStatement
	refuseMerge("grant approval")
	merge.Approval.Statement = MergeApprovalStatement
	merge.CIException = &HumanCIException{}
	refuseMerge("CI exception")
	merge.CIException = nil
	ack := MergeRequest{SchemaVersion: MergeSchemaVersion, IssueNumber: 1, MergedAcknowledgment: &HumanCIException{}}
	if _, err := Merge(t.Context(), env, requestBytes(t, ack)); !errors.Is(err, ErrBadApproval) {
		t.Fatalf("already-merged acknowledgment = %v", err)
	}
	g.prState = "MERGED"
	refuseMerge("out-of-band merged PR")
	g.prState = "OPEN"
	g.strict = false
	refuseMerge("changed required policy")
	g.strict = true
	g.checks[0]["conclusion"] = "FAILURE"
	refuseMerge("lost CI success")
	g.checks[0]["conclusion"] = "SUCCESS"
	merged, err := Merge(t.Context(), env, requestBytes(t, merge))
	if err != nil || !merged.Merged || g.mergeCalls != 1 {
		t.Fatalf("merge = %+v, %v; calls %d", merged, err, g.mergeCalls)
	}
	wantPhase(t, env.RepoRoot, 1, state.PhaseMerged)
	if got := findVerification(t, g.issueBody, first.Name); got != first {
		t.Fatalf("merge changed acceptance: %+v", got)
	}
}

func TestResidualRiskRejectsIneligibleReview(t *testing.T) {
	cases := map[string]func(*ReviewRequest, *riskServer){
		"unsatisfied":  func(r *ReviewRequest, _ *riskServer) { r.Judgments[0].Judgment = JudgmentUnsatisfied },
		"unverified":   func(r *ReviewRequest, _ *riskServer) { r.Judgments[0].Judgment = JudgmentUnverified },
		"wrong":        func(r *ReviewRequest, _ *riskServer) { r.Judgments[0].Judgment = JudgmentWrong },
		"missing test": func(r *ReviewRequest, _ *riskServer) { r.Verifications = nil },
		"failed test":  func(r *ReviewRequest, _ *riskServer) { r.Verifications[0].Result = "fail" },
		"ineligible":   func(r *ReviewRequest, _ *riskServer) { r.RiskHold.EligibleOnly = false },
		"no recovery":  func(r *ReviewRequest, _ *riskServer) { r.RiskHold.Recovery = "" },
		"wrong route":  func(r *ReviewRequest, _ *riskServer) { r.Reviewer.Model = "another" },
		"classic":      func(_ *ReviewRequest, g *riskServer) { g.classic = true },
		"inherited":    func(_ *ReviewRequest, g *riskServer) { g.inherited = true },
		"queue":        func(_ *ReviewRequest, g *riskServer) { g.extraRule = "merge_queue" },
		"old adapter":  func(r *ReviewRequest, _ *riskServer) { r.SchemaVersion = 4 },
		"opted out":    func(*ReviewRequest, *riskServer) {},
	}
	for name, change := range cases {
		t.Run(name, func(t *testing.T) {
			env, g, req, _ := newRiskFixture(t, name != "opted out")
			change(&req, g)
			if got, err := Review(t.Context(), env, requestBytes(t, req)); err == nil {
				t.Fatalf("recorded ineligible hold: %+v", got)
			}
			wantPhase(t, env.RepoRoot, 1, state.PhasePROpen)
			if strings.Contains(g.issueBody, "risk-hold-1") {
				t.Fatal("ineligible review wrote a hold")
			}
		})
	}
}

func TestResidualRiskRejectsUnauthenticatedStaleOrNonGreenDecision(t *testing.T) {
	tests := map[string]func(*riskServer, *AcceptRiskRequest){
		"run":        func(_ *riskServer, r *AcceptRiskRequest) { r.RunID = "other-run" },
		"block":      func(_ *riskServer, r *AcceptRiskRequest) { r.BlockID++ },
		"review ID":  func(_ *riskServer, r *AcceptRiskRequest) { r.DecisionReviewID = "PRR_other" },
		"head drift": func(g *riskServer, _ *AcceptRiskRequest) { g.head = riskNewHead },
		"wrong head": func(g *riskServer, _ *AcceptRiskRequest) { g.decision["commit"] = map[string]any{"oid": riskNewHead} },
		"wrong PR": func(g *riskServer, _ *AcceptRiskRequest) {
			g.decision["pullRequest"] = map[string]any{"id": "PR_other"}
		},
		"wrong repo":      func(g *riskServer, _ *AcceptRiskRequest) { g.decision["repository"] = map[string]any{"id": "R_other"} },
		"wrong challenge": func(g *riskServer, _ *AcceptRiskRequest) { g.decision["body"] = riskStatement + "other\n\nReason" },
		"missing reason": func(g *riskServer, _ *AcceptRiskRequest) {
			g.decision["body"] = strings.Split(g.decision["body"].(string), "\n\n")[0]
		},
		"early decision": func(g *riskServer, _ *AcceptRiskRequest) { g.decision["createdAt"] = fixedNow() },
		"wrong state":    func(g *riskServer, _ *AcceptRiskRequest) { g.decision["state"] = "APPROVED" },
		"edited":         func(g *riskServer, _ *AcceptRiskRequest) { g.decision["lastEditedAt"] = g.now },
		"unknown edits":  func(g *riskServer, _ *AcceptRiskRequest) { delete(g.decision, "editor") },
		"wrong actor": func(g *riskServer, _ *AcceptRiskRequest) {
			g.decision["author"] = map[string]any{"id": "U_other", "login": "other", "databaseId": 33}
		},
		"shared actor":     func(g *riskServer, _ *AcceptRiskRequest) { g.viewerID = "U_admin" },
		"admin viewer":     func(g *riskServer, _ *AcceptRiskRequest) { g.canAdminister = true },
		"custom writer":    func(g *riskServer, _ *AcceptRiskRequest) { g.viewerRole = "branch-manager" },
		"policy bypass":    func(g *riskServer, _ *AcceptRiskRequest) { g.viewerBypass = "pull_requests_only" },
		"unknown bypass":   func(g *riskServer, _ *AcceptRiskRequest) { g.viewerBypass = "" },
		"no longer admin":  func(g *riskServer, _ *AcceptRiskRequest) { g.adminRole = "write" },
		"missing check":    func(g *riskServer, _ *AcceptRiskRequest) { g.checks[0]["name"] = "optional" },
		"empty rollup":     func(g *riskServer, _ *AcceptRiskRequest) { g.checks = []map[string]any{} },
		"truncated checks": func(g *riskServer, _ *AcceptRiskRequest) { g.incomplete = true },
		"pending check":    func(g *riskServer, _ *AcceptRiskRequest) { g.checks[0]["status"] = "IN_PROGRESS" },
		"skipped check":    func(g *riskServer, _ *AcceptRiskRequest) { g.checks[0]["conclusion"] = "SKIPPED" },
		"failed check":     func(g *riskServer, _ *AcceptRiskRequest) { g.checks[0]["conclusion"] = "FAILURE" },
		"wrong app": func(g *riskServer, _ *AcceptRiskRequest) {
			g.checks[0]["checkSuite"].(map[string]any)["app"] = map[string]any{"databaseId": 44}
		},
		"other-head CI": func(g *riskServer, _ *AcceptRiskRequest) {
			g.checks[0]["checkSuite"].(map[string]any)["commit"] = map[string]any{"oid": riskNewHead}
		},
		"duplicate name": func(g *riskServer, _ *AcceptRiskRequest) {
			g.checks = append(g.checks, map[string]any{"kind": "StatusContext", "context": "build"})
		},
		"changed policy": func(g *riskServer, _ *AcceptRiskRequest) { g.strict = false },
	}
	for name, change := range tests {
		t.Run(name, func(t *testing.T) {
			env, g, req, accept := newRiskFixture(t, true)
			holdRisk(t, env, g, req)
			change(g, &accept)
			if got, err := AcceptRisk(t.Context(), env, requestBytes(t, accept)); err == nil {
				t.Fatalf("accepted ineligible decision: %+v", got)
			}
			wantPhase(t, env.RepoRoot, 1, state.PhaseBlocked)
			if strings.Contains(g.issueBody, "risk-acceptance-1") {
				t.Fatal("ineligible decision wrote an acceptance")
			}
		})
	}
}

func TestResidualRiskChangedHeadRequiresFreshReviewAndDecisionIfRiskRemains(t *testing.T) {
	env, g, req, accept := newRiskFixture(t, true)
	old := holdRisk(t, env, g, req)
	g.head = riskNewHead
	g.green()
	req.ReviewedHeadOID = g.head
	next := holdRisk(t, env, g, req)
	if next.RiskChallenge == old.RiskChallenge || next.RiskBlockID != 2 {
		t.Fatalf("changed head reused hold: old=%+v new=%+v", old, next)
	}
	g.decision["body"] = fmt.Sprintf("%s%s\n\nStill possible", riskStatement, old.RiskChallenge)
	accept.BlockID = next.RiskBlockID
	if _, err := AcceptRisk(t.Context(), env, requestBytes(t, accept)); !errors.Is(err, ErrBadApproval) {
		t.Fatalf("accepted old challenge at new head: %v", err)
	}
	g.submit(next.RiskChallenge)
	if _, err := AcceptRisk(t.Context(), env, requestBytes(t, accept)); err != nil {
		t.Fatal(err)
	}
}

func TestResidualRiskRefusesOtherBlocksAndEarlierException(t *testing.T) {
	env, g, req, accept := newRiskFixture(t, true)
	m := baseManifest()
	m.Verifications = []manifest.Verification{{Name: "ci-exception-27", Result: CIExceptionStatement, CommitOID: riskHead, At: fixedNow().Format(time.RFC3339)}}
	body, err := manifest.Upsert(g.issueBody, m)
	if err != nil {
		t.Fatal(err)
	}
	g.issueBody, g.prBody = body, body
	if _, err := Review(t.Context(), env, requestBytes(t, req)); !errors.Is(err, ErrBadApproval) {
		t.Fatalf("accepted earlier exception in risk hold: %v", err)
	}
	if _, err := Block(t.Context(), env, requestBytes(t, BlockRequest{BlockSchemaVersion, 1, "human-decision", "Unresolved defect"})); err != nil {
		t.Fatal(err)
	}
	if _, err := AcceptRisk(t.Context(), env, requestBytes(t, accept)); !errors.Is(err, ErrBadApproval) {
		t.Fatalf("accept-risk accepted an ordinary block: %v", err)
	}
}

func TestResidualRiskInvalidatedByNewBlockOrRepairGeneration(t *testing.T) {
	t.Run("new blocker", func(t *testing.T) {
		env, g, req, accept := newRiskFixture(t, true)
		holdRisk(t, env, g, req)
		if _, err := Block(t.Context(), env, requestBytes(t, BlockRequest{BlockSchemaVersion, 1, "validation", "New regression evidence"})); err != nil {
			t.Fatal(err)
		}
		if _, err := AcceptRisk(t.Context(), env, requestBytes(t, accept)); !errors.Is(err, ErrBadApproval) {
			t.Fatalf("accepted superseded blocker: %v", err)
		}
	})
	t.Run("new repair", func(t *testing.T) {
		env, g, req, accept := newRiskFixture(t, true)
		holdRisk(t, env, g, req)
		if _, err := AcceptRisk(t.Context(), env, requestBytes(t, accept)); err != nil {
			t.Fatal(err)
		}
		st := loadRun(t, env.RepoRoot)
		issue := &st.Run.Issues[0]
		issue.Repairs = []state.IntegrationRepair{{Number: 1, PhaseBefore: state.PhaseAwaitingMerge, HeadOID: riskHead, At: g.now.Format(time.RFC3339),
			ReviewCycles: issue.ReviewCycles, AuthorizedBy: "human", Reason: "integrate without changing work"}}
		issue.LastReviewVerdict, issue.ReviewedHeadOID, issue.RiskReviewDigest = "", "", ""
		if err := state.Save(env.RepoRoot, st); err != nil {
			t.Fatal(err)
		}
		req.RiskHold, req.Verdict, req.RepairNumber = nil, VerdictApprove, 1
		req.RiskFollowUp = &RiskFollowUp{1, riskAcknowledged, "I saw the old receipt."}
		if _, err := Review(t.Context(), env, requestBytes(t, req)); !errors.Is(err, ErrReviewStale) {
			t.Fatalf("reused old receipt after repair: %v", err)
		}
	})
}

func TestResidualRiskApprovalChangedHeadMayExplainAbsence(t *testing.T) {
	env, g, req, accept := newRiskFixture(t, true)
	holdRisk(t, env, g, req)
	g.head = riskNewHead
	g.green()
	req.ReviewedHeadOID, req.Verdict, req.RiskHold = g.head, VerdictApprove, nil
	req.RiskFollowUp = &RiskFollowUp{1, riskAddressed, "The new serialization makes the trigger impossible."}
	if result, err := Review(t.Context(), env, requestBytes(t, req)); err != nil || result.Phase != state.PhaseInReview {
		t.Fatalf("changed head approval = %+v, %v", result, err)
	}
	if _, err := AcceptRisk(t.Context(), env, requestBytes(t, accept)); !errors.Is(err, ErrWrongPhase) {
		t.Fatalf("superseded hold got accepted: %v", err)
	}
	if _, err := MergeReport(t.Context(), env, requestBytes(t, MergeReportRequest{SchemaVersion: MergeReportSchemaVersion, IssueNumber: 1})); err != nil {
		t.Fatal(err)
	}
}

func TestResidualRiskDoesNotMigrateAnOlderActiveContract(t *testing.T) {
	env, _, req, _ := newRiskFixture(t, true)
	st := loadRun(t, env.RepoRoot)
	st.Run.Plan.ContractVersion = 4
	if err := state.Save(env.RepoRoot, st); err != nil {
		t.Fatal(err)
	}
	before := string(stateBytes(t, env.RepoRoot))
	if doc, err := Status(t.Context(), env); err != nil || doc.Run.Plan.ContractVersion != 4 {
		t.Fatalf("older run inspection = %+v, %v", doc, err)
	}
	if _, err := Review(t.Context(), env, requestBytes(t, req)); !errors.Is(err, ErrBadApproval) {
		t.Fatalf("old contract review = %v", err)
	}
	if _, err := Resume(t.Context(), env, ResumeRequest{}); !errors.Is(err, ErrBadApproval) {
		t.Fatalf("old contract resume = %v", err)
	}
	if string(stateBytes(t, env.RepoRoot)) != before {
		t.Fatal("active older run was rewritten")
	}
}

func TestResidualRiskAuditSurvivesOrdinaryDetailRotation(t *testing.T) {
	m := baseManifest()
	const note = "important risk reasoning"
	for _, name := range []string{"risk-hold-999", "risk-acceptance-999", "risk-review-999"} {
		m.Verifications = append(m.Verifications, manifest.Verification{Name: name, Result: "recorded", Detail: note})
	}
	for i := range 4 {
		m.Verifications = append(m.Verifications, manifest.Verification{Name: fmt.Sprintf("ordinary-%d", i), Result: "pass", Detail: strings.Repeat("x", 10000)})
	}
	body, err := upsertCapped("original prose", m)
	if err != nil {
		t.Fatal(err)
	}
	for _, v := range m.Verifications[:3] {
		if got := findVerification(t, body, v.Name); got.Detail != note {
			t.Fatalf("%s was rotated", v.Name)
		}
	}
	if got := findVerification(t, body, "ordinary-0"); got.Detail != "" {
		t.Fatal("fixture did not exercise ordinary detail rotation")
	}
}
