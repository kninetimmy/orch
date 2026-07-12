package run

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kninetimmy/orch/internal/execx"
	"github.com/kninetimmy/orch/internal/execx/execxtest"
	"github.com/kninetimmy/orch/internal/ghops"
	"github.com/kninetimmy/orch/internal/gitops"
	"github.com/kninetimmy/orch/internal/lockfile"
	"github.com/kninetimmy/orch/internal/paths"
	"github.com/kninetimmy/orch/internal/state"
)

// setupGitEnv isolates every git invocation in the test process from
// the developer's real configuration (copied from internal/gitops's
// integration-test idiom).
func setupGitEnv(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skipf("git not on PATH: %v", err)
	}
	cfg := filepath.Join(t.TempDir(), "gitconfig")
	content := "[user]\n\tname = Orch Test\n\temail = orch-test@example.invalid\n"
	if err := os.WriteFile(cfg, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GIT_CONFIG_GLOBAL", cfg)
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
}

func rawGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	res, err := (execx.Local{}).Run(context.Background(), execx.Cmd{
		Name: "git", Args: args, Dir: dir, Env: []string{"GIT_TERMINAL_PROMPT=0", "LC_ALL=C"},
	})
	if err != nil {
		t.Fatalf("git %v: %v", args, err)
	}
	if res.ExitCode != 0 {
		t.Fatalf("git %v exited %d: %s", args, res.ExitCode, res.Stderr)
	}
	return res.Stdout
}

