// Package instructions renders, detects, upgrades, and removes the
// Orch-managed instruction block inside a repository's root
// AGENTS.md/CLAUDE.md file (PRD §19):
//
//	<!-- orchestrator:managed:start version=1 -->
//	...engine-owned block content...
//	<!-- orchestrator:managed:end -->
//
// Every byte outside the region is human-owned and preserved verbatim
// by Plan/PlanFile — the same exact-line-marker, byte-splice discipline
// internal/manifest applies to issue and PR bodies. The engine owns
// the canonical content for each block version (block.go); Inspect
// classifies an existing region against it without ever erroring, and
// Plan/PlanRemove turn that classification into a proposed byte-exact
// change plus a unified diff, leaving approval and the actual write to
// the caller (apply.go). Conflicts found outside the two managed files
// (scan.go) are reported, never edited — nested files are someone
// else's to fix.
//
// The package is stdlib-only and policy-free: it trusts whatever path
// it is handed. It does not canonicalize paths, does not check
// containment under a repository root, and does not decide which
// files count as "the" root AGENTS.md/CLAUDE.md. Building safe paths
// with paths.FindRoot and filepath.Join, and keeping this package's
// writes confined to files a human or `orch init`/`configure`
// (tasks 15/16) explicitly named, is entirely the caller's
// responsibility.
package instructions

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
)

// Status classifies a managed region relative to the engine's
// canonical content. It is a closed enumeration; inspect/classify are
// its only producers.
type Status int

const (
	// StatusAbsent reports that content carries no managed region at
	// all: neither marker is present.
	StatusAbsent Status = iota
	// StatusCurrent reports a region at CurrentVersion whose body
	// matches the canonical render for that version (compared after
	// normalizing CRLF to LF within the region only, manifest.Parse's
	// idiom, so a checkout-time line-ending conversion is not a drift).
	StatusCurrent
	// StatusStale reports a valid region whose marker version is
	// older than CurrentVersion. Unreachable today (see block.go's
	// classify): CurrentVersion is 1 and the begin-marker grammar
	// accepts only version >= 1, so no real content can produce it
	// until a version 2 exists.
	StatusStale
	// StatusDrifted reports a region at CurrentVersion whose body
	// does not match the canonical render even after CRLF
	// normalization — a hand edit.
	StatusDrifted
	// StatusNewerVersion reports a region whose marker version is
	// newer than CurrentVersion: this build does not know that
	// version's canonical content and fails closed rather than guess.
	StatusNewerVersion
	// StatusMalformed reports a structural marker problem: a
	// duplicate, nested, misordered, or unpaired begin/end marker, or
	// a begin marker whose version overflows strconv.Atoi.
	StatusMalformed
)

// Report is Inspect's pure classification of a managed region: never
// an error, always a Status plus enough Detail for a human message.
// Version is meaningful for every Status except StatusAbsent.
type Report struct {
	Status  Status
	Version int
	Detail  string
}

// Blocking reports whether r represents a structural conflict Plan
// refuses to proceed past: Malformed, Drifted, or NewerVersion.
// PlanRemove does not consult Blocking — removal needs only
// unambiguous marker lines, not a trusted body, so it treats Drifted
// and NewerVersion the same as Current and Stale (see PlanRemove).
func (r Report) Blocking() bool {
	switch r.Status {
	case StatusMalformed, StatusDrifted, StatusNewerVersion:
		return true
	default:
		return false
	}
}

// Inspect classifies content's managed region. It is pure and never
// errors: a malformed region is StatusMalformed with the problem
// described in Detail, not an error return, so callers can always
// display "here's what's going on" without a preceding error check.
func Inspect(content string) Report {
	report, _, _, _ := inspect(content)
	return report
}

// InspectFile reads path and reports Inspect of its content. A
// missing file is StatusAbsent with a nil error, mirroring Inspect's
// own absent case for content that was never written. Any other read
// failure is a non-nil error naming the path.
func InspectFile(path string) (Report, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return Report{Status: StatusAbsent}, nil
	}
	if err != nil {
		return Report{}, fmt.Errorf("read %s: %w", path, err)
	}
	return Inspect(string(data)), nil
}

// inspect is the shared core of Inspect and Plan: it locates the
// managed region and classifies it, returning the split lines and
// location alongside so Plan can reuse them for its Upgrade/Remove
// splices without locating twice. The returned error is nil except
// when locate reports a structural problem, in which case it already
// wraps ErrMalformed: Inspect discards it into the Report's Detail
// (Inspect never errors), while Plan propagates it so callers can
// errors.Is against ErrMalformed.
func inspect(content string) (Report, []line, location, error) {
	lines := splitLines(content)
	loc, err := locate(lines)
	if errors.Is(err, errNotFound) {
		return Report{Status: StatusAbsent}, lines, location{}, nil
	}
	if err != nil {
		return Report{Status: StatusMalformed, Detail: err.Error()}, lines, location{}, err
	}
	return classify(content, lines, loc), lines, loc, nil
}
