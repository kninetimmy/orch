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

	"github.com/kninetimmy/orch/internal/config"
	"github.com/kninetimmy/orch/internal/execx"
	"github.com/kninetimmy/orch/internal/execx/execxtest"
	"github.com/kninetimmy/orch/internal/ghops"
	"github.com/kninetimmy/orch/internal/grant"
	"github.com/kninetimmy/orch/internal/manifest"
	"github.com/kninetimmy/orch/internal/state"
)

const grantSession = "session-grant"

func openGrants(t *testing.T, root string) *grant.Store {
	t.Helper()
	s, err := grant.Open(context.Background(), execx.Local{}, root)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

// seedGrant creates a grant in root's clone (plan and merge gates, two runs,
// two merges, active at fixedNow unless edit says otherwise) and makes this
// test's Claude Code session its holder. A grant edited to expire before
// fixedNow is created an hour before it expires.
func seedGrant(t *testing.T, root string, edit func(*grant.Proposal)) *grant.Grant {
	t.Helper()
	t.Setenv(grant.SessionEnv, grantSession)
	p := grant.Proposal{
		SchemaVersion: grant.SchemaVersion, Scope: []grant.ScopeItem{{Name: "issue-353", Description: "grant approvals"}},
		Gates: []string{"plan", "merge"}, ExpiresAt: fixedNow().Add(8 * time.Hour), RunLimit: 2, MergeLimit: 2,
		RelayPermissionMode: "acceptEdits",
	}
	if edit != nil {
		edit(&p)
	}
	created := fixedNow()
	if !p.ExpiresAt.After(created) {
		created = p.ExpiresAt.Add(-time.Hour)
	}
	pv, err := grant.MakePreview(p, grantSession, created)
	if err != nil {
		t.Fatal(err)
	}
	g, err := openGrants(t, root).Create(&grant.CreateRequest{SchemaVersion: grant.SchemaVersion, Terms: pv.Terms, Approval: grant.Approval{
		GrantDigest: pv.Digest, ApprovedBy: "kninetimmy", ApprovedAt: created, Statement: grant.ApprovalStatement,
	}}, grantSession, created)
	if err != nil {
		t.Fatal(err)
	}
	return g
}

func getGrant(t *testing.T, root, id string) *grant.Grant {
	t.Helper()
	g, err := openGrants(t, root).Get(id)
	if err != nil {
		t.Fatal(err)
	}
	return g
}

// activationRequest is a valid activation request for planJSON carrying
// approvedBy and statement.
func activationRequest(t *testing.T, root, planJSON, approvedBy, statement string) []byte {
	t.Helper()
	p, err := DecodePlan([]byte(planJSON))
	if err != nil {
		t.Fatal(err)
	}
	cfg, err := config.Load(root)
	if err != nil {
		t.Fatal(err)
	}
	contract, err := effectiveContract(p, cfg)
	if err != nil {
		t.Fatal(err)
	}
	digest, err := contentDigest(contract)
	if err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(map[string]any{
		"schema_version": ActivationSchemaVersion,
		"plan":           json.RawMessage(planJSON),
		"approval":       map[string]any{"plan_digest": digest, "approved_by": approvedBy, "approved_at": "2026-07-11T12:00:00Z", "statement": statement},
	})
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func validPlanCalls() []execxtest.Call {
	return append(fullTaxonomyScript(), ghIssueCreateCall("Fix the status lock race", []string{"ready", "bug", "implementer", "standard"}, 1))
}

func TestGrantActivation(t *testing.T) {
	root := newActivateRepoWithConfig(t, metricsEnabledConfigTOML)
	g := seedGrant(t, root, nil)
	id := g.Terms.ID
	script := &execxtest.Script{T: t, Calls: validPlanCalls()}
	env := Env{RepoRoot: root, Runner: muxRunner{git: execx.Local{}, gh: script}, Now: fixedNow}

	res, err := Activate(context.Background(), env, activationRequest(t, root, validPlanJSON(), "grant:"+id, GrantApprovalStatement))
	if err != nil {
		t.Fatalf("Activate: %v", err)
	}
	script.AssertExhausted()

	if got := loadRun(t, root).Run.Plan.ApprovedBy; got != "grant:"+id {
		t.Errorf("plan approver = %q, want grant:%s", got, id)
	}
	got := getGrant(t, root, id)
	if len(got.Runs) != 1 || got.Runs[0].Ref != res.RunID {
		t.Errorf("grant runs = %+v, want the activated run", got.Runs)
	}
	if want := []grant.RecordedApproval{{Gate: "plan", RunID: res.RunID, At: fixedNow()}}; !reflect.DeepEqual(got.Approvals, want) {
		t.Errorf("grant approvals = %+v, want %+v", got.Approvals, want)
	}
	body := script.StdinAt(len(fullTaxonomyScript()))
	line := "Plan approved under Orch grant " + id + "; no person reviewed or approved this plan."
	if v := findVerification(t, body, planApprovalName); v.Result != line || !strings.Contains(body, line) {
		t.Errorf("audit record plan approval = %+v, want %q", v, line)
	}
	for _, ev := range loadMetricsDocs(t, root)[0].Events {
		if ev.ApprovalSource != "grant:"+id {
			t.Errorf("activate event approval source = %q, want grant:%s", ev.ApprovalSource, id)
		}
	}
}

// Each activation stop refuses the grant approval before activation touches
// anything and spends no run, and a human approval in the same situation
// still activates.
func TestGrantActivationStops(t *testing.T) {
	type tc struct {
		edit       func(*grant.Proposal)
		after      func(t *testing.T, root string, g *grant.Grant) // after seeding
		noGrant    bool
		plan       string
		bothHosts  bool
		humanCalls []execxtest.Call
		approver   func(id string) string
		want       string
	}
	cases := map[string]tc{
		"no grant was created": {noGrant: true, want: "no autonomy grant is active"},
		"grant expired":        {edit: func(p *grant.Proposal) { p.ExpiresAt = fixedNow().Add(-time.Minute) }, want: "no autonomy grant is active"},
		"grant revoked": {after: func(t *testing.T, root string, _ *grant.Grant) {
			if _, err := openGrants(t, root).Revoke(fixedNow()); err != nil {
				t.Fatal(err)
			}
		}, want: "no autonomy grant is active"},
		"plan gate not covered": {edit: func(p *grant.Proposal) { p.Gates = []string{"merge"} }, want: "does not cover the plan gate"},
		"run budget used": {edit: func(p *grant.Proposal) { p.RunLimit = 1 }, after: func(t *testing.T, root string, g *grant.Grant) {
			if _, err := openGrants(t, root).RecordRun(g.Terms.ID, "run-earlier", fixedNow()); err != nil {
				t.Fatal(err)
			}
		}, want: "used all 1 of its runs"},
		"host is not claude": {
			plan: codexPlanJSON(), bothHosts: true,
			humanCalls: append(fullTaxonomyScript(), ghIssueCreateCall("Issue A", []string{"ready", "feature", "implementer", "standard"}, 1)),
			want:       "a grant approves only claude runs",
		},
		"an issue declares a risk domain": {
			plan: twoIssuePlanJSON(),
			humanCalls: append(fullTaxonomyScript(),
				ghIssueCreateCall("Issue A", []string{"ready", "feature", "implementer", "standard"}, 1),
				ghIssueCreateCall("Issue B", []string{"ready", "feature", "specialist", "critical"}, 2)),
			want: "b (concurrency)",
		},
		"caller is not the session holder": {after: func(t *testing.T, _ string, _ *grant.Grant) { t.Setenv(grant.SessionEnv, "another-session") }, want: "session holder"},
		"caller has no session":            {after: func(t *testing.T, _ string, _ *grant.Grant) { t.Setenv(grant.SessionEnv, "") }, want: "session holder"},
		"approval names another grant":     {approver: func(string) string { return "grant:grant-20260711T000000Z-00000000" }, want: "is not \"grant:"},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			root := newActivateRepo(t)
			if c.bothHosts {
				root = newBothHostActivateRepo(t)
			}
			plan, humanCalls := c.plan, c.humanCalls
			if plan == "" {
				plan, humanCalls = validPlanJSON(), validPlanCalls()
			}
			id := "grant-20260711T120000Z-00000000"
			var before *grant.Grant
			if !c.noGrant {
				g := seedGrant(t, root, c.edit)
				id = g.Terms.ID
				if c.after != nil {
					c.after(t, root, g)
				}
				before = getGrant(t, root, id)
			}
			approver := "grant:" + id
			if c.approver != nil {
				approver = c.approver(id)
			}

			script := &execxtest.Script{T: t}
			env := Env{RepoRoot: root, Runner: muxRunner{git: execx.Local{}, gh: script}, Now: fixedNow}
			_, err := Activate(context.Background(), env, activationRequest(t, root, plan, approver, GrantApprovalStatement))
			if !errors.Is(err, ErrGrantStop) || !strings.Contains(err.Error(), c.want) {
				t.Fatalf("err = %v, want a grant stop naming %q", err, c.want)
			}
			script.AssertExhausted() // nothing reached GitHub
			assertNoActivationArtifacts(t, root)
			if before != nil && !reflect.DeepEqual(getGrant(t, root, id), before) {
				t.Error("a refused grant activation changed the grant")
			}

			human := &execxtest.Script{T: t, Calls: humanCalls}
			env.Runner = muxRunner{git: execx.Local{}, gh: human}
			if _, err := Activate(context.Background(), env, activationRequest(t, root, plan, "alice", ApprovalStatement)); err != nil {
				t.Fatalf("human activation in the same situation: %v", err)
			}
			human.AssertExhausted()
			if before != nil && !reflect.DeepEqual(getGrant(t, root, id), before) {
				t.Error("a human activation changed the grant")
			}
		})
	}
}

