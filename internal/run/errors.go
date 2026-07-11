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
)
