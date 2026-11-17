package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/kninetimmy/orch/internal/execx"
	"github.com/kninetimmy/orch/internal/lockfile"
	"github.com/kninetimmy/orch/internal/manifest"
	"github.com/kninetimmy/orch/internal/metrics"
	"github.com/kninetimmy/orch/internal/run"
	"github.com/kninetimmy/orch/internal/state"
)

// Real isolated git repositories, the actual CLI/serializer/lifecycle, and a
// deterministic GitHub boundary. No command can reach a hosted repository.
type ciExceptionGitHub struct {
	t                        *testing.T
	root, head, branch       string
	issueBody, prBody        string
	issueState, prState      string
	mergedAt, bucket         string
	failRead, checkData      string
	noRequired               bool
	failMirror, failMerge    bool
	prReads, changeOnPRRead  int
	mutations, mergeAttempts int
}

func (g *ciExceptionGitHub) Run(ctx context.Context, cmd execx.Cmd) (execx.Result, error) {
	if cmd.Name == "git" {
		return (execx.Local{}).Run(ctx, cmd)
	}
	if cmd.Name != "gh" {
		g.t.Fatalf("unexpected external command: %+v", cmd)
	}
	args := strings.Join(cmd.Args, " ")
	if args == g.failRead {
		return execx.Result{ExitCode: 1, Stderr: "scripted authentication/transport failure"}, nil
	}
	var out any
	switch args {
	case "auth status":
		return execx.Result{}, nil
	case "repo view --json nameWithOwner,defaultBranchRef,url":
		out = map[string]any{"nameWithOwner": "o/r", "defaultBranchRef": map[string]string{"name": "main"}, "url": "https://github.com/o/r"}
	case "issue view 1 --json number,state,title,url,labels,body":
		out = map[string]any{"number": 1, "state": g.issueState, "title": "Fixture", "url": "https://github.com/o/r/issues/1", "labels": []any{}, "body": g.issueBody}
	case "pr view 10 --json number,state,title,url,headRefName,baseRefName,headRefOid,mergeStateStatus,mergedAt,body":
		g.prReads++
		if g.changeOnPRRead == g.prReads {
			g.head = strings.Repeat("e", 40)
		}
		out = map[string]any{"number": 10, "state": g.prState, "title": "Fixture", "url": "https://github.com/o/r/pull/10",
			"headRefName": g.branch, "baseRefName": "main", "headRefOid": g.head, "mergeStateStatus": "CLEAN", "mergedAt": g.mergedAt, "body": g.prBody}
	case "pr view 10 --json statusCheckRollup":
		out = map[string]any{"statusCheckRollup": []any{map[string]string{}, map[string]string{}}}
	case "pr checks 10 --required --json name,state,bucket,link":
		if g.noRequired {
			return execx.Result{ExitCode: 1, Stdout: " \n\t", Stderr: "no required checks reported on the '" + g.branch + "' branch\n"}, nil
		}
		if g.checkData != "" {
			return execx.Result{Stdout: g.checkData}, nil
		}
		code, checkState := 1, "FAILURE"
		switch g.bucket {
		case "pending":
			code, checkState = 8, "IN_PROGRESS"
		case "cancel":
			checkState = "CANCELLED"
		case "pass":
			code, checkState = 0, "SUCCESS"
		}
		data, err := json.Marshal([]map[string]string{{"name": "test", "state": checkState, "bucket": g.bucket, "link": "https://checks/test"}})
		return execx.Result{ExitCode: code, Stdout: string(data)}, err
	case "issue edit 1 --body-file -", "pr edit 10 --body-file -":
		if args == "pr edit 10 --body-file -" && g.failMirror {
			g.failMirror = false
			return execx.Result{ExitCode: 1, Stderr: "scripted partial mirror failure"}, nil
		}
		body, err := io.ReadAll(cmd.Stdin)
		if err != nil {
			return execx.Result{}, err
		}
		g.mutations++
		if cmd.Args[0] == "issue" {
			g.issueBody = string(body)
		} else {
			if g.prState != "OPEN" {
				g.t.Fatal("attempted to edit a merged PR audit mirror")
			}
			g.prBody = string(body)
		}
		return execx.Result{}, nil
	case "issue close 1":
		g.mutations++
		g.issueState = "CLOSED"
		return execx.Result{}, nil
	case "pr merge 10 --squash --match-head-commit " + g.head:
		g.mergeAttempts++
		if g.failMerge {
			return execx.Result{ExitCode: 1, Stderr: "GitHub branch protection refused merge"}, nil
		}
		g.mergeOutOfBand()
		g.mutations++
		return execx.Result{}, nil
	default:
		if strings.HasPrefix(args, "issue edit 1 --remove-label ") {
			g.mutations++
			return execx.Result{}, nil
		}
		g.t.Fatalf("unexpected GitHub command (no bypass allowed): %s", args)
	}
	data, err := json.Marshal(out)
	return execx.Result{Stdout: string(data)}, err
}

