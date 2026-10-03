# implement-held-effort-window behavioral key v1

Controller-source only. Never export or mount this file for evaluated workers.
Author: Codex /root/implement_310, routed gpt-6.1-sol @ max, 2026-10-03.
Independent validator: pending fresh Orch review; see ../../preparation-report.md
and the stable issue #310 audit link. Author checks are not independent validation.
Historical upstream: https://github.com/kninetimmy/orch/pull/210.
Source, bytes, exact commands and control artifacts are pinned in manifest.json.
Historical merge status supports provenance only.

| Distinction | Required behavior or finding | Source/reproduction evidence | Severity if missed |
| --- | --- | --- | --- |
| 1 / window | Codex max/ultra and Claude max are included in their contiguous four-value windows; low retains the leading window. | internal/interview/sequence.go: hostEfforts/effortsOffered/effortOptions. Reproduce `TestCorpus/window`. | major |
| 2 / labels | Every option label equals its submitted effort token; exactly the default is recommended. | internal/interview/sequence.go: effortOptions. Reproduce `TestCorpus/labels`. | major |
| 3 / typed | validEffort continues to accept the full host enum, including values omitted from the offered window, and reject invalid values/Claude ultra. | internal/interview/configurelocal.go: validEffort. Reproduce `TestCorpus/typed`. | major |

Known-good outcome: The historical helper finds the default index and moves the four-value window end past it; labels use literal tokens. Both caller families use effortOptions, while typed validation still uses hostEfforts.

- Rejected outcome 1: Keep a fixed first-four window, so max/ultra defaults are not selectable.
- Rejected outcome 2: Keep prose/title-case labels instead of submitted tokens.
- Rejected outcome 3: Validate free text against only the offered window.

Acceptable alternative and its construction: A separately constructed start-index algorithm uses max(0,index-4+1) and a bounded end. The controls compile exact AST declarations from the two full pinned context files and the real question types; declaration extraction and omitted whole-interview behavior are disclosed.

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

- `unfixed`: unfixed pinned predecessor; expected behavioral failures `TestCorpus/window`, `TestCorpus/labels`.
- `bad-labels`: external patch `controls/effort-labels.json`; expected behavioral failures `TestCorpus/labels`.
- `bad-typed`: external patch `controls/effort-typed.json`; expected behavioral failures `TestCorpus/typed`.
- `alternative`: external patch `controls/effort-alternative.json`; expected behavioral failures none.

Grade scout conclusions only with the cited source or reproduction support.
Grade implementations by required behavior and scope, not reference-code shape.
Grade reviews by distinct supported blockers and verdict, with no unjustified
blocker; advisory suggestions neither fail nor repair a missed blocking defect.
Unkeyed real defects and disputes stay unresolved pending independent adjudication
and a versioned key correction. Record prior exposure and anonymization limits.
This case covers the selected component, not correctness of the whole historical
PR, complete Orch workflow, native readiness or population-level model quality.
