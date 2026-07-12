package bootstrap

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kninetimmy/orch/internal/config"
	"github.com/kninetimmy/orch/internal/execx"
	"github.com/kninetimmy/orch/internal/execx/execxtest"
	"github.com/kninetimmy/orch/internal/ghops"
	"github.com/kninetimmy/orch/internal/gitops"
	"github.com/kninetimmy/orch/internal/instructions"
	"github.com/kninetimmy/orch/internal/interview"
	"github.com/kninetimmy/orch/internal/lockfile"
	"github.com/kninetimmy/orch/internal/paths"
	"github.com/kninetimmy/orch/internal/question"
	"github.com/kninetimmy/orch/internal/state"
)

// writeConfiguredFixtureFiles writes root's committed configuration
// (both hosts, matching interview's own both-hosts committed fixture),
// CLAUDE.md/AGENTS.md with the current managed block already installed,
// and a .gitignore already carrying every base line — so a session
// that changes nothing about host enablement or instruction files
// still ends in a real, isolated configuration change (a single
// setting) rather than tripping over unrelated "needs installing"
// diffs that would otherwise dominate every test's assertions.
func writeConfiguredFixtureFiles(t *testing.T, root string) {
	t.Helper()
	cfg := &config.Config{
		SchemaVersion: 1,
		Concurrency:   config.Concurrency{MaxSubagents: 3},
		Merge:         config.Merge{Strategy: "squash"},
		Memhub:        config.Memhub{Mode: "off"},
		Hosts: config.Hosts{
			Claude: &config.Host{Roles: config.Roles{
				Architect:       config.RoleProfile{Model: "claude-opus-4-8", Effort: "xhigh"},
				Scout:           config.RoleProfile{Model: "claude-sonnet-5", Effort: "low"},
				Implementer:     config.RoleProfile{Model: "claude-sonnet-5", Effort: "xhigh"},
				Specialist:      config.RoleProfile{Model: "claude-opus-4-8", Effort: "high"},
				Reviewer:        config.RoleProfile{Model: "claude-opus-4-8", Effort: "high"},
				ReviewDowngrade: config.RoleProfile{Model: "claude-sonnet-5", Effort: "high"},
			}},
			Codex: &config.Host{Roles: config.Roles{
				Architect:       config.RoleProfile{Model: "gpt-5.6-sol", Effort: "high"},
				Scout:           config.RoleProfile{Model: "gpt-5.6-terra", Effort: "low"},
				Implementer:     config.RoleProfile{Model: "gpt-5.6-terra", Effort: "high"},
				Specialist:      config.RoleProfile{Model: "gpt-5.6-sol", Effort: "medium"},
				Reviewer:        config.RoleProfile{Model: "gpt-5.6-sol", Effort: "medium"},
				ReviewDowngrade: config.RoleProfile{Model: "gpt-5.6-terra", Effort: "high"},
			}},
		},
	}
	rev, err := config.Revision(cfg)
	if err != nil {
		t.Fatal(err)
	}
	cfg.ConfigRevision = rev
	rendered, err := config.Render(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, ".orchestrator"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, filepath.FromSlash(config.Path)), rendered, 0o644); err != nil {
		t.Fatal(err)
	}

	block, err := instructions.Render(instructions.CurrentVersion)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "CLAUDE.md"), []byte(block), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "AGENTS.md"), []byte(block), 0o644); err != nil {
		t.Fatal(err)
	}

	gitignore := strings.Join([]string{
		".orchestrator/worktrees/",
		".orchestrator/config.local.toml",
		".orchestrator/state.json",
		".orchestrator/delivery.lock",
	}, "\n") + "\n"
	if err := os.WriteFile(filepath.Join(root, ".gitignore"), []byte(gitignore), 0o644); err != nil {
		t.Fatal(err)
	}
}