// A human approval cannot name a grant as its approver: the recorded plan
// approver is what marks a run as one a grant activated.
func TestHumanActivationCannotNameAGrant(t *testing.T) {
	root := newActivateRepo(t)
	script := &execxtest.Script{T: t}
	env := Env{RepoRoot: root, Runner: muxRunner{git: execx.Local{}, gh: script}, Now: fixedNow}
	_, err := Activate(context.Background(), env, activationRequest(t, root, validPlanJSON(), "grant:someone", ApprovalStatement))
	if !errors.Is(err, ErrBadApproval) {
		t.Fatalf("err = %v, want ErrBadApproval", err)
	}
	script.AssertExhausted()
	assertNoActivationArtifacts(t, root)
}

// grantMergeRun is a Delivery run with one issue awaiting merge at
// head-oid-2. With approvedBy "grant" the run reads as one g activated and
// g records it, with "grant-unrecorded" it reads so but g has no record of
// it, and any other value is the run's human approver.
func grantMergeRun(t *testing.T, toml string, edit func(*grant.Proposal), issue func(*state.Issue), approvedBy string) (string, *grant.Grant) {
	t.Helper()
	root := newActivateRepoWithConfig(t, toml)
	g := seedGrant(t, root, edit)
	iss := fixtureIssue("a", 1, state.PhaseAwaitingMerge)
	iss.ApprovedHeadOID = "head-oid-2"
	if issue != nil {
		issue(&iss)
	}
	enterDeliveryAt(t, root, "r1", []state.Issue{iss})
	st := loadRun(t, root)
	st.Run.Plan.ApprovedBy = approvedBy
	if strings.HasPrefix(approvedBy, "grant") {
		st.Run.Plan.ApprovedBy = "grant:" + g.Terms.ID
	}
	if approvedBy == "grant" {
		if _, err := openGrants(t, root).RecordApproval(g.Terms.ID, grant.RecordedApproval{Gate: "plan", RunID: st.Run.ID}, g.CreatedAt); err != nil {
			t.Fatal(err)
		}
	}
	if err := state.Save(root, st); err != nil {
		t.Fatal(err)
	}
	return root, getGrant(t, root, g.Terms.ID)
}

