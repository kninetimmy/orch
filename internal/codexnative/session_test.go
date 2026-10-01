package codexnative

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/BurntSushi/toml"
	"github.com/kninetimmy/orch/internal/agents"
	"github.com/kninetimmy/orch/internal/execx"
	"github.com/kninetimmy/orch/internal/manifest"
	"github.com/kninetimmy/orch/internal/metrics"
)

// This fixture is the only isolation-bypass seam: it is compiled solely into
// ordinary tests, starts the test binary, and never exposes a caller callback or
// flag in production. Native prerequisite/auth/catalog RPCs still run end to end.
func scriptedSessionConnection(t *testing.T, ctx context.Context, s *Session, scenario string) (*connection, func()) {
	t.Helper()
	b, err := prepareIsolation(s.task.Layout, s.task.Role == "scout" || metricRole(s.task.Role) == "reviewer")
	if err != nil {
		t.Fatal(err)
	}
	processCtx, cleanup := sessionContext(ctx)
	payload, err := os.Executable()
	if err != nil {
		cleanup()
		t.Fatal(err)
	}
	env := append(serverEnvironment(os.Environ(), b.scratch), "ORCH_CODEX_SESSION_TEST_SERVER="+scenario, "ORCH_CODEX_SESSION_TEST_ROLE="+s.task.Role)
	c, err := startWithEnv(processCtx, execx.Cmd{Name: payload, Args: b.args(), Dir: b.workspace}, env)
	if err != nil {
		cleanup()
		t.Fatal(err)
	}
	c.isolation, c.profile = true, b.profile()
	capabilities, err := inspect(c, "scripted-session", s.task.Selection)
	if err != nil || capabilities.Selection != s.task.Selection {
		_ = c.close()
		cleanup()
		t.Fatalf("scripted capability preflight: %+v, %v", capabilities, err)
	}
	var readiness struct{ Status string }
	if err := c.call("windowsSandbox/readiness", struct{}{}, &readiness); err != nil || readiness.Status != "ready" {
		_ = c.close()
		cleanup()
		t.Fatalf("scripted readiness: %v", err)
	}
	var profiles struct {
		Data []struct {
			ID      string
			Allowed bool
		}
	}
	if err := c.call("permissionProfile/list", map[string]string{"cwd": b.workspace}, &profiles); err != nil || len(profiles.Data) != 1 || profiles.Data[0].ID != b.profile() || !profiles.Data[0].Allowed {
		_ = c.close()
		cleanup()
		t.Fatalf("scripted isolation prerequisite: %v", err)
	}
	c.session = true // synthetic closed host, not modelToolBoundary evidence
	return c, cleanup
}

func sessionTask(t *testing.T) (Options, Task) {
	t.Helper()
	payload, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	layout := isolationLayout(t)
	return Options{Executable: payload, Dir: layout.Workspace, ClientVersion: "scripted-session"}, Task{
		ID: "approved-task-294", RunID: "run-20260930T120000Z-12345678", IssueNumber: 294, Role: "specialist", Attempt: "implementation-1",
		Selection: manifest.Selection{Model: "gpt-6.1-sol", Effort: "max"}, Prompt: "Execute exactly the approved synthetic task.", Layout: layout}
}

