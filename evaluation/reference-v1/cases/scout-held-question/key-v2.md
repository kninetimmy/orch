# scout-held-question behavioral key v2

Controller-source only. Never export or mount this file for evaluated workers.
Author: Codex /root/implement_310, routed gpt-6.1-sol @ max, 2026-10-03.
Independent validator: pending fresh Orch review; see ../../preparation-report.md
and the stable issue #310 audit link. Author checks are not independent validation.
Historical upstream: https://github.com/kninetimmy/orch/pull/16.
Source, bytes, exact commands and control artifacts are pinned in manifest.json.
Revision 2 strengthens supplied-answer preservation and adds the independently
identified always-empty-map bad control. Revision 1 evidence remains retained;
its control success did not establish this required distinction.
Historical merge status supports provenance only.

| Distinction | Required behavior or finding | Source/reproduction evidence | Severity if missed |
| --- | --- | --- | --- |
| 1 / fields | Unknown struct fields are rejected by DisallowUnknownFields. | internal/question/decode.go: decoder setup. Reproduce `TestCorpus/fields`. | major |
| 2 / schema | Only schema_version 1 is accepted at this pinned history. | internal/question/question.go: SchemaVersion; decode.go: version guard. Reproduce `TestCorpus/schema`. | major |
| 3 / map | Omitted/null answers normalize to a non-nil empty map; supplied answers survive. | internal/question/decode.go: map normalization. Reproduce `TestCorpus/map`. | major |
| 4 / tail | The promise is incomplete: valid schema-1 JSON followed by } or ] is accepted; another object or x is rejected. | internal/question/decode.go: dec.More is not an EOF check; TestCorpus/tail reproduces accepted closers. Reproduce `TestCorpus/tail`. | major |

Known-good outcome: The decoder rejects unknown fields and non-1 schema values, and normalizes absent/null answers. Its all-trailing-data claim is too strong: dec.More treats a closing brace/bracket as an end boundary, so a valid object followed by an unmatched closer is accepted. The tail probe fails only that contract assertion while the other probes pass.

- Rejected outcome 1: Claim unknown fields are ignored.
- Rejected outcome 2: Claim any schema version is accepted.
- Rejected outcome 3: Claim omitted answers stay nil or supplied answers are discarded.
- Rejected outcome 4: Claim all trailing bytes are rejected merely because dec.More is called.

Acceptable alternative and its construction: A different factual answer is not meaningful. An independently written source trace or the concrete accepted-closing-delimiter reproduction is acceptable. Both EOF-decoding and remaining-byte validation repair controls corroborate the defect, but implementing a repair is outside the scout task.

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

- `bad-fields`: external patch `controls/question-fields.json`; expected behavioral failures `TestCorpus/tail`, `TestCorpus/fields`.
- `bad-schema`: external patch `controls/question-schema.json`; expected behavioral failures `TestCorpus/tail`, `TestCorpus/schema`.
- `bad-map`: external patch `controls/question-map.json`; expected behavioral failures `TestCorpus/tail`, `TestCorpus/map`.
- `bad-supplied-answer`: external patch `controls/question-empty-map.json`; expected behavioral failures `TestCorpus/tail`, `TestCorpus/map` even though the returned map is non-nil.
- `strict-repair`: external patch `controls/question-repair.json`; expected behavioral failures none.
- `alternative-repair`: external patch `controls/question-alternative.json`; expected behavioral failures none.

Grade scout conclusions only with the cited source or reproduction support.
Grade implementations by required behavior and scope, not reference-code shape.
Grade reviews by distinct supported blockers and verdict, with no unjustified
blocker; advisory suggestions neither fail nor repair a missed blocking defect.
Unkeyed real defects and disputes stay unresolved pending independent adjudication
and a versioned key correction. Record prior exposure and anonymization limits.
This case covers the selected component, not correctness of the whole historical
PR, complete Orch workflow, native readiness or population-level model quality.
