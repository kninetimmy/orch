package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/kninetimmy/orch/internal/execx"
	"github.com/kninetimmy/orch/internal/execx/execxtest"
	"github.com/kninetimmy/orch/internal/paths"
	"github.com/kninetimmy/orch/internal/run"
	"github.com/kninetimmy/orch/internal/state"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const mcpTestSession = "ses_fixture"
const mcpGitHubTargetFields = "nameWithOwner,defaultBranchRef,url,id"
const mcpGitHubRepoID = "R_kgDOAAAABQ"
const mcpReplacementRepoID = "R_kgDOAAAABq"
const mcpRelocatedRepo = "github.com/fixture/relocated"
const mcpGitHubRepoReply = `{"nameWithOwner":"fixture/isolated","defaultBranchRef":{"name":"main"},"url":"https://github.com/fixture/isolated","id":"` + mcpGitHubRepoID + `"}`

func mcpWorkspaceMeta() mcp.Meta {
	return mcp.Meta{openCodeSessionMeta: mcpTestSession}
}

// A real loopback exchange through the production session reader. pid is the
// actual parent expected by the Orch subprocess (or the in-process test runner).
func mcpWorkspaceHost(t *testing.T, pid int, session func(*http.Request) (int, any)) string {
	t.Helper()
	const password = "loopback-fixture-only"
	host := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		username, got, ok := r.BasicAuth()
		if !ok || username != "opencode" || got != password || r.Method != http.MethodGet || r.URL.RawQuery != "" {
			t.Error("workspace lookup did not use an authenticated, unscoped GET")
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		if r.URL.Path == "/api/info" {
			_ = json.NewEncoder(w).Encode(map[string]any{"pid": pid})
			return
		}
		status, data := session(r)
		w.WriteHeader(status)
		_ = json.NewEncoder(w).Encode(data)
	}))
	t.Cleanup(host.Close)
	dir := t.TempDir()
	mcpWrite(t, filepath.Join(dir, "service.json"), mcpEvalBytes(t, map[string]any{"pid": pid, "url": host.URL, "password": password}))
	return dir
}

func mcpWorkspaceReply(id, directory, echo string) map[string]any {
	return map[string]any{"location": map[string]any{"directory": echo},
		"data": map[string]any{"id": id, "location": map[string]any{"directory": directory}}}
}

func mcpMatchingHost(t *testing.T, pid int, directory string) string {
	t.Helper()
	return mcpWorkspaceHost(t, pid, func(r *http.Request) (int, any) {
		if r.URL.Path != "/api/session/"+mcpTestSession {
			t.Errorf("workspace lookup used unexpected path: %s", r.URL.Path)
			return http.StatusNotFound, nil
		}
		return http.StatusOK, mcpWorkspaceReply(mcpTestSession, directory, "")
	})
}

func mcpWorkspaceCalls(root, stateDir string) []execxtest.Call {
	return []execxtest.Call{
		{Name: "opencode2", Args: []string{"--version"}, Dir: root, Stdout: "opencode v2.2.6"},
		{Name: "opencode2", Args: []string{"debug", "paths", "state"}, Dir: root, Stdout: stateDir},
	}
}

func mcpPrimaryCalls(root string) []execxtest.Call {
	return []execxtest.Call{
		{Name: "git", Args: []string{"rev-parse", "--show-toplevel"}, Dir: root, Stdout: root},
		{Name: "git", Args: []string{"worktree", "list", "--porcelain"}, Dir: root, Stdout: "worktree " + root + "\n"},
	}
}

func mcpTargetCalls(root string) []execxtest.Call {
	return append(mcpPrimaryCalls(root),
		execxtest.Call{Name: "git", Args: []string{"rev-parse", "--path-format=absolute", "--git-common-dir"}, Dir: root, Stdout: filepath.Join(root, ".orchestrator")},
		execxtest.Call{Name: "gh", Args: []string{"auth", "status"}, Dir: root},
		execxtest.Call{Name: "gh", Args: []string{"repo", "view", "--json", mcpGitHubTargetFields}, Dir: root, Stdout: mcpGitHubRepoReply})
}

