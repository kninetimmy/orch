package instructions

import (
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// EndMarker delimits the end of the managed region. A line is the end
// marker only if, after stripping at most one trailing "\r", it equals
// this string exactly (the internal/manifest marker-matching idiom):
// a mid-line mention in prose is ordinary content, never a match.
const EndMarker = "<!-- orchestrator:managed:end -->"

// beginFormat is the fmt.Sprintf format Render uses to write a begin
// marker line for a given version. beginPattern is the regexp that
// recognizes one. The two must stay in lockstep — every version
// Render writes must match beginPattern's grammar, which block.go's
// tests assert directly.
const beginFormat = "<!-- orchestrator:managed:start version=%d -->"

// beginPattern matches a begin-marker line after the same \r-stripping
// as EndMarker: the literal prefix/suffix around a version with no
// leading zero and no sign. Near-miss lines — wrong spacing, a missing
// version, or a leading zero — simply do not match and are ordinary
// content; that is a deliberate, documented trade-off (manifest's
// exact-match idiom), not a bug. A begin marker line inside a fenced
// code block is not distinguished from a real one either: line
// scanning knows nothing about fences, so a fenced exact marker is
// still counted (see the hostile fixture test in plan_test.go, which
// turns that into a real StatusMalformed).
var beginPattern = regexp.MustCompile(`^<!-- orchestrator:managed:start version=([1-9][0-9]*) -->$`)

// line is one physical line of content: its start byte offset, its
// text up to but excluding the terminating "\n" (a trailing "\r" is
// kept so marker matching can strip it), and the offset just past the
// "\n" (or the end of content for a final line without a newline).
type line struct {
	start   int
	text    string
	fullEnd int
}

// splitLines splits content on "\n". A final "\n" does not yield a
// trailing empty line; empty content yields no lines.
func splitLines(content string) []line {
	var lines []line
	start := 0
	for i := 0; i < len(content); i++ {
		if content[i] == '\n' {
			lines = append(lines, line{start: start, text: content[start:i], fullEnd: i + 1})
			start = i + 1
		}
	}
	if start < len(content) {
		lines = append(lines, line{start: start, text: content[start:], fullEnd: len(content)})
	}
	return lines
}

// location is the line indices of the begin and end markers bounding
// the single valid managed region, plus the version the begin marker
// declared.
type location struct {
	begin, end, version int
}

// errNotFound is locate's private sentinel for "no markers at all".
// Inspect turns it into StatusAbsent rather than a caller-visible
// error; it never escapes this package.
var errNotFound = errors.New("no managed region markers found")

// locate finds the one begin marker preceding the one end marker and
// parses the begin marker's declared version. Zero markers of either
// kind is errNotFound (becomes StatusAbsent); any other marker count
// or ordering — duplicate begins, duplicate ends, an end before a
// begin, or a begin/end with no partner — is ErrMalformed. A begin
// marker whose version overflows strconv.Atoi is also ErrMalformed,
// even when it is the sole marker pair found.
func locate(lines []line) (location, error) {
	var begins, ends, versions []int
	for i, l := range lines {
		text := strings.TrimSuffix(l.text, "\r")
		if text == EndMarker {
			ends = append(ends, i)
			continue
		}
		if m := beginPattern.FindStringSubmatch(text); m != nil {
			v, err := strconv.Atoi(m[1])
			if err != nil {
				return location{}, fmt.Errorf("%w: begin marker version %q is not a representable integer: %v", ErrMalformed, m[1], err)
			}
			begins = append(begins, i)
			versions = append(versions, v)
		}
	}
	if len(begins) == 0 && len(ends) == 0 {
		return location{}, errNotFound
	}
	if len(begins) == 1 && len(ends) == 1 && begins[0] < ends[0] {
		return location{begin: begins[0], end: ends[0], version: versions[0]}, nil
	}
	return location{}, fmt.Errorf("%w: found %d begin and %d end markers (want exactly one begin marker before one end marker)", ErrMalformed, len(begins), len(ends))
}

// regionText returns the full managed block — from the begin marker's
// first byte through the end marker's last byte, excluding the end
// marker's own line terminator — with no normalization here. classify
// CRLF-normalizes the extracted region (only) before its drift
// compare, manifest.Parse's idiom, so a checkout-time CRLF conversion
// is not misreported as a hand edit while any other byte difference
// still is.
func regionText(content string, lines []line, loc location) string {
	start := lines[loc.begin].start
	end := lines[loc.end].start + len(EndMarker)
	return content[start:end]
}
