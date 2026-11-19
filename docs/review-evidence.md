# Evidence-only review handoff

Before review schema 4, the only verdicts were `approve` and `request-changes`,
and criterion judgments were `satisfied`, `unsatisfied` or `wrong`. Missing
execution evidence therefore looked like a code-fix request or an invalid
criterion. Now `needs-evidence` is explicitly non-approving and `unverified`
means a valid criterion lacks adequate evidence. Neither is approval, a new
phase, an execution permission or an autonomy grant.

Every review still carries one numbered judgment and nonblank reason per
approved criterion. `needs-evidence` requires at least one `unverified`
criterion and permits only `satisfied` for the others. Approval with any
unverified criterion, or an evidence-only verdict claiming an unsatisfied or
wrong criterion, is refused. A genuine finding still uses `request-changes`,
including mixed findings/missing evidence. A wrong criterion still blocks for
the human; the executor cannot amend approved scope.

## Collect, then independently review

1. The routed reviewer identifies missing evidence, its criteria and the full
   reviewed PR head. The Architect transcribes the report into schema-4
   `orch run review`, never substituting its own judgments.
2. The engine retains `in-review`, records the outcome and pending criterion
   numbers, and writes `review-evidence-<cycle>` alongside the existing review
   cycle/current criterion entries in the issue audit home and open PR mirror.
   The receipt retains **all** judgments/reasons, artifact metadata and the full
   engine-observed head even under body-cap rotation. Oversized retained history
   fails closed rather than discarding it. `orch run next` after reload directs
   evidence collection plus fresh review, not fix-and-review or merge.
3. The executor or Architect collects execution output under its **existing**
   permissions. OpenCode's reviewer still cannot edit or use shell. Claude and
   Codex keep their existing read-only investigation/test permissions. No adapter
   gains a tool, permission or authorization from this outcome.
4. Dispatch a **fresh** independent reviewer using the currently routed
   selection and stable live head. Supply identifiable artifacts and have that
   reviewer judge their provenance, actual result and criterion fit. Submit its
   judgments with `execution_evidence` for every pending criterion it now judges
   satisfied. A same-head approval requires no intervening fix commit.

For example, the recovery request includes this metadata (replace every example
value with observed evidence; the command is data, not an instruction to Orch):

```json
{
  "execution_evidence": [{
    "criterion": 2,
    "head_oid": "0123456789abcdef0123456789abcdef01234567",
    "artifact": "file:///evidence/owner-output.log#sha256=<actual-digest>",
    "command": "existing-owner-check --show-owner",
    "result": "pass: owner=worker-1",
    "collected_by": "executor-session-1",
    "collected_at": "2026-11-19T16:00:00Z"
  }]
}
```

Each supplied criterion has one artifact entry; the same log can identify
multiple criteria through separate entries. Head must be the full live reviewed
head; artifact, command, result and attribution must be nonblank and at most
2,000 code points each; collection time must be valid RFC3339, non-future and
not predate an integration repair. Generic `verifications` cannot discharge a
pending criterion: their automatic head stamp is not proof that an old log was
executed on that head.

Pending evidence survives genuine fixes, resume and integration repair.
Criteria that required evidence are remembered after discharge: a later head
or repair again needs evidence for those criteria and fresh independent review.
An old review cannot pin a newer head under either ordinary or manual-CI
merge-report. Approval also requires both review audit writes to have succeeded;
a partial write keeps evidence pending and cannot authorize a merge.
Integration-repair approval still requires its repair acknowledgment and fresh
verification; a non-approving report may describe the missing repair evidence.
Required CI and the separate human merge gate are unchanged.
Review-cycle timestamps retain their existing second precision for comparison
with GitHub's `mergedAt`; evidence receipts retain the same instant at full
precision so sub-second collection times remain verifiable after reload.

**Limits:** metadata and `collected_by` are caller assertions, not authenticated
proof. Orch validates shape, head and repair/time staleness; it does not open
artifacts, execute command text, authenticate a fresh agent/session, or judge
whether an output proves a criterion. Same-head age, environment suitability,
artifact identity/content and result fit remain the independent reviewer's job.

## Compatibility

Review request/result schema **4** and approval contract **3** are closed.
The optional review fields remain in state schema 6; manifest schema 5's
generic verification format is unchanged. Contract-2 engines refuse contract-3
runs before lifecycle saves could discard new fields/receipts. This engine
likewise refuses execution/resume of older approved contracts. Inspection
remains available; no active-run migration is performed. Preserve and finish
with the original engine, or abort and obtain fresh plan approval. Do not
replace the installed engine or rewrite an active authorization to continue.

