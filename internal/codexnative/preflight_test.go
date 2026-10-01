package codexnative

import (
	"bufio"
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

	"github.com/kninetimmy/orch/internal/manifest"
)

// The test binary is the scripted app-server. No installed Codex, credentials,
// network or model is used, including on the three CI operating systems.
func TestMain(m *testing.M) {
	if scenario := os.Getenv("ORCH_CODEX_SESSION_TEST_SERVER"); scenario != "" {
		scriptedSessionServer(scenario)
		os.Exit(0)
	}
	if len(os.Args) > 1 && os.Args[1] == "orch-isolation-fixture" {
		os.Exit(isolationFixture(os.Args[2:]))
	}
	if len(os.Args) > 4 && reflect.DeepEqual(os.Args[1:4], []string{"app-server", "--listen", "stdio://"}) {
		scriptedIsolationServer()
		os.Exit(0)
	}
	if scenario := os.Getenv("ORCH_CODEX_NATIVE_TEST_SERVER"); scenario != "" {
		scriptedServer(scenario)
		os.Exit(0)
	}
	os.Exit(m.Run())
}

func scriptedServer(scenario string) {
	if !reflect.DeepEqual(os.Args[1:], []string{"app-server", "--listen", "stdio://"}) {
		os.Exit(20)
	}
	cwd, err := os.Getwd()
	actualDir, actualErr := filepath.EvalSymlinks(cwd)
	expectedDir, expectedErr := filepath.EvalSymlinks(os.Getenv("ORCH_CODEX_NATIVE_TEST_CWD"))
	if err != nil || actualErr != nil || expectedErr != nil || !strings.EqualFold(actualDir, expectedDir) {
		os.Exit(21)
	}
	if scenario == "blocked-write" {
		time.Sleep(time.Minute)
		return
	}
	write := func(value any) {
		if err := json.NewEncoder(os.Stdout).Encode(value); err != nil {
			os.Exit(22)
		}
	}
	response := func(id string, result any) { write(map[string]any{"id": id, "result": result}) }
	stage, page := 0, 0
	scanner := bufio.NewScanner(os.Stdin)
	scanner.Buffer(make([]byte, 4096), maxMessageBytes+1)
	for scanner.Scan() {
		var request struct {
			ID     string          `json:"id"`
			Method string          `json:"method"`
			Params json.RawMessage `json:"params"`
		}
		if err := json.Unmarshal(scanner.Bytes(), &request); err != nil {
			os.Exit(23)
		}
		if stage < 3 {
			if request.Method != []string{"initialize", "initialized", "account/read"}[stage] {
				os.Exit(24)
			}
			stage++
		} else if request.Method != "model/list" {
			os.Exit(25) // catches any login, thread/turn, execution or approval call
		}
		switch request.Method {
		case "initialize":
			if scenario == "process-exit" {
				os.Exit(7)
			}
			if scenario == "transport-interrupted" {
				_ = os.Stdout.Close()
				time.Sleep(time.Minute)
				return
			}
			if scenario == "response-timeout" || scenario == "caller-cancel" {
				time.Sleep(time.Minute)
				return
			}
			if scenario == "malformed-json" {
				_, _ = fmt.Fprintln(os.Stdout, `{"private":"DO-NOT-LEAK",`)
				continue
			}
			if scenario == "oversized-message" {
				_, _ = fmt.Fprintln(os.Stdout, strings.Repeat("x", maxMessageBytes+1))
				continue
			}
			if scenario == "unterminated-message" {
				_, _ = fmt.Fprint(os.Stdout, `{"id":"`+request.ID+`","result":{}}`)
				return
			}
			if scenario == "server-request" {
				write(map[string]any{"id": "server-1", "method": "account/chatgptAuthTokens/refresh", "params": map[string]any{}})
				continue
			}
			if scenario == "conflicting-response" {
				write(map[string]any{"id": request.ID, "result": map[string]any{}, "error": nil})
				continue
			}
			if scenario == "mismatched-id" {
				response("another-request", map[string]any{})
				continue
			}
			var params struct {
				ClientInfo struct {
					Name    string `json:"name"`
					Version string `json:"version"`
				} `json:"clientInfo"`
				Capabilities json.RawMessage `json:"capabilities"`
			}
			if err := json.Unmarshal(request.Params, &params); err != nil || params.ClientInfo.Name != "orch" || params.ClientInfo.Version == "" || len(params.Capabilities) != 0 {
				os.Exit(26)
			}
			agent := "Codex Desktop/0.159.2 (windows; x86_64)"
			if scenario == "cli-version" {
				agent = "codex_cli_rs/0.159.2 (linux; x86_64)"
			}
			if scenario == "orch-version" {
				agent = "orch/0.159.2 (Windows 10.0.26200; x86_64) unknown (orch; isolation-smoke)"
			}
			if scenario == "missing-version" {
				agent = "unknown"
			}
			response(request.ID, map[string]any{"userAgent": agent, "platformFamily": "windows", "platformOs": "windows"})
			write(map[string]any{"method": "remoteControl/status/changed", "params": map[string]any{"enabled": false}})
			if scenario != "missing-auth-mode" {
				mode := "chatgpt"
				if scenario == "external-tokens" {
					mode = "chatgptAuthTokens"
				}
				write(map[string]any{"method": "account/updated", "params": map[string]any{"authMode": mode}})
			}
		case "initialized":
			if request.ID != "" {
				os.Exit(27)
			}
		case "account/read":
			if string(request.Params) != `{"refreshToken":false}` {
				os.Exit(28)
			}
			var account any = map[string]any{"type": "chatgpt", "email": "private@example.invalid", "planType": "pro"}
			if scenario == "missing-account" {
				account = nil
			}
			if scenario == "api-key" {
				account = map[string]any{"type": "apiKey"}
			}
			response(request.ID, map[string]any{"account": account, "requiresOpenaiAuth": scenario != "alternate-provider"})
		case "model/list":
			var params struct {
				Cursor        *string `json:"cursor"`
				Limit         int     `json:"limit"`
				IncludeHidden bool    `json:"includeHidden"`
			}
			if err := json.Unmarshal(request.Params, &params); err != nil || params.Limit != 100 || !params.IncludeHidden {
				os.Exit(29)
			}
			if (page == 0 && params.Cursor != nil) || (page > 0 && (params.Cursor == nil || *params.Cursor != fmt.Sprintf("page-%d:$()`;", page))) {
				os.Exit(30)
			}
			model := map[string]any{"id": "gpt-6.1-sol", "model": "gpt-6.1-sol", "hidden": true, "defaultReasoningEffort": "high", "supportedReasoningEfforts": []any{map[string]any{"reasoningEffort": "max"}}}
			data := []any{}
			var next any
			if page < 2 {
				next = fmt.Sprintf("page-%d:$()`;", page+1)
			}
			if page == 0 {
				data = append(data, map[string]any{"id": "other-model", "model": "other-model", "supportedReasoningEfforts": []any{}})
			}
			if page == 1 && scenario != "missing-model" {
				data = append(data, model)
			}
			if scenario == "unsupported-effort" {
				model["defaultReasoningEffort"] = "max" // a default is not support
				model["supportedReasoningEfforts"] = []any{map[string]any{"reasoningEffort": "high"}}
			}
			if scenario == "substituted-model" {
				model["model"] = "another-version"
			}
			if scenario == "model-alias" {
				model["id"] = "another-alias"
			}
			if scenario == "missing-efforts" {
				delete(model, "supportedReasoningEfforts")
			}
			if scenario == "malformed-effort" {
				model["supportedReasoningEfforts"] = []any{nil, map[string]any{"reasoningEffort": "max"}}
			}
			if scenario == "duplicate-model" && page == 2 {
				data = append(data, model)
			}
			if scenario == "repeated-cursor" && page == 1 {
				next = "page-1:$()`;"
			}
			if scenario == "late-rejection" && page == 2 {
				write(map[string]any{"id": request.ID, "error": map[string]any{"code": -32601, "message": "DO-NOT-LEAK"}})
				continue
			}
			if scenario == "auth-change" && page == 2 {
				write(map[string]any{"method": "account/updated", "params": map[string]any{"authMode": "apikey"}})
			}
			write(map[string]any{"method": "account/rateLimits/updated", "params": map[string]any{}})
			response(request.ID, map[string]any{"data": data, "nextCursor": next})
			page++
		}
	}
	if scenario == "shutdown-timeout" {
		time.Sleep(time.Minute)
	}
	if err := scanner.Err(); err != nil {
		os.Exit(31)
	}
}

