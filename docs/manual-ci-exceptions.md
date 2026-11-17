# Manual CI acceptance and already-merged acknowledgment

These are **human-only, request-local assertions**, not stored bypass permissions.
The CLI cannot authenticate a human: as with ordinary approval, the Architect
must obtain an actual affirmative answer and record its author and time. Never
infer that answer from a stalled runner, a grant, a prior receipt or a merged PR.

## Before and after

| Before #517 | After |
| --- | --- |
| Blank stdout with the exact single-line `no required checks reported on the '<branch>' branch` notice was accepted only on exit 0. The observed exit-1 notice failed. | Exit 0 **or 1**, with empty/whitespace-only stdout and only that notice, means `no-checks`, retaining the total rollup count. This is not passing CI. Exit 8 without data, unsupported exits, mixed/unrelated stderr, malformed arrays/rollups and invalid buckets fail closed. Nonempty check data accompanied by stderr now also fails closed. |
| Pending, failed or cancelled required checks prevented a merge report and open-PR merge with no manual continuation. | This remains the default. An explicit human `ci_exception` can accept those **observed check results** for one current run/issue/PR/independently reviewed full head and repair generation. It cannot accept a failed authentication, transport or malformed probe. |
| An out-of-band merge stranded `in-review` work; resume recorded an operational block and would not invent a merge-report pin or approval. | Resume still does exactly that. A separate explicit post-merge acknowledgment can finish an already-merged, independently approved reviewed head, including that exact operational block. It records acknowledgment **after** the merge, not prior approval. |
| All verification details could rotate out under the body cap. | Ordinary details still can. Every numbered manual CI receipt retains its complete decision and actual CI snapshot, including historical receipts; an oversized protected history fails closed instead of erasing it. |

## Inspect and ask first

Use the primary checkout for lifecycle commands. Inspect `orch run status --json`,
`orch run readiness --issue 123 --json`, and the live PR. Obtain the full head OID
from the stable, independently approved routed review, not from an assumption
that the current PR is reviewed. The latest cycle and **every** acceptance
judgment must agree with run state, approved work/routing/configuration and the
live head. For an open PR its audit mirror must agree too; a partial manual
receipt mirror write alone is retryable.

Present actual CI (`state`, `required` names/states/buckets/links, `total`), the
run, issue, PR, head and latest repair number. Ask whether the human accepts that
incomplete CI **for this identity only**, and obtain a nonempty reason. This is
not the separate merge confirmation.

The examples below are JSON documents on stdin, not shell-interpolated values.
Replace all illustrative identities and timestamps with the inspected values
and actual human decisions. `approved_at` must not be in the future and must be
strictly after the approving review and latest repair. `repair_number` is zero
(or omitted) without a repair, otherwise the latest issue-local repair number.

## Open PR: accept CI, then separately approve merge

Send to `orch run merge-report`:

```json
{
  "schema_version": 1,
  "issue_number": 123,
  "ci_exception": {
    "run_id": "<current run ID>",
    "issue_number": 123,
    "pr_number": 124,
    "head_oid": "0123456789abcdef0123456789abcdef01234567",
    "approved_by": "<human decision maker>",
    "approved_at": "2026-11-17T20:00:00Z",
    "reason": "Accept the unavailable runner for this reviewed commit",
    "statement": "accept-incomplete-ci",
    "repair_number": 0
  }
}
```

Show the returned report, including its unchanged `ci` and `ci_exception`
receipt. Only after a **separate fresh affirmative merge confirmation** send
to `orch run merge`:

```json
{
  "schema_version": 1,
  "issue_number": 123,
  "approval": {
    "pr_number": 124,
    "head_oid": "0123456789abcdef0123456789abcdef01234567",
    "approved_by": "<human merge approver>",
    "approved_at": "2026-11-17T20:01:00Z",
    "statement": "approve-merge",
    "repair_number": 0
  },
  "ci_exception": {
    "run_id": "<current run ID>",
    "issue_number": 123,
    "pr_number": 124,
    "head_oid": "0123456789abcdef0123456789abcdef01234567",
    "approved_by": "<human decision maker>",
    "approved_at": "2026-11-17T20:00:00Z",
    "reason": "Accept the unavailable runner for this reviewed commit",
    "statement": "accept-incomplete-ci",
    "repair_number": 0
  }
}
```

Resubmit the explicit CI assertion with the merge request; the recorded receipt
never supplies it. The merge confirmation must be at or after the CI decision,
and after any repair. Orch rechecks the live identities, review and CI. GitHub
still enforces branch protection; the command remains the configured strategy
plus `--match-head-commit`, never `--admin` or another protection bypass.

## Already merged: acknowledge what happened, do not invent approval