func mcpCanonical(t *testing.T, root string) string {
	t.Helper()
	dir, err := paths.Canonical(root)
	if err != nil {
		t.Fatal(err)
	}
	return dir
}

func mcpFixtureGate(t *testing.T, root string, plan []byte, extra ...execxtest.Call) *run.GateDoc {
	t.Helper()
	root = mcpCanonical(t, root)
	script := &execxtest.Script{T: t, Calls: append(mcpTargetCalls(root), extra...)}
	gate, err := run.Plan(t.Context(), run.Env{RepoRoot: root, Runner: script}, plan)
	if err != nil {
		t.Fatal(err)
	}
	script.AssertExhausted()
	return gate
}

func mcpProbeBin(t *testing.T, names ...string) string {
	t.Helper()
	bin := t.TempDir()
	suffix := ""
	if runtime.GOOS == "windows" {
		suffix = ".exe"
	}
	probe := filepath.Join(bin, names[0]+suffix)
	build := exec.CommandContext(t.Context(), "go", "build", "-o", probe, "./testdata/doctor-probe")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build isolated command probe: %v: %s", err, out)
	}
	data, err := os.ReadFile(probe)
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range names[1:] {
		if err := os.WriteFile(filepath.Join(bin, name+suffix), data, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	return bin
}

func mcpProbeFile(t *testing.T, root, log string, calls []execxtest.Call) string {
	t.Helper()
	responses := map[string]any{}
	for _, call := range calls {
		responses[call.Name+" "+strings.Join(call.Args, " ")] = struct {
			execx.Result
			Dir string
		}{execx.Result{Stdout: call.Stdout, Stderr: call.Stderr, ExitCode: call.Exit}, call.Dir}
	}
	file := filepath.Join(t.TempDir(), "commands.json")
	mcpWrite(t, file, mcpEvalBytes(t, map[string]any{"Root": root, "Log": log, "Probes": responses}))
	return file
}

func mcpProbeLog(calls []execxtest.Call) string {
	var log strings.Builder
	for _, call := range calls {
		fmt.Fprintf(&log, "%s\t%s %s\n", call.Dir, call.Name, strings.Join(call.Args, " "))
	}
	return log.String()
}

func mcpReadProbeLog(t *testing.T, file string) []byte {
	t.Helper()
	data, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func mcpOnlyProbes(t *testing.T, log []byte, allowed []execxtest.Call) {
	t.Helper()
	want := strings.Split(strings.TrimSuffix(mcpProbeLog(allowed), "\n"), "\n")
	for line := range strings.SplitSeq(strings.TrimSuffix(string(log), "\n"), "\n") {
		if line != "" && !slices.Contains(want, line) {
			t.Fatalf("refusal or inspection ran an unexpected command: %s", line)
		}
	}
}

func mcpInProcess(t *testing.T, env Env, args ...string) (*mcp.ClientSession, func()) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), time.Minute)
	input, writer := io.Pipe()
	reader, output := io.Pipe()
	env.Stdin, env.Stdout = input, output
	done := make(chan int, 1)
	go func() { done <- Run(append([]string{"mcp"}, args...), env) }()
	client := mcp.NewClient(&mcp.Implementation{Name: "workspace-test", Version: "1"}, nil)
	session, err := client.Connect(ctx, &mcp.IOTransport{Reader: reader, Writer: writer}, nil)
	if err != nil {
		cancel()
		_ = writer.Close()
		_ = output.Close()
		t.Fatal(err)
	}
	closeSession := func() {
		t.Helper()
		if err := session.Close(); err != nil {
			t.Error(err)
		}
		cancel()
		if code := <-done; code != ExitOK {
			t.Errorf("server exit %d", code)
		}
	}
	return session, closeSession
}

