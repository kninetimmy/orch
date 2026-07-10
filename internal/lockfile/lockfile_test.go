package lockfile

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// lockDir returns a temp repo root containing .orchestrator/.
func lockDir(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, ".orchestrator"), 0o755); err != nil {
		t.Fatal(err)
	}
	return root
}

func testOwner() Owner {
	return Owner{
		RunID:      "run-20260710T120000Z-deadbeef",
		Host:       "claude",
		Hostname:   "testhost",
		PID:        os.Getpid(),
		AcquiredAt: time.Date(2026, 7, 10, 12, 0, 0, 0, time.UTC),
	}
}

func TestAcquireInspectRoundTrip(t *testing.T) {
	root := lockDir(t)
	want := testOwner()
	if err := Acquire(root, want); err != nil {
		t.Fatalf("Acquire: %v", err)
	}
	got, err := Inspect(root)
	if err != nil {
		t.Fatalf("Inspect: %v", err)
	}
	want.SchemaVersion = SchemaVersion
	if got == nil || *got != want {
		t.Errorf("Inspect = %+v, want %+v", got, want)
	}
}

func TestAcquireHeldNamesOwner(t *testing.T) {
	root := lockDir(t)
	if err := Acquire(root, testOwner()); err != nil {
		t.Fatalf("Acquire: %v", err)
	}
	err := Acquire(root, testOwner())
	if !errors.Is(err, ErrHeld) {
		t.Fatalf("second Acquire = %v, want ErrHeld", err)
	}
	for _, want := range []string{"claude", "testhost"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not name %q", err, want)
		}
	}
}

func TestAcquireOverCorruptLockDenied(t *testing.T) {
	root := lockDir(t)
	if err := os.WriteFile(filepath.Join(root, ".orchestrator", "delivery.lock"), []byte("{broken"), 0o644); err != nil {
		t.Fatal(err)
	}
	err := Acquire(root, testOwner())
	if !errors.Is(err, ErrHeld) {
		t.Fatalf("Acquire over corrupt lock = %v, want ErrHeld", err)
	}
	if !strings.Contains(err.Error(), "orch abort") {
		t.Errorf("error %q missing abort remediation", err)
	}
}

func TestInspect(t *testing.T) {
	t.Run("absent", func(t *testing.T) {
		o, err := Inspect(lockDir(t))
		if o != nil || err != nil {
			t.Errorf("Inspect = %+v, %v; want nil, nil", o, err)
		}
	})
	for name, content := range map[string]string{
		"corrupt":      "not json",
		"wrong schema": `{"schema_version": 99, "run_id": "r", "host": "claude"}`,
		"empty":        "",
	} {
		t.Run(name, func(t *testing.T) {
			root := lockDir(t)
			if err := os.WriteFile(filepath.Join(root, ".orchestrator", "delivery.lock"), []byte(content), 0o644); err != nil {
				t.Fatal(err)
			}
			if _, err := Inspect(root); err == nil {
				t.Error("Inspect succeeded on untrustworthy lock; want error")
			}
		})
	}
}

func TestReleaseIdempotent(t *testing.T) {
	root := lockDir(t)
	if err := Acquire(root, testOwner()); err != nil {
		t.Fatal(err)
	}
	if err := Release(root); err != nil {
		t.Fatalf("first Release: %v", err)
	}
	if err := Release(root); err != nil {
		t.Fatalf("second Release: %v", err)
	}
	if o, err := Inspect(root); o != nil || err != nil {
		t.Errorf("lock still present after Release: %+v, %v", o, err)
	}
}

func TestAcquireExactlyOneWinner(t *testing.T) {
	root := lockDir(t)
	const n = 8
	var wg sync.WaitGroup
	errs := make([]error, n)
	for i := range errs {
		wg.Add(1)
		go func() {
			defer wg.Done()
			errs[i] = Acquire(root, testOwner())
		}()
	}
	wg.Wait()
	wins := 0
	for _, err := range errs {
		switch {
		case err == nil:
			wins++
		case errors.Is(err, ErrHeld):
		default:
			t.Errorf("unexpected error: %v", err)
		}
	}
	if wins != 1 {
		t.Errorf("%d goroutines acquired the lock, want exactly 1", wins)
	}
}

func TestPIDAlive(t *testing.T) {
	if !PIDAlive(os.Getpid()) {
		t.Error("PIDAlive(self) = false, want true")
	}
	if PIDAlive(0) || PIDAlive(-1) {
		t.Error("PIDAlive(non-positive) = true, want false")
	}
	if pid, ok := exitedPID(t); ok && PIDAlive(pid) {
		t.Errorf("PIDAlive(%d) = true for an exited process, want false", pid)
	}
}

// exitedPID runs this test binary with no matching tests so it exits
// immediately, returning its (now dead) PID.
func exitedPID(t *testing.T) (int, bool) {
	t.Helper()
	cmd := exec.Command(os.Args[0], "-test.run=TestNoSuchTestExists")
	if err := cmd.Start(); err != nil {
		t.Logf("cannot start helper process: %v", err)
		return 0, false
	}
	pid := cmd.Process.Pid
	if err := cmd.Wait(); err != nil {
		t.Logf("helper process: %v", err)
		return 0, false
	}
	return pid, true
}