If the registered PR is already `MERGED`, its head must equal the independently
approved review. Ask a different question: does the human acknowledge this
**already completed merge**, accept its actual CI evidence with the stated
reason, and authorize issue closure/bookkeeping? Record the answer after the
GitHub merge time. A review recorded after that merge cannot qualify.

Send to `orch run merge`, **omitting `approval` and `ci_exception`**:

```json
{
  "schema_version": 1,
  "issue_number": 123,
  "merged_acknowledgment": {
    "run_id": "<current run ID>",
    "issue_number": 123,
    "pr_number": 124,
    "head_oid": "0123456789abcdef0123456789abcdef01234567",
    "approved_by": "<human acknowledging the completed merge>",
    "approved_at": "2026-11-17T20:05:00Z",
    "reason": "Acknowledge this reviewed PR merged outside Orch despite cancelled CI",
    "statement": "acknowledge-already-merged-pr",
    "repair_number": 0
  }
}
```

This accepts `in-review`, an existing matching `awaiting-merge` pin, the exact
out-of-band-merge operational reconciliation block, or `merged` for a retry.
It never invokes `gh pr merge`, adopts another head, replaces a conflicting pin,
or clears an unrelated operational block, unknown legacy block or unresolved
decision. All historical blocks/resolutions remain. Only at successful closure
does state record the delivered reviewed head in `approved_head_oid` (required
by the existing merged-state invariant); it does not create an earlier
`awaiting-merge` gate or prior merge-approval record.

Both paths then use ordinary, separate `orch run cleanup` with
`{"schema_version":1,"issue_number":123,"statement":"cleanup-issue"}` and, once
**every** issue is cleaned, `orch run complete` with `{"schema_version":1}`.
Neither path implicitly deletes artifacts or completes a run.

## Audit, retries and compatibility

The issue is the durable audit home; only an **OPEN** PR receives a mirror.
Engine-owned `ci-exception-N` and `merged-acknowledgment-N` entries retain the
run/issue/PR/URL/full head, actor, actual decision time, reason, repair generation,
statement, full observed CI and engine observation time; acknowledgment also
retains GitHub's merge time. These names are refused from **both** caller-evidence
inputs (`pr-open` and `review`), for every numbered receipt, not just `-1`.
Existing `required-ci` observations are not rewritten to passing or removed.
Reports expose `ci_exception`; merge results expose `manual_ci_audit`, including
historical receipts. Identical retries retain the first receipt/time; changed
observations append history. Body limits can refuse new receipts, never silently
truncate their reasons or CI evidence.

Enabled metrics retain the ordinary completed-merge observation. A post-merge
acknowledgment marks its `reason` as `acknowledge-already-merged-pr` and omits
`approval_source`, rather than inventing a prior human/grant merge approval.

If a body write or merge fails, preserve artifacts and inspect the reported
partial step (`orch resume --dry-run` is useful). Reconcile normally, then retry
with the same explicit decision only while its run, issue, PR, reviewed head and
repair still match. A partial receipt is not authority. A new commit, new review
after the decision or integration repair requires new inspection and a new
human decision. After a completed open-PR merge, ordinary matching-pin crash
recovery remains available; unpinned out-of-band merges require the distinct
acknowledgment above.

State remains schema 6, approval contract 2, manifest 5 and request schemas 1.
Existing compatible active runs opt in without changing approved plan text,
digest or execution fingerprint. Incompatible/older runs retain their refusal:
use their original engine or abort and re-plan. Older engines reject the new
request fields; no persisted permission can be silently dropped into authority.
An older engine may not preserve the new audit details under its old body-cap
policy, so finish this opt-in flow with an exception-aware engine.

No exception changes scope, review/criterion judgments, hooks, containment,
authentication, GitHub policy, secret-stop recovery or decision resolution.
Neither assertion may name `grant:<id>`; `grant-approve-merge` cannot carry a CI
exception. Existing grant stops and required-passing-CI policy remain. No native
MCP mutator, installer/configuration change, dependency or automatic exception
is introduced. Manifest consistency remains non-cryptographic: consistent manual
edits cannot establish real human identity or independently prove provenance.

## Blast radius

