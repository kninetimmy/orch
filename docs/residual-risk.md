# Accept one residual review risk

This opt-in Delivery path pauses for one specific uncertainty the independent
reviewer cannot responsibly carry as advisory. It does not waive a criterion,
missing evidence, CI or GitHub protection, fix a known defect, or approve a
merge. Credible serious data loss, security risk or secret exposure use their
ordinary review/block/escalation paths. If the same review has another blocker,
use that outcome instead. Advisory findings do not need an acceptance.

## Opt in before approving a new run

Ship the following non-secret designation in a **separately reviewed committed
configuration change**, then gate a new run. Omission disables the path; local
overrides cannot set it. GateDoc 5 displays
`execution.residual_risk_admin_user_id`. Review the ID with the rest of the
contract before approving; do not change it on an active run.

```toml
[residual_risk]
admin_user_id = "<designated user's stable GitHub node ID>"
```

- Use a **user-owned repository on GitHub.com**. Orch verifies the owner is a
  `User` on the approved repository node; organization-owned and unverifiable
  repositories are outside this path, including organizations where additive
  custom roles could let a Write user edit rules. The designated human uses a
  separate GitHub account/session with standard repository **Admin**. The
  agent's `gh` must authenticate as a different standard **Write** user, not as
  the admin, an app, or a custom role. Before the authorization follow-up,
  role proof used the single-user permissions endpoint. Now Orch uses the
  [fully paginated collaborator list](https://docs.github.com/en/rest/collaborators/collaborators#list-repository-collaborators),
  which documents Write access. Both users must appear with the immutable
  numeric IDs from GitHub, exact roles and explicit permission flags. Missing
  or contradictory evidence refuses; there is no fallback.
  Keep the admin's tokens, browser session, SSH keys, and other credentials
  outside the agent environment. Do not give automation out-of-band credentials
  for the admin or any protection bypass. Never elevate a token to get a read
  to work. The engine verifies GitHub identities and current roles; it cannot
  attest who is at the keyboard, credential custody, or access to unused
  credentials.
- The supported effective policy is one repository-local required-status-checks
  rule on the default branch, with a nonempty set of unique GitHub-App-pinned
  checks. Other supported repository-local rules are deletion, non-fast-forward,
  pull-request review, linear history and signed commits; GitHub still enforces
  them. Orch verifies that classic protection is absent and reads the full
  effective rules, including possible inheritance. GitHub must also report
  `current_user_can_bypass: "never"` for automation on every applicable ruleset.
  Orch refuses classic or mixed protection, inherited or unknown active rules,
  merge queues, multiple
  required-check rules, unpinned or ambiguous checks, or inaccessible policy.
  The optional creation-only flag `do_not_enforce_on_create` may be absent:
  its absence is recorded distinctly from `false`, and null or non-Boolean
  values are refused. It cannot waive checks for the existing base branch.
  Do not remove existing protection to fit this feature. Ineligible repositories
  can use ordinary Delivery.
- Successful CI means one identifiable GitHub CheckRun per required context
  from the expected app and repository at the **full PR head commit**, completed
  with `SUCCESS`. Missing reports, another app, role/probe errors, non-success
  conclusions (including skipped and neutral), duplicate names, and incomplete
  rollups (including over one 100-context page) cannot qualify. Ordinary
  `gh pr checks --required` is not this proof.
- All three hosts use GateDoc 5, Activation 4, Review 5 and approval contract 5.
  Keep the original engine/adapter to finish an older active run, or explicitly
  abort and re-plan. Do not migrate active runs. Grants cannot use this path.

## Record a hold and obtain the admin's decision

1. Use the primary checkout for engine verbs. Have a **fresh routed reviewer**
   independently review the stable live head. Submit its Review v5 with
   `verdict: "risk-hold"`, every criterion `satisfied` with a reason, complete
   required verification and execution evidence, and:

   ```json
   "risk_hold": {
     "trigger": "The specific condition that could occur",
     "impact": "Who or what is affected and how",
     "recovery": "How the affected party can recover",
     "eligible_only": true
   }
   ```

   Each field is at most 2,000 bytes and must be nonblank. `eligible_only`
   attests that the one residual uncertainty is the only blocker and isn't a
   defect, credible serious loss, security risk or secret exposure. Evidence
   for each approved required test must identify the exact `command` and
   `result: "pass"` or `"passed"` in the audit on this head, including tests CI
   does not run. Include the latest `repair_number` and fresh post-repair
   verification where applicable. No evidence status or reviewer judgment is
   promoted by the engine; the independent reviewer must honestly examine the
   artifacts and eligibility. The routing check does not authenticate the
   reviewer's session or substitute for dispatching a fresh reviewer.

2. Confirm Review result v5 with `phase: "blocked"`, `verdict: "risk-hold"`,
   `risk_block_id`, and `risk_challenge`. Show the designated admin the exact
   PR, head, trigger, impact, recovery and challenge. In their separate GitHub
   session, the admin inspects that revision and submits a new review through
   **Files changed → Review changes → Comment** (not Approve, Request changes,
   a pending review, an inline comment, or a conversation comment). Leave the
   submitted review unedited. Its body is the exact first line below, one
   blank line, and a nonempty reason of at most 2,000 bytes. Do not include
   credentials or secrets in the reason; the review and audit are visible to
   repository readers.

   ```text
   orch accept-risk <exact challenge from the engine>

   I accept this specific uncertainty because <reason>.
   ```

3. Obtain the review's GitHub **node ID**, not its numeric database ID; for
   example inspect the matching permalink using read-only
   `gh pr view <registered PR URL> --json reviews` as automation. Send the
   following JSON on stdin to `orch run accept-risk` and require result v1:

   ```json
   {
     "schema_version": 1,
     "run_id": "<current run ID>",
     "issue_number": 123,
     "block_id": 1,
     "decision_review_id": "<GitHub PR review node ID>"
   }
   ```

   The engine fetches the decision, compares the complete binding and rechecks
   nonempty successful CI. It retains the original hold and writes one receipt
   with the GitHub actor, reason, review, exact head, policy and check identities.
   It transitions to `in-review` without changing the run, issue, PR, branch or
   worktree, minting a commit, or consuming a fix cycle. The recorded verdict
   remains `risk-hold`, not `approve`.

## Review again, then use the separate merge gate

- On the same head and repair, send the exact accepted block and receipt to a
  **fresh independent routed reviewer**. Review v5 approval must include all
  satisfied judgments/evidence and
  `"risk_follow_up":{"block_id":1,"disposition":"acknowledge-accepted","reason":"<the reviewer's reasoning>"}`.
  One follow-up review can apply the decision; a superseding review cannot
  reuse it. A later eligible concern needs a fresh hold if it persists.
- If the head changes while held, inspect the full live head and obtain a new
  routed review (the detached review checkout accepts that different live head).
  Use an ordinary fix/evidence outcome for new blockers; if the uncertainty
  persists and remains eligible, submit a new hold with a new challenge and
  decision. If independent evidence shows it is absent on the new head or after
  a newer integration repair, an approving review must address the latest
  block with `disposition: "no-longer-present"` and say why. An older decision
  never carries forward.
  If a later secret was reported on the held issue, [resolve its separate
  decision first](decision-block-recovery.md); releasing the global stop alone
  does not permit changed-head review.
- Use `orch run merge-report` on the approving head, then obtain **a separate
  fresh human merge approval**, dated strictly after `risk_merge_report_at`.
  `orch run merge` checks the current review, applicable policy and successful
  CI again and asks GitHub to merge only the exact expected head on the
  verified PR node. The risk COMMENT is not the merge approval. Normal
  cleanup and completion are still separate.

Neither generic resolution nor resume can discharge a residual-risk hold.
For **every issue with any recorded hold**, even on a later head, this path
cannot use manual CI, grant approval, or already-merged recovery. An externally
merged PR (or an uncertain response after sending the merge) must be investigated
with its audit preserved, not marked delivered through an old pin. Existing
ordinary recovery remains available to issues outside the path.

On a partial audit write, retry the exact review/acceptance request after
inspecting the engine refusal. Both mirrors must converge before phase changes;
a changed head, review, policy or check observation during an unfinished write
can require preserving the run and returning through fresh plan approval.
The engine re-observes GitHub at each gate; those reads are not a transaction
with another actor's writes. GitHub enforces the expected-head check and its
protections at merge. The working trust assumptions above still apply.

## Blast radius

| Element | Before → after; what still holds |
| --- | --- |
| Committed TOML `Config`, `Parse`, `Render`, `Revision`, local overlay | No risk authorization setting → optional `residual_risk.admin_user_id`. It round-trips and contributes to the semantic revision under every host configuration. Omission leaves the ordinary configuration/revision unchanged. The restriction applies to the entire new table and all its leaves in every local override. |
| `ExecutionConfig`, `GateDoc`, `Plan`, `Activate`, `PlanRef`, CLI/MCP/doctor pins | Gate 4/Activation 3/contract 4 → 5/4/5 for all three hosts and every activation path. Keep the exact target, scope, config, approval, and MCP workspace/human checks; show the optional admin and retain the verified repository ID at activation. Status/issue-context inspection can still read an older run; all lifecycle execution and resume still require the matching contract. No new MCP mutator. |
| `state.Issue`, `Block`, `RiskHold`, `RiskAcceptance`, `RiskReviewPending`, `validateIssues`, `LatestRiskBlock` / `ActiveRiskBlock` | Original typed blocks/history → a dedicated cause, immutable hold/decision references, pending review retry and merge-report time. State schema 6, its atomic save and existing phases remain. A risk block cannot carry a generic resolution; ordinary block records are unchanged. |
| `Review`, `ReviewRequest` / `ReviewResult`, `reviewVerdict`, `checkJudgments`, `reviewRisk`, `checkRiskFollowUp` / `verifyRiskFollowUp` | Review 4's approve, request-changes and needs-evidence → Review 5 retains all three and adds `risk-hold`, optional hold/follow-up data and a challenge result. Every verdict still requires routed selection, one reasoned judgment per criterion, and live reviewed head. Only risk hold and risk-path approval add the exact required-test proof; neither allows unverified, wrong or unsatisfied criteria. Missing-evidence collection, genuine fix cycles and existing reviewer permissions remain. |
| `openRiskAudit`, `currentRiskReview`, `riskTestEvidence`, `riskReceipt`, `addRiskEntry`, `readinessReviewEvidence`, `sameReadinessAudit`, `rejectEngineOwnedNames`, `upsertCapped` | Ordinary audit/readiness and rotating prose remain. Risk adds exact binding and matching typed receipts in both mirrors. **Every** numbered `risk-hold-N`, `risk-acceptance-N` and `risk-review-N` is reserved in all caller-verification inputs and retained whole, like existing execution-evidence receipts. Neither general audit prose nor a missing mirror promotes proof. |
| `AcceptRisk`, `readRiskDecision`, CLI run dispatch/usage | No authenticated same-head disposition → schema-1 `accept-risk` under the existing mutation serializer. It is restricted to the current dedicated hold, verified decision and strict CI. It leaves the prior non-approving verdict, worktree and review/fix history unchanged; an exact retry cannot create a second acceptance. |
| GitHub `RiskRead` and `RiskRoles`; owner comparison in `openRiskAudit` | Before the follow-up, risk probes accepted any GitHub.com repo owner type and relied on two single-user permission reads. After, they verify a user owner on the bound repository node and require both immutable users' exact standard roles and flags from the complete collaborator list. The owner also has to agree across the bracketed CI/audit read. These restrictions apply to every residual-risk gate; ordinary GitHub operations are unchanged. |
| GitHub `RiskRequiredPolicy`, `RiskPolicy`, `RiskCI` | Before the follow-up, the creation-only flag had to be present. After, an omitted flag remains absent in the fingerprint and receipt, distinct from explicit `false`; malformed and null values refuse. The branch/policy reads, independent ruleset `never` proof, and nonempty app-pinned exact-head `SUCCESS` checks still apply to every residual-risk gate that uses them. Skipped checks still do not count as success. Ordinary `RequiredCI` is unchanged. |
| GitHub `RiskDecision`, `WriteRiskAudit`, `MergeRiskPR` | These retain node-bound GraphQL and expected-head enforcement on the verified PR node. The role-proof follow-up does not change the decision, audit or merge mutation contracts. |
| `Block`, `ResolveBlock`, `Resume` and `ReviewWorktree` | Operational recovery and explicit ordinary decision resolution remain. A generic block cannot replace the original risk reason but invalidates its acceptance eligibility. Neither generic resolution nor resume can discharge the risk hold; only its dedicated decision or fresh review of a different live head can progress. The review checkout stays detached/unregistered; only a held residual-risk issue can request this blocked-phase checkout. |
| `Repair`, `CI`, `Next`, `Readiness` | Existing repair authorization and invalidation, ordinary CI reporting and read-only guidance remain. A new repair invalidates the risk review/report, too. Strict CI inspection on a risk issue does not overwrite the ordinary CI audit singleton; next/readiness distinguish hold/retry and strict non-authorizing merge evidence. |
| `MergeReport`, `checkApprovingReview`, `Merge`, `checkHumanCIException` / `acknowledgeMerged`, `finishMerge` | Same ordinary wire, head pins, closure/audit/cleanup and separate approval remain. For **every issue with a recorded hold**, only an independently approved current review, unchanged policy, strict green CI and a new human approval after the report permit merge. Manual CI, grant approval and both forms of already-merged recovery stay available only outside this path. |
| Review metrics, adapter tests and instructions | Review metrics still record verdict/cycle/usage; the new residual block cause is not a fix count. Claude Code, Codex and OpenCode Delivery instructions, and both reviewer profiles on each host, use the same contract and preserve routing/permissions. Existing CI/workflow files and branch protection are not changed. |
