package interview

import (
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kninetimmy/orch/internal/execx/execxtest"
	"github.com/kninetimmy/orch/internal/paths"
)

func TestDetectBoundKeepsProbesInBoundRepository(t *testing.T) {
	root, err := paths.Canonical(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	// A same-directory spelling is accepted, but never used as the probe cwd.
	gitRoot := root + string(filepath.Separator) + "."
	script := &execxtest.Script{T: t, Calls: []execxtest.Call{
		{Name: "git", Args: []string{"rev-parse", "--show-toplevel"}, Dir: root, Stdout: gitRoot + "\n"},
		{Name: "memhub", Args: []string{"status"}, Dir: root},
		{Name: "opencode2", Args: []string{"--version"}, Dir: root, Stdout: "opencode v2.2.6"},
		{Name: "opencode2", Args: []string{"api", "get", "/api/model?" + url.Values{"location[directory]": {root}}.Encode()}, Dir: root, Stdout: fmt.Sprintf(`{"location":{"directory":%q},"data":[]}`, root)},
	}}
	facts, err := DetectBound(t.Context(), Deps{RepoRoot: root, LookPath: fakeLookPath("git", "memhub", "opencode2"), Runner: script})
	script.AssertExhausted()
	if err != nil || !facts.Git || facts.GitRoot != gitRoot || !facts.OpenCodeCLI || !facts.MemhubHealthy {
		t.Fatalf("bound detection = %+v, %v", facts, err)
	}
}

func TestDetectBoundRejectsUnverifiableOrDifferentGitRootBeforeReads(t *testing.T) {
	root, other := t.TempDir(), t.TempDir()
	file := filepath.Join(other, "not-a-directory")
	if err := os.WriteFile(file, []byte("fixture"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, gitRoot := range []string{other, filepath.Dir(root), filepath.Join(root, "missing"), file, "relative", ""} {
		t.Run(gitRoot, func(t *testing.T) {
			script := &execxtest.Script{T: t, Calls: []execxtest.Call{
				{Name: "git", Args: []string{"rev-parse", "--show-toplevel"}, Dir: root, Stdout: gitRoot + "\n"},
			}}
			facts, err := DetectBound(t.Context(), Deps{RepoRoot: root, LookPath: fakeLookPath("git", "memhub", "opencode2"), Runner: script})
			script.AssertExhausted()
			if err == nil || !strings.Contains(err.Error(), "interview repository binding") || facts.Instructions != nil {
				t.Fatalf("bound detection = %+v, %v", facts, err)
			}
		})
	}
}

func TestDetectBoundRetainsAbsentGitFacts(t *testing.T) {
	for _, gitPresent := range []bool{false, true} {
		script := &execxtest.Script{T: t}
		lookPath := fakeLookPath()
		if gitPresent {
			lookPath = fakeLookPath("git")
			script.Calls = []execxtest.Call{{Name: "git", Args: []string{"rev-parse", "--show-toplevel"}, Exit: 128}}
		}
		facts, err := DetectBound(t.Context(), Deps{RepoRoot: t.TempDir(), LookPath: lookPath, Runner: script})
		script.AssertExhausted()
		if err != nil || facts.Git || facts.GitRoot != "" || len(facts.Instructions) != 3 {
			t.Fatalf("absent Git detection = %+v, %v", facts, err)
		}
	}
}

func TestDetectStillDiscoversGitRoot(t *testing.T) {
	root, other := t.TempDir(), t.TempDir()
	if err := os.WriteFile(filepath.Join(other, "AGENTS.md"), []byte("# Existing conventions\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	script := &execxtest.Script{T: t, Calls: []execxtest.Call{
		{Name: "git", Args: []string{"rev-parse", "--show-toplevel"}, Dir: root, Stdout: other},
	}}
	facts := Detect(t.Context(), Deps{RepoRoot: root, LookPath: fakeLookPath("git"), Runner: script})
	script.AssertExhausted()
	if !facts.Git || facts.GitRoot != other || !facts.Instructions["opencode"].Exists {
		t.Fatalf("ordinary detection no longer follows Git root: %+v", facts)
	}
}
