package cli

import (
	"bytes"
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/kninetimmy/orch/internal/execx"
	"github.com/kninetimmy/orch/internal/lockfile"
	"github.com/kninetimmy/orch/internal/manifest"
	"github.com/kninetimmy/orch/internal/metrics"
	"github.com/kninetimmy/orch/internal/state"
)

// TestNoArgCommandsRejectTrailingArgs proves noArgs still rejects a
// trailing argument for every PRD §22 command except the ones that parse
// their own argv: the adapter-plumbing `run` verb, `resume` (which
// takes flags), `init` (which takes --step/--bootstrap; see
// init_test.go for its own trailing-argument coverage),
// `configure-local` (which takes --step/--apply; see
// configurelocal_test.go for its own trailing-argument coverage), and
// `configure` (which takes --step/--deliver; see configure_test.go for
// its own trailing-argument coverage).
func TestNoArgCommandsRejectTrailingArgs(t *testing.T) {
	for _, name := range []string{"status", "doctor", "abort", "metrics"} {
		t.Run(name, func(t *testing.T) {
			env, _, stderr := testEnv(t)
			if code := Run([]string{name, "extra"}, env); code != ExitUsage {
				t.Errorf("exit = %d, want %d", code, ExitUsage)
			}
			if !strings.Contains(stderr.String(), "unexpected argument") {
				t.Errorf("stderr = %q", stderr.String())
			}
		})
	}
}

func TestRunVerbUsageErrors(t *testing.T) {
	cases := [][]string{
		{"run"},
		{"run", "bogus"},
		{"run", "status"},          // missing --json
		{"run", "plan", "extra"},   // too many args
		{"run", "status", "--xml"}, // wrong flag
	}
	for _, args := range cases {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			env, _, stderr := testEnv(t)
			if code := Run(args, env); code != ExitUsage {
				t.Errorf("exit = %d, want %d", code, ExitUsage)
			}
			if !strings.Contains(stderr.String(), "orch run: usage") {
				t.Errorf("stderr = %q", stderr.String())
			}
		})
	}
}

func TestRunPlanEndToEnd(t *testing.T) {
	env, stdout, stderr := testEnv(t)
	writeConfig(t, env.RepoRoot, validTOML)
	env.Stdin = bytes.NewReader([]byte(minimalPlanJSON))

	if code := Run([]string{"run", "plan"}, env); code != ExitOK {
		t.Fatalf("exit = %d, want %d; stderr = %s", code, ExitOK, stderr.String())
	}
	out := stdout.String()
	for _, want := range []string{`"schema_version"`, `"plan_digest"`, `"plan_title"`, `"fix-status-lock"`} {
		if !strings.Contains(out, want) {
			t.Errorf("stdout missing %q:\n%s", want, out)
		}
	}
}

func TestRunPlanMalformedStdinIsExitError(t *testing.T) {
	env, _, stderr := testEnv(t)
	writeConfig(t, env.RepoRoot, validTOML)
	env.Stdin = bytes.NewReader([]byte("{not valid json"))

	if code := Run([]string{"run", "plan"}, env); code != ExitError {
		t.Fatalf("exit = %d, want %d", code, ExitError)
	}
	if stderr.String() == "" {
		t.Error("stderr is empty, want a decode error")
	}
}

func TestRunStatusJSONEndToEnd(t *testing.T) {
	env, stdout, stderr := testEnv(t)
	writeConfig(t, env.RepoRoot, validTOML)

	if code := Run([]string{"run", "status", "--json"}, env); code != ExitOK {
		t.Fatalf("exit = %d, want %d; stderr = %s", code, ExitOK, stderr.String())
	}
	if !strings.Contains(stdout.String(), `"mode": "assist"`) {
		t.Errorf("stdout = %s", stdout.String())
	}
}

// blockingReader stands in for a console stdin that never reaches EOF:
// any Read is a bug that would hang the process.
type blockingReader struct{ t *testing.T }

func (r blockingReader) Read([]byte) (int, error) {
	r.t.Fatal("status read stdin; it must not (a console stdin never reaches EOF)")
	return 0, nil
}

// TestRunStatusNeverReadsStdin pins the fix for the interactive hang:
// `orch run status --json` invoked without piped stdin must not block
// waiting for EOF on the console.
func TestRunStatusNeverReadsStdin(t *testing.T) {
	env, stdout, stderr := testEnv(t)
	writeConfig(t, env.RepoRoot, validTOML)
	env.Stdin = blockingReader{t: t}

	if code := Run([]string{"run", "status", "--json"}, env); code != ExitOK {
		t.Fatalf("exit = %d, want %d; stderr = %s", code, ExitOK, stderr.String())
	}
	if !strings.Contains(stdout.String(), `"mode": "assist"`) {
		t.Errorf("stdout = %s", stdout.String())
	}
}

