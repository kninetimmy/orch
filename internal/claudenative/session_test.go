package claudenative

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/kninetimmy/orch/internal/claudenative/claudefake"
	"github.com/kninetimmy/orch/internal/manifest"
	"github.com/kninetimmy/orch/internal/metrics"
	"github.com/kninetimmy/orch/internal/nativehost"
)

func TestMain(m *testing.M) {
	claudefake.Main() // exits when this binary is launched as the scripted Claude Code
	shutdownTimeout, initWait = time.Second, 300*time.Millisecond
	os.Exit(m.Run())
}

type fixture struct {
	task   Task
	config string
	exe    string
}

// newFixture builds an approved evaluation task over synthetic directories and
// points CLAUDE_CONFIG_DIR at a scratch config home naming the scenario.
func newFixture(t *testing.T, role, scenario string) fixture {
	t.Helper()
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	layout := nativehost.IsolationPaths{Workspace: filepath.Join(base, "packet"), Scratch: filepath.Join(base, "scratch"), MainCheckout: filepath.Join(base, "main"),
		ControllerState: filepath.Join(base, "controller"), SiblingWorkspaces: []string{filepath.Join(base, "sibling")}, CredentialPaths: []string{filepath.Join(base, "credentials")}}
	config := filepath.Join(base, "claude-config")
	for _, dir := range []string{layout.Workspace, layout.Scratch, layout.MainCheckout, layout.ControllerState, layout.SiblingWorkspaces[0], config} {
		if err := os.Mkdir(dir, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(config, "fixture-scenario"), []byte(scenario), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CLAUDE_CONFIG_DIR", config)
	t.Setenv("CLAUDECODE", "1")
	t.Setenv("ANTHROPIC_API_KEY", "fixture-not-a-key")
	prompt, instructions := "Declared synthetic public task.", "Declared synthetic public role."
	selection := manifest.Selection{Model: "claude-sonnet-5", Effort: "xhigh"}
	binding := &nativehost.EvaluationBinding{Identity: metrics.EvaluationIdentity{ID: "eval-aaaaaaaaaaaaaaaaaaaaaaaaaa", PlanDigest: "sha256:" + strings.Repeat("a", 64),
		Unit: 1, CaseID: "public-case", CaseVersion: 1, CaseSHA256: strings.Repeat("b", 64), PacketSHA256: strings.Repeat("c", 64), Repetition: 1,
		Side: "baseline", Attempt: 1, Kind: "initial", Role: role}, OrchRevision: strings.Repeat("d", 40), ProfileSHA256: strings.Repeat("e", 64),
		Selection: selection, Workspace: layout.Workspace, Scratch: layout.Scratch, PromptSHA256: textSHA256(prompt), InstructionsSHA256: textSHA256(instructions),
		InstructionSources: []nativehost.InstructionSource{}}
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	return fixture{task: Task{ID: binding.TaskID(), Role: role, Selection: selection, Prompt: prompt, Instructions: instructions, Layout: layout, Evaluation: binding}, config: config, exe: exe}
}

func (f fixture) run(t *testing.T, timeout time.Duration) (*Session, error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), timeout)
	defer cancel()
	return RunSession(ctx, Options{Executable: f.exe}, f.task)
}

type launch struct {
	Args []string `json:"args"`
	Cwd  string   `json:"cwd"`
	Env  []string `json:"env"`
}

func readLog[T any](t *testing.T, path string) []T {
	t.Helper()
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		t.Fatal(err)
	}
	var values []T
	scanner := bufio.NewScanner(strings.NewReader(string(data)))
	scanner.Buffer(make([]byte, 64<<10), 8<<20)
	for scanner.Scan() {
		var v T
		if err := json.Unmarshal(scanner.Bytes(), &v); err != nil {
			t.Fatal(err)
		}
		values = append(values, v)
	}
	return values
}

