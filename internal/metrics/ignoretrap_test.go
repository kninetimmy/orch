package metrics

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeGitignore(t *testing.T, root, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(root, ".gitignore"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// TestTrapActiveDisabledNeverTrips proves TrapActive never trips when
// metrics is disabled, regardless of .gitignore's contents (including a
// missing file).
func TestTrapActiveDisabledNeverTrips(t *testing.T) {
	root := t.TempDir()
	trapped, err := TrapActive(false, root)
	if err != nil {
		t.Fatalf("TrapActive: %v", err)
	}
	if trapped {
		t.Error("TrapActive(false, ...) = true, want false with no .gitignore at all")
	}

	writeGitignore(t, root, "node_modules/\n")
	trapped, err = TrapActive(false, root)
	if err != nil {
		t.Fatalf("TrapActive: %v", err)
	}
	if trapped {
		t.Error("TrapActive(false, ...) = true, want false with an unrelated .gitignore")
	}
}

// TestTrapActiveEnabledMissingLine proves TrapActive trips when metrics
// is enabled and .gitignore either does not exist or lacks the line.
func TestTrapActiveEnabledMissingLine(t *testing.T) {
	root := t.TempDir()
	trapped, err := TrapActive(true, root)
	if err != nil {
		t.Fatalf("TrapActive: %v", err)
	}
	if !trapped {
		t.Error("TrapActive(true, ...) = false, want true with no .gitignore at all")
	}

	writeGitignore(t, root, "node_modules/\n.orchestrator/state.json\n")
	trapped, err = TrapActive(true, root)
	if err != nil {
		t.Fatalf("TrapActive: %v", err)
	}
	if !trapped {
		t.Error("TrapActive(true, ...) = false, want true when .gitignore omits the metrics line")
	}
}

// TestTrapActiveEnabledLinePresent proves the line's presence — CRLF
// tolerated, mirroring interview's own readGitignore — clears the trap.
func TestTrapActiveEnabledLinePresent(t *testing.T) {
	root := t.TempDir()
	writeGitignore(t, root, "node_modules/\r\n"+GitignoreLine+"\r\n")
	trapped, err := TrapActive(true, root)
	if err != nil {
		t.Fatalf("TrapActive: %v", err)
	}
	if trapped {
		t.Error("TrapActive(true, ...) = true, want false once .gitignore carries the line")
	}
}

// TestExplainTrapWording proves ExplainTrap names metrics as the cause
// and orch configure as the remedy when trapped, and is empty otherwise.
func TestExplainTrapWording(t *testing.T) {
	root := t.TempDir()
	explain := ExplainTrap(true, root)
	if explain == "" {
		t.Fatal("ExplainTrap = \"\", want a cause-and-remedy suffix while trapped")
	}
	if !strings.Contains(explain, "metrics") {
		t.Errorf("ExplainTrap = %q, want it to name metrics", explain)
	}
	if !strings.Contains(explain, "orch configure") {
		t.Errorf("ExplainTrap = %q, want it to name the orch configure remedy", explain)
	}

	writeGitignore(t, root, GitignoreLine+"\n")
	if got := ExplainTrap(true, root); got != "" {
		t.Errorf("ExplainTrap = %q, want empty once the line is present", got)
	}
	if got := ExplainTrap(false, root); got != "" {
		t.Errorf("ExplainTrap = %q, want empty when metrics is disabled", got)
	}
}

// TestGitignoreLineMatchesDir pins GitignoreLine against Dir plus a
// trailing slash — the directory-only ignore-pattern form.
func TestGitignoreLineMatchesDir(t *testing.T) {
	if GitignoreLine != Dir+"/" {
		t.Errorf("GitignoreLine = %q, want %q", GitignoreLine, Dir+"/")
	}
}