func TestRunPlanAndStatusDoNotWaitForMutationSerialization(t *testing.T) {
	env, _, _ := testEnv(t)
	writeConfig(t, env.RepoRoot, validTOML)
	lock, err := lockfile.AcquireMutation(env.RepoRoot)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := lock.Release(); err != nil {
			t.Errorf("Release: %v", err)
		}
	}()

	for name, args := range map[string][]string{
		"plan":   {"run", "plan"},
		"status": {"run", "status", "--json"},
	} {
		t.Run(name, func(t *testing.T) {
			callEnv := env
			var stdout, callStderr bytes.Buffer
			callEnv.Stdout = &stdout
			callEnv.Stderr = &callStderr
			if name == "plan" {
				callEnv.Stdin = strings.NewReader(minimalPlanJSON)
			}
			done := make(chan int, 1)
			go func() { done <- Run(args, callEnv) }()
			select {
			case code := <-done:
				if code != ExitOK {
					t.Fatalf("exit = %d, want %d; stderr = %s", code, ExitOK, callStderr.String())
				}
			case <-time.After(2 * time.Second):
				t.Fatalf("%s waited for mutation serialization", name)
			}
		})
	}
}

type firstCallGateRunner struct {
	inner   execx.Runner
	entered chan struct{}
	release <-chan struct{}
	once    sync.Once
}

func (r *firstCallGateRunner) Run(ctx context.Context, cmd execx.Cmd) (execx.Result, error) {
	r.once.Do(func() {
		close(r.entered)
		<-r.release
	})
	return r.inner.Run(ctx, cmd)
}

type dispatchRunner struct{ fakeRunner }

func (r dispatchRunner) Run(ctx context.Context, cmd execx.Cmd) (execx.Result, error) {
	if cmd.Name == "gh" && len(cmd.Args) > 1 && cmd.Args[0] == "issue" && cmd.Args[1] == "edit" {
		return execx.Result{}, nil
	}
	return r.fakeRunner.Run(ctx, cmd)
}

func TestConcurrentDispatchesPreserveBothStateAndMetrics(t *testing.T) {
	env, stdout1, stderr1 := testEnv(t)
	writeConfig(t, env.RepoRoot, validTOML+"\n[metrics]\nenabled = true\n")
	planned := []state.Issue{
		{PlanID: "a", Phase: state.PhasePlanned},
		{PlanID: "b", Phase: state.PhasePlanned},
	}
	st, err := state.EnterDelivery(env.RepoRoot, "claude", testPlanRef(), planned)
	if err != nil {
		t.Fatal(err)
	}
	selection := manifest.Selection{Model: "claude-sonnet-5", Effort: "xhigh"}
	for i, number := range []int{240, 241} {
		st.Run.Issues[i] = state.Issue{
			PlanID:             string(rune('a' + i)),
			Title:              fmt.Sprintf("Issue %d", number),
			Phase:              state.PhaseWorktreeReady,
			Number:             number,
			Branch:             fmt.Sprintf("orch/issue-%d", number),
			Worktree:           fmt.Sprintf(".orchestrator/worktrees/issue-%d", number),
			Objective:          "preserve this transition",
			AcceptanceCriteria: []string{"state remains recorded"},
			RequiredTests:      []string{"go test ./..."},
			Decision: &state.Decision{
				Role:      manifest.RoleImplementer,
				Executor:  selection,
				Reviewer:  selection,
				Rationale: "test",
			},
		}
	}
	if err := state.Save(env.RepoRoot, st); err != nil {
		t.Fatal(err)
	}

	entered := make(chan struct{})
	release := make(chan struct{})
	env.Runner = &firstCallGateRunner{
		inner:   dispatchRunner{fakeRunner{toplevel: env.RepoRoot}},
		entered: entered,
		release: release,
	}
	env.Stdin = strings.NewReader(`{"schema_version":4,"issue_number":240}`)
	done1 := make(chan int, 1)
	go func() { done1 <- Run([]string{"run", "dispatch"}, env) }()
	<-entered // issue #240 loaded state and is now paused before mutation

	var stdout2, stderr2 bytes.Buffer
	env2 := env
	env2.Stdout = &stdout2
	env2.Stderr = &stderr2
	env2.Runner = dispatchRunner{fakeRunner{toplevel: env.RepoRoot}}
	env2.Stdin = strings.NewReader(`{"schema_version":4,"issue_number":241}`)
	done2 := make(chan int, 1)
	go func() { done2 <- Run([]string{"run", "dispatch"}, env2) }()

	var code2 int
	secondFinishedEarly := false
	select {
	case code2 = <-done2:
		// Without command serialization #241 saves its independently loaded
		// state first; releasing #240 below then reproduces the lost update.
		secondFinishedEarly = true
	case <-time.After(200 * time.Millisecond):
		// Serialized: #241 is waiting before its state load.
	}
	close(release)
	code1 := <-done1
	if !secondFinishedEarly {
		code2 = <-done2
	}
	if code1 != ExitOK || code2 != ExitOK {
		t.Fatalf("dispatch exits = (%d, %d), want (%d, %d); stderr #240 %q; stderr #241 %q; stdout #240 %q; stdout #241 %q",
			code1, code2, ExitOK, ExitOK, stderr1.String(), stderr2.String(), stdout1.String(), stdout2.String())
	}

	got, err := state.Load(env.RepoRoot)
	if err != nil {
		t.Fatal(err)
	}
	for _, issue := range got.Run.Issues {
		if issue.Phase != state.PhaseDispatched {
			t.Errorf("issue #%d phase = %s, want %s", issue.Number, issue.Phase, state.PhaseDispatched)
		}
	}
	docs, err := metrics.LoadAll(env.RepoRoot)
	if err != nil {
		t.Fatal(err)
	}
	if len(docs) != 1 || len(docs[0].Events) != 2 {
		t.Fatalf("metrics documents = %+v, want one document with two events", docs)
	}
	seen := map[int]bool{}
	for _, event := range docs[0].Events {
		seen[event.IssueNumber] = true
	}
	for _, number := range []int{240, 241} {
		if !seen[number] {
			t.Errorf("metrics missing dispatch for issue #%d: %+v", number, docs[0].Events)
		}
	}
}