// writeConfiguredFixtureFilesClaudeOnly is writeConfiguredFixtureFiles
// restricted to a claude-only committed configuration, with no
// AGENTS.md at all (codex was never enabled) — the fixture the
// disable-validation-failure test needs so codex's absence-check has
// something to be tricked into failing.
func writeConfiguredFixtureFilesClaudeOnly(t *testing.T, root string) {
	t.Helper()
	cfg := &config.Config{
		SchemaVersion: 1,
		Concurrency:   config.Concurrency{MaxSubagents: 3},
		Merge:         config.Merge{Strategy: "squash"},
		Memhub:        config.Memhub{Mode: "off"},
		Hosts: config.Hosts{
			Claude: &config.Host{Roles: config.Roles{
				Architect:       config.RoleProfile{Model: "claude-opus-4-8", Effort: "xhigh"},
				Scout:           config.RoleProfile{Model: "claude-sonnet-5", Effort: "low"},
				Implementer:     config.RoleProfile{Model: "claude-sonnet-5", Effort: "xhigh"},
				Specialist:      config.RoleProfile{Model: "claude-opus-4-8", Effort: "high"},
				Reviewer:        config.RoleProfile{Model: "claude-opus-4-8", Effort: "high"},
				ReviewDowngrade: config.RoleProfile{Model: "claude-sonnet-5", Effort: "high"},
			}},
		},
	}
	rev, err := config.Revision(cfg)
	if err != nil {
		t.Fatal(err)
	}
	cfg.ConfigRevision = rev
	rendered, err := config.Render(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, ".orchestrator"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, filepath.FromSlash(config.Path)), rendered, 0o644); err != nil {
		t.Fatal(err)
	}

	block, err := instructions.Render(instructions.CurrentVersion)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "CLAUDE.md"), []byte(block), 0o644); err != nil {
		t.Fatal(err)
	}

	gitignore := strings.Join([]string{
		".orchestrator/worktrees/",
		".orchestrator/config.local.toml",
		".orchestrator/state.json",
		".orchestrator/delivery.lock",
	}, "\n") + "\n"
	if err := os.WriteFile(filepath.Join(root, ".gitignore"), []byte(gitignore), 0o644); err != nil {
		t.Fatal(err)
	}
}