## Blast radius / PR-body enumeration

| Element touched | Before → after; preserved behavior |
| --- | --- |
| `run.Review`, verdict/judgment sets, request/result wire | Before: two verdicts, three judgments. After: explicit non-approving evidence handoff, per-criterion pending metadata and identifiable execution input. Routed selection matching, approved criterion coverage, wrong-criterion human block, genuine change requests, live-head check, usage events and existing phase remain. |
| `ContractVersion`, effective execution fingerprint/compatibility gate | Before: contract 2. After: contract 3 explicitly fences evidence-unaware engines and refuses old approved runs. The existing digest, plan approval and no-hot-migration rules still apply to every lifecycle verb and resume, not only `Review`. |
| `state.Issue`, `validateIssues`, state save/reload | Before: verdict/count only for ordinary review. After: successfully audited reviewed head, pending criteria and historically evidence-dependent criteria persist with sorted/in-range validation. State remains v6, atomic-save and existing phase/repair/block validation remain. No migration. |
| New `ExecutionEvidence`, `reviewEvidenceRecord`, `reviewVerdict`, `checkExecutionEvidence`, `unionCriteria`, `reviewEvidenceReceipt`, `checkReviewEvidenceReceipt`, `reviewEvidenceNamePattern`; existing `rejectEngineOwnedNames` / `upsertCapped` | New numbered receipts reuse existing manifest verifications. Before: every review detail could rotate away. After: complete evidence receipts survive or fail the body limit. **Every** `review-evidence-N` name is reserved, not only `review-evidence-1`; every caller-input path uses the shared rejection function. Ordinary detail caps, caller replace-by-name entries and append-only review cycles remain. Manifest schema/render/parser are unchanged. |
| `nextIssue`, `readinessReviewEvidence`, `sameReadinessAudit` | Before: non-approval meant fix-and-review; audit knew only old sets. After: evidence-only directs collect-and-review, mixed fixes retain pending needs, and readiness recognizes/validates new outcomes and retained receipts against state/head. Ordinary rotated prose can still differ; retained evidence receipt contents cannot. Inspection stays non-authorizing; its schemas remain unchanged. |
| Grant review gate and historical `fixCycleBlocked` | Before: every non-approval counted. After: `needs-evidence` counts neither now nor historically; genuine request-changes still counts, including after a resolved limit block. Existing grant scope/session/expiry/protected-file/merge stops are unchanged. |
| `MergeReport`, `checkApprovingReview`, manual-CI shared prerequisites | Before: ordinary non-repair approval could pin a newer live head than reviewed. After: both paths require the successfully recorded reviewed head and no pending evidence. No evidence-only merge report. Existing repair, CI, pin, manual human-decision and later merge approval rules remain. |
| Repair handling in `Review`, `Repair` and resume's `applyOutcome` | Before: even non-approving repaired reviews needed verification. After: a non-approval may record missing evidence but cannot mark an unverified repair verified; approval still needs repair verification. Existing approval invalidation also clears the successful reviewed-head marker; pending/history criterion evidence is not erased. Repair authorization, guard phases and recovery/decision semantics remain. |
| CLI run help; Claude/Codex/OpenCode Delivery guides; both reviewer definitions per host | Before/after handoff and wire version are stated in each affected guide. All six reviewers distinguish missing evidence from defects/invalid criteria; OpenCode execution remains outside reviewers. Existing model routing, tools/frontmatter/permissions and gates remain. |
| Deterministic run/state/CLI/MCP/adapter/metrics fixtures and checks | Existing lifecycle/routing/repair/CI/usage tests remain; schema/head fixtures follow the new closed contract, including valid stale-head OIDs and successfully audited head markers. Added engine and actual-CLI regressions cover non-approval, same-head recovery, missing/stale/mixed evidence, durable reload/retention, partial writes, contract mismatch and grant accounting without provider/model calls. Adapter tests recognize the already shipped read-only `next` verb. Prose-presence checks assert shipped contracts, not model behavior. |

Unchanged: guard implementation and writable-phase set, native host/plugin
execution, routing policy, manifest field schema, CI workflows, dependencies,
grant authorization/storage, installed binaries and release state. A successful
review still is not human merge approval.
