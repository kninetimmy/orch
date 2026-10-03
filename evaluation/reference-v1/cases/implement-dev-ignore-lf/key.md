# implement-dev-ignore-lf behavioral key v1

Controller-source only. Never export or mount this file for evaluated workers.
Author: Codex /root/implement_310, routed gpt-6.1-sol @ max, 2026-10-03.
Independent validator: pending fresh Orch review; see ../../preparation-report.md
and the stable issue #310 audit link. Author checks are not independent validation.
Historical upstream: https://github.com/kninetimmy/orch/pull/110.
Source, bytes, exact commands and control artifacts are pinned in manifest.json.
Historical merge status supports provenance only.

| Distinction | Required behavior or finding | Source/reproduction evidence | Severity if missed |
| --- | --- | --- | --- |
| 1 / ignore-lf | A .gitignore fixture written with CRLF is normalized and checked out as LF even with core.autocrlf=true. | .gitattributes and TestCorpus/ignore-lf local Git hash/index/checkout reproduction. Reproduce `TestCorpus/ignore-lf`. | major |
| 2 / preserved | Existing *.go, *.sh and *.md eol=lf policies remain effective. | .gitattributes and TestCorpus/preserved check-attr evidence. Reproduce `TestCorpus/preserved`. | major |

Known-good outcome: Add the literal .gitignore text eol=lf attribute rule; the controller fixture confirms checkout bytes and preserved existing rules.

- Rejected outcome 1: Omit the .gitignore rule, leaving core.autocrlf=true checkout as CRLF.
- Rejected outcome 2: Remove an existing LF rule while adding .gitignore's rule.

Acceptable alternative and its construction: A separately constructed anchored /.gitignore text eol=lf rule passes for this root-scoped task. Broader nested .gitignore coverage is not required by this packet. Both variants rely on Git, not a custom newline converter.

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

- `unfixed`: unfixed pinned predecessor; expected behavioral failures `TestCorpus/ignore-lf`.
- `bad-preserved`: external patch `controls/attributes-preserved.json`; expected behavioral failures `TestCorpus/preserved`.
- `alternative`: external patch `controls/attributes-alternative.json`; expected behavioral failures none.

Grade scout conclusions only with the cited source or reproduction support.
Grade implementations by required behavior and scope, not reference-code shape.
Grade reviews by distinct supported blockers and verdict, with no unjustified
blocker; advisory suggestions neither fail nor repair a missed blocking defect.
Unkeyed real defects and disputes stay unresolved pending independent adjudication
and a versioned key correction. Record prior exposure and anonymization limits.
This case covers the selected component, not correctness of the whole historical
PR, complete Orch workflow, native readiness or population-level model quality.
