package instructions

import (
	"fmt"
	"strings"
)

// CurrentVersion is the managed-block schema this build renders and
// recognizes as current. Only version 1 exists today; a future
// version is added by extending canonicalBody's registry, never by
// mutating an existing version's body — Inspect's drift and stale
// classification for old content depends on old bodies staying
// byte-stable forever.
const CurrentVersion = 1

// bodyV1 is the engine-owned body of the version-1 managed block (PRD
// §19). Its wording is reviewable at PR time; its shape is fixed:
// LF-only, no trailing newline, no line that could ever equal
// EndMarker or match beginPattern (block_test.go asserts this, so the
// body itself can never forge a marker and needs no escaping
// machinery).
const bodyV1 = "This file is partially managed by Orch (see `.orchestrator/config.toml`).\n" +
	"- In **Assist** mode, tracked-file changes are mechanically denied; a mutating\n" +
	"  request triggers read-only planning instead.\n" +
	"- In **Delivery** mode, work happens in an isolated per-issue worktree, never in\n" +
	"  this checkout directly.\n" +
	"- Model/effort routing, concurrency, and host plugin setup live in\n" +
	"  `.orchestrator/config.toml` — edit that file, not this block.\n" +
	"- Orch upgrades this block through Delivery. Do not hand-edit it; a hand edit\n" +
	"  blocks the next install/upgrade until reverted or removed."

// canonicalBody returns the engine-owned body text for version, or
// false if version is not a schema this build knows. Adding a version
// means adding a case here, never touching an existing one.
func canonicalBody(version int) (string, bool) {
	switch version {
	case 1:
		return bodyV1, true
	default:
		return "", false
	}
}

// Render returns the full managed block for version — begin marker,
// canonical body, end marker — as LF-only text with no trailing
// newline, or an error if version is unknown. It is pure: the same
// version always yields the same bytes, which is what makes Inspect's
// drift check sound (classify byte-compares a found region against
// this output).
func Render(version int) (string, error) {
	body, ok := canonicalBody(version)
	if !ok {
		return "", fmt.Errorf("render managed block: unknown version %d", version)
	}
	begin := fmt.Sprintf(beginFormat, version)
	return begin + "\n" + body + "\n" + EndMarker, nil
}

// classify turns a located region into a Report by comparing its
// declared version against CurrentVersion and, when they match, its
// exact bytes against Render's canonical output.
//
// Reaching the StatusStale branch requires loc.version < CurrentVersion,
// which no real content can produce today: locate's begin regexp
// accepts only version >= 1, and CurrentVersion is 1. block_test.go
// exercises the branch directly with a synthetic location{version: 0}
// — it earns real coverage the day CurrentVersion becomes 2.
func classify(content string, lines []line, loc location) Report {
	switch {
	case loc.version < CurrentVersion:
		return Report{
			Status:  StatusStale,
			Version: loc.version,
			Detail:  fmt.Sprintf("block is version %d; current is %d", loc.version, CurrentVersion),
		}
	case loc.version > CurrentVersion:
		return Report{
			Status:  StatusNewerVersion,
			Version: loc.version,
			Detail:  fmt.Sprintf("block declares version %d, newer than the %d this build knows how to render", loc.version, CurrentVersion),
		}
	}

	canonical, err := Render(loc.version)
	if err != nil {
		// Unreachable: loc.version == CurrentVersion here, and
		// CurrentVersion always has a canonicalBody entry. Fail
		// closed regardless.
		return Report{Status: StatusMalformed, Version: loc.version, Detail: err.Error()}
	}
	// A checkout-time CRLF conversion (core.autocrlf in a repo that
	// does not pin eol=lf) is not a hand edit: normalize \r\n to \n in
	// the extracted region only before the compare, manifest.Parse's
	// exact idiom. A lone \r that is not part of a CRLF pair survives
	// normalization and still fails the compare — genuinely mangled
	// bytes stay Drifted.
	region := strings.ReplaceAll(regionText(content, lines, loc), "\r\n", "\n")
	if region != canonical {
		return Report{
			Status:  StatusDrifted,
			Version: loc.version,
			Detail:  "block body does not match the canonical render for this version",
		}
	}
	return Report{Status: StatusCurrent, Version: loc.version}
}