func mergeRequest(statement, approvedBy string) []byte {
	return fmt.Appendf(nil, `{"schema_version":1,"issue_number":1,"approval":{"pr_number":10,"head_oid":"head-oid-2","approved_by":%q,"approved_at":"2026-07-11T12:00:00Z","statement":%q}}`, approvedBy, statement)
}

func ciCalls(bucket string) []execxtest.Call {
	return []execxtest.Call{
		{Name: "gh", Args: []string{"pr", "view", "10", "--json", "statusCheckRollup"}, Stdout: `{"statusCheckRollup":[{}]}`},
		{Name: "gh", Args: []string{"pr", "checks", "10", "--required", "--json", "name,state,bucket,link"}, Stdout: fmt.Sprintf(`[{"name":"build","state":"X","bucket":%q,"link":""}]`, bucket)},
	}
}

func prFilesCalls(changed int, lines string) []execxtest.Call {
	return []execxtest.Call{
		{Name: "gh", Args: []string{"pr", "view", "10", "--json", "headRefOid,changedFiles"}, Stdout: fmt.Sprintf(`{"headRefOid":"head-oid-2","changedFiles":%d}`, changed)},
		{Name: "gh", Args: []string{"api", "--paginate", "repos/{owner}/{repo}/pulls/10/files?per_page=100", "--jq", `.[] | [.filename, (.previous_filename // "")]`}, Stdout: lines},
	}
}

