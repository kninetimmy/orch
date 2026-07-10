package state

import (
	"crypto/rand"
	"fmt"
	"os"
	"time"

	"github.com/kninetimmy/orch/internal/lockfile"
)

// EnterDelivery acquires the cross-host Delivery lock and records a new
// delivery run. Ordering matters for crash safety: lock first, state
// second — a crash between the two leaves an orphaned lock, which
// denies every new run (fail closed) until `orch abort` clears it. The
// reverse order could leave delivery state without a lock, letting a
// second host acquire.
//
// In this slice only tests call EnterDelivery; the plan gate that will
// invoke it is future work (PRD §22: no manual delivery command).
func EnterDelivery(repoRoot, host string) (*State, error) {
	if host != "claude" && host != "codex" {
		return nil, fmt.Errorf("unknown host %q (want claude or codex)", host)
	}
	now := time.Now().UTC()
	runID, err := newRunID(now)
	if err != nil {
		return nil, err
	}
	hostname, err := os.Hostname()
	if err != nil {
		hostname = "unknown"
	}
	if err := lockfile.Acquire(repoRoot, lockfile.Owner{
		RunID:      runID,
		Host:       host,
		Hostname:   hostname,
		PID:        os.Getpid(),
		AcquiredAt: now,
	}); err != nil {
		return nil, err
	}
	st := &State{
		SchemaVersion: SchemaVersion,
		Mode:          ModeDelivery,
		Run:           &Run{ID: runID, Host: host, StartedAt: now},
		UpdatedAt:     now,
	}
	if err := write(repoRoot, st); err != nil {
		_ = lockfile.Release(repoRoot) // best effort; a leftover lock still fails closed
		return nil, err
	}
	return st, nil
}

// AbortResult describes what Abort found and did.
type AbortResult struct {
	// PriorRun is the delivery run the state file recorded, if any.
	PriorRun *Run
	// LockOwner is the readable owner of a released lock, if any.
	LockOwner *lockfile.Owner
	// LockCleared reports that a lock file (readable or not) was removed.
	LockCleared bool
	// StateReset reports that an unreadable or inconsistent state file
	// was overwritten with assist.
	StateReset bool
}

// Abort returns the repository to Assist and releases the Delivery
// lock (PRD §15). It is the explicit human recovery path, so it also
// clears corrupt state files and orphaned or unreadable locks, and it
// is idempotent. It never touches branches or worktrees. Ordering
// matters for crash safety: state first, lock second — see
// EnterDelivery.
func Abort(repoRoot string) (*AbortResult, error) {
	res := &AbortResult{}

	st, err := Load(repoRoot)
	if err != nil {
		res.StateReset = true
	} else if st.Mode == ModeDelivery {
		res.PriorRun = st.Run
	}

	owner, inspectErr := lockfile.Inspect(repoRoot)
	res.LockOwner = owner
	res.LockCleared = owner != nil || inspectErr != nil

	if res.PriorRun == nil && !res.StateReset && !res.LockCleared {
		return res, nil // already assist, nothing to change
	}

	if res.PriorRun != nil || res.StateReset {
		assist := &State{SchemaVersion: SchemaVersion, Mode: ModeAssist, UpdatedAt: time.Now().UTC()}
		if err := write(repoRoot, assist); err != nil {
			return nil, err
		}
	}
	if res.LockCleared {
		if err := lockfile.Release(repoRoot); err != nil {
			return nil, err
		}
	}
	return res, nil
}

// CheckConsistent verifies the invariant that delivery mode and the
// Delivery lock exist together and describe the same run. st and owner
// come from Load and lockfile.Inspect.
func CheckConsistent(st *State, owner *lockfile.Owner) error {
	switch {
	case st.Mode == ModeAssist && owner == nil:
		return nil
	case st.Mode == ModeAssist:
		return fmt.Errorf("delivery lock exists (run %s) but state is assist; run `orch abort` to clear the orphaned lock", owner.RunID)
	case owner == nil:
		return fmt.Errorf("state records delivery run %s but no delivery lock exists; run `orch abort` to reset", st.Run.ID)
	case st.Run.ID != owner.RunID:
		return fmt.Errorf("state run %s does not match lock run %s; run `orch abort` to reset", st.Run.ID, owner.RunID)
	}
	return nil
}

// newRunID builds a sortable, collision-resistant run identifier:
// UTC timestamp plus random hex.
func newRunID(now time.Time) (string, error) {
	var b [4]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", fmt.Errorf("generate run id: %w", err)
	}
	return fmt.Sprintf("run-%s-%x", now.Format("20060102T150405Z"), b), nil
}