func (f fixture) sessions(t *testing.T) []launch {
	var sessions []launch
	for _, l := range readLog[launch](t, filepath.Join(f.config, "launches.jsonl")) {
		if l.Args[0] == "--print" {
			sessions = append(sessions, l)
		}
	}
	return sessions
}

func flagValues(args []string, flag string) []string {
	i := slices.Index(args, flag)
	if i < 0 {
		return nil
	}
	var values []string
	for _, arg := range args[i+1:] {
		if strings.HasPrefix(arg, "--") {
			break
		}
		values = append(values, arg)
	}
	return values
}

func TestLaunchArgsContainment(t *testing.T) {
	for _, role := range []string{"scout", "implementer", "reviewer"} {
		f := newFixture(t, role, "complete")
		for _, resume := range []bool{false, true} {
			args, err := launchArgs(f.task, "11111111-1111-4111-8111-111111111111", resume)
			if err != nil {
				t.Fatal(err)
			}
			for _, flag := range []string{"--print", "--verbose", "--restricted", "--safe-mode", "--strict-mcp-config", "--include-hook-events"} {
				if !slices.Contains(args, flag) {
					t.Fatalf("%s: missing %s", role, flag)
				}
			}
			for flag, want := range map[string]string{"--output-format": "stream-json", "--input-format": "stream-json", "--permission-mode": "dontAsk",
				"--permission-prompts": "none", "--model": "claude-sonnet-5", "--effort": "xhigh", "--add-dir": f.task.Layout.Scratch} {
				if got := flagValues(args, flag); len(got) != 1 || got[0] != want {
					t.Fatalf("%s %s = %v, want %s", role, flag, got, want)
				}
			}
			for _, arg := range args {
				if slices.Contains([]string{"--bare", "--dangerously-skip-permissions", "--allow-dangerously-skip-permissions", "--fallback-model", "bypassPermissions", "--no-session-persistence", "--fork-session"}, arg) ||
					strings.Contains(arg, f.task.Prompt) || strings.Contains(arg, f.task.Instructions) || arg == f.task.Layout.MainCheckout {
					t.Fatalf("%s: forbidden or free-text argument %q", role, arg)
				}
			}
			wantTools, wantAllowed := "Read,Glob,Grep,Bash", []string{"Read", "Glob", "Grep"}
			if role == "implementer" {
				wantTools, wantAllowed = wantTools+",Edit,Write", append(wantAllowed, "Edit", "Write")
			}
			wantAllowed = append(wantAllowed, "Bash(go build)", "Bash(go build ./...)", "Bash(go test)", "Bash(go test ./...)", "Bash(go vet)", "Bash(go vet ./...)", "Bash(gofmt -l .)")
			if got := flagValues(args, "--tools"); len(got) != 1 || got[0] != wantTools {
				t.Fatalf("%s tools %v", role, got)
			}
			got := flagValues(args, "--allowedTools")
			if !slices.Equal(got, wantAllowed) {
				t.Fatalf("%s allowed tools %v", role, got)
			}
			for _, rule := range got {
				if strings.HasPrefix(rule, "Bash") && strings.Contains(rule, "*") {
					t.Fatalf("%s wildcard Bash rule %q", role, rule)
				}
			}
			if resume != (flagValues(args, "--resume") != nil) || resume == (flagValues(args, "--session-id") != nil) {
				t.Fatalf("%s resume=%t args %v", role, resume, args)
			}
		}
	}
	f := newFixture(t, "scout", "complete")
	f.task.Layout.Scratch += "%PATH%"
	if _, err := launchArgs(f.task, "11111111-1111-4111-8111-111111111111", false); !errors.Is(err, ErrUnavailable) {
		t.Fatal("unsafe argument accepted")
	}
}

