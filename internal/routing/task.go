package routing

// Task carries the routing-relevant facts about one unit of work. All
// fields default to the safe answer: a zero Task is a read/write,
// low-difficulty change with no risk domains and no downgrade
// permission, which Decide routes to a plain implementer with a strong
// reviewer.
type Task struct {
	// ReadOnly marks work that only reads the repository (PRD §9 Scout).
	ReadOnly bool
	// UnusuallyDifficult marks work whose implementation is itself hard
	// (PRD §9 Specialist), forcing a specialist regardless of risk.
	UnusuallyDifficult bool
	// RiskDomains lists the sensitive domains the change touches; any
	// non-empty set forces a specialist plus a strong reviewer (PRD §11).
	RiskDomains []RiskDomain
	// Downgrade holds the caller's affirmative claims about the change.
	// It only ever weakens the reviewer, and only when Eligible.
	Downgrade DowngradeFacts
}

// DowngradeFacts are the four conditions PRD §11 requires before a
// reviewer may be downgraded: the change must be affirmatively
// mechanical, low-risk, fully specified, and unsurprising.
type DowngradeFacts struct {
	Mechanical     bool
	LowRisk        bool
	FullySpecified bool
	Unsurprising   bool
}

// Eligible reports whether all four downgrade facts are affirmatively
// true. The zero value is not eligible: absence of information is never
// permission to downgrade review (PRD §11: uncertainty favors the
// stronger route).
func (f DowngradeFacts) Eligible() bool {
	return f.Mechanical && f.LowRisk && f.FullySpecified && f.Unsurprising
}

// requested reports whether the caller asserted any downgrade fact. It
// distinguishes a partial, refused downgrade request (some facts true)
// from a task that never asked for one (the zero value), which the
// rationale narrates differently.
func (f DowngradeFacts) requested() bool {
	return f.Mechanical || f.LowRisk || f.FullySpecified || f.Unsurprising
}
