//go:build corpus_validation

package evalcorpus

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"go/ast"
	"go/format"
	"go/parser"
	"go/token"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

type edit struct {
	Path string `json:"path"`
	Old  string `json:"old"`
	New  string `json:"new"`
}

type controlResult struct {
	Case             string            `json:"case"`
	Control          string            `json:"control"`
	Command          []string          `json:"command"`
	Directory        string            `json:"directory"`
	LimitSeconds     int               `json:"limit_seconds"`
	ExpectedFailures []string          `json:"expected_failures"`
	ObservedFailures []string          `json:"observed_failures"`
	ObservedPasses   []string          `json:"observed_passes"`
	Verified         bool              `json:"verified"`
	Error            string            `json:"error,omitempty"`
	OutputSHA256     string            `json:"output_sha256"`
	Artifacts        map[string]string `json:"artifact_sha256"`
}

// The suite exercises maintainer controls only. Natural-language outcome
// examples and semantic rubric validity require the fresh independent review.
func TestCorpusValidation(t *testing.T) {
	repo, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	corpusDir := filepath.Join(repo, "evaluation", "reference-v1")
	manifestBytes, err := os.ReadFile(filepath.Join(corpusDir, "manifest.json"))
	if err != nil {
		t.Fatal(err)
	}
	m, err := Load(manifestBytes)
	if err != nil {
		t.Fatal(err)
	}
	base := filepath.Join(repo, ".orchestrator", "worktrees")
	if err := os.MkdirAll(base, 0o700); err != nil {
		t.Fatal(err)
	}
	workspace, err := os.MkdirTemp(base, "corpus-v1-")
	if err != nil {
		t.Fatal(err)
	}
	if os.Getenv("ORCH_CORPUS_RETAIN") != "1" {
		t.Cleanup(func() {
			if err := os.RemoveAll(workspace); err != nil {
				t.Errorf("cleanup own disposable workspace: %v", err)
			}
		})
	}
	for _, dir := range []string{"worker-packets", "controller"} {
		if err := os.Mkdir(filepath.Join(workspace, dir), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	ctx, cancel := context.WithTimeout(t.Context(), 540*time.Second)
	defer cancel()
	var results []controlResult
	var raw []map[string]string
	packets := map[string]string{}
	for _, c := range m.Cases {
		t.Run(c.ID, func(t *testing.T) {
			exportCtx, stop := context.WithTimeout(ctx, 60*time.Second)
			_, err := Export(exportCtx, repo, corpusDir, filepath.Join(workspace, "worker-packets"), c.ID, c.Inputs)
			stop()
			if err != nil {
				t.Fatalf("worker packet preparation: %v", err)
			}
			packets[c.ID] = c.PacketSHA256
			if _, err := readFiles(ctx, repo, corpusDir, []File{c.Key}, false); err != nil {
				t.Fatalf("external key digest/provenance: %v", err)
			}
			if err := os.Mkdir(filepath.Join(workspace, "controller", c.ID), 0o700); err != nil {
				t.Fatal(err)
			}
			for _, control := range c.Controls {
				t.Run(control.Name, func(t *testing.T) {
					result, output := runControl(ctx, repo, corpusDir, workspace, c, control)
					results = append(results, result)
					raw = append(raw, map[string]string{"case": c.ID, "control": control.Name, "output": string(output), "sha256": result.OutputSHA256})
					if !result.Verified {
						t.Errorf("control evidence mismatch: %s\n%s", result.Error, output)
					}
				})
			}
		})
	}
	version, versionErr := exec.CommandContext(ctx, "go", "version").Output()
	gitVersion, gitErr := gitRead(ctx, repo, "--version")
	if err := errors.Join(versionErr, gitErr); err != nil {
		t.Error(err)
	}
	status := "author-controls-passed; independent validation pending; execution blocked"
	if t.Failed() {
		status = "preparation incomplete: author controls failed"
	}
	report := map[string]any{
		"version": 1, "status": status, "manifest_sha256": Digest(manifestBytes),
		"author": m.Author, "independent_validator": nil,
		"go_version": strings.TrimSpace(string(version)), "git_version": strings.TrimSpace(string(gitVersion)),
		"invocation":            "go test -tags=corpus_validation -count=1 ./internal/evalcorpus",
		"environment":           []string{"GOWORK=off", "GOTOOLCHAIN=local", "GOPROXY=off", "GOSUMDB=off", "GOFLAGS=", "CGO_ENABLED=0"},
		"overall_limit_seconds": 540, "packet_sha256": packets, "controls": results,
		"limitations": []string{"No semantic prose grading; fresh independent review is required.", "Controller source in the repository is not protected runtime storage.", "Packet inspection is not operating-system or model-tool isolation.", "Effort-window controls compile declared AST components, not the full interviews.", "No evaluation model trials, measured baseline or Phase 1 completion."},
	}
	for name, value := range map[string]any{"preparation-results.json": report, "control-output.json": raw} {
		data, err := json.MarshalIndent(value, "", "  ")
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(workspace, name), append(data, '\n'), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	t.Logf("maintainer evidence: %s (%s)", workspace, status)
}

func runControl(ctx context.Context, repo, corpusDir, workspace string, c Case, control Control) (result controlResult, output []byte) {
	result = controlResult{Case: c.ID, Control: control.Name, Command: c.Command,
		Directory: "controller/" + c.ID + "/" + control.Name, LimitSeconds: c.CheckSeconds,
		ExpectedFailures: control.ExpectedFailures, ObservedFailures: []string{}, ObservedPasses: []string{}, Artifacts: map[string]string{}}
	fail := func(err error) (controlResult, []byte) { result.Error = err.Error(); return result, output }
	exportCtx, stop := context.WithTimeout(ctx, 60*time.Second)
	data, err := readFiles(exportCtx, repo, corpusDir, control.Files, false)
	stop()
	if err != nil {
		return fail(fmt.Errorf("control setup: %w", err))
	}
	probe, err := readFiles(ctx, repo, corpusDir, []File{c.Probe}, false)
	if err != nil {
		return fail(fmt.Errorf("control probe setup: %w", err))
	}
	if control.Patch != nil {
		patch, err := readFiles(ctx, repo, corpusDir, []File{*control.Patch}, false)
		if err != nil {
			return fail(err)
		}
		var edits []edit
		if err := decode(patch[control.Patch.Path], &edits); err != nil {
			return fail(err)
		}
		for _, e := range edits {
			if err := safePath(e.Path, false); err != nil {
				return fail(err)
			}
			if e.Old == "" || bytes.Count(data[e.Path], []byte(e.Old)) != 1 {
				return fail(fmt.Errorf("control patch must match exactly once: %s", e.Path))
			}
			data[e.Path] = bytes.Replace(data[e.Path], []byte(e.Old), []byte(e.New), 1)
		}
	}
	if c.ID == "implement-held-effort-window" {
		if err := projectEffort(data); err != nil {
			return fail(fmt.Errorf("declared effort component extraction: %w", err))
		}
	}
	data[c.Probe.Path] = probe[c.Probe.Path]
	dir := filepath.Join(workspace, filepath.FromSlash(result.Directory))
	if err := os.Mkdir(dir, 0o700); err != nil {
		return fail(err)
	}
	root, err := os.OpenRoot(dir)
	if err != nil {
		return fail(err)
	}
	defer func() { _ = root.Close() }()
	for p, b := range data {
		if err := safePath(p, false); err != nil {
			return fail(err)
		}
		if err := root.MkdirAll(filepath.Dir(filepath.FromSlash(p)), 0o700); err != nil {
			return fail(err)
		}
		if err := root.WriteFile(filepath.FromSlash(p), b, 0o600); err != nil {
			return fail(err)
		}
		result.Artifacts[p] = Digest(b)
	}
	checkCtx, cancel := context.WithTimeout(ctx, time.Duration(c.CheckSeconds)*time.Second)
	defer cancel()
	cmd := exec.CommandContext(checkCtx, c.Command[0], c.Command[1:]...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GOWORK=off", "GOTOOLCHAIN=local", "GOPROXY=off", "GOSUMDB=off", "GOFLAGS=", "CGO_ENABLED=0")
	cmd.WaitDelay = 2 * time.Second
	output, err = cmd.CombinedOutput()
	result.OutputSHA256 = Digest(output)
	var exit *exec.ExitError
	if checkCtx.Err() != nil || (err != nil && !errors.As(err, &exit)) {
		return fail(fmt.Errorf("control setup/deadline failure: %w", err))
	}
	seenParent := false
	scanner := bufio.NewScanner(bytes.NewReader(output))
	scanner.Buffer(make([]byte, 4096), maxFileBytes)
	for scanner.Scan() {
		var event struct{ Action, Test string }
		if err := json.Unmarshal(scanner.Bytes(), &event); err != nil {
			return fail(fmt.Errorf("control protocol/setup error: %w", err))
		}
		if event.Test == "TestCorpus" && event.Action == "run" {
			seenParent = true
		}
		if strings.HasPrefix(event.Test, "TestCorpus/") {
			if event.Action == "fail" {
				result.ObservedFailures = append(result.ObservedFailures, event.Test)
			} else if event.Action == "pass" {
				result.ObservedPasses = append(result.ObservedPasses, event.Test)
			}
		}
	}
	if err := scanner.Err(); err != nil {
		return fail(err)
	}
	if !seenParent || len(result.ObservedPasses)+len(result.ObservedFailures) == 0 {
		return fail(fmt.Errorf("control setup failure: no behavioral TestCorpus subtests ran"))
	}
	slices.Sort(result.ObservedFailures)
	slices.Sort(result.ObservedPasses)
	expected := slices.Clone(control.ExpectedFailures)
	slices.Sort(expected)
	if !slices.Equal(result.ObservedFailures, expected) || (err != nil) != (len(expected) > 0) {
		return fail(fmt.Errorf("expected failures %v; observed %v; process error %v", expected, result.ObservedFailures, err))
	}
	result.Verified = true
	return result, output
}

// This one case intentionally targets a small interview component. Keep exact
// declaration ASTs and only their used imports; do not substitute helper stubs.
func projectEffort(data map[string][]byte) error {
	wanted := []string{"hostEfforts", "maxOfferedEfforts", "effortsOffered", "effortOptions", "effortLabel", "validEffort"}
	seen := map[string]bool{}
	for _, source := range []string{"internal/interview/sequence.go", "internal/interview/configurelocal.go"} {
		set := token.NewFileSet()
		file, err := parser.ParseFile(set, source, data[source], 0)
		if err != nil {
			return err
		}
		var declarations []ast.Decl
		for _, decl := range file.Decls {
			name := ""
			switch d := decl.(type) {
			case *ast.FuncDecl:
				name = d.Name.Name
			case *ast.GenDecl:
				if len(d.Specs) == 1 {
					if value, ok := d.Specs[0].(*ast.ValueSpec); ok && len(value.Names) == 1 {
						name = value.Names[0].Name
					}
				}
			}
			if slices.Contains(wanted, name) {
				seen[name] = true
				declarations = append(declarations, decl)
			}
		}
		used := map[string]bool{}
		for _, decl := range declarations {
			ast.Inspect(decl, func(node ast.Node) bool {
				if selector, ok := node.(*ast.SelectorExpr); ok {
					if id, ok := selector.X.(*ast.Ident); ok {
						used[id.Name] = true
					}
				}
				return true
			})
		}
		var imports []ast.Spec
		for _, spec := range file.Imports {
			name := filepath.Base(strings.Trim(spec.Path.Value, "\""))
			if spec.Name != nil {
				name = spec.Name.Name
			}
			if used[name] {
				imports = append(imports, spec)
			}
		}
		if len(imports) > 0 {
			declarations = append([]ast.Decl{&ast.GenDecl{Tok: token.IMPORT, Specs: imports}}, declarations...)
		}
		file.Decls = declarations
		var output bytes.Buffer
		if err := format.Node(&output, set, file); err != nil {
			return err
		}
		data[source] = output.Bytes()
	}
	for _, name := range wanted {
		if name != "effortLabel" && !seen[name] {
			return fmt.Errorf("required source declaration missing: %s", name)
		}
	}
	return nil
}
