package evalplan

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/BurntSushi/toml"
	"github.com/kninetimmy/orch/internal/codexnative"
	"github.com/kninetimmy/orch/internal/config"
	"github.com/kninetimmy/orch/internal/evalcorpus"
	"github.com/kninetimmy/orch/internal/metrics"
)

func TestMain(m *testing.M) {
	if len(os.Args) >= 4 && reflect.DeepEqual(os.Args[1:4], []string{"app-server", "--listen", "stdio://"}) {
		evaluationNativeServer()
		os.Exit(0)
	}
	os.Exit(m.Run())
}

// Script the native RPC boundary, never installed Codex, authentication or inference.
func evaluationNativeServer() {
	var config map[string]any
	var overrides []string
	for i := 4; i+1 < len(os.Args); i += 2 {
		overrides = append(overrides, os.Args[i+1])
	}
	if _, err := toml.Decode(strings.Join(overrides, "\n"), &config); err != nil {
		os.Exit(80)
	}
	if config == nil {
		config = map[string]any{}
	}
	if config["mcp_servers"] == nil {
		config["mcp_servers"] = map[string]any{}
	}
	cwd, err := os.Getwd()
	if err != nil {
		os.Exit(81)
	}
	profile, _ := config["default_permissions"].(string)
	thread, session, turn := "thread-"+filepath.Base(cwd), "session-"+filepath.Base(cwd), "turn-"+filepath.Base(cwd)
	write := func(value any) {
		if json.NewEncoder(os.Stdout).Encode(value) != nil {
			os.Exit(82)
		}
	}
	response := func(id string, result any) { write(map[string]any{"id": id, "result": result}) }
	notify := func(method string, params any) { write(map[string]any{"method": method, "params": params}) }
	completed := func(status string) {
		notify("turn/completed", map[string]any{"threadId": thread, "turn": map[string]any{"id": turn, "status": status, "items": []any{map[string]any{"id": "answer", "type": "agentMessage", "phase": "final_answer", "text": "Explicit synthetic evaluation output."}}}})
	}
	scenario := ""
	scanner := bufio.NewScanner(os.Stdin)
	scanner.Buffer(make([]byte, 4096), 1<<20)
	for scanner.Scan() {
		var request struct {
			ID, Method string
			Params     json.RawMessage
		}
		if json.Unmarshal(scanner.Bytes(), &request) != nil {
			os.Exit(83)
		}
		if policy, ok := config["shell_environment_policy"].(map[string]any); ok {
			scratch := policy["set"].(map[string]any)["TEMP"].(string)
			log := filepath.Join(scratch, "native-calls.txt")
			data, err := os.ReadFile(log)
			if err != nil && !errors.Is(err, os.ErrNotExist) {
				os.Exit(84)
			}
			if os.WriteFile(log, append(data, []byte(request.Method+"\n")...), 0o600) != nil {
				os.Exit(85)
			}
		}
		switch request.Method {
		case "initialize":
			var params struct{ ClientInfo struct{ Version string } }
			if json.Unmarshal(request.Params, &params) != nil {
				os.Exit(86)
			}
			scenario = strings.TrimPrefix(params.ClientInfo.Version, "fixture-evaluation:")
			response(request.ID, map[string]any{"userAgent": "orch/0.160.0 (scripted)", "platformFamily": "windows", "platformOs": "windows"})
			notify("account/updated", map[string]string{"authMode": "chatgpt"})
		case "initialized":
		case "account/read":
			response(request.ID, map[string]any{"account": map[string]string{"type": "chatgpt"}, "requiresOpenaiAuth": true})
		case "model/list":
			data := []any{}
			for _, model := range []string{"gpt-5.6-terra", "gpt-5.6-sol"} {
				data = append(data, map[string]any{"id": model, "model": model, "supportedReasoningEfforts": []any{map[string]string{"reasoningEffort": "low"}, map[string]string{"reasoningEffort": "medium"}, map[string]string{"reasoningEffort": "high"}}})
			}
			response(request.ID, map[string]any{"data": data, "nextCursor": nil})
		case "config/read":
			if scenario == "protection" && profile != "" {
				config["permissions"].(map[string]any)[profile].(map[string]any)["filesystem"].(map[string]any)[cwd] = "deny"
			}
			if scenario == "external-protection" && profile != "" {
				filesystem := config["permissions"].(map[string]any)[profile].(map[string]any)["filesystem"].(map[string]any)
				for path := range filesystem {
					if strings.HasSuffix(path, "earlier-private-evidence") {
						delete(filesystem, path)
					}
				}
			}
			if scenario == "skills" && config["skills"] != nil {
				config["skills"].(map[string]any)["include_instructions"] = true
			}
			response(request.ID, map[string]any{"config": config})
		case "configRequirements/read":
			var requirements any
			if scenario == "managed" {
				requirements = map[string]string{"additionalDeveloperInstructions": privateSentinel}
			}
			response(request.ID, map[string]any{"requirements": requirements})
		case "experimentalFeature/list":
			data := []any{}
			for name, enabled := range config["features"].(map[string]any) {
				data = append(data, map[string]any{"name": name, "enabled": enabled})
			}
			response(request.ID, map[string]any{"data": data, "nextCursor": nil})
		case "windowsSandbox/readiness":
			response(request.ID, map[string]string{"status": "ready"})
		case "permissionProfile/list":
			response(request.ID, map[string]any{"data": []any{map[string]any{"id": profile, "allowed": true}}, "nextCursor": nil})
		case "hooks/list":
			response(request.ID, map[string]any{"data": []any{map[string]any{"cwd": cwd, "hooks": []any{}, "errors": []any{}, "warnings": []any{}}}})
		case "plugin/installed":
			response(request.ID, map[string]any{"marketplaces": []any{}, "marketplaceLoadErrors": []any{}})
		case "thread/start":
			var params struct {
				Cwd, Model, ModelProvider, ApprovalPolicy, Permissions, DeveloperInstructions string
				Config                                                                        map[string]string
				RuntimeWorkspaceRoots, SelectedCapabilityRoots, DynamicTools                  []any
			}
			role, err := os.ReadFile(filepath.Join(cwd, "ROLE.md"))
			if err != nil || json.Unmarshal(request.Params, &params) != nil || !strings.HasPrefix(params.DeveloperInstructions, string(role)) || strings.Contains(params.DeveloperInstructions, privateSentinel) || params.Cwd != cwd || params.ModelProvider != "openai" || params.ApprovalPolicy != "never" || params.Permissions != profile || params.RuntimeWorkspaceRoots == nil || len(params.RuntimeWorkspaceRoots) != 0 || params.SelectedCapabilityRoots == nil || len(params.SelectedCapabilityRoots) != 0 || params.DynamicTools == nil || len(params.DynamicTools) != 0 {
				os.Exit(87)
			}
			sources := []string{}
			if info, err := os.Stat(filepath.Join(os.Getenv("CODEX_HOME"), "AGENTS.md")); err == nil && info.Size() != 0 {
				sources = append(sources, filepath.Join(os.Getenv("CODEX_HOME"), "AGENTS.md"))
			}
			if scenario == "private-source" {
				sources = []string{filepath.Join(filepath.Dir(cwd), "private", "AGENTS.md")}
			}
			model := params.Model
			if scenario == "profile" {
				model = "unapproved-model"
			}
			response(request.ID, map[string]any{"thread": map[string]any{"id": thread, "sessionId": session, "cwd": cwd, "turns": []any{}}, "cwd": cwd, "model": model, "modelProvider": "openai", "reasoningEffort": params.Config["model_reasoning_effort"], "runtimeWorkspaceRoots": []any{}, "instructionSources": sources, "approvalPolicy": "never", "activePermissionProfile": map[string]any{"id": profile, "extends": nil}})
		case "turn/start":
			var params struct {
				ThreadID, Model, Effort string
				Input                   []struct{ Type, Text string }
			}
			task, err := os.ReadFile(filepath.Join(cwd, "TASK.md"))
			if err != nil || json.Unmarshal(request.Params, &params) != nil || params.ThreadID != thread || len(params.Input) != 1 || params.Input[0].Type != "text" || params.Input[0].Text != string(task) || strings.Contains(params.Input[0].Text, privateSentinel) {
				os.Exit(88)
			}
			response(request.ID, map[string]any{"turn": map[string]any{"id": turn, "status": "inProgress", "items": []any{}}})
			usage := func(n int) {
				notify("thread/tokenUsage/updated", map[string]any{"threadId": thread, "turnId": turn, "tokenUsage": map[string]any{"total": map[string]any{"inputTokens": n, "outputTokens": 0}}})
			}
			usage(20)
			usage(20)
			usage(30)
			if scenario == "wait" {
				continue
			}
			if params.Effort == "high" {
				if os.WriteFile(filepath.Join(cwd, "code", "public.go"), []byte("package packet\n// changed synthetic task\n"), 0o600) != nil {
					os.Exit(89)
				}
				notify("item/completed", map[string]any{"threadId": thread, "turnId": turn, "item": map[string]any{"id": "public-change", "type": "fileChange", "changes": []any{map[string]string{"path": "code/public.go"}}}})
			}
			completed("completed")
		case "turn/interrupt":
			response(request.ID, map[string]any{})
			completed("interrupted")
		default:
			os.Exit(90)
		}
	}
}

