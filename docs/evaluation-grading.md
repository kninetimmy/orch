# Evidence-backed retained evaluation grading (issue #318)

`orch eval grade` imports bounded evaluator assertions for an explicitly selected
retained attempt. It reads artifacts as data; it never runs a supplied command,
worker, probe, control, model or grader program. Native production turns remain
refused for **every role through both `RunSession` and `Session.Resume`**. An
asserted semantic pass is separate from execution eligibility and acceptance.
No profile adoption, live trial, measured baseline or Phase 1 completion follows.

Before issue #318, reports stated that all semantic grades, annotations, review
judgments, disputes and regrades were unknown. After this change, separate
immutable grading records can supply attributed judgments and coverage; absent,
disputed, unvalidated or invalidated grading remains unknown. The original
schema-1 evaluation, progress and attempt records still retain `grade: unknown`.
Their bytes, preview digests, approval bindings and resource observations do not
change when a grade is published.

## Controller-only definitions and preparation evidence

The twelve [grading-v1 definitions](../evaluation/grading-v1/) add versioned data
beside the frozen `reference-v1` corpus. Every definition pins the manifest byte
digest, case ID/version, full retained case digest, packet, key and probe digests,
and each control's name, canonical digest and expected behavioral failures.
Case/control digests are SHA-256 of Go `json.MarshalIndent(value, "", "  ")`
plus a final LF, the same representation used by retained `case.json`. Artifact
digests cover exact bytes. Version 1 copies the required key distinctions and
severities; implementation definitions also require behavior, regression and
scope judgments. Reference text and code shape are never scoring predicates.

| Case | Required distinction IDs | Keyed review blockers |
| --- | --- | --- |
| `scout-dev-paths` | segments, missing-tail, nearest-root | none |
| `scout-dev-scan` | hidden, root-pair, names | none |
| `scout-held-git-gates` | confirmation, checked-out, fast-forward | none; the first two conclusions are critical |
| `scout-held-question` | fields, schema, map, tail | none; supplied answers must survive |
| `implement-dev-ci-empty` | empty, malformed, states, scope | none |
| `implement-dev-ignore-lf` | ignore-lf, preserved, scope | none; root-anchored Git attributes are acceptable |
| `implement-held-capture` | unrelated, identified, unique, delta, scope | none; identified and unique are critical |
| `implement-held-effort-window` | window, labels, typed, scope | none; component extraction limits remain |
| `review-dev-risk-clean` | closed, order, copy | none |
| `review-dev-ci-defective` | empty, malformed, states | one major `CI-EMPTY` |
| `review-held-effort-clean` | allowed, rejected, roles | none; all six roles are covered |
| `review-held-question-defective` | tail, fields, schema, map | one major `JSON-TAIL`; brace/bracket triggers are one defect |

The author is `Codex/issue-318-author`, an approved authoring agent with access to
all keys, including held-out cases, the grading implementation and controller
controls. Training familiarity is unknown. The
[author evidence register](../evaluation/grading-v1/author-evidence.json) records
the checks actually run. Author mappings and deterministic scripted examples are
preparation evidence. They are **not** fresh independent semantic validation,
authenticated identities, evaluation model-trial results or a measured baseline.
The Architect arranges a fresh independent review after this implementation.

A fresh person/agent must inspect every pinned source/key/probe, every semantic
distinction, alternatives and classification, reproduce every exact control
command and expected behavioral outcome, and retain failures, exposure and
unresolved disputes. Agreement, keyword matching and a completion label are
insufficient. A validation submission records an assertion that those checks
were done; the CLI cannot authenticate the person or prove prose correctness.

## Exact public input

```sh
orch eval grade --run EVALUATION_ID --storage-root ROOT --unit ORDINAL \
  --attempt NUMBER --submission ./judgments.json --json
orch eval status --run EVALUATION_ID --storage-root ROOT --json
orch eval report --run EVALUATION_ID --storage-root ROOT --format text
orch eval report --run EVALUATION_ID --storage-root ROOT --format markdown
orch eval report --run EVALUATION_ID --storage-root ROOT --format json
```

