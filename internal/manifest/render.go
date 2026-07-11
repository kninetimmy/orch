package manifest

import (
	"encoding/json"
	"fmt"
	"strings"
)

// Render returns the full managed region for m as a single LF-terminated
// block with no trailing newline. It is a pure function of m — it never
// reads the clock — which is what makes Parse's drift check sound: the
// same m always yields the same bytes. It validates m first (an invalid
// manifest is a caller bug, reported plainly) and emits, in order, the
// begin marker, the human-readable markdown, the canonical-JSON data
// comment, and the end marker.
func Render(m Manifest) (string, error) {
	if err := m.validate(); err != nil {
		return "", fmt.Errorf("render manifest: %w", err)
	}
	// json.MarshalIndent keeps its default HTML escaping ON: "<", ">",
	// and "&" encode as \u003c, \u003e, \u0026, so no string field can
	// emit a literal "-->" or forge a marker inside the data comment.
	// Never swap this for a json.Encoder with SetEscapeHTML(false).
	data, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return "", fmt.Errorf("render manifest: encode JSON: %w", err)
	}

	var b strings.Builder
	b.WriteString(BeginMarker)
	b.WriteByte('\n')
	writeHuman(&b, m)
	b.WriteByte('\n')
	b.WriteString(dataOpen)
	b.WriteByte('\n')
	b.Write(data)
	b.WriteByte('\n')
	b.WriteString(dataClose)
	b.WriteByte('\n')
	b.WriteString(EndMarker)
	return b.String(), nil
}

// writeHuman renders the human-readable markdown between the begin
// marker and the data comment. Its shape is fixed: a heading, a summary
// table, the routing rationale as a paragraph (never a table cell), then
// the escalations and verifications sections, each always present so the
// layout is deterministic.
func writeHuman(b *strings.Builder, m Manifest) {
	b.WriteString("### Orch audit record\n\n")
	b.WriteString("| Field | Value |\n")
	b.WriteString("| --- | --- |\n")
	fmt.Fprintf(b, "| Role | %s |\n", mdCodeCell(string(m.Role)))
	fmt.Fprintf(b, "| Executor | %s — effort %s |\n", mdCodeCell(m.Executor.Model), mdCodeCell(m.Executor.Effort))
	fmt.Fprintf(b, "| Reviewer | %s — effort %s |\n", mdCodeCell(m.Reviewer.Model), mdCodeCell(m.Reviewer.Effort))
	fmt.Fprintf(b, "| Config revision | %s |\n", mdCodeCell(m.ConfigRevision))
	b.WriteByte('\n')
	fmt.Fprintf(b, "**Routing rationale:** %s\n", mdText(m.RoutingRationale))
	b.WriteByte('\n')
	writeEscalations(b, m.Escalations)
	b.WriteByte('\n')
	writeVerifications(b, m.Verifications)
}

func writeEscalations(b *strings.Builder, es []Escalation) {
	if len(es) == 0 {
		b.WriteString("**Escalations:** _none_\n")
		return
	}
	b.WriteString("**Escalations:**\n")
	for _, e := range es {
		b.WriteString(escalationBullet(e))
		b.WriteByte('\n')
	}
}

// escalationBullet renders one escalation as a single list line:
//
//   - <At> — <kind> (<role>): `from` (effort `e`) → `to` (effort `e`) — <reason>
//
// The "<At> — " prefix and the "(<role>)" segment are omitted when their
// fields are empty.
func escalationBullet(e Escalation) string {
	var b strings.Builder
	b.WriteString("- ")
	if e.At != "" {
		b.WriteString(mdText(e.At))
		b.WriteString(" — ")
	}
	b.WriteString(e.Kind)
	if e.Role != "" {
		fmt.Fprintf(&b, " (%s)", e.Role)
	}
	fmt.Fprintf(&b, ": %s (effort %s) → %s (effort %s) — %s",
		mdCode(e.From.Model), mdCode(e.From.Effort),
		mdCode(e.To.Model), mdCode(e.To.Effort),
		mdText(e.Reason))
	return b.String()
}

func writeVerifications(b *strings.Builder, vs []Verification) {
	if len(vs) == 0 {
		b.WriteString("**Verification:** _none_\n")
		return
	}
	b.WriteString("**Verification:**\n")
	for _, v := range vs {
		b.WriteString(verificationBullet(v))
		b.WriteByte('\n')
	}
}

// verificationBullet renders one verification as a single list line:
//
//   - **<name>** — <result> — `<command>` — <detail> (<at>)
//
// The command, detail, and timestamp segments are omitted when their
// fields are empty.
func verificationBullet(v Verification) string {
	var b strings.Builder
	fmt.Fprintf(&b, "- **%s** — %s", mdText(v.Name), mdText(v.Result))
	if v.Command != "" {
		fmt.Fprintf(&b, " — %s", mdCode(v.Command))
	}
	if v.Detail != "" {
		fmt.Fprintf(&b, " — %s", mdText(v.Detail))
	}
	if v.At != "" {
		fmt.Fprintf(&b, " (%s)", mdText(v.At))
	}
	return b.String()
}

// mdText escapes every free-text value bound for the human section
// (rationale, reason, detail, verification names and results, At
// timestamps) so no rendered line can equal a marker: "&", "<", ">"
// become entities (ampersand first, so the entity ampersands are not
// re-escaped), and raw carriage returns are dropped so Render output
// stays LF-only even when a field value carries CRLF (the JSON record
// still preserves the original value). A "-->" becomes "--&gt;" and
// "<!-- ... -->" loses its opening angle bracket, so injected text can
// neither forge a data-close nor a begin/end marker.
func mdText(s string) string {
	s = strings.ReplaceAll(s, "\r", "")
	s = strings.ReplaceAll(s, "&", "&amp;")
	s = strings.ReplaceAll(s, "<", "&lt;")
	s = strings.ReplaceAll(s, ">", "&gt;")
	return s
}

// mdCode renders an identifier (model, effort, command) as an inline
// code span safe outside a table cell.
func mdCode(s string) string {
	return codeSpan(s, false)
}

// mdCodeCell renders an identifier as an inline code span safe inside a
// GFM table cell: pipes are backslash-escaped so the row is not split.
func mdCodeCell(s string) string {
	return codeSpan(s, true)
}

// codeSpan wraps s in a CommonMark inline code span. Newlines collapse
// to spaces (a code span is single-line); the backtick fence is one
// longer than the longest backtick run in s so any interior backticks
// are literal; and the content is space-padded when it starts or ends
// with a backtick so the fence is unambiguous. In a table cell pipes are
// escaped to "\|", which GFM unescapes to a literal pipe inside the span.
func codeSpan(s string, inTable bool) string {
	s = strings.ReplaceAll(s, "\r\n", " ")
	s = strings.ReplaceAll(s, "\n", " ")
	s = strings.ReplaceAll(s, "\r", " ")
	if inTable {
		s = strings.ReplaceAll(s, "|", `\|`)
	}
	fence := strings.Repeat("`", longestBacktickRun(s)+1)
	pad := ""
	if strings.HasPrefix(s, "`") || strings.HasSuffix(s, "`") {
		pad = " "
	}
	return fence + pad + s + pad + fence
}

func longestBacktickRun(s string) int {
	longest, cur := 0, 0
	for _, r := range s {
		if r == '`' {
			cur++
			if cur > longest {
				longest = cur
			}
		} else {
			cur = 0
		}
	}
	return longest
}
