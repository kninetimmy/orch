package manifest

import (
	"errors"
	"strings"
)

// Upsert returns body with m's managed region either replaced in place
// (when a valid region is present) or appended after a blank line (when
// none is present). Bytes outside the region are preserved verbatim, so
// the human-owned prose, prefix, and suffix — including their CRLF line
// endings — survive unchanged.
//
// An invalid manifest propagates Render's validation error and a
// malformed existing region propagates ErrBadManifest, in both cases
// without rewriting body. Upsert is idempotent:
// Upsert(Upsert(b, m), m) == Upsert(b, m).
func Upsert(body string, m Manifest) (string, error) {
	region, err := Render(m)
	if err != nil {
		return "", err
	}

	lines := splitLines(body)
	loc, err := locate(lines)
	if errors.Is(err, ErrNoManifest) {
		return appendRegion(body, region), nil
	}
	if err != nil {
		return "", err
	}

	// Replace the region in place. The end marker's line terminator and
	// everything after it stay in the suffix, so a CRLF suffix is
	// preserved byte-for-byte.
	begin := lines[loc.begin].start
	end := lines[loc.end].start + len(EndMarker)
	return body[:begin] + region + body[end:], nil
}

// appendRegion appends region to body separated by a blank line,
// fixing a missing final newline first. An empty body yields the region
// alone, which keeps Upsert idempotent.
func appendRegion(body, region string) string {
	if body == "" {
		return region
	}
	var b strings.Builder
	b.WriteString(body)
	if !strings.HasSuffix(body, "\n") {
		b.WriteByte('\n')
	}
	b.WriteByte('\n')
	b.WriteString(region)
	return b.String()
}