Selectors are explicit positive decimal integers, with no signs or leading
zeroes. A missing/duplicate/unknown flag exits 2. Invalid data, identity conflicts,
unverifiable paths, failed publication and unsupported versions exit 1. Successful
publication or identical completed replay exits 0 and renders the same snapshot
as status. `report` retains a new immutable three-format bundle for that snapshot.
Grading confers no execution or Delivery authority and requires no host access.

The submission must be one UTF-8 JSON object, at most 64 KiB, with the complete
generated wire shape. Unknown/duplicate/non-lowercase fields, nulls, omitted
required fields, trailing documents and nesting beyond 32 levels fail. Optional
fields are omitted, never null. Artifact paths resolve relative to the submission
file, or are explicit local absolute paths. Every path uses the existing local
path/drive/reparse/hard-link/directory-identity protections.

| Field | Required meaning |
| --- | --- |
| `schema_version`, `id`, `operation` | Version `1`; unique case-style ID (1..96 lowercase letters/digits/hyphens); operation `grade`, `validate-rubric`, `dispute` or `invalidate-rubric`. An ID can only replay identical normalized submission bytes. |
| `evaluation_id`, `evaluation_sha256`, `plan_digest` | Exact retained evaluation ID/byte digest and existing `sha256:` plan digest. These never select a Delivery run. |
| `unit`, `attempt`, `attempt_sha256` | Complete frozen unit `{ordinal, case_id, case_version, repetition, side}` and completed attempt number/byte digest. They must match both CLI selectors and retained progress. Unrun or unfinished attempts cannot be submitted. |
| `case_sha256`, `packet_sha256` | Exact retained case and public packet identity. Case/rubric/attempt mismatches fail. |
| `attempt_artifacts` | Full sorted `{path, sha256}` array: `initial` plus `artifacts` from the guarded attempt record, `case.json`, and any `output`/`invalid_native` artifacts. Paths are relative to the attempt directory and sorted by path. Omitting, duplicating or altering any entry fails. Inspect guarded `attempt.json` to construct this array; do not replace it with output excerpts. |
| `rubric` | `{path, sha256}` for a controller-only `Rubric` definition, at most 2 MiB. The exact imported bytes are retained; all corpus/case/key/probe/control bindings and role-required requirements are checked. Version-1 classification must match the frozen key. A later independently checked version may correct the semantic classification while retaining its original frozen case/key bindings. |
| `previous_rubric_sha256` | Omit for initial/same-version records. A replacement requires the exact active rubric digest, a strictly higher version and a correction reason. Same-version conflicting bytes fail. |
| `evaluator` | `{identity, kind, relationship, independent_of, source_access, exposure}`. Kind is `human`/`agent`; relationship `author`, `evaluator` or `independent-validator`. All lists are explicit, unique, at most 32 bounded public identifiers. Access and exposure lists are nonempty; disclose unknown familiarity and feedback access. Identity/access/independence/exposure are attributed assertions, never authentication. |
| `reason` | Required nonempty private prose, at most 4096 bytes. State initial assessment, correction, dispute/adjudication or invalidation reasons. It remains guarded evidence. |
| `evidence` | Explicit array of at most 32 `{id, artifact:{path,sha256}, route, citation, reproduction?}` objects. Unique case-style IDs, routes `source`, `reproduction` or `scope`, and nonempty private citations of at most 4096 bytes. Every supplied artifact is digest checked and copied as data, at most 2 MiB each and 16 MiB for the entire bundle. |
| `reproduction` | Required only for that route: `{command, phase, passed, failed}`. Command is 1..32 bounded strings, retained but never executed. Phase is `setup`, `compiler` or `behavior`. Test IDs are explicit unique arrays, at most 128 each, disjoint. Behavioral evidence must name observed tests. Setup/compiler evidence cannot claim behavioral passes/failures. |
| `judgments` | For `grade`, exactly one `{id,state,evidence,reason}` for every rubric requirement. State is `satisfied`, `violated`, `unknown` or `disputed`. Evidence is a unique explicit array of evidence IDs. Each conclusive judgment needs supported permitted evidence and a private reason. Missing, duplicate, unknown or malformed judgments fail. Omit on other operations. |
| `review` | Required only on review grades: `{verdict,evidence,reason,findings}`. Verdict `approve`, `request-changes` or `unknown`. Findings are at most 64 `{id,defect_id,severity,state,evidence,reason}` objects with unique finding IDs. Severity `critical`, `major` or `advisory`; state `supported`, `unsupported`, `additional-real`, `disputed` or `unknown`. A keyed blocking finding must use its pinned severity; an unkeyed blocking finding cannot be marked supported before adjudication/version correction. |
| `validation` | Only `validate-rubric`: `{checks,resolves}`. Checks use the judgment shape and exactly name `key`, `probe`, `alternatives`, `classification`, `source:PATH` for every supplied input, `control:NAME` for every control, and `requirement:ID` for every requirement. A satisfied control check requires the exact pinned command and expected behavioral failures, including intentionally failing historical controls. Semantic checks also need source/reproduction evidence. Failed/unavailable checks remain failed/unknown. `resolves` explicitly lists dispute submission IDs, or is empty. |
| `supersedes`, `target` | A corrective/regrading `grade` must supersede the exact latest grade ID for that same attempt; first grades omit it. `dispute` requires `target` naming a retained grade for that attempt/current rubric. Other operations omit both. |