func TestEnvironmentDropsParentSessionAndKeys(t *testing.T) {
	env := environment([]string{"CLAUDECODE=1", "ANTHROPIC_API_KEY=x", "ANTHROPIC_BASE_URL=x", "CLAUDE_CODE_USE_BEDROCK=1", "GOFLAGS=-toolexec=x", "PATH=/bin", "HOME=/h", "CLAUDE_CONFIG_DIR=/c", "TEMP=/t",
		"GOWORK=/w/go.work", "GOTOOLCHAIN=auto", "GOPROXY=https://proxy.golang.org", "GOSUMDB=sum.golang.org", "CGO_ENABLED=1", "gotoolchain=go1.99.0"}, "/scratch", false)
	want := []string{"PATH=/bin", "HOME=/h", "CLAUDE_CONFIG_DIR=/c", "TEMP=/scratch", "TMP=/scratch", "TMPDIR=/scratch",
		"GOWORK=off", "GOTOOLCHAIN=local", "GOPROXY=off", "GOSUMDB=off", "CGO_ENABLED=0"}
	if !slices.Equal(env, want) {
		t.Fatalf("environment %v", env)
	}
	if env := environment(nil, "/scratch", true); !slices.Contains(env, "CLAUDE_CODE_RESUME_INTERRUPTED_TURN=1") {
		t.Fatal("resume environment lacks interrupted-turn continuation")
	}
}

func TestScriptedSessionCompletesAndRecordsEvidence(t *testing.T) {
	uuid := regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)
	for _, scenario := range []string{"complete", "init-after-prompt"} {
		for _, role := range []string{"scout", "implementer", "reviewer"} {
			t.Run(scenario+"/"+role, func(t *testing.T) {
				f := newFixture(t, role, scenario)
				s, err := f.run(t, 20*time.Second)
				if err != nil {
					t.Fatal(err)
				}
				r := s.Result()
				if r.Outcome != SessionSuccessful || !r.Started || r.Output != "Scripted synthetic evaluation output." || !uuid.MatchString(r.SessionID) ||
					r.Observed.Model != "claude-sonnet-5" || r.Observed.Effort != "" || r.HostVersion != "2.1.289" || r.Instructions.Status != "none-declared" {
					t.Fatalf("result %+v", r)
				}
				if c := r.Cleanup; c.InterruptionAsked || c.ShutdownObserved == nil || !*c.ShutdownObserved {
					t.Fatalf("cleanup %+v", c)
				}
				sessions := f.sessions(t)
				if len(sessions) != 1 || sessions[0].Cwd != f.task.Layout.Workspace || flagValues(sessions[0].Args, "--session-id")[0] != r.SessionID {
					t.Fatalf("launches %+v", sessions)
				}
				for _, name := range sessions[0].Env {
					if slices.Contains([]string{"CLAUDECODE", "ANTHROPIC_API_KEY"}, strings.ToUpper(name)) {
						t.Fatalf("child inherited %s", name)
					}
				}
				inputs := readLog[map[string]any](t, filepath.Join(f.config, "input.jsonl"))
				if len(inputs) != 1 || inputs[0]["type"] != "user" {
					t.Fatalf("stream input %+v", inputs)
				}
				content := inputs[0]["message"].(map[string]any)["content"].([]any)
				if len(content) != 2 || content[0].(map[string]any)["text"] != f.task.Instructions || content[1].(map[string]any)["text"] != f.task.Prompt {
					t.Fatalf("user message %+v", content)
				}
				checkObservations(t, r, "applied-effort-not-reported", "sample", "native-completion-not-verification")
				sample := r.Observations[1].Sample
				for i, v := range []*int64{sample.Counters.InputTokens, sample.Counters.OutputTokens, sample.Counters.CacheReadTokens, sample.Counters.CacheCreationTokens, sample.Counters.ReasoningOutputTokens} {
					if v == nil || *v != []int64{100, 40, 900, 80, 12}[i] {
						t.Fatalf("counter %d = %v", i, v)
					}
				}
				if sample.Counters.TotalTokens != nil {
					t.Fatal("unreported total synthesized")
				}
				_, err = os.Stat(filepath.Join(f.task.Layout.Workspace, "fixture-output.txt"))
				if (role == "implementer") != (err == nil) {
					t.Fatalf("%s write capability: %v", role, err)
				}
			})
		}
	}
}