| Element / consumers | Existing behavior retained or explicit change |
| --- | --- |
| `GH.RequiredCI`, `deriveCIState`, `CISummary` | Exit-1 exact blank-output notice is now supported; total and honest no-checks/passing/pending/failing meanings remain. Missing/null rollups and stderr mixed with check data now fail closed. Known-bucket arrays on exits 0/1/8 remain evidence. No probe error becomes a CI state. |
| **All original five consumers**: `CI`, `MergeReport`, `Merge`, `Readiness`, `observeIssue`/`Resume`, plus new `observeManualCI` | All six shared-helper callers receive the same parser behavior and probe errors. Polling still only observes/writes actual CI, readiness stays unlocked/read-only and unauthorized, resume never invents approval. `requireMergeableCI` itself is unchanged for every ordinary caller; only explicitly asserted manual report/merge paths can accept pending/failing results. |
| `HumanCIException`, `CIExceptionStatement`, `MergedAcknowledgmentStatement`, `checkHumanCIException`, `observeManualCI` | New request-local human decisions pin the current tuple and repair generation, reject grants/unresolved decisions, reuse execution/approved-work/dependency/review validation and bracket probes with PR reads. Full independent review and every criterion are mandatory for **both** manual paths, not merely the open-PR exception. No persistent permission or general exception framework. |
| `MergeReportRequest`/`Result`, `MergeReport`, `mergeReportResult` | Optional schema-1 `ci_exception` records a receipt before the ordinary head pin/needs-human gate. Default open-PR/approving-review/CI requirements remain. Results keep real CI plus the receipt. Pure readiness previews cannot manufacture it or authorize a merge. |
| `MergeRequest`/`Result`, `Merge`, `finishMerge`, existing `metrics.Event` | Optional schema-1 `ci_exception` still requires separately confirmed `approve-merge`; it cannot accompany grant approval. Separate `merged_acknowledgment` authorizes only reviewed already-merged closure. Ordinary closure/status/metrics and separate cleanup remain; acknowledgment's merge observation carries a reason marker and no fabricated prior `approval_source`. Already-merged ordinary retries now also refuse a differing live head; the post-merge read verifies the exact pinned head before any delivery bookkeeping. |
| `acknowledgeMerged`, `outOfBandMergeReason`, `reconcilePROpen` | Adds explicit recovery of only the corresponding known operational block. The historical reason remains byte-stable for existing runs. Resume's classification, no approval adoption, phase/decision behavior and observations remain unchanged without the explicit request. |
| `recordManualCI`, `manualCIRecord`, `manualCIAuditPattern`, `withoutManualCIAudit` | New append-only, non-authorizing receipt snapshots; exact retries deduplicate, changed observations append. Only receipt differences may be repaired after a partial mirror write; all other canonical audit evidence must agree. Neither receipt discovery nor removal for comparison grants authority. |
| `manifestbody.go`: `engineVerificationNames`, `rejectEngineOwnedNames`, `upsertCapped`, `writeManifest`; `Manifest.Verification` | Existing singleton ownership remains; both caller-verification verbs additionally reserve **every** numbered manual receipt. Body rotation still removes ordinary details, but protects all manual decision/evidence details; oversized protected bodies fail. Issue durability, OPEN-only PR mirroring, verification shape, manifest schema/render/parser and its non-cryptographic limitation are unchanged. |
| `loadVerb`, configuration/compatibility, state and approval contracts | Reused unchanged; lock, stopped-run, effective-configuration, state6/contract2 and phase refusals remain. No state permission, migration, plan/fingerprint rewrite, routing change or new phase. Acknowledgment sets only the delivered reviewed head/merged phase and clears its eligible blocked reason; original histories stay. |
| `Complete`/`terminalCounts`, cleanup, abandonment, repair, grants, guards and git/gh helpers | Runtime cleanup/completion/counting remain unchanged: delivered reviewed head marks merged work, all issues must be cleaned, primary must be clean/default and fast-forward. The comment now includes acknowledgment as another source of that delivered-head pin. Repair still invalidates reviews/approvals and needs new decisions. Grant, hook, containment, signing and GitHub-protection boundaries are not relaxed; no helpers gain bypass flags. |
| CLI `runRunVerb`, `runUsage`, command help | Adds read-only `run help`/`--help`/`-h` and exact manual request guidance. Existing JSON verbs, stdin/stdout and **whole-command mutation serialization for every mutating verb**, not just merge, remain. No new MCP surface or adapter runtime. |
| Shipped Claude/Codex/OpenCode Delivery skills, README, PRD and recovery/readiness docs | Add opt-in instructions and separate human confirmations, document the distinct post-merge path and preserve historical before/after statements in place. Existing ordinary workflow and host-specific presentation/routing requirements remain. |
| Regression tests and shared `adaptertest.CheckRunVerbTokens` / `runVerbTokens` | Parser tests pin exact observed exit 1 and malformed/mixed failures; isolated real-git CLI/lifecycle tests exercise independent review, explicit decisions, actual CI, truthful enabled metrics, closure, retries, separate cleanup and completion, plus default/identity/review/repair/decision/grant/probe refusals. Caller-forgery and protected-cap tests cover every receipt kind. The shared skill-token allowlist now admits actual read-only `run help` for all three hosts; unknown commands still fail. No hosted runner, production repository, installed binary or live SamTV mutation. |