Rubric definitions require author provenance, nonempty acceptable alternatives,
unique requirements with explicit expectations/severities/permitted evidence
routes/check IDs, and distinct keyed blockers with requirement links. Scout and
review definitions require conclusions. Implementations require behavior,
regression and scope conditions; non-scope requirements accept only reproduction
evidence. All identities, command arguments and lists are bounded. Public
identifier syntax is the existing 1..128-character ASCII identifier contract.

Scout passes require every conclusion to be supported; unresolved conclusions
remain unknown. Implementation passes require every behavior, regression and
scope condition to be supported. Before review-cycle-1 repair, this document
stated: "A satisfied reproduction judgment needs observed passes and no failed
checks." That rule also rejected correct descriptive conclusions reproducing a
historical defect. After R1, satisfied scout/review conclusions accept behavioral
reproductions with passing or intentionally failing checks. Satisfied
implementation behavior/regression judgments still require observed passes and
no failed checks; a violated behavior/regression needs an
observed behavioral failure. Compiler/setup failures cannot stand in for that
failure. Different tests, algorithms, source explanations and acceptable solutions
are allowed: the grader checks evidence structure/bindings and the evaluator's
assertion, not text similarity or reference-code shape.

## Review derivation and disputes

Distinct keyed blockers are counted once by defect identity and severity, even
if several findings describe the same trigger. A blocking verdict alone catches
nothing. Approval of a keyed defective case is a false approval. Advisories never
count as catches or blockers. Missed blockers, unsupported blocking allegations,
false approvals and a clean case wrongly blocked are separate reported results.
Conflicting duplicate matches, unknown blocking findings, disputed matches and
alleged additional real defects yield unknown correctness; extra real defects
are never automatically labeled false positives.

Every grade is retrospective and retains its own attempt identity. Initial and
final permitted attempts are reported separately; a repair success never erases
the initial grade. A receipt timestamp compared with already-recorded repair
starts identifies grading after feedback. No assertion claims that an initial
grade existed before feedback. Refused, infrastructure/timeout/protocol/safety
invalid or unfinished evidence cannot become accepted execution. Scripted
preparation passes are explicitly labeled and never counted as accepted models.

