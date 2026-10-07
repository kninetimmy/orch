// Package claudefake is a scripted stand-in for Claude Code used only by
// tests. A test binary calls Main from TestMain; when the binary is launched
// with Claude Code's arguments it replays the scenario named in
// $CLAUDE_CONFIG_DIR/fixture-scenario instead of running tests. It uses no
// model, no network and no credentials. No production package imports it.
package claudefake

import (
	"bufio"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"time"
)

// Flags the stand-in advertises in --help, matching Claude Code 2.1.289.
var helpFlags = []string{"-p, --print", "--verbose", "--output-format <format>", "--input-format <format>", "--session-id <uuid>",
	"-r, --resume [value]", "--model <model>", "--effort <level>", "--tools <tools...>", "--allowedTools, --allowed-tools <tools...>",
	"--add-dir <directories...>", "--permission-mode <mode>  (choices: \"acceptEdits\", \"auto\", \"bypassPermissions\", \"manual\", \"dontAsk\", \"plan\")",
	"--permission-prompts <target>", "--restricted", "--safe-mode", "--strict-mcp-config", "--include-hook-events",
	"--bare", "--fallback-model <model>", "--dangerously-skip-permissions", "--no-session-persistence"}

// Required and forbidden launch arguments; a violation exits 70 before startup.
var (
	required  = []string{"--print", "--verbose", "--restricted", "--safe-mode", "--strict-mcp-config", "--include-hook-events"}
	forbidden = []string{"--bare", "--dangerously-skip-permissions", "--allow-dangerously-skip-permissions", "--fallback-model", "bypassPermissions", "--no-session-persistence"}
)

// Scenario returns the scripted scenario for this process.
func scenario(dir string) string {
	data, err := os.ReadFile(filepath.Join(dir, "fixture-scenario"))
	if err != nil {
		return "complete"
	}
	return strings.TrimSpace(string(data))
}

// Main runs the stand-in and exits when os.Args look like a Claude Code
// launch; otherwise it returns so the test binary runs its tests.
func Main() {
	if len(os.Args) < 2 || !slices.Contains([]string{"--help", "--print", "fixture-sleep"}, os.Args[1]) {
		return
	}
	dir := os.Getenv("CLAUDE_CONFIG_DIR")
	switch os.Args[1] {
	case "fixture-sleep":
		time.Sleep(time.Hour)
	case "--help":
		logLine(dir, "launches.jsonl", map[string]any{"args": os.Args[1:]})
		for _, flag := range helpFlags {
			if scenario(dir) == "missing-flag" && flag == "--safe-mode" {
				continue
			}
			_, _ = os.Stdout.WriteString("  " + flag + "\n")
		}
	default:
		session(dir)
	}
	os.Exit(0)
}

func logLine(dir, name string, value any) {
	f, err := os.OpenFile(filepath.Join(dir, name), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		os.Exit(71)
	}
	data, _ := json.Marshal(value)
	_, err = f.Write(append(data, '\n'))
	if err != nil || f.Close() != nil {
		os.Exit(72)
	}
}

func value(args []string, flag string) string {
	if i := slices.Index(args, flag); i >= 0 && i+1 < len(args) {
		return args[i+1]
	}
	return ""
}