// newConfiguredRepo builds a real sandbox repository on branch main,
// already initialized (both hosts enabled, instruction files
// installed, .gitignore complete) and committed, with a bare origin
// remote with main pushed — `orch configure`'s own fixture, unlike
// newBootstrapRepo, which deliberately carries no .orchestrator/ at
// all.
func newConfiguredRepo(t *testing.T) string {
	t.Helper()
	setupGitEnv(t)
	root, err := paths.Canonical(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	rawGit(t, root, "init", "-b", "main")
	writeConfiguredFixtureFiles(t, root)
	rawGit(t, root, "add", "-A")
	rawGit(t, root, "commit", "-m", "initial")

	origin := filepath.Join(t.TempDir(), "origin.git")
	rawGit(t, filepath.Dir(origin), "init", "--bare", origin)
	rawGit(t, root, "remote", "add", "origin", origin)
	rawGit(t, root, "push", "origin", "main")
	return root
}

// newConfiguredRepoClaudeOnly is newConfiguredRepo restricted to a
// claude-only committed configuration.
func newConfiguredRepoClaudeOnly(t *testing.T) string {
	t.Helper()
	setupGitEnv(t)
	root, err := paths.Canonical(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	rawGit(t, root, "init", "-b", "main")
	writeConfiguredFixtureFilesClaudeOnly(t, root)
	rawGit(t, root, "add", "-A")
	rawGit(t, root, "commit", "-m", "initial")

	origin := filepath.Join(t.TempDir(), "origin.git")
	rawGit(t, filepath.Dir(origin), "init", "--bare", origin)
	rawGit(t, root, "remote", "add", "origin", origin)
	rawGit(t, root, "push", "origin", "main")
	return root
}

// fullConfigureAnswers walks interview.NextConfigure to completion
// from an empty answer set, answering every question with its Default
// unless overrides names a specific answer, and approving the summary
// once reached (fullAnswers' precedent, duplicated for NextConfigure
// since interview's own walk helpers are unexported).
func fullConfigureAnswers(t *testing.T, facts interview.Facts, repoRoot string, overrides map[string]string) map[string]string {
	t.Helper()
	answers := map[string]string{}
	for i := 0; i < 100; i++ {
		doc, err := interview.NextConfigure(facts, answers, repoRoot)
		if err != nil {
			t.Fatalf("NextConfigure: %v", err)
		}
		switch doc.Kind {
		case question.DocQuestions:
			for _, q := range doc.Questions {
				if v, ok := overrides[q.ID]; ok {
					answers[q.ID] = v
					continue
				}
				if q.Default == "" {
					t.Fatalf("question %s has no default", q.ID)
				}
				answers[q.ID] = q.Default
			}
		case question.DocSummary:
			if len(doc.Summary.Blockers) != 0 {
				t.Fatalf("unexpected blockers: %v", doc.Summary.Blockers)
			}
			answers["approval"] = "approve"
		case question.DocComplete:
			if !doc.Complete.BootstrapReady {
				t.Fatalf("Complete.BootstrapReady = false, want true: %+v", doc.Complete)
			}
			return answers
		default:
			t.Fatalf("unexpected document kind %q", doc.Kind)
		}
	}
	t.Fatal("NextConfigure did not reach a complete document within 100 steps")
	return nil
}

// changeMergeStrategyOverrides picks the settings area and changes
// merge.strategy to rebase — the smallest real, deterministic edit
// every configure test that needs "some approvable change" (as opposed
// to one exercising a specific host-enablement scenario) can use.
var changeMergeStrategyOverrides = map[string]string{"pick.settings": "yes", "merge.strategy": "rebase"}

func ghConfigureIssueCreateCall(number int) execxtest.Call {
	args := []string{"issue", "create", "--title", configureTitle, "--body-file", "-"}
	for _, l := range []string{"in-progress", "infra", "implementer", "standard"} {
		args = append(args, "--label", l)
	}
	return execxtest.Call{Name: "gh", Args: args, Stdout: fmt.Sprintf("https://github.com/o/r/issues/%d\n", number)}
}

func ghConfigurePRCreateCall(number int) execxtest.Call {
	return execxtest.Call{
		Name:   "gh",
		Args:   []string{"pr", "create", "--head", ConfigureBranch, "--base", "main", "--title", configureTitle, "--body-file", "-"},
		Stdout: fmt.Sprintf("https://github.com/o/r/pull/%d\n", number),
	}
}

// preflightScriptConfigure is preflightScript against ConfigureBranch.
func preflightScriptConfigure() []execxtest.Call {
	return []execxtest.Call{ghAuthCall(), ghRepoViewCall("main"), ghPRForBranchCall(ConfigureBranch, "[]")}
}

// taxonomyScriptConfigure is taxonomyScript against ConfigureBranch.
func taxonomyScriptConfigure() []execxtest.Call {
	calls := preflightScriptConfigure()
	calls = append(calls, ghLabelListEmptyCall())
	return append(calls, ghLabelCreateCalls()...)
}

func TestExecuteConfigureHappyPath(t *testing.T) {
	root := newConfiguredRepo(t)
	answers := fullConfigureAnswers(t, bothHostsFacts(root), root, changeMergeStrategyOverrides)

	calls := taxonomyScriptConfigure()
	calls = append(calls, ghConfigureIssueCreateCall(1))
	calls = append(calls, ghConfigurePRCreateCall(1))
	calls = append(calls, ghSetStatusCall(1, ghops.StatusAwaitingReview))
	script := &execxtest.Script{T: t, Calls: calls}
	deps := testDeps(root, answers, script, muxRunner{git: execx.Local{}})

	report, err := ExecuteConfigure(context.Background(), deps)
	if err != nil {
		t.Fatalf("ExecuteConfigure: %v", err)
	}
	script.AssertExhausted()

	if report.Branch != ConfigureBranch {
		t.Errorf("Branch = %q, want %q", report.Branch, ConfigureBranch)
	}
	if report.Issue.Number != 1 || report.PR.Number != 1 {
		t.Errorf("Issue/PR = %+v/%+v", report.Issue, report.PR)
	}
	for _, v := range report.Validations {
		if v.Result != "pass" {
			t.Errorf("validation %s = %+v, want pass", v.Name, v)
		}
	}

	// The primary checkout was never written.
	got, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(config.Path)))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(got), `strategy = "rebase"`) {
		t.Error("primary checkout's config.toml carries the new merge strategy; ExecuteConfigure must never write the primary checkout")
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

	// The branch carries the new merge strategy and a PR body naming
	// both the diff and the closed issue.
	head, err := git.RevParse(context.Background(), ConfigureBranch)
	if err != nil || head == "" {
		t.Fatalf("local branch %s not preserved: %v", ConfigureBranch, err)
	}
	show := rawGit(t, root, "show", head+":"+config.Path)
	if !strings.Contains(show, `strategy = "rebase"`) {
		t.Errorf("committed config.toml on %s does not carry the new merge strategy:\n%s", ConfigureBranch, show)
	}
	commitMsg := rawGit(t, root, "log", "-1", "--format=%B", head)
	if !strings.Contains(commitMsg, "Update orch configuration") || !strings.Contains(commitMsg, "Closes #1") {
		t.Errorf("commit message = %q, want the configure title and Closes #1", commitMsg)
	}
}

