package run

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/kninetimmy/orch/internal/lockfile"
	"github.com/kninetimmy/orch/internal/state"
)

func TestStatusAssist(t *testing.T) {
	root := setupRepo(t, testConfigTOML)
	doc, err := Status(context.Background(), Env{RepoRoot: root})
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if doc.Mode != state.ModeAssist || !doc.Consistent || doc.Warning != "" {
		t.Errorf("doc = %+v, want clean assist", doc)
	}
	if doc.Lock != nil || doc.Run != nil {
		t.Errorf("doc = %+v, want no lock and no run", doc)
	}
}

func TestStatusDelivery(t *testing.T) {
	root := setupRepo(t, testConfigTOML)
	planRef := state.PlanRef{Title: "t", Digest: "sha256:x", ConfigRevision: "r1"}
	issues := []state.Issue{{PlanID: "a", Title: "A", Phase: state.PhasePlanned}}
	st, err := state.EnterDelivery(root, "claude", planRef, issues)
	if err != nil {
		t.Fatal(err)
	}

	doc, err := Status(context.Background(), Env{RepoRoot: root})
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if doc.Mode != state.ModeDelivery || !doc.Consistent || doc.Warning != "" {
		t.Errorf("doc = %+v, want consistent delivery", doc)
	}
	if doc.Lock == nil || doc.Lock.RunID != st.Run.ID {
		t.Fatalf("Lock = %+v, want run %s", doc.Lock, st.Run.ID)
	}
	if doc.Run == nil || doc.Run.ID != st.Run.ID || doc.Run.Plan.Digest != "sha256:x" {
		t.Fatalf("Run = %+v", doc.Run)
	}
	if len(doc.Run.Issues) != 1 || doc.Run.Issues[0].PlanID != "a" {
		t.Errorf("Run.Issues = %+v", doc.Run.Issues)
	}
}

func TestStatusInconsistentOrphanedLockWarnsNotFails(t *testing.T) {
	root := setupRepo(t, testConfigTOML)
	err := lockfile.Acquire(root, lockfile.Owner{RunID: "run-orphan", Host: "codex", Hostname: "h", PID: 1, AcquiredAt: time.Now().UTC()})
	if err != nil {
		t.Fatal(err)
	}

	doc, err := Status(context.Background(), Env{RepoRoot: root})
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if doc.Consistent {
		t.Error("Consistent = true, want false for an orphaned lock")
	}
	if !strings.Contains(doc.Warning, "orch abort") {
		t.Errorf("Warning = %q, want abort remediation", doc.Warning)
	}
	if doc.Mode != state.ModeAssist {
		t.Errorf("Mode = %s, want assist (state file still says assist)", doc.Mode)
	}
}

func TestStatusDoesNotLoadConfig(t *testing.T) {
	// No .orchestrator/config.toml is written at all: Status must still
	// succeed because it never loads config.
	root := t.TempDir()
	if _, err := Status(context.Background(), Env{RepoRoot: root}); err != nil {
		t.Fatalf("Status: %v", err)
	}
}
