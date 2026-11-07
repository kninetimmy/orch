package interview

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"testing"

	"github.com/kninetimmy/orch/internal/execx/execxtest"
	"github.com/kninetimmy/orch/internal/paths"
	"github.com/kninetimmy/orch/internal/question"
)

// fakeLookPath resolves only the names present, returning a stand-in
// path for each and exec.ErrNotFound for everything else.
func fakeLookPath(present ...string) func(string) (string, error) {
	set := map[string]bool{}
	for _, p := range present {
		set[p] = true
	}
	return func(name string) (string, error) {
		if set[name] {
			return "/usr/bin/" + name, nil
		}
		return "", errors.New(name + ": not found")
	}
}

func TestDetectAllPresentAndHealthy(t *testing.T) {
	repo := t.TempDir()
	root, err := paths.Canonical(repo)
	if err != nil {
		t.Fatal(err)
	}
	script := &execxtest.Script{T: t, Calls: []execxtest.Call{
		{Name: "git", Args: []string{"rev-parse", "--show-toplevel"}, Dir: repo, Stdout: repo + "\n", Exit: 0},
		{Name: "memhub", Args: []string{"status"}, Dir: repo, Exit: 0},
		{Name: "opencode2", Args: []string{"--version"}, Dir: repo, Stdout: "opencode v2.2.6"},
		{Name: "opencode2", Args: []string{"api", "get", "/api/model?" + url.Values{"location[directory]": {root}}.Encode()}, Dir: repo, Stdout: fmt.Sprintf(`{"location":{"directory":%q},"data":[]}`, root)},
	}}
	deps := Deps{RepoRoot: repo, LookPath: fakeLookPath("claude", "codex", "opencode2", "git", "gh", "memhub"), Runner: script}

	facts := Detect(context.Background(), deps)
	script.AssertExhausted()

	if !facts.ClaudeCLI || !facts.CodexCLI || !facts.OpenCodeCLI || !facts.Gh {
		t.Fatalf("facts = %+v, want claude/codex/opencode/gh all true", facts)
	}
	if !facts.Git || facts.GitRoot != repo {
		t.Errorf("git facts = %v/%q, want true/%q", facts.Git, facts.GitRoot, repo)
	}
	if !facts.MemhubCLI || !facts.MemhubHealthy {
		t.Errorf("memhub facts = %+v, want cli/healthy both true", facts)
	}
	if facts.MemhubDetail != "" {
		t.Errorf("MemhubDetail = %q, want empty on success", facts.MemhubDetail)
	}
	if facts.OpenCodeCatalogError != "" || facts.OpenCodeCatalog.Models == nil {
		t.Errorf("OpenCode catalog = %+v / %q, want successful empty catalog", facts.OpenCodeCatalog, facts.OpenCodeCatalogError)
	}
}

func TestDetectKeepsOpenCodeCatalogFailureAsSafeData(t *testing.T) {
	root := t.TempDir()
	canonical, err := paths.Canonical(root)
	if err != nil {
		t.Fatal(err)
	}
	script := &execxtest.Script{T: t, Calls: []execxtest.Call{
		{Name: "opencode2", Args: []string{"--version"}, Dir: root, Stdout: "opencode v2.2.6"},
		{
			Name: "opencode2",
			Args: []string{"api", "get", "/api/model?" + url.Values{"location[directory]": {canonical}}.Encode()},
			Dir:  root, Exit: 1, Stderr: "credential-token raw-provider-payload",
		},
	}}
	facts := Detect(context.Background(), Deps{RepoRoot: root, LookPath: fakeLookPath("opencode2"), Runner: script})
	script.AssertExhausted()
	if !facts.OpenCodeCLI || !strings.Contains(facts.OpenCodeCatalogError, "exited 1") {
		t.Fatalf("catalog detection = %+v / %q", facts.OpenCodeCatalog, facts.OpenCodeCatalogError)
	}
	if strings.Contains(facts.OpenCodeCatalogError, "credential-token") || strings.Contains(facts.OpenCodeCatalogError, "raw-provider-payload") {
		t.Fatalf("catalog failure disclosed command output: %s", facts.OpenCodeCatalogError)
	}
}