func TestExecuteConfigureNotComplete(t *testing.T) {
	root := newConfiguredRepo(t)
	deps := testDeps(root, map[string]string{}, &execxtest.Script{T: t}, muxRunner{git: execx.Local{}})

	_, err := ExecuteConfigure(context.Background(), deps)
	if !errors.Is(err, ErrNotComplete) {
		t.Fatalf("err = %v, want ErrNotComplete", err)
	}
}

func TestExecuteConfigureUninitialized(t *testing.T) {
	root := newBootstrapRepo(t) // no .orchestrator/ at all
	deps := testDeps(root, map[string]string{}, &execxtest.Script{T: t}, muxRunner{git: execx.Local{}})

	_, err := ExecuteConfigure(context.Background(), deps)
	if !errors.Is(err, config.ErrNotInitialized) {
		t.Fatalf("err = %v, want config.ErrNotInitialized", err)
	}
}

func TestExecuteConfigureDeliveryLockActive(t *testing.T) {
	root := newConfiguredRepo(t)
	if err := lockfile.Acquire(root, lockfile.Owner{
		RunID: "r1", Host: "claude", Hostname: "h", PID: 1, AcquiredAt: time.Now(),
	}); err != nil {
		t.Fatal(err)
	}

	deps := testDeps(root, map[string]string{}, &execxtest.Script{T: t}, muxRunner{git: execx.Local{}})
	_, err := ExecuteConfigure(context.Background(), deps)
	if !errors.Is(err, ErrDeliveryActive) {
		t.Fatalf("err = %v, want ErrDeliveryActive", err)
	}
	if !strings.Contains(err.Error(), "delivery lock") {
		t.Errorf("err = %v, want it to name the delivery lock", err)
	}
}

func TestExecuteConfigureStateModeDelivery(t *testing.T) {
	root := newConfiguredRepo(t)
	planRef := state.PlanRef{Title: "t", Digest: "sha256:test", ConfigRevision: "r1"}
	issues := []state.Issue{{PlanID: "iss-a", Title: "A", Phase: state.PhasePlanned}}
	if _, err := state.EnterDelivery(root, "claude", planRef, issues); err != nil {
		t.Fatal(err)
	}

	deps := testDeps(root, map[string]string{}, &execxtest.Script{T: t}, muxRunner{git: execx.Local{}})
	_, err := ExecuteConfigure(context.Background(), deps)
	if !errors.Is(err, ErrDeliveryActive) {
		t.Fatalf("err = %v, want ErrDeliveryActive", err)
	}
	if !strings.Contains(err.Error(), "delivery run") {
		t.Errorf("err = %v, want it to name the active delivery run", err)
	}
}

func TestExecuteConfigureUnreadableStateFailsClosed(t *testing.T) {
	root := newConfiguredRepo(t)
	if err := os.WriteFile(filepath.Join(root, ".orchestrator", "state.json"), []byte("{not valid json"), 0o644); err != nil {
		t.Fatal(err)
	}

	deps := testDeps(root, map[string]string{}, &execxtest.Script{T: t}, muxRunner{git: execx.Local{}})
	_, err := ExecuteConfigure(context.Background(), deps)
	if err == nil {
		t.Fatal("ExecuteConfigure succeeded, want a fail-closed error over the unreadable state file")
	}
	if errors.Is(err, ErrDeliveryActive) {
		t.Errorf("err = %v, want the raw state.Load parse error, not ErrDeliveryActive (an unreadable file is a distinct failure from a genuinely active run)", err)
	}
}

