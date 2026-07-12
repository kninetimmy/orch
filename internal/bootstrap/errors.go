package bootstrap

import (
	"errors"

	"github.com/kninetimmy/orch/internal/config"
)

// Sentinel errors callers test with errors.Is. Specifics wrap these
// with %w and detail (config/run house style) rather than replace
// them.
var (
	// ErrNotComplete reports that Next did not re-derive a terminal
	// DocComplete document from deps.Answers: either the interview is
	// not finished (still questions/summary) or it was aborted.
	ErrNotComplete = errors.New("interview has not reached the terminal complete document")
	// ErrNotBootstrapReady reports a re-derived Complete document whose
	// BootstrapReady is false (git or gh was not detected).
	ErrNotBootstrapReady = errors.New("bootstrap readiness requirements are not met")
	// ErrAlreadyInitialized reports a committed configuration already
	// present at the git root.
	ErrAlreadyInitialized = errors.New("already initialized: " + config.Path + " exists")
	// ErrBranchExists reports the fixed bootstrap branch already
	// resolving locally (contract call 3: an orphan preflight).
	ErrBranchExists = errors.New("bootstrap branch already exists locally")
	// ErrRemoteBranchExists reports the fixed bootstrap branch already
	// present on the remote (contract call 3).
	ErrRemoteBranchExists = errors.New("bootstrap branch already exists on the remote")
	// ErrOpenPRExists reports an open PR already carrying the fixed
	// bootstrap branch as its head (contract call 3).
	ErrOpenPRExists = errors.New("an open bootstrap pull request already exists")
	// ErrValidationFailed reports a §18.13 mechanical check that did
	// not pass inside the freshly written worktree.
	ErrValidationFailed = errors.New("bootstrap installation validation failed")
)