func (g *ciExceptionGitHub) mergeOutOfBand() {
	g.t.Helper()
	lookupTestGit(g.t, g.root, "push", "origin", g.head+":refs/heads/main")
	g.prState, g.mergedAt = "MERGED", time.Now().UTC().Format(time.RFC3339)
}

func ciExceptionFixture(t *testing.T) (Env, *ciExceptionGitHub) {
	t.Helper()
	global := filepath.Join(t.TempDir(), "gitconfig")
	mcpWrite(t, global, []byte("[user]\nname = Test\nemail = test@example.invalid\n[core]\nautocrlf = false\n"))
	t.Setenv("GIT_CONFIG_GLOBAL", global)
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	env, _, _ := testEnv(t)
	root := env.RepoRoot
	writeConfig(t, root, validTOML+"\n[metrics]\nenabled = true\n")
	lookupTestGit(t, root, "init", "-b", "main")
	mcpWrite(t, filepath.Join(root, ".gitignore"), []byte(".orchestrator/\n.claude/agents/\n.codex/agents/\n.opencode/agents/\n"))
	lookupTestGit(t, root, "add", ".gitignore")
	lookupTestGit(t, root, "commit", "-m", "Initial fixture")
	origin := filepath.Join(t.TempDir(), "origin.git")
	lookupTestGit(t, root, "init", "--bare", origin)
	lookupTestGit(t, root, "remote", "add", "origin", origin)
	lookupTestGit(t, root, "push", "origin", "main")
	issue := state.Issue{PlanID: "a", Title: "Fixture", Phase: state.PhasePlanned, Number: 1, URL: "https://github.com/o/r/issues/1",
		Branch: "orch/issue-1", Worktree: ".orchestrator/worktrees/issue-1", PRNumber: 10, PRURL: "https://github.com/o/r/pull/10",
		Objective: "Implement the approved fixture", AcceptanceCriteria: []string{"The fixture works"}, RequiredTests: []string{"go test ./..."},
		Decision: &state.Decision{Role: manifest.RoleImplementer, Executor: manifest.Selection{Model: "claude-sonnet-5", Effort: "xhigh"},
			Reviewer: manifest.Selection{Model: "claude-opus-4-8", Effort: "high"}, Rationale: "fixture"}}
	wt := filepath.Join(root, filepath.FromSlash(issue.Worktree))
	lookupTestGit(t, root, "worktree", "add", "-b", issue.Branch, wt)
	mcpWrite(t, filepath.Join(wt, "feature.txt"), []byte("approved implementation\n"))
	lookupTestGit(t, wt, "add", "feature.txt")
	lookupTestGit(t, wt, "commit", "-m", "Implement fixture")
	head := lookupTestGit(t, wt, "rev-parse", "HEAD")
	lookupTestGit(t, wt, "push", "origin", issue.Branch)
	st, err := state.EnterDelivery(root, "claude", testApprovedPlanRef(t, root), []state.Issue{issue})
	if err != nil {
		t.Fatal(err)
	}
	st.Run.Issues[0].Phase = state.PhasePROpen
	if err := state.Save(root, st); err != nil {
		t.Fatal(err)
	}
	m := manifest.Manifest{SchemaVersion: manifest.SchemaVersion, Objective: issue.Objective, AcceptanceCriteria: issue.AcceptanceCriteria,
		RequiredTests: issue.RequiredTests, Role: issue.Decision.Role, Executor: issue.Decision.Executor, Reviewer: issue.Decision.Reviewer,
		RoutingRationale: issue.Decision.Rationale, ConfigRevision: "r1", EffortDelivery: manifest.EffortDeliveryParameter}
	body, err := manifest.Upsert("original prose\n", m)
	if err != nil {
		t.Fatal(err)
	}
	gh := &ciExceptionGitHub{t: t, root: root, head: head, branch: issue.Branch, issueBody: body, prBody: body, issueState: "OPEN", prState: "OPEN", bucket: "cancel"}
	env.Runner = gh
	review := run.ReviewRequest{SchemaVersion: 3, IssueNumber: 1, ReviewedHeadOID: head, Verdict: run.VerdictApprove,
		Summary: "Independent review of the fixture", Reviewer: issue.Decision.Reviewer,
		Judgments: []run.CriterionJudgment{{Criterion: 1, Judgment: run.JudgmentSatisfied, Reason: "exercised the fixture"}}}
	ciExceptionInvoke(t, env, "review", review, ExitOK, "")
	return env, gh
}

