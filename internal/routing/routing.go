// Package routing is Orch's pure role-routing and escalation policy
// core (PRD §9 roles, §10 default profiles, §11 routing and
// escalation). Given a host Profile, a Task's read-only/risk/difficulty
// facts, and the History of prior attempts, Decide picks the role,
// executor, and reviewer for a fresh routing, and Escalate applies the
// PRD §11 escalation rules when a running attempt reports trouble.
//
// The package is deliberately narrow and fail-closed. It performs no
// I/O, reads no clock, and imports only internal/manifest, whose
// Role/Selection/Escalation types it emits verbatim so the run engine
// can record a decision in a PRD §13 audit manifest without a
// translation layer. It never ranks model strength: model identifiers
// are opaque strings and it trusts the profile's role semantics (config
// owns the model vocabulary). Degenerate profiles — where a stronger
// role's model equals one that has already failed — are caught by
// string equality on the failed set and resolved by returning to the
// Architect, never by guessing at a strength order.
package routing

import (
	"errors"
	"fmt"

	"github.com/kninetimmy/orch/internal/manifest"
)

// Profile is the per-host model/effort assignment routing consumes. Its
// six selections mirror config.Roles (PRD §10) one-for-one, but routing
// does not import config: the run engine maps each config.RoleProfile
// to a manifest.Selection and hands the result here, keeping this
// package off the TOML chain.
type Profile struct {
	Architect       manifest.Selection
	Scout           manifest.Selection
	Implementer     manifest.Selection
	Specialist      manifest.Selection
	Reviewer        manifest.Selection
	ReviewDowngrade manifest.Selection
}

// Sentinel errors callers test with errors.Is. Specifics wrap these
// with %w and a remediation hint (state.go / manifest.go house style)
// rather than replace them.
var (
	// ErrBadProfile reports a Profile with an incomplete selection: some
	// role is missing its model or effort. Routing cannot emit a valid
	// audit record from it, so it refuses rather than guess a default.
	ErrBadProfile = errors.New("routing profile is incomplete")
	// ErrBadTask reports a Task carrying an unknown risk domain — a value
	// outside the closed PRD §11 set (see Domains).
	ErrBadTask = errors.New("routing task is invalid")
	// ErrBadTrigger reports an Escalate call with a trigger outside the
	// closed set (see the Trigger constants).
	ErrBadTrigger = errors.New("escalation trigger is unknown")
	// ErrTriggerMismatch reports an escalation trigger applied to a
	// decision it does not fit — e.g. scout-uncertainty on a non-scout
	// decision, or reviewer-uncertainty on a decision whose reviewer was
	// never downgraded.
	ErrTriggerMismatch = errors.New("escalation trigger does not match the decision")
	// ErrNoStrongerRoute reports that initial routing needs a stronger
	// executor than the profile can supply because the only candidate has
	// already failed. Unlike an escalation exhaustion (which returns to
	// the Architect), this is a plan-gate problem surfaced at Decide time.
	ErrNoStrongerRoute = errors.New("no stronger executor route remains")
)

// validate reports the first Profile completeness violation, wrapping
// ErrBadProfile with the offending field. Both entry points reject an
// invalid profile before making any decision (fail closed).
func (p Profile) validate() error {
	for _, f := range []struct {
		name string
		sel  manifest.Selection
	}{
		{"architect", p.Architect},
		{"scout", p.Scout},
		{"implementer", p.Implementer},
		{"specialist", p.Specialist},
		{"reviewer", p.Reviewer},
		{"review_downgrade", p.ReviewDowngrade},
	} {
		if f.sel.Model == "" {
			return fmt.Errorf("%w: %s.model is empty", ErrBadProfile, f.name)
		}
		if f.sel.Effort == "" {
			return fmt.Errorf("%w: %s.effort is empty", ErrBadProfile, f.name)
		}
	}
	return nil
}