func TestExecuteConfigureDirtyTreeFailsClosed(t *testing.T) {
	root := newConfiguredRepo(t)
	answers := fullConfigureAnswers(t, bothHostsFacts(root), root, changeMergeStrategyOverrides)

	if err := os.WriteFile(filepath.Join(root, "untracked.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	deps := testDeps(root, answers, &execxtest.Script{T: t}, muxRunner{git: execx.Local{}})
	_, err := ExecuteConfigure(context.Background(), deps)
	if !errors.Is(err, gitops.ErrNotClean) {
		t.Fatalf("err = %v, want ErrNotClean", err)
	}
}

func TestExecuteConfigureLocalBranchOrphanFailsClosed(t *testing.T) {
	root := newConfiguredRepo(t)
	answers := fullConfigureAnswers(t, bothHostsFacts(root), root, changeMergeStrategyOverrides)
	rawGit(t, root, "branch", ConfigureBranch)

	script := &execxtest.Script{T: t, Calls: []execxtest.Call{ghAuthCall(), ghRepoViewCall("main")}}
	deps := testDeps(root, answers, script, muxRunner{git: execx.Local{}})
	_, err := ExecuteConfigure(context.Background(), deps)
	if !errors.Is(err, ErrBranchExists) {
		t.Fatalf("err = %v, want ErrBranchExists", err)
	}
	if !strings.Contains(err.Error(), "git branch -D "+ConfigureBranch) || !strings.Contains(err.Error(), "orch configure --deliver") {
		t.Errorf("err = %v, want the exact remediation command naming `orch configure --deliver`", err)
	}
	script.AssertExhausted()
}

func TestExecuteConfigureRemoteBranchOrphanFailsClosed(t *testing.T) {
	root := newConfiguredRepo(t)
	answers := fullConfigureAnswers(t, bothHostsFacts(root), root, changeMergeStrategyOverrides)
	rawGit(t, root, "branch", ConfigureBranch)
	rawGit(t, root, "push", "origin", ConfigureBranch)
	rawGit(t, root, "branch", "-D", ConfigureBranch)

	script := &execxtest.Script{T: t, Calls: []execxtest.Call{ghAuthCall(), ghRepoViewCall("main")}}
	deps := testDeps(root, answers, script, muxRunner{git: execx.Local{}})
	_, err := ExecuteConfigure(context.Background(), deps)
	if !errors.Is(err, ErrRemoteBranchExists) {
		t.Fatalf("err = %v, want ErrRemoteBranchExists", err)
	}
	if !strings.Contains(err.Error(), "orch configure --deliver") {
		t.Errorf("err = %v, want the exact remediation naming `orch configure --deliver`", err)
	}
	script.AssertExhausted()
}

func TestExecuteConfigureOpenPROrphanFailsClosed(t *testing.T) {
	root := newConfiguredRepo(t)
	answers := fullConfigureAnswers(t, bothHostsFacts(root), root, changeMergeStrategyOverrides)

	prJSON := `[{"number":9,"state":"OPEN","title":"t","url":"https://github.com/o/r/pull/9","headRefName":"orch/configure","baseRefName":"main","headRefOid":"h","mergeStateStatus":"CLEAN","mergedAt":null,"body":""}]`
	script := &execxtest.Script{T: t, Calls: []execxtest.Call{ghAuthCall(), ghRepoViewCall("main"), ghPRForBranchCall(ConfigureBranch, prJSON)}}
	deps := testDeps(root, answers, script, muxRunner{git: execx.Local{}})
	_, err := ExecuteConfigure(context.Background(), deps)
	if !errors.Is(err, ErrOpenPRExists) {
		t.Fatalf("err = %v, want ErrOpenPRExists", err)
	}
	if !strings.Contains(err.Error(), "PR #9") || !strings.Contains(err.Error(), "orch configure --deliver") {
		t.Errorf("err = %v, want it to name the PR and the `orch configure --deliver` remediation", err)
	}
	script.AssertExhausted()
}

// TestExecuteConfigurePushFailureLeavesArtifacts forces `git push` to
// fail after the commit already succeeded, and asserts the issue and
// branch are both preserved (the branch is real work now) and the
// issue is marked needs-human.
func TestExecuteConfigurePushFailureLeavesArtifacts(t *testing.T) {
	root := newConfiguredRepo(t)
	answers := fullConfigureAnswers(t, bothHostsFacts(root), root, changeMergeStrategyOverrides)

	calls := taxonomyScriptConfigure()
	calls = append(calls, ghConfigureIssueCreateCall(1))
	calls = append(calls, ghSetStatusCall(1, ghops.StatusNeedsHuman))
	script := &execxtest.Script{T: t, Calls: calls}
	deps := testDeps(root, answers, script, muxRunner{git: execx.Local{}, pushFails: true})

	_, err := ExecuteConfigure(context.Background(), deps)
	if err == nil {
		t.Fatal("ExecuteConfigure succeeded, want a push failure")
	}
	script.AssertExhausted()
	if !strings.Contains(err.Error(), "issue #1") {
		t.Errorf("err = %v, want it to name issue #1", err)
	}
	if !strings.Contains(err.Error(), "carries the update commit") {
		t.Errorf("err = %v, want it to preserve (not delete) the branch carrying the commit", err)
	}
	if !strings.Contains(err.Error(), "orch configure --deliver") {
		t.Errorf("err = %v, want the re-run remediation naming `orch configure --deliver`", err)
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
	if _, perr := git.RevParse(context.Background(), ConfigureBranch); perr != nil {
		t.Errorf("local branch %s not preserved after a push failure: %v", ConfigureBranch, perr)
	}
}

// TestExecuteConfigureDisableValidationFailure proves validateConfigure's
// own per-host absent check: a claude-only committed configuration is
// edited (a settings-only change, never touching host enablement), but
// the worktree's AGENTS.md is planted with a valid current managed
// block immediately after the worktree is created — codex was never
// enabled, so validateConfigure expects AGENTS.md StatusAbsent and
// fails closed when it instead finds StatusCurrent.
func TestExecuteConfigureDisableValidationFailure(t *testing.T) {
	root := newConfiguredRepoClaudeOnly(t)
	facts := interview.Facts{ClaudeCLI: true, CodexCLI: true, Git: true, GitRoot: root, Gh: true}
	answers := fullConfigureAnswers(t, facts, root, changeMergeStrategyOverrides)

	block, err := instructions.Render(instructions.CurrentVersion)
	if err != nil {
		t.Fatal(err)
	}

	calls := taxonomyScriptConfigure()
	calls = append(calls, ghConfigureIssueCreateCall(1))
	calls = append(calls, ghSetStatusCall(1, ghops.StatusNeedsHuman))
	script := &execxtest.Script{T: t, Calls: calls}
	deps := testDeps(root, answers, script, muxRunner{git: execx.Local{}, injectAfterAdd: "AGENTS.md", injectedContent: block})

	_, err = ExecuteConfigure(context.Background(), deps)
	if !errors.Is(err, ErrValidationFailed) {
		t.Fatalf("err = %v, want ErrValidationFailed", err)
	}
	if !strings.Contains(err.Error(), "instructions:AGENTS.md") {
		t.Errorf("err = %v, want it to name instructions:AGENTS.md", err)
	}
	script.AssertExhausted()

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
	if _, perr := git.RevParse(context.Background(), ConfigureBranch); perr == nil {
		t.Error("local branch still resolves; want it force-deleted (no commit was ever made)")
	}
}

// TestConfigureBranchDriftGuards pins ConfigureBranch distinct from
// BootstrapBranch and outside internal/run's per-issue orch/issue-<n>-
// prefix space (internal/run's names.go builds that prefix inline with
// no exported constant to import against, so the literal is duplicated
// here — the same literal-duplication precedent Detect's own
// execProber duplication and interview's baseGitignoreLines already
// use).
func TestConfigureBranchDriftGuards(t *testing.T) {
	if ConfigureBranch == BootstrapBranch {
		t.Fatalf("ConfigureBranch (%q) must not equal BootstrapBranch", ConfigureBranch)
	}
	if strings.HasPrefix(ConfigureBranch, "orch/issue-") {
		t.Fatalf("ConfigureBranch (%q) must not fall in run's orch/issue-<n>- prefix space", ConfigureBranch)
	}
	if strings.HasPrefix(BootstrapBranch, "orch/issue-") {
		t.Fatalf("BootstrapBranch (%q) must not fall in run's orch/issue-<n>- prefix space", BootstrapBranch)
	}
}

// TestInstructionFilePinned drift-pins interview.InstructionFile — the
// host-name-to-root-instruction-file mapping validateConfigure's
// per-host check iterates through.
func TestInstructionFilePinned(t *testing.T) {
	want := map[string]string{"claude": "CLAUDE.md", "codex": "AGENTS.md"}
	for host, file := range want {
		if got := interview.InstructionFile(host); got != file {
			t.Errorf("interview.InstructionFile(%q) = %q, want %q", host, got, file)
		}
	}
}