func ciExceptionInvoke(t *testing.T, env Env, verb string, request any, wantCode int, wantError string) []byte {
	t.Helper()
	var stdout, stderr bytes.Buffer
	env.Stdout, env.Stderr = &stdout, &stderr
	env.Stdin = bytes.NewReader(mcpEvalBytes(t, request))
	args := []string{"run", verb}
	if verb == "resume" {
		args = []string{"resume", "--json"}
	}
	code := Run(args, env)
	if code != wantCode || (wantError != "" && !strings.Contains(stderr.String(), wantError)) {
		t.Fatalf("%v exit=%d stdout=%s stderr=%s, want exit=%d error=%q", args, code, stdout.String(), stderr.String(), wantCode, wantError)
	}
	if code != ExitOK && stdout.Len() != 0 {
		t.Fatalf("failed command returned success evidence: %s", stdout.String())
	}
	return stdout.Bytes()
}

func ciExceptionState(t *testing.T, root string) *state.State {
	t.Helper()
	st, err := state.Load(root)
	if err != nil {
		t.Fatal(err)
	}
	return st
}

func ciExceptionDecision(t *testing.T, env Env, gh *ciExceptionGitHub, statement string) *run.HumanCIException {
	t.Helper()
	return &run.HumanCIException{RunID: ciExceptionState(t, env.RepoRoot).Run.ID, IssueNumber: 1, PRNumber: 10, HeadOID: gh.head,
		ApprovedBy: "fixture-owner", ApprovedAt: time.Now().UTC(), Reason: "Accept the unavailable runner for this reviewed commit only", Statement: statement}
}

func ciExceptionManifest(t *testing.T, body string) manifest.Manifest {
	t.Helper()
	m, err := manifest.Parse(body)
	if err != nil {
		t.Fatal(err)
	}
	return m
}

