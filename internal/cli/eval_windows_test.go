package cli

import (
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// A real NTFS junction needs no developer-mode/admin symlink privilege. The
// command only prepares generated test paths; preview never executes it.
func TestEvalPreviewRejectsWindowsJunction(t *testing.T) {
	f := newEvalFixture(t)
	alias := filepath.Join(filepath.Dir(f.repo), "junction")
	cmd := exec.CommandContext(t.Context(), "cmd", "/c", "mklink", "/J", alias, f.plan.StorageRoot)
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("junction fixture: %v: %s", err, output)
	}
	f.plan.StorageRoot = alias
	f.write(t)
	if code, _, failure := f.run(t); code != ExitError || !strings.Contains(failure, "linked/reparse") {
		t.Fatalf("junction storage accepted: %d %s", code, failure)
	}
	f.plan.StorageRoot = filepath.Join(filepath.Dir(f.repo), "storage")
	f.plan.WorkerRoots = []string{alias}
	f.write(t)
	if code, _, failure := f.run(t); code != ExitError || !strings.Contains(failure, "linked/reparse") {
		t.Fatalf("junction worker exclusion accepted: %d %s", code, failure)
	}
}