// checkObservations requires each observation, in order, to be the named
// unavailable reason, a counter "sample" or an "outcome", all naming the
// session, host claude and the requested profile.
func checkObservations(t *testing.T, r SessionResult, kinds ...string) {
	t.Helper()
	if len(r.Observations) != len(kinds) {
		t.Fatalf("observations %+v", r.Observations)
	}
	for i, o := range r.Observations {
		ok := o.Host == "claude" && o.Session == r.SessionID && o.Requested != nil && *o.Requested == (metrics.Profile{Model: "claude-sonnet-5", Effort: "xhigh"}) && o.Validate() == nil
		switch kinds[i] {
		case "sample":
			ok = ok && o.Sample != nil
		case "outcome":
			ok = ok && o.Outcome != ""
		default:
			ok = ok && o.Unavailable != nil && o.Unavailable.Reason == kinds[i]
		}
		if !ok {
			t.Fatalf("observation %d: %+v", i, o)
		}
	}
	if _, err := metrics.CounterContributions(r.Observations); err != nil {
		t.Fatal(err)
	}
}

func TestStartupStateRefusesWithoutOutput(t *testing.T) {
	for scenario, reason := range map[string]string{"extra-tool": "tool set", "mcp": "MCP servers", "plugin": "not built in", "apikey": "subscription login", "permission-mode": "permission mode"} {
		t.Run(scenario, func(t *testing.T) {
			f := newFixture(t, "implementer", scenario)
			s, err := f.run(t, 20*time.Second)
			if !errors.Is(err, ErrUnavailable) || s == nil || !strings.Contains(err.Error(), reason) {
				t.Fatalf("admitted: %v", err)
			}
			r := s.Result()
			if r.Outcome != SessionRefused || r.Started || r.Output != "" || r.Cleanup.ShutdownObserved == nil || !*r.Cleanup.ShutdownObserved {
				t.Fatalf("result %+v", r)
			}
			checkObservations(t, r, "native-session-refused")
			if inputs := readLog[map[string]any](t, filepath.Join(f.config, "input.jsonl")); len(inputs) != 0 {
				t.Fatal("task was sent to a session whose startup state differs")
			}
		})
	}
}

func TestMissingCapabilityRefusesBeforeLaunch(t *testing.T) {
	f := newFixture(t, "scout", "missing-flag")
	s, err := f.run(t, 20*time.Second)
	if !errors.Is(err, ErrUnavailable) || !strings.Contains(err.Error(), "--safe-mode") || s.Result().Outcome != SessionRefused || s.Result().SessionID != "" {
		t.Fatalf("missing flag admitted: %v", err)
	}
	if launches := readLog[launch](t, filepath.Join(f.config, "launches.jsonl")); len(launches) != 1 || launches[0].Args[0] != "--help" {
		t.Fatalf("launches %+v", launches)
	}
}

func TestWorkspaceInstructionFilesRefuseBeforeLaunch(t *testing.T) {
	for _, name := range []string{"CLAUDE.md", "agents.md", ".claude", "CLAUDE.local.md"} {
		t.Run(name, func(t *testing.T) {
			f := newFixture(t, "scout", "complete")
			path := filepath.Join(f.task.Layout.Workspace, name)
			var err error
			if name == ".claude" {
				err = os.Mkdir(path, 0o700)
			} else {
				err = os.WriteFile(path, []byte("Undeclared instructions."), 0o600)
			}
			if err != nil {
				t.Fatal(err)
			}
			s, err := f.run(t, 20*time.Second)
			if !errors.Is(err, ErrUnavailable) || s.Result().Outcome != SessionRefused {
				t.Fatalf("admitted: %v", err)
			}
			if launches := readLog[launch](t, filepath.Join(f.config, "launches.jsonl")); len(launches) != 0 {
				t.Fatalf("Claude Code launched: %+v", launches)
			}
		})
	}
}

