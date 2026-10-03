# implement-held-capture behavioral key v1

Controller-source only. Never export or mount this file for evaluated workers.
Author: Codex /root/implement_310, routed gpt-6.1-sol @ max, 2026-10-03.
Independent validator: pending fresh Orch review; see ../../preparation-report.md
and the stable issue #310 audit link. Author checks are not independent validation.
Historical upstream: https://github.com/kninetimmy/orch/pull/148.
Source, bytes, exact commands and control artifacts are pinned in manifest.json.
Historical merge status supports provenance only.

| Distinction | Required behavior or finding | Source/reproduction evidence | Severity if missed |
| --- | --- | --- | --- |
| 1 / unrelated | Unidentified empty, malformed and unrelated-session files leave the exact child total=17 available. | internal/codexusage/capture.go: candidate identification before full validation; synthetic fixture. Reproduce `TestCorpus/unrelated`. | major |
| 2 / identified | Any invalid identified-child neighbor poisons an otherwise valid capture; setup requires the valid neighbor. | internal/codexusage/capture.go: candidate/valid distinction and identified state. Reproduce `TestCorpus/identified`. | critical |
| 3 / unique | Two exact child rollouts are unavailable; a different requested parent or task never receives the child's total. | internal/codexusage/capture.go: matches count and identity checks. Reproduce `TestCorpus/unique`. | critical |
| 4 / delta | Resume returns exact nonnegative deltas, including measured zero; negative or larger previous totals are unavailable. | internal/codexusage/capture.go: previousTotal guards. Reproduce `TestCorpus/delta`. | major |

Known-good outcome: Identify the target first, ignore only unidentified records, and keep every identified failure and ambiguous match unavailable. The reference is the selected historical fix, checked against code behavior rather than its merge.

- Rejected outcome 1: Allow an unrelated malformed/empty neighbor to zero exact attribution.
- Rejected outcome 2: Skip an invalid identified neighbor when a valid matching rollout exists.
- Rejected outcome 3: Return a total for duplicate matches or a nonmatching task.
- Rejected outcome 4: Accept an invalid baseline total or compute an incorrect resume delta.

Acceptable alternative and its construction: A separately constructed collection-based TotalTokens walks the same pinned parser, rejects any identified-invalid input, then accepts exactly one total and validates the baseline before subtraction. This changes the aggregation strategy while preserving the required behavior; it does not independently revalidate every parser branch.

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

- `unfixed`: unfixed pinned predecessor; expected behavioral failures `TestCorpus/unrelated`.
- `bad-identified`: external patch `controls/capture-identified.json`; expected behavioral failures `TestCorpus/identified`.
- `bad-unique`: external patch `controls/capture-unique.json`; expected behavioral failures `TestCorpus/unique`.
- `bad-attribution`: external patch `controls/capture-attribution.json`; expected behavioral failures `TestCorpus/unique`.
- `bad-delta`: external patch `controls/capture-delta.json`; expected behavioral failures `TestCorpus/delta`.
- `alternative`: external patch `controls/capture-alternative.json`; expected behavioral failures none.

Grade scout conclusions only with the cited source or reproduction support.
Grade implementations by required behavior and scope, not reference-code shape.
Grade reviews by distinct supported blockers and verdict, with no unjustified
blocker; advisory suggestions neither fail nor repair a missed blocking defect.
Unkeyed real defects and disputes stay unresolved pending independent adjudication
and a versioned key correction. Record prior exposure and anonymization limits.
This case covers the selected component, not correctness of the whole historical
PR, complete Orch workflow, native readiness or population-level model quality.
