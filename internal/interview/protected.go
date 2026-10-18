package interview

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"github.com/kninetimmy/orch/internal/config"
	"github.com/kninetimmy/orch/internal/question"
)

// idProtectedPaths is the repository's protected-paths question: its answer
// materializes onto the committed [protected] section. idPickProtected is
// `orch configure`'s picker question for it.
const (
	idProtectedPaths = "protected.paths"
	idPickProtected  = "pick.protected"
)

const (
	// protectedDeferAnswer defers the survey: init writes no [protected]
	// section, and configure leaves the committed one unchanged.
	protectedDeferAnswer = "defer"
	// protectedEmptyAnswer approves an empty list: a [protected] section
	// holding the binary's methodology version and no entries.
	protectedEmptyAnswer = "[]"
)

// protectedDoc is the protected-paths question. Its free-text answer is
// either protectedDeferAnswer or a JSON array of {"path","reason"} objects
// (parseProtectedAnswer), the entries the human approved after the survey.
func protectedDoc(configure bool) docSpec {
	deferLabel, deferDesc := "Defer", "Write no protected list now; grants refuse until one is approved."
	if configure {
		deferLabel, deferDesc = "Keep current", "Leave the committed protected list unchanged."
	}
	return docSpec{questions: []question.Question{{
		ID:       idProtectedPaths,
		Header:   "Protected",
		Prompt:   "Approved grant-protected paths for this repository",
		Preamble: fmt.Sprintf("Grant-protected paths are repository paths no autonomy grant may merge a change to, added to Orch's built-in floor. They come from an Architect survey (survey methodology version %d) that you approve here and again when the PR merges.", config.SurveyMethodologyVersion),
		Hint:     `Answer "defer", "[]" (approve an empty list), or a JSON array like [{"path":"internal/auth","reason":"session and token handling"}]: each path repository-relative, each reason one line.`,
		Kind:     question.KindSelect,
		FreeText: true,
		Default:  protectedDeferAnswer,
		Options: []question.Option{
			{Value: protectedDeferAnswer, Label: deferLabel, Description: deferDesc},
			{Value: protectedEmptyAnswer, Label: "No protected paths", Description: "Approve an empty list beyond the built-in floor."},
		},
	}}}
}

// pickProtectedQuestion is `orch configure`'s "review protected paths?"
// picker question: it defaults to yes when the committed configuration has
// no [protected] section or records another methodology version than this
// binary's, and to no otherwise.
func pickProtectedQuestion(committed *config.Config) question.Question {
	def := "no"
	if committed.CheckGrantProtected() != nil {
		def = "yes"
	}
	return question.Question{
		ID:      idPickProtected,
		Header:  "Protected",
		Prompt:  "Review or change the grant-protected paths?",
		Kind:    question.KindSelect,
		Default: def,
		Options: yesNoOptions(def),
	}
}

// parseProtectedAnswer decodes a protected.paths answer: deferred is true for
// protectedDeferAnswer; otherwise the answer must be a JSON array of
// {"path","reason"} objects, each passing config.CheckProtectedPath and
// carrying a single-line reason. An empty array is an approved empty list.
func parseProtectedAnswer(value string) (entries []config.ProtectedPath, deferred bool, err error) {
	trimmed := strings.TrimSpace(value)
	if trimmed == protectedDeferAnswer {
		return nil, true, nil
	}
	dec := json.NewDecoder(strings.NewReader(trimmed))
	dec.DisallowUnknownFields()
	var raw []config.ProtectedPath
	if err := decodeProtected(dec, &raw); err != nil {
		return nil, false, fmt.Errorf("%w: %s: want %q or a JSON array of {\"path\",\"reason\"} objects: %v", ErrBadAnswer, idProtectedPaths, protectedDeferAnswer, err)
	}
	for i, e := range raw {
		if err := config.CheckProtectedPath(e); err != nil {
			return nil, false, fmt.Errorf("%w: %s[%d]: %v", ErrBadAnswer, idProtectedPaths, i, err)
		}
		if strings.ContainsAny(e.Reason, "\r\n") {
			return nil, false, fmt.Errorf("%w: %s[%d]: reason for %q must be one line", ErrBadAnswer, idProtectedPaths, i, e.Path)
		}
	}
	return raw, false, nil
}

// decodeProtected decodes exactly one JSON value into dst, rejecting trailing
// content. config.ProtectedPath carries toml tags only, so the JSON object
// keys are its Go field names matched case-insensitively: path and reason.
func decodeProtected(dec *json.Decoder, dst *[]config.ProtectedPath) error {
	if err := dec.Decode(dst); err != nil {
		return err
	}
	if _, err := dec.Token(); err != io.EOF {
		return fmt.Errorf("unexpected content after the array")
	}
	return nil
}

// applyProtected sets cfg's [protected] section from a present
// protected.paths answer: the approved entries under this binary's
// methodology version. A deferral and an absent answer leave cfg untouched —
// no section for init, the committed section for configure.
func applyProtected(cfg *config.Config, answers map[string]string) error {
	value, ok := answers[idProtectedPaths]
	if !ok {
		return nil
	}
	entries, deferred, err := parseProtectedAnswer(value)
	if err != nil {
		return err
	}
	if deferred {
		return nil
	}
	if len(entries) == 0 {
		entries = nil // an approved empty list round-trips through Parse as nil
	}
	cfg.Protected = &config.Protected{MethodologyVersion: config.SurveyMethodologyVersion, Paths: entries}
	return nil
}
