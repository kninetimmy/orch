// This file sketches PR B's per-issue lifecycle verbs as doc comments
// only — nothing here is implemented in PR A. Every field these verbs
// will need already exists in state schema v2 (Issue's PR B fields;
// Phase's PR B values), so PR B needs no v3 migration.
//
// orch run dispatch
//
//	Phase → dispatched, SetStatus in-progress, emits the worktree and
//	branch plus the routing selection for the adapter to spawn the
//	executor into.
//
// orch run pr-open
//
//	CreatePR with the manifest (Verifications recorded), persists
//	PRNumber/PRURL, moves to awaiting-review.
//
// orch run review
//
//	ReviewCycles++, one consolidated review cycle per PRD §12.11;
//	escalations feed the persisted Attempts (↔ routing.History) into
//	routing.Escalate.
//
// orch run ci
//
//	Folds ghops.RequiredCI's tri-state into the manifest.
//
// orch run merge-report
//
//	Pins PR.HeadRefOid → ApprovedHeadOID, moves to awaiting-merge.
//
// orch run merge
//
//	The per-PR human approval assertion plus the pinned head OID drive
//	ghops.MergePR(strategy, headOID, ExplicitConfirmation()), then
//	confirm issue closure.
//
// orch run cleanup
//
//	Explicit confirmation: DeleteRemoteBranch, RemoveWorktree,
//	ForceDeleteBranch.
//
// orch run complete
//
//	Once every issue is merged/abandoned and cleaned: FastForward the
//	primary checkout, run the memhub wrap-up hook, and return state to
//	Assist with the lock released (auto-return, PRD §7).
package run