// The fake GitHub backend keeps stable IDs and mutable selectors separate. A
// repo can lose its old selector between a read and a mutation, even if that
// mutation supplied an explicit --repo. Git uses disposable local repos.
type workspaceDeliveryRunner struct {
	stateDir, target, switchAfterView string
	repoID                            any // nil omits the field; nonstrings simulate malformed GitHub output.
	githubReads                       int
	writes                            map[string][]string // by stable repo ID
	reassignOnWrite, reassigned       bool
	rejectLabel                       bool
	mutableActivationWrites           int
}

func (r *workspaceDeliveryRunner) beforeWrite() {
	if r.reassignOnWrite {
		r.reassignOnWrite, r.reassigned = false, true
	}
}

func (r *workspaceDeliveryRunner) selectorID(selector string) string {
	switch selector {
	case mcpActivationRepo:
		if r.reassigned {
			return mcpReplacementRepoID
		}
		return mcpGitHubRepoID
	case mcpRelocatedRepo:
		return mcpGitHubRepoID
	default:
		return selector
	}
}

func (r *workspaceDeliveryRunner) graphQL(cmd execx.Cmd) (execx.Result, error) {
	if !slices.Equal(cmd.Args, mcpActivationGraphQLArgs()) || cmd.Stdin == nil {
		return execx.Result{}, fmt.Errorf("unexpected activation GraphQL command %q", cmd.Args)
	}
	var req struct {
		OperationName, Query string
		Variables            struct {
			RepositoryID string
			Input        struct {
				RepositoryID, Name, Color, Description, Title, Body string
				LabelIDs                                            []string
			}
		}
	}
	if err := json.NewDecoder(cmd.Stdin).Decode(&req); err != nil {
		return execx.Result{}, err
	}
	input := req.Variables.Input
	switch req.OperationName {
	case "OrchActivationLabels":
		r.githubReads++
		if req.Variables.RepositoryID != mcpGitHubRepoID || !strings.Contains(req.Query, "node(id: $repositoryId)") ||
			!strings.Contains(req.Query, "... on Repository") {
			break
		}
		return execx.Result{Stdout: mcpActivationLabelPage(req.Variables.RepositoryID)}, nil
	case "OrchCreateLabel":
		r.beforeWrite()
		if input.RepositoryID != mcpGitHubRepoID || !strings.Contains(req.Query, "createLabel(input: $input)") ||
			input.Name != "ready" || input.Color != "1D76DB" || !strings.Contains(input.Description, "PRD §13") {
			break
		}
		if r.rejectLabel {
			return execx.Result{ExitCode: 1, Stderr: "Cannot query field createLabel on type Mutation"}, nil
		}
		r.writes[input.RepositoryID] = append(r.writes[input.RepositoryID], "createLabel")
		return execx.Result{Stdout: mcpActivationLabelReply}, nil
	case "OrchCreateIssue":
		r.beforeWrite()
		if input.RepositoryID != mcpGitHubRepoID || !strings.Contains(req.Query, "createIssue(input: $input)") ||
			input.Title != "Fix the status lock race" || !strings.Contains(input.Body, "**Objective**") ||
			!slices.Equal(input.LabelIDs, []string{"LA_ready", "LA_bug", "LA_implementer", "LA_standard"}) {
			break
		}
		r.writes[input.RepositoryID] = append(r.writes[input.RepositoryID], "createIssue")
		repo := mcpActivationRepo
		if r.reassigned {
			repo = mcpRelocatedRepo
		}
		return execx.Result{Stdout: mcpActivationIssueReply(repo, input.RepositoryID)}, nil
	}
	return execx.Result{}, fmt.Errorf("unexpected activation GraphQL request %q", req.OperationName)
}