A `dispute`, disputed conclusion/match, conflicting duplicate match or
`additional-real` finding keeps the affected case's grades unknown until a fresh
independent adjudicator validates a higher rubric version with explicit dispute
resolutions. The validator must assert independence from the author, disputer and
target grade evaluator as applicable; identities must differ. The entire required
validation must pass. Reasons and evidence for the correction are retained.
Invalidation cannot be undone by simply validating the same invalidated version.

Any replacement immediately makes earlier-version grades unknown. All affected
retained attempts on **both baseline and candidate sides**, including retries and
repairs, must receive a current-version regrade or an explicit unknown judgment
before rubric comparative readiness is reported. Prior records are kept. Native
execution, coverage, decision semantics and measurement eligibility remain
independently unestablished; a corrected rubric alone never creates a comparison
decision, denominator, zero human work or cost-per-acceptance.

## Guarded append-only storage and compatibility

```text
STORAGE_ROOT/
  EVALUATION_ID/                 # existing execution records, unchanged
  grades/EVALUATION_ID/
    record-000001.json           # receipt, identity, predecessor digest, file pins
    bundle-SUBMISSION_SHA256/
      submission.json           # normalized exact submission; private reasons
      rubric.json               # exact imported controller-only definition
      evidence-ID               # exact imported evidence bytes
      complete.json             # exact ordered file/digest manifest
    publication/                # exclusive bounded publisher claim
  reports/EVALUATION_ID/SNAPSHOT_SHA256/  # existing immutable report bundles
```

Receipts form a contiguous digest chain, at most 2048 records per evaluation.
This sibling grading namespace has its own existing 256 MiB/65536-entry inventory
ceiling and depth checks; grades and failed/pending imports never spend controller
or stop capacity. Publication reuses exclusive directory claims and atomic
non-replacing file publication. Readers validate every receipt, original identity,
rubric, completed evidence manifest and exact byte digest. They reject unsafe
paths, links, aliases, extra completed-bundle files and identity/digest conflicts.
Both new publication and inspection perform the same validation.

Concurrent writers may receive a bounded active-publisher refusal. A completed
identical submission replays its original receipt; a changed ID binding fails
without replacement. Interrupted claims, pending files or orphan bundles remain
inspectable and make grades unknown. No publisher takeover, sweeping or recovery
rewrite is provided. An earlier complete replay remains available after a later
interruption; a new publication refuses unresolved partial storage. The caller's
import files must still be independently readable/digest-correct on submission.
Status/reports use retained copies and do not need original import/source files.
Privileged filesystem mutation and worker-access denial are not proved by local
guarded storage or these assertions.

New reports/snapshots use schema 2 and add grade history, public assertion metadata,
evidence references, active rubric/dispute/regrade status, per-attempt grades and
initial/final coverage by role/partition/side. Every format uses the same snapshot.
Counts include scheduled, semantically eligible, invalid, unrun, unknown,
supported passes/failures and preparation examples. Bounds are
`passes / scheduled` through `(passes + unknown) / scheduled`; invalid evidence
cannot add possible successes. Zero scheduled opportunities are not applicable,
with absent bounds. Native model acceptance stays zero and cost-per-acceptance
undefined while its execution/coverage/measurement prerequisites are unavailable.
Existing safety findings and resource missingness remain visible.

The existing core `Status` still returns the unchanged execution progress contract;
public CLI status reads `Inspect`, which adds grading. Existing schema-1 reports
decode with absent optional grading fields, and their text/Markdown rendering
is preserved. Earlier report directories/digests/bytes are never modified; new
grading yields a different schema-2 snapshot/destination. Private keys,
control/solution bytes, worker output, citations, reproduction commands and
arbitrary evaluator prose remain in guarded evidence, not report summaries.

## Verification and blast radius

Author verification is deterministic and uses no evaluation model. Run the
repository's required commands plus this explicit focused grading gate:

```sh
go test -tags=corpus_validation -count=1 -run '^TestGrading' ./internal/evalplan
```

