// Package guard is the mechanical write-enforcement core behind the
// host-adapter PreToolUse hooks (PRD §23: "Assist reliably blocks
// tracked-file changes" and "Delivery cannot write outside registered
// worktrees"). Adapters shell out to `orch guard` before every agent
// file write; guard answers allow/deny and fails closed on any
// ambiguity — an unreadable state or an unverifiable path is a deny,
// never a permissive default.
//
// All decision logic lives here in the Go core, not in adapter shell
// (PRD §6: adapters stay thin). The package is policy-mechanical: it
// enforces the Assist read-only rule (PRD §7), the Delivery
// containment/branch/phase rules and role read-only-ness (PRD §15), and
// treats orchestrator internals and git internals as never writable.
//
// The verdict is split into a pure decision (evaluate, over a resolved
// facts value, no I/O — the normative decision table as data) and the
// I/O that resolves those facts (resolve.go). Rows the table classifies
// as "cannot verify" or "propagate message" surface as a Go error from
// Check, so the CLI can map them to the hook protocol's blocking exit;
// every genuine allow/deny decision is a Verdict.
package guard

import (
	"fmt"

	"github.com/kninetimmy/orch/internal/manifest"
	"github.com/kninetimmy/orch/internal/state"
)

// Request is one guard invocation: the write targets, plus the optional
// narrowing assertions an adapter may pass. Role and Issue are
// narrowing-only — they can only ever deny more, never grant. The empty
// Role asserts nothing and falls through to the pure containment rule.
type Request struct {
	Paths []string
	Role  manifest.Role
	Issue int
}

// Verdict is guard's answer for a Request. Path names the target the
// verdict is about (the first denial for a multi-path Request); Reason
// is the one-line human explanation for a denial and is empty on allow.
type Verdict struct {
	Allow  bool
	Path   string
	Reason string
}

// facts are the resolved, decision-relevant inputs for one path, filled
// by resolve. Fields that require I/O the decision table classifies as
// "cannot verify" (rows 1, 2, 5) never reach here — resolve returns those
// as errors — so evaluate stays pure and total over what remains.
type facts struct {
	// path is the canonical target, used in every reason.
	path string

	// inRepo is false when no .orchestrator ancestor governs path
	// (row 3: allow, guard governs this repo only).
	inRepo bool
	// gitSeg reports a .git segment between the root and the target
	// (row 4: git internals).
	gitSeg bool

	// mode is the operating mode read from state (assist or delivery).
	mode state.Mode

	// Assist facts.
	underOrch bool  // row 6: path under <root>/.orchestrator/
	ignored   bool  // row 7: git says path is ignored
	ignoreErr error // row 8: the ignore probe failed to run

	// Delivery facts.
	stopped         string      // row 9: Run.StoppedReason
	worktreeMatched bool        // row 11: a registered worktree contains path
	worktreeIssue   int         // row 12: containing issue's number
	worktreePhase   state.Phase // row 13: containing issue's phase
	worktreeBranch  string      // row 14: containing issue's registered branch
	headRef         string      // row 14: the worktree's actual HEAD ref line
	headErr         error       // row 14: HEAD was unreadable
}

// evaluate is the pure decision core: the normative decision table
// (rows 3, 4, 6–15) as a precedence-ordered switch over resolved facts.
// It performs no I/O. Rows 1, 2, and 5 are resolved as errors by Check
// and never reach here.
func evaluate(req Request, f facts) Verdict {
	// Row 3: outside any orch repo — guard governs this repo only.
	if !f.inRepo {
		return allow(f.path)
	}
	// Row 4: git internals are never writable (covers <root>/.git/** and
	// a worktree's own .git).
	if f.gitSeg {
		return deny(f.path, "git internals are never writable")
	}

	switch f.mode {
	case state.ModeAssist:
		// Row 6: orchestrator internals are not writable. state.json is
		// gitignored, so blocking it here blocks self-promotion to
		// Delivery from a guarded write.
		if f.underOrch {
			return deny(f.path, "orchestrator internals are not writable")
		}
		// Row 7: an ignored in-repo path is local scratch — allow.
		if f.ignored {
			return allow(f.path)
		}
		// Row 8: not ignored, or the ignore check could not run — assist
		// is read-only for repository files.
		if f.ignoreErr != nil {
			return deny(f.path, fmt.Sprintf("cannot confirm ignore status (%v); assist is read-only for repository files", f.ignoreErr))
		}
		return deny(f.path, "assist is read-only for repository files; ask for a Delivery plan")

	case state.ModeDelivery:
		// Row 9: a secret block stopped the run; it gates all agent writes.
		if f.stopped != "" {
			return deny(f.path, "run stopped: "+f.stopped)
		}
		// Row 10: an asserted role outside {implementer, specialist} is
		// mechanically read-only (§15). No assertion falls through.
		if req.Role != "" && req.Role != manifest.RoleImplementer && req.Role != manifest.RoleSpecialist {
			return deny(f.path, fmt.Sprintf("role %s is mechanically read-only", req.Role))
		}
		// Row 11: outside every registered worktree.
		if !f.worktreeMatched {
			return deny(f.path, "outside every registered worktree")
		}
		// Row 12: an asserted --issue must own the containing worktree.
		if req.Issue != 0 && f.worktreeIssue != req.Issue {
			return deny(f.path, fmt.Sprintf("worktree belongs to issue #%d, not the asserted #%d", f.worktreeIssue, req.Issue))
		}
		// Row 13: only dispatched, pr-open, and in-review worktrees are
		// writable. awaiting-merge is OID-pinned; blocked/abandoned
		// preserve work.
		if !writablePhase(f.worktreePhase) {
			return deny(f.path, "worktree not writable in phase "+string(f.worktreePhase))
		}
		// Row 14: the worktree must be on its registered branch, checked
		// with zero subprocesses (§15 "never on main", strictly).
		if f.headErr != nil || f.headRef != headRefFor(f.worktreeBranch) {
			return deny(f.path, "worktree not on its registered branch "+f.worktreeBranch)
		}
		// Row 15: every check passed.
		return allow(f.path)

	default:
		// resolve rejects any mode state.validate would not, so this is
		// unreachable; deny to stay closed regardless.
		return deny(f.path, "unknown operating mode "+string(f.mode))
	}
}

// writablePhase reports the Delivery phases in which a registered
// worktree accepts agent writes: dispatched, pr-open, and in-review.
func writablePhase(p state.Phase) bool {
	switch p {
	case state.PhaseDispatched, state.PhasePROpen, state.PhaseInReview:
		return true
	default:
		return false
	}
}

// headRefFor is the exact symbolic-ref line a worktree's HEAD must hold
// to be considered on its registered branch.
func headRefFor(branch string) string {
	return "ref: refs/heads/" + branch
}

func allow(path string) Verdict {
	return Verdict{Allow: true, Path: path}
}

func deny(path, reason string) Verdict {
	return Verdict{Allow: false, Path: path, Reason: reason}
}
