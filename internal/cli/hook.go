package cli

import (
	"fmt"
	"strings"

	"github.com/kninetimmy/orch/internal/config"
	"github.com/kninetimmy/orch/internal/paths"
	"github.com/kninetimmy/orch/internal/state"
)

// hookUsage is the one-line usage for the adapter plumbing surface,
// mirroring guardUsage. `orch hook claude session-start` is the only
// valid form.
const hookUsage = "orch hook: usage: orch hook claude session-start"

// runHook dispatches the host lifecycle-event verbs (PRD §23 adapter
// plumbing). Host adapters call it from their own lifecycle hooks; it is
// never invoked by a human directly.
func runHook(env Env, args []string) error {
	if len(args) != 2 || args[0] != "claude" || args[1] != "session-start" {
		return usageError(hookUsage)
	}
	return hookClaudeSessionStart(env)
}

// hookClaudeSessionStart answers a Claude Code SessionStart event. Its
// stdout becomes injected session context, so unlike every other verb in
// this package it is deliberately fail-OPEN, not fail-closed: a broken or
// non-orch repository must never break a Claude Code session from
// starting. It never reads stdin (the same console-hang concern as `run
// status --json`) and always exits 0 once its arguments are valid — any
// discovery, config, or state failure degrades to silent output, not an
// error. The architect skill's own `orch run status --json` call surfaces
// a broken repo loudly later, once a session is actually underway.
func hookClaudeSessionStart(env Env) error {
	root, err := paths.FindOutermostRoot(env.RepoRoot)
	if err != nil {
		return nil // no .orchestrator ancestor (or an unwalkable one): silent
	}
	cfg, err := config.Load(root)
	if err != nil {
		return nil // unreadable/invalid config: silent
	}
	st, err := state.Load(root)
	if err != nil {
		return nil // unreadable/invalid state: silent
	}
	fmt.Fprint(env.Stdout, sessionStartContext(cfg, st))
	return nil
}

// deliveryPhaseOrder lists state.Phase in the lifecycle order state.go
// declares them, so the run summary line's phase counts render in a
// stable, human-legible sequence rather than map iteration order.
var deliveryPhaseOrder = []state.Phase{
	state.PhasePlanned, state.PhaseIssueCreated, state.PhaseWorktreeReady,
	state.PhaseDispatched, state.PhasePROpen, state.PhaseInReview,
	state.PhaseAwaitingMerge, state.PhaseMerged, state.PhaseCleaned,
	state.PhaseAbandoned, state.PhaseBlocked,
}

// sessionStartContext renders the compact plain-text context block the
// SessionStart hook injects: Claude Code takes a hook's raw stdout as
// context text, not a JSON envelope, so this is deliberately prose, not a
// document.
func sessionStartContext(cfg *config.Config, st *state.State) string {
	var b strings.Builder
	if st.Mode == state.ModeDelivery {
		fmt.Fprintln(&b, "This repository is managed by Orch (mode: delivery).")
		fmt.Fprintln(&b, deliveryRunSummary(st.Run))
	} else {
		fmt.Fprintln(&b, "This repository is managed by Orch (mode: assist).")
	}
	fmt.Fprintf(&b, "Memhub mode: %s.\n", cfg.Memhub.Mode)
	fmt.Fprintln(&b, "Before planning or acting on any request that would change tracked files, load and follow the `orch-architect` skill. Setup interviews: /orch:init, /orch:configure, /orch:configure-local.")
	return b.String()
}

// deliveryRunSummary renders the one-line Delivery run summary: run id,
// host, and per-phase issue counts in deliveryPhaseOrder, skipping any
// phase with zero issues.
func deliveryRunSummary(run *state.Run) string {
	counts := make(map[state.Phase]int, len(deliveryPhaseOrder))
	for _, iss := range run.Issues {
		counts[iss.Phase]++
	}
	var parts []string
	for _, phase := range deliveryPhaseOrder {
		if n := counts[phase]; n > 0 {
			parts = append(parts, fmt.Sprintf("%d %s", n, phase))
		}
	}
	return fmt.Sprintf("Delivery run %s (host %s): %s.", run.ID, run.Host, strings.Join(parts, ", "))
}
