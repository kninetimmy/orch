# scout-dev-paths behavioral key v1

Controller-source only. Never export or mount this file for evaluated workers.
Author: Codex /root/implement_310, routed gpt-6.1-sol @ max, 2026-10-03.
Independent validator: pending fresh Orch review; see ../../preparation-report.md
and the stable issue #310 audit link. Author checks are not independent validation.
Historical upstream: https://github.com/kninetimmy/orch/pull/4.
Source, bytes, exact commands and control artifacts are pinned in manifest.json.
Historical merge status supports provenance only.

| Distinction | Required behavior or finding | Source/reproduction evidence | Severity if missed |
| --- | --- | --- | --- |
| 1 / segments | Sibling root-name prefixes and parent traversal are outside; root and ordinary children are inside. | internal/paths/paths.go: Inside and inside; filepath.Rel boundary test. Reproduce `TestCorpus/segments`. | major |
| 2 / missing-tail | Canonical resolves the deepest existing ancestor and appends missing components; it does not create them. | internal/paths/paths.go: Canonical rest loop. Reproduce `TestCorpus/missing-tail`. | major |
| 3 / nearest-root | FindRoot returns the nearest qualifying ancestor; no qualifying ancestor wraps ErrNotFound. | internal/paths/paths.go: FindRoot upward scan. Reproduce `TestCorpus/nearest-root`. | major |

Known-good outcome: A shared string prefix is insufficient: the relative-path test rejects ../ and exact parent traversal. Canonical preserves missing components after resolving the existing ancestor. FindRoot checks the current directory first, then walks upward, so a nested marker wins. This is path logic, not an OS or model sandbox.

- Rejected outcome 1: Claim that /repo-other is inside /repo because the string prefix matches.
- Rejected outcome 2: Claim that a missing tail is discarded or must already exist.
- Rejected outcome 3: Claim that FindRoot intentionally selects the outermost marker.

Acceptable alternative and its construction: The facts are fixed by the supplied source, so a different correct conclusion is not meaningful. An independently written trace using filepath.Rel, the rest stack and the upward loop, or the three reproductions, is an acceptable alternative evidence route.

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

- `bad-segments`: external patch `controls/paths-segments.json`; expected behavioral failures `TestCorpus/segments`.
- `bad-missing-tail`: external patch `controls/paths-tail.json`; expected behavioral failures `TestCorpus/missing-tail`.
- `bad-nearest-root`: external patch `controls/paths-root.json`; expected behavioral failures `TestCorpus/nearest-root`.

Grade scout conclusions only with the cited source or reproduction support.
Grade implementations by required behavior and scope, not reference-code shape.
Grade reviews by distinct supported blockers and verdict, with no unjustified
blocker; advisory suggestions neither fail nor repair a missed blocking defect.
Unkeyed real defects and disputes stay unresolved pending independent adjudication
and a versioned key correction. Record prior exposure and anonymization limits.
This case covers the selected component, not correctness of the whole historical
PR, complete Orch workflow, native readiness or population-level model quality.
