# review-held-effort-clean behavioral key v1

Controller-source only. Never export or mount this file for evaluated workers.
Author: Codex /root/implement_310, routed gpt-6.1-sol @ max, 2026-10-03.
Independent validator: pending fresh Orch review; see ../../preparation-report.md
and the stable issue #310 audit link. Author checks are not independent validation.
Historical upstream: https://github.com/kninetimmy/orch/pull/126.
Source, bytes, exact commands and control artifacts are pinned in manifest.json.
Historical merge status supports provenance only.

| Distinction | Required behavior or finding | Source/reproduction evidence | Severity if missed |
| --- | --- | --- | --- |
| 1 / allowed | Codex max/ultra and Claude xhigh/max are accepted, alongside ordinary host values. | internal/config/validate.go: effortsByHost and validateHost. Reproduce `TestCorpus/allowed`. | major |
| 2 / rejected | Claude ultra, Codex none/minimal and non-enum tokens are rejected for each role. | internal/config/validate.go: exact host map lookup. Reproduce `TestCorpus/rejected`. | major |
| 3 / roles | All six roles, including specialist and review_downgrade, pass through effort validation. | internal/config/validate.go: roles slice. Reproduce `TestCorpus/roles`. | major |

Known-good outcome: approve; the host-specific maps implement the declared enums, and validateHost checks all six role entries. Each required distinction is reproduced on the pinned predicates.

- Rejected outcome 1: request-changes: claim Codex ultra or Claude max is rejected.
- Rejected outcome 2: request-changes: claim Claude ultra or Codex none is accepted.
- Rejected outcome 3: request-changes: claim specialist or review_downgrade bypasses validation.

Acceptable alternative and its construction: An independent source-based approval with the same scoped guarantees is acceptable; a nonblocking suggestion for additional future model compatibility testing remains advisory. The verdict is fixed for the supplied change, so a different code implementation is not required.

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

- `contrast-allowed`: external patch `controls/config-allowed.json`; expected behavioral failures `TestCorpus/allowed`.
- `contrast-rejected`: external patch `controls/config-rejected.json`; expected behavioral failures `TestCorpus/rejected`.
- `contrast-roles`: external patch `controls/config-roles.json`; expected behavioral failures `TestCorpus/roles`.

Grade scout conclusions only with the cited source or reproduction support.
Grade implementations by required behavior and scope, not reference-code shape.
Grade reviews by distinct supported blockers and verdict, with no unjustified
blocker; advisory suggestions neither fail nor repair a missed blocking defect.
Unkeyed real defects and disputes stay unresolved pending independent adjudication
and a versioned key correction. Record prior exposure and anonymization limits.
This case covers the selected component, not correctness of the whole historical
PR, complete Orch workflow, native readiness or population-level model quality.
