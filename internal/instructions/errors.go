package instructions

import "errors"

// Sentinel errors callers test with errors.Is. Each marks a distinct
// fail-closed structural condition; specifics wrap these with %w and a
// human-readable detail (manifest/state style) rather than replace
// them.
var (
	// ErrMalformed reports a managed region that is present but
	// structurally unusable: a duplicate, nested, misordered, or
	// unpaired begin/end marker, or a begin marker whose version
	// overflows strconv.Atoi. Plan and PlanRemove both fail closed on
	// it.
	ErrMalformed = errors.New("managed region is malformed")
	// ErrDrifted reports a region at CurrentVersion whose body does
	// not byte-match the canonical render for that version — the
	// block was hand-edited. Plan fails closed on it; PlanRemove does
	// not (removal needs only the marker lines, not a trusted body).
	ErrDrifted = errors.New("managed region body has drifted from the canonical render")
	// ErrNewerVersion reports a region whose marker declares a
	// version newer than CurrentVersion. This build has no canonical
	// content for that version to compare or upgrade to, so Plan
	// fails closed rather than guess; PlanRemove does not (the marker
	// lines alone are enough to remove the region).
	ErrNewerVersion = errors.New("managed region declares a version newer than this build knows")
)