func TestManualCIExceptionCLIThroughCompletion(t *testing.T) {
	for _, scenario := range []string{"pending", "fail", "cancel", "already merged", "already merged operational block", "already merged closed issue", "already merged no required exit1"} {
		t.Run(scenario, func(t *testing.T) {
			env, gh := ciExceptionFixture(t)
			st := ciExceptionState(t, env.RepoRoot)
			plan := st.Run.Plan
			if scenario == "pending" || scenario == "fail" || scenario == "cancel" {
				gh.bucket = scenario
			}
			ciExceptionInvoke(t, env, "ci", run.CIRequest{SchemaVersion: 1, IssueNumber: 1}, ExitOK, "")
			beforeCI := ciExceptionManifest(t, gh.issueBody).Verifications
			if strings.HasPrefix(scenario, "already merged") {
				gh.mergeOutOfBand()
				if scenario == "already merged no required exit1" {
					gh.noRequired = true
				}
				if scenario == "already merged closed issue" {
					gh.issueState = "CLOSED"
				}
				ciExceptionInvoke(t, env, "merge", run.MergeRequest{SchemaVersion: 1, IssueNumber: 1,
					Approval: run.MergeApproval{Statement: run.MergeApprovalStatement}}, ExitError, "phase in-review")
				if scenario == "already merged operational block" {
					out := ciExceptionInvoke(t, env, "resume", nil, ExitOK, "")
					if !strings.Contains(string(out), "merged out of band") || ciExceptionState(t, env.RepoRoot).Run.Issues[0].Phase != state.PhaseBlocked {
						t.Fatal("resume adopted an unapproved out-of-band merge")
					}
				}
				ack := ciExceptionDecision(t, env, gh, run.MergedAcknowledgmentStatement)
				if gh.issueState == "OPEN" {
					gh.failRead = "issue close 1"
					ciExceptionInvoke(t, env, "merge", run.MergeRequest{SchemaVersion: 1, IssueNumber: 1, MergedAcknowledgment: ack}, ExitError, "scripted authentication/transport failure")
					if got := ciExceptionState(t, env.RepoRoot).Run.Issues[0]; got.ApprovedHeadOID != "" || got.Phase == state.PhaseMerged {
						t.Fatal("partial acknowledgment fabricated a prior pin or marked closure complete")
					}
					gh.failRead = ""
				}
				out := ciExceptionInvoke(t, env, "merge", run.MergeRequest{SchemaVersion: 1, IssueNumber: 1, MergedAcknowledgment: ack}, ExitOK, "")
				if !strings.Contains(string(out), "merged-acknowledgment-1") || gh.mergeAttempts != 0 {
					t.Fatalf("acknowledgment merged or lost evidence: %s", out)
				}
				first := ciExceptionManifest(t, gh.issueBody)
				// A successful acknowledgment is retryable without another merge,
				// a fabricated prior approval, or replacement of its first receipt.
				ciExceptionInvoke(t, env, "merge", run.MergeRequest{SchemaVersion: 1, IssueNumber: 1, MergedAcknowledgment: ack}, ExitOK, "")
				after := ciExceptionManifest(t, gh.issueBody)
				if !reflect.DeepEqual(first.Verifications[len(first.Verifications)-2], after.Verifications[len(after.Verifications)-2]) {
					t.Fatal("identical acknowledgment retry changed its durable receipt")
				}
			} else {
				ciExceptionInvoke(t, env, "merge-report", run.MergeReportRequest{SchemaVersion: 1, IssueNumber: 1}, ExitError, "required CI is")
				decision := ciExceptionDecision(t, env, gh, run.CIExceptionStatement)
				report := ciExceptionInvoke(t, env, "merge-report", run.MergeReportRequest{SchemaVersion: 1, IssueNumber: 1, CIException: decision}, ExitOK, "")
				if !strings.Contains(string(report), "ci-exception-1") || !strings.Contains(string(report), `"bucket": "`+gh.bucket+`"`) {
					t.Fatalf("report hid actual CI/exception: %s", report)
				}
				approval := run.MergeApproval{PRNumber: 10, HeadOID: gh.head, ApprovedBy: "fixture-owner", ApprovedAt: time.Now().UTC(), Statement: run.MergeApprovalStatement}
				// A recorded exception is audit, not persistent permission.
				ciExceptionInvoke(t, env, "merge", run.MergeRequest{SchemaVersion: 1, IssueNumber: 1, Approval: approval}, ExitError, "required CI is")
				req := run.MergeRequest{SchemaVersion: 1, IssueNumber: 1, Approval: approval, CIException: decision}
				gh.failMerge = true
				ciExceptionInvoke(t, env, "merge", req, ExitError, "GitHub branch protection refused merge")
				if ciExceptionState(t, env.RepoRoot).Run.Issues[0].Phase != state.PhaseAwaitingMerge || gh.prState != "OPEN" {
					t.Fatal("refused GitHub merge was marked delivered")
				}
				gh.failMerge = false
				out := ciExceptionInvoke(t, env, "merge", req, ExitOK, "")
				if !strings.Contains(string(out), "ci-exception-1") || gh.mergeAttempts != 2 {
					t.Fatalf("merge retry lost audit: %s", out)
				}
			}
			final := ciExceptionState(t, env.RepoRoot)
			if final.Run.Plan != plan || final.SchemaVersion != 6 || final.Run.Issues[0].Phase != state.PhaseMerged || gh.issueState != "CLOSED" {
				t.Fatal("completion changed approval contracts or did not close the merged issue")
			}
			data, err := os.ReadFile(filepath.Join(env.RepoRoot, metrics.Dir, st.Run.ID+".json"))
			if err != nil {
				t.Fatal(err)
			}
			var recorded metrics.Document
			if err := json.Unmarshal(data, &recorded); err != nil {
				t.Fatal(err)
			}
			merges := 0
			for _, event := range recorded.Events {
				if event.Verb == "merge" {
					merges++
					if strings.HasPrefix(scenario, "already merged") {
						if event.ApprovalSource != "" || event.Reason != run.MergedAcknowledgmentStatement {
							t.Fatal("post-merge metrics fabricated prior merge approval")
						}
					} else if event.ApprovalSource != "human" {
						t.Fatal("ordinary human merge lost its approval source")
					}
				}
			}
			if merges == 0 {
				t.Fatal("completed merge/acknowledgment was not observed in enabled metrics")
			}
			after := ciExceptionManifest(t, gh.issueBody)
			for _, v := range beforeCI {
				found := false
				for _, got := range after.Verifications {
					if got.Name == v.Name && reflect.DeepEqual(got, v) {
						found = true
					}
				}
				if !found {
					t.Fatalf("exception changed original CI/review evidence: %+v", v)
				}
			}
			if _, err := os.Stat(filepath.Join(env.RepoRoot, ".orchestrator", "worktrees", "issue-1")); err != nil {
				t.Fatal("merge/acknowledgment implicitly cleaned up the worktree")
			}
			ciExceptionInvoke(t, env, "complete", run.CompleteRequest{SchemaVersion: 1}, ExitError, "not cleaned")
			ciExceptionInvoke(t, env, "cleanup", run.CleanupRequest{SchemaVersion: 1, IssueNumber: 1, Statement: "cleanup-issue"}, ExitOK, "")
			out := ciExceptionInvoke(t, env, "complete", run.CompleteRequest{SchemaVersion: 1}, ExitOK, "")
			if !strings.Contains(string(out), `"merged": 1`) || ciExceptionState(t, env.RepoRoot).Mode != state.ModeAssist {
				t.Fatalf("run did not complete normally: %s", out)
			}
			if owner, err := lockfile.Inspect(env.RepoRoot); owner != nil || err != nil {
				t.Fatalf("Delivery lock remains: %+v %v", owner, err)
			}
			if got := lookupTestGit(t, env.RepoRoot, "rev-parse", "HEAD"); got != gh.head {
				t.Fatal("completion did not fast-forward to the delivered implementation")
			}
			t.Logf("CLI %s: independent review -> explicit human decision -> merged/closed -> separate cleanup -> complete; state6/contract2 plan unchanged", scenario)
		})
	}
}

