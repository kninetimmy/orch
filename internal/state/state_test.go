package state

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kninetimmy/orch/internal/lockfile"
)

// stateDir returns a temp repo root containing .orchestrator/.
func stateDir(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, ".orchestrator"), 0o755); err != nil {
		t.Fatal(err)
	}
	return root
}

func writeState(t *testing.T, root, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(root, ".orchestrator", "state.json"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestLoadMissingFileIsAssist(t *testing.T) {
	st, err := Load(stateDir(t))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if st.Mode != ModeAssist || st.Run != nil {
		t.Errorf("Load = %+v, want assist with no run", st)
	}
}

func TestLoadRejectsBadState(t *testing.T) {
	cases := map[string]struct {
		content string
		wantMsg string
	}{
		"corrupt json":    {"{broken", "parse"},
		"wrong schema":    {`{"schema_version": 99, "mode": "assist"}`, "schema_version 99"},
		"unknown mode":    {`{"schema_version": 1, "mode": "turbo"}`, `unknown mode "turbo"`},
		"delivery no run": {`{"schema_version": 1, "mode": "delivery"}`, "without a recorded run"},
		"assist with run": {`{"schema_version": 1, "mode": "assist", "run": {"id": "r", "host": "claude", "started_at": "2026-07-10T12:00:00Z"}}`, "assist mode with a recorded run"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			root := stateDir(t)
			writeState(t, root, tc.content)
			_, err := Load(root)
			if err == nil {
				t.Fatal("Load succeeded, want error")
			}
			if !strings.Contains(err.Error(), tc.wantMsg) {
				t.Errorf("error %q missing %q", err, tc.wantMsg)
			}
		})
	}
}

func TestWriteReplacesExistingState(t *testing.T) {
	root := stateDir(t)
	first := &State{SchemaVersion: SchemaVersion, Mode: ModeAssist, UpdatedAt: time.Now().UTC()}
	if err := write(root, first); err != nil {
		t.Fatalf("first write: %v", err)
	}
	second := &State{
		SchemaVersion: SchemaVersion,
		Mode:          ModeDelivery,
		Run:           &Run{ID: "run-x", Host: "claude", StartedAt: time.Now().UTC()},
		UpdatedAt:     time.Now().UTC(),
	}
	// Rename over an existing file must work on Windows too.
	if err := write(root, second); err != nil {
		t.Fatalf("second write: %v", err)
	}
	got, err := Load(root)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got.Mode != ModeDelivery || got.Run == nil || got.Run.ID != "run-x" {
		t.Errorf("Load = %+v, want delivery run-x", got)
	}
	leftovers, err := filepath.Glob(filepath.Join(root, ".orchestrator", "state-*.tmp"))
	if err != nil {
		t.Fatal(err)
	}
	if len(leftovers) > 0 {
		t.Errorf("temp files left behind: %v", leftovers)
	}
}

func TestEnterDelivery(t *testing.T) {
	root := stateDir(t)
	st, err := EnterDelivery(root, "claude")
	if err != nil {
		t.Fatalf("EnterDelivery: %v", err)
	}
	if st.Mode != ModeDelivery || st.Run == nil {
		t.Fatalf("EnterDelivery state = %+v", st)
	}
	owner, err := lockfile.Inspect(root)
	if err != nil || owner == nil {
		t.Fatalf("Inspect = %+v, %v", owner, err)
	}
	if owner.RunID != st.Run.ID {
		t.Errorf("lock run %s != state run %s", owner.RunID, st.Run.ID)
	}
	if err := CheckConsistent(st, owner); err != nil {
		t.Errorf("CheckConsistent: %v", err)
	}
}

func TestEnterDeliveryRejectsUnknownHost(t *testing.T) {
	if _, err := EnterDelivery(stateDir(t), "emacs"); err == nil {
		t.Error("EnterDelivery accepted unknown host")
	}
}

func TestEnterDeliveryContentionLeavesStateUntouched(t *testing.T) {
	root := stateDir(t)
	if _, err := EnterDelivery(root, "claude"); err != nil {
		t.Fatal(err)
	}
	before, err := Load(root)
	if err != nil {
		t.Fatal(err)
	}
	_, err = EnterDelivery(root, "codex")
	if !errors.Is(err, lockfile.ErrHeld) {
		t.Fatalf("second EnterDelivery = %v, want ErrHeld", err)
	}
	after, err := Load(root)
	if err != nil {
		t.Fatal(err)
	}
	if after.Run.ID != before.Run.ID {
		t.Errorf("state changed on contention: %s -> %s", before.Run.ID, after.Run.ID)
	}
}

func TestAbortFromDelivery(t *testing.T) {
	root := stateDir(t)
	st, err := EnterDelivery(root, "claude")
	if err != nil {
		t.Fatal(err)
	}
	res, err := Abort(root)
	if err != nil {
		t.Fatalf("Abort: %v", err)
	}
	if res.PriorRun == nil || res.PriorRun.ID != st.Run.ID {
		t.Errorf("PriorRun = %+v, want run %s", res.PriorRun, st.Run.ID)
	}
	if res.LockOwner == nil || !res.LockCleared {
		t.Errorf("lock not reported released: %+v", res)
	}
	after, err := Load(root)
	if err != nil {
		t.Fatal(err)
	}
	if after.Mode != ModeAssist {
		t.Errorf("mode after abort = %s, want assist", after.Mode)
	}
	if o, err := lockfile.Inspect(root); o != nil || err != nil {
		t.Errorf("lock still present after abort: %+v, %v", o, err)
	}
}

func TestAbortIdempotentFromAssist(t *testing.T) {
	root := stateDir(t)
	res, err := Abort(root)
	if err != nil {
		t.Fatalf("Abort: %v", err)
	}
	if res.PriorRun != nil || res.LockCleared || res.StateReset {
		t.Errorf("Abort on clean assist did something: %+v", res)
	}
	if _, err := os.Stat(filepath.Join(root, ".orchestrator", "state.json")); !errors.Is(err, os.ErrNotExist) {
		t.Error("Abort on clean assist created a state file")
	}
}

func TestAbortClearsOrphanedLock(t *testing.T) {
	root := stateDir(t)
	err := lockfile.Acquire(root, lockfile.Owner{RunID: "run-orphan", Host: "codex", Hostname: "h", PID: 1, AcquiredAt: time.Now().UTC()})
	if err != nil {
		t.Fatal(err)
	}
	res, err := Abort(root)
	if err != nil {
		t.Fatalf("Abort: %v", err)
	}
	if !res.LockCleared || res.LockOwner == nil || res.LockOwner.RunID != "run-orphan" {
		t.Errorf("orphaned lock not reported: %+v", res)
	}
	if o, _ := lockfile.Inspect(root); o != nil {
		t.Error("orphaned lock still present after abort")
	}
}

func TestAbortResetsCorruptState(t *testing.T) {
	root := stateDir(t)
	writeState(t, root, "{broken")
	res, err := Abort(root)
	if err != nil {
		t.Fatalf("Abort: %v", err)
	}
	if !res.StateReset {
		t.Errorf("StateReset = false: %+v", res)
	}
	after, err := Load(root)
	if err != nil {
		t.Fatalf("Load after abort: %v", err)
	}
	if after.Mode != ModeAssist {
		t.Errorf("mode = %s, want assist", after.Mode)
	}
}

func TestCheckConsistent(t *testing.T) {
	run := &Run{ID: "run-a", Host: "claude", StartedAt: time.Now().UTC()}
	assist := &State{SchemaVersion: SchemaVersion, Mode: ModeAssist}
	delivery := &State{SchemaVersion: SchemaVersion, Mode: ModeDelivery, Run: run}
	owner := &lockfile.Owner{SchemaVersion: lockfile.SchemaVersion, RunID: "run-a"}
	otherOwner := &lockfile.Owner{SchemaVersion: lockfile.SchemaVersion, RunID: "run-b"}

	cases := map[string]struct {
		st     *State
		owner  *lockfile.Owner
		wantOK bool
	}{
		"assist no lock":    {assist, nil, true},
		"delivery matching": {delivery, owner, true},
		"assist with lock":  {assist, owner, false},
		"delivery no lock":  {delivery, nil, false},
		"run id mismatch":   {delivery, otherOwner, false},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			err := CheckConsistent(tc.st, tc.owner)
			if tc.wantOK && err != nil {
				t.Errorf("CheckConsistent = %v, want nil", err)
			}
			if !tc.wantOK {
				if err == nil {
					t.Fatal("CheckConsistent = nil, want error")
				}
				if !strings.Contains(err.Error(), "orch abort") {
					t.Errorf("error %q missing abort remediation", err)
				}
			}
		})
	}
}
