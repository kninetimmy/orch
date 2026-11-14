package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestLookupContextUsesPrimaryConfigAndSelectedCheckout(t *testing.T) {
	root := t.TempDir()
	git := func(dir string, args ...string) string {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v: %s", args, err, out)
		}
		return strings.TrimSpace(string(out))
	}
	git(root, "init", "-b", "main")
	writeConfig(t, root, strings.Replace(validTOML, `mode = "off"`, `mode = "required"`, 1))
	if err := os.WriteFile(filepath.Join(root, "tracked.txt"), []byte("fixture"), 0o644); err != nil {
		t.Fatal(err)
	}
	git(root, "add", ".")
	git(root, "-c", "user.name=Fixture", "-c", "user.email=fixture@example.invalid", "commit", "-m", "Seed fixture")
	selected := filepath.Join(t.TempDir(), "review")
	git(root, "worktree", "add", "--detach", selected, "HEAD")
	// A copied checkout configuration must not become the mode authority.
	if err := os.WriteFile(filepath.Join(selected, ".orchestrator", "config.toml"), []byte("invalid copied config ["), 0o644); err != nil {
		t.Fatal(err)
	}
	nested := filepath.Join(selected, "nested")
	if err := os.Mkdir(nested, 0o755); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	env := Env{RepoRoot: nested, Stdout: &stdout, Stderr: &stderr}
	args := []string{"hook", "opencode", "lookup-context", "--json"}
	if code := Run(args, env); code != ExitOK {
		t.Fatalf("exit %d: %s", code, &stderr)
	}
	var doc lookupContext
	if err := json.Unmarshal(stdout.Bytes(), &doc); err != nil {
		t.Fatal(err)
	}
	if doc.SchemaVersion != 1 || doc.MemhubMode != "required" || doc.Head != git(selected, "rev-parse", "HEAD") {
		t.Fatalf("context = %+v", doc)
	}
	for _, pair := range [][2]string{{doc.MemoryRoot, root}, {doc.CodeRoot, selected}, {doc.GitCommonDir, filepath.Join(root, ".git")}} {
		if err := lookupSameDirectory(pair[0], pair[1]); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := os.Stat(filepath.Join(selected, ".memhub")); !os.IsNotExist(err) {
		t.Fatalf("lookup context touched memory: %v", err)
	}
	stdout.Reset()
	stderr.Reset()
	env.RepoRoot = t.TempDir()
	if code := Run(args, env); code != ExitError || stdout.Len() != 0 {
		t.Fatalf("unverifiable context: exit %d, stdout %s", code, &stdout)
	}
	stdout.Reset()
	stderr.Reset()
	env.RepoRoot = filepath.Join(selected, "removed")
	if code := Run(args, env); code != ExitError || stdout.Len() != 0 {
		t.Fatalf("missing context: exit %d, stdout %s", code, &stdout)
	}
}