func TestManualCIExceptionCLIRefusals(t *testing.T) {
	for _, entry := range []string{"merge-report", "merge", "acknowledge"} {
		for _, name := range []string{"wrong run", "wrong issue", "wrong PR", "short head", "wrong head", "empty reason", "empty approver", "grant actor",
			"wrong statement", "missing time", "future time", "pre-review time", "stale review", "missing judgment", "unsatisfied judgment", "wrong work",
			"unresolved decision", "secret stop", "legacy contract", "legacy state", "config drift", "repair replay", "repair stale review",
			"auth failure", "PR failure", "issue failure", "repo failure", "rollup failure", "checks failure", "malformed checks", "changed during probe",
			"oversized reason", "wrong live branch", "wrong registered issue URL", "wrong registered PR URL"} {
			t.Run(entry+"/"+name, func(t *testing.T) {
				env, gh := ciExceptionFixture(t)
				st := ciExceptionState(t, env.RepoRoot)
				if entry == "merge" {
					st.Run.Issues[0].Phase, st.Run.Issues[0].ApprovedHeadOID = state.PhaseAwaitingMerge, gh.head
				}
				statement := run.CIExceptionStatement
				if entry == "acknowledge" {
					gh.mergeOutOfBand()
					statement = run.MergedAcknowledgmentStatement
				}
				decision := ciExceptionDecision(t, env, gh, statement)
				m := ciExceptionManifest(t, gh.issueBody)
				switch name {
				case "wrong run":
					decision.RunID = "another-run"
				case "wrong issue":
					decision.IssueNumber++
				case "wrong PR":
					decision.PRNumber++
				case "short head":
					decision.HeadOID = "HEAD"
				case "wrong head":
					decision.HeadOID = strings.Repeat("b", 40)
				case "empty reason":
					decision.Reason = " \t"
				case "empty approver":
					decision.ApprovedBy = " "
				case "grant actor":
					decision.ApprovedBy = "grant:fixture"
				case "wrong statement":
					decision.Statement = "approve-merge"
				case "missing time":
					decision.ApprovedAt = time.Time{}
				case "future time":
					decision.ApprovedAt = time.Now().Add(time.Hour)
				case "pre-review time":
					decision.ApprovedAt = time.Now().Add(-time.Hour)
				case "stale review":
					for i := range m.Verifications {
						m.Verifications[i].CommitOID = strings.Repeat("a", 40)
					}
				case "missing judgment":
					m.Verifications = m.Verifications[1:]
				case "unsatisfied judgment":
					m.Verifications[0].Result = run.JudgmentUnsatisfied
				case "wrong work":
					m.Objective = "different scope"
				case "unresolved decision":
					st.Run.Issues[0].Blocks = []state.Block{{ID: 1, Cause: state.BlockHuman, Reason: "owner decision outstanding"}}
				case "secret stop":
					st.Run.StoppedReason = "secret requires recovery"
				case "legacy contract":
					st.Run.Plan.ContractVersion = 1
				case "legacy state":
					st.SchemaVersion = 5
				case "config drift":
					st.Run.Plan.ConfigRevision = "other"
				case "repair replay", "repair stale review":
					st.Run.Issues[0].Repairs = []state.IntegrationRepair{{Number: 1, PhaseBefore: state.PhaseAwaitingMerge,
						HeadOID: gh.head, AuthorizedBy: "owner", Reason: "integration", At: time.Now().Add(-time.Hour).UTC().Format(time.RFC3339),
						ReviewCycles: 0, VerifiedHeadOID: gh.head}}
					if name == "repair stale review" {
						st.Run.Issues[0].Repairs[0].ReviewCycles = 1
						st.Run.Issues[0].Repairs[0].VerifiedHeadOID = ""
						decision.RepairNumber = 1
					}
				case "auth failure":
					gh.failRead = "auth status"
				case "PR failure":
					gh.failRead = "pr view 10 --json number,state,title,url,headRefName,baseRefName,headRefOid,mergeStateStatus,mergedAt,body"
				case "issue failure":
					gh.failRead = "issue view 1 --json number,state,title,url,labels,body"
				case "repo failure":
					gh.failRead = "repo view --json nameWithOwner,defaultBranchRef,url"
				case "rollup failure":
					gh.failRead = "pr view 10 --json statusCheckRollup"
				case "checks failure":
					gh.failRead = "pr checks 10 --required --json name,state,bucket,link"
				case "malformed checks":
					gh.checkData = "not JSON"
				case "changed during probe":
					gh.changeOnPRRead = gh.prReads + 2
				case "oversized reason":
					decision.Reason = strings.Repeat("x", 40000)
				case "wrong live branch":
					gh.branch = "another-branch"
				case "wrong registered issue URL":
					st.Run.Issues[0].URL = "https://github.com/other/repo/issues/1"
				case "wrong registered PR URL":
					st.Run.Issues[0].PRURL = "https://github.com/other/repo/pull/10"
				}
				var err error
				gh.issueBody, err = manifest.Upsert("original prose\n", m)
				if err != nil {
					t.Fatal(err)
				}
				gh.prBody = gh.issueBody
				if err := state.Save(env.RepoRoot, st); err != nil {
					t.Fatal(err)
				}
				before, err := os.ReadFile(filepath.Join(env.RepoRoot, state.Path))
				if err != nil {
					t.Fatal(err)
				}
				body, mirror, mutations := gh.issueBody, gh.prBody, gh.mutations
				verb := entry
				var request any = run.MergeReportRequest{SchemaVersion: 1, IssueNumber: 1, CIException: decision}
				switch entry {
				case "merge":
					request = run.MergeRequest{SchemaVersion: 1, IssueNumber: 1, CIException: decision,
						Approval: run.MergeApproval{PRNumber: 10, HeadOID: gh.head, ApprovedBy: "owner", ApprovedAt: time.Now().UTC(), Statement: run.MergeApprovalStatement}}
				case "acknowledge":
					verb, request = "merge", run.MergeRequest{SchemaVersion: 1, IssueNumber: 1, MergedAcknowledgment: decision}
				}
				ciExceptionInvoke(t, env, verb, request, ExitError, "")
				after, err := os.ReadFile(filepath.Join(env.RepoRoot, state.Path))
				if err != nil {
					t.Fatal(err)
				}
				if !bytes.Equal(before, after) || gh.issueBody != body || gh.prBody != mirror || gh.mutations != mutations || gh.mergeAttempts != 0 {
					t.Fatal("refused manual decision mutated state, audit, labels or merge")
				}
			})
		}
	}
}

