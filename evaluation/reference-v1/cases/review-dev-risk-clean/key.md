# review-dev-risk-clean behavioral key v1

Controller-source only. Never export or mount this file for evaluated workers.
Author: Codex /root/implement_310, routed gpt-6.1-sol @ max, 2026-10-03.
Independent validator: pending fresh Orch review; see ../../preparation-report.md
and the stable issue #310 audit link. Author checks are not independent validation.
Historical upstream: https://github.com/kninetimmy/orch/pull/9.
Source, bytes, exact commands and control artifacts are pinned in manifest.json.
Historical merge status supports provenance only.

| Distinction | Required behavior or finding | Source/reproduction evidence | Severity if missed |
| --- | --- | --- | --- |
| 1 / closed | Every declared risk domain is valid; empty, unknown and wrong-case tokens are invalid. | internal/routing/risk.go: Valid switch. Reproduce `TestCorpus/closed`. | major |
| 2 / order | Domains returns the nine declared values in canonical order. | internal/routing/risk.go: Domains literal. Reproduce `TestCorpus/order`. | major |
| 3 / copy | Each Domains call creates independent slice backing storage. | internal/routing/risk.go: new slice literal per call. Reproduce `TestCorpus/copy`. | major |

Known-good outcome: approve; no blocking findings. The closed switch rejects unknown tokens, the literal has canonical order, and a mutation followed by a second call preserves the original values.

- Rejected outcome 1: request-changes: claim unknown or wrong-case domains are accepted.
- Rejected outcome 2: request-changes: claim the current source returns domains in arbitrary map order.
- Rejected outcome 3: request-changes: claim returned slices share mutable backing storage.

Acceptable alternative and its construction: A differently written approval using source analysis rather than reproduction is acceptable. A nonblocking suggestion to keep ordering tests is advisory, not a blocker. The fixed source has one correct verdict; a different implementation is not meaningful for this review packet.

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

- `contrast-unknown`: external patch `controls/risk-closed.json`; expected behavioral failures `TestCorpus/closed`.
- `contrast-order`: external patch `controls/risk-order.json`; expected behavioral failures `TestCorpus/order`.
- `contrast-shared`: external patch `controls/risk-copy.json`; expected behavioral failures `TestCorpus/copy`.

Grade scout conclusions only with the cited source or reproduction support.
Grade implementations by required behavior and scope, not reference-code shape.
Grade reviews by distinct supported blockers and verdict, with no unjustified
blocker; advisory suggestions neither fail nor repair a missed blocking defect.
Unkeyed real defects and disputes stay unresolved pending independent adjudication
and a versioned key correction. Record prior exposure and anonymization limits.
This case covers the selected component, not correctness of the whole historical
PR, complete Orch workflow, native readiness or population-level model quality.
