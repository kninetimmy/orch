// Package interview implements the pure `orch init` question engine
// (PRD §18): given the environment Detect found and the answers
// collected so far, Next derives the next question.Document to show —
// a batch of independent questions, the pre-approval summary, the
// terminal hand-off document, or an abort notice. The engine holds no
// session state of its own: every call is a pure function of its
// three inputs, so an adapter's stateless step loop (below) is the
// only protocol this package needs to support, and a revision is
// simply the adapter editing or deleting an answer and resubmitting —
// Next re-derives the sequence and re-asks whatever that invalidates.
//
// # Step-loop protocol
//
// This is committed now so PR B's CLI wiring has a fixed contract to
// implement against; nothing in this package is reachable from
// `orch init` yet (internal/cli's stub and
// TestNotImplementedCommandsFailClosed are untouched by this PR).
//
//   - Bare `orch init` (human invocation, PRD §22): prints a detection
//     report plus guidance that an adapter drives the interactive
//     interview, and the plumbing invocation to run. It never reads
//     stdin (the `orch run status` precedent: a command a human might
//     run directly must never block on a console that never reaches
//     EOF) and exits 0.
//   - `orch init --step` (adapter plumbing): reads one question.AnswerSet
//     JSON document from stdin and writes one question.Document
//     (json.MarshalIndent) to stdout. The very first call sends
//     `{"schema_version":1,"answers":{}}`. An invalid answer makes Next
//     return an error; the CLI exits 1 with that error's message
//     (ValidateAnswer's message, when the failure is a bad answer) on
//     stderr, and the adapter re-asks the same question. Malformed
//     usage stays exit 2, matching every other command.
//   - A flag on `init`, rather than a `run`-style verb, because the
//     `run` verbs hard-depend on config.Load plus the state/lock pair —
//     none of which exist before initialization — while PRD §22 already
//     names `init` as the one logical command adapters wrap end to end
//     (`orch resume`'s own flag-based loop is the closest existing
//     precedent).
package interview

import (
	"fmt"
	"sort"
	"strings"

	"github.com/kninetimmy/orch/internal/question"
)

// Next derives the next document to show, given facts (Detect's most
// recent snapshot — re-run on every step, since facts never travel
// inside answers) and answers (every answer collected so far, keyed by
// question.Question.ID). repoRoot is consulted only once the question
// sequence is fully answered, to plan the instruction-file changes and
// scan for nested conflicts (buildSummary) — every earlier step is a
// pure function of facts and answers alone.
//
// Next fails closed on any answer that does not belong to the
// currently derivable sequence (ErrUnknownAnswer — covering both a
// genuine unknown id and a question that is not, or no longer,
// reachable, such as a Codex role answer while Codex is disabled), on
// both hosts being disabled (ErrNoHostEnabled), on any present answer
// failing question.ValidateAnswer, and on an "approval" answer
// submitted while the summary carries Blockers (ErrApprovalBlocked).
func Next(facts Facts, answers map[string]string, repoRoot string) (question.Document, error) {
	if answers == nil {
		answers = map[string]string{}
	}

	seq, err := buildSequence(facts, answers)
	if err != nil {
		return question.Document{}, err
	}
	complete := allDocsAnswered(seq, answers)

	applicable := applicableQuestions(seq, complete)
	if err := validateAnswers(applicable, answers); err != nil {
		return question.Document{}, err
	}

	for i, d := range seq {
		if !d.allAnswered(answers) {
			return question.Document{
				SchemaVersion: question.SchemaVersion,
				Kind:          question.DocQuestions,
				Progress:      &question.Progress{Index: i + 1, Total: len(seq)},
				Questions:     d.questions,
			}, nil
		}
	}

	return nextAfterSequence(facts, answers, repoRoot)
}

// nextAfterSequence handles Next's tail: every "questions"-kind
// document is answered, so the engine materializes the configuration,
// builds the summary, and resolves approval.
func nextAfterSequence(facts Facts, answers map[string]string, repoRoot string) (question.Document, error) {
	cfg, err := materialize(answers)
	if err != nil {
		return question.Document{}, err
	}
	summary, err := buildSummary(cfg, repoRoot)
	if err != nil {
		return question.Document{}, err
	}

	approvalVal, answered := answers[idApproval]
	if len(summary.Blockers) > 0 {
		if answered {
			return question.Document{}, fmt.Errorf("%w: %s", ErrApprovalBlocked, joinBlockers(summary.Blockers))
		}
		return question.Document{
			SchemaVersion: question.SchemaVersion,
			Kind:          question.DocSummary,
			Summary:       &summary,
		}, nil
	}

	if !answered {
		return question.Document{
			SchemaVersion: question.SchemaVersion,
			Kind:          question.DocSummary,
			Questions:     []question.Question{approvalQuestion()},
			Summary:       &summary,
		}, nil
	}

	if approvalVal == "approve" {
		return question.Document{
			SchemaVersion: question.SchemaVersion,
			Kind:          question.DocComplete,
			Complete:      buildComplete(summary, facts),
		}, nil
	}
	// ValidateAnswer already restricted approvalVal to "approve" or
	// "abort" before this point is reached.
	return question.Document{SchemaVersion: question.SchemaVersion, Kind: question.DocAborted}, nil
}

// allDocsAnswered reports whether every document in seq is fully
// answered.
func allDocsAnswered(seq []docSpec, answers map[string]string) bool {
	for _, d := range seq {
		if !d.allAnswered(answers) {
			return false
		}
	}
	return true
}

// applicableQuestions collects every question in seq into an id-keyed
// map, adding the approval question only once seqComplete — before
// that, "approval" is itself not-yet-applicable and validateAnswers
// rejects it like any other unreachable id.
func applicableQuestions(seq []docSpec, seqComplete bool) map[string]question.Question {
	applicable := map[string]question.Question{}
	for _, d := range seq {
		for _, q := range d.questions {
			applicable[q.ID] = q
		}
	}
	if seqComplete {
		aq := approvalQuestion()
		applicable[aq.ID] = aq
	}
	return applicable
}

// validateAnswers checks every key in answers against applicable,
// fail-closed: an id absent from applicable is ErrUnknownAnswer, and a
// present id's value must pass question.ValidateAnswer. Keys are
// walked in sorted order so that, when more than one answer is
// invalid at once, which error comes back is deterministic rather
// than depending on Go's randomized map iteration order.
func validateAnswers(applicable map[string]question.Question, answers map[string]string) error {
	ids := make([]string, 0, len(answers))
	for id := range answers {
		ids = append(ids, id)
	}
	sort.Strings(ids)

	for _, id := range ids {
		val := answers[id]
		q, ok := applicable[id]
		if !ok {
			return fmt.Errorf("%w: %q", ErrUnknownAnswer, id)
		}
		if err := question.ValidateAnswer(q, val); err != nil {
			return err
		}
	}
	return nil
}

// joinBlockers renders Blockers for ErrApprovalBlocked's message.
func joinBlockers(blockers []string) string {
	return strings.Join(blockers, "; ")
}