func TestDetectMemhubUnhealthy(t *testing.T) {
	script := &execxtest.Script{T: t, Calls: []execxtest.Call{
		{Name: "git", Args: []string{"rev-parse", "--show-toplevel"}, Dir: "/repo", Stdout: "/repo\n", Exit: 0},
		{Name: "memhub", Args: []string{"status"}, Dir: "/repo", Stderr: "db locked", Exit: 1},
	}}
	deps := Deps{RepoRoot: "/repo", LookPath: fakeLookPath("git", "memhub"), Runner: script}

	facts := Detect(context.Background(), deps)
	script.AssertExhausted()

	if !facts.MemhubCLI {
		t.Error("MemhubCLI = false, want true (it was found on PATH)")
	}
	if facts.MemhubHealthy {
		t.Error("MemhubHealthy = true, want false on a non-zero exit")
	}
	const wantDetail = "memhub status exited 1: db locked"
	if facts.MemhubDetail != wantDetail {
		t.Errorf("MemhubDetail = %q, want %q", facts.MemhubDetail, wantDetail)
	}
}

func TestDetectV2OnlyOpencodeAndRetainsAdvisory(t *testing.T) {
	root, err := paths.Canonical(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	script := &execxtest.Script{T: t, Calls: []execxtest.Call{
		{Name: "opencode2", Args: []string{"--version"}, Err: errors.New("not found")},
		{Name: "opencode", Args: []string{"--version"}, Stdout: "opencode v3.0.0"},
		{Name: "opencode", Args: []string{"api", "get", "/api/model?" + url.Values{"location[directory]": {root}}.Encode()}, Stdout: fmt.Sprintf(`{"location":{"directory":%q},"data":[]}`, root)},
	}}
	facts := Detect(context.Background(), Deps{RepoRoot: root, LookPath: fakeLookPath("opencode"), Runner: script})
	script.AssertExhausted()
	if !facts.OpenCodeCLI || facts.OpenCodeCatalogError != "" || facts.OpenCodeWarning == "" {
		t.Fatalf("expected V2 catalog and advisory, got %+v", facts)
	}
	if got := buildComplete(question.Summary{}, facts).Detection["opencode_warning"]; got != facts.OpenCodeWarning {
		t.Fatalf("terminal warning=%q, want %q", got, facts.OpenCodeWarning)
	}
}

func TestDetectDoesNotTreatLegacyOpencodeAsV2(t *testing.T) {
	script := &execxtest.Script{T: t, Calls: []execxtest.Call{
		{Name: "opencode2", Args: []string{"--version"}, Err: errors.New("not found")},
		{Name: "opencode", Args: []string{"--version"}, Stdout: "opencode v1.18.34"},
	}}
	facts := Detect(context.Background(), Deps{RepoRoot: t.TempDir(), LookPath: fakeLookPath("opencode"), Runner: script})
	script.AssertExhausted()
	if facts.OpenCodeCLI || !strings.Contains(facts.OpenCodeCatalogError, "legacy V1") {
		t.Fatalf("expected explicit legacy-runtime detection, got %+v", facts)
	}
	if err := requireOpenCodeCatalog(facts); err == nil || !strings.Contains(err.Error(), "legacy V1") {
		t.Fatalf("setup lost the specific runtime failure: %v", err)
	}
}

func TestDetectNoGit(t *testing.T) {
	deps := Deps{RepoRoot: "/repo", LookPath: fakeLookPath("claude"), Runner: &execxtest.Script{T: t}}

	facts := Detect(context.Background(), deps)

	if facts.Git {
		t.Error("Git = true, want false when git is not on PATH")
	}
	if facts.GitRoot != "" {
		t.Errorf("GitRoot = %q, want empty", facts.GitRoot)
	}
	if facts.MemhubCLI || facts.MemhubHealthy {
		t.Errorf("memhub facts = %+v, want both false when memhub is not on PATH", facts)
	}
	if !facts.ClaudeCLI {
		t.Error("ClaudeCLI = false, want true")
	}
	if facts.CodexCLI || facts.OpenCodeCLI || facts.Gh {
		t.Errorf("facts = %+v, want codex/opencode/gh false", facts)
	}
}

func TestDetectGitNotARepo(t *testing.T) {
	script := &execxtest.Script{T: t, Calls: []execxtest.Call{
		{Name: "git", Args: []string{"rev-parse", "--show-toplevel"}, Dir: "/repo", Stderr: "not a git repository", Exit: 128},
	}}
	deps := Deps{RepoRoot: "/repo", LookPath: fakeLookPath("git"), Runner: script}

	facts := Detect(context.Background(), deps)
	script.AssertExhausted()

	if facts.Git {
		t.Error("Git = true, want false when the repo root is not inside a git worktree")
	}
}

func TestDetectNilLookPath(t *testing.T) {
	facts := Detect(context.Background(), Deps{RepoRoot: "/repo"})
	if facts.ClaudeCLI || facts.CodexCLI || facts.OpenCodeCLI || facts.Git || facts.Gh || facts.MemhubCLI {
		t.Errorf("facts = %+v, want every field false with a nil LookPath", facts)
	}
}
