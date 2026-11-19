package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kninetimmy/orch/internal/execx"
	"github.com/kninetimmy/orch/internal/guard"
	"github.com/kninetimmy/orch/internal/lockfile"
	"github.com/kninetimmy/orch/internal/manifest"
	"github.com/kninetimmy/orch/internal/run"
	"github.com/kninetimmy/orch/internal/state"
)

func runStatusFixture(t *testing.T) (root, issue, review string, st *state.State) {
	t.Helper()
	root = t.TempDir()
	lookupTestGit(t, root, "init", "-b", "main")
	writeConfig(t, root, strings.Replace(validTOML, `mode = "off"`, `mode = "required"`, 1)+"\n[metrics]\nenabled = true\n")
	mcpWrite(t, filepath.Join(root, ".gitignore"), []byte(".orchestrator/state.json\n.orchestrator/delivery.lock\n.orchestrator/worktrees/\n.orchestrator/reviews/\n"))
	mcpWrite(t, filepath.Join(root, "tracked.txt"), []byte("fixture\n"))
	lookupTestGit(t, root, "add", ".")
	lookupTestGit(t, root, "-c", "user.name=Fixture", "-c", "user.email=fixture@example.invalid", "commit", "-m", "Seed status fixture")
	issue = filepath.Join(root, ".orchestrator", "worktrees", "issue-527")
	review = filepath.Join(root, ".orchestrator", "reviews", "issue-527")
	lookupTestGit(t, root, "worktree", "add", "-b", "orch/issue-527", issue, "HEAD")
	lookupTestGit(t, root, "worktree", "add", "--detach", review, "HEAD")
	var err error
	st, err = state.EnterDelivery(root, "opencode", testPlanRef(), []state.Issue{
		{PlanID: "status", Title: "Status", Phase: state.PhasePlanned},
		{PlanID: "later", Title: "Later", Phase: state.PhasePlanned},
	})
	if err != nil {
		t.Fatal(err)
	}
	st.Run.Issues[0] = state.Issue{
		PlanID: "status", Number: 527, Title: "Status", Phase: state.PhaseDispatched,
		Branch: "orch/issue-527", Worktree: ".orchestrator/worktrees/issue-527",
		Decision: &state.Decision{
			Role: manifest.RoleSpecialist, Executor: manifest.Selection{Model: "executor"},
			Reviewer: manifest.Selection{Model: "reviewer"}, Rationale: "fixture",
		},
	}
	if err := state.Save(root, st); err != nil {
		t.Fatal(err)
	}
	return
}

// Only these local read probes may be added to status; GitHub, Memhub and
// mutation commands are bugs even if they happen to leave no changed files.
type statusInspectionRunner struct {
	t                *testing.T
	worktreeEvidence func(string) string
}

func (r statusInspectionRunner) Run(ctx context.Context, cmd execx.Cmd) (execx.Result, error) {
	r.t.Helper()
	if cmd.Name != "git" {
		r.t.Fatalf("status launched %s %v", cmd.Name, cmd.Args)
	}
	switch strings.Join(cmd.Args, " ") {
	case "rev-parse --show-toplevel", "worktree list --porcelain", "rev-parse --path-format=absolute --git-common-dir":
	default:
		r.t.Fatalf("status launched unexpected Git command: %v", cmd.Args)
	}
	result, err := (execx.Local{}).Run(ctx, cmd)
	if err == nil && len(cmd.Args) > 0 && cmd.Args[0] == "worktree" && r.worktreeEvidence != nil {
		result.Stdout = r.worktreeEvidence(result.Stdout)
	}
	return result, err
}

