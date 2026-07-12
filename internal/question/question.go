// Package question defines the native-question-tool compatibility
// contract (PRD §18, §24 — resolving the deferred "native
// question-tool compatibility handling" decision): a JSON document the
// Go core emits describing 1-4 mutually independent questions, and the
// AnswerSet an adapter submits back. Every type decodes and encodes
// with stdlib encoding/json only. SchemaVersion pins the wire schema
// this build understands; DecodeAnswers rejects any other value and
// any field it does not recognize, rather than silently ignore drift
// (the run-verb precedent: internal/run/plandoc.go's DecodePlan).
//
// Design goes to the lowest common denominator between the two
// supported hosts' native dialogs. There is no multi-select kind: a
// multi-valued ask decomposes into several independent yes/no selects
// sharing one Document, so a single-question host (Codex's `ask`) can
// present them one at a time while a batching host (Claude Code's
// AskUserQuestion) may present an entire document's questions
// together — batching is always permitted, never required.
// Question.FreeText admits an answer outside the question's listed
// Options: a Claude adapter gets this "for free" through
// AskUserQuestion's automatic "Other" entry, while a Codex adapter
// falls back to a plain text prompt.
//
// This package never imports internal/interview. Complete.Detection is
// a flat string map specifically so the dependency arrow stays
// interview -> question, never the reverse.
package question

// SchemaVersion is the AnswerSet/Document wire schema this build
// emits and accepts. DecodeAnswers rejects any other value.
const SchemaVersion = 1

// DocKind classifies a Document. It is a closed enumeration matched
// exactly against the wire strings shown.
type DocKind string

const (
	// DocQuestions carries one to four independent Questions still
	// awaiting an answer.
	DocQuestions DocKind = "questions"
	// DocSummary carries the fully materialized configuration and
	// proposed file changes (PRD §18 steps 7-8), plus the approval
	// question embedded in Questions — unless Summary.Blockers is
	// non-empty, in which case approval is withheld and Questions is
	// empty until the blocker is resolved.
	DocSummary DocKind = "summary"
	// DocComplete carries the terminal Complete payload: the entire
	// hand-off interface to the bootstrap executor.
	DocComplete DocKind = "complete"
	// DocAborted reports that the user chose not to proceed
	// (approval == "abort"). The interview ends; nothing is written.
	DocAborted DocKind = "aborted"
)

// Document is the one document type the core ever emits in the
// question step-loop protocol: either a batch of independent
// Questions, the pre-approval summary, the terminal Complete payload,
// or an abort notice. Exactly one of Questions/Summary/Complete is
// meaningful for a given Kind; the others are omitted from the wire
// form.
type Document struct {
	SchemaVersion int        `json:"schema_version"`
	Kind          DocKind    `json:"kind"`
	Progress      *Progress  `json:"progress,omitempty"`
	Questions     []Question `json:"questions,omitempty"`
	Summary       *Summary   `json:"summary,omitempty"`
	Complete      *Complete  `json:"complete,omitempty"`
}

// Progress is an advisory position within the current question
// sequence. It is informational only — no caller may infer anything
// about whether the interview is "done" from Index reaching Total,
// since Total grows as answers enable additional hosts and their role
// questions.
type Progress struct {
	Index int `json:"index"`
	Total int `json:"total"`
}

// QuestionKind classifies a Question's answer shape. It is a closed
// enumeration matched exactly against the wire strings shown.
type QuestionKind string

const (
	// KindSelect is a closed choice among Options, optionally
	// admitting a non-option answer when FreeText is true.
	KindSelect QuestionKind = "select"
	// KindText is open free text; it never carries Options.
	KindText QuestionKind = "text"
)

// Question is one independent ask within a Document. Header is a
// short display label (≤12 characters — SpecCheck enforces this) for
// hosts that group several simultaneously displayed questions;
// Preamble is optional explanatory prose shown once above the
// question itself.
type Question struct {
	ID       string       `json:"id"`
	Header   string       `json:"header"`
	Prompt   string       `json:"prompt"`
	Preamble string       `json:"preamble,omitempty"`
	Kind     QuestionKind `json:"kind"`
	Options  []Option     `json:"options,omitempty"`
	FreeText bool         `json:"free_text,omitempty"`
	Default  string       `json:"default,omitempty"`
	Hint     string       `json:"hint,omitempty"`
}

// Option is one choice of a KindSelect Question. Recommended marks
// the option a Claude-style renderer highlights and a Codex-style
// renderer lists first; it does not by itself make the option the
// answer — Question.Default does that.
type Option struct {
	Value       string `json:"value"`
	Label       string `json:"label"`
	Description string `json:"description,omitempty"`
	Recommended bool   `json:"recommended,omitempty"`
}

// AnswerSet is the adapter's request: every answer collected so far,
// keyed by Question.ID. It is resubmitted in full on every step of the
// stateless step-loop protocol (see internal/interview's package doc)
// — the core holds no session of its own.
type AnswerSet struct {
	SchemaVersion int               `json:"schema_version"`
	Answers       map[string]string `json:"answers"`
}

// Summary is the PRD §18 steps 7-8 payload: the resulting
// configuration and the proposed instruction-file changes, shown for
// approval before any bootstrap PR is opened.
type Summary struct {
	ConfigTOML     string       `json:"config_toml"`
	ConfigRevision string       `json:"config_revision"`
	Files          []FileChange `json:"files"`
	GitignoreLines []string     `json:"gitignore_lines,omitempty"`
	Conflicts      []string     `json:"conflicts,omitempty"`
	Blockers       []string     `json:"blockers,omitempty"`
	// ConfigDiff is emit-only: `orch configure`'s unified diff between
	// the committed config.toml and the newly materialized render, for
	// display alongside Files. It is deliberately not a FileChange —
	// config.toml is written by bootstrap.writeFiles directly from
	// ConfigTOML, never from an entry in Files, so a caller can never
	// double-write it. Always "" for `orch init`/`orch configure-local`,
	// which never populate it.
	ConfigDiff string `json:"config_diff,omitempty"`
}

// FileChange is one proposed instruction-file edit. Path is
// repo-relative with forward slashes regardless of host OS. The
// bootstrap executor writes NewContent verbatim; Diff is display-only
// (a unified diff against the file's current content, or its absence).
// Delete is emit-only: configure-local sets it when clearing the last
// machine-local override deletes config.local.toml outright rather than
// writing an empty file (an empty override file would make
// config.HasLocalOverride and `orch status` misleading). It carries no
// meaning on an incoming AnswerSet — the wire schema stays 1; strict
// decoding governs AnswerSet only, not this emit-only Document field.
type FileChange struct {
	Path       string `json:"path"`
	Existed    bool   `json:"existed"`
	Diff       string `json:"diff,omitempty"`
	NewContent string `json:"new_content"`
	Delete     bool   `json:"delete,omitempty"`
}

// Complete is the terminal hand-off document: the entire interface
// between the interview and the bootstrap executor (PR B). Detection
// is a flat string map of the facts the interview ran on, carried
// forward as an audit record (PRD §13); it is deliberately not a
// richer type so this package never needs to import
// internal/interview to describe it.
type Complete struct {
	Summary         Summary           `json:"summary"`
	Detection       map[string]string `json:"detection"`
	BootstrapReady  bool              `json:"bootstrap_ready"`
	NotReadyReasons []string          `json:"not_ready_reasons,omitempty"`
}
