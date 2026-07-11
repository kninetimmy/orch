package instructions

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"strings"
)

// Action is the change Plan or PlanRemove proposes for a managed
// region. It is a closed enumeration.
type Action int

const (
	// ActionNone proposes no change: nothing to install, and the
	// existing region already matches the canonical render.
	ActionNone Action = iota
	// ActionInstall proposes appending a fresh CurrentVersion block
	// to content that carries no managed region.
	ActionInstall
	// ActionUpgrade proposes replacing an older-versioned block with
	// the CurrentVersion render, in place.
	ActionUpgrade
	// ActionRemove proposes deleting the managed region's marker
	// lines (PlanRemove only).
	ActionRemove
)

// Change is a proposed byte-exact edit: Old is the content before the
// change and New is the content after. Diff is unifiedDiff(Old, New)
// and is "" exactly when Old == New (ActionNone). FileExisted reports
// whether the file existed before the change — Plan infers this from
// content == "" standing in for "missing file"; PlanFile overrides it
// with the truth from its own read. DeleteWholeFile is set only by
// PlanRemove/PlanRemoveFile: it reports that New, once stripped of the
// managed region, is otherwise empty (IsOtherwiseEmpty), so the
// caller may choose to delete the file outright instead of writing a
// whitespace remainder.
type Change struct {
	Action          Action
	FileExisted     bool
	Old, New, Diff  string
	DeleteWholeFile bool
}

// Plan proposes a change for content's managed region: install when
// absent, upgrade when a valid older version is present, no-op when
// the region already matches the canonical render, and a fail-closed
// error for every other structural taxonomy row (StatusDrifted wraps
// ErrDrifted, StatusNewerVersion wraps ErrNewerVersion, StatusMalformed
// wraps ErrMalformed) — install/upgrade never proceed over content
// Inspect cannot trust. content == "" stands in for a missing file.
//
// Plan never writes anything; Apply does. There is deliberately no
// combined plan-and-apply entry point here (see Apply's doc comment).
func Plan(content string) (Change, error) {
	report, lines, loc, err := inspect(content)
	if err != nil {
		return Change{}, err
	}

	ch := Change{FileExisted: content != "", Old: content}
	switch report.Status {
	case StatusAbsent:
		ch.Action = ActionInstall
		newContent, err := installBlock(content)
		if err != nil {
			return Change{}, err
		}
		ch.New = newContent
	case StatusCurrent:
		ch.Action = ActionNone
		ch.New = content
	case StatusStale:
		// Unreachable today: see Report's StatusStale doc comment.
		// Kept so Plan is ready the day a version 2 exists.
		ch.Action = ActionUpgrade
		newContent, err := upgradeBlock(content, lines, loc)
		if err != nil {
			return Change{}, err
		}
		ch.New = newContent
	case StatusDrifted:
		return Change{}, fmt.Errorf("%w: %s", ErrDrifted, report.Detail)
	case StatusNewerVersion:
		return Change{}, fmt.Errorf("%w: %s", ErrNewerVersion, report.Detail)
	default:
		// Unreachable: inspect returns a non-nil err (handled above)
		// for every StatusMalformed outcome.
		return Change{}, fmt.Errorf("plan: unexpected status %d", report.Status)
	}

	ch.Diff = unifiedDiff(ch.Old, ch.New)
	return ch, nil
}

// PlanFile reads path and calls Plan on its content, then overrides
// the result's FileExisted with the truth from the read: a missing
// file is FileExisted == false regardless of what Plan inferred from
// an empty content string.
func PlanFile(path string) (Change, error) {
	content, existed, err := readOrAbsent(path)
	if err != nil {
		return Change{}, err
	}
	ch, err := Plan(content)
	if err != nil {
		return Change{}, err
	}
	ch.FileExisted = existed
	return ch, nil
}

