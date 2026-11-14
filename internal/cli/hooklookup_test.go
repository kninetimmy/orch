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

func lookupTestGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v: %s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

func TestLookupContextUsesPrimaryConfigAndSelectedCheckout(t *testing.T) {
	root := t.TempDir()
	git := func(dir string, args ...string) string {
		t.Helper()
		return lookupTestGit(t, dir, args...)
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
	if doc.SchemaVersion != 2 || !doc.Managed || doc.MemhubMode != "required" || doc.Head != git(selected, "rev-parse", "HEAD") {
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
	// Removing a linked checkout's copied marker is not proof of non-management.
	if err := os.RemoveAll(filepath.Join(selected, ".orchestrator")); err != nil {
		t.Fatal(err)
	}
	if code := Run(args, env); code != ExitOK {
		t.Fatalf("markerless linked checkout: exit %d: %s", code, &stderr)
	}
	if err := json.Unmarshal(stdout.Bytes(), &doc); err != nil || !doc.Managed || doc.MemhubMode != "required" {
		t.Fatalf("markerless linked context = %s, error %v", &stdout, err)
	}
	stdout.Reset()
	stderr.Reset()
	env.RepoRoot = filepath.Join(selected, "removed")
	if code := Run(args, env); code != ExitError || stdout.Len() != 0 {
		t.Fatalf("missing context: exit %d, stdout %s", code, &stdout)
	}
}

func TestLookupContextAuthoritativeUnmanagedLocations(t *testing.T) {
	plain := t.TempDir()
	lookupTestGit(t, plain, "init", "-b", "main")
	nested := filepath.Join(plain, "a", "b")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, dir := range []string{plain, nested, t.TempDir()} {
		t.Run(filepath.Base(dir), func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			code := Run([]string{"hook", "opencode", "lookup-context", "--json"}, Env{RepoRoot: dir, Stdout: &stdout, Stderr: &stderr})
			if code != ExitOK || stderr.Len() != 0 {
				t.Fatalf("exit %d: %s", code, &stderr)
			}
			if got := strings.TrimSpace(stdout.String()); got != `{"schema_version":2,"managed":false}` {
				t.Fatalf("unmanaged context = %s", got)
			}
			if _, err := os.Stat(filepath.Join(dir, ".memhub")); !os.IsNotExist(err) {
				t.Fatalf("lookup context touched memory: %v", err)
			}
		})
	}
}

func TestLookupContextDiscoveryFailuresAreNotUnmanaged(t *testing.T) {
	cases := map[string]func(*testing.T, string) string{
		"missing location": func(t *testing.T, root string) string {
			return filepath.Join(root, "removed")
		},
		"non-directory location": func(t *testing.T, root string) string {
			file := filepath.Join(root, "file")
			if err := os.WriteFile(file, nil, 0o644); err != nil {
				t.Fatal(err)
			}
			return file
		},
		"invalid Orch marker": func(t *testing.T, root string) string {
			if err := os.WriteFile(filepath.Join(root, ".orchestrator"), nil, 0o644); err != nil {
				t.Fatal(err)
			}
			return root
		},
		"broken Git marker": func(t *testing.T, root string) string {
			if err := os.WriteFile(filepath.Join(root, ".git"), []byte("gitdir: missing"), 0o644); err != nil {
				t.Fatal(err)
			}
			return root
		},
		"managed non-Git location": func(t *testing.T, root string) string {
			writeConfig(t, root, validTOML)
			return root
		},
		"invalid primary config": func(t *testing.T, root string) string {
			lookupTestGit(t, root, "init", "-b", "main")
			writeConfig(t, root, "invalid config [")
			return root
		},
		"unreadable primary config": func(t *testing.T, root string) string {
			lookupTestGit(t, root, "init", "-b", "main")
			// A directory cannot be read as a config, even with elevated rights.
			if err := os.MkdirAll(filepath.Join(root, ".orchestrator", "config.toml"), 0o755); err != nil {
				t.Fatal(err)
			}
			return root
		},
		"nested marker cannot manage an unrelated primary": func(t *testing.T, root string) string {
			lookupTestGit(t, root, "init", "-b", "main")
			nested := filepath.Join(root, "nested")
			if err := os.Mkdir(nested, 0o755); err != nil {
				t.Fatal(err)
			}
			writeConfig(t, nested, validTOML)
			return nested
		},
		"nested checkout cannot replace managed ancestor": func(t *testing.T, root string) string {
			writeConfig(t, root, validTOML)
			nested := filepath.Join(root, "nested")
			if err := os.Mkdir(nested, 0o755); err != nil {
				t.Fatal(err)
			}
			lookupTestGit(t, nested, "init", "-b", "main")
			return nested
		},
	}
	for name, setup := range cases {
		t.Run(name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			env := Env{RepoRoot: setup(t, t.TempDir()), Stdout: &stdout, Stderr: &stderr}
			code := Run([]string{"hook", "opencode", "lookup-context", "--json"}, env)
			if code != ExitError || stdout.Len() != 0 || stderr.Len() == 0 {
				t.Fatalf("discovery failure: exit %d, stdout %s, stderr %s", code, &stdout, &stderr)
			}
		})
	}
}

func TestLookupContextUnreadableAncestorIsNotUnmanaged(t *testing.T) {
	root := t.TempDir()
	nested := filepath.Join(root, "locked", "nested")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatal(err)
	}
	locked := filepath.Dir(nested)
	if err := os.Chmod(locked, 0); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(locked, 0o755) })
	if _, err := os.Stat(nested); err == nil {
		t.Skip("host/elevated user does not enforce directory mode permissions")
	}
	var stdout, stderr bytes.Buffer
	code := Run([]string{"hook", "opencode", "lookup-context", "--json"}, Env{RepoRoot: nested, Stdout: &stdout, Stderr: &stderr})
	if code != ExitError || stdout.Len() != 0 || stderr.Len() == 0 {
		t.Fatalf("unreadable discovery: exit %d, stdout %s, stderr %s", code, &stdout, &stderr)
	}
}
