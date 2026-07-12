package bootstrap

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/kninetimmy/orch/internal/execx"
	"github.com/kninetimmy/orch/internal/execx/execxtest"
	"github.com/kninetimmy/orch/internal/ghops"
	"github.com/kninetimmy/orch/internal/interview"
	"github.com/kninetimmy/orch/internal/paths"
	"github.com/kninetimmy/orch/internal/question"
)

// setupGitEnv isolates every git invocation in the test process from
// the developer's real configuration (internal/run/activate_test.go's
// idiom, copied from internal/gitops's own integration-test idiom).
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

// newBootstrapRepo builds a real sandbox repository on branch main with
// a committed README (no .orchestrator/, so bootstrap's
// not-already-initialized preflight holds) and a bare origin remote
// with main pushed, so the push/PR paths have a remote to work
// against.
func newBootstrapRepo(t *testing.T) string {
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
	rawGit(t, root, "add", "-A")
	rawGit(t, root, "commit", "-m", "initial")

	origin := filepath.Join(t.TempDir(), "origin.git")
	rawGit(t, filepath.Dir(origin), "init", "--bare", origin)
	rawGit(t, root, "remote", "add", "origin", origin)
	rawGit(t, root, "push", "origin", "main")
	return root
}

// muxRunner sends "git" commands to a real runner and "gh" commands to
// a scripted one (internal/run/activate_test.go's idiom): real git
// sandboxes prove the actual argv sequence and filesystem effects,
// scripted GitHub calls pin the exact gh invocations without a network.
type muxRunner struct {
	git execx.Runner
	gh  *execxtest.Script
	// pushFails, when true, fails `git push` without touching the real
	// repository, so a push-failure test can exercise Stage 1's
	// post-commit cleanup path deterministically.
	pushFails bool
	// corruptAfterAdd, when true, drops a file named ".orchestrator"
	// (blocking os.MkdirAll) into the worktree directory immediately
	// after `git worktree add` succeeds, so a pre-commit write failure
	// can be forced deterministically.
	corruptAfterAdd bool
}

func (m muxRunner) Run(ctx context.Context, c execx.Cmd) (execx.Result, error) {
	if c.Name != "git" {
		return m.gh.Run(ctx, c)
	}
	if m.pushFails && len(c.Args) > 0 && c.Args[0] == "push" {
		return execx.Result{ExitCode: 1, Stderr: "fatal: unable to access '(simulated)': could not resolve host"}, nil
	}
	res, err := m.git.Run(ctx, c)
	if m.corruptAfterAdd && err == nil && res.ExitCode == 0 &&
		len(c.Args) >= 5 && c.Args[0] == "worktree" && c.Args[1] == "add" {
		path := c.Args[4]
		if werr := os.WriteFile(filepath.Join(path, ".orchestrator"), []byte("blocker"), 0o644); werr != nil {
			return execx.Result{}, fmt.Errorf("test setup: corrupt worktree: %w", werr)
		}
	}
	return res, err
}

// fakeLookPath resolves claude, codex, and gh (both host CLIs plus
// GitHub, so BootstrapReady only ever turns on git/gh), and fails
// closed on everything else — including memhub, so Detect's
// memhub-probe path (a fake `memhub status` call) is never exercised
// by these tests.
func fakeLookPath(name string) (string, error) {
	switch name {
	case "claude", "codex", "gh", "git":
		return "/fake/" + name, nil
	default:
		return "", fmt.Errorf("%s not found", name)
	}
}

// fixedNow is the deterministic clock Deps.Now injects.
func fixedNow() time.Time { return time.Date(2026, 7, 11, 12, 0, 0, 0, time.UTC) }

