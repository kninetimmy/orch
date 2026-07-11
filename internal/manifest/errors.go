package manifest

import "errors"

// Sentinel errors callers test with errors.Is. Each marks a distinct
// fail-closed condition Parse detects structurally; specifics wrap these
// with %w and a remediation hint (state.go style) rather than replace
// them.
var (
	// ErrNoManifest reports that the body carries no manifest region:
	// neither marker is present. Upsert treats this as "append", Parse
	// as "nothing to read".
	ErrNoManifest = errors.New("no manifest region in body")
	// ErrBadManifest reports a region that is present but unusable:
	// mispaired markers, a missing/unterminated/duplicated data comment,
	// unparsable JSON, an unsupported schema_version, or a decoded record
	// that fails validation.
	ErrBadManifest = errors.New("manifest region is malformed")
	// ErrDrift reports that the region does not match the canonical
	// render of its own JSON record — the managed region was hand-edited
	// inconsistently and cannot be trusted (PRD §15: fail closed).
	ErrDrift = errors.New("manifest region does not match its canonical record")
)
