package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/kninetimmy/orch/internal/evalcorpus"
	"github.com/kninetimmy/orch/internal/evalplan"
	"github.com/kninetimmy/orch/internal/execx"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func mcpPreviewFixture(t *testing.T) (*evalFixture, string) {
	t.Helper()
	f := newEvalFixture(t)
	external := f.file
	// The proposal alone is confined to the repository. Existing preview rules
	// deliberately allow explicit absolute references to guarded local artifacts.
	for _, ref := range []*evalplan.Artifact{&f.plan.Corpus, &f.plan.Baseline.Profile, &f.plan.Candidate.Profile, &f.plan.DecisionRule,
		f.plan.Readiness.IndependentValidation, f.plan.Readiness.Exposure, f.plan.Readiness.NativeExecution} {
		if !filepath.IsAbs(ref.Path) {
			ref.Path = filepath.Join(filepath.Dir(external), ref.Path)
		}
	}
	f.file = filepath.Join(f.repo, "plans", "preview.json")
	if err := os.Mkdir(filepath.Dir(f.file), 0o700); err != nil {
		t.Fatal(err)
	}
	f.write(t)
	return f, external
}

// Inventory includes every existing entry's kind/mode, regular-file mtime and
// bytes. Comparing maps permits exactly one new digest filename, not arbitrary
// writes elsewhere in the storage directory or a hidden execution subtree.
func mcpPreviewInventory(t *testing.T, root string) map[string]string {
	t.Helper()
	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]string{}
	for _, entry := range entries {
		out[entry.Name()] = mcpTree(t, filepath.Join(root, entry.Name()))
	}
	return out
}

func mcpPreviewResult(t *testing.T, result *mcp.CallToolResult) *evalplan.Record {
	t.Helper()
	if result.IsError || len(result.Content) != 1 {
		t.Fatalf("preview result = %+v", result)
	}
	text, ok := result.Content[0].(*mcp.TextContent)
	if !ok {
		t.Fatalf("preview content = %T", result.Content[0])
	}
	var r evalplan.Record
	if err := json.Unmarshal([]byte(text.Text), &r); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(result.StructuredContent, mcpDecoded(t, []byte(text.Text))) || strings.Contains(text.Text, "PRIVATE_") || strings.Contains(text.Text, "WOULD_EXECUTE") {
		t.Fatal("preview structured/text parity or private-artifact projection failed")
	}
	if r.Kind != "maintainer-preparation-record" || r.Preview.ExecutionAvailable || r.Preview.ApprovalGranted ||
		len(r.Preview.Schedule) != 16 || r.Preview.Counts.Units != 16 || len(r.Preview.Checks) == 0 || len(r.Preview.ReadinessBlockers) == 0 ||
		r.PlanDigest != "sha256:"+evalcorpus.Digest(evalJSON(t, r.Plan)) {
		t.Fatalf("preparation evidence/schedule/digest/authority changed: %+v", r)
	}
	return &r
}