// fullAnswers walks the interview to completion from an empty answer
// set, answering every question with its Default and approving the
// summary (interview's own answerAllWithDefaults/TestGoldenTranscript
// idiom, duplicated here since it is unexported in internal/interview
// and this package's tests need the walk to run against a real
// sandbox root rather than the interview package's own fixtures).
func fullAnswers(t *testing.T, facts interview.Facts, repoRoot string) map[string]string {
	t.Helper()
	answers := map[string]string{}
	for i := 0; i < 100; i++ {
		doc, err := interview.Next(facts, answers, repoRoot)
		if err != nil {
			t.Fatalf("Next: %v", err)
		}
		switch doc.Kind {
		case question.DocQuestions:
			for _, q := range doc.Questions {
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
	t.Fatal("interview did not reach a complete document within 100 steps")
	return nil
}

// answersWithoutReadyCheck is fullAnswers without its BootstrapReady
// assertion, for tests that deliberately drive the interview to
// completion with a fact that makes BootstrapReady false (e.g. gh
// undetected) and want to exercise Execute's own re-derived check.
func answersWithoutReadyCheck(t *testing.T, facts interview.Facts, repoRoot string) map[string]string {
	t.Helper()
	answers := map[string]string{}
	for i := 0; i < 100; i++ {
		doc, err := interview.Next(facts, answers, repoRoot)
		if err != nil {
			t.Fatalf("Next: %v", err)
		}
		switch doc.Kind {
		case question.DocQuestions:
			for _, q := range doc.Questions {
				answers[q.ID] = q.Default
			}
		case question.DocSummary:
			answers["approval"] = "approve"
		case question.DocComplete:
			return answers
		default:
			t.Fatalf("unexpected document kind %q", doc.Kind)
		}
	}
	t.Fatal("interview did not reach a complete document within 100 steps")
	return nil
}

// bothHostsFacts mirrors interview's own fixture: both host CLIs, git,
// and gh detected (no memhub — fakeLookPath never resolves it).
func bothHostsFacts(repoRoot string) interview.Facts {
	return interview.Facts{ClaudeCLI: true, CodexCLI: true, Git: true, GitRoot: repoRoot, Gh: true}
}

// --- gh call script helpers (internal/run/activate_test.go,
// lifecycle_test.go idiom) ---

const prFields = "number,state,title,url,headRefName,baseRefName,headRefOid,mergeStateStatus,mergedAt,body"

func ghAuthCall() execxtest.Call { return execxtest.Call{Name: "gh", Args: []string{"auth", "status"}} }

func ghAuthFailsCall() execxtest.Call {
	return execxtest.Call{Name: "gh", Args: []string{"auth", "status"}, Exit: 1}
}

func ghRepoViewCall(defaultBranch string) execxtest.Call {
	return execxtest.Call{
		Name: "gh", Args: []string{"repo", "view", "--json", "nameWithOwner,defaultBranchRef,url"},
		Stdout: fmt.Sprintf(`{"nameWithOwner":"o/r","defaultBranchRef":{"name":%q},"url":"https://github.com/o/r"}`, defaultBranch),
	}
}

func ghPRForBranchCall(branch, stdout string) execxtest.Call {
	return execxtest.Call{Name: "gh", Args: []string{"pr", "list", "--head", branch, "--state", "open", "--json", prFields}, Stdout: stdout}
}

func ghLabelListEmptyCall() execxtest.Call {
	return execxtest.Call{Name: "gh", Args: []string{"label", "list", "--json", "name", "--limit", "1000"}, Stdout: "[]"}
}

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

func ghIssueCreateCall(number int) execxtest.Call {
	args := []string{"issue", "create", "--title", bootstrapTitle, "--body-file", "-"}
	for _, l := range []string{"in-progress", "infra", "implementer", "standard"} {
		args = append(args, "--label", l)
	}
	return execxtest.Call{Name: "gh", Args: args, Stdout: fmt.Sprintf("https://github.com/o/r/issues/%d\n", number)}
}

func ghCreatePRCall(number int) execxtest.Call {
	return execxtest.Call{
		Name:   "gh",
		Args:   []string{"pr", "create", "--head", BootstrapBranch, "--base", "main", "--title", bootstrapTitle, "--body-file", "-"},
		Stdout: fmt.Sprintf("https://github.com/o/r/pull/%d\n", number),
	}
}

func ghSetStatusCall(number int, to ghops.Status) execxtest.Call {
	all := []ghops.Status{ghops.StatusReady, ghops.StatusInProgress, ghops.StatusBlocked, ghops.StatusNeedsHuman, ghops.StatusAwaitingReview}
	args := []string{"issue", "edit", strconv.Itoa(number)}
	for _, s := range all {
		if s != to {
			args = append(args, "--remove-label", string(s))
		}
	}
	args = append(args, "--add-label", string(to))
	return execxtest.Call{Name: "gh", Args: args}
}

// preflightScript is the gh transcript every Stage-0-passing test
// needs before EnsureLabelTaxonomy: auth, repo view, and an empty
// open-PR list for the bootstrap branch.
func preflightScript() []execxtest.Call {
	return []execxtest.Call{ghAuthCall(), ghRepoViewCall("main"), ghPRForBranchCall(BootstrapBranch, "[]")}
}

// taxonomyScript appends the idempotent label-taxonomy calls
// (list-then-create-every-missing) after preflightScript.
func taxonomyScript() []execxtest.Call {
	calls := preflightScript()
	calls = append(calls, ghLabelListEmptyCall())
	return append(calls, ghLabelCreateCalls()...)
}

// deps builds a Deps against root using script for every gh call.
func testDeps(root string, answers map[string]string, script *execxtest.Script, mux muxRunner) Deps {
	mux.gh = script
	return Deps{RepoRoot: root, Answers: answers, Runner: mux, LookPath: fakeLookPath, Now: fixedNow}
}