func scriptedSessionServer(scenario string) {
	cwd, err := os.Getwd()
	if err != nil {
		os.Exit(60)
	}
	var config map[string]any
	if len(os.Args) > 4 {
		if _, err := toml.Decode(strings.Join(configOverrides(os.Args[1:]), "\n"), &config); err != nil {
			os.Exit(61)
		}
	}
	profile, _ := config["default_permissions"].(string)
	write := func(value any) {
		if json.NewEncoder(os.Stdout).Encode(value) != nil {
			os.Exit(62)
		}
	}
	response := func(id string, value any) { write(map[string]any{"id": id, "result": value}) }
	notify := func(method string, params any) { write(map[string]any{"method": method, "params": params}) }
	turn := func(status string) map[string]any {
		return map[string]any{"id": "turn-294", "status": status, "items": []any{}}
	}
	usage := func(input int64) {
		total := map[string]any{"inputTokens": input, "totalTokens": input + 50}
		if input == 100 {
			total["outputTokens"], total["cachedInputTokens"], total["reasoningOutputTokens"] = 0, 0, 5
		}
		notify("thread/tokenUsage/updated", map[string]any{"threadId": "thread-294", "turnId": "turn-294", "tokenUsage": map[string]any{"total": total, "last": map[string]any{}}})
	}
	completed := func(status string) {
		value := turn(status)
		if status == "completed" {
			item := map[string]any{"id": "answer-294", "type": "agentMessage", "text": "Synthetic task result.", "phase": "final_answer"}
			value["items"] = []any{item}
			notify("item/completed", map[string]any{"threadId": "thread-294", "turnId": "turn-294", "item": item})
		}
		notify("turn/completed", map[string]any{"threadId": "thread-294", "turn": value})
	}
	scanner := bufio.NewScanner(os.Stdin)
	scanner.Buffer(make([]byte, 4096), maxMessageBytes+1)
	for scanner.Scan() {
		var request struct {
			ID     string          `json:"id"`
			Method string          `json:"method"`
			Params json.RawMessage `json:"params"`
		}
		if json.Unmarshal(scanner.Bytes(), &request) != nil {
			os.Exit(63)
		}
		callsFile := filepath.Join(cwd, ".scripted-native-calls")
		calls, err := os.ReadFile(callsFile)
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			os.Exit(64)
		}
		if os.WriteFile(callsFile, append(calls, []byte(request.Method+"\n")...), 0o600) != nil {
			os.Exit(65)
		}
		switch request.Method {
		case "initialize":
			response(request.ID, map[string]any{"userAgent": "orch/0.159.2 (scripted)", "platformFamily": "windows", "platformOs": "windows"})
			notify("account/updated", map[string]string{"authMode": "chatgpt"})
		case "initialized":
		case "account/read":
			if string(request.Params) != `{"refreshToken":false}` {
				os.Exit(66)
			}
			response(request.ID, map[string]any{"account": map[string]string{"type": "chatgpt"}, "requiresOpenaiAuth": true})
		case "model/list":
			response(request.ID, map[string]any{"data": []any{map[string]any{"id": "gpt-6.1-sol", "model": "gpt-6.1-sol", "supportedReasoningEfforts": []any{map[string]string{"reasoningEffort": "max"}}}}, "nextCursor": nil})
		case "windowsSandbox/readiness":
			response(request.ID, map[string]string{"status": "ready"})
		case "permissionProfile/list":
			response(request.ID, map[string]any{"data": []any{map[string]any{"id": profile, "allowed": true}}, "nextCursor": nil})
		case "thread/start", "thread/resume":
			var params struct {
				Cwd          string            `json:"cwd"`
				Model        string            `json:"model"`
				Config       map[string]string `json:"config"`
				Approval     string            `json:"approvalPolicy"`
				Permissions  string            `json:"permissions"`
				Instructions string            `json:"developerInstructions"`
				Thread       string            `json:"threadId"`
			}
			instructions, err := agents.CodexInstructions(os.Getenv("ORCH_CODEX_SESSION_TEST_ROLE"))
			if json.Unmarshal(request.Params, &params) != nil || err != nil || !strings.HasPrefix(params.Instructions, instructions) || params.Cwd != cwd || params.Model != "gpt-6.1-sol" || params.Config["model_reasoning_effort"] != "max" || params.Approval != "never" || params.Permissions != profile {
				os.Exit(67)
			}
			if (request.Method == "thread/resume") != (params.Thread == "thread-294") {
				os.Exit(68)
			}
			thread := map[string]any{"id": "thread-294", "sessionId": "tree-session-294", "cwd": cwd, "turns": []any{}}
			result := map[string]any{"thread": thread, "cwd": cwd, "model": "gpt-6.1-sol", "reasoningEffort": "max", "approvalPolicy": "never", "activePermissionProfile": map[string]any{"id": profile, "extends": nil}}
			if scenario == "unknown-profile" {
				delete(result, "model")
				delete(result, "reasoningEffort")
			}
			if scenario == "thread-model" {
				result["model"] = "other-model"
			}
			if scenario == "thread-effort" {
				result["reasoningEffort"] = "high"
			}
			if scenario == "thread-permissions" {
				result["approvalPolicy"] = "on-request"
			}
			if scenario == "thread-workspace" {
				result["cwd"] = filepath.Dir(cwd)
			}
			if request.Method == "thread/resume" {
				status := "completed"
				if scenario == "resume-active" {
					status = "inProgress"
				}
				value := turn(status)
				if status == "completed" {
					value["items"] = []any{map[string]any{"id": "answer-294", "type": "agentMessage", "text": "Synthetic task result.", "phase": "final_answer"}}
				}
				thread["turns"] = []any{value}
				switch scenario {
				case "resume-identity":
					thread["sessionId"] = "other-tree-session"
				case "resume-extra-turn":
					thread["turns"] = []any{value, turn("completed")}
				default:
					usage(100) // exact event replay before the resume response
					usage(120)
				}
				response(request.ID, result)
				if status == "inProgress" {
					completed("completed")
				}
			} else {
				notify("thread/started", map[string]any{"thread": thread})
				response(request.ID, result)
			}
		case "turn/start":
			var params struct {
				Thread      string                        `json:"threadId"`
				Model       string                        `json:"model"`
				Effort      string                        `json:"effort"`
				Permissions string                        `json:"permissions"`
				Input       []struct{ Type, Text string } `json:"input"`
			}
			if json.Unmarshal(request.Params, &params) != nil || params.Thread != "thread-294" || params.Model != "gpt-6.1-sol" || params.Effort != "max" || params.Permissions != profile || len(params.Input) != 1 || params.Input[0].Type != "text" || params.Input[0].Text != "Execute exactly the approved synthetic task." {
				os.Exit(69)
			}
			if scenario == "unknown-turn" {
				return
			}
			notify("turn/started", map[string]any{"threadId": "thread-294", "turn": turn("inProgress")})
			response(request.ID, map[string]any{"turn": turn("inProgress")})
			switch scenario {
			case "wait", "interrupt-stall":
			case "disconnect":
				usage(100)
				usage(100)
				if os.WriteFile(filepath.Join(cwd, ".dirty-task"), []byte("preserve dirty work"), 0o600) != nil {
					os.Exit(70)
				}
				return
			case "native-failed":
				completed("failed")
			case "settings-model", "settings-effort", "settings-permissions":
				settings := map[string]any{"model": "gpt-6.1-sol", "effort": "max", "cwd": cwd, "approvalPolicy": "never", "activePermissionProfile": map[string]string{"id": profile}}
				switch scenario {
				case "settings-model":
					settings["model"] = "other-model"
				case "settings-effort":
					settings["effort"] = "high"
				default:
					settings["approvalPolicy"] = "on-request"
				}
				notify("thread/settings/updated", map[string]any{"threadId": "thread-294", "threadSettings": settings})
			case "reroute":
				notify("model/rerouted", map[string]string{"threadId": "thread-294", "turnId": "turn-294", "fromModel": "gpt-6.1-sol", "toModel": "gpt-6.1-sol", "reason": "rateLimit"})
			case "delegation":
				notify("item/started", map[string]any{"threadId": "thread-294", "turnId": "turn-294", "item": map[string]string{"id": "spawn-1", "type": "collabAgentToolCall"}})
			case "tool-outside", "file-escape", "task-input", "unknown-tool", "file-change":
				item := map[string]any{"id": "boundary-item", "type": "commandExecution", "cwd": filepath.Dir(cwd)}
				switch scenario {
				case "file-escape", "file-change":
					path := "synthetic.txt"
					if scenario == "file-escape" {
						path = "../main/unapproved.txt"
					}
					item = map[string]any{"id": "boundary-item", "type": "fileChange", "changes": []any{map[string]string{"path": path}}}
				case "task-input":
					item = map[string]any{"id": "boundary-item", "type": "userMessage", "content": []any{map[string]string{"type": "text", "text": "Expand the approved task."}}}
				case "unknown-tool":
					item["type"] = "unapprovedFutureTool"
				}
				notify("item/started", map[string]any{"threadId": "thread-294", "turnId": "turn-294", "item": item})
				if scenario == "file-change" {
					completed("completed")
				}
			case "permission-request", "task-expansion":
				method := "item/permissions/requestApproval"
				if scenario == "task-expansion" {
					method = "item/tool/requestUserInput"
				}
				write(map[string]any{"id": "server-unapproved", "method": method, "params": map[string]any{}})
			case "unknown-profile":
				notify("thread/tokenUsage/updated", map[string]any{"threadId": "thread-294", "turnId": "turn-294", "tokenUsage": map[string]any{"total": map[string]any{}}})
				completed("completed")
			case "regressing-counters":
				usage(100)
				usage(90)
			default:
				notify("item/started", map[string]any{"threadId": "thread-294", "turnId": "turn-294", "item": map[string]string{"id": "command-294", "type": "commandExecution", "cwd": cwd}})
				usage(100)
				usage(100)
				usage(120)
				completed("completed")
			}
		case "turn/interrupt":
			var params struct{ ThreadID, TurnID string }
			if json.Unmarshal(request.Params, &params) != nil || params.ThreadID != "thread-294" || params.TurnID != "turn-294" {
				os.Exit(71)
			}
			if scenario != "interrupt-stall" {
				response(request.ID, map[string]any{})
				completed("interrupted")
			}
		default:
			os.Exit(72) // no login, fork, steering, tool approval or process fallback
		}
	}
}