// mergeTail is every call Merge makes from `gh pr merge` on.
func mergeTail(t *testing.T) []execxtest.Call {
	return []execxtest.Call{
		ghMergePRCall(10, "head-oid-2"), ghPRViewCall(10, "MERGED", "head-oid-2"),
		ghSetStatusCall(1, ghops.StatusDelivered), ghIssueViewCall(t, 1, "OPEN", baseManifestBody(t)),
		ghCloseIssueCall(1), ghSetIssueBodyCall(1),
	}
}

func joinCalls(parts ...[]execxtest.Call) []execxtest.Call {
	var out []execxtest.Call
	for _, p := range parts {
		out = append(out, p...)
	}
	return out
}

func runMerge(t *testing.T, root string, req []byte, calls []execxtest.Call) (*MergeResult, *execxtest.Script, error) {
	t.Helper()
	script := &execxtest.Script{T: t, Calls: calls}
	res, err := Merge(context.Background(), Env{RepoRoot: root, Runner: muxRunner{git: execx.Local{}, gh: script}, Now: fixedNow}, req)
	return res, script, err
}

func TestGrantMerge(t *testing.T) {
	root, g := grantMergeRun(t, metricsEnabledConfigTOML, nil, nil, "grant")
	id := g.Terms.ID
	runID := loadRun(t, root).Run.ID
	calls := joinCalls([]execxtest.Call{ghAuth(), ghPRViewCall(10, "OPEN", "head-oid-2")}, ciCalls("pass"),
		prFilesCalls(1, `["internal/run/plan.go",""]`), mergeTail(t))
	if _, script, err := runMerge(t, root, mergeRequest(GrantMergeApprovalStatement, "grant:"+id), calls); err != nil {
		t.Fatalf("Merge: %v", err)
	} else {
		script.AssertExhausted()
		line := "Merge approved under Orch grant " + id + "; no person reviewed or approved this merge."
		if v := findVerification(t, script.StdinAt(len(calls)-1), mergeApprovalName); v.Result != line || v.CommitOID != "head-oid-2" {
			t.Errorf("audit record merge approval = %+v, want %q at head-oid-2", v, line)
		}
	}
	wantPhase(t, root, 1, state.PhaseMerged)
	got := getGrant(t, root, id)
	if len(got.Merges) != 1 || got.Merges[0].Ref != grant.MergeRef(runID, 1) {
		t.Errorf("grant merges = %+v", got.Merges)
	}
	if a := got.Approvals[len(got.Approvals)-1]; a != (grant.RecordedApproval{Gate: "merge", RunID: runID, Issue: 1, PR: 10, Head: "head-oid-2", At: fixedNow()}) {
		t.Errorf("merge approval = %+v", a)
	}
	evs := loadMetricsDocs(t, root)[0].Events
	if ev := evs[len(evs)-1]; ev.Verb != "merge" || ev.ApprovalSource != "grant:"+id {
		t.Errorf("merge event = %+v, want approval source grant:%s", ev, id)
	}
}

