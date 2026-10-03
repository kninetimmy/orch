# scout-held-git-gates behavioral key v1

Controller-source only. Never export or mount this file for evaluated workers.
Author: Codex /root/implement_310, routed gpt-6.1-sol @ max, 2026-10-03.
Independent validator: pending fresh Orch review; see ../../preparation-report.md
and the stable issue #310 audit link. Author checks are not independent validation.
Historical upstream: https://github.com/kninetimmy/orch/pull/5.
Source, bytes, exact commands and control artifacts are pinned in manifest.json.
Historical merge status supports provenance only.

| Distinction | Required behavior or finding | Source/reproduction evidence | Severity if missed |
| --- | --- | --- | --- |
| 1 / confirmation | All four named destructive helpers reject a zero Confirmation before issuing commands. | internal/gitops/branch.go and worktree.go: initial c.ok guards. Reproduce `TestCorpus/confirmation`. | critical |
| 2 / checked-out | ForceDeleteBranch refuses a branch checked out in any listed worktree; the caller must independently verify the squash merge. | internal/gitops/branch.go: ForceDeleteBranch worktree loop and caller contract. Reproduce `TestCorpus/checked-out`. | critical |
| 3 / fast-forward | FastForward checks current branch and clean tree before fetch, proves ancestry before merge, and refuses divergence. | internal/gitops/branch.go: FastForward; internal/gitops/checks.go: CurrentBranch/RequireClean. Reproduce `TestCorpus/fast-forward`. | major |

Known-good outcome: Zero confirmation is rejected by all four deletion entry points. ForceDeleteBranch checks branch existence and every registered worktree before -D; it does not query GitHub to prove the PR merged. FastForward verifies current branch and cleanliness before fetching, then requires HEAD ancestry before --ff-only merge.

- Rejected outcome 1: Claim only ForceDeleteBranch needs confirmation or deletion runs before approval checking.
- Rejected outcome 2: Claim checked-out branches can be force-deleted after confirmation.
- Rejected outcome 3: Claim a dirty or diverged primary branch can reach the merge command.

Acceptable alternative and its construction: The mechanical gates have one source-defined meaning. Independently tracing all four entry points or supplying the injected-runner command sequences is an acceptable alternate outcome. Claims of GitHub verification or universal concurrency isolation are excluded.

Reference code expected failures: none.
A failure here can be the keyed historical defect, not a setup problem.
Controls contrast the exact required behavior; they do not grade natural-language
answers. For scouts, the negative examples above are refuted by source/probes;
bad code contrasts also show the reproduction would distinguish those claims.
For reviews, the reference/repair contrasts justify the verdict, while negative
outcomes cover missed blockers, false approvals and unjustified blockers as
applicable. Clean cases have no real blocker to miss; their bad controls allege
one of the refuted defects. The validator checks each example semantically,
reproduces all controls and independently checks whether any omitted real blocker
changes the classification. Agent agreement and phrase matching are insufficient.

- `bad-confirmation`: external patch `controls/git-confirm.json`; expected behavioral failures `TestCorpus/confirmation`.
- `bad-worktree`: external patch `controls/git-worktree.json`; expected behavioral failures `TestCorpus/checked-out`.
- `bad-forward`: external patch `controls/git-forward.json`; expected behavioral failures `TestCorpus/fast-forward`.

Grade scout conclusions only with the cited source or reproduction support.
Grade implementations by required behavior and scope, not reference-code shape.
Grade reviews by distinct supported blockers and verdict, with no unjustified
blocker; advisory suggestions neither fail nor repair a missed blocking defect.
Unkeyed real defects and disputes stay unresolved pending independent adjudication
and a versioned key correction. Record prior exposure and anonymization limits.
This case covers the selected component, not correctness of the whole historical
PR, complete Orch workflow, native readiness or population-level model quality.