func TestSessionScriptedLifecycle(t *testing.T) {
	for _, test := range []struct {
		scenario string
		outcome  SessionOutcome
		wantErr  error
	}{
		{"complete", SessionSuccessful, nil},
		{"unknown-profile", SessionSuccessful, nil},
		{"native-failed", SessionFailed, nil},
		{"thread-model", SessionFailed, ErrProfileMismatch},
		{"thread-effort", SessionFailed, ErrProfileMismatch},
		{"thread-permissions", SessionFailed, ErrTaskBoundary},
		{"thread-workspace", SessionFailed, ErrTaskBoundary},
		{"settings-model", SessionFailed, ErrProfileMismatch},
		{"settings-effort", SessionFailed, ErrProfileMismatch},
		{"settings-permissions", SessionFailed, ErrTaskBoundary},
		{"reroute", SessionFailed, ErrProfileMismatch},
		{"delegation", SessionFailed, ErrTaskBoundary},
		{"tool-outside", SessionFailed, ErrTaskBoundary},
		{"file-escape", SessionFailed, ErrTaskBoundary},
		{"task-input", SessionFailed, ErrTaskBoundary},
		{"unknown-tool", SessionFailed, ErrTaskBoundary},
		{"file-change", SessionSuccessful, nil},
		{"permission-request", SessionFailed, ErrMalformedMessage},
		{"task-expansion", SessionFailed, ErrMalformedMessage},
		{"regressing-counters", SessionFailed, nil},
	} {
		t.Run(test.scenario, func(t *testing.T) {
			options, task := sessionTask(t)
			s, _, err := newSession(options, task)
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			c, cleanup := scriptedSessionConnection(t, ctx, s, test.scenario)
			defer cleanup()
			err = s.execute(ctx, c, false)
			result := s.Result()
			if result.Outcome != test.outcome || (test.wantErr != nil && !errors.Is(err, test.wantErr)) || (test.outcome == SessionSuccessful && err != nil) || (test.outcome == SessionFailed && err == nil) {
				t.Fatalf("outcome=%s error=%v; want %s, %v", result.Outcome, err, test.outcome, test.wantErr)
			}
			calls, err := os.ReadFile(filepath.Join(task.Layout.Workspace, ".scripted-native-calls"))
			if err != nil {
				t.Fatal(err)
			}
			if strings.HasPrefix(test.scenario, "thread-") && strings.Contains(string(calls), "turn/start") {
				t.Fatal("thread mismatch progressed to model execution")
			}
			if result.Requested != task.Selection || result.ThreadID != "thread-294" || result.NativeSessionID != "tree-session-294" {
				t.Fatalf("lost native/requested identities: %+v", result)
			}
			if test.scenario == "unknown-profile" {
				if result.Observed.Model != "" || result.Observed.Effort != "" || result.Observations[0].Sample != nil || result.Observations[0].Unavailable == nil {
					t.Fatal("unknown profile or counters inferred from request/defaults")
				}
			}
			if test.scenario == "complete" {
				if result.Output != "Synthetic task result." || len(result.Observations) != 3 || result.Observations[0].Sample.Counters.CacheCreationTokens != nil || result.Observations[1].Sample.Counters.OutputTokens != nil {
					t.Fatalf("duplicate output, counters or missingness changed: %+v", result)
				}
				deltas, err := metrics.CounterContributions(result.Observations)
				if err != nil || *deltas[0].TotalTokens != 150 || *deltas[1].TotalTokens != 20 || *deltas[0].OutputTokens != 0 {
					t.Fatalf("native contribution: %+v %v", deltas, err)
				}
			}
			for _, observation := range result.Observations {
				if observation.Interval != nil || observation.Outcome == "approval" || observation.Role == "architect" || observation.Source != "codex-app-server" || observation.Session != result.ThreadID {
					t.Fatal("fabricated timing, approval, root coverage or source")
				}
				if err := observation.Validate(); err != nil {
					t.Fatal(err)
				}
			}
		})
	}
}

