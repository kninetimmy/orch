package interview

import "errors"

// Sentinel errors callers test with errors.Is. Specifics wrap these
// with %w and detail (config/run house style) rather than replace
// them.
var (
	// ErrNoHostEnabled reports that both host.claude.enabled and
	// host.codex.enabled are answered "no" — the same "at least one
	// host" rule config.validate enforces on the committed file,
	// checked here as soon as both toggles are known so the adapter
	// can fix it before walking any role questions.
	ErrNoHostEnabled = errors.New("at least one of Claude Code or Codex CLI must be enabled")
	// ErrUnknownAnswer reports an answer key that names no question in
	// the sequence the current facts and prior answers derive — either
	// a genuine typo, or a question that is not (or no longer)
	// reachable, such as a Codex role answer submitted while
	// host.codex.enabled is "no".
	ErrUnknownAnswer = errors.New("answer does not match a currently applicable question")
	// ErrApprovalBlocked reports an "approval" answer submitted while
	// Summary.Blockers is non-empty: approval is withheld until every
	// blocker is resolved and the interview is re-run.
	ErrApprovalBlocked = errors.New("cannot approve while blockers remain")
	// ErrBadAnswer reports a free-text answer that fails its
	// ingestion-time semantic check at materialization: a model string
	// containing whitespace, or a concurrency value that is not an
	// integer >= 1.
	ErrBadAnswer = errors.New("answer fails its semantic validation")
)