func TestManualCIExceptionPartialMirrorRetry(t *testing.T) {
	for _, changed := range []bool{false, true} {
		t.Run(map[bool]string{false: "identical", true: "changed CI"}[changed], func(t *testing.T) {
			env, gh := ciExceptionFixture(t)
			decision := ciExceptionDecision(t, env, gh, run.CIExceptionStatement)
			req := run.MergeReportRequest{SchemaVersion: 1, IssueNumber: 1, CIException: decision}
			gh.failMirror = true
			ciExceptionInvoke(t, env, "merge-report", req, ExitError, "scripted partial mirror failure")
			if ciExceptionState(t, env.RepoRoot).Run.Issues[0].Phase != state.PhaseInReview || gh.issueBody == gh.prBody {
				t.Fatal("partial receipt write incorrectly pinned a gate")
			}
			first := ciExceptionManifest(t, gh.issueBody)
			receipt := first.Verifications[len(first.Verifications)-1]
			if changed {
				gh.bucket = "pending"
			}
			ciExceptionInvoke(t, env, "merge-report", req, ExitOK, "")
			m := ciExceptionManifest(t, gh.issueBody)
			count, retained := 0, false
			for _, v := range m.Verifications {
				if strings.HasPrefix(v.Name, "ci-exception-") {
					count++
					retained = retained || reflect.DeepEqual(v, receipt)
				}
			}
			want := 1
			if changed {
				want = 2
			}
			if count != want || !retained || gh.issueBody != gh.prBody {
				t.Fatal("retry lost/duplicated its receipt, erased old CI or failed to restore the mirror")
			}
		})
	}
}

