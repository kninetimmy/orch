package question

import (
	"fmt"
	"strings"
)

// maxHeaderLen is the display-label length cap SpecCheck enforces
// (see Question.Header's doc comment).
const maxHeaderLen = 12

// SpecCheck validates a Question the engine is about to emit for
// well-formedness: id/prompt present, header within maxHeaderLen,
// select questions carry 2-4 options with unique values, text
// questions carry none, and a non-empty Default names one of the
// options unless FreeText admits values outside them. A Question that
// fails SpecCheck is a bug in the engine itself, not bad user input,
// so — unlike run.PlanDoc.Validate's "collect every problem" style —
// SpecCheck reports the first problem found and stops.
func SpecCheck(q Question) error {
	if q.ID == "" {
		return fmt.Errorf("question: id must not be empty")
	}
	if len(q.Header) > maxHeaderLen {
		return fmt.Errorf("question %s: header %q exceeds %d characters", q.ID, q.Header, maxHeaderLen)
	}
	if q.Prompt == "" {
		return fmt.Errorf("question %s: prompt must not be empty", q.ID)
	}

	switch q.Kind {
	case KindText:
		if len(q.Options) > 0 {
			return fmt.Errorf("question %s: kind text must not carry options", q.ID)
		}
		return nil
	case KindSelect:
		return specCheckSelect(q)
	default:
		return fmt.Errorf("question %s: unknown kind %q", q.ID, q.Kind)
	}
}

// specCheckSelect is SpecCheck's KindSelect branch.
func specCheckSelect(q Question) error {
	if len(q.Options) < 2 || len(q.Options) > 4 {
		return fmt.Errorf("question %s: select needs 2-4 options, got %d", q.ID, len(q.Options))
	}
	seen := map[string]bool{}
	for _, o := range q.Options {
		if o.Value == "" {
			return fmt.Errorf("question %s: option value must not be empty", q.ID)
		}
		if seen[o.Value] {
			return fmt.Errorf("question %s: duplicate option value %q", q.ID, o.Value)
		}
		seen[o.Value] = true
	}
	if q.Default != "" && !q.FreeText && !seen[q.Default] {
		return fmt.Errorf("question %s: default %q is not one of the options", q.ID, q.Default)
	}
	return nil
}

// ValidateAnswer reports whether value is an acceptable answer to q:
// for KindSelect, value must equal one of q.Options' values unless
// q.FreeText admits any other non-blank value; for KindText, value
// must be non-empty after strings.TrimSpace. The returned error names
// the question id and, for a select, the allowed values — that exact
// string is the message an adapter re-displays when re-asking after a
// rejected answer.
func ValidateAnswer(q Question, value string) error {
	switch q.Kind {
	case KindText:
		if strings.TrimSpace(value) == "" {
			return fmt.Errorf("question %s: answer must not be empty", q.ID)
		}
		return nil
	case KindSelect:
		for _, o := range q.Options {
			if o.Value == value {
				return nil
			}
		}
		if q.FreeText && strings.TrimSpace(value) != "" {
			return nil
		}
		return fmt.Errorf("question %s: %q is not one of %s", q.ID, value, optionValues(q.Options))
	default:
		return fmt.Errorf("question %s: unknown kind %q", q.ID, q.Kind)
	}
}

// optionValues joins opts' values for ValidateAnswer's error message.
func optionValues(opts []Option) string {
	values := make([]string, len(opts))
	for i, o := range opts {
		values[i] = o.Value
	}
	return strings.Join(values, ", ")
}