// newActivateRepoWithIgnore builds a real sandbox repo on branch main
// with a committed config.toml and the given .gitignore content.
func newActivateRepoWithIgnore(t *testing.T, gitignore string) string {
	t.Helper()
	setupGitEnv(t)
	root, err := paths.Canonical(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	rawGit(t, root, "init", "-b", "main")
	if err := os.WriteFile(filepath.Join(root, "README.md"), []byte("hi\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, ".gitignore"), []byte(gitignore), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, ".orchestrator"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, ".orchestrator", "config.toml"), []byte(testConfigTOML), 0o644); err != nil {
		t.Fatal(err)
	}
	rawGit(t, root, "add", "-A")
	rawGit(t, root, "commit", "-m", "initial")
	return root
}

// fullGitignore ignores the worktree container plus Orch's own
// machine-local files (mirroring this repo's real .gitignore), so a
// completed activation leaves the primary checkout clean.
const fullGitignore = ".orchestrator/worktrees/\n.orchestrator/state.json\n.orchestrator/delivery.lock\n"

func newActivateRepo(t *testing.T) string {
	t.Helper()
	return newActivateRepoWithIgnore(t, fullGitignore)
}

// muxRunner sends "git" commands to a real runner and everything else
// (gh, memhub) to a scripted one, per the activate_test idiom: real
// git sandboxes, scripted GitHub/memhub.
type muxRunner struct {
	git execx.Runner
	gh  *execxtest.Script
}

func (m muxRunner) Run(ctx context.Context, c execx.Cmd) (execx.Result, error) {
	if c.Name == "git" {
		return m.git.Run(ctx, c)
	}
	return m.gh.Run(ctx, c)
}

func ghOpenCall() execxtest.Call {
	return execxtest.Call{Name: "gh", Args: []string{"auth", "status"}}
}

func ghRepoViewCall(defaultBranch string) execxtest.Call {
	return execxtest.Call{
		Name: "gh", Args: []string{"repo", "view", "--json", "nameWithOwner,defaultBranchRef,url"},
		Stdout: fmt.Sprintf(`{"nameWithOwner":"o/r","defaultBranchRef":{"name":%q},"url":"https://github.com/o/r"}`, defaultBranch),
	}
}

func ghLabelListCall(stdout string) execxtest.Call {
	return execxtest.Call{Name: "gh", Args: []string{"label", "list", "--json", "name", "--limit", "1000"}, Stdout: stdout}
}

func ghLabelListEmptyCall() execxtest.Call {
	return ghLabelListCall("[]")
}

// taxonomyLabels mirrors ghops's private label taxonomy table
// (name, color, description) in creation order.
var taxonomyLabels = []struct{ name, color, desc string }{
	{"ready", "1D76DB", "orch status label — exactly one per issue (PRD §13)"},
	{"in-progress", "1D76DB", "orch status label — exactly one per issue (PRD §13)"},
	{"blocked", "1D76DB", "orch status label — exactly one per issue (PRD §13)"},
	{"needs-human", "1D76DB", "orch status label — exactly one per issue (PRD §13)"},
	{"awaiting-review", "1D76DB", "orch status label — exactly one per issue (PRD §13)"},
	{"feature", "0E8A16", "orch type label — exactly one per issue (PRD §13)"},
	{"bug", "0E8A16", "orch type label — exactly one per issue (PRD §13)"},
	{"chore", "0E8A16", "orch type label — exactly one per issue (PRD §13)"},
	{"infra", "0E8A16", "orch type label — exactly one per issue (PRD §13)"},
	{"docs", "0E8A16", "orch type label — exactly one per issue (PRD §13)"},
	{"research", "0E8A16", "orch type label — exactly one per issue (PRD §13)"},
	{"implementer", "5319E7", "orch role label — exactly one per issue (PRD §13)"},
	{"specialist", "5319E7", "orch role label — exactly one per issue (PRD §13)"},
	{"standard", "FBCA04", "orch risk label — exactly one per issue (PRD §13)"},
	{"critical", "B60205", "orch risk label — exactly one per issue (PRD §13)"},
}

func ghLabelCreateCalls() []execxtest.Call {
	calls := make([]execxtest.Call, len(taxonomyLabels))
	for i, l := range taxonomyLabels {
		calls[i] = execxtest.Call{Name: "gh", Args: []string{"label", "create", l.name, "--color", l.color, "--description", l.desc}}
	}
	return calls
}

func ghIssueCreateCall(title string, labels []string, number int) execxtest.Call {
	args := []string{"issue", "create", "--title", title, "--body-file", "-"}
	for _, l := range labels {
		args = append(args, "--label", l)
	}
	return execxtest.Call{Name: "gh", Args: args, Stdout: fmt.Sprintf("https://github.com/o/r/issues/%d\n", number)}
}

// buildActivationJSON assembles an activation request with an
// explicit digest and statement, so tests can construct both valid
// and deliberately-mismatched approvals.
func buildActivationJSON(t *testing.T, planJSON, digest, statement string) []byte {
	t.Helper()
	req := map[string]any{
		"schema_version": ActivationSchemaVersion,
		"plan":           json.RawMessage(planJSON),
		"approval": map[string]any{
			"plan_digest": digest,
			"approved_by": "alice",
			"approved_at": "2026-07-11T12:00:00Z",
			"statement":   statement,
		},
	}
	data, err := json.Marshal(req)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

// activationJSON builds a valid activation request for planJSON: the
// correct recomputed digest and the correct approval statement.
func activationJSON(t *testing.T, planJSON string) []byte {
	t.Helper()
	p, err := DecodePlan([]byte(planJSON))
	if err != nil {
		t.Fatal(err)
	}
	digest, err := p.Digest()
	if err != nil {
		t.Fatal(err)
	}
	return buildActivationJSON(t, planJSON, digest, ApprovalStatement)
}

func fullTaxonomyScript() []execxtest.Call {
	calls := []execxtest.Call{ghOpenCall(), ghRepoViewCall("main"), ghLabelListEmptyCall()}
	return append(calls, ghLabelCreateCalls()...)
}

func assertNoDeliveryState(t *testing.T, root string) {
	t.Helper()
	st, err := state.Load(root)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if st.Mode != state.ModeAssist {
		t.Errorf("mode = %s, want assist", st.Mode)
	}
	owner, err := lockfile.Inspect(root)
	if err != nil {
		t.Fatalf("Inspect: %v", err)
	}
	if owner != nil {
		t.Errorf("lock present: %+v, want none", owner)
	}
}

func TestActivateHappyPathTwoIssuesTwoWaves(t *testing.T) {
	root := newActivateRepo(t)
	calls := fullTaxonomyScript()
	calls = append(calls,
		ghIssueCreateCall("Issue A", []string{"ready", "feature", "implementer", "standard"}, 1),
		ghIssueCreateCall("Issue B", []string{"ready", "feature", "specialist", "critical"}, 2),
	)
	script := &execxtest.Script{T: t, Calls: calls}
	env := Env{RepoRoot: root, Runner: muxRunner{git: execx.Local{}, gh: script}, Now: fixedNow}

	result, err := Activate(context.Background(), env, activationJSON(t, twoIssuePlanJSON()))
	if err != nil {
		t.Fatalf("Activate: %v", err)
	}
	script.AssertExhausted()

	if len(result.Issues) != 2 {
		t.Fatalf("Issues = %+v, want 2", result.Issues)
	}
	wantA := ActivationResultIssue{ID: "a", Number: 1, URL: "https://github.com/o/r/issues/1", Branch: "orch/issue-1-issue-a", Worktree: ".orchestrator/worktrees/issue-1"}
	wantB := ActivationResultIssue{ID: "b", Number: 2, URL: "https://github.com/o/r/issues/2", Branch: "orch/issue-2-issue-b", Worktree: ".orchestrator/worktrees/issue-2"}
	if result.Issues[0] != wantA {
		t.Errorf("Issues[0] = %+v, want %+v", result.Issues[0], wantA)
	}
	if result.Issues[1] != wantB {
		t.Errorf("Issues[1] = %+v, want %+v", result.Issues[1], wantB)
	}

	g, err := gitops.Open(context.Background(), execx.Local{}, root)
	if err != nil {
		t.Fatal(err)
	}
	if err := g.RequireClean(context.Background(), ""); err != nil {
		t.Errorf("primary checkout not clean after activation: %v", err)
	}
	worktrees, err := g.ListWorktrees(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(worktrees) != 3 {
		t.Errorf("ListWorktrees = %+v, want primary + 2 issue worktrees", worktrees)
	}

	st, err := state.Load(root)
	if err != nil {
		t.Fatal(err)
	}
	if st.Mode != state.ModeDelivery || st.Run.ID != result.RunID {
		t.Fatalf("state = %+v, want delivery run %s", st, result.RunID)
	}
	if st.Run.Plan.Digest == "" || st.Run.Plan.ApprovedBy != "alice" {
		t.Errorf("Plan = %+v", st.Run.Plan)
	}
	for i, want := range []state.Issue{
		{PlanID: "a", Title: "Issue A", Phase: state.PhaseWorktreeReady, Number: 1, Branch: "orch/issue-1-issue-a", Worktree: ".orchestrator/worktrees/issue-1"},
		{PlanID: "b", Title: "Issue B", Phase: state.PhaseWorktreeReady, Number: 2, Branch: "orch/issue-2-issue-b", Worktree: ".orchestrator/worktrees/issue-2"},
	} {
		got := st.Run.Issues[i]
		if got.PlanID != want.PlanID || got.Phase != want.Phase || got.Number != want.Number || got.Branch != want.Branch || got.Worktree != want.Worktree {
			t.Errorf("Issues[%d] = %+v, want %+v", i, got, want)
		}
	}
}

// areaPlanJSON is a single-issue plan declaring two area labels,
// passing Validate against testConfig().
func areaPlanJSON() string {
	return `{
  "schema_version": 1,
  "host": "claude",
  "title": "Area label plan",
  "issues": [
    {
      "id": "a",
      "title": "Issue A",
      "objective": "Do A",
      "acceptance_criteria": ["A works"],
      "type": "feature",
      "facts": {"read_only": false},
      "wave": 1,
      "required_tests": ["go test ./..."],
      "usage_class": "light",
      "area_labels": ["core", "cli"]
    }
  ]
}`
}

func TestActivateAreaLabelsPresentProceeds(t *testing.T) {
	root := newActivateRepo(t)
	// Preflight matches area labels case-insensitively (GitHub label
	// names are); the taxonomy step then lists again and creates the
	// full §13 set.
	repoLabels := `[{"name":"Core"},{"name":"cli"}]`
	calls := []execxtest.Call{ghOpenCall(), ghRepoViewCall("main"), ghLabelListCall(repoLabels), ghLabelListCall(repoLabels)}
	calls = append(calls, ghLabelCreateCalls()...)
	calls = append(calls,
		ghIssueCreateCall("Issue A", []string{"ready", "feature", "implementer", "standard", "core", "cli"}, 1),
	)
	script := &execxtest.Script{T: t, Calls: calls}
	env := Env{RepoRoot: root, Runner: muxRunner{git: execx.Local{}, gh: script}, Now: fixedNow}

	result, err := Activate(context.Background(), env, activationJSON(t, areaPlanJSON()))
	if err != nil {
		t.Fatalf("Activate: %v", err)
	}
	script.AssertExhausted()
	if len(result.Issues) != 1 || result.Issues[0].Number != 1 {
		t.Errorf("Issues = %+v, want one issue #1", result.Issues)
	}
}

func TestActivateMissingAreaLabelLeavesNothing(t *testing.T) {
	root := newActivateRepo(t)
	// The repo has "core" but not "cli": activation must fail closed at
	// the read-only preflight, before the taxonomy step or any issue.
	script := &execxtest.Script{T: t, Calls: []execxtest.Call{
		ghOpenCall(), ghRepoViewCall("main"), ghLabelListCall(`[{"name":"core"}]`),
	}}
	env := Env{RepoRoot: root, Runner: muxRunner{git: execx.Local{}, gh: script}, Now: fixedNow}

	_, err := Activate(context.Background(), env, activationJSON(t, areaPlanJSON()))
	if !errors.Is(err, ErrAreaLabelMissing) {
		t.Fatalf("err = %v, want ErrAreaLabelMissing", err)
	}
	if !strings.Contains(err.Error(), "cli") || strings.Contains(err.Error(), "core,") {
		t.Errorf("err = %v, want the missing label named and the present one not", err)
	}
	script.AssertExhausted()
	assertNoDeliveryState(t, root)
}

func TestActivateFailureAtSecondIssueCreateIsResumable(t *testing.T) {
	root := newActivateRepo(t)
	calls := fullTaxonomyScript()
	calls = append(calls,
		ghIssueCreateCall("Issue A", []string{"ready", "feature", "implementer", "standard"}, 1),
		execxtest.Call{
			Name: "gh", Args: []string{"issue", "create", "--title", "Issue B", "--body-file", "-", "--label", "ready", "--label", "feature", "--label", "specialist", "--label", "critical"},
			Exit: 1, Stderr: "network error",
		},
	)
	script := &execxtest.Script{T: t, Calls: calls}
	env := Env{RepoRoot: root, Runner: muxRunner{git: execx.Local{}, gh: script}, Now: fixedNow}

	_, err := Activate(context.Background(), env, activationJSON(t, twoIssuePlanJSON()))
	if err == nil {
		t.Fatal("Activate succeeded, want error")
	}
	script.AssertExhausted()

	st, err := state.Load(root)
	if err != nil {
		t.Fatal(err)
	}
	if st.Mode != state.ModeDelivery {
		t.Fatalf("mode = %s, want delivery (lock held, resumable)", st.Mode)
	}
	if st.Run.Issues[0].Phase != state.PhaseWorktreeReady {
		t.Errorf("issue a phase = %s, want worktree-ready", st.Run.Issues[0].Phase)
	}
	if st.Run.Issues[1].Phase != state.PhasePlanned {
		t.Errorf("issue b phase = %s, want planned", st.Run.Issues[1].Phase)
	}
	owner, err := lockfile.Inspect(root)
	if err != nil || owner == nil {
		t.Fatalf("lock not held: %+v, %v", owner, err)
	}

	branchBefore := st.Run.Issues[0].Branch
	worktreeBefore := filepath.Join(root, filepath.FromSlash(st.Run.Issues[0].Worktree))

	res, err := state.Abort(root)
	if err != nil {
		t.Fatalf("Abort: %v", err)
	}
	if res.PriorRun == nil {
		t.Error("Abort did not report a prior run")
	}
	if _, err := os.Stat(worktreeBefore); err != nil {
		t.Errorf("worktree %s vanished after abort: %v", worktreeBefore, err)
	}
	g, err := gitops.Open(context.Background(), execx.Local{}, root)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := g.RevParse(context.Background(), branchBefore); err != nil {
		t.Errorf("branch %s not preserved after abort: %v", branchBefore, err)
	}

	after, err := state.Load(root)
	if err != nil {
		t.Fatal(err)
	}
	if after.Mode != state.ModeAssist {
		t.Errorf("mode after abort = %s, want assist", after.Mode)
	}
}

func TestActivateDirtyTreeLeavesNothing(t *testing.T) {
	root := newActivateRepo(t)
	if err := os.WriteFile(filepath.Join(root, "untracked.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	script := &execxtest.Script{T: t}
	env := Env{RepoRoot: root, Runner: muxRunner{git: execx.Local{}, gh: script}, Now: fixedNow}

	_, err := Activate(context.Background(), env, activationJSON(t, twoIssuePlanJSON()))
	if !errors.Is(err, gitops.ErrNotClean) {
		t.Fatalf("err = %v, want ErrNotClean", err)
	}
	script.AssertExhausted()
	assertNoDeliveryState(t, root)
}

func TestActivateLockHeldLeavesExistingStateUntouched(t *testing.T) {
	root := newActivateRepo(t)
	before, err := state.EnterDelivery(root, "codex", state.PlanRef{Digest: "sha256:x"}, []state.Issue{{PlanID: "z", Phase: state.PhasePlanned}})
	if err != nil {
		t.Fatal(err)
	}
	script := &execxtest.Script{T: t}
	env := Env{RepoRoot: root, Runner: muxRunner{git: execx.Local{}, gh: script}, Now: fixedNow}

	_, err = Activate(context.Background(), env, activationJSON(t, twoIssuePlanJSON()))
	if !errors.Is(err, ErrDeliveryActive) {
		t.Fatalf("err = %v, want ErrDeliveryActive", err)
	}
	script.AssertExhausted()

	after, err := state.Load(root)
	if err != nil {
		t.Fatal(err)
	}
	if after.Run.ID != before.Run.ID {
		t.Errorf("existing run changed: %s -> %s", before.Run.ID, after.Run.ID)
	}
}

func TestActivateContainerNotIgnoredLeavesNothing(t *testing.T) {
	root := newActivateRepoWithIgnore(t, "") // worktree container not ignored
	script := &execxtest.Script{T: t, Calls: []execxtest.Call{ghOpenCall(), ghRepoViewCall("main")}}
	env := Env{RepoRoot: root, Runner: muxRunner{git: execx.Local{}, gh: script}, Now: fixedNow}

	_, err := Activate(context.Background(), env, activationJSON(t, twoIssuePlanJSON()))
	if !errors.Is(err, gitops.ErrNotIgnored) {
		t.Fatalf("err = %v, want ErrNotIgnored", err)
	}
	script.AssertExhausted()
	assertNoDeliveryState(t, root)
}

func TestActivateDigestMismatchLeavesNothing(t *testing.T) {
	root := newActivateRepo(t)
	script := &execxtest.Script{T: t}
	env := Env{RepoRoot: root, Runner: muxRunner{git: execx.Local{}, gh: script}, Now: fixedNow}

	req := buildActivationJSON(t, twoIssuePlanJSON(), "sha256:wrong", ApprovalStatement)
	_, err := Activate(context.Background(), env, req)
	if !errors.Is(err, ErrBadApproval) {
		t.Fatalf("err = %v, want ErrBadApproval", err)
	}
	script.AssertExhausted()
	assertNoDeliveryState(t, root)
}

func TestActivateWrongStatementLeavesNothing(t *testing.T) {
	root := newActivateRepo(t)
	script := &execxtest.Script{T: t}
	env := Env{RepoRoot: root, Runner: muxRunner{git: execx.Local{}, gh: script}, Now: fixedNow}

	p, err := DecodePlan([]byte(twoIssuePlanJSON()))
	if err != nil {
		t.Fatal(err)
	}
	digest, err := p.Digest()
	if err != nil {
		t.Fatal(err)
	}
	req := buildActivationJSON(t, twoIssuePlanJSON(), digest, "yes-please")
	_, err = Activate(context.Background(), env, req)
	if !errors.Is(err, ErrBadApproval) {
		t.Fatalf("err = %v, want ErrBadApproval", err)
	}
	script.AssertExhausted()
	assertNoDeliveryState(t, root)
}

func TestActivateUnauthGHLeavesNothing(t *testing.T) {
	root := newActivateRepo(t)
	script := &execxtest.Script{T: t, Calls: []execxtest.Call{
		{Name: "gh", Args: []string{"auth", "status"}, Exit: 1},
	}}
	env := Env{RepoRoot: root, Runner: muxRunner{git: execx.Local{}, gh: script}, Now: fixedNow}

	_, err := Activate(context.Background(), env, activationJSON(t, twoIssuePlanJSON()))
	if !errors.Is(err, ghops.ErrNotAuthenticated) {
		t.Fatalf("err = %v, want ErrNotAuthenticated", err)
	}
	script.AssertExhausted()
	assertNoDeliveryState(t, root)
}

func TestActivateBranchExistsMidRun(t *testing.T) {
	root := newActivateRepo(t)
	// Pre-create the branch AddWorktree will try to create for issue b.
	rawGit(t, root, "branch", "orch/issue-2-issue-b")

	calls := fullTaxonomyScript()
	calls = append(calls,
		ghIssueCreateCall("Issue A", []string{"ready", "feature", "implementer", "standard"}, 1),
		ghIssueCreateCall("Issue B", []string{"ready", "feature", "specialist", "critical"}, 2),
	)
	script := &execxtest.Script{T: t, Calls: calls}
	env := Env{RepoRoot: root, Runner: muxRunner{git: execx.Local{}, gh: script}, Now: fixedNow}

	_, err := Activate(context.Background(), env, activationJSON(t, twoIssuePlanJSON()))
	if !errors.Is(err, gitops.ErrBranchExists) {
		t.Fatalf("err = %v, want ErrBranchExists", err)
	}
	script.AssertExhausted()

	st, err := state.Load(root)
	if err != nil {
		t.Fatal(err)
	}
	if st.Run.Issues[0].Phase != state.PhaseWorktreeReady {
		t.Errorf("issue a phase = %s, want worktree-ready", st.Run.Issues[0].Phase)
	}
	if st.Run.Issues[1].Phase != state.PhaseIssueCreated {
		t.Errorf("issue b phase = %s, want issue-created (worktree add failed)", st.Run.Issues[1].Phase)
	}
}