func (r *workspaceDeliveryRunner) Run(ctx context.Context, cmd execx.Cmd) (execx.Result, error) {
	switch cmd.Name {
	case "git":
		return (execx.Local{}).Run(ctx, cmd)
	case "opencode2":
		if slices.Equal(cmd.Args, []string{"debug", "paths", "state"}) {
			return execx.Result{Stdout: r.stateDir}, nil
		}
		return (fakeRunner{opencodeVersion: "opencode v2.2.6"}).Run(ctx, cmd)
	case "gh":
		switch strings.Join(cmd.Args, " ") {
		case "auth status":
			r.githubReads++
			return execx.Result{}, nil
		case "repo view --json nameWithOwner,defaultBranchRef,url", "repo view --json " + mcpGitHubTargetFields:
			r.githubReads++
			target := r.target
			if r.switchAfterView != "" {
				r.target, r.switchAfterView = r.switchAfterView, ""
			}
			_, slug, _ := strings.Cut(target, "/")
			repo := map[string]any{"nameWithOwner": slug, "defaultBranchRef": map[string]string{"name": "main"}, "url": "https://" + target}
			if cmd.Args[3] == mcpGitHubTargetFields && r.repoID != nil {
				repo["id"] = r.repoID
				if r.reassigned && target == mcpActivationRepo {
					repo["id"] = mcpReplacementRepoID
				}
			}
			data, err := json.Marshal(repo)
			return execx.Result{Stdout: string(data)}, err
		}
		target, args := r.target, cmd.Args
		if len(args) >= 4 && args[len(args)-2] == "--repo" {
			target, args = args[len(args)-1], args[:len(args)-2]
		}
		command := strings.Join(args[:2], " ")
		switch command {
		case "api graphql":
			return r.graphQL(cmd)
		case "label list":
			r.githubReads++
			return execx.Result{Stdout: `[{"name":"in-progress"},{"name":"blocked"},{"name":"needs-human"},{"name":"awaiting-review"},{"name":"delivered"},{"name":"feature"},{"name":"bug"},{"name":"chore"},{"name":"infra"},{"name":"docs"},{"name":"research"},{"name":"implementer"},{"name":"specialist"},{"name":"standard"},{"name":"critical"}]`}, nil
		case "label create", "issue create":
			r.beforeWrite()
			r.mutableActivationWrites++
			id := r.selectorID(target)
			r.writes[id] = append(r.writes[id], command)
			return execx.Result{Stdout: "https://" + target + "/issues/71"}, nil
		case "issue edit":
			id := r.selectorID(target)
			r.writes[id] = append(r.writes[id], command)
			return execx.Result{}, nil
		}
	}
	return execx.Result{}, fmt.Errorf("unexpected isolated workspace command %s %q", cmd.Name, cmd.Args)
}