func TestManualCIExceptionGrantAndAcknowledgmentBoundaries(t *testing.T) {
	for _, name := range []string{"grant merge", "grant acknowledgment", "open acknowledgment", "unrelated operational block", "legacy unknown block", "before merge acknowledgment", "mixed approval", "different pin"} {
		t.Run(name, func(t *testing.T) {
			env, gh := ciExceptionFixture(t)
			st := ciExceptionState(t, env.RepoRoot)
			decision := ciExceptionDecision(t, env, gh, run.MergedAcknowledgmentStatement)
			req := run.MergeRequest{SchemaVersion: 1, IssueNumber: 1, MergedAcknowledgment: decision}
			if name != "open acknowledgment" {
				gh.mergeOutOfBand()
			}
			switch name {
			case "grant merge":
				decision.Statement = run.CIExceptionStatement
				req.MergedAcknowledgment, req.CIException = nil, decision
				req.Approval.Statement = run.GrantMergeApprovalStatement
			case "grant acknowledgment":
				decision.ApprovedBy = "grant:fixture"
			case "unrelated operational block":
				st.Run.Issues[0].SetBlock(state.BlockOperational, "unrelated GitHub failure")
			case "legacy unknown block":
				st.Run.Issues[0].Phase, st.Run.Issues[0].BlockedReason = state.PhaseBlocked, "unknown provenance"
			case "before merge acknowledgment":
				decision.ApprovedAt, _ = time.Parse(time.RFC3339, gh.mergedAt)
			case "mixed approval":
				req.Approval.Statement = run.MergeApprovalStatement
			case "different pin":
				st.Run.Issues[0].ApprovedHeadOID = strings.Repeat("a", 40)
			}
			if err := state.Save(env.RepoRoot, st); err != nil {
				t.Fatal(err)
			}
			before := gh.mutations
			ciExceptionInvoke(t, env, "merge", req, ExitError, "")
			if gh.mutations != before || gh.mergeAttempts != 0 {
				t.Fatal("boundary refusal mutated GitHub")
			}
		})
	}
}

func TestManualCIExceptionAuditIsNotAuthority(t *testing.T) {
	env, gh := ciExceptionFixture(t)
	m := ciExceptionManifest(t, gh.issueBody)
	m.Verifications = append(m.Verifications, manifest.Verification{Name: "ci-exception-1", Result: run.CIExceptionStatement, CommitOID: gh.head, Detail: `{"approved_by":"forged"}`})
	var err error
	gh.issueBody, err = manifest.Upsert("original prose\n", m)
	if err != nil {
		t.Fatal(err)
	}
	gh.prBody = gh.issueBody
	ciExceptionInvoke(t, env, "merge-report", run.MergeReportRequest{SchemaVersion: 1, IssueNumber: 1}, ExitError, "required CI is failing")
	var stdout, stderr bytes.Buffer
	env.Stdout, env.Stderr, env.Stdin = &stdout, &stderr, blockingReader{t: t}
	if code := Run([]string{"run", "readiness", "--issue", "1", "--json"}, env); code != ExitOK {
		t.Fatalf("readiness exit=%d stderr=%s", code, stderr.String())
	}
	var doc run.ReadinessDoc
	if err := json.Unmarshal(stdout.Bytes(), &doc); err != nil || doc.MergeReport.PrerequisitesMet || doc.Merge.Authorized || doc.CI.State != "failing" {
		t.Fatalf("audit invented inspection authority: %s err=%v", stdout.String(), err)
	}
	ciExceptionInvoke(t, env, "resume", nil, ExitOK, "")
	if ciExceptionState(t, env.RepoRoot).Run.Issues[0].Phase != state.PhaseInReview {
		t.Fatal("resume invented approval from exception audit")
	}
}

