# review-dev-ci-defective behavioral key v1

Controller-source only. Never export or mount this file for evaluated workers.
Author: Codex /root/implement_310, routed gpt-6.1-sol @ max, 2026-10-03.
Independent validator: pending fresh Orch review; see ../../preparation-report.md
and the stable issue #310 audit link. Author checks are not independent validation.
Historical upstream: https://github.com/kninetimmy/orch/pull/6.
Source, bytes, exact commands and control artifacts are pinned in manifest.json.
Historical merge status supports provenance only.

| Distinction | Required behavior or finding | Source/reproduction evidence | Severity if missed |
| --- | --- | --- | --- |
| 1 / empty | RequiredCI wrongly unmarshals empty/whitespace stdout and returns an error instead of usable no-checks evidence. | internal/ghops/checks.go: unconditional JSON unmarshal; TestCorpus/empty. Reproduce `TestCorpus/empty`. | major |
| 2 / malformed | Nonempty malformed JSON and unexpected exit codes are correctly rejected; alleging their acceptance is unjustified. | internal/ghops/checks.go: exit and unmarshal error guards. Reproduce `TestCorpus/malformed`. | major |
| 3 / states | Existing no-checks/pending/failing/passing derivation is correct; allegations that pending passes are unjustified. | internal/ghops/checks.go: deriveCIState. Reproduce `TestCorpus/states`. | major |

Known-good outcome: request-changes; one major blocker CI-EMPTY: with a nonempty rollup and exit-0 empty/whitespace required-check output, unconditional json.Unmarshal returns a parse error, breaking the usable no-checks result. Cite the empty-output reproduction and checks.go. No extra unsupported blockers.

- Rejected outcome 1: approve with no findings: false approval and missed CI-EMPTY blocker.
- Rejected outcome 2: request-changes without CI-EMPTY trigger/evidence: not a catch merely because verdict blocks.
- Rejected outcome 3: Report CI-EMPTY plus an unsupported major allegation that pending buckets pass or malformed JSON is accepted.

Acceptable alternative and its construction: A report that describes the empty and whitespace triggers as one defect, with either the exact source or injected-runner evidence, is acceptable. A suggested guard or [] normalization can be advisory. The repaired historical and alternative controls establish that blocking CI-EMPTY is justified.

Reference code expected failures: `TestCorpus/empty`.
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

- `repair`: historical repair; expected behavioral failures none.
- `bad-malformed`: external patch `controls/ci-malformed.json`; expected behavioral failures `TestCorpus/malformed`.
- `bad-states`: external patch `controls/ci-states.json`; expected behavioral failures `TestCorpus/states`.
- `alternative-repair`: external patch `controls/ci-alternative.json`; expected behavioral failures none.

Grade scout conclusions only with the cited source or reproduction support.
Grade implementations by required behavior and scope, not reference-code shape.
Grade reviews by distinct supported blockers and verdict, with no unjustified
blocker; advisory suggestions neither fail nor repair a missed blocking defect.
Unkeyed real defects and disputes stay unresolved pending independent adjudication
and a versioned key correction. Record prior exposure and anonymization limits.
This case covers the selected component, not correctness of the whole historical
PR, complete Orch workflow, native readiness or population-level model quality.
