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

## Explicit integration repair

Two historical continuations were insufficient:

| Before | After |
| --- | --- |
| Resolving a pre-PR block and resuming lowered preserved implementation to `worktree-ready`. Dispatch's `merge --ff-only` refused when default and implementation had diverged, while the guard denied edits. The only supported continuation was re-dispatch, which could not perform that integration. | Resume still chooses the conservative `worktree-ready` phase and ordinary dispatch still requires fast-forward/dependencies. Explicit `orch run repair` can instead return recovered committed work to `dispatched` in its existing branch/worktree, without moving HEAD. |
| At `awaiting-merge`, resume retained the merge pin when the PR head was unchanged, even if its advanced base now conflicted. Head movement was the only demotion; the guard denied integration edits. | Resume still keeps unchanged pins and demotes moved heads. Explicit repair can now invalidate the unchanged pin and approving verdict atomically, returning the open PR to `in-review` before any guarded writes become possible. |

The Architect obtains an explicit human/Architect integration decision, not a
new scope or an autonomy grant. As with resolution, the engine cannot authenticate
the person: `authorized_by` and `reason` are attributable adapter assertions.
Use the repository's primary checkout for lifecycle commands. Inspect
`orch run status --json`, `orch run next --json`, the registered worktree's
full `git rev-parse HEAD`, and (if present) the live PR head first.

For a blocked issue, resolve any pending decision and run `orch resume` first.
Repair does not accept `blocked`, unknown legacy blocks, or any other immutable
phase besides the two named below. Pre-PR repair requires recovered block history,
no recorded or live open PR, and preserved implementation commits. Ordinary initial
work still uses dispatch.

Send this JSON on stdin to `orch run repair`, substituting the inspected values:

```json
{
  "schema_version": 1,
  "run_id": "<current run ID>",
  "issue_number": 123,
  "repair_number": 1,
  "expected_phase": "awaiting-merge",
  "expected_head_oid": "<full current commit OID>",
  "authorized_by": "<decision maker>",
  "reason": "Integrate the advanced default branch while retaining approved work",
  "statement": "authorize-integration-repair-without-scope-change"
}
```

Use `worktree-ready` for recovered pre-PR work. `repair_number` must be one
greater than the number of entries in that issue's `repairs` history (initially
1); the number prevents replay even if phase and head later recur. For a pinned
PR, the expected OID must match **all three** of the registered local branch,
live PR head and old merge pin. Reconcile moved heads with resume rather than
guessing a new authorization target.

Repair validates the current run/lock/configuration, dependencies, approved
work/routing in the live issue and PR audit records, their repository/identity,
OPEN state, PR base/branch and remote-branch presence, and the existing clean, registered, non-primary
worktree on its exact branch/head in the same git repository. It does not fetch,
merge, reset, rebase, push, create/recreate artifacts, dispatch an agent, amend
scope, resolve a decision, review, approve a merge, or spend/extend a grant.
The result includes the repair receipt and an `execution` brief using dispatch
schema 4, preserving current routing, native execution profiles and approved
work verbatim. A receipt authorizes only this integration work, not progression.

After success:

1. Have the current routed executor fetch origin and integrate in that same worktree, retain
   approved implementation, run **all required checks**, commit and push.
2. For pre-PR work, use normal `pr-open` with verification evidence.
3. Obtain fresh independent review from the routed reviewer. Normal review
   schema 3 now accepts `repair_number`; repaired issues require the latest
   number, even if the head is unchanged. Include `verifications` on the
   repaired head (the existing `{name, command, result, detail}` shape).
   The engine stamps the live reviewed OID; a new head needs new evidence.
   An earlier review request without that repair acknowledgement is rejected.
4. Resume normal required-CI inspection, `merge-report`, and the separate merge
   approval. The new report must pin the verified/reviewed live head. A repaired
   issue's merge approval must copy the report's top-level `repair_number` to
   `approval.repair_number` and have `approved_at` strictly later than
   the repair receipt's `at`, even if the head is unchanged. Earlier approvals
   cannot be replayed against the renewed gate. Existing grant checks remain
   separate and unchanged; repair supplies no grant authority.