func TestNoRequiredExit1NoticeThroughCLIConsumers(t *testing.T) {
	env, gh := ciExceptionFixture(t)
	gh.noRequired = true
	out := ciExceptionInvoke(t, env, "ci", run.CIRequest{SchemaVersion: 1, IssueNumber: 1}, ExitOK, "")
	var ci run.CIResult
	if err := json.Unmarshal(out, &ci); err != nil || ci.State != "no-checks" || ci.Total != 2 || len(ci.Required) != 0 {
		t.Fatalf("CI=%s err=%v", out, err)
	}
	var stdout, stderr bytes.Buffer
	env.Stdout, env.Stderr, env.Stdin = &stdout, &stderr, blockingReader{t: t}
	if code := Run([]string{"run", "readiness", "--issue", "1", "--json"}, env); code != ExitOK {
		t.Fatalf("readiness exit=%d stderr=%s", code, stderr.String())
	}
	var readiness run.ReadinessDoc
	if err := json.Unmarshal(stdout.Bytes(), &readiness); err != nil || !readiness.MergeReport.PrerequisitesMet ||
		readiness.Merge.Authorized || readiness.CI.State != "no-checks" || readiness.CI.Total != 2 {
		t.Fatalf("readiness=%s err=%v", stdout.String(), err)
	}
	out = ciExceptionInvoke(t, env, "resume", nil, ExitOK, "")
	if !strings.Contains(string(out), `"ci_state": "no-checks"`) || ciExceptionState(t, env.RepoRoot).Run.Issues[0].Phase != state.PhaseInReview {
		t.Fatalf("resume invented approval or lost CI: %s", out)
	}
	out = ciExceptionInvoke(t, env, "merge-report", run.MergeReportRequest{SchemaVersion: 1, IssueNumber: 1}, ExitOK, "")
	var report run.MergeReportResult
	if err := json.Unmarshal(out, &report); err != nil || report.CI.State != "no-checks" || report.CI.Total != 2 || report.NoCIStatement == "" || report.CIException != nil {
		t.Fatalf("report=%s err=%v", out, err)
	}
	ciExceptionInvoke(t, env, "merge", run.MergeRequest{SchemaVersion: 1, IssueNumber: 1,
		Approval: run.MergeApproval{PRNumber: 10, HeadOID: gh.head, ApprovedBy: "owner", ApprovedAt: time.Now().UTC(), Statement: run.MergeApprovalStatement}}, ExitOK, "")
	if len(ciExceptionManifest(t, gh.issueBody).Verifications) != 4 {
		t.Fatal("ordinary no-required-checks flow invented an exception receipt")
	}
}

func TestManualCIHelpDescribesSeparateRequests(t *testing.T) {
	env, stdout, stderr := testEnv(t)
	env.Stdin = blockingReader{t: t}
	if code := Run([]string{"run", "help"}, env); code != ExitOK || stderr.Len() != 0 {
		t.Fatalf("help exit=%d stderr=%s", code, stderr.String())
	}
	for _, want := range []string{`"ci_exception"`, `"run_id"`, `"issue_number"`, `"pr_number"`, `"head_oid"`, `"approved_by"`, `"approved_at"`, `"reason"`,
		`"repair_number"`, `"accept-incomplete-ci"`, "separate fresh approve-merge", "merged_acknowledgment", `"acknowledge-already-merged-pr"`, "AFTER the merge", "omit approval", "separate cleanup"} {
		if !strings.Contains(stdout.String(), want) {
			t.Fatalf("help omits %q: %s", want, stdout.String())
		}
	}
}