func session(dir string) {
	args := os.Args[1:]
	cwd, _ := os.Getwd()
	env := []string{}
	for _, entry := range os.Environ() {
		name, _, _ := strings.Cut(entry, "=")
		env = append(env, name)
	}
	logLine(dir, "launches.jsonl", map[string]any{"args": args, "cwd": cwd, "env": env, "pid": os.Getpid()})
	for _, flag := range required {
		if !slices.Contains(args, flag) {
			os.Exit(70)
		}
	}
	for _, flag := range forbidden {
		if slices.Contains(args, flag) {
			os.Exit(70)
		}
	}
	// Claude Code 2.1.289 creates this in its temp directory and leaves it empty.
	if os.MkdirAll(filepath.Join(os.TempDir(), "claude"), 0o700) != nil {
		os.Exit(78)
	}
	name := scenario(dir)
	id, resume := value(args, "--session-id"), value(args, "--resume")
	if resume != "" {
		id = resume
	}
	model := value(args, "--model")
	tools := strings.Split(value(args, "--tools"), ",")
	out := json.NewEncoder(os.Stdout)
	emit := func(v map[string]any) {
		if _, ok := v["session_id"]; !ok {
			v["session_id"] = id
		}
		if out.Encode(v) != nil {
			os.Exit(73)
		}
	}
	init := map[string]any{"type": "system", "subtype": "init", "cwd": cwd, "session_id": id, "tools": tools, "mcp_servers": []any{},
		"model": model, "permissionMode": value(args, "--permission-mode"), "apiKeySource": "none", "claude_code_version": "2.1.289",
		"plugins": []any{map[string]string{"name": "cc-plugin-agents-md", "path": "builtin", "source": "cc-plugin-agents-md@builtin"}}}
	switch name {
	case "model-init":
		init["model"] = "claude-substitute-model"
	case "extra-tool":
		init["tools"] = append(tools, "WebFetch")
	case "mcp":
		init["mcp_servers"] = []any{map[string]string{"name": "remote", "status": "connected"}}
	case "plugin":
		init["plugins"] = []any{map[string]string{"name": "custom", "path": filepath.Join(dir, "plugin"), "source": "custom@marketplace"}}
	case "apikey":
		init["apiKeySource"] = "ANTHROPIC_API_KEY"
	case "permission-mode":
		init["permissionMode"] = "default"
	case "other-session":
		init["session_id"] = "00000000-0000-4000-8000-000000000000"
	}
	usage := func(scale int64) map[string]any {
		counters := map[string]any{"inputTokens": 100 * scale, "outputTokens": 40 * scale, "cacheReadInputTokens": 900 * scale, "cacheCreationInputTokens": 80 * scale, "thinkingTokens": 12 * scale}
		if name == "partial-usage" {
			counters = map[string]any{"inputTokens": 100, "outputTokens": 40}
		}
		models := map[string]any{model: counters}
		if name == "model-usage" {
			models["claude-substitute-model"] = map[string]any{"inputTokens": 5}
		}
		return models
	}
	success := func(scale int64) {
		emit(map[string]any{"type": "result", "subtype": "success", "is_error": false, "result": "Scripted synthetic evaluation output.", "modelUsage": usage(scale)})
	}
	if resume != "" {
		emit(init)
		success(2) // the session's running totals include the earlier invocation
	} else if name != "init-after-prompt" {
		emit(init)
	}
	input := make(chan map[string]any)
	go func() {
		scanner := bufio.NewScanner(os.Stdin)
		scanner.Buffer(make([]byte, 64<<10), 8<<20)
		for scanner.Scan() {
			var m map[string]any
			if json.Unmarshal(scanner.Bytes(), &m) != nil {
				os.Exit(74)
			}
			logLine(dir, "input.jsonl", m)
			input <- m
		}
		close(input)
	}()
	for m := range input {
		switch m["type"] {
		case "control_request":
			request, _ := m["request"].(map[string]any)
			if name == "hang" || request["subtype"] != "interrupt" {
				continue
			}
			emit(map[string]any{"type": "control_response", "response": map[string]any{"subtype": "success", "request_id": m["request_id"], "response": map[string]any{"still_queued": []any{}}}})
			emit(map[string]any{"type": "result", "subtype": "error_during_execution", "is_error": true, "modelUsage": usage(1)})
			continue
		case "user":
		default:
			os.Exit(75)
		}
		if name == "init-after-prompt" {
			emit(init)
		}
		assistant := map[string]any{"type": "assistant", "parent_tool_use_id": nil, "message": map[string]any{"model": model, "content": []any{map[string]string{"type": "text", "text": "Working."}}}}
		switch name {
		case "model-assistant":
			assistant["message"].(map[string]any)["model"] = "claude-substitute-model"
		case "delegate":
			assistant["parent_tool_use_id"] = "toolu_fixture"
		case "unapproved-tool":
			assistant["message"].(map[string]any)["content"] = []any{map[string]string{"type": "tool_use", "name": "WebFetch"}}
		}
		emit(assistant)
		// Claude Code 2.1.289 emits this, with a string message, each time
		// dontAsk denies a tool call.
		emit(map[string]any{"type": "system", "subtype": "permission_denied", "message": "Permission to use Bash has been denied.",
			"tool_name": "Bash", "tool_use_id": "toolu_fixture_denied"})
		switch name {
		case "slow":
			continue
		case "hang":
			child := exec.Command(os.Args[0], "fixture-sleep")
			if child.Start() != nil {
				os.Exit(76)
			}
			logLine(dir, "pids.jsonl", map[string]int{"session": os.Getpid(), "descendant": child.Process.Pid})
			time.Sleep(time.Hour)
		case "disconnect":
			os.Exit(3)
		}
		if slices.Contains(tools, "Write") {
			if os.WriteFile(filepath.Join(cwd, "fixture-output.txt"), []byte("synthetic implementer change\n"), 0o600) != nil {
				os.Exit(77)
			}
		}
		success(1)
	}
	if name == "hang" {
		time.Sleep(time.Hour)
	}
}
