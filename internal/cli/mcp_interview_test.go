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

	"github.com/kninetimmy/orch/internal/config"
	"github.com/kninetimmy/orch/internal/execx"
	"github.com/kninetimmy/orch/internal/lockfile"
	"github.com/kninetimmy/orch/internal/paths"
	"github.com/kninetimmy/orch/internal/question"
	"github.com/kninetimmy/orch/internal/state"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func mcpInterviewExercise(t *testing.T, binary string) {
	t.Helper()
	root, err := paths.Canonical(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	cwd, probes := t.TempDir(), t.TempDir()
	writeConfig(t, root, validOpenCodeTOML)
	writeConfig(t, cwd, "invalid launching configuration [")
	mcpWrite(t, filepath.Join(cwd, state.Path), []byte("{invalid launching state"))
	cwdBefore := mcpTree(t, cwd)
	suffix := ""
	if runtime.GOOS == "windows" {
		suffix = ".exe"
	}
	probeBinary := filepath.Join(probes, "git"+suffix)
	build := exec.CommandContext(t.Context(), "go", "build", "-o", probeBinary, "./testdata/doctor-probe")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build interview probes: %v: %s", err, out)
	}
	data, err := os.ReadFile(probeBinary)
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"gh", "opencode2", "memhub"} {
		if err := os.WriteFile(filepath.Join(probes, name+suffix), data, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	// Reuse the scripted executable, but allow only this interview's read probes.
	allowed := []execx.Cmd{
		{Name: "git", Args: []string{"rev-parse", "--show-toplevel"}, Dir: root},
		{Name: "memhub", Args: []string{"status"}, Dir: root},
		{Name: "opencode2", Args: []string{"--version"}, Dir: root},
		{Name: "opencode2", Args: []string{"api", "get", "/api/model?" + url.Values{"location[directory]": {root}}.Encode()}, Dir: root},
	}
	fixturePath, logPath := filepath.Join(probes, "fixture.json"), filepath.Join(probes, "commands.log")
	runner := fakeRunner{toplevel: root, opencodeVersion: "opencode v3.0.0", // advisory must stay off stdout
		opencodeCatalog: openCodeCatalogResponse(root, `
{"id":"gpt-5.6-sol","providerID":"openai","enabled":true,"capabilities":{"tools":true,"input":["text"],"output":["text"]},"variants":[{"id":"high"},{"id":"xhigh"},{"id":"max"}]},
{"id":"gpt-5.6-luna","providerID":"openai","enabled":true,"capabilities":{"tools":true,"input":["text"],"output":["text"]},"variants":[{"id":"max"}]},
{"id":"gpt-5.6-terra","providerID":"openai","enabled":true,"capabilities":{"tools":true,"input":["text"],"output":["text"]},"variants":[{"id":"max"}]},
{"id":"bare","providerID":"local","enabled":true,"capabilities":{"tools":true,"input":["text"],"output":["text"]},"variants":[]}`),
	}
	setProbes := func(runner fakeRunner) {
		t.Helper()
		responses := map[string]execx.Result{}
		for _, cmd := range allowed {
			response, err := runner.Run(t.Context(), cmd)
			if err != nil {
				t.Fatal(err)
			}
			responses[cmd.Name+" "+strings.Join(cmd.Args, " ")] = response
		}
		data, err := json.Marshal(map[string]any{"Root": root, "Log": logPath, "Probes": responses})
		if err != nil {
			t.Fatal(err)
		}
		mcpWrite(t, fixturePath, data)
	}
	setProbes(runner)
	readLog := func() string {
		t.Helper()
		data, err := os.ReadFile(logPath)
		if err != nil && !os.IsNotExist(err) {
			t.Fatal(err)
		}
		return string(data)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 180*time.Second)
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
	client := mcp.NewClient(&mcp.Implementation{Name: "interview-test", Version: "1"}, nil)
	before := mcpTree(t, root)
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
		before, logBefore := mcpTree(t, root), readLog()
		if err := session.Close(); err != nil {
			t.Error(err)
		}
		if cmd.ProcessState == nil || !cmd.ProcessState.Success() || serverStderr.Len() != 0 || mcpTree(t, root) != before || mcpTree(t, cwd) != cwdBefore || readLog() != logBefore {
			t.Errorf("shutdown: state=%v stderr=%s or repository/probes changed", cmd.ProcessState, &serverStderr)
		}
	}
	defer closeSession()
	mcpDiscover(t, ctx, session, root)
	if mcpTree(t, root) != before || mcpTree(t, cwd) != cwdBefore || readLog() != "" {
		t.Fatal("initialization/discovery changed repositories or launched probes")
	}
	toolName := func(kind string) string { return "orch_" + strings.ReplaceAll(kind, "-", "_") + "_step" }
	step := func(kind string, input any) (question.Document, string) {
		t.Helper()
		data, err := json.Marshal(input)
		if err != nil {
			t.Fatal(err)
		}
		before := mcpTree(t, root)
		cli := process(kind, "--step")
		var stdout, stderr bytes.Buffer
		cli.Stdin, cli.Stdout, cli.Stderr = bytes.NewReader(data), &stdout, &stderr
		cliErr := cli.Run()
		result := mcpCall(t, ctx, session, root, toolName(kind), input)
		if mcpTree(t, root) != before || mcpTree(t, cwd) != cwdBefore || len(result.Content) != 1 {
			t.Fatalf("interview changed repositories or missing content: %+v", result)
		}
		text, ok := result.Content[0].(*mcp.TextContent)
		if !ok {
			t.Fatalf("interview content = %T", result.Content[0])
		}
		if result.IsError {
			if cliErr == nil || stdout.Len() != 0 || result.StructuredContent != nil || stderr.String() != "orch "+kind+": "+text.Text+"\n" {
				t.Fatalf("error parity: CLI=%v stdout=%s stderr=%s MCP=%+v", cliErr, &stdout, &stderr, result)
			}
			return question.Document{}, text.Text
		}
		if cliErr != nil || stderr.Len() != 0 || !reflect.DeepEqual(mcpDecoded(t, stdout.Bytes()), result.StructuredContent) || !reflect.DeepEqual(mcpDecoded(t, stdout.Bytes()), mcpDecoded(t, []byte(text.Text))) {
			t.Fatalf("step parity: CLI=%v stdout=%s stderr=%s MCP=%+v", cliErr, &stdout, &stderr, result)
		}
		var doc question.Document
		if err := json.Unmarshal(stdout.Bytes(), &doc); err != nil || doc.SchemaVersion != question.SchemaVersion {
			t.Fatalf("step document = %+v, %v", doc, err)
		}
		return doc, ""
	}
	mustStep := func(kind string, answers map[string]string) question.Document {
		t.Helper()
		doc, failure := step(kind, question.AnswerSet{SchemaVersion: question.SchemaVersion, Answers: answers})
		if failure != "" {
			t.Fatal(failure)
		}
		return doc
	}
	rolePrefix := func(kind string) string {
		if kind == "configure" {
			return "host.opencode.role."
		}
		return "hosts.opencode.roles."
	}
	roleAnswers := func(kind string) map[string]string {
		if kind == "configure" {
			return map[string]string{"pick.hosts": "no", "pick.protected": "no", "pick.settings": "no", "pick.roles.opencode": "yes"}
		}
		return map[string]string{"pick.opencode": "yes", "pick.settings": "no"}
	}
	for _, kind := range []string{"configure", "configure-local"} {
		for _, spelling := range []string{"native", "legacy"} {
			configText := validOpenCodeTOML
			if spelling == "legacy" {
				configText = strings.ReplaceAll(configText, "variant =", "effort =")
			}
			mcpWrite(t, filepath.Join(root, config.Path), []byte(configText))
			answers := map[string]string{}
			var proposal *question.Summary
			complete, modelSeen, variantSeen := false, false, false
			for n := 0; n < 100 && !complete; n++ {
				doc := mustStep(kind, answers)
				switch doc.Kind {
				case question.DocQuestions:
					for _, q := range doc.Questions {
						answers[q.ID] = q.Default
						switch q.ID {
						case "pick.roles.opencode", "pick.opencode", "pick.settings":
							answers[q.ID] = "yes"
						case "pick.protected":
							answers[q.ID] = "no"
						case "concurrency.max_subagents":
							if q.Default != "3" {
								t.Fatalf("fresh committed default = %q", q.Default)
							}
							answers[q.ID] = "4"
						case rolePrefix(kind) + "architect.model":
							modelSeen = true
							if q.Default != "openai/gpt-5.6-sol" || q.Pagination == nil || len(q.Options) != 4 || q.FreeText {
								t.Fatalf("catalog model question = %+v", q)
							}
						case rolePrefix(kind) + "architect.variant":
							variantSeen = true
							if q.Default != "xhigh" || q.Pagination == nil || len(q.Options) != 4 {
								t.Fatalf("%s variant question = %+v", spelling, q)
							}
							answers[q.ID] = "high"
						case rolePrefix(kind) + "scout.model":
							answers[q.ID] = "local/bare"
						case rolePrefix(kind) + "scout.variant":
							t.Fatal("bare model asked for a variant")
						}
					}
				case question.DocSummary:
					if len(doc.Summary.Blockers) != 0 || len(doc.Questions) != 1 {
						t.Fatalf("blocked summary: %+v", doc)
					}
					proposal = doc.Summary
					answers["approval"] = "approve"
				case question.DocComplete:
					if proposal == nil || !reflect.DeepEqual(*proposal, doc.Complete.Summary) || !doc.Complete.BootstrapReady || !modelSeen || !variantSeen {
						t.Fatalf("incomplete transcript: %+v", doc)
					}
					if kind == "configure" && doc.Complete.Detection["opencode_warning"] == "" {
						t.Fatal("runtime advisory was lost from completion")
					}
					if again := mustStep(kind, answers); !reflect.DeepEqual(again, doc) {
						t.Fatal("repeated completion changed document")
					}
					cfg, err := config.Parse([]byte(configText))
					if err != nil {
						t.Fatal(err)
					}
					if kind == "configure" {
						cfg, err = config.Parse([]byte(proposal.ConfigTOML))
					} else {
						cfg, err = config.MergeLocal(cfg, []byte(proposal.ConfigTOML))
					}
					if err != nil || cfg.Hosts.OpenCode.Roles.Architect.EffectiveOpenCodeVariant() != "high" || cfg.Hosts.OpenCode.Roles.Scout.Model != "local/bare" || cfg.Hosts.OpenCode.Roles.Scout.EffectiveOpenCodeVariant() != "" {
						t.Fatalf("native/bare materialization = %+v, %v", cfg, err)
					}
					if kind == "configure" {
						changed := runner
						changed.memhubStatusExit, changed.memhubStatusStderr = 1, "unhealthy fixture"
						setProbes(changed)
						fresh := mustStep(kind, answers)
						if fresh.Complete.Detection["memhub_healthy"] != "no" || fresh.Complete.Detection["memhub_detail"] == "" {
							t.Fatalf("host facts were cached: %+v", fresh.Complete.Detection)
						}
						setProbes(runner)
					}
					answers["approval"] = "abort"
					if aborted := mustStep(kind, answers); aborted.Kind != question.DocAborted {
						t.Fatalf("abort document = %+v", aborted)
					}
					complete = true
				default:
					t.Fatalf("unexpected interview document: %+v", doc)
				}
			}
			if !complete {
				t.Fatalf("%s/%s did not complete", kind, spelling)
			}
			t.Logf("CLI = structured = JSON text: %s/%s initial questions through summary, completion, retry and abort; repository unchanged", kind, spelling)
		}
		// Preserve DecodeAnswers' omitted/null-map normalization.
		for _, input := range []json.RawMessage{json.RawMessage(`{"schema_version":2}`), json.RawMessage(`{"schema_version":2,"answers":null}`)} {
			if doc, failure := step(kind, input); failure != "" || doc.Kind != question.DocQuestions {
				t.Fatalf("empty answers normalization: %+v, %s", doc, failure)
			}
		}
		logBefore := readLog()
		for _, input := range []json.RawMessage{
			json.RawMessage(`{}`), json.RawMessage(`null`), json.RawMessage(`[]`), json.RawMessage(`"answers"`),
			json.RawMessage(`{"schema_version":1,"answers":{}}`), json.RawMessage(`{"schema_version":"2","answers":{}}`),
			json.RawMessage(`{"schema_version":2,"answers":[]}`), json.RawMessage(`{"schema_version":2,"answers":{"pick.settings":true}}`),
			json.RawMessage(`{"schema_version":2,"answers":{},"repo_root":"elsewhere"}`),
			json.RawMessage(`{"schema_version":2,"answers":{},"complete":{}}`),
		} {
			result := mcpCall(t, ctx, session, root, toolName(kind), input)
			if !result.IsError || result.StructuredContent != nil {
				t.Fatalf("malformed AnswerSet accepted: %s => %+v", input, result)
			}
		}
		if readLog() != logBefore {
			t.Fatal("malformed AnswerSet launched probes")
		}
		for _, answers := range []map[string]string{
			{"unexpected": "yes"}, {"pick.settings": "maybe"}, {"approval": "approve"},
			{rolePrefix(kind) + "architect.model": "openai/gpt-5.6-sol"},
		} {
			if _, failure := step(kind, question.AnswerSet{SchemaVersion: question.SchemaVersion, Answers: answers}); failure == "" {
				t.Fatalf("invalid/stale answers accepted: %v", answers)
			}
		}
		for _, catalog := range []string{"{invalid catalog", openCodeCatalogResponse(root, ""), openCodeCatalogResponse(cwd, "")} {
			changed := runner
			changed.opencodeCatalog = catalog
			setProbes(changed)
			if _, failure := step(kind, question.AnswerSet{SchemaVersion: question.SchemaVersion, Answers: roleAnswers(kind)}); !strings.Contains(failure, "catalog") {
				t.Fatalf("catalog failure lost: %s", failure)
			}
		}
		changed := runner
		changed.opencodeCatalogExit = 1
		setProbes(changed)
		if _, failure := step(kind, question.AnswerSet{SchemaVersion: question.SchemaVersion, Answers: roleAnswers(kind)}); !strings.Contains(failure, "catalog") {
			t.Fatalf("unavailable catalog failure lost: %s", failure)
		}
		setProbes(runner)
		// A variant answer becomes stale when that model no longer has variants.
		answers := roleAnswers(kind)
		answers[rolePrefix(kind)+"architect.model"] = "openai/gpt-5.6-sol"
		answers[rolePrefix(kind)+"architect.variant"] = "high"
		changed = runner
		changed.opencodeCatalog = openCodeCatalogResponse(root, `{"id":"gpt-5.6-sol","providerID":"openai","enabled":true,"capabilities":{"tools":true,"input":["text"],"output":["text"]},"variants":[]}`)
		setProbes(changed)
		if _, failure := step(kind, question.AnswerSet{SchemaVersion: question.SchemaVersion, Answers: answers}); !strings.Contains(failure, "currently applicable question") {
			t.Fatalf("stale variant refusal lost: %s", failure)
		}
		delete(answers, rolePrefix(kind)+"architect.variant")
		doc := mustStep(kind, answers)
		for _, q := range doc.Questions {
			if q.ID == rolePrefix(kind)+"architect.variant" {
				t.Fatal("bare model still asks for a variant")
			}
		}
		setProbes(runner)
	}

	// Fresh committed versus local defaults retain the CLI interviews' distinct
	// semantics. A malformed local file is repair data, not a loader failure.
	mcpWrite(t, filepath.Join(root, config.Path), []byte(validOpenCodeTOML+"\n[concurrency]\nmax_subagents = 6\n"))
	mcpWrite(t, filepath.Join(root, config.LocalOverridePath), []byte("[concurrency]\nmax_subagents = 7\n"))
	for _, kind := range []string{"configure", "configure-local"} {
		answers := map[string]string{"pick.settings": "yes", "pick.opencode": "no"}
		want := "7"
		if kind == "configure" {
			answers, want = map[string]string{"pick.hosts": "no", "pick.settings": "yes", "pick.protected": "no", "pick.roles.opencode": "no"}, "6"
		}
		doc := mustStep(kind, answers)
		if len(doc.Questions) == 0 || doc.Questions[0].ID != "concurrency.max_subagents" || doc.Questions[0].Default != want {
			t.Fatalf("%s fresh defaults = %+v, want %s", kind, doc, want)
		}
	}
	mcpWrite(t, filepath.Join(root, config.LocalOverridePath), []byte("invalid local ["))
	mustStep("configure-local", map[string]string{})
	for _, invalid := range []string{"invalid config [", strings.Replace(validOpenCodeTOML, "schema_version  = 1", "schema_version  = 99", 1), validOpenCodeTOML + "\n[concurrency]\nmax_subagents = 0\n", ""} {
		path := filepath.Join(root, config.Path)
		if invalid == "" {
			if err := os.Remove(path); err != nil {
				t.Fatal(err)
			}
		} else {
			mcpWrite(t, path, []byte(invalid))
		}
		for _, kind := range []string{"configure", "configure-local"} {
			if _, failure := step(kind, question.AnswerSet{SchemaVersion: question.SchemaVersion}); failure == "" {
				t.Fatal("invalid/missing committed configuration accepted")
			}
		}
	}
	mcpWrite(t, filepath.Join(root, config.Path), []byte(validOpenCodeTOML))
	for _, refusal := range []string{"active Delivery", "held lock", "corrupt state", "corrupt lock"} {
		switch refusal {
		case "active Delivery":
			if _, err := state.EnterDelivery(root, "opencode", testPlanRef(), testIssues()); err != nil {
				t.Fatal(err)
			}
			// Prove active state refuses even without its normally paired lock.
			if err := os.Remove(filepath.Join(root, lockfile.Path)); err != nil {
				t.Fatal(err)
			}
		case "held lock":
			if err := state.Save(root, &state.State{SchemaVersion: state.SchemaVersion, Mode: state.ModeAssist}); err != nil {
				t.Fatal(err)
			}
			if err := lockfile.Acquire(root, lockfile.Owner{RunID: "held", Host: "opencode", Hostname: "fixture", PID: 1, AcquiredAt: time.Now()}); err != nil {
				t.Fatal(err)
			}
		case "corrupt state":
			mcpWrite(t, filepath.Join(root, state.Path), []byte("{corrupt state"))
		case "corrupt lock":
			if err := state.Save(root, &state.State{SchemaVersion: state.SchemaVersion, Mode: state.ModeAssist}); err != nil {
				t.Fatal(err)
			}
			mcpWrite(t, filepath.Join(root, lockfile.Path), []byte("{corrupt lock"))
		}
		logBefore := readLog()
		for _, kind := range []string{"configure", "configure-local"} {
			if _, failure := step(kind, question.AnswerSet{SchemaVersion: question.SchemaVersion}); failure == "" {
				t.Fatalf("%s was not refused", refusal)
			}
		}
		if readLog() != logBefore {
			t.Fatalf("%s launched probes", refusal)
		}
	}
	if err := os.Remove(filepath.Join(root, lockfile.Path)); err != nil {
		t.Fatal(err)
	}
	// CLI discovery is unchanged; only MCP must reject a different Git root.
	changed := runner
	changed.toplevel = cwd
	setProbes(changed)
	for _, kind := range []string{"configure", "configure-local"} {
		logBefore := readLog()
		result := mcpCall(t, ctx, session, root, toolName(kind), question.AnswerSet{SchemaVersion: question.SchemaVersion})
		if !result.IsError || result.StructuredContent != nil || len(result.Content) != 1 || !strings.Contains(result.Content[0].(*mcp.TextContent).Text, "does not match bound repository") {
			t.Fatalf("Git root mismatch accepted: %+v", result)
		}
		if added := strings.TrimPrefix(readLog(), logBefore); strings.Count(added, "\n") != 1 || !strings.Contains(added, "git rev-parse --show-toplevel") {
			t.Fatalf("mismatch reached probes beyond Git: %s", added)
		}
	}
	setProbes(runner)
	for _, kind := range []string{"configure", "configure-local"} {
		mustStep(kind, map[string]string{}) // same-connection recovery
	}
	rootInfo, err := os.Stat(root)
	if err != nil {
		t.Fatal(err)
	}
	for _, line := range strings.Split(strings.TrimSpace(readLog()), "\n") {
		parts := strings.SplitN(line, "\t", 2)
		if len(parts) != 2 {
			t.Fatalf("invalid probe log: %s", line)
		}
		dirInfo, err := os.Stat(parts[0])
		if err != nil || !os.SameFile(rootInfo, dirInfo) {
			t.Fatalf("probe escaped repository: %s, %v", line, err)
		}
		found := false
		for _, cmd := range allowed {
			found = found || parts[1] == cmd.Name+" "+strings.Join(cmd.Args, " ")
		}
		if !found {
			t.Fatalf("unapproved interview command: %s", line)
		}
	}
	closeSession()
}
