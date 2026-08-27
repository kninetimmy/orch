package manifest

import (
	"encoding/json"
	"fmt"
	"slices"
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
// marker and the data comment. Its shape is fixed and follows the JSON
// record's field order: a heading, the approved work, a summary table,
// the routing rationale as a paragraph (never a table cell), then the
// escalations and verifications sections, each always present so the
// layout is deterministic.
func writeHuman(b *strings.Builder, m Manifest) {
	b.WriteString("### Orch audit record\n\n")
	writeWork(b, m)
	b.WriteString("| Field | Value |\n")
	b.WriteString("| --- | --- |\n")
	fmt.Fprintf(b, "| Role | %s |\n", mdCodeCell(string(m.Role)))
	fmt.Fprintf(b, "| Executor | %s |\n", selectionCell(m.Executor))
	fmt.Fprintf(b, "| Reviewer | %s |\n", selectionCell(m.Reviewer))
	deliveryLabel := "Effort delivery"
	if m.EffortDelivery == EffortDeliveryModelVariant {
		deliveryLabel = "Profile delivery"
	}
	fmt.Fprintf(b, "| %s | %s — %s |\n", deliveryLabel, mdCodeCell(string(m.EffortDelivery)), effortDeliveryNote(m.EffortDelivery))
	fmt.Fprintf(b, "| Config revision | %s |\n", mdCodeCell(m.ConfigRevision))
	b.WriteByte('\n')
	fmt.Fprintf(b, "**Routing rationale:** %s\n", mdText(m.RoutingRationale))
	b.WriteByte('\n')
	writeEscalations(b, m.Escalations)
	b.WriteByte('\n')
	writeVerifications(b, m.Verifications)
}

// CIDoesNotRunNote is the clause rendered after a required test the
// record names in Manifest.TestsCIDoesNotRun, leading separator
// included so every renderer emits the same bytes. It is exported
// because the run engine writes the created issue's prose required-tests
// list above this managed region and must annotate it identically: a
// reader comparing the two halves of one issue body must not find one
// half saying CI does not run a test and the other silent about it.
const CIDoesNotRunNote = " — CI does not run this test"

// writeWork renders the approved work the record carries: the objective
// as a paragraph, then the acceptance criteria and the required tests as
// bullet lists (tests are commands, so they render as code spans). A
// required test the record names in TestsCIDoesNotRun carries
// CIDoesNotRunNote after its command.
//
// It repeats prose the created issue body already carries above the
// managed region, deliberately: the drift check compares a re-render of
// the decoded record against the found region, so a field with no human
// view is a field whose only view is the hidden JSON, and every one of
// these carries text an executor is handed as approved.
func writeWork(b *strings.Builder, m Manifest) {
	fmt.Fprintf(b, "**Objective:** %s\n\n", mdText(m.Objective))
	b.WriteString("**Acceptance criteria:**\n")
	for _, ac := range m.AcceptanceCriteria {
		fmt.Fprintf(b, "- %s\n", mdText(ac))
	}
	b.WriteString("\n**Required tests:**\n")
	for _, rt := range m.RequiredTests {
		fmt.Fprintf(b, "- %s", mdCode(rt))
		if slices.Contains(m.TestsCIDoesNotRun, rt) {
			b.WriteString(CIDoesNotRunNote)
		}
		b.WriteByte('\n')
	}
	b.WriteByte('\n')
}

// effortDeliveryNote is the clause rendered beside an EffortDelivery value in
// the summary table. Without it the table does not say what the host enforced.
func effortDeliveryNote(d EffortDelivery) string {
	switch d {
	case EffortDeliveryParameter:
		return "the host applied the routed effort as a real model parameter"
	case EffortDeliveryPromptCue:
		return "this host has no effort parameter; the routed effort reached the executor as a prompt cue only"
	case EffortDeliveryModelVariant:
		return "OpenCode applies a selected variant as #variant and leaves a no-variant model reference bare"
	default:
		// Unreachable: validate rejects any other value before Render.
		return "unrecognized effort delivery"
	}
}

func selectionCell(s Selection) string {
	switch {
	case s.Effort != "":
		return fmt.Sprintf("%s — effort %s", mdCodeCell(s.Model), mdCodeCell(s.Effort))
	case s.Variant != "":
		return fmt.Sprintf("%s — variant %s", mdCodeCell(s.Reference()), mdCodeCell(s.Variant))
	default:
		return fmt.Sprintf("%s — no variant", mdCodeCell(s.Model))
	}
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
//   - <At> — <kind> (<role>): <from selection> → <to selection> — <reason>
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
	fmt.Fprintf(&b, ": %s → %s — %s", selectionInline(e.From), selectionInline(e.To), mdText(e.Reason))
	return b.String()
}

func selectionInline(s Selection) string {
	switch {
	case s.Effort != "":
		return fmt.Sprintf("%s (effort %s)", mdCode(s.Model), mdCode(s.Effort))
	case s.Variant != "":
		return fmt.Sprintf("%s (variant %s)", mdCode(s.Reference()), mdCode(s.Variant))
	default:
		return mdCode(s.Model) + " (no variant)"
	}
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
//   - **<name>** — <result> — `<command>` — <detail> — commit `<oid>` (<at>)
//
// The command, detail, commit, and timestamp segments are omitted when
// their fields are empty. The commit appears here and not only in the
// canonical JSON deliberately: the drift check compares a re-render
// against the found region, so a field with no human view is a field
// whose only view is the hidden data comment — and this one is what
// tells a reader whether the evidence above it was gathered at the head
// that merged or at an earlier one.
func verificationBullet(v Verification) string {
	var b strings.Builder
	fmt.Fprintf(&b, "- **%s** — %s", mdText(v.Name), mdText(v.Result))
	if v.Command != "" {
		fmt.Fprintf(&b, " — %s", mdCode(v.Command))
	}
	if v.Detail != "" {
		fmt.Fprintf(&b, " — %s", mdText(v.Detail))
	}
	if v.CommitOID != "" {
		fmt.Fprintf(&b, " — commit %s", mdCode(v.CommitOID))
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
