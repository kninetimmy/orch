# Decision-block recovery

Healthy git or GitHub artifacts do not resolve a decision. Block provenance
is persisted per issue; every block has an issue-local ID, cause, original
reason and optional resolution. The tuple (run ID, issue number, block ID)
identifies one block, even if a later block repeats the same reason.

The CLI cannot authenticate a human. Like plan and merge approval, resolution
is an adapter assertion: the Architect must obtain an explicit decision and
record who made it and why the original approved work remains valid.

`orch run resolve-block` accepts schema 1, `run_id`, `issue_number`, `block_id`,
`resolved_by`, `detail`, and the exact statement
`resolve-block-without-scope-change`. It records the resolution atomically but
leaves the issue blocked until `orch resume` observes its artifacts. It neither
resets escalation history nor approves a PR. Wrong criteria can only continue
if the decision is to retain the original criteria; changing criteria or scope
requires aborting and returning through plan approval. The operation has no
field for replacement work. Replayed resolutions cannot resolve a later block.

A secret still stops the entire run. `orch resume --resume-stopped-run` lifts
that run-level stop only; its issue decision still needs resolution. A later
generic failure cannot replace an unresolved decision or its reason. Legacy
blocks with no cause cannot be guessed operational: preserve their artifacts
and return through plan approval. No active run is migrated by this change.

## Affected structure and preserved behavior

| Element and consumers | Before and after |
| --- | --- |
| `state.Issue`, `Block`, `BlockResolution`, `SetBlock`, `DecisionPending` | Before only `BlockedReason` survived. State v6 adds ordered block/resolution history. Every producer uses the same sticky-decision rule; it is not specific to wrong-criterion reviews. Original reasons remain available after resolution and operational recovery. |
| `state.Load`, `Save`, validation | Before v5 wrote v5 and could inspect v4. Now v6 writes v6 and can inspect v4/v5. Invalid causes, identities, resolutions and future versions fail closed without rewriting state. Unknown old blocks remain readable. Activation's incremental saves, `verbCtx.save` for lifecycle mutations, and resume's final save all inherit validation. Atomic writes, phase invariants and missing-file Assist behavior are preserved. |
| All `state.Load` consumers | `run.Status`, CLI status/doctor/hook, configuration's active-run guard, bootstrap configure, and the activation gate still inspect state without authorizing work. Lifecycle `loadVerb` (dispatch, PR open, review-worktree, review, escalation, CI, merge report/merge, block, abandon, cleanup, complete, now resolve-block) and `resumeLoad` still enforce `checkExecutionConfig`; v4/v5 cannot execute with the v6 engine. `guard.Checker` binds `loadState` to `state.Load`; its containment and writable-phase rules still deny blocked worktrees. `state.Abort` and `CompleteDelivery` also load through this boundary; explicit abort retains its existing reset behavior, while completion still requires its consistency checks. Unsupported data cannot become permission to write. |
| `Review` wrong judgments | Before it blocked, but healthy recovery erased the decision. Now it creates `wrong-criterion` provenance. Criterion coverage, independent reviewer checks, recorded judgments and needs-human labeling remain. Review request/result v3 is unchanged. |
| `escalateReturnToArchitect` | Before ambiguity/exhaustion recorded a reason susceptible to recovery. Now it creates `return-to-architect` provenance. Rerouting and failed-attempt retirement remain unchanged; resolution does not reset attempts. Escalation wire v2 is unchanged. |
| `Block` | Before schema 1 re-blocking overwrote any reason. Schema 2 keeps unresolved decisions. `hook`, `auth`, `github` are operational; `secret`, `validation`, `other`, `human-decision` require a decision. Secret run-stop, branch/worktree preservation and stopped-run exception remain. A later secret also preserves an existing run-stop reason. |
| `Resume`, `reconcileIssue`, `rederive`, `applyOutcome`, `sameWork` | Before schema 1 re-derived all blocks and copied approved work from audit bodies. Schema 2 only re-derives operational or explicitly resolved blocks and rejects edited/missing approved text. Sticky reasons survive missing artifacts, dry runs and repetition. Observation remains read-only, transport errors remain no-write failures, and operational recovery, orphan adoption, approved-head demotion and merge gates remain. All four approved-work fields, including the CI exclusion declaration, are checked. |
| `ResolveBlock` and CLI `runVerbs` | New schema 1 operation records only the decision for the exact current block. It shares serialization, execution-config, phase and stopped-run checks with every mutating lifecycle verb. It performs no git/GitHub changes. Disabled metrics remain disabled; enabled metrics record the new verb using the existing event shape. |
| `StatusDoc`, CLI resume rendering, all three host skills | Before StatusDoc v3 exposed state v5 approvals. StatusDoc v4 preserves them and exposes v6 block history; resume report v2 retains the report fields and adds the stronger semantics. Claude, Codex and OpenCode require matching versions and the same resolution/re-plan discipline. OpenCode's native Selection shapes and JavaScript plugin are unchanged. Doctor derives required skill versions from engine constants. |
| Abandonment, cleanup, abort, completion | Existing explicit abandonment/cleanup gates remain. Abandonment can terminate an issue without resolving its decision; its original block evidence stays in state until the normal run termination. Abort is still the explicit path to a new approved plan; no automatic deletion or migration was added. |
| Tests and fixtures | The actual review-gap reproduction failed before the fix. Tests now cover real block producers through reload/resume, legacy unknowns, repeated/dry runs, generic failures, operational recovery, exact resolution and scope drift. Operational seed fixtures now say so explicitly; the former repopulation test now verifies refusal. CLI routing and schema checks remain. |

These restrictions cover every decision block and every lifecycle mutation,
not only `Review`, `Dispatch`, or one host. The implementation adds no budget
driver, controller, dependency, model routing or host-isolation feature.