CI does **not** run that tagged grading gate. It must be run locally and repeated
by the fresh independent reviewer. The existing `corpus_validation` opt-in keeps
the expensive journal/filesystem scenarios out of the already near-limit default
package suite; no timeout or CI configuration is changed. Normal tests retain
bounded input, state, scoring, coverage, legacy rendering and public-process
checks. `TestEvalControllerCLIProcess` compiles the public CLI, supplies only
local Git on PATH, submits grades, checks replay/conflict preservation, status and
all report formats, and proves that conclusive assertions cannot promote refused
execution. The existing tagged corpus suite reconstructs pinned
maintainer controls as an **author reproduction**; a fresh reviewer must repeat
it and assess the semantic mappings. Neither run is a model baseline.

| Structure touched | Before, after, and whether prior behavior holds |
| --- | --- |
| Twelve `evaluation/grading-v1/CASE.json` files: schema/version/controller-only flag, corpus/case/packet/key/probe/control bindings, author provenance, requirements, blockers and alternatives | New controller data maps every frozen key distinction and allowed alternative, adding implementation scope requirements. No prior artifact is replaced. Versioned corrections retain original source/key bindings. All frozen `reference-v1` bytes, wording, controls, partitions and exposure evidence hold. |
| `evaluation/grading-v1/author-evidence.json` | New attributed author check/exposure/failure register. It cannot act as an independent validation assertion or model result; actual fresh review remains the Architect's next step. |
| `rubric.go`: `Evaluator`, `Requirement`, `KeyedDefect`, `ControlBinding`, `Rubric`; `boundedProse`, `uniqueNames`, `Evaluator.validate`, `validateRubric`, `validationChecks` | New bounded data/binding/provenance checks. Existing corpus/configuration contracts hold. Shared identity, path and evidence restrictions apply to every grading operation and definition, not one evaluator or case alone. A higher independently checked rubric can correct semantics without editing the original key. |
| `grade_input.go`: bounds, `Reproduction`, `GradeEvidence`, `Judgment`, `FindingJudgment`, `ReviewJudgment`, `RubricValidation`, `GradeSubmission`; `attemptArtifacts`, `evidenceMap`, `support`, `validateJudgments`, `validateReview`, `validateValidation`, `validateGradeInput` | New strict, complete attempt-artifact/evidence/operation validation. All supplied commands remain data. Behavior, regression and scope are independently required; known-bad control failures differ from setup/compiler failures. Existing strict JSON and artifact limits/path protections are reused and keep their behavior. |
| Review-cycle-1 `validateJudgments` evidence polarity and `TestGradingFailingProbesSupportDescriptiveConclusions` | Before R1, satisfied descriptive scout/review conclusions required a fully passing reproduction, rejecting the real `TestCorpus/tail` and `TestCorpus/empty` reference defects. After R1, all conclusion judgments share the existing observed-control outcome route; implementation behavior/regression pass requirements and every setup/compiler safeguard hold. Regression uses the three actual pinned cases without changing their definitions/corpus. The restriction is by requirement kind for every caller, not one case or named probe. |
| `grading.go`: `GradeReceipt`, `retainedGrade`, `gradeJournal`; naming/file helpers, `selectedAttempt`, `openGradeArea`, `readGradeJournal`, `readGradeBundle`, `SubmitGrade`, `replayGrade`, `publishGrade` | New immutable sibling journal and bounded exclusive publisher. Every publication, replay and inspection checks identity/digests and guarded storage. Controller progress, consumed attempts, stop capacity, approvals and Delivery/metrics associations hold unchanged. No execution callback, native option or recovery/takeover route is added. |
| `grading.go`: `rubricState`, `gradeState`, `gradeAttemptKey`, `disputedGrade`, `gradingState`, `judgmentResult` | New append-only correction/dispute/version state derivation. Earlier grade and reason evidence remains. Same-version conflicts fail; disputed/invalidated/replaced rubrics cannot retain passes. Both sides' affected attempts require regrading/explicit unknowns. |
| `grade_report.go`: `ReviewScore`, `PublicJudgment`, `GradeResult`, `GradeHistory`, `RubricSummary`, `UnitGrades`, `GradeCoverage`, `GradingSummary`; `reviewScore`, `semanticResult`, `assessmentEligibility`, reference helpers, `inspectGrading`, `gradeCoverage`, `gradingHeading` | New attributed role scores, defect/severity deduplication, retrospective initial/final projection and bounds. Verdict-only catches, advisory catches, automatic extra-defect false positives and acceptance from unknown/invalid/scripted evidence are excluded for every review/case/side. Zero opportunities remain not applicable; comparison/cost/native eligibility holds inconclusive/undefined. |
| `report.go`: `ReportAttempt`, `Snapshot`, `blockers`, `Inspect`, `RenderReport`, returned `Report` version | Before, all reports exposed unknown semantic grades. After, schema-2 inspection/rendering adds guarded grading while execution `grade: unknown` bytes remain unchanged. The same document above preserves that removed summary behavior. Optional absent grading preserves schema-1 JSON/rendering. Existing `terminalState`, `publicNative`, `measure`, counters/pairs/ranges, safety projection and retained bundle helpers keep their behavior. |
| `internal/cli/eval.go`: `runEval`, `evalUsage`, `runEvalController` and new decimal selector parsing | Before, `grade` was unsupported. After, one strict submission path selects a retained evaluation/unit/attempt and prints `Inspect`. Preview/run/status/stop/report arguments, approval and exit mapping hold. No backend/profile/default/configuration/permissions switch is added. |
| `internal/cli/cli.go`: `eval` command help entry | Before, the family description listed preview/run/status/stop/report. After, it also lists grade. Existing command dispatch, exit mapping and every other help entry hold. |
| `internal/evalplan/grading_test.go`: mapping/scoring, legacy rendering, bounded input, correction/dispute/version state and initial/final coverage checks | New deterministic normal-suite checks of the approved grading criteria using the same production validation/derivation helpers. Existing tests remain; no fixtures execute models or supplied commands. |
| `internal/evalplan/grading_validation_test.go`: `corpus_validation` tag, fixture/input/validation/submit/snapshot helpers, repair/regrade/report, dispute/adjudication, malformed/tampered, concurrency/interruption checks | New explicit local grading gate preserves all complete journal/path/digest/publication scenarios while avoiding a default-suite timeout. It is actually run and retained as author evidence; CI does not run it and fresh review must repeat it. Successful execution and asserted independence are synthetic preparation fixtures only; no production test seam or installed model requirement is added. |
| `internal/cli/eval_controller_test.go`: `TestEvalControllerCLIProcess`, `evalRefusedGrade`, synthetic rubric/evidence construction | Existing bounded public-process/refusal/report checks remain, extended with submission/replay/failure preservation, explicit selectors and earlier report preservation. Git-only PATH still prevents installed host/model access. |
| `README.md`, `docs/evaluation-contract.md`, `docs/evaluation-workflow.md`, this document | Existing unknown-grading statements are preserved as explicit before/after context in the documents that stated them, with the exact new contract linked here. Native refusal, trust/approval, protected-resource, missingness and Phase 1 restrictions hold. |

Unmodified shared structures: `plan.go`, `load.go`, `storage.go`, `guarded.go`,
platform path helpers, approval/controller/attempt source and cleanup logic,
`internal/evalcorpus`, native session/protocol code, configuration/routing,
metrics/Delivery state and association rules, adapter manifests, dependencies,
CI, installation and release machinery. Their existing restrictions still hold
for all consumers. In particular, `RunSession` is not the sole restricted
symbol: **both production turn entry points**, including `Session.Resume`, require
`IsolationPreflight` for **all task roles**. Metadata/command diagnostics remain
separate native APIs and confer no model-turn permission. The baseline unchecked
Windows `CloseHandle` lint warning is outside this issue and is unchanged.