// A grant merge that fails after `gh pr merge` has already spent its unit;
// the re-run finds the PR merged and spends nothing more.
func TestGrantMergeRerunSpendsOneUnit(t *testing.T) {
	root, g := grantMergeRun(t, testConfigTOML, nil, nil, "grant")
	req := mergeRequest(GrantMergeApprovalStatement, "grant:"+g.Terms.ID)
	readBack := execxtest.Call{Name: "gh", Args: ghPRViewCall(10, "MERGED", "head-oid-2").Args, Exit: 1, Stderr: "network down"}
	_, script, err := runMerge(t, root, req, joinCalls([]execxtest.Call{ghAuth(), ghPRViewCall(10, "OPEN", "head-oid-2")}, ciCalls("pass"),
		prFilesCalls(1, `["README.md",""]`), []execxtest.Call{ghMergePRCall(10, "head-oid-2"), readBack}))
	if err == nil {
		t.Fatal("merge succeeded though the read-back failed")
	}
	script.AssertExhausted()
	if n := len(getGrant(t, root, g.Terms.ID).Merges); n != 1 {
		t.Fatalf("merges after the failed verb = %d, want 1", n)
	}
	tail := mergeTail(t)
	_, script, err = runMerge(t, root, req, joinCalls([]execxtest.Call{ghAuth(), ghPRViewCall(10, "MERGED", "head-oid-2")}, tail[2:]))
	if err != nil {
		t.Fatalf("re-run: %v", err)
	}
	script.AssertExhausted()
	wantPhase(t, root, 1, state.PhaseMerged)
	if got := getGrant(t, root, g.Terms.ID); len(got.Merges) != 1 || len(got.Approvals) != 2 {
		t.Fatalf("grant after the re-run = %+v, want one merge spent", got)
	}
}

