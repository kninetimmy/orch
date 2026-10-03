package evalplan

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/kninetimmy/orch/internal/evalcorpus"
)

func storageFixture(t *testing.T) *Record {
	t.Helper()
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	plan := Plan{Version: 1, Scope: "screen", StorageRoot: root}
	data, err := json.Marshal(plan)
	if err != nil {
		t.Fatal(err)
	}
	digest := evalcorpus.Digest(data)
	return &Record{SchemaVersion: 1, Kind: "maintainer-preparation-record", PlanDigest: "sha256:" + digest, StorageDestination: filepath.Join(root, digest+".json"), Plan: plan, Preview: Evidence{WorkerAccessProtection: "unverified"}}
}

func TestStorageConcurrentImmutableAndInterrupted(t *testing.T) {
	r := storageFixture(t)
	// An interrupted unpublished write must neither look complete nor block a
	// valid new submission. Unknown pending files are preserved, never swept.
	pending := filepath.Join(r.Plan.StorageRoot, ".pending-interrupted")
	if err := os.WriteFile(pending, []byte(`{"partial":`), 0o600); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	errors := make(chan error, 12)
	for range 12 {
		wg.Go(func() { errors <- save(r) })
	}
	wg.Wait()
	close(errors)
	for err := range errors {
		if err != nil {
			t.Fatal(err)
		}
	}
	data, err := os.ReadFile(r.StorageDestination)
	if err != nil {
		t.Fatal(err)
	}
	var observed Record
	if err := json.Unmarshal(data, &observed); err != nil {
		t.Fatal(err)
	}
	if observed.PlanDigest != r.PlanDigest {
		t.Fatal("wrong retained digest")
	}
	if pendingBytes, err := os.ReadFile(pending); err != nil || string(pendingBytes) != `{"partial":` {
		t.Fatal("unrelated pending file replaced/removed")
	}
	if err := os.WriteFile(r.StorageDestination, []byte("unrelated or interrupted content"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := save(r); err == nil || !strings.Contains(err.Error(), "conflicts or is corrupt") {
		t.Fatalf("corrupt record not rejected: %v", err)
	}
	if data, err := os.ReadFile(r.StorageDestination); err != nil || string(data) != "unrelated or interrupted content" {
		t.Fatal("existing record overwritten")
	}
}

func TestStorageRejectsLinkedRecord(t *testing.T) {
	r := storageFixture(t)
	outside := filepath.Join(filepath.Dir(r.Plan.StorageRoot), "alias-record")
	if err := os.WriteFile(outside, []byte("unrelated"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Link(outside, r.StorageDestination); err != nil {
		t.Fatal(err)
	}
	if err := save(r); err == nil || !strings.Contains(err.Error(), "linked file") {
		t.Fatalf("linked destination accepted: %v", err)
	}
	if data, err := os.ReadFile(outside); err != nil || string(data) != "unrelated" {
		t.Fatal("aliased unrelated file changed")
	}
}

func TestLocalPathRejections(t *testing.T) {
	for _, name := range []string{"../escape", "nested/../escape", "nested/./escape", "NUL.txt", "COM1", "LPT².txt", "trailing.", "a:stream", "https://example.invalid/plan", `\\server\share\plan`, `\\?\C:\plan`, "short~1", "a\x00b"} {
		if _, err := localPath(t.TempDir(), name, false); err == nil {
			t.Errorf("accepted path alias %q", name)
		}
	}
}