func TestMCPDeliveryWorkspaceAndTarget(t *testing.T) {
	const approvedRepo = mcpActivationRepo
	const alternate = "github.example.invalid/other/repo"
	parent := t.TempDir()
	global := filepath.Join(parent, "gitconfig")
	mcpWrite(t, global, []byte("[user]\nname = Fixture\nemail = fixture@example.invalid\n[core]\nautocrlf = false\n"))
	t.Setenv("GIT_CONFIG_GLOBAL", global)
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	t.Setenv("GIT_ALLOW_PROTOCOL", "file")
	// Keep git status's optional index-cache refresh out of byte snapshots.
	t.Setenv("GIT_OPTIONAL_LOCKS", "0")
	root, other, origin := mcpCanonical(t, t.TempDir()), mcpCanonical(t, t.TempDir()), filepath.Join(parent, "origin.git")
	lookupTestGit(t, root, "init", "-b", "main")
	lookupTestGit(t, parent, "init", "--bare", "-b", "main", origin)
	writeConfig(t, root, validOpenCodeTOML)
	mcpWrite(t, filepath.Join(root, ".gitignore"), []byte(".orchestrator/state.json\n.orchestrator/delivery.lock\n.orchestrator/worktrees/\n.opencode/agents/\n"))
	lookupTestGit(t, root, "add", ".")
	lookupTestGit(t, root, "commit", "-m", "Seed identical checkouts")
	lookupTestGit(t, root, "remote", "add", "origin", origin)
	lookupTestGit(t, root, "push", "origin", "main")
	lookupTestGit(t, parent, "clone", origin, other)
	if err := runRenderAgents(Env{RepoRoot: other, Runner: execx.Local{}, Stdout: io.Discard}); err != nil {
		t.Fatal(err)
	}
	linked := filepath.Join(parent, "linked")
	lookupTestGit(t, root, "worktree", "add", "--detach", linked, "HEAD")
	if lookupTestGit(t, root, "rev-parse", "HEAD") != lookupTestGit(t, other, "rev-parse", "HEAD") {
		t.Fatal("fixtures must share a revision")
	}

	type hostReply struct {
		status int
		data   any
	}
	var response atomic.Pointer[hostReply]
	locate := func(dir string) {
		response.Store(&hostReply{http.StatusOK, mcpWorkspaceReply(mcpTestSession, dir, root)})
	}
	locate(root)
	stateDir := mcpWorkspaceHost(t, os.Getppid(), func(r *http.Request) (int, any) {
		if r.URL.Path != "/api/session/"+mcpTestSession {
			return http.StatusNotFound, nil
		}
		reply := response.Load()
		return reply.status, reply.data
	})
	runner := &workspaceDeliveryRunner{stateDir: stateDir, target: approvedRepo, repoID: mcpGitHubRepoID, writes: map[string][]string{}}
	start := func(bound string) *mcp.ClientSession {
		t.Helper()
		session, close := mcpInProcess(t, Env{RepoRoot: other, Runner: runner, Stderr: io.Discard}, "--repo", bound)
		t.Cleanup(close)
		return session
	}
	mcpA, mcpB := start(root), start(other)
	ctx, cancel := context.WithTimeout(t.Context(), time.Minute)
	defer cancel()
	call := func(s *mcp.ClientSession, name string, args any, meta mcp.Meta) *mcp.CallToolResult {
		t.Helper()
		res, err := s.CallTool(ctx, &mcp.CallToolParams{Meta: meta, Name: name, Arguments: args})
		if err != nil {
			t.Fatal(err)
		}
		return res
	}
	snapshot := func() [4]string {
		return [4]string{mcpTree(t, root), mcpTree(t, other), mcpTree(t, linked), string(mcpEvalBytes(t, runner.writes))}
	}
	refuse := func(s *mcp.ClientSession, name string, args any, meta mcp.Meta, reason string, githubReads int) {
		t.Helper()
		before, reads := snapshot(), runner.githubReads
		mcpEvalError(t, call(s, name, args, meta), reason)
		after := snapshot()
		if after != before || runner.githubReads-reads != githubReads {
			t.Fatalf("%s refusal: primary changed=%t other=%t linked=%t GitHub writes=%t reads=%d want %d",
				name, after[0] != before[0], after[1] != before[1], after[2] != before[2], after[3] != before[3], runner.githubReads-reads, githubReads)
		}
	}

	plan := mustMCPActivationPlan(t)
	plan.Host = "opencode"
	meta := mcpWorkspaceMeta()
	gateArgs := map[string]any{"plan": plan}
	gate := func(s *mcp.ClientSession, repoID string) run.GateDoc {
		t.Helper()
		res := call(s, "orch_plan_gate", gateArgs, meta)
		var doc run.GateDoc
		if res.IsError || json.Unmarshal(mcpEvalBytes(t, res.StructuredContent), &doc) != nil || doc.SchemaVersion != run.GateSchemaVersion ||
			doc.Target.GitHub.String() != approvedRepo || doc.Target.GitHub.ID != repoID || doc.Target.CheckoutID == "" {
			t.Fatalf("gate omitted verified approval target: %+v", res)
		}
		return doc
	}
	before := snapshot()
	a := gate(mcpA, mcpGitHubRepoID)
	locate(other)
	b := gate(mcpB, mcpGitHubRepoID)
	if a.Target.Checkout != root || b.Target.Checkout != other || a.Target.CheckoutID == b.Target.CheckoutID ||
		a.PlanDigest == b.PlanDigest || a.Execution != b.Execution || snapshot() != before {
		t.Fatal("identical plan/HEAD/config in distinct primary checkouts reused approval or changed state")
	}
	request := json.RawMessage(mcpApprovalForGate(t, &a, plan))
	dispatch := json.RawMessage(mcpDispatchRequest)
	all := func(s *mcp.ClientSession, metadata mcp.Meta, reason string) {
		t.Helper()
		refuse(s, "orch_plan_gate", gateArgs, metadata, reason, 0)
		refuse(s, "orch_run_activate", request, metadata, reason, 0)
		refuse(s, "orch_run_dispatch", dispatch, metadata, reason, 0)
	}
	all(mcpA, meta, "does not match the bound checkout") // Host echoes A, reports B.
	refuse(mcpB, "orch_run_activate", request, meta, "does not match the approved checkout", 0)
	retagged := mcpDecoded(t, request).(map[string]any)
	retagged["approval"].(map[string]any)["target"] = b.Target
	refuse(mcpB, "orch_run_activate", retagged, meta, "does not match the recomputed digest", 0)
	locate(linked)
	all(mcpA, meta, "does not match the bound checkout")
	// Binding the server itself to the linked checkout is not a way around the
	// primary check. Supply its location independently (matching the server).
	all(start(linked), meta, "not the primary checkout")
	locate(root)
	all(mcpA, nil, "missing or invalid session metadata")
	all(mcpA, mcp.Meta{openCodeSessionMeta: "ses_fixture/extra"}, "missing or invalid session metadata")
	locate(filepath.Join(parent, "missing"))
	all(mcpA, meta, "cannot be verified as an existing directory")
	response.Store(&hostReply{http.StatusOK, map[string]any{"location": map[string]any{"directory": root}}})
	all(mcpA, meta, "did not return the invoking session's local directory")
	response.Store(&hostReply{http.StatusOK, mcpWorkspaceReply("ses_other", root, root)})
	all(mcpA, meta, "did not return the invoking session's local directory")
	response.Store(&hostReply{http.StatusServiceUnavailable, nil})
	all(mcpA, meta, "did not return the invoking session's local directory")
	locate(root)
	registration := filepath.Join(stateDir, "service.json")
	record := mcpReadProbeLog(t, registration)
	foreign := mcpDecoded(t, record).(map[string]any)
	foreign["pid"] = os.Getppid() + 1
	mcpWrite(t, registration, mcpEvalBytes(t, foreign))
	all(mcpA, meta, "registration is not for the OpenCode process")
	mcpWrite(t, registration, record)

	for _, tc := range []struct {
		name, reason string
		id           any
	}{
		{"missing", "missing or malformed GitHub repository ID", nil},
		{"malformed token", "missing or malformed GitHub repository ID", "invalid ID"},
		{"wrong JSON type", "returned unparsable JSON", 42},
	} {
		runner.repoID = tc.id
		refuse(mcpA, "orch_plan_gate", gateArgs, meta, tc.reason, 2)
		refuse(mcpA, "orch_run_activate", request, meta, tc.reason, 2)
		t.Logf("%s GitHub repository ID: no gate, no activation, no effects", tc.name)
	}
	const replacementID = mcpReplacementRepoID
	runner.repoID = replacementID
	replacement := gate(mcpA, replacementID)
	sameTarget := replacement.Target
	sameTarget.GitHub.ID = mcpGitHubRepoID
	if sameTarget != a.Target || replacement.Execution != a.Execution || replacement.PlanDigest == a.PlanDigest || snapshot() != before {
		t.Fatal("changing only the repository ID reused approval, changed other target evidence, or mutated state")
	}
	refuse(mcpA, "orch_run_activate", request, meta, `live repository ID "`+replacementID+`", approved ID "`+mcpGitHubRepoID+`"`, 2)
	retagged["approval"].(map[string]any)["target"] = replacement.Target
	refuse(mcpA, "orch_run_activate", retagged, meta, "does not match the recomputed digest", 0)
	t.Log("same host/owner/name with different repository ID: new digest; old approval and edited echo refused without effects")
	runner.repoID = mcpGitHubRepoID

	for _, target := range []string{"github.example.invalid/fixture/isolated", "github.com/other/isolated", "github.com/fixture/renamed"} {
		runner.target = target
		refuse(mcpA, "orch_run_activate", request, meta, "live GitHub target "+target+" does not match the approved target", 2)
	}
	// Even if the host doesn't support the ID-addressed API, do not fall
	// back to the old selector: it is reassigned at the first mutation.
	runner.target, runner.switchAfterView = approvedRepo, alternate
	runner.reassignOnWrite, runner.rejectLabel = true, true
	refuse(mcpA, "orch_run_activate", request, meta, "Cannot query field createLabel", 3)
	if !runner.reassigned || runner.mutableActivationWrites != 0 {
		t.Fatal("missing ID write capability was not exercised or attempted a mutable fallback")
	}
	t.Log("ID mutation unavailable after reassignment: explicit failure, no fallback or effects")

	// The default changes after resolution and the checked name is reassigned
	// at the first write, after any preflight. Both mutations must go to the
	// original ID, now at another name.
	runner.target, runner.switchAfterView = approvedRepo, alternate
	runner.reassignOnWrite, runner.reassigned, runner.rejectLabel = true, false, false
	result := call(mcpA, "orch_run_activate", request, meta)
	var activated run.ActivationResult
	if result.IsError || json.Unmarshal(mcpEvalBytes(t, result.StructuredContent), &activated) != nil || activated.SchemaVersion != run.ActivationSchemaVersion ||
		len(activated.Issues) != 1 || activated.Issues[0].Number != 71 || activated.Issues[0].URL != "https://"+mcpRelocatedRepo+"/issues/71" ||
		!runner.reassigned || runner.selectorID(approvedRepo) != replacementID || runner.mutableActivationWrites != 0 ||
		!slices.Equal(runner.writes[mcpGitHubRepoID], []string{"createLabel", "createIssue"}) || len(runner.writes) != 1 ||
		mcpTree(t, other) != before[1] || mcpTree(t, linked) != before[2] {
		t.Fatalf("activation did not land exclusively on the checked target: %+v; GitHub=%v", result, runner.writes)
	}
	t.Log("checked name reassigned at write boundary: label and issue written only to the original ID")
	st, err := state.Load(root)
	if err != nil || st.Mode != state.ModeDelivery || st.Run.Plan.Digest != a.PlanDigest || st.Run.Plan.GitHubRepositoryID != mcpGitHubRepoID ||
		st.Run.Issues[0].Phase != state.PhaseWorktreeReady {
		t.Fatalf("activation state: %+v, %v", st, err)
	}
	worktree := filepath.Join(root, filepath.FromSlash(activated.Issues[0].Worktree))
	if got := lookupTestGit(t, worktree, "branch", "--show-current"); got != activated.Issues[0].Branch {
		t.Fatalf("activation did not create the approved worktree: %s", got)
	}

	runner.target, runner.reassigned = approvedRepo, false // Restore the matching dispatch fixture.
	locate(other)
	refuse(mcpA, "orch_run_dispatch", dispatch, meta, "does not match the bound checkout", 0)
	locate(worktree)
	refuse(mcpA, "orch_run_dispatch", dispatch, meta, "does not match the bound checkout", 0)
	locate(root + string(filepath.Separator) + ".")
	refuse(mcpA, "orch_run_dispatch", dispatch, nil, "missing or invalid session metadata", 0)
	result = call(mcpA, "orch_run_dispatch", dispatch, meta)
	st, err = state.Load(root)
	if result.IsError || err != nil || st.Run.Issues[0].Phase != state.PhaseDispatched ||
		!slices.Equal(runner.writes[mcpGitHubRepoID], []string{"createLabel", "createIssue", "issue edit"}) ||
		len(runner.writes) != 1 || mcpTree(t, other) != before[1] || mcpTree(t, linked) != before[2] {
		t.Fatalf("matching dispatch did not preserve its lifecycle: result=%+v state=%+v err=%v GitHub=%v", result, st, err, runner.writes)
	}
	t.Log("independent session evidence, exact checkout and GitHub approval binding, zero refusal mutations, pinned activation and real dispatch")
}