// Each merge stop refuses the grant approval, leaves the issue awaiting merge
// and spends nothing, and a human approval in the same situation still
// merges wherever it does today.
func TestGrantMergeStops(t *testing.T) {
	start := []execxtest.Call{ghAuth(), ghPRViewCall(10, "OPEN", "head-oid-2")}
	type tc struct {
		edit       func(*grant.Proposal)
		issue      func(*state.Issue)
		approvedBy string // the run's plan approver; default "grant"
		after      func(t *testing.T, root string, g *grant.Grant)
		approver   func(id string) string
		calls      []execxtest.Call // gh calls before the grant stop
		humanCalls []execxtest.Call // nil: a human approval fails here today too
		want       string
	}
	humanMerge := joinCalls(start, []execxtest.Call{ghRollupEmptyCall(10)}, nil)
	cases := map[string]tc{
		"grant expired": {edit: func(p *grant.Proposal) { p.ExpiresAt = fixedNow().Add(-time.Minute) }, want: "expired at"},
		"grant revoked": {after: func(t *testing.T, root string, _ *grant.Grant) {
			if _, err := openGrants(t, root).Revoke(fixedNow()); err != nil {
				t.Fatal(err)
			}
		}, want: "was revoked at"},
		"merge budget used": {edit: func(p *grant.Proposal) { p.MergeLimit = 1 }, after: func(t *testing.T, root string, g *grant.Grant) {
			if _, err := openGrants(t, root).RecordApproval(g.Terms.ID, grant.RecordedApproval{Gate: "merge", RunID: "run-other", Issue: 2}, fixedNow()); err != nil {
				t.Fatal(err)
			}
		}, want: "used all 1 of its merges"},
		"merge gate not covered":           {edit: func(p *grant.Proposal) { p.Gates = []string{"plan"} }, want: "does not cover the merge gate"},
		"caller is not the session holder": {after: func(t *testing.T, _ string, _ *grant.Grant) { t.Setenv(grant.SessionEnv, "another-session") }, want: "session holder"},
		"approval names another grant":     {approver: func(string) string { return "grant:grant-20260711T000000Z-00000000" }, want: "is not \"grant:"},
		"run activated by a human":         {approvedBy: "alice", want: "activated by a human"},
		"run the grant has no record of":   {approvedBy: "grant-unrecorded", want: "no record of activating run"},
		"history holds an escalation": {issue: func(i *state.Issue) {
			i.Attempts = []state.Attempt{{Role: manifest.RoleImplementer, Selection: i.Decision.Executor, Failed: true, Reason: "weak-model-failure"}}
		}, want: "escalation, block or block resolution"},
		"history holds a resolved block": {issue: func(i *state.Issue) {
			i.Blocks = []state.Block{{ID: 1, Cause: state.BlockHuman, Reason: "choose", Resolution: &state.BlockResolution{ResolvedBy: "human", Detail: "d", At: "2026-07-11T12:00:00Z", Statement: ResolveBlockStatement}}}
		}, want: "escalation, block or block resolution"},
		"reviewer was downgraded": {issue: func(i *state.Issue) { i.Decision = downgradedDecision() }, want: "downgraded reviewer"},
		"no required checks":      {calls: joinCalls(start, []execxtest.Call{ghRollupEmptyCall(10)}), want: "required CI is no-checks, not passing"},
		"required CI pending":     {calls: joinCalls(start, ciCalls("pending")), want: "required CI is pending", humanCalls: []execxtest.Call{}},
		"required CI failing":     {calls: joinCalls(start, ciCalls("fail")), want: "required CI is failing", humanCalls: []execxtest.Call{}},
		"PR changes a protected path": {
			calls: joinCalls(start, ciCalls("pass"), prFilesCalls(2, "[\"README.md\",\"\"]\n[\"CLAUDE.md.\",\"\"]")),
			want:  "changes protected paths: CLAUDE.md.",
		},
		"PR renames a protected path away": {
			calls: joinCalls(start, ciCalls("pass"), prFilesCalls(1, `["docs/guard.go","internal/guard/guard.go"]`)),
			want:  "changes protected paths: internal/guard/guard.go",
		},
		"PR file list incomplete": {
			calls: joinCalls(start, ciCalls("pass"), prFilesCalls(2, `["README.md",""]`)),
			want:  "complete file list could not be confirmed",
		},
		"PR already merged without a grant unit": {
			calls:      []execxtest.Call{ghAuth(), ghPRViewCall(10, "MERGED", "head-oid-2")},
			want:       "spent no merge on it",
			humanCalls: joinCalls([]execxtest.Call{ghAuth(), ghPRViewCall(10, "MERGED", "head-oid-2")}, mergeTail(t)[2:]),
		},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			approvedBy := c.approvedBy
			if approvedBy == "" {
				approvedBy = "grant"
			}
			root, g := grantMergeRun(t, testConfigTOML, c.edit, c.issue, approvedBy)
			id := g.Terms.ID
			if c.after != nil {
				c.after(t, root, g)
			}
			before, beforeState := getGrant(t, root, id), stateBytes(t, root)
			approver := "grant:" + id
			if c.approver != nil {
				approver = c.approver(id)
			}

			_, script, err := runMerge(t, root, mergeRequest(GrantMergeApprovalStatement, approver), c.calls)
			if !errors.Is(err, ErrGrantStop) || !strings.Contains(err.Error(), c.want) {
				t.Fatalf("err = %v, want a grant stop naming %q", err, c.want)
			}
			script.AssertExhausted()
			if string(stateBytes(t, root)) != string(beforeState) {
				t.Error("a refused grant merge changed state")
			}
			wantPhase(t, root, 1, state.PhaseAwaitingMerge)
			if !reflect.DeepEqual(getGrant(t, root, id), before) {
				t.Error("a refused grant merge changed the grant")
			}

			humanCalls := c.humanCalls
			if humanCalls == nil {
				humanCalls = joinCalls(humanMerge, mergeTail(t))
			}
			if len(humanCalls) == 0 {
				return
			}
			_, human, err := runMerge(t, root, mergeRequest(MergeApprovalStatement, "alice"), humanCalls)
			if err != nil {
				t.Fatalf("human merge in the same situation: %v", err)
			}
			human.AssertExhausted()
			wantPhase(t, root, 1, state.PhaseMerged)
		})
	}
}