func TestModelSubstitutionIsSafetyFailure(t *testing.T) {
	for _, scenario := range []string{"model-init", "model-assistant", "model-usage"} {
		t.Run(scenario, func(t *testing.T) {
			f := newFixture(t, "scout", scenario)
			s, err := f.run(t, 20*time.Second)
			r := s.Result()
			if !errors.Is(err, ErrProfileMismatch) || r.Outcome != SessionFailed || r.Observed.Model != "claude-substitute-model" || r.Output != "" {
				t.Fatalf("substitution accepted: %+v %v", r, err)
			}
			if last := r.Observations[len(r.Observations)-1]; last.Observed == nil || last.Observed.Model != "claude-substitute-model" {
				t.Fatalf("observed model not recorded: %+v", last)
			}
		})
	}
}

func TestSessionBoundaryViolations(t *testing.T) {
	for _, scenario := range []string{"delegate", "unapproved-tool", "other-session"} {
		t.Run(scenario, func(t *testing.T) {
			s, err := newFixture(t, "scout", scenario).run(t, 20*time.Second)
			if !errors.Is(err, ErrTaskBoundary) || s.Result().Outcome != SessionFailed || s.Result().Output != "" {
				t.Fatalf("boundary violation accepted: %v", err)
			}
		})
	}
}