func statusFromCheckout(t *testing.T, dir string) (run.StatusDoc, string) {
	t.Helper()
	var stdout, stderr bytes.Buffer
	env := Env{RepoRoot: dir, Stdout: &stdout, Stderr: &stderr, Stdin: blockingReader{t}, Runner: statusInspectionRunner{t: t}}
	if code := Run([]string{"run", "status", "--json"}, env); code != ExitOK || stderr.Len() != 0 {
		t.Fatalf("status in %s: exit %d, stderr %s", dir, code, &stderr)
	}
	var doc run.StatusDoc
	if err := json.Unmarshal(stdout.Bytes(), &doc); err != nil {
		t.Fatal(err)
	}
	if doc.SchemaVersion != 4 {
		t.Fatalf("status schema = %d, want unchanged schema 4", doc.SchemaVersion)
	}
	return doc, stdout.String()
}

func TestRunStatusLinkedCheckoutsObservePrimary(t *testing.T) {
	root, issue, review, st := runStatusFixture(t)
	nested := filepath.Join(issue, "nested")
	if err := os.Mkdir(nested, 0o755); err != nil {
		t.Fatal(err)
	}
	// Neither a copied config nor even an invalid primary config may turn
	// this state inspection into a configuration/Memhub preflight.
	mcpWrite(t, filepath.Join(root, ".orchestrator", "config.toml"), []byte("invalid config ["))
	mcpWrite(t, filepath.Join(issue, ".orchestrator", "config.toml"), []byte("invalid copied config ["))
	before := mcpTree(t, root)
	for _, consistent := range []bool{true, false} {
		if !consistent {
			if err := lockfile.Release(root); err != nil {
				t.Fatal(err)
			}
			before = mcpTree(t, root)
		}
		want, wantJSON := statusFromCheckout(t, root)
		if want.Mode != state.ModeDelivery || want.Run == nil || want.Run.ID != st.Run.ID ||
			want.Consistent != consistent || len(want.Run.Issues) != 2 ||
			want.Run.Issues[0].Phase != state.PhaseDispatched || want.Run.Issues[1].Phase != state.PhasePlanned ||
			(want.Warning != "") == consistent || (want.Lock != nil) != consistent {
			t.Fatalf("primary status = %+v", want)
		}
		for _, dir := range []string{issue, nested, review} {
			_, got := statusFromCheckout(t, dir)
			if got != wantJSON {
				t.Errorf("status in %s differs from primary:\n%s\nwant:\n%s", dir, got, wantJSON)
			}
		}
		if mcpTree(t, root) != before {
			t.Fatal("status changed repository files (including Git metadata)")
		}
	}
}

func TestRunStatusDoesNotGrantWritesOrRedirectOtherVerbs(t *testing.T) {
	root, issue, review, _ := runStatusFixture(t)
	for _, dir := range []string{issue, review} {
		statusFromCheckout(t, dir)
	}
	checker := guard.NewChecker(execx.Local{})
	for _, tc := range []struct {
		dir   string
		allow bool
	}{{issue, true}, {review, false}} {
		verdict, err := checker.Check(t.Context(), guard.Request{Paths: []string{filepath.Join(tc.dir, "tracked.txt")}})
		if err != nil || verdict.Allow != tc.allow {
			t.Fatalf("guard in %s: %+v, error %v", tc.dir, verdict, err)
		}
		t.Logf("guard in %s: allow=%t reason=%q", filepath.Base(filepath.Dir(tc.dir))+"/"+filepath.Base(tc.dir), verdict.Allow, verdict.Reason)
	}
	before := mcpTree(t, root)
	// Exercise the same dispatcher used by every JSON-taking mutator without
	// performing a lifecycle action: the probe must receive the original cwd.
	const verb = "status-root-probe"
	runVerbs[verb] = func(_ context.Context, env run.Env, _ []byte) (any, error) {
		if env.RepoRoot != issue {
			t.Fatalf("mutating verb redirected to %s", env.RepoRoot)
		}
		return struct{}{}, nil
	}
	defer delete(runVerbs, verb)
	var stdout, stderr bytes.Buffer
	env := Env{RepoRoot: issue, Stdout: &stdout, Stderr: &stderr, Stdin: strings.NewReader("{}")}
	if code := Run([]string{"run", verb}, env); code != ExitOK {
		t.Fatalf("mutator probe: exit %d, stderr %s", code, &stderr)
	}
	if mcpTree(t, root) != before {
		t.Fatal("mutator probe changed repository files")
	}
	stdout.Reset()
	if code := Run([]string{"run", "next", "--json"}, env); code != ExitOK || !strings.Contains(stdout.String(), `"mode": "assist"`) {
		t.Fatalf("next binding changed: exit %d, stdout %s, stderr %s", code, &stdout, &stderr)
	}
}

