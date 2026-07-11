package manifest

import (
	"encoding/json"
	"fmt"
	"strings"
)

// Parse locates the managed region in body, decodes its canonical JSON,
// and fails closed on any drift. It returns ErrNoManifest when no region
// is present, ErrBadManifest when the region is present but unusable
// (mispaired markers, a broken data comment, unparsable JSON, an
// unsupported schema_version, or a record that fails validation), and
// ErrDrift when the region does not byte-match the canonical render of
// its own decoded record.
//
// Only the extracted region is CRLF-normalized ("\r\n"→"\n") before the
// decode and drift compare; bytes outside the region are never touched.
// A lone "\r" inside the region survives normalization and fails the
// compare, which is the intended fail-closed behavior.
func Parse(body string) (Manifest, error) {
	lines := splitLines(body)
	loc, err := locate(lines)
	if err != nil {
		return Manifest{}, err
	}

	region := regionText(body, lines, loc)
	jsonText, err := extractData(region)
	if err != nil {
		return Manifest{}, err
	}

	var m Manifest
	if err := json.Unmarshal([]byte(jsonText), &m); err != nil {
		return Manifest{}, fmt.Errorf("%w: decode JSON: %v", ErrBadManifest, err)
	}
	if err := m.validate(); err != nil {
		return Manifest{}, fmt.Errorf("%w: %v", ErrBadManifest, err)
	}

	rendered, err := Render(m)
	if err != nil {
		// Unreachable: validate already passed. Fail closed regardless.
		return Manifest{}, fmt.Errorf("%w: %v", ErrBadManifest, err)
	}
	if region != rendered {
		return Manifest{}, fmt.Errorf("%w (the managed region was hand-edited; regenerate it from the run record)", ErrDrift)
	}
	return m, nil
}

// line is one physical line of the body: its start byte offset, its text
// up to but excluding the terminating "\n" (a trailing "\r" is kept so
// marker matching can strip it), and the offset just past the "\n" (or
// the end of body for a final line without a newline).
type line struct {
	start   int
	text    string
	fullEnd int
}

// splitLines splits body on "\n". A final "\n" does not yield a trailing
// empty line; an empty body yields no lines.
func splitLines(body string) []line {
	var lines []line
	start := 0
	for i := 0; i < len(body); i++ {
		if body[i] == '\n' {
			lines = append(lines, line{start: start, text: body[start:i], fullEnd: i + 1})
			start = i + 1
		}
	}
	if start < len(body) {
		lines = append(lines, line{start: start, text: body[start:], fullEnd: len(body)})
	}
	return lines
}

// location is the line indices of the begin and end markers bounding the
// single valid region.
type location struct{ begin, end int }

// locate finds the one begin marker preceding the one end marker.
// Zero markers is ErrNoManifest; any other count or ordering is
// ErrBadManifest.
func locate(lines []line) (location, error) {
	var begins, ends []int
	for i, l := range lines {
		switch strings.TrimSuffix(l.text, "\r") {
		case BeginMarker:
			begins = append(begins, i)
		case EndMarker:
			ends = append(ends, i)
		}
	}
	if len(begins) == 0 && len(ends) == 0 {
		return location{}, ErrNoManifest
	}
	if len(begins) == 1 && len(ends) == 1 && begins[0] < ends[0] {
		return location{begin: begins[0], end: ends[0]}, nil
	}
	return location{}, fmt.Errorf("%w: found %d begin and %d end markers (want exactly one begin marker before one end marker)", ErrBadManifest, len(begins), len(ends))
}

// regionText returns the region — from the begin marker's first byte
// through the end marker's last byte, excluding the end marker's line
// terminator — with its interior CRLF pairs normalized to LF. The result
// is directly comparable to Render output, which carries no trailing
// newline.
func regionText(body string, lines []line, loc location) string {
	start := lines[loc.begin].start
	end := lines[loc.end].start + len(EndMarker)
	return strings.ReplaceAll(body[start:end], "\r\n", "\n")
}

// extractData returns the canonical JSON text from a normalized region:
// the lines between the single dataOpen line and the first following
// dataClose line. Missing, unterminated, or duplicated data comments are
// ErrBadManifest.
func extractData(region string) (string, error) {
	rlines := strings.Split(region, "\n")
	openCount, openIdx := 0, -1
	for i, l := range rlines {
		if l == dataOpen {
			openCount++
			openIdx = i
		}
	}
	if openCount == 0 {
		return "", fmt.Errorf("%w: missing %q data comment", ErrBadManifest, dataOpen)
	}
	if openCount > 1 {
		return "", fmt.Errorf("%w: %d %q data comments (want exactly one)", ErrBadManifest, openCount, dataOpen)
	}
	for i := openIdx + 1; i < len(rlines); i++ {
		if rlines[i] == dataClose {
			return strings.Join(rlines[openIdx+1:i], "\n"), nil
		}
	}
	return "", fmt.Errorf("%w: unterminated data comment (no %q line after %q)", ErrBadManifest, dataClose, dataOpen)
}
