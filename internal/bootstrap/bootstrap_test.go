package bootstrap

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kninetimmy/orch/internal/config"
	"github.com/kninetimmy/orch/internal/execx"
	"github.com/kninetimmy/orch/internal/execx/execxtest"
	"github.com/kninetimmy/orch/internal/ghops"
	"github.com/kninetimmy/orch/internal/gitops"
)

func TestExecuteHappyPath(t *testing.T) {
	root := newBootstrapRepo(t)
	answers := fullAnswers(t, bothHostsFacts(root), root)

	calls := taxonomyScript()
	calls = append(calls, ghIssueCreateCall(1))
	calls = append(calls, ghCreatePRCall(1))
	calls = append(calls, ghSetStatusCall(1, ghops.StatusAwaitingReview))
	script := &execxtest.Script{T: t, Calls: calls}
	deps := testDeps(root, answers, script, muxRunner{git: execx.Local{}})

	report, err := Execute(context.Background(), deps)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	script.AssertExhausted()

	if report.SchemaVersion != ReportSchemaVersion {
		t.Errorf("SchemaVersion = %d, want %d", report.SchemaVersion, ReportSchemaVersion)
	}
	if report.Issue.Number != 1 || report.Issue.URL != "https://github.com/o/r/issues/1" {
		t.Errorf("Issue = %+v", report.Issue)
	}
	if report.PR.Number != 1 || report.PR.URL != "https://github.com/o/r/pull/1" {
		t.Errorf("PR = %+v", report.PR)
	}
	if report.Branch != BootstrapBranch {
		t.Errorf("Branch = %q, want %q", report.Branch, BootstrapBranch)
	}
	if len(report.Validations) == 0 {
		t.Error("Validations is empty, want the §18.13 entries")
	}
	for _, v := range report.Validations {
		if v.Result != "pass" {
			t.Errorf("validation %s = %+v, want pass", v.Name, v)
		}
	}
	wantNextSteps := []string{
		"review and merge https://github.com/o/r/pull/1",
		"git pull",
		"optionally: git branch -d orch/bootstrap",
		"orch status",
	}
	if len(report.NextSteps) != len(wantNextSteps) {
		t.Fatalf("NextSteps = %v, want %v", report.NextSteps, wantNextSteps)
	}
	for i, want := range wantNextSteps {
		if report.NextSteps[i] != want {
			t.Errorf("NextSteps[%d] = %q, want %q", i, report.NextSteps[i], want)
		}
	}

	// The primary checkout was never written (acceptance §23).
	if _, err := os.Stat(filepath.Join(root, config.Path)); err == nil {
		t.Error("primary checkout carries config.toml; bootstrap must never write it there")
	}

	git, err := gitops.Open(context.Background(), execx.Local{}, root)
	if err != nil {
		t.Fatal(err)
	}
	worktrees, err := git.ListWorktrees(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(worktrees) != 1 {
		t.Errorf("ListWorktrees = %+v, want only the primary checkout (disposable worktree removed)", worktrees)
	}
	head, err := git.RevParse(context.Background(), BootstrapBranch)
	if err != nil || head == "" {
		t.Errorf("local branch %s not preserved: %v", BootstrapBranch, err)
	}
	remoteHead := rawGit(t, root, "rev-parse", "refs/remotes/origin/"+BootstrapBranch)
	if strings.TrimSpace(remoteHead) != head {
		t.Errorf("origin/%s = %q, want %q (pushed)", BootstrapBranch, strings.TrimSpace(remoteHead), head)
	}
}

func TestExecuteNotComplete(t *testing.T) {
	root := newBootstrapRepo(t)
	deps := testDeps(root, map[string]string{}, &execxtest.Script{T: t}, muxRunner{git: execx.Local{}})

	_, err := Execute(context.Background(), deps)
	if !errors.Is(err, ErrNotComplete) {
		t.Fatalf("err = %v, want ErrNotComplete", err)
	}
}

func TestExecuteNotBootstrapReady(t *testing.T) {
	root := newBootstrapRepo(t)
	// gh is never resolved, so BootstrapReady is false even though the
	// interview otherwise completes; walk without fullAnswers' own
	// BootstrapReady assertion, which this test deliberately violates.
	facts := bothHostsFacts(root)
	facts.Gh = false
	answers := answersWithoutReadyCheck(t, facts, root)

	lookPath := func(name string) (string, error) {
		if name == "gh" {
			return "", os.ErrNotExist
		}
		return fakeLookPath(name)
	}
	deps := Deps{RepoRoot: root, Answers: answers, Runner: muxRunner{git: execx.Local{}, gh: &execxtest.Script{T: t}}, LookPath: lookPath, Now: fixedNow}

	_, err := Execute(context.Background(), deps)
	if !errors.Is(err, ErrNotBootstrapReady) {
		t.Fatalf("err = %v, want ErrNotBootstrapReady", err)
	}
	if !strings.Contains(err.Error(), "gh was not detected") {
		t.Errorf("err = %v, want it to name gh", err)
	}
}

func TestExecuteAlreadyInitialized(t *testing.T) {
	root := newBootstrapRepo(t)
	answers := fullAnswers(t, bothHostsFacts(root), root)

	if err := os.MkdirAll(filepath.Join(root, ".orchestrator"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, config.Path), []byte("schema_version = 1\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	deps := testDeps(root, answers, &execxtest.Script{T: t}, muxRunner{git: execx.Local{}})
	_, err := Execute(context.Background(), deps)
	if !errors.Is(err, ErrAlreadyInitialized) {
		t.Fatalf("err = %v, want ErrAlreadyInitialized", err)
	}
}

func TestExecuteDirtyTreeFailsClosed(t *testing.T) {
	root := newBootstrapRepo(t)
	answers := fullAnswers(t, bothHostsFacts(root), root)

	if err := os.WriteFile(filepath.Join(root, "untracked.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	deps := testDeps(root, answers, &execxtest.Script{T: t}, muxRunner{git: execx.Local{}})
	_, err := Execute(context.Background(), deps)
	if !errors.Is(err, gitops.ErrNotClean) {
		t.Fatalf("err = %v, want ErrNotClean", err)
	}
}

func TestExecuteGhNotAuthenticated(t *testing.T) {
	root := newBootstrapRepo(t)
	answers := fullAnswers(t, bothHostsFacts(root), root)

	script := &execxtest.Script{T: t, Calls: []execxtest.Call{ghAuthFailsCall()}}
	deps := testDeps(root, answers, script, muxRunner{git: execx.Local{}})
	_, err := Execute(context.Background(), deps)
	if !errors.Is(err, ghops.ErrNotAuthenticated) {
		t.Fatalf("err = %v, want ErrNotAuthenticated", err)
	}
	script.AssertExhausted()
}

func TestExecuteLocalBranchOrphanFailsClosed(t *testing.T) {
	root := newBootstrapRepo(t)
	answers := fullAnswers(t, bothHostsFacts(root), root)
	rawGit(t, root, "branch", BootstrapBranch)

	script := &execxtest.Script{T: t, Calls: []execxtest.Call{ghAuthCall(), ghRepoViewCall("main")}}
	deps := testDeps(root, answers, script, muxRunner{git: execx.Local{}})
	_, err := Execute(context.Background(), deps)
	if !errors.Is(err, ErrBranchExists) {
		t.Fatalf("err = %v, want ErrBranchExists", err)
	}
	if !strings.Contains(err.Error(), "git branch -D orch/bootstrap") {
		t.Errorf("err = %v, want the exact remediation command", err)
	}
	script.AssertExhausted()
}

func TestExecuteRemoteBranchOrphanFailsClosed(t *testing.T) {
	root := newBootstrapRepo(t)
	answers := fullAnswers(t, bothHostsFacts(root), root)
	rawGit(t, root, "branch", BootstrapBranch)
	rawGit(t, root, "push", "origin", BootstrapBranch)
	rawGit(t, root, "branch", "-D", BootstrapBranch)

	script := &execxtest.Script{T: t, Calls: []execxtest.Call{ghAuthCall(), ghRepoViewCall("main")}}
	deps := testDeps(root, answers, script, muxRunner{git: execx.Local{}})
	_, err := Execute(context.Background(), deps)
	if !errors.Is(err, ErrRemoteBranchExists) {
		t.Fatalf("err = %v, want ErrRemoteBranchExists", err)
	}
	if !strings.Contains(err.Error(), "git push origin --delete orch/bootstrap") {
		t.Errorf("err = %v, want the exact remediation command", err)
	}
	script.AssertExhausted()
}

func TestExecuteOpenPROrphanFailsClosed(t *testing.T) {
	root := newBootstrapRepo(t)
	answers := fullAnswers(t, bothHostsFacts(root), root)

	prJSON := `[{"number":9,"state":"OPEN","title":"t","url":"https://github.com/o/r/pull/9","headRefName":"orch/bootstrap","baseRefName":"main","headRefOid":"h","mergeStateStatus":"CLEAN","mergedAt":null,"body":""}]`
	script := &execxtest.Script{T: t, Calls: []execxtest.Call{ghAuthCall(), ghRepoViewCall("main"), ghPRForBranchCall(BootstrapBranch, prJSON)}}
	deps := testDeps(root, answers, script, muxRunner{git: execx.Local{}})
	_, err := Execute(context.Background(), deps)
	if !errors.Is(err, ErrOpenPRExists) {
		t.Fatalf("err = %v, want ErrOpenPRExists", err)
	}
	if !strings.Contains(err.Error(), "PR #9") {
		t.Errorf("err = %v, want it to name the PR", err)
	}
	script.AssertExhausted()
}

// TestExecutePreCommitFailureCleansUp forces writeFiles to fail
// (corruptAfterAdd blocks os.MkdirAll inside the fresh worktree) and
// asserts the worktree is removed, the just-created local branch is
// force-deleted, the issue is marked needs-human, and the error names
// the remediation.
func TestExecutePreCommitFailureCleansUp(t *testing.T) {
	root := newBootstrapRepo(t)
	answers := fullAnswers(t, bothHostsFacts(root), root)

	calls := taxonomyScript()
	calls = append(calls, ghIssueCreateCall(1))
	calls = append(calls, ghSetStatusCall(1, ghops.StatusNeedsHuman))
	script := &execxtest.Script{T: t, Calls: calls}
	deps := testDeps(root, answers, script, muxRunner{git: execx.Local{}, corruptAfterAdd: true})

	_, err := Execute(context.Background(), deps)
	if err == nil {
		t.Fatal("Execute succeeded, want a pre-commit write failure")
	}
	script.AssertExhausted()
	if !strings.Contains(err.Error(), "issue #1") {
		t.Errorf("err = %v, want it to name issue #1", err)
	}
	if !strings.Contains(err.Error(), "re-run `orch init --bootstrap`") {
		t.Errorf("err = %v, want the re-run remediation", err)
	}

	git, gerr := gitops.Open(context.Background(), execx.Local{}, root)
	if gerr != nil {
		t.Fatal(gerr)
	}
	worktrees, lerr := git.ListWorktrees(context.Background())
	if lerr != nil {
		t.Fatal(lerr)
	}
	if len(worktrees) != 1 {
		t.Errorf("ListWorktrees = %+v, want only the primary checkout (worktree removed)", worktrees)
	}
	if _, perr := git.RevParse(context.Background(), BootstrapBranch); perr == nil {
		t.Error("local branch still resolves; want it force-deleted (no commit was ever made)")
	}
}

// TestExecutePushFailureLeavesArtifacts forces `git push` to fail
// after the commit already succeeded, and asserts the issue and
// branch are both named as preserved artifacts (the branch is real
// work now, so it must not be deleted) and the issue is marked
// needs-human.
func TestExecutePushFailureLeavesArtifacts(t *testing.T) {
	root := newBootstrapRepo(t)
	answers := fullAnswers(t, bothHostsFacts(root), root)

	calls := taxonomyScript()
	calls = append(calls, ghIssueCreateCall(1))
	calls = append(calls, ghSetStatusCall(1, ghops.StatusNeedsHuman))
	script := &execxtest.Script{T: t, Calls: calls}
	deps := testDeps(root, answers, script, muxRunner{git: execx.Local{}, pushFails: true})

	_, err := Execute(context.Background(), deps)
	if err == nil {
		t.Fatal("Execute succeeded, want a push failure")
	}
	script.AssertExhausted()
	if !strings.Contains(err.Error(), "issue #1") {
		t.Errorf("err = %v, want it to name issue #1", err)
	}
	if !strings.Contains(err.Error(), "carries the bootstrap commit") {
		t.Errorf("err = %v, want it to preserve (not delete) the branch carrying the commit", err)
	}

	git, gerr := gitops.Open(context.Background(), execx.Local{}, root)
	if gerr != nil {
		t.Fatal(gerr)
	}
	worktrees, lerr := git.ListWorktrees(context.Background())
	if lerr != nil {
		t.Fatal(lerr)
	}
	if len(worktrees) != 1 {
		t.Errorf("ListWorktrees = %+v, want only the primary checkout (worktree removed)", worktrees)
	}
	if _, perr := git.RevParse(context.Background(), BootstrapBranch); perr != nil {
		t.Errorf("local branch %s not preserved after a push failure: %v", BootstrapBranch, perr)
	}
}