func TestSessionNativeInterruptionAndBounds(t *testing.T) {
	for _, scenario := range []string{"deadline", "cancel", "interrupt-stall"} {
		t.Run(scenario, func(t *testing.T) {
			options, task := sessionTask(t)
			s, _, err := newSession(options, task)
			if err != nil {
				t.Fatal(err)
			}
			duration := time.Second
			if scenario == "cancel" {
				duration = 5 * time.Second
			}
			ctx, cancel := context.WithTimeout(context.Background(), duration)
			defer cancel()
			serverScenario := "wait"
			if scenario == "interrupt-stall" {
				serverScenario = scenario
			}
			c, cleanup := scriptedSessionConnection(t, ctx, s, serverScenario)
			defer cleanup()
			if scenario == "cancel" {
				go func() {
					timer := time.NewTicker(10 * time.Millisecond)
					defer timer.Stop()
					for {
						select {
						case <-ctx.Done():
							return
						case <-timer.C:
							calls, _ := os.ReadFile(filepath.Join(task.Layout.Workspace, ".scripted-native-calls"))
							if strings.Contains(string(calls), "turn/start") {
								cancel()
								return
							}
						}
					}
				}()
			}
			start := time.Now()
			err = s.execute(ctx, c, false)
			want := SessionTimedOut
			if scenario == "cancel" {
				want = SessionCancelled
			}
			if err == nil || s.result.Outcome != want || time.Since(start) > 5*time.Second {
				t.Fatalf("unbounded or misclassified interrupt: %s %v %s", s.result.Outcome, err, time.Since(start))
			}
			calls, err := os.ReadFile(filepath.Join(task.Layout.Workspace, ".scripted-native-calls"))
			if err != nil || !strings.Contains(string(calls), "turn/interrupt") {
				t.Fatalf("native interrupt not sent: %s %v", calls, err)
			}
			if scenario != "interrupt-stall" && s.result.NativeStatus != "interrupted" {
				t.Fatal("native interruption completion not observed")
			}
		})
	}
}