// grantReviewRun is an in-review issue whose record already holds three
// review cycles, two of them non-approving; grantRun makes the run one a
// grant (fix-cycle limit 2) activated.
func grantReviewRun(t *testing.T, grantRun bool) (string, string) {
	t.Helper()
	root := setupDeliveryGitRepo(t, "r1", []state.Issue{fixtureIssue("a", 1, state.PhaseInReview)})
	createBranchWorktree(t, root, "orch/issue-1")
	g := seedGrant(t, root, nil)
	if grantRun {
		st := loadRun(t, root)
		st.Run.Plan.ApprovedBy = "grant:" + g.Terms.ID
		if err := state.Save(root, st); err != nil {
			t.Fatal(err)
		}
	}
	m := baseManifest()
	for i, verdict := range []string{VerdictRequestChanges, VerdictApprove, VerdictRequestChanges} {
		m.Verifications = append(m.Verifications, manifest.Verification{Name: fmt.Sprintf("review-cycle-%d", i+1), Result: verdict})
	}
	body, err := manifest.Upsert("**Objective**\n\ndo it\n", m)
	if err != nil {
		t.Fatal(err)
	}
	return root, body
}

const unsatisfied = `[{"criterion":1,"judgment":"unsatisfied","reason":"-race still fails"}]`

func TestGrantFixCycleLimitBlocksTheIssue(t *testing.T) {
	root, body := grantReviewRun(t, true)
	script := &execxtest.Script{T: t, Calls: []execxtest.Call{
		ghAuth(), ghPRViewCall(10, "OPEN", "head-oid-1"), ghIssueViewCall(t, 1, "OPEN", body),
		ghIssueViewCall(t, 1, "OPEN", body), ghSetIssueBodyCall(1), ghSetPRBodyCall(10),
		ghSetStatusCall(1, ghops.StatusNeedsHuman),
	}}
	res, err := Review(context.Background(), Env{RepoRoot: root, Runner: muxRunner{git: execx.Local{}, gh: script}, Now: fixedNow}, []byte(reviewReq(VerdictRequestChanges, unsatisfied)))
	if err != nil {
		t.Fatalf("Review: %v", err)
	}
	script.AssertExhausted()
	if res.Phase != state.PhaseBlocked || !strings.Contains(res.BlockedReason, "Non-approving review 3 exceeds autonomy grant") || !strings.Contains(res.BlockedReason, "fix-cycle limit of 2") {
		t.Fatalf("result = %+v, want the third non-approving review blocked", res)
	}
	iss := loadRun(t, root).Run.Issues[0]
	if !iss.DecisionPending() || iss.Blocks[len(iss.Blocks)-1].Cause != state.BlockHuman {
		t.Fatalf("issue = %+v, want a pending human decision", iss)
	}
	assertDecisionSurvivesResume(t, root, baseManifestBody(t), true)
}

