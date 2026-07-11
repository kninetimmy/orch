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
// Concurrency invariant: plan and status are read-only. activate is
// the only PR A mutator, and it acquires the cross-host Delivery lock
// via state.EnterDelivery before any mutation except the idempotent
// label-taxonomy ensure (PRD §14 F6). Every future mutating verb (PR
// B) must verify state.Load + lockfile.Inspect + state.CheckConsistent
// and st.Run.ID == owner.RunID before touching anything. Within one
// invocation writes are sequential, so PRD §14's serialization
// requirement holds trivially.
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