func TestSessionDisconnectResumeReplay(t *testing.T) {
	for _, scenario := range []string{"resume-completed", "resume-active", "resume-identity", "resume-extra-turn"} {
		t.Run(scenario, func(t *testing.T) {
			options, task := sessionTask(t)
			s, _, err := newSession(options, task)
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			c, cleanup := scriptedSessionConnection(t, ctx, s, "disconnect")
			err = s.execute(ctx, c, false)
			cleanup()
			if !errors.Is(err, ErrProcessExit) || s.result.Outcome != SessionDisconnected || len(s.result.Observations) != 2 {
				t.Fatalf("incomplete stream lost: %+v %v", s.result, err)
			}
			before := s.Result()
			modified := s.Result()
			*modified.Observations[0].Sample.Counters.InputTokens = 999
			if *s.Result().Observations[0].Sample.Counters.InputTokens != 100 {
				t.Fatal("caller snapshot changed immutable replay history")
			}
			for _, change := range []func(*Task){
				func(v *Task) { v.ID = "different-task" },
				func(v *Task) { v.Prompt += " Expand the task." },
				func(v *Task) { v.Selection.Effort = "high" },
				func(v *Task) { v.Role = "implementer" },
				func(v *Task) { v.Layout.Workspace = v.Layout.Scratch },
			} {
				changed := task
				change(&changed)
				if err := s.Resume(ctx, changed); !errors.Is(err, ErrTaskBoundary) {
					t.Fatalf("changed task accepted for resume: %v", err)
				}
			}
			if err := s.checkResume(ctx, task); err != nil {
				t.Fatal(err)
			}
			t.Setenv("ORCH_CODEX_SESSION_TEST_SERVER", "production")
			if err := s.Resume(ctx, task); !errors.Is(err, ErrIsolationUnavailable) || !reflect.DeepEqual(before, s.Result()) {
				t.Fatalf("resume bypassed production isolation or changed checkpoint: %v", err)
			}
			// Like initial execution, only this private synthetic seam can pass
			// the deliberately unavailable production model-tool boundary.
			c, cleanup = scriptedSessionConnection(t, ctx, s, scenario)
			err = s.execute(ctx, c, true)
			cleanup()
			if scenario == "resume-identity" || scenario == "resume-extra-turn" {
				if !errors.Is(err, ErrTaskBoundary) || s.result.Outcome != SessionFailed {
					t.Fatalf("unverified resume accepted: %v", err)
				}
				return
			}
			if err != nil || s.result.Outcome != SessionSuccessful || !reflect.DeepEqual(before.Observations, s.Result().Observations[:2]) {
				t.Fatalf("resume/replay changed retained evidence: %v", err)
			}
			calls, err := os.ReadFile(filepath.Join(task.Layout.Workspace, ".scripted-native-calls"))
			if err != nil || strings.Count(string(calls), "turn/start\n") != 1 || strings.Count(string(calls), "thread/resume\n") != 1 {
				t.Fatalf("completed/active turn replayed: %s %v", calls, err)
			}
			dirty, err := os.ReadFile(filepath.Join(task.Layout.Workspace, ".dirty-task"))
			if err != nil || string(dirty) != "preserve dirty work" {
				t.Fatal("disconnect/resume changed dirty work")
			}
			root := t.TempDir()
			for _, observation := range s.Result().Observations {
				accepted, err := metrics.Record(root, observation)
				if err != nil || !accepted {
					t.Fatalf("native observation recording: %v", err)
				}
				accepted, err = metrics.Record(root, observation)
				if err != nil || accepted {
					t.Fatalf("native observation replay doubled usage: %v", err)
				}
			}
			docs, err := metrics.LoadAll(root)
			if err != nil || len(docs) != 1 || len(docs[0].Events) != 0 {
				t.Fatalf("legacy usage double-submitted: %+v %v", docs, err)
			}
			deltas, err := metrics.CounterContributions(docs[0].Observations)
			if err != nil || *deltas[0].InputTokens != 100 || *deltas[2].InputTokens != 20 {
				t.Fatalf("resumed native totals counted twice: %+v %v", deltas, err)
			}
			if err := s.Resume(ctx, task); err == nil {
				t.Fatal("completed turn could be replayed")
			}
		})
	}
}