Authorization, pin/verdict invalidation and the writable phase are saved together
using existing atomic state persistence. Optional `repairs` entries retain the
actor, reason, original phase/head, timestamp and pre-repair review-cycle count
in state v6; they are created only by an explicit repair, never inferred or
migrated from an older run. Status/issue context expose this history. Block
resolutions, attempts, routing and review counts remain intact. Before the audit
fix, a prior successful review's same-head verification receipt could survive a
later review's failed audit write. Now each repaired review invalidates that
receipt before saving its new verdict and restores it only after both audit-body
writes succeed, so a partial write cannot enable a new report. Retrying after
such a failure requires verification evidence again. Resume/restart does not
restore the old pin or incomplete review's receipt.

Before approval contract v2, optional v6 receipts could be ignored and discarded
by a receipt-unaware lifecycle engine. New approvals now bind contract v2 in both
the approved contract and execution fingerprint: the existing compatibility gate
rejects these runs in contract-v1 engines before any lifecycle save. Conversely,
the v2 engine can inspect but cannot execute existing contract-v1 runs. Finish
those with their original engine, or abort and re-plan for fresh approval; no
active run is migrated. State remains v6. Older engines do not implement the
additional repair-specific verification/approval preflights and cannot continue
newly approved v2 runs.
Unrepaired review schema 3, merge schema 1 and report schema 1 retain their wire
contracts. Repair acknowledgements are optional extensions for ordinary runs,
but mandatory after explicit repair: an older client dropping the new field
gets a refusal, never silent progression. The new repair verb has its own
schema 1; dispatch's schema-4 approved execution brief is unchanged.

On denial, preserve the branch/worktree and inspect the specific error:
stale requests need fresh inspection and authorization; decision blocks need
their original resolution; stopped runs need explicit stopped-run recovery;
configuration drift needs restoration of the approved contract or re-planning;
unmet dependencies must finish, and abandoned dependencies require re-planning.
Missing/mismatched artifacts or audit records need investigation/restoration,
not a reset or a fabricated PR. Closed/merged PRs cannot enter repair: reconcile
their actual lifecycle or use explicit abandonment/re-planning. Dirty work must
be preserved and committed/stashed before retrying. Changed scope/criteria always
require fresh plan approval. No direct MCP repair mutator or host auto-dispatch
was added; clients may surface engine guidance and the Architect uses the CLI.

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

### Integration-repair blast radius

