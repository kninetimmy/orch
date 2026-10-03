package evalcorpus

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func manifestFixture(t *testing.T) (Manifest, []byte) {
	t.Helper()
	data, err := os.ReadFile("../../evaluation/reference-v1/manifest.json")
	if err != nil {
		t.Fatal(err)
	}
	m, err := Load(data)
	if err != nil {
		t.Fatal(err)
	}
	return m, data
}

func TestManifest(t *testing.T) {
	_, original := manifestFixture(t)
	for _, mutate := range []func(*Manifest){
		func(m *Manifest) { m.Cases = m.Cases[:11] },
		func(m *Manifest) { m.Cases[2].Lineage = m.Cases[0].Lineage },
		func(m *Manifest) { m.Cases[0].Partition = "held-out" },
		func(m *Manifest) { m.Cases[8].Classification = "defective" },
		func(m *Manifest) { m.Cases[0].Inputs[0].Path = "../escape" },
		func(m *Manifest) { m.Cases[0].Inputs[0].Commit = strings.Repeat("f", 40) },
		func(m *Manifest) { m.Cases[0].Inputs[2].Source = "cases/scout-dev-paths/key.md" },
		func(m *Manifest) { m.Cases[0].PacketSHA256 = strings.Repeat("0", 64) },
		func(m *Manifest) { m.Cases[0].CheckSeconds = 0 },
		func(m *Manifest) { m.Cases[0].Controls[0].Files[0].SHA256 = "not-a-digest" },
	} {
		var m Manifest
		if err := json.Unmarshal(original, &m); err != nil {
			t.Fatal(err)
		}
		mutate(&m)
		data, err := json.Marshal(m)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := Load(data); err == nil {
			t.Error("accepted invalid manifest")
		}
	}
	for _, data := range [][]byte{append(bytes.Clone(original), '}'), bytes.Replace(original, []byte(`"version": 1`), []byte(`"surprise": 1, "version": 1`), 1)} {
		if _, err := Load(data); err == nil {
			t.Error("accepted trailing data or unknown field")
		}
	}
}