// TestRunLifecycleVerbsAreRouted proves each new verb is recognized by
// the dispatcher (not a usage error) and reaches the run engine, which
// fails closed because the repo is in Assist. A recognized verb with a
// valid document yields ExitError, never ExitUsage.
func TestRunLifecycleVerbsAreRouted(t *testing.T) {
	verbs := map[string]string{
		"dispatch":        `{"schema_version":4,"issue_number":1}`,
		"pr-open":         `{"schema_version":1,"issue_number":1,"verifications":[{"name":"t","result":"pass"}]}`,
		"review-worktree": `{"schema_version":1,"issue_number":1,"head_oid":"0123456789abcdef0123456789abcdef01234567"}`,
		"review":          `{"schema_version":3,"issue_number":1,"reviewed_head_oid":"h","verdict":"approve","summary":"s","reviewer":{"model":"m","effort":"e"},"judgments":[{"criterion":1,"judgment":"satisfied","reason":"r"}]}`,
		"escalate":        `{"schema_version":2,"issue_number":1,"trigger":"architectural-ambiguity","detail":"x"}`,
		"ci":              `{"schema_version":1,"issue_number":1}`,
		"merge-report":    `{"schema_version":1,"issue_number":1}`,
		"merge":           `{"schema_version":1,"issue_number":1,"approval":{"pr_number":1,"head_oid":"h","approved_by":"a","approved_at":"2026-07-11T12:00:00Z","statement":"approve-merge"}}`,
		"block":           `{"schema_version":1,"issue_number":1,"class":"other","detail":"x"}`,
		"abandon":         `{"schema_version":1,"issue_number":1,"reason":"x","statement":"abandon-issue"}`,
		"cleanup":         `{"schema_version":1,"issue_number":1,"statement":"cleanup-issue"}`,
		"complete":        `{"schema_version":1}`,
	}
	for verb, doc := range verbs {
		t.Run(verb, func(t *testing.T) {
			env, _, stderr := testEnv(t)
			writeConfig(t, env.RepoRoot, validTOML)
			env.Stdin = bytes.NewReader([]byte(doc))
			if code := Run([]string{"run", verb}, env); code != ExitError {
				t.Fatalf("exit = %d, want %d (recognized verb, no delivery run); stderr = %s", code, ExitError, stderr.String())
			}
			if !strings.Contains(stderr.String(), "no delivery run is active") {
				t.Errorf("stderr = %q, want the no-delivery-run remediation", stderr.String())
			}
		})
	}
}

const minimalPlanJSON = `{
  "schema_version": 1,
  "host": "claude",
  "title": "Fix status lock",
  "issues": [
    {
      "id": "fix-status-lock",
      "title": "Fix the status lock race",
      "objective": "Make status reporting race-free",
      "acceptance_criteria": ["no data race under -race"],
      "type": "bug",
      "facts": {"read_only": false},
      "wave": 1,
      "required_tests": ["go test ./..."],
      "usage_class": "light"
    }
  ]
}`