| Element and consumers | Existing behavior retained / explicit change |
| --- | --- |
| `Repair`, `RepairRequest`, `RepairResult` | New schema 1 CLI engine transition only for recovered `worktree-ready` and pinned `awaiting-merge`. Exact authorization plus existing policy/artifact checks precede one atomic save. No autonomous action or git/GitHub mutation is introduced. |
| `state.Issue.Repairs`, `IntegrationRepair`, state validation | Optional v6 issue-local receipts add repair provenance and a post-repair verified head. No schema migration or new phase is added; existing block/resolution, attempt, routing and review-count histories are retained. Invalid receipt identity, phase, OIDs, actor/reason, timestamp and counts fail closed through **every** repair-aware `Load`/`Save` consumer, not only repair. Existing v4/v5 inspection/execution restrictions remain. |
| `ContractVersion`, `ExecutionConfig`, approval/activation and compatibility gates | Before contract v2, older lifecycle engines could accept runs while discarding optional repair receipts. New approvals and execution fingerprints bind v2; existing `checkExecutionConfig` rejects v2 in v1 engines and v1 in v2 engines before lifecycle writes. This restriction covers **every** lifecycle verb, resume and next/readiness preflight, not only repair. Status/issue-context inspection remains available; explicit abort retains its reset behavior. Existing runs are not migrated: use their original engine or abort/re-plan. |
| `loadVerb`, `checkApprovedWork`, `checkDependencies`, configuration checks | Reused unchanged, including lock ownership, stopped-run/config-drift refusals and dependency abandonment returning to the plan gate, with the versioned compatibility fence above. Repair does not exempt these checks. Session attribution and opt-in `recordMetric` retain existing behavior; enabled metrics add a `repair` event, disabled metrics stay disabled. |
| `Dispatch`, `dispatchResult` | Ordinary dispatch retains its fetch, ff-only, dependency, status and phase behavior. Its existing schema-4 execution brief is factored for reuse, not re-routed or reinterpreted. Repair returns that brief without spawning. |
| `gitops.FastForwardIn`, `FastForward`, worktree/git/path helpers | Runtime mechanics are unchanged; the historical pre-dispatch assumption is qualified in `branch.go`. Divergence refusal applies to **every ff-only operation**, not only `FastForwardIn`. Repair reuses read-only git/worktree/common-directory checks and canonical containment; no history rewrite or artifact creation/removal helper changes. |
| `Resume`, `rederive`, `reconcileAwaitingMerge` | Reconciliation and conservative immutable phases remain unchanged. Reasons now identify the separate repair path for both historical traps; the before/after above records removal of “re-dispatch/head movement is the only continuation,” not removal of decision or fast-forward gates. |
| `Review`, `ReviewRequest` | Existing schema 3, routed reviewer, stable-head, judgment coverage and cycle accumulation remain. Optional `repair_number` is required only after repair; fresh verification is required for each newly reviewed repaired head. A pre-repair request cannot authorize repair review. Every new repaired verdict invalidates the previous receipt before its first save; only successful issue **and** PR audit writes restore verification, including on the same head. An incomplete attempt retains its cycle count but cannot authorize progression; retry needs evidence again. Ordinary unrepaired reviews retain their behavior. |
| `MergeReport`, `Merge`, `MergeApproval`, `checkRepairReview` | Existing open-PR/CI, pin, human/grant and exact-head gates remain. Repaired issues additionally need the post-repair reviewed/verified live head, the latest repair number in report/approval, and a merge approval timestamp after authorization. This applies to both accepted merge-approval kinds, not only `approve-merge`; no grant is created or enlarged. |
| `Next` / `nextIssue`, `Readiness`, issue context and MCP read-only consumers | Next keeps its local-only/read-only policy and gains actionable repair prerequisites. Readiness shares the repair-review preflight and cannot report stale repair evidence current. Issue context/status expose optional receipts via existing state projections; no inspection invokes repair or satisfies an approval. Existing wire versions and MCP tool/resource counts stay unchanged. |
| CLI `runVerbs`, `runUsage`, `commands` | Adds `repair` to JSON plumbing and recovery help. It uses the same full-command mutation serializer as **every mutating run verb**, resume, abort and metrics record; the restriction is not Dispatch-specific. No adapter packaging, instruction, installed-host configuration or model selection changes. |
| Guard and remaining lifecycle | Guard code is unchanged: **only** `dispatched`, `pr-open`, `in-review` permit contained branch-matched writes. Every other phase stays non-writable until a supported engine transition; this is a phase-class rule, not a special exception for one verb or host. PR opening, escalation, decision resolution, abandonment, cleanup, abort and completion keep existing gates. |
| Regression tests | Isolated persisted lifecycle tests use real divergent/conflicting git histories and scripted GitHub responses, proving original-work preservation, containment, restart, stale-request/review/merge denial, renewed verification/review/CI/report/approval and no state mutation on invalid repair. Compatibility tests freeze the contract-v1 gate, reject unmigrated v1 execution and retain historical v1 no-fixes digests separately from intentionally changed v2 approvals. Partial-audit tests cover both body writes on the first and a subsequent same-head review, restart and evidence-bearing retry. They create no production GitHub artifacts or model sessions. |