func scriptedPreflight(t *testing.T, scenario string, timeout time.Duration, clientVersion string) (Capabilities, error) {
	t.Helper()
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	// Metacharacters in cwd and cursors must remain data, on every host.
	dir := filepath.Join(t.TempDir(), "cwd $(); data")
	if err := os.Mkdir(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("ORCH_CODEX_NATIVE_TEST_SERVER", scenario)
	t.Setenv("ORCH_CODEX_NATIVE_TEST_CWD", dir)
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	if scenario == "caller-cancel" {
		timer := time.AfterFunc(250*time.Millisecond, cancel)
		defer timer.Stop()
	}
	return Preflight(ctx, Options{Executable: executable, Dir: dir, ClientVersion: clientVersion}, manifest.Selection{Model: "gpt-6.1-sol", Effort: "max"})
}

func TestPreflightHandshakeInterleavingAndAllPages(t *testing.T) {
	for _, scenario := range []string{"success", "cli-version", "orch-version"} {
		t.Run(scenario, func(t *testing.T) {
			capabilities, err := scriptedPreflight(t, scenario, 5*time.Second, "test-build")
			if err != nil {
				t.Fatal(err)
			}
			if capabilities.HostVersion != "0.159.2" || capabilities.PlatformOS != "windows" || capabilities.Selection != (manifest.Selection{Model: "gpt-6.1-sol", Effort: "max"}) {
				t.Fatalf("unexpected capability evidence: %+v", capabilities)
			}
		})
	}
}

func TestPreflightFailsClosed(t *testing.T) {
	for _, test := range []struct {
		scenario string
		want     string
	}{
		{"missing-version", "native host version unavailable"},
		{"missing-account", "signed-in ChatGPT"},
		{"api-key", "signed-in ChatGPT"},
		{"external-tokens", "managed ChatGPT authentication"},
		{"auth-change", "managed ChatGPT authentication"},
		{"alternate-provider", "OpenAI-authenticated provider"},
		{"missing-model", "is unavailable"},
		{"unsupported-effort", "is unsupported"},
		{"substituted-model", "substitutes the requested model"},
		{"model-alias", "substitutes the requested model"},
		{"missing-efforts", "incomplete model capability"},
		{"malformed-effort", "incomplete reasoning effort"},
		{"duplicate-model", "duplicate requested model"},
		{"repeated-cursor", "repeated model/list cursor"},
		{"late-rejection", "model/list rejected (code -32601)"},
	} {
		t.Run(test.scenario, func(t *testing.T) {
			capabilities, err := scriptedPreflight(t, test.scenario, 5*time.Second, "test-build")
			if err == nil || !strings.Contains(err.Error(), test.want) || strings.Contains(err.Error(), "DO-NOT-LEAK") || capabilities.Selection.Model != "" {
				t.Fatalf("preflight evidence=%+v error=%v; want %q without verified selection", capabilities, err, test.want)
			}
			if test.scenario != "missing-version" && capabilities.HostVersion != "0.159.2" {
				t.Fatal("native host version was not retained on failure")
			}
		})
	}
}

func TestPreflightMalformedAndInterruptedTransport(t *testing.T) {
	for _, test := range []struct {
		scenario string
		want     error
	}{
		{"malformed-json", ErrMalformedMessage},
		{"oversized-message", ErrMalformedMessage},
		{"unterminated-message", ErrMalformedMessage},
		{"server-request", ErrMalformedMessage},
		{"conflicting-response", ErrMalformedMessage},
		{"mismatched-id", ErrMalformedMessage},
		{"process-exit", ErrProcessExit},
		{"transport-interrupted", ErrTransportInterrupted},
		{"shutdown-timeout", ErrTimeout},
	} {
		t.Run(test.scenario, func(t *testing.T) {
			_, err := scriptedPreflight(t, test.scenario, 8*time.Second, "test-build")
			if !errors.Is(err, test.want) || strings.Contains(err.Error(), "DO-NOT-LEAK") {
				t.Fatalf("error=%v; want %v", err, test.want)
			}
		})
	}
}

func TestPreflightReadWriteAndAuthDeadlines(t *testing.T) {
	for _, scenario := range []string{"response-timeout", "blocked-write", "missing-auth-mode"} {
		t.Run(scenario, func(t *testing.T) {
			version := "test-build"
			if scenario == "blocked-write" {
				version = strings.Repeat("v", maxMessageBytes/2)
			}
			_, err := scriptedPreflight(t, scenario, 250*time.Millisecond, version)
			if !errors.Is(err, ErrTimeout) || !errors.Is(err, context.DeadlineExceeded) {
				t.Fatalf("error=%v; want bounded deadline failure", err)
			}
		})
	}
	_, err := scriptedPreflight(t, "success", 5*time.Second, strings.Repeat("v", maxMessageBytes))
	if err == nil || !strings.Contains(err.Error(), "request exceeds") {
		t.Fatalf("oversized request: %v", err)
	}
	t.Run("caller-cancel", func(t *testing.T) {
		_, err := scriptedPreflight(t, "caller-cancel", 5*time.Second, "test-build")
		if !errors.Is(err, context.Canceled) || errors.Is(err, ErrTimeout) {
			t.Fatalf("caller cancellation: %v", err)
		}
	})
}

func TestPreflightSelectionAndExecutionBoundary(t *testing.T) {
	options := Options{Executable: "must-not-launch", Dir: t.TempDir(), ClientVersion: "test-build"}
	for _, selection := range []manifest.Selection{
		{Model: "gpt-6.1-sol"}, {Effort: "max"},
		{Model: "gpt-6.1-sol", Effort: "max", Variant: "anything"},
		{Model: "gpt-6.1-sol", Effort: "max", NoVariant: true},
	} {
		if _, err := Preflight(context.Background(), options, selection); err == nil || !strings.Contains(err.Error(), "exact model and effort") {
			t.Fatalf("selection validation: %v", err)
		}
	}
	selection := manifest.Selection{Model: "gpt-6.1-sol", Effort: "max"}
	options.Dir = ""
	if _, err := Preflight(context.Background(), options, selection); err == nil || !strings.Contains(err.Error(), "absolute working directory") {
		t.Fatalf("cwd validation: %v", err)
	}
	options.Dir = t.TempDir()
	if _, err := Preflight(context.Background(), options, selection); err == nil || !strings.Contains(err.Error(), "executable not found") {
		t.Fatalf("missing executable: %v", err)
	}
	var c connection
	for _, method := range []string{"thread/start", "thread/resume", "turn/start", "turn/steer", "command/exec", "command/exec/terminate", "process/spawn", "windowsSandbox/readiness", "permissionProfile/list", "account/login/start", "account/logout", "config/value/write"} {
		if err := c.call(method, nil, nil); err == nil || !strings.Contains(err.Error(), "method unavailable") {
			t.Fatalf("unavailable method %s: %v", method, err)
		}
	}
}