func mcpEvalPreviewExercise(t *testing.T, binary string) {
	t.Helper()
	f, externalPlan := mcpPreviewFixture(t)
	root, storage, parent, cwd := f.repo, f.plan.StorageRoot, filepath.Dir(f.repo), t.TempDir()
	writeConfig(t, cwd, "invalid unrelated launching configuration [")
	ctx, cancel := context.WithTimeout(t.Context(), 120*time.Second)
	defer cancel()
	input := map[string]any{"plan_file": "plans/preview.json"}
	for _, args := range [][]string{nil, {"--repo", root}} {
		session, closeSession := mcpConnect(t, ctx, binary, root, root, args...)
		before := mcpTree(t, parent)
		mcpEvalError(t, mcpCall(t, ctx, session, root, "orch_eval_preview", input), "--eval-storage-root")
		closeSession()
		if mcpTree(t, parent) != before {
			t.Fatal("unbound preview changed repository/storage")
		}
	}

	// Reuse the existing exact scripted-probe executable. Only Git is on PATH;
	// answers come from the fixture's local read probes, not a host or controller.
	probes := t.TempDir()
	gitBinary := filepath.Join(probes, "git")
	if runtime.GOOS == "windows" {
		gitBinary += ".exe"
	}
	build := exec.CommandContext(t.Context(), "go", "build", "-o", gitBinary, "./testdata/doctor-probe")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build preview Git probes: %v: %s", err, out)
	}
	responses := map[string]execx.Result{}
	prefix := []string{"--no-optional-locks", "--no-pager", "-c", "core.fsmonitor=false"}
	for _, args := range [][]string{
		{"cat-file", "-e", f.plan.Baseline.OrchRevision + "^{commit}"},
		{"rev-parse", "--path-format=absolute", "--show-toplevel", "--git-common-dir", "--git-dir"},
		{"worktree", "list", "--porcelain", "-z"},
	} {
		command := execx.Cmd{Name: "git", Args: append(slices.Clone(prefix), args...), Dir: root,
			Env: []string{"GIT_TERMINAL_PROMPT=0", "GIT_NO_REPLACE_OBJECTS=1", "GIT_NO_LAZY_FETCH=1", "LC_ALL=C"}}
		response, err := (execx.Local{}).Run(ctx, command)
		if err != nil || response.ExitCode != 0 {
			t.Fatalf("fixture Git read: %v %+v", err, response)
		}
		responses["git "+strings.Join(command.Args, " ")] = response
	}
	missingPin := strings.Repeat("f", 40)
	responses["git "+strings.Join(append(slices.Clone(prefix), "cat-file", "-e", missingPin+"^{commit}"), " ")] = execx.Result{ExitCode: 1}
	fixturePath, logPath := filepath.Join(probes, "fixture.json"), filepath.Join(probes, "commands.log")
	evalWrite(t, fixturePath, evalJSON(t, map[string]any{"Root": root, "Log": logPath, "Probes": responses}))
	process := func(args ...string) *exec.Cmd {
		cmd := mcpProcess(ctx, binary, cwd, args...)
		cmd.Env = append(cmd.Env, "PATH="+probes, "ORCH_TEST_DOCTOR_FIXTURE="+fixturePath)
		return cmd
	}
	cmd := process("mcp", "--repo", root, "--eval-storage-root", storage)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	client := mcp.NewClient(&mcp.Implementation{Name: "preview-test", Version: "1"}, nil)
	before, cwdBefore := mcpTree(t, parent), mcpTree(t, cwd)
	session, err := client.Connect(ctx, &mcp.CommandTransport{Command: cmd}, nil)
	if err != nil {
		t.Fatalf("preview connect: %v: %s", err, &stderr)
	}
	closed := false
	closeSession := func() {
		t.Helper()
		if closed {
			return
		}
		closed = true
		before := mcpTree(t, parent)
		if err := session.Close(); err != nil {
			t.Error(err)
		}
		if cmd.ProcessState == nil || !cmd.ProcessState.Success() || stderr.Len() != 0 || mcpTree(t, parent) != before || mcpTree(t, cwd) != cwdBefore {
			t.Errorf("preview shutdown: state=%v stderr=%s or changed repository/storage", cmd.ProcessState, &stderr)
		}
	}
	t.Cleanup(closeSession)
	mcpDiscover(t, ctx, session, root)
	reject := func(t *testing.T, args any, want string) {
		t.Helper()
		before := mcpTree(t, parent)
		mcpEvalError(t, mcpCall(t, ctx, session, root, "orch_eval_preview", args), want)
		if mcpTree(t, parent) != before || mcpTree(t, cwd) != cwdBefore {
			t.Fatal("rejected preview wrote repository, bound/unbound storage, worker or scratch files")
		}
	}
	for _, args := range []any{nil, json.RawMessage("null"), []any{}, true, 7, "object", map[string]any{},
		map[string]any{"plan_file": ""}, map[string]any{"plan_file": nil}, map[string]any{"plan_file": 1}} {
		reject(t, args, "")
	}
	for _, field := range []string{"storage_root", "eval_storage_root", "repo", "repo_root", "repository", "execute", "approval", "unexpected"} {
		reject(t, map[string]any{"plan_file": "plans/preview.json", field: storage}, "")
	}
	for _, file := range []string{f.file, externalPlan, "../artifacts/plan.json", "plans/../plans/preview.json", "./plans/preview.json",
		"plans/./preview.json", "plans//preview.json", `plans\preview.json`, `C:\plan.json`, `C:plan.json`, `\\server\share\plan`,
		"plans/preview.json:stream", "plans/con", "plans/short~1.json", "plans/trailing.", "plans/missing.json", "plans", " "} {
		reject(t, map[string]any{"plan_file": file}, "plan file")
	}
	if mcpTree(t, parent) != before {
		t.Fatal("initialization/discovery/invalid inputs changed files")
	}
	if _, err := os.Stat(logPath); !os.IsNotExist(err) {
		t.Fatalf("initialization/discovery/invalid plan paths ran a command: %v", err)
	}

	pending := filepath.Join(storage, ".pending-interrupted")
	evalWrite(t, pending, []byte(`{"interrupted":`))
	evalWrite(t, filepath.Join(storage, "maintainer-sentinel"), []byte("preserve unrelated bytes"))
	var record *evalplan.Record
	for _, version := range []int{1, 2, 3} {
		f.plan.Version = version
		if version >= 2 {
			instructions, protected := []evalplan.Artifact{}, []string{}
			f.plan.Instructions, f.plan.ProtectedRoots = &instructions, &protected
		}
		if version == 3 {
			f.plan.Host = "claude"
		}
		f.write(t)
		outsideBefore, storageBefore := mcpPreviewInventory(t, parent), mcpPreviewInventory(t, storage)
		delete(outsideBefore, filepath.Base(storage))
		record = mcpPreviewResult(t, mcpCall(t, ctx, session, root, "orch_eval_preview", input))
		if record.SchemaVersion != version || filepath.Dir(record.StorageDestination) != storage {
			t.Fatal("wrong preparation version or storage destination")
		}
		stored, err := os.ReadFile(record.StorageDestination)
		if err != nil || !bytes.Equal(stored, mcpEvalBytes(t, record)) {
			t.Fatalf("immutable preparation bytes differ: %v", err)
		}
		storageAfter := mcpPreviewInventory(t, storage)
		delete(storageAfter, filepath.Base(record.StorageDestination))
		if !reflect.DeepEqual(storageAfter, storageBefore) {
			t.Fatal("preview wrote beyond its single digest-named record or swept an interrupted publication")
		}
		outsideAfter := mcpPreviewInventory(t, parent)
		delete(outsideAfter, filepath.Base(storage))
		if !reflect.DeepEqual(outsideBefore, outsideAfter) || mcpTree(t, cwd) != cwdBefore {
			t.Fatal("preview changed repository, Git metadata, artifacts, worker, scratch or launching repository")
		}
		// Shared and CLI calls replay the already published preparation exactly;
		// unchanged snapshots also check idempotence (including regular-file mtime).
		before := mcpTree(t, parent)
		shared, err := evalplan.Preview(ctx, root, f.file, evalRunner{f, t})
		if err != nil || !reflect.DeepEqual(shared, record) {
			t.Fatalf("shared/MCP preparation differs: %v", err)
		}
		for _, file := range []string{f.file, externalPlan} {
			if version != 1 && file == externalPlan {
				continue
			}
			cli := process("eval", "preview", "--plan", file, "--json")
			cli.Dir = root
			var stdout, failure bytes.Buffer
			cli.Stdout, cli.Stderr = &stdout, &failure
			if err := cli.Run(); err != nil || failure.Len() != 0 || !bytes.Equal(stdout.Bytes(), stored) {
				t.Fatalf("CLI/shared/MCP preparation parity: %v stderr=%s stdout=%s", err, &failure, &stdout)
			}
		}
		if replay := mcpPreviewResult(t, mcpCall(t, ctx, session, root, "orch_eval_preview", input)); !reflect.DeepEqual(replay, record) || mcpTree(t, parent) != before {
			t.Fatal("repeated preparation changed immutable bytes, metadata or other files")
		}
		t.Logf("preview v%d: CLI = shared preparation = structured = JSON text; one digest record only; replay/pending preservation/no execution verified", version)
	}

	valid := evalJSON(t, f.plan)
	otherStorage := filepath.Join(parent, "other-storage")
	if err := os.Mkdir(otherStorage, 0o700); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name, want string
		mutate     func()
	}{
		{"destination override", "does not match the launch-bound", func() { f.plan.StorageRoot = otherStorage }},
		{"relative destination", "absolute", func() { f.plan.StorageRoot = "storage" }},
		{"repository exclusion", "overlaps", func() { f.plan.StorageRoot = root }},
		{"Git exclusion", "overlaps", func() { f.plan.StorageRoot = filepath.Join(root, ".git") }},
		{"worker exclusion", "overlaps", func() { f.plan.WorkerRoots = []string{storage} }},
		{"scratch exclusion", "overlaps", func() { f.plan.ScratchRoots = []string{storage} }},
		{"missing pin", "unavailable locally; no fetch", func() { f.plan.Baseline.OrchRevision = missingPin }},
		{"artifact traversal", "traversing", func() { f.plan.Corpus.Path = "../artifacts/manifest.json" }},
		{"artifact digest", "digest mismatch", func() { f.plan.DecisionRule.SHA256 = strings.Repeat("f", 64) }},
		{"finite limits", "overall_seconds", func() { f.plan.Limits.OverallSeconds = 0 }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := json.Unmarshal(valid, &f.plan); err != nil {
				t.Fatal(err)
			}
			tc.mutate()
			f.write(t)
			reject(t, input, tc.want)
		})
	}
	for _, data := range [][]byte{
		[]byte(`{"version":`), []byte(`null`), append([]byte(`{"version":3,`), valid[1:]...),
		bytes.Replace(valid, []byte(`"overall_seconds":600`), []byte(`"unknown_limit":600`), 1),
		append(slices.Clone(valid), bytes.Repeat([]byte(" "), evalplan.MaxPlanBytes)...),
	} {
		evalWrite(t, f.file, data)
		reject(t, input, "")
	}
	evalWrite(t, f.file, valid)
	alias := filepath.Join(root, "plans", "hardlink.json")
	if err := os.Link(f.file, alias); err != nil {
		t.Fatal(err)
	}
	reject(t, map[string]any{"plan_file": "plans/hardlink.json"}, "linked file")
	reject(t, input, "linked file")
	if err := os.Remove(alias); err != nil {
		t.Fatal(err)
	}
	for _, target := range []string{filepath.Dir(f.file), filepath.Dir(externalPlan)} {
		alias := filepath.Join(root, "linked-plans")
		mcpDirectoryLink(t, target, alias)
		name := filepath.Base(f.file)
		if target == filepath.Dir(externalPlan) {
			name = filepath.Base(externalPlan)
		}
		reject(t, map[string]any{"plan_file": "linked-plans/" + name}, "")
		if err := os.Remove(alias); err != nil {
			t.Fatal(err)
		}
	}
	if runtime.GOOS != "windows" {
		if err := os.Symlink(f.file, alias); err != nil {
			t.Fatal(err)
		}
		reject(t, map[string]any{"plan_file": "plans/hardlink.json"}, "linked/reparse")
		if err := os.Remove(alias); err != nil {
			t.Fatal(err)
		}
	}
	// Revalidate both bindings on the same connection, rather than treating a
	// previously verified path as permanent permission to follow later aliases.
	for _, path := range []string{storage, root} {
		if err := os.Rename(path, path+"-moved"); err != nil {
			t.Fatal(err)
		}
		mcpDirectoryLink(t, path+"-moved", path)
		reject(t, input, "")
		if err := os.Remove(path); err != nil {
			t.Fatal(err)
		}
		if err := os.Rename(path+"-moved", path); err != nil {
			t.Fatal(err)
		}
	}
	// Neither a conflicting completed name nor an interrupted post-Link alias
	// may be replaced, cleaned up or represented as successful preparation.
	stored, err := os.ReadFile(record.StorageDestination)
	if err != nil {
		t.Fatal(err)
	}
	evalWrite(t, record.StorageDestination, []byte(`{"partial":`))
	reject(t, input, "conflicts or is corrupt")
	evalWrite(t, record.StorageDestination, stored)
	linkedPending := filepath.Join(storage, ".pending-linked-interruption")
	if err := os.Link(record.StorageDestination, linkedPending); err != nil {
		t.Fatal(err)
	}
	reject(t, input, "linked file")
	if err := os.Remove(linkedPending); err != nil {
		t.Fatal(err)
	}
	mcpPreviewResult(t, mcpCall(t, ctx, session, root, "orch_eval_preview", input))
	closeSession()
	transcript, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatal(err)
	}
	for _, line := range strings.Split(strings.TrimSpace(string(transcript)), "\n") {
		cwd, command, ok := strings.Cut(line, "\t")
		if _, allowed := responses[command]; !ok || !allowed || cwd != root {
			t.Fatalf("preview escaped the exact repository-bound Git read allowlist: %q", line)
		}
	}
	t.Log("real stdio: strict inputs, bounded paths/destination, linked/reparse/runtime aliases, corrupt/conflicting/interrupted publication and recovery verified; only existing Git read probes, no worker/approval/Delivery/configuration/GitHub artifacts")
}