func TestExportRejections(t *testing.T) {
	ctx := t.Context()
	corpus, parent := t.TempDir(), t.TempDir()
	if err := os.WriteFile(filepath.Join(corpus, "input.txt"), []byte("declared\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	f := File{Path: "nested/input.txt", Source: "input.txt", SHA256: Digest([]byte("declared\n"))}
	dir, err := Export(ctx, "", corpus, parent, "packet", []File{f})
	if err != nil {
		t.Fatal(err)
	}
	root, err := os.OpenRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = root.Close() }()
	if err := Inspect(root, []File{f}); err != nil {
		t.Fatal(err)
	}
	if _, err := Export(ctx, "", corpus, parent, "packet", []File{f}); err == nil || !strings.Contains(err.Error(), "already exists") {
		t.Fatalf("overwrote existing destination: %v", err)
	}
	if err := root.WriteFile("extra.txt", []byte("controller-key"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := Inspect(root, []File{f}); err == nil {
		t.Error("inspection accepted undeclared file")
	}
	for _, p := range []string{"../escape", "/absolute", "C:/escape", "..\\escape", "a/../escape", "a/.git/config", ".memhub/answers", "evaluation/key", "NUL.txt", "trailing.", "a:stream", "a/*"} {
		bad := f
		bad.Path = p
		if _, err := Export(ctx, "", corpus, parent, "negative", []File{bad}); err == nil {
			t.Errorf("accepted invalid/escaping/forbidden path %q", p)
		}
	}
	bad := f
	bad.SHA256 = strings.Repeat("0", 64)
	if _, err := Export(ctx, "", corpus, parent, "mismatch", []File{bad}); err == nil || !strings.Contains(err.Error(), "digest mismatch") {
		t.Fatalf("digest mismatch not rejected specifically: %v", err)
	}
	bad = f
	bad.Source = "absent"
	if _, err := Export(ctx, "", corpus, parent, "missing", []File{bad}); err == nil {
		t.Error("accepted missing artifact")
	}
	for _, name := range []string{"../escape", "child/nested", "/absolute"} {
		if _, err := Export(ctx, "", corpus, parent, name, []File{f}); err == nil {
			t.Errorf("accepted invalid destination %q", name)
		}
	}
	for _, name := range []string{"negative", "mismatch", "missing"} {
		if _, err := os.Lstat(filepath.Join(parent, name)); !os.IsNotExist(err) {
			t.Errorf("failed export created destination %s: %v", name, err)
		}
	}
}

// Synthetic local Git objects make these rejection tests independent of Orch
// history and of platform symlink privileges. No branch, hook or GitHub changes.
func TestHistoricalExportRejections(t *testing.T) {
	repo := t.TempDir()
	git := func(stdin []byte, args ...string) []byte {
		t.Helper()
		ctx, stop := context.WithTimeout(t.Context(), 10*time.Second)
		defer stop()
		cmd := exec.CommandContext(ctx, "git", args...)
		cmd.Dir = repo
		cmd.Stdin = bytes.NewReader(stdin)
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("synthetic git object setup: %v: %s", err, out)
		}
		return bytes.TrimSpace(out)
	}
	git(nil, "init", "--quiet")
	object := func(kind string, data []byte) string {
		return string(git(data, "hash-object", "-t", kind, "-w", "--stdin"))
	}
	blob := object("blob", []byte("safe\n"))
	commit := func(tree string) string {
		return object("commit", []byte("tree "+tree+"\nauthor Corpus Fixture <fixture@example.invalid> 0 +0000\ncommitter Corpus Fixture <fixture@example.invalid> 0 +0000\n\nfixture\n"))
	}
	tree := string(git([]byte("100644 blob "+blob+"\tsafe.txt\n120000 blob "+blob+"\tlink\n"), "mktree"))
	base := commit(tree)
	withModule := string(git([]byte("160000 commit "+base+"\tmodule\n"), "mktree"))
	submodule := commit(withModule)
	corpus, parent := t.TempDir(), t.TempDir()
	f := File{Path: "safe.txt", Source: "safe.txt", Commit: base, SHA256: Digest([]byte("safe\n"))}
	if _, err := Export(t.Context(), repo, corpus, parent, "valid", []File{f}); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ source, commit, message string }{
		{"link", base, "source link forbidden"},
		{"link/child", base, "source link forbidden"},
		{"module", submodule, "source submodule forbidden"},
		{"module/child", submodule, "source submodule forbidden"},
		{"absent", base, "missing source path"},
		{"safe.txt", strings.Repeat("f", 40), "missing source commit"},
	} {
		bad := f
		bad.Source, bad.Commit = tc.source, tc.commit
		if _, err := Export(context.Background(), repo, corpus, parent, "rejected", []File{bad}); err == nil || !strings.Contains(err.Error(), tc.message) {
			t.Errorf("%s: want %q, got %v", tc.source, tc.message, err)
		}
	}
}

func TestExportLinks(t *testing.T) {
	corpus, parent, outside := t.TempDir(), t.TempDir(), t.TempDir()
	if err := os.WriteFile(filepath.Join(outside, "key.txt"), []byte("not public"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(parent, "link")); err != nil {
		t.Skipf("local symlink creation unavailable; Git-tree link rejection is tested without this prerequisite: %v", err)
	}
	f := File{Path: "input.txt", Source: "input.txt", SHA256: Digest([]byte("public"))}
	if err := os.WriteFile(filepath.Join(corpus, "input.txt"), []byte("public"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Export(t.Context(), "", corpus, filepath.Join(parent, "link"), "escape", []File{f}); err == nil {
		t.Error("destination parent link accepted")
	}
	if err := os.Symlink(filepath.Join(outside, "key.txt"), filepath.Join(corpus, "key.txt")); err != nil {
		t.Fatal(err)
	}
	f.Source = "key.txt"
	f.SHA256 = Digest([]byte("not public"))
	if _, err := Export(t.Context(), "", corpus, parent, "source-link", []File{f}); err == nil {
		t.Error("corpus artifact link accepted")
	}
	if _, err := os.Lstat(filepath.Join(outside, "escape")); !os.IsNotExist(err) {
		t.Error("export escaped destination containment")
	}
}