func TestRunStatusDoesNotInheritEnclosingClone(t *testing.T) {
	root, _, _, _ := runStatusFixture(t)
	other := filepath.Join(root, "different-clone")
	lookupTestGit(t, root, "clone", "--no-hardlinks", root, other)
	nested := filepath.Join(other, "nested")
	if err := os.Mkdir(nested, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, dir := range []string{other, nested} {
		doc, _ := statusFromCheckout(t, dir)
		if doc.Mode != state.ModeAssist || !doc.Consistent || doc.Run != nil || doc.Lock != nil {
			t.Fatalf("different clone inherited Delivery: %+v", doc)
		}
	}
}

func TestRunStatusUnverifiableCheckoutFailsExplicitly(t *testing.T) {
	for _, failure := range []string{"broken Git marker", "missing Git marker", "missing primary", "unregistered checkout", "unrelated primary"} {
		t.Run(failure, func(t *testing.T) {
			root, issue, _, _ := runStatusFixture(t)
			runner := statusInspectionRunner{t: t}
			switch failure {
			case "broken Git marker":
				mcpWrite(t, filepath.Join(issue, ".git"), []byte("gitdir: missing\n"))
			case "missing Git marker":
				if err := os.Remove(filepath.Join(issue, ".git")); err != nil {
					t.Fatal(err)
				}
			case "missing primary", "unrelated primary":
				// Inject stale/colliding registration evidence into a real Git
				// fixture; it must not make an unverified primary authoritative.
				primary := filepath.Join(root, "missing-primary")
				if failure == "unrelated primary" {
					primary = t.TempDir()
					lookupTestGit(t, primary, "init", "-b", "main")
				}
				runner.worktreeEvidence = func(out string) string {
					lines := strings.Split(out, "\n")
					lines[0] = "worktree " + filepath.ToSlash(primary)
					return strings.Join(lines, "\n")
				}
			case "unregistered checkout":
				runner.worktreeEvidence = func(out string) string { return strings.SplitN(out, "\n\n", 2)[0] + "\n\n" }
			}
			before := mcpTree(t, root)
			var stdout, stderr bytes.Buffer
			env := Env{RepoRoot: issue, Stdout: &stdout, Stderr: &stderr, Stdin: blockingReader{t}, Runner: runner}
			if code := Run([]string{"run", "status", "--json"}, env); code != ExitError || stdout.Len() != 0 || stderr.Len() == 0 {
				t.Fatalf("unverifiable status: exit %d, stdout %s, stderr %s", code, &stdout, &stderr)
			}
			if mcpTree(t, root) != before {
				t.Fatal("failed status changed repository files")
			}
			t.Log(strings.TrimSpace(stderr.String()))
		})
	}
}

func TestRunStatusPrimaryAbsentStateIsAssist(t *testing.T) {
	root, _, _, _ := runStatusFixture(t)
	if err := os.Remove(filepath.Join(root, filepath.FromSlash(state.Path))); err != nil {
		t.Fatal(err)
	}
	if err := lockfile.Release(root); err != nil {
		t.Fatal(err)
	}
	before := mcpTree(t, root)
	doc, _ := statusFromCheckout(t, root)
	if doc.Mode != state.ModeAssist || !doc.Consistent || doc.Run != nil || doc.Lock != nil || mcpTree(t, root) != before {
		t.Fatalf("absent primary state/lock = %+v", doc)
	}
}
