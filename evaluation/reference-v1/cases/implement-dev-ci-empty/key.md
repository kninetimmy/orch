# implement-dev-ci-empty behavioral key v1

Controller-source only. Never export or mount this file for evaluated workers.
Author: Codex /root/implement_310, routed gpt-6.1-sol @ max, 2026-10-03.
Independent validator: pending fresh Orch review; see ../../preparation-report.md
and the stable issue #310 audit link. Author checks are not independent validation.
Historical upstream: https://github.com/kninetimmy/orch/pull/44.
Source, bytes, exact commands and control artifacts are pinned in manifest.json.
Historical merge status supports provenance only.

| Distinction | Required behavior or finding | Source/reproduction evidence | Severity if missed |
| --- | --- | --- | --- |
| 1 / empty | Empty, whitespace and [] responses produce no-checks, preserve Total=1 and have zero Required entries. | internal/ghops/checks.go: RequiredCI response decoding; injected gh runner. Reproduce `TestCorpus/empty`. | major |
| 2 / malformed | Nonempty malformed JSON and unexpected gh exit codes remain errors. | internal/ghops/checks.go: exit-code switch and JSON error branch. Reproduce `TestCorpus/malformed`. | major |
| 3 / states | No required checks is distinct from passing; pending/cancel/fail precedence is unchanged. | internal/ghops/checks.go: deriveCIState. Reproduce `TestCorpus/states`. | major |

Known-good outcome: The historical fix guards parsing only when trimmed stdout is nonempty; an empty decoded subset still flows through deriveCIState, retaining the rollup count.

- Rejected outcome 1: Leave the original unmarshal-on-empty behavior.
- Rejected outcome 2: Treat malformed nonempty JSON or exit 42 as no required checks.
- Rejected outcome 3: Convert pending to passing, or conflate no-checks with passing.

Acceptable alternative and its construction: The author separately constructed normalization of empty stdout to [] before decoding. It preserves the same required behavior and passes all probes. Construction is separate from the reference function body, not independent-person validation; the fresh reviewer must check it.

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

- `unfixed`: unfixed pinned predecessor; expected behavioral failures `TestCorpus/empty`.
- `bad-malformed`: external patch `controls/ci-malformed.json`; expected behavioral failures `TestCorpus/malformed`.
- `bad-states`: external patch `controls/ci-states.json`; expected behavioral failures `TestCorpus/states`.
- `alternative`: external patch `controls/ci-alternative.json`; expected behavioral failures none.

Grade scout conclusions only with the cited source or reproduction support.
Grade implementations by required behavior and scope, not reference-code shape.
Grade reviews by distinct supported blockers and verdict, with no unjustified
blocker; advisory suggestions neither fail nor repair a missed blocking defect.
Unkeyed real defects and disputes stay unresolved pending independent adjudication
and a versioned key correction. Record prior exposure and anonymization limits.
This case covers the selected component, not correctness of the whole historical
PR, complete Orch workflow, native readiness or population-level model quality.
