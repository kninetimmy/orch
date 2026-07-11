package ghops

import "errors"

// Sentinel errors callers test with errors.Is. Each marks an expected
// fail-closed condition detected by a structured check (exit codes,
// closed enums, the Confirmation token) — never by parsing gh's
// human-readable output, whose wording varies across versions.
var (
	// ErrNotAuthenticated reports that gh has no usable credentials
	// (PRD §5: Delivery fails closed unless authentication passes).
	ErrNotAuthenticated = errors.New("gh is not authenticated")
	// ErrNoGitHubRepo reports that gh could not resolve a GitHub
	// repository from this directory (PRD §5: Assist may operate
	// without a remote; Delivery fails closed).
	ErrNoGitHubRepo = errors.New("no GitHub repository resolved from this directory")
	// ErrBadLabels reports labels violating the PRD §13 taxonomy:
	// exactly one value per group, no reserved or forbidden area
	// names (models never become GitHub labels).
	ErrBadLabels = errors.New("labels do not satisfy the label taxonomy")
	// ErrNotConfirmed reports a destructive operation attempted
	// without ExplicitConfirmation (PRD §15).
	ErrNotConfirmed = errors.New("destructive operation requires explicit confirmation")
)
