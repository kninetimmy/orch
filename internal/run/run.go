// Package run is the Delivery run engine: the policy home that
// composes internal/paths, internal/execx, internal/gitops,
// internal/ghops, internal/manifest, internal/routing, and
// internal/state+internal/lockfile (all policy-free) into the
// adapter-facing plumbing surface PRD §22 calls for. There is no
// manual delivery command — host adapters shell out to `orch run
// <verb>` after their own native dialogs, exchanging JSON documents on
// stdin/stdout:
//
//	orch run plan            # plan JSON on stdin → gate document JSON on stdout
//	orch run activate        # activation request JSON on stdin → activation result JSON on stdout
//	orch run status --json   # run-state JSON on stdout
//
// The per-issue lifecycle verbs (see lifecycle.go) extend the surface —
// dispatch, pr-open, review, escalate, ci, merge-report, merge, block,
// abandon, cleanup — plus the run-level complete. Each reads a
// schema-versioned request document on stdin and writes a
// schema-versioned result on stdout.
//
// Concurrency invariant: plan and status are read-only. Every mutating
// verb acquires or re-verifies the cross-host Delivery lock: activate
// enters Delivery via state.EnterDelivery, and each lifecycle verb runs
// the shared preconditions in loadVerb (state.Load + lockfile.Inspect +
// state.CheckConsistent with st.Run.ID == owner.RunID, plus config-drift
// and stopped-run gates) before touching anything. Within one invocation
// writes are sequential, so PRD §14's serialization requirement holds
// trivially. Failure semantics are pure: a verb never mutates phase,
// state, GitHub, or git as a side effect of failing.
//
// All documents this package accepts are schema-versioned
// (exact-match, fail closed) and decoded with DisallowUnknownFields:
// an adapter sending a field this build does not recognize is a bug to
// surface immediately, not silently drop.
package run

import (
	"time"

	"github.com/kninetimmy/orch/internal/execx"
)

// Env carries the invocation context for every run verb, mirroring
// cli.Env's shape but scoped to what this package needs.
type Env struct {
	// RepoRoot is the directory containing .orchestrator/.
	RepoRoot string
	// Runner executes external commands (git, gh, memhub).
	Runner execx.Runner
	// Now returns the current time; nil defaults to time.Now. Declared
	// for every verb's determinism (tests inject a fixed clock); no PR
	// A document currently carries a timestamp field, so no verb reads
	// it yet — PR B's lifecycle verbs will.
	Now func() time.Time
}