func TestSessionProductionGateAndBinding(t *testing.T) {
	options, task := sessionTask(t)
	if _, err := RunSession(context.Background(), options, task); err == nil {
		t.Fatal("unbounded session accepted")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	t.Setenv("ORCH_CODEX_SESSION_TEST_SERVER", "production")
	s, err := RunSession(ctx, options, task)
	if !errors.Is(err, ErrIsolationUnavailable) || s == nil || s.result.ThreadID != "" || s.result.Outcome != SessionFailed {
		t.Fatalf("production isolation bypass: %+v %v", s, err)
	}
	calls, err := os.ReadFile(filepath.Join(task.Layout.Workspace, ".scripted-native-calls"))
	if err != nil || strings.Contains(string(calls), "thread/start") || strings.Contains(string(calls), "turn/start") {
		t.Fatalf("failed preflight progressed: %s %v", calls, err)
	}
	for _, method := range []string{"thread/start", "thread/resume", "turn/start", "turn/interrupt", "thread/fork", "turn/steer", "process/spawn"} {
		c := &connection{}
		if _, err := c.request(method, struct{}{}); err == nil {
			t.Fatalf("metadata connection enabled %s", method)
		}
	}
	options.Dir = task.Layout.Scratch
	if _, _, err := newSession(options, task); !errors.Is(err, ErrTaskBoundary) {
		t.Fatal("cwd binding was not required")
	}
	options.Dir = task.Layout.Workspace
	task.Role = "architect"
	if _, _, err := newSession(options, task); err == nil {
		t.Fatal("unavailable Architect role silently fabricated")
	}
}

func TestSessionUnknownTurnCannotResume(t *testing.T) {
	options, task := sessionTask(t)
	s, _, err := newSession(options, task)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	c, cleanup := scriptedSessionConnection(t, ctx, s, "unknown-turn")
	defer cleanup()
	err = s.execute(ctx, c, false)
	if !errors.Is(err, ErrProcessExit) || s.result.TurnID != "" || s.checkResume(ctx, task) == nil {
		t.Fatalf("uncertain native turn can be replayed: %+v %v", s.result, err)
	}
}

func TestSessionReadOnlyRolesAndRetentionBounds(t *testing.T) {
	for _, role := range []string{"scout", "reviewer", "review_downgrade"} {
		t.Run(role, func(t *testing.T) {
			options, task := sessionTask(t)
			task.Role = role
			s, _, err := newSession(options, task)
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			c, cleanup := scriptedSessionConnection(t, ctx, s, "file-change")
			defer cleanup()
			if err := s.execute(ctx, c, false); !errors.Is(err, ErrTaskBoundary) || s.result.Outcome != SessionFailed {
				t.Fatalf("read-only role accepted a file change: %v", err)
			}
			if s.result.Observations[0].Role != metricRole(role) {
				t.Fatal("safe reviewer lost its observation attribution")
			}
		})
	}
	options, task := sessionTask(t)
	s, _, err := newSession(options, task)
	if err != nil {
		t.Fatal(err)
	}
	s.events = maxSessionEvents
	if err := s.notification(message{Method: json.RawMessage(`"other"`), Params: json.RawMessage(`{}`)}); err == nil {
		t.Fatal("unbounded native event retention")
	}
	s.result.Output = strings.Repeat("x", maxMessageBytes)
	if err := s.item(json.RawMessage(`{"id":"too-large","type":"agentMessage","text":"x"}`), true); err == nil {
		t.Fatal("unbounded native output retention")
	}
}