type previewIdentityRunner struct {
	evalRunner
	replace func()
}

func (r previewIdentityRunner) Run(ctx context.Context, cmd execx.Cmd) (execx.Result, error) {
	result, err := r.evalRunner.Run(ctx, cmd)
	if err == nil && cmd.Args[4] == "worktree" {
		r.replace()
	}
	return result, err
}

func TestEvalPreviewRepositoryRechecksOpenedIdentities(t *testing.T) {
	for _, binding := range []string{"repository", "storage"} {
		t.Run(binding, func(t *testing.T) {
			f, _ := mcpPreviewFixture(t)
			path := f.repo
			want := "directory identity changed"
			if binding == "storage" {
				path = f.plan.StorageRoot
				want = "does not match the launch-bound"
			}
			before := mcpTree(t, filepath.Dir(f.repo))
			runner := previewIdentityRunner{evalRunner{f, t}, func() {
				if err := os.Rename(path, path+"-moved"); err != nil {
					t.Fatal(err)
				}
				if err := os.Mkdir(path, 0o700); err != nil {
					t.Fatal(err)
				}
			}}
			_, err := evalplan.PreviewRepository(t.Context(), f.repo, f.plan.StorageRoot, "plans/preview.json", runner)
			if err == nil || !strings.Contains(err.Error(), want) {
				t.Fatalf("replaced %s identity accepted: %v", binding, err)
			}
			if entries, err := os.ReadDir(path); err != nil || len(entries) != 0 {
				t.Fatalf("publication entered replacement %s: %v %v", binding, entries, err)
			}
			if err := os.Remove(path); err != nil {
				t.Fatal(err)
			}
			if err := os.Rename(path+"-moved", path); err != nil {
				t.Fatal(err)
			}
			if mcpTree(t, filepath.Dir(f.repo)) != before {
				t.Fatal("identity refusal published bytes or changed retained files")
			}
		})
	}
}
