package gitops

import "errors"

// Sentinel errors callers test with errors.Is. Each marks an expected
// fail-closed condition detected by a pre-flight plumbing check —
// never by parsing git's human-readable stderr, whose wording varies
// across versions.
var (
	// ErrNotARepo reports that the path is not inside a git work tree.
	ErrNotARepo = errors.New("not a git repository")
	// ErrNotClean reports modified, staged, or untracked files where
	// a clean tree is required (PRD §5, §15).
	ErrNotClean = errors.New("working tree is not clean")
	// ErrDetachedHead reports a detached HEAD; Delivery never
	// operates detached.
	ErrDetachedHead = errors.New("HEAD is detached")
	// ErrProtectedBranch reports that the active branch is one the
	// caller declared protected (PRD §15: never operate on main).
	ErrProtectedBranch = errors.New("active branch is a protected branch")
	// ErrBranchExists reports a branch that already exists where a
	// new one must be created (PRD §12: one fresh branch per issue).
	ErrBranchExists = errors.New("branch already exists")
	// ErrBranchNotMerged reports git refusing to delete an unmerged
	// branch; the work it holds is preserved (PRD §15).
	ErrBranchNotMerged = errors.New("branch is not fully merged")
	// ErrUnknownWorktree reports a removal target that is not a
	// registered worktree; gitops never deletes arbitrary paths.
	ErrUnknownWorktree = errors.New("path is not a registered worktree")
	// ErrNotFastForward reports diverged history where only a
	// fast-forward is allowed (PRD §12 step 20); nothing changed.
	ErrNotFastForward = errors.New("fast-forward is not possible")
	// ErrNotConfirmed reports a destructive operation attempted
	// without ExplicitConfirmation (PRD §15).
	ErrNotConfirmed = errors.New("destructive operation requires explicit confirmation")
)
