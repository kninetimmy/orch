# review-held-question-defective behavioral key v1

Controller-source only. Never export or mount this file for evaluated workers.
Author: Codex /root/implement_310, routed gpt-6.1-sol @ max, 2026-10-03.
Independent validator: pending fresh Orch review; see ../../preparation-report.md
and the stable issue #310 audit link. Author checks are not independent validation.
Historical upstream: https://github.com/kninetimmy/orch/pull/16.
Source, bytes, exact commands and control artifacts are pinned in manifest.json.
Historical merge status supports provenance only.

| Distinction | Required behavior or finding | Source/reproduction evidence | Severity if missed |
| --- | --- | --- | --- |
| 1 / tail | A valid schema-1 object followed by an unmatched } or ] is accepted because dec.More does not enforce EOF. | internal/question/decode.go: dec.More guard; TestCorpus/tail. Reproduce `TestCorpus/tail`. | major |
| 2 / fields | Unknown struct fields are correctly rejected; an allegation that they are ignored is unjustified. | internal/question/decode.go: DisallowUnknownFields. Reproduce `TestCorpus/fields`. | major |
| 3 / schema | Unsupported schema versions are correctly rejected; an allegation they are accepted is unjustified. | internal/question/decode.go: SchemaVersion guard. Reproduce `TestCorpus/schema`. | major |
| 4 / map | Omitted/null answers correctly become a non-nil map and existing entries survive. | internal/question/decode.go: map normalization. Reproduce `TestCorpus/map`. | major |

Known-good outcome: request-changes; one major blocker JSON-TAIL. An input such as `{"schema_version":1}}` is accepted, violating the API's all-trailing-data requirement. dec.More detects sequence/container availability, not EOF. Unknown-field, schema and map handling otherwise meet this scoped contract.

- Rejected outcome 1: approve with no findings: false approval and missed JSON-TAIL.
- Rejected outcome 2: request-changes only because a hypothetical unknown-field defect exists: misses the real blocker and invents another.
- Rejected outcome 3: Report JSON-TAIL without a concrete accepted input or source explanation, or add an unsupported blocker about nil-map normalization.

Acceptable alternative and its construction: A single deduplicated finding may use } or ] as the trigger, source analysis or reproduction, and suggest a second Decode requiring io.EOF or non-whitespace remaining-byte validation. Both repair controls pass the same tests. Blocking based only on an agent's claim is unsupported.

Reference code expected failures: `TestCorpus/tail`.
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

- `strict-repair`: external patch `controls/question-repair.json`; expected behavioral failures none.
- `alternative-repair`: external patch `controls/question-alternative.json`; expected behavioral failures none.
- `bad-fields`: external patch `controls/question-fields.json`; expected behavioral failures `TestCorpus/tail`, `TestCorpus/fields`.
- `bad-schema`: external patch `controls/question-schema.json`; expected behavioral failures `TestCorpus/tail`, `TestCorpus/schema`.
- `bad-map`: external patch `controls/question-map.json`; expected behavioral failures `TestCorpus/tail`, `TestCorpus/map`.

Grade scout conclusions only with the cited source or reproduction support.
Grade implementations by required behavior and scope, not reference-code shape.
Grade reviews by distinct supported blockers and verdict, with no unjustified
blocker; advisory suggestions neither fail nor repair a missed blocking defect.
Unkeyed real defects and disputes stay unresolved pending independent adjudication
and a versioned key correction. Record prior exposure and anonymization limits.
This case covers the selected component, not correctness of the whole historical
PR, complete Orch workflow, native readiness or population-level model quality.