// The limit counts non-approving reviews only, and a run a human activated
// never takes the block however many there are.
func TestGrantFixCycleLimitBounds(t *testing.T) {
	for name, c := range map[string]struct {
		grantRun bool
		verdict  string
		cycles   []string
	}{
		"second non-approving review on a grant run": {true, VerdictRequestChanges, []string{VerdictRequestChanges, VerdictApprove}},
		"approving review on a grant run":            {true, VerdictApprove, nil},
		"third non-approving review on a human run":  {false, VerdictRequestChanges, nil},
	} {
		t.Run(name, func(t *testing.T) {
			root, body := grantReviewRun(t, c.grantRun)
			if c.cycles != nil {
				m := baseManifest()
				for i, v := range c.cycles {
					m.Verifications = append(m.Verifications, manifest.Verification{Name: fmt.Sprintf("review-cycle-%d", i+1), Result: v})
				}
				var err error
				if body, err = manifest.Upsert("**Objective**\n\ndo it\n", m); err != nil {
					t.Fatal(err)
				}
			}
			calls := []execxtest.Call{ghAuth(), ghPRViewCall(10, "OPEN", "head-oid-1")}
			judgments := unsatisfied
			if c.grantRun && c.verdict != VerdictApprove {
				calls = append(calls, ghIssueViewCall(t, 1, "OPEN", body))
			}
			calls = append(calls, ghIssueViewCall(t, 1, "OPEN", body), ghSetIssueBodyCall(1), ghSetPRBodyCall(10))
			if c.verdict == VerdictApprove {
				judgments = `[{"criterion":1,"judgment":"satisfied","reason":"clean"}]`
			} else {
				calls = append(calls, ghSetStatusCall(1, ghops.StatusInProgress))
			}
			script := &execxtest.Script{T: t, Calls: calls}
			res, err := Review(context.Background(), Env{RepoRoot: root, Runner: muxRunner{git: execx.Local{}, gh: script}, Now: fixedNow}, []byte(reviewReq(c.verdict, judgments)))
			if err != nil {
				t.Fatalf("Review: %v", err)
			}
			script.AssertExhausted()
			if res.Phase != state.PhaseInReview || res.BlockedReason != "" {
				t.Fatalf("result = %+v, want the issue still in review", res)
			}
		})
	}
}

// The plan line a grant activation writes survives pr-open in both the
// issue body and the PR body it creates, beside the executor's evidence,
// and resume does not mistake it for pr-open evidence.
func TestGrantPlanLineSurvivesPROpen(t *testing.T) {
	root := newLifecycleRepo(t)
	g := seedGrant(t, root, nil)
	const branch = "orch/issue-1-fix-the-status-lock-race"
	script := &execxtest.Script{T: t, Calls: validPlanCalls()}
	env := Env{RepoRoot: root, Runner: muxRunner{git: execx.Local{}, gh: script}, Now: fixedNow}
	if _, err := Activate(context.Background(), env, activationRequest(t, root, validPlanJSON(), "grant:"+g.Terms.ID, GrantApprovalStatement)); err != nil {
		t.Fatalf("Activate: %v", err)
	}
	script.AssertExhausted()
	activated := script.StdinAt(len(fullTaxonomyScript()))
	m, err := manifest.Parse(activated)
	if err != nil {
		t.Fatal(err)
	}
	if n := evidenceCount(m.Verifications); n != 0 {
		t.Fatalf("activation-seeded record counts %d evidence entries, want 0", n)
	}

	runVerb(t, root, Dispatch, `{"schema_version":4,"issue_number":1}`, ghAuth(), ghRepoViewCall("main"), ghSetStatusCall(1, ghops.StatusInProgress))
	wtDir := filepath.Join(root, ".orchestrator", "worktrees", "issue-1")
	if err := os.WriteFile(filepath.Join(wtDir, "feature.go"), []byte("package feature\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	rawGit(t, wtDir, "add", "-A")
	rawGit(t, wtDir, "commit", "-m", "work")
	_, prOpen := runVerbScript(t, root, PROpen, `{"schema_version":1,"issue_number":1,"verifications":[{"name":"go test","result":"pass"}]}`,
		ghAuth(), ghRepoViewCall("main"), ghPRListEmptyCall(branch),
		ghIssueViewCall(t, 1, "OPEN", activated), ghSetIssueBodyCall(1),
		ghCreatePRCall(branch, "Fix the status lock race", 10), ghSetStatusCall(1, ghops.StatusAwaitingReview))

	line := planApprovedLine(g.Terms.ID)
	for view, posted := range map[string]string{"issue body": prOpen.StdinAt(4), "PR body": prOpen.StdinAt(5)} {
		if !strings.Contains(posted, line) || findVerification(t, posted, planApprovalName).Result != line {
			t.Errorf("%s after pr-open lost the plan line %q", view, line)
		}
		findVerification(t, posted, "go test")
		if got := strings.Count(posted, "**"+planApprovalName+"**"); got != 1 {
			t.Errorf("%s renders the plan-approval entry %d times, want 1", view, got)
		}
	}
}
