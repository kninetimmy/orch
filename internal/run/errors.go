package run

import "errors"

// Sentinel errors callers test with errors.Is. Specifics wrap these
// with %w and detail (state.go / manifest.go house style) rather than
// replace them.
var (
	// ErrPlanInvalid reports a plan document that fails validation:
	// unsupported schema, a host not enabled, malformed or duplicate
	// issue ids, unresolved/self-referencing/non-acyclic dependencies,
	// invalid risk domains, read-only work (rejected — F3), labels that
	// fail the PRD §13 taxonomy or the model denylist, an unrecognized
	// usage class, or any empty required text field.
	ErrPlanInvalid = errors.New("plan document is invalid")
	// ErrBadApproval reports a malformed activation request envelope or
	// an approval assertion that does not match the plan it approves:
	// unsupported schema, unparsable/unknown-field JSON, a statement
	// other than ApprovalStatement, or a plan_digest that does not
	// equal the recomputed digest (the "adjust = resubmit" loop).
	ErrBadApproval = errors.New("activation approval is invalid")
	// ErrMemhubRequired reports that config.Memhub.Mode is "required"
	// and the memhub probe failed or could not run (PRD §20: fail
	// closed rather than proceed without memory).
	ErrMemhubRequired = errors.New("memhub is required but unavailable")
	// ErrDeliveryActive reports that a Delivery run is already active
	// (or the state/lock pair is inconsistent) when a verb requires
	// Assist with no lock held.
	ErrDeliveryActive = errors.New("a delivery run is already active")

	// ErrNoDeliveryRun reports that a lifecycle verb ran without an
	// active Delivery run to act on (the repository is in Assist).
	ErrNoDeliveryRun = errors.New("no delivery run is active")
	// ErrConfigDrift reports that the committed configuration changed
	// mid-run (its revision no longer matches the run's): config changes
	// ship via their own Delivery run, so drift fails closed.
	ErrConfigDrift = errors.New("configuration changed mid-run")
	// ErrRunStopped reports that the run was stopped by a secret block
	// (PRD §16); every mutating verb but block itself is denied until
	// `orch abort` or a future `orch resume`.
	ErrRunStopped = errors.New("delivery run is stopped")
	// ErrUnknownIssue reports an issue_number that matches no run issue.
	ErrUnknownIssue = errors.New("issue number does not match any run issue")
	// ErrWrongPhase reports an issue whose phase is not in the verb's
	// allowed set.
	ErrWrongPhase = errors.New("issue is not in a phase this verb accepts")
	// ErrBadRequest reports a malformed verb request document: an
	// unsupported schema, unparsable or unknown-field JSON, a wrong
	// confirmation statement, or a value outside a closed set.
	ErrBadRequest = errors.New("verb request document is invalid")
	// ErrDependencyUnmet reports a dispatch blocked by a dependency issue
	// that is not yet merged or cleaned (PRD §11 wave ordering).
	ErrDependencyUnmet = errors.New("a dependency issue is not yet merged")
	// ErrDependencyAbandoned reports a dispatch blocked by an abandoned
	// dependency: the plan's scope changed materially and must go back
	// through the plan gate (PRD §8/§16).
	ErrDependencyAbandoned = errors.New("a dependency issue was abandoned")
	// ErrPRExists reports an open pull request already present for a
	// branch at pr-open — an orphan from a crashed run (PRD §23).
	ErrPRExists = errors.New("an open pull request already exists for the branch")
	// ErrReviewStale reports a review whose reviewed head OID no longer
	// matches the PR's live head: the PR changed under the reviewer.
	ErrReviewStale = errors.New("review does not match the pull request head")
	// ErrReviewerMismatch reports a review performed by a selection other
	// than the issue's routed reviewer (PRD §13: the audit record's
	// reviewer is the one that reviewed).
	ErrReviewerMismatch = errors.New("reviewer is not the routed reviewer")
	// ErrCINotReady reports required CI that is pending or failing where
	// a mergeable state (passing or no-checks) is required (PRD §16).
	ErrCINotReady = errors.New("required CI is not in a mergeable state")
	// ErrMergeApproval reports a merge approval that does not authorize
	// the merge: a wrong statement, a PR/head mismatch, or drift after
	// approval (PRD §8: approval pins one PR state).
	ErrMergeApproval = errors.New("merge approval does not authorize this merge")
	// ErrBodyTooLarge reports an audit body that cannot be brought under
	// GitHub's 65,536-character limit even after dropping detail text.
	ErrBodyTooLarge = errors.New("audit body exceeds GitHub's size limit")
	// ErrNotAllCleaned reports a complete attempted before every issue
	// reached the cleaned phase (PRD §7).
	ErrNotAllCleaned = errors.New("not every issue is cleaned")
)
