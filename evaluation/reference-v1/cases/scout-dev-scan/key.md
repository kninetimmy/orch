# scout-dev-scan behavioral key v1

Controller-source only. Never export or mount this file for evaluated workers.
Author: Codex /root/implement_310, routed gpt-6.1-sol @ max, 2026-10-03.
Independent validator: pending fresh Orch review; see ../../preparation-report.md
and the stable issue #310 audit link. Author checks are not independent validation.
Historical upstream: https://github.com/kninetimmy/orch/pull/15.
Source, bytes, exact commands and control artifacts are pinned in manifest.json.
Historical merge status supports provenance only.

| Distinction | Required behavior or finding | Source/reproduction evidence | Severity if missed |
| --- | --- | --- | --- |
| 1 / hidden | Every dot-prefixed descendant directory is skipped; the root itself is not skipped. | internal/instructions/scan.go: Scan directory branch. Reproduce `TestCorpus/hidden`. | major |
| 2 / root-pair | Root's direct AGENTS.md and CLAUDE.md are excluded from nested conflicts. | internal/instructions/scan.go: isRootPair call and helper. Reproduce `TestCorpus/root-pair`. | major |
| 3 / names | Both target basenames are recognized case-insensitively; README.md is not a target. | internal/instructions/scan.go: targetNames and isTargetName. Reproduce `TestCorpus/names`. | major |

Known-good outcome: Scan skips every dot-prefixed descendant directory through SkipDir, excludes the direct root pair, and compares the two target names with EqualFold. It returns conflicts and never edits them. The hidden-directory rule applies to every directory basename of that form, not only .git.

- Rejected outcome 1: Claim .memhub and arbitrary .hidden directories are traversed because only .git is skipped.
- Rejected outcome 2: Claim root instruction files are nested conflicts.
- Rejected outcome 3: Claim only uppercase AGENTS.md is recognized, or README.md also qualifies.

Acceptable alternative and its construction: Different source-defined behavior is not meaningful. An independently constructed directory fixture and the three subtests are acceptable in place of a line-by-line trace; symlink and unreadable-file behavior lies outside this case's required distinctions.

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

- `bad-hidden`: external patch `controls/scan-hidden.json`; expected behavioral failures `TestCorpus/hidden`.
- `bad-root`: external patch `controls/scan-root.json`; expected behavioral failures `TestCorpus/root-pair`.
- `bad-names`: external patch `controls/scan-names.json`; expected behavioral failures `TestCorpus/names`.

Grade scout conclusions only with the cited source or reproduction support.
Grade implementations by required behavior and scope, not reference-code shape.
Grade reviews by distinct supported blockers and verdict, with no unjustified
blocker; advisory suggestions neither fail nor repair a missed blocking defect.
Unkeyed real defects and disputes stay unresolved pending independent adjudication
and a versioned key correction. Record prior exposure and anonymization limits.
This case covers the selected component, not correctness of the whole historical
PR, complete Orch workflow, native readiness or population-level model quality.