func TestUnreportedCountersStayUnknown(t *testing.T) {
	s, err := newFixture(t, "scout", "partial-usage").run(t, 20*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	c := s.Result().Observations[1].Sample.Counters
	if c.InputTokens == nil || c.OutputTokens == nil || c.CacheReadTokens != nil || c.CacheCreationTokens != nil || c.ReasoningOutputTokens != nil || c.TotalTokens != nil {
		t.Fatalf("absent counters became measured: %+v", c)
	}
}

func TestInterruptOnTimeoutAndCancel(t *testing.T) {
	for _, cancelled := range []bool{false, true} {
		t.Run(fmt.Sprintf("cancel=%t", cancelled), func(t *testing.T) {
			f := newFixture(t, "implementer", "slow")
			ctx, cancel := context.WithTimeout(t.Context(), 1500*time.Millisecond)
			defer cancel()
			if cancelled {
				time.AfterFunc(700*time.Millisecond, cancel)
			}
			s, err := RunSession(ctx, Options{Executable: f.exe}, f.task)
			r := s.Result()
			want, cause := SessionTimedOut, context.DeadlineExceeded
			if cancelled {
				want, cause = SessionCancelled, context.Canceled
			}
			c := r.Cleanup
			if !errors.Is(err, cause) || r.Outcome != want || r.NativeStatus != "interrupted" || r.Output != "" || !c.InterruptionAsked ||
				c.InterruptAcknowledged == nil || !*c.InterruptAcknowledged || c.ShutdownObserved == nil || !*c.ShutdownObserved {
				t.Fatalf("interrupt evidence %+v %v", r, err)
			}
			checkObservations(t, r, "applied-effort-not-reported", "native-counters-unreliable-after-error", "native-session-"+string(want))
		})
	}
}

func TestUnresponsiveSessionTreeIsKilled(t *testing.T) {
	f := newFixture(t, "scout", "hang")
	s, err := f.run(t, 1500*time.Millisecond)
	r := s.Result()
	if !errors.Is(err, context.DeadlineExceeded) || r.Outcome != SessionTimedOut || !r.Cleanup.InterruptionAsked ||
		r.Cleanup.InterruptAcknowledged == nil || *r.Cleanup.InterruptAcknowledged || r.Cleanup.ShutdownObserved == nil || *r.Cleanup.ShutdownObserved {
		t.Fatalf("unresponsive cleanup evidence %+v %v", r, err)
	}
	pids := readLog[map[string]int](t, filepath.Join(f.config, "pids.jsonl"))
	if len(pids) != 1 {
		t.Fatalf("pids %+v", pids)
	}
	for _, pid := range []int{pids[0]["session"], pids[0]["descendant"]} {
		deadline := time.Now().Add(5 * time.Second)
		for processAlive(pid) {
			if time.Now().After(deadline) {
				t.Fatalf("process %d from the attempt is still running", pid)
			}
			time.Sleep(50 * time.Millisecond)
		}
	}
}

func TestResumeRequiresSameTaskAndProtectedPaths(t *testing.T) {
	f := newFixture(t, "scout", "disconnect")
	s, err := f.run(t, 20*time.Second)
	r := s.Result()
	if !errors.Is(err, ErrProcessExit) || r.Outcome != SessionDisconnected || r.Output != "" || r.Cleanup.InterruptionAsked || r.Cleanup.ShutdownObserved == nil || !*r.Cleanup.ShutdownObserved {
		t.Fatalf("disconnect %+v %v", r, err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
	defer cancel()
	changed := f.task
	changed.Layout.SiblingWorkspaces = append([]string{}, filepath.Join(filepath.Dir(f.task.Layout.Workspace), "controller"))
	if err := s.Resume(ctx, changed); !errors.Is(err, ErrTaskBoundary) {
		t.Fatalf("resume with other protected paths: %v", err)
	}
	other := newFixture(t, "scout", "complete").task
	t.Setenv("CLAUDE_CONFIG_DIR", f.config)
	if err := s.Resume(ctx, other); !errors.Is(err, ErrTaskBoundary) {
		t.Fatalf("resume with another task: %v", err)
	}
	if err := os.WriteFile(filepath.Join(f.config, "fixture-scenario"), []byte("complete"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := s.Resume(ctx, f.task); err != nil {
		t.Fatal(err)
	}
	resumed := s.Result()
	sessions := f.sessions(t)
	if resumed.Outcome != SessionSuccessful || resumed.SessionID != r.SessionID || len(sessions) != 2 ||
		flagValues(sessions[1].Args, "--resume")[0] != r.SessionID || slices.Contains(sessions[1].Args, "--session-id") {
		t.Fatalf("resume %+v %+v", resumed, sessions)
	}
	if inputs := readLog[map[string]any](t, filepath.Join(f.config, "input.jsonl")); len(inputs) != 1 {
		t.Fatalf("resume sent new input: %+v", inputs)
	}
	checkObservations(t, resumed, "applied-effort-not-reported", "native-session-disconnected", "applied-effort-not-reported", "sample", "native-completion-not-verification")
	if err := s.Resume(ctx, f.task); err == nil {
		t.Fatal("completed session resumed")
	}
}

func TestBindingRefusals(t *testing.T) {
	for name, change := range map[string]func(*Task){
		"no-evaluation": func(task *Task) { task.Evaluation = nil },
		"declared-source": func(task *Task) {
			task.Evaluation.InstructionSources = []nativehost.InstructionSource{{Path: "x", SHA256: "y"}}
		},
		"effort":  func(task *Task) { task.Selection.Effort, task.Evaluation.Selection.Effort = "extreme", "extreme" },
		"variant": func(task *Task) { task.Selection.Variant, task.Evaluation.Selection.Variant = "v", "v" },
		"prompt":  func(task *Task) { task.Prompt += " expanded" },
		"role":    func(task *Task) { task.Role = "implementer" },
		"overlap": func(task *Task) { task.Layout.SiblingWorkspaces = []string{task.Layout.Workspace} },
	} {
		t.Run(name, func(t *testing.T) {
			f := newFixture(t, "scout", "complete")
			change(&f.task)
			if s, err := f.run(t, 20*time.Second); s != nil || err == nil {
				t.Fatal("binding admitted")
			}
			if launches := readLog[launch](t, filepath.Join(f.config, "launches.jsonl")); len(launches) != 0 {
				t.Fatal("Claude Code launched for a refused binding")
			}
		})
	}
	ctx := context.Background()
	if s, err := RunSession(ctx, Options{}, newFixture(t, "scout", "complete").task); s != nil || err == nil {
		t.Fatal("session admitted without a deadline")
	}
}
