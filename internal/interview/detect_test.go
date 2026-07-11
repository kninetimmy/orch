package interview

import (
	"context"
	"errors"
	"testing"

	"github.com/kninetimmy/orch/internal/execx/execxtest"
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
	script := &execxtest.Script{T: t, Calls: []execxtest.Call{
		{Name: "git", Args: []string{"rev-parse", "--show-toplevel"}, Dir: "/repo", Stdout: "/repo\n", Exit: 0},
		{Name: "memhub", Args: []string{"status"}, Dir: "/repo", Exit: 0},
	}}
	deps := Deps{RepoRoot: "/repo", LookPath: fakeLookPath("claude", "codex", "git", "gh", "memhub"), Runner: script}

	facts := Detect(context.Background(), deps)
	script.AssertExhausted()

	if !facts.ClaudeCLI || !facts.CodexCLI || !facts.Gh {
		t.Fatalf("facts = %+v, want claude/codex/gh all true", facts)
	}
	if !facts.Git || facts.GitRoot != "/repo" {
		t.Errorf("git facts = %v/%q, want true/\"/repo\"", facts.Git, facts.GitRoot)
	}
	if !facts.MemhubCLI || !facts.MemhubHealthy {
		t.Errorf("memhub facts = %+v, want cli/healthy both true", facts)
	}
	if facts.MemhubDetail != "" {
		t.Errorf("MemhubDetail = %q, want empty on success", facts.MemhubDetail)
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
	if facts.MemhubDetail != "db locked" {
		t.Errorf("MemhubDetail = %q, want %q", facts.MemhubDetail, "db locked")
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
	if facts.CodexCLI || facts.Gh {
		t.Errorf("facts = %+v, want codex/gh false", facts)
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
	if facts.ClaudeCLI || facts.CodexCLI || facts.Git || facts.Gh || facts.MemhubCLI {
		t.Errorf("facts = %+v, want every field false with a nil LookPath", facts)
	}
}
