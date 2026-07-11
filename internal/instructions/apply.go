package instructions

import (
	"fmt"
	"os"
	"path/filepath"
)

// Confirmation proves the caller obtained explicit human approval for
// a whole-file deletion (PRD §15 shape; gitops.Confirmation
// precedent). It is a distinct type from gitops.Confirmation so a
// token approved for one destructive domain (branch/worktree removal)
// can never be reused to authorize this package's file deletion. The
// zero value fails closed; only ExplicitConfirmation constructs a
// valid token, and this package never prompts — collecting the
// approval is the caller's job.
type Confirmation struct{ ok bool }

// ExplicitConfirmation returns the token that authorizes one whole-file
// deletion in ApplyRemove/Remove. Call it only after a human approved
// that specific deletion.
func ExplicitConfirmation() Confirmation { return Confirmation{ok: true} }

// write atomically replaces the file at path with data: a temp file in
// the same directory, synced, then renamed over the destination —
// internal/state.write's shape, which also replaces atomically on
// Windows. On any failure the original file is left untouched and the
// temp file is removed on a best-effort basis.
func write(path string, data []byte) error {
	dir := filepath.Dir(path)
	f, err := os.CreateTemp(dir, filepath.Base(path)+"-*.tmp")
	if err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	tmp := f.Name()
	_, err = f.Write(data)
	if err == nil {
		err = f.Sync()
	}
	if closeErr := f.Close(); err == nil {
		err = closeErr
	}
	if err == nil {
		err = os.Rename(tmp, path)
	}
	if err != nil {
		_ = os.Remove(tmp) // best effort; the real file is untouched
		return fmt.Errorf("write %s: %w", path, err)
	}
	return nil
}

// Apply writes change.New to path, or does nothing when change.Old
// equals change.New (ActionNone). There is deliberately no combined
// plan-and-apply entry point for install/upgrade: the PRD requires a
// diff preview before approval, and merging the two steps would make
// it too easy to skip that gate.
func Apply(path string, change Change) error {
	if change.Old == change.New {
		return nil
	}
	return write(path, []byte(change.New))
}

// RemoveResult reports what ApplyRemove actually did.
type RemoveResult int

const (
	// RemovedNothing reports that change.Action was ActionNone: there
	// was no managed region to remove.
	RemovedNothing RemoveResult = iota
	// RemovedBlockOnly reports that the managed region's marker lines
	// were stripped and the file rewritten with change.New.
	RemovedBlockOnly
	// RemovedWholeFile reports that the file itself was deleted,
	// because stripping the region would have left it otherwise
	// empty and the caller supplied ExplicitConfirmation.
	RemovedWholeFile
)

// ApplyRemove applies change (from PlanRemove/PlanRemoveFile) to path.
// ActionNone does nothing (RemovedNothing). When change.DeleteWholeFile
// is true and confirm is a valid ExplicitConfirmation, it deletes the
// file outright (RemovedWholeFile) — it never writes the whitespace
// remainder first, so a crash between steps can never leave a
// half-finished intermediate file. Otherwise — no confirmation, or the
// file has real content left once the region is stripped — it writes
// change.New (RemovedBlockOnly).
//
// A missing Confirmation soft-degrades to a block-only removal rather
// than failing outright: leaving the (now block-free) file in place is
// always a safe, reversible partial completion, unlike gitops'
// RemoveWorktree, which has no such degrade available for a real
// destructive git operation with no safe partial form. This is a
// judgment call — flagged for reviewer attention.
func ApplyRemove(path string, change Change, confirm Confirmation) (RemoveResult, error) {
	if change.Action == ActionNone {
		return RemovedNothing, nil
	}
	if change.DeleteWholeFile && confirm.ok {
		if err := os.Remove(path); err != nil {
			return RemovedNothing, fmt.Errorf("remove %s: %w", path, err)
		}
		return RemovedWholeFile, nil
	}
	if err := write(path, []byte(change.New)); err != nil {
		return RemovedNothing, err
	}
	return RemovedBlockOnly, nil
}

// Remove is PlanRemoveFile followed by ApplyRemove: the convenience
// entry point for deinit callers that don't need the plan and apply
// steps separately.
func Remove(path string, confirm Confirmation) (Change, RemoveResult, error) {
	change, err := PlanRemoveFile(path)
	if err != nil {
		return Change{}, RemovedNothing, err
	}
	result, err := ApplyRemove(path, change, confirm)
	if err != nil {
		return change, RemovedNothing, err
	}
	return change, result, nil
}