// PlanRemove proposes removing content's managed region: no-op when
// no markers are present, ActionRemove when an unambiguous begin/end
// pair is found regardless of its version or whether its body has
// drifted (removal only needs the marker lines, not a trusted body —
// StatusDrifted and StatusNewerVersion are deliberately treated the
// same as StatusCurrent/StatusStale here), and a fail-closed error
// wrapping ErrMalformed when the markers themselves are structurally
// broken. content == "" stands in for a missing file.
func PlanRemove(content string) (Change, error) {
	lines := splitLines(content)
	loc, err := locate(lines)
	ch := Change{FileExisted: content != "", Old: content}

	if errors.Is(err, errNotFound) {
		ch.Action = ActionNone
		ch.New = content
		return ch, nil
	}
	if err != nil {
		// Already wraps ErrMalformed.
		return Change{}, err
	}

	begin := lines[loc.begin].start
	end := lines[loc.end].start + len(EndMarker)
	ch.Action = ActionRemove
	ch.New = content[:begin] + content[end:]
	ch.Diff = unifiedDiff(ch.Old, ch.New)
	ch.DeleteWholeFile = IsOtherwiseEmpty(ch.New)
	return ch, nil
}

// PlanRemoveFile reads path and calls PlanRemove on its content, then
// overrides the result's FileExisted with the truth from the read.
func PlanRemoveFile(path string) (Change, error) {
	content, existed, err := readOrAbsent(path)
	if err != nil {
		return Change{}, err
	}
	ch, err := PlanRemove(content)
	if err != nil {
		return Change{}, err
	}
	ch.FileExisted = existed
	return ch, nil
}

// IsOtherwiseEmpty reports whether content is empty once surrounding
// whitespace is discounted (strings.TrimSpace(content) == "").
// PlanRemove uses it to decide DeleteWholeFile: a file that held
// nothing but the managed region and blank lines around it can be
// deleted outright instead of left behind as whitespace.
func IsOtherwiseEmpty(content string) bool {
	return strings.TrimSpace(content) == ""
}

// readOrAbsent reads path, reporting existed == false (and a zero
// error) for a missing file, mirroring InspectFile's own absent case.
// Any other read failure is a non-nil error naming the path.
func readOrAbsent(path string) (content string, existed bool, err error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return "", false, nil
	}
	if err != nil {
		return "", false, fmt.Errorf("read %s: %w", path, err)
	}
	return string(data), true, nil
}

// installBlock returns content with the CurrentVersion block appended,
// manifest.Upsert's append-path shape: empty content yields the block
// alone; otherwise a trailing newline is added if missing, then a
// blank line, then the block.
func installBlock(content string) (string, error) {
	block, err := Render(CurrentVersion)
	if err != nil {
		// Unreachable: CurrentVersion always has a canonicalBody
		// entry. Fail closed regardless.
		return "", fmt.Errorf("plan install: %w", err)
	}
	if content == "" {
		return block, nil
	}
	var b strings.Builder
	b.WriteString(content)
	if !strings.HasSuffix(content, "\n") {
		b.WriteByte('\n')
	}
	b.WriteByte('\n')
	b.WriteString(block)
	return b.String(), nil
}

// upgradeBlock replaces an existing region in place with the
// CurrentVersion render, using manifest.Upsert's replace-path byte
// offsets exactly: content[:begin] + Render(CurrentVersion) +
// content[end:], where end is the start of the end marker's line plus
// len(EndMarker) — the end marker's own line terminator, and
// everything after it, stays untouched in the suffix. Bytes outside
// the region are preserved verbatim regardless of their line endings;
// the newly-written block itself is always the LF-only bytes Render
// produces.
func upgradeBlock(content string, lines []line, loc location) (string, error) {
	block, err := Render(CurrentVersion)
	if err != nil {
		// Unreachable: see installBlock.
		return "", fmt.Errorf("plan upgrade: %w", err)
	}
	begin := lines[loc.begin].start
	end := lines[loc.end].start + len(EndMarker)
	return content[:begin] + block + content[end:], nil
}