func nativeWorkerFixture(t *testing.T) (*Evaluation, workerRequest, nativeWorker) {
	t.Helper()
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	layout := codexnative.IsolationPaths{Workspace: filepath.Join(base, "packet"), Scratch: filepath.Join(base, "scratch"), MainCheckout: filepath.Join(base, "main"), ControllerState: filepath.Join(base, "controller"), SiblingWorkspaces: []string{filepath.Join(base, "earlier-private-evidence")}, CredentialPaths: []string{os.Getenv("CODEX_HOME")}}
	for _, dir := range append([]string{layout.Workspace, layout.Scratch, layout.MainCheckout, layout.ControllerState}, layout.SiblingWorkspaces...) {
		if err := os.Mkdir(dir, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	profileBytes, err := os.ReadFile("../config/testdata/valid/full.toml")
	if err != nil {
		t.Fatal(err)
	}
	cfg, err := config.Parse(profileBytes)
	if err != nil {
		t.Fatal(err)
	}
	profiles, err := configuration(cfg)
	if err != nil {
		t.Fatal(err)
	}
	instructions, protected := []Artifact{}, layout.SiblingWorkspaces
	e := &Evaluation{ID: "eval-aaaaaaaaaaaaaaaaaaaaaaaaaa", Preparation: Record{PlanDigest: "sha256:" + strings.Repeat("a", 64), Plan: Plan{Version: 2, Instructions: &instructions, ProtectedRoots: &protected,
		Baseline: PinnedSelection{Selection: Selection{OrchRevision: strings.Repeat("a", 40), Profile: Artifact{SHA256: evalcorpus.Digest(profileBytes)}}, Configuration: profiles},
		Cases:    []PublicCase{{Role: "scout"}, {Role: "implementation"}, {Role: "review"}}}}}
	unit := Unit{Ordinal: 1, CaseID: "public-case", CaseVersion: 1, Repetition: 1, Side: "baseline"}
	source := caseSource{definition: evalcorpus.Case{Role: "scout"}, public: map[string][]byte{"TASK.md": []byte("Declared synthetic public task."), "ROLE.md": []byte("Declared synthetic public role."), "code/public.go": []byte("package packet\n")}}
	for name, bytes := range source.public {
		writeFixture(t, filepath.Join(layout.Workspace, filepath.FromSlash(name)), bytes)
	}
	writeFixture(t, filepath.Join(layout.SiblingWorkspaces[0], "control.txt"), []byte(privateSentinel))
	record := AttemptRecord{Unit: unit, Number: 1, Kind: "initial", CaseSHA256: strings.Repeat("b", 64), PacketSHA256: strings.Repeat("c", 64)}
	task, err := evaluationTask(e, record, source, layout)
	if err != nil {
		t.Fatal(err)
	}
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	request := workerRequest{Unit: unit, Role: source.definition.Role, Layout: layout, Task: task, PlanVersion: 2, Intervention: "none", Revisions: []string{e.Preparation.Plan.Baseline.OrchRevision}}
	return e, request, nativeWorker{clientVersion: "fixture-evaluation:complete", executable: executable, revision: e.Preparation.Plan.Baseline.OrchRevision}
}

func TestNativeEvaluationControllerBindingAndRejections(t *testing.T) {
	if controllerTestProcess(t) {
		return
	}
	home, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("CODEX_HOME", home)
	e, request, executor := nativeWorkerFixture(t)
	for _, c := range e.Preparation.Plan.Cases {
		role, _, profile, err := evaluationProfile(e.Preparation.Plan, Unit{Side: "baseline"}, c.Role)
		wantRole, wantModel, wantEffort := "scout", "gpt-5.6-terra", "low"
		if c.Role == "implementation" {
			wantRole, wantEffort = "implementer", "high"
		}
		if c.Role == "review" {
			wantRole, wantModel, wantEffort = "reviewer", "gpt-5.6-sol", "medium"
		}
		if err != nil || role != wantRole || profile.Model != wantModel || profile.Effort != wantEffort {
			t.Fatal("public role/profile mapping changed")
		}
	}
	for _, change := range []func(*workerRequest){
		func(r *workerRequest) { r.Unit.CaseVersion++ },
		func(r *workerRequest) { r.Role = "review" },
		func(r *workerRequest) { r.Task.Selection.Effort = "high" },
		func(r *workerRequest) { r.Revisions = append(r.Revisions, strings.Repeat("0", 40)) },
		func(r *workerRequest) { r.Intervention = "orch-revision" },
	} {
		bad := request
		change(&bad)
		result, _ := executor.execute(t.Context(), bad)
		if result.Outcome != "refused" && result.Outcome != "safety-failure" {
			t.Fatalf("mismatched binding admitted: %+v", result)
		}
	}
	if runtime.GOOS != "windows" {
		t.Log("production platform gate refuses; native scripted session evidence is separately tested on every OS")
		return
	}
	for _, scenario := range []string{"complete", "profile", "protection", "external-protection", "managed", "skills", "private-source", "wait"} {
		executor.clientVersion = "fixture-evaluation:" + scenario
		ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
		if scenario == "wait" {
			cancel()
			ctx, cancel = context.WithTimeout(t.Context(), time.Second)
		}
		result, err := executor.execute(ctx, request)
		cancel()
		if scenario == "complete" {
			if err != nil || result.Outcome != "native-completed" || result.Native == nil || result.Native.Instructions.Status != "verified-sources-and-hashes" || len(result.Native.Observations) != 3 || result.Eligibility == nil || !result.Eligibility.ModelToolsVerified {
				t.Fatalf("native integration: %+v %v", result, err)
			}
		} else if scenario == "wait" {
			if result.Outcome != "timeout" || result.Native.Cleanup.InterruptAcknowledged == nil || !*result.Native.Cleanup.InterruptAcknowledged {
				t.Fatalf("native interruption evidence: %+v %v", result, err)
			}
		} else if result.Outcome != "refused" && result.Outcome != "safety-failure" {
			t.Fatalf("native mismatch %s admitted: %+v %v", scenario, result, err)
		}
	}
}

func TestNativeEvaluationControllerRetainsExecution(t *testing.T) {
	if controllerTestProcess(t) {
		return
	}
	home, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("CODEX_HOME", home)
	f := newControllerFixture(t, "screen", 1)
	f.proposal.Version = 2
	instructionPath := filepath.Join(os.Getenv("CODEX_HOME"), "AGENTS.md")
	instructionBytes := []byte("Generic approved collaboration instructions; no corpus answers.")
	writeFixture(t, instructionPath, instructionBytes)
	sources := []Artifact{{Path: instructionPath, SHA256: evalcorpus.Digest(instructionBytes)}}
	f.proposal.Instructions = &sources
	protected := []string{}
	f.proposal.ProtectedRoots = &protected
	f.preview(t)
	e, err := PrepareApproved(t.Context(), f.repo, f.root, f.record.PlanDigest, Approval{1, f.record.PlanDigest, "test-human", now(), ApprovalStatement})
	if err != nil {
		t.Fatal(err)
	}
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	unapproved := f.prepare(t)
	if _, err := run(t.Context(), f.root, unapproved.ID, nativeWorker{clientVersion: "fixture-evaluation:complete", executable: executable, revision: e.Preparation.Plan.Baseline.OrchRevision}); err == nil {
		t.Fatal("native controller bypassed approval")
	}
	p, err := run(t.Context(), f.root, e.ID, nativeWorker{clientVersion: "fixture-evaluation:complete", executable: executable, revision: e.Preparation.Plan.Baseline.OrchRevision})
	if err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" {
		if p.State != "refused" || p.Slots[1].Status != "unrun" {
			t.Fatal("unsupported platform executed native schedule")
		}
		return
	}
	if p.State != "completed" {
		t.Fatalf("native frozen schedule did not finish: %+v", p)
	}
	r, err := RetainReport(f.root, e.ID)
	if err != nil {
		t.Fatal(err)
	}
	if r.SchemaVersion != 3 || r.Snapshot.CompletedAttempts != 4 || r.Snapshot.UnrunUnits != 0 {
		t.Fatal("completion lost schedule evidence")
	}
	for _, coverage := range r.Snapshot.Grading.Coverage {
		if coverage.Accepted != 0 {
			t.Fatal("native completion invented semantic acceptance")
		}
	}
	for _, a := range r.Snapshot.Attempts {
		if a.Native == nil || a.Native.SchemaVersion != 2 || a.Native.Binding.Identity.ID != e.ID || a.Native.Binding.Identity.PlanDigest != f.record.PlanDigest || a.Grade != "unknown" || a.ExecutionSource != "codex-native-evaluation" || a.Cleanup.Status != "preserved-dirty" || a.Cleanup.NativeAcknowledged == nil || !*a.Cleanup.NativeAcknowledged {
			t.Fatalf("retained native evidence: %+v", a)
		}
		if a.Native.Binding.Identity.Role == "implementer" {
			if data, err := os.ReadFile(filepath.Join(a.Native.Binding.Workspace, "code", "public.go")); err != nil || !strings.Contains(string(data), "changed synthetic task") {
				t.Fatal("dirty implementation output was removed")
			}
		}
	}
	for _, v := range r.Snapshot.Values {
		if v.Counter == "input_tokens" && v.Value != 30 || v.Counter == "output_tokens" && v.Value != 0 || v.Counter != "input_tokens" && v.Counter != "output_tokens" {
			t.Fatalf("native delta/presence semantics lost: %+v", v)
		}
	}
	before := r.SnapshotSHA256
	if again, err := RetainReport(f.root, e.ID); err != nil || again.SnapshotSHA256 != before {
		t.Fatal("immutable report changed")
	}
	if _, err := run(t.Context(), f.root, e.ID, nativeWorker{}); err == nil {
		t.Fatal("consumed native schedule replayed")
	}
	t.Log("All output is scripted native RPC evidence; no subscription inference, semantic pass or measured baseline.")
}

func TestNativeEvaluationEvidenceVersionBinding(t *testing.T) {
	e := &Evaluation{Preparation: Record{Plan: Plan{Version: 2}}}
	legacy := &NativeEvidence{Observations: []metrics.Observation{{SchemaVersion: 2, RunID: "run-retained-fixture", ID: "legacy",
		At: now(), Source: "fixture", Host: "codex", Unavailable: &metrics.Unavailable{Reason: "legacy"}}}}
	for _, test := range []struct {
		name   string
		native *NativeEvidence
	}{{"missing-native-binding", nil}, {"legacy-Delivery-observation", legacy}} {
		t.Run(test.name, func(t *testing.T) {
			a := AttemptRecord{SchemaVersion: 2, ExecutionSource: "codex-native-evaluation", Outcome: "native-completed", Native: test.native, Initial: []DigestedFile{}, Artifacts: []DigestedFile{}}
			var retained AttemptRecord
			if err := strictStored(fixtureJSON(t, a), &retained); err != nil {
				t.Fatal(err)
			}
			if err := validateAttemptNative(&retained, e); err == nil {
				t.Fatal("schema-2 native completion accepted without versioned evaluation binding")
			}
		})
	}
	e.Preparation.Plan.Version = 1
	a := AttemptRecord{SchemaVersion: 1, ExecutionSource: "no-model-test-script", Outcome: "native-completed", Native: legacy}
	if err := validateAttemptNative(&a, e); err != nil {
		t.Fatalf("legacy retained evidence became unreadable: %v", err)
	}
}
