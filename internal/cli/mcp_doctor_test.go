package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/kninetimmy/orch/internal/execx"
	"github.com/kninetimmy/orch/internal/paths"
	"github.com/kninetimmy/orch/internal/state"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func mcpDoctorExercise(t *testing.T, binary string) {
	t.Helper()
	root, cwd, probes := t.TempDir(), t.TempDir(), t.TempDir()
	configText := strings.Replace(validBothHostsTOML, `mode = "off"`, `mode = "required"`, 1) +
		validOpenCodeTOML[strings.Index(validOpenCodeTOML, "[hosts.opencode"):] + "\n[metrics]\nenabled = true\n"
	writeConfig(t, root, configText)
	mcpWrite(t, filepath.Join(root, ".gitignore"), []byte(".orchestrator/metrics/\n"))
	writeConfig(t, cwd, "invalid launching repository [")
	mcpWrite(t, filepath.Join(cwd, state.Path), []byte("{corrupt cwd"))
	canonical, err := paths.Canonical(root)
	if err != nil {
		t.Fatal(err)
	}
	rootInfo, err := os.Stat(canonical)
	if err != nil {
		t.Fatal(err)
	}
	suffix := ""
	if runtime.GOOS == "windows" {
		suffix = ".exe"
	}
	probeBinary := filepath.Join(probes, "git"+suffix)
	build := exec.CommandContext(t.Context(), "go", "build", "-o", probeBinary, "./testdata/doctor-probe")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build doctor probes: %v: %s", err, out)
	}
	data, err := os.ReadFile(probeBinary)
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"gh", "claude", "codex", "opencode2", "memhub"} {
		if err := os.WriteFile(filepath.Join(probes, name+suffix), data, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	// This explicit allowlist is independent of doctorReport. Any added or
	// mutating command fails in the helper and in the transcript assertion.
	allowed := []execx.Cmd{
		{Name: "git", Args: []string{"rev-parse", "--show-toplevel"}},
		{Name: "gh", Args: []string{"auth", "status"}},
		{Name: "gh", Args: []string{"repo", "view", "--json", "nameWithOwner,defaultBranchRef,url"}},
		{Name: "claude", Args: []string{"plugin", "list", "--json"}},
		{Name: "claude", Args: []string{"--version"}},
		{Name: "codex", Args: []string{"plugin", "list", "--json"}},
		{Name: "opencode2", Args: []string{"--version"}},
		{Name: "memhub", Args: []string{"status"}},
		{Name: "memhub", Args: []string{"recall", "orch memhub recall probe", "--json", "--max-results", "1"}},
	}
	query := url.Values{"location[directory]": {canonical}}
	for _, endpoint := range []string{"plugin", "skill", "model"} {
		allowed = append(allowed, execx.Cmd{Name: "opencode2", Args: []string{"api", "get", "/api/" + endpoint + "?" + query.Encode()}})
	}
	fixturePath, logPath := filepath.Join(probes, "fixture.json"), filepath.Join(probes, "commands.log")
	setProbes := func(runner fakeRunner) {
		t.Helper()
		responses := map[string]execx.Result{}
		for _, cmd := range allowed {
			cmd.Dir = canonical
			response, err := runner.Run(t.Context(), cmd)
			if err != nil {
				t.Fatal(err)
			}
			responses[cmd.Name+" "+strings.Join(cmd.Args, " ")] = response
		}
		data, err := json.Marshal(map[string]any{"Root": canonical, "Log": logPath, "Probes": responses})
		if err != nil {
			t.Fatal(err)
		}
		mcpWrite(t, fixturePath, data)
	}
	setProbes(fakeRunner{toplevel: canonical})
	ctx, cancel := context.WithTimeout(t.Context(), 120*time.Second)
	defer cancel()
	process := func(args ...string) *exec.Cmd {
		cmd := mcpProcess(ctx, binary, root, args...)
		cmd.Env = append(cmd.Env, "PATH="+probes, "ORCH_TEST_DOCTOR_FIXTURE="+fixturePath)
		return cmd
	}
	cmd := process("mcp", "--repo", root)
	cmd.Dir = cwd
	var serverStderr bytes.Buffer
	cmd.Stderr = &serverStderr
	client := mcp.NewClient(&mcp.Implementation{Name: "doctor-test", Version: "1"}, nil)
	before, cwdBefore := mcpTree(t, root), mcpTree(t, cwd)
	session, err := client.Connect(ctx, &mcp.CommandTransport{Command: cmd}, nil)
	if err != nil {
		t.Fatalf("connect: %v: %s", err, &serverStderr)
	}
	closed := false
	closeSession := func() {
		t.Helper()
		if closed {
			return
		}
		closed = true
		before, cwdBefore := mcpTree(t, root), mcpTree(t, cwd)
		if err := session.Close(); err != nil {
			t.Error(err)
		}
		if cmd.ProcessState == nil || !cmd.ProcessState.Success() || serverStderr.Len() != 0 || mcpTree(t, root) != before || mcpTree(t, cwd) != cwdBefore {
			t.Errorf("shutdown: state=%v stderr=%s or repository changed", cmd.ProcessState, &serverStderr)
		}
	}
	t.Cleanup(closeSession)
	mcpDiscover(t, ctx, session, root)
	for _, args := range []any{
		map[string]any{"host": "other"}, map[string]any{"host": ""}, map[string]any{"host": nil}, map[string]any{"host": 1},
		map[string]any{"repo": cwd}, map[string]any{"repo_root": cwd}, map[string]any{"unexpected": true},
		map[string]any{"host": "claude", "repo": cwd}, []any{}, "not an object", 1,
	} {
		result := mcpCall(t, ctx, session, root, "orch_doctor", args)
		if !result.IsError || result.StructuredContent != nil {
			t.Fatalf("invalid doctor arguments accepted: %#v => %+v", args, result)
		}
	}
	if mcpTree(t, root) != before || mcpTree(t, cwd) != cwdBefore {
		t.Fatal("initialization/discovery/invalid arguments changed repositories")
	}
	if _, err := os.Stat(logPath); !os.IsNotExist(err) {
		t.Fatalf("initialization/discovery/invalid arguments launched probes: %v", err)
	}
	for _, name := range []string{"healthy", "OpenCode warning", "other host", "selected host", "all hosts", "authentication", "configuration", "absent tools"} {
		t.Run(name, func(t *testing.T) {
			runner := fakeRunner{toplevel: canonical}
			host, healthy, status := "", true, "success"
			switch name {
			case "OpenCode warning":
				host, status, runner.opencodeVersion = "opencode", "advisory", "opencode v2.10.0"
			case "other host", "selected host", "all hosts":
				runner.codexPluginExit, runner.codexPluginStderr = 1, "unavailable"
				status, healthy = "failure", false
				switch name {
				case "other host":
					host, status, healthy = "claude", "advisory", true
				case "selected host":
					host = "codex"
				}
			case "authentication":
				runner.authExit, healthy = 1, false
			case "configuration":
				mcpWrite(t, filepath.Join(root, ".orchestrator", "config.toml"), []byte("invalid ["))
				healthy = false
			case "absent tools":
				for _, tool := range []string{"git", "gh", "claude", "codex", "opencode2", "memhub"} {
					if err := os.Remove(filepath.Join(probes, tool+suffix)); err != nil {
						t.Fatal(err)
					}
				}
				healthy = false
			}
			setProbes(runner)
			args, input := []string{"doctor", "--json"}, map[string]any{}
			if host != "" {
				args, input["host"] = append(args, "--host", host), host
			}
			before, cwdBefore := mcpTree(t, root), mcpTree(t, cwd)
			cli := process(args...)
			var stdout, stderr bytes.Buffer
			cli.Stdout, cli.Stderr = &stdout, &stderr
			cliErr := cli.Run()
			wantExit := ExitOK
			if !healthy {
				wantExit = ExitError
			}
			if cli.ProcessState == nil || cli.ProcessState.ExitCode() != wantExit || (cliErr == nil) != healthy {
				t.Fatalf("CLI err=%v stdout=%s stderr=%s", cliErr, &stdout, &stderr)
			}
			result := mcpCall(t, ctx, session, root, "orch_doctor", input)
			if result.IsError || len(result.Content) != 1 {
				t.Fatalf("ordinary diagnostic failure became a tool error: %+v", result)
			}
			text, ok := result.Content[0].(*mcp.TextContent)
			if !ok || !reflect.DeepEqual(mcpDecoded(t, stdout.Bytes()), result.StructuredContent) || !reflect.DeepEqual(mcpDecoded(t, stdout.Bytes()), mcpDecoded(t, []byte(text.Text))) {
				t.Fatalf("parity: CLI=%s MCP=%+v", &stdout, result)
			}
			var doc DoctorReport
			if err := json.Unmarshal(stdout.Bytes(), &doc); err != nil {
				t.Fatal(err)
			}
			if doc.SchemaVersion != 1 || doc.Healthy != healthy || doc.SelectedHost != host {
				t.Fatalf("doctor report: %+v", doc)
			}
			if name == "other host" || name == "selected host" || name == "all hosts" || name == "OpenCode warning" {
				checkName := "codex adapter"
				if name == "OpenCode warning" {
					checkName = "opencode runtime"
				}
				found := false
				for _, check := range doc.Checks {
					found = found || (check.Name == checkName && check.Status == status && check.Detail != "")
				}
				if !found {
					t.Fatalf("missing %s/%s: %+v", checkName, status, doc)
				}
			}
			if mcpTree(t, root) != before || mcpTree(t, cwd) != cwdBefore {
				t.Fatal("doctor changed bound or launching repository")
			}
			if name == "configuration" {
				mcpWrite(t, filepath.Join(root, ".orchestrator", "config.toml"), []byte(configText))
			}
			t.Logf("CLI = structured = JSON text: doctor %s healthy=%t; repositories unchanged", name, doc.Healthy)
		})
	}
	log, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatal(err)
	}
	for _, line := range strings.Split(strings.TrimSpace(string(log)), "\n") {
		parts := strings.SplitN(line, "\t", 2)
		if len(parts) != 2 {
			t.Fatalf("invalid probe log: %s", line)
		}
		dirInfo, err := os.Stat(parts[0])
		if err != nil {
			t.Fatalf("stat probe directory %q: %v", parts[0], err)
		}
		if !os.SameFile(rootInfo, dirInfo) {
			t.Fatalf("probe escaped binding: %s", line)
		}
		found := false
		for _, cmd := range allowed {
			found = found || parts[1] == cmd.Name+" "+strings.Join(cmd.Args, " ")
		}
		if !found {
			t.Fatalf("mutation or unapproved command: %s", line)
		}
	}
	closeSession()
}
