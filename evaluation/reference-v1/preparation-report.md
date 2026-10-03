# Reference-v1 preparation report

Status: **author preparation controls passed; fresh independent validation
pending after cycle-1 request-changes; evaluation execution blocked**. This is not a measured baseline, a
model trial, a protected runtime store or completion of Phase 1.

Author: Codex `/root/implement_310`, routed gpt-6.1-sol @ max, 2026-10-03,
Delivery `run-20261003T181029Z-9e5e94f5`. The approved scope is
[issue #310](https://github.com/kninetimmy/orch/issues/310). The [PR #311 engine review audit](https://github.com/kninetimmy/orch/pull/311)
retains the fresh cycle-1 reviewer identity and request-changes result. That
review reproduced the original controls but found a semantic false negative;
it did not validate preparation. Revised material remains pending fresh review.
The prior report is retained at
[head 12129e4](https://github.com/kninetimmy/orch/blob/12129e42b0dbcaeb7f283c166de3671defaab79c/evaluation/reference-v1/preparation-report.md).

## Frozen material and scope

[manifest.json](manifest.json) pins exactly twelve case versions, full starting
commits and upstream PR/commit provenance, every exported byte digest, exact
worker tasks/instructions, snapshot packet digests, external key/probe/patch
digests, control source commits, commands and expected failure IDs. Its SHA-256
for the final author run is
`2c3724026d5dc586c01c628bc103670eeb084a1abd173b28342c7372aa9a8ddd`.
[artifact-digests.json](artifact-digests.json) covers the versioned corpus,
including this report, registers and retained outputs; it excludes itself.

Worker inputs are only the declared historical files plus the exact
[TASK files](cases/), [role instructions](roles/), [context](public/context.md)
and [stdlib component module](public/go.mod.txt). The module is an authored
component harness, not a claim of a full historical checkout. No historical
tests, solution/future revisions, hidden probes, external keys, .git metadata
or pointers, or answer-bearing memory enter the worker packet. For implementation
cases the starting commit precedes the repaired reference code. Scout/review
cases intentionally receive the code they must inspect, including any keyed
historical defect. The source comments are real pinned permitted context.

| Case/version and exact task | Role / classification | Partition | Lineage | Controller-source evidence |
| --- | --- | --- | --- | --- |
| [scout-dev-paths v1](cases/scout-dev-paths/task.md) | scout | development | path-containment | [external key](cases/scout-dev-paths/key.md), [probe](controls/paths_test.go.txt) |
| [scout-dev-scan v1](cases/scout-dev-scan/task.md) | scout | development | managed-instruction-scan | [external key](cases/scout-dev-scan/key.md), [probe](controls/scan_test.go.txt) |
| [scout-held-git-gates v1](cases/scout-held-git-gates/task.md) | scout | held-out | git-mechanical-gates | [external key](cases/scout-held-git-gates/key.md), [probe](controls/git_test.go.txt) |
| [scout-held-question v2 / task v1](cases/scout-held-question/task.md) | scout | held-out | answer-wire | [external key](cases/scout-held-question/key-v2.md), [probe](controls/question_test-v2.go.txt) |
| [implement-dev-ci-empty v1](cases/implement-dev-ci-empty/task.md) | implementation | development | required-ci-empty | [external key](cases/implement-dev-ci-empty/key.md), [probe](controls/ci_test.go.txt) |
| [implement-dev-ignore-lf v1](cases/implement-dev-ignore-lf/task.md) | implementation | development | gitignore-lf | [external key](cases/implement-dev-ignore-lf/key.md), [probe](controls/attributes_test.go.txt) |
| [implement-held-capture v1](cases/implement-held-capture/task.md) | implementation | held-out | exact-child-rollout | [external key](cases/implement-held-capture/key.md), [probe](controls/capture_test.go.txt) |
| [implement-held-effort-window v1](cases/implement-held-effort-window/task.md) | implementation | held-out | effort-policy | [external key](cases/implement-held-effort-window/key.md), [probe](controls/effort_test.go.txt) |
| [review-dev-risk-clean v1](cases/review-dev-risk-clean/task.md) | review / clean | development | risk-domain-api | [external key](cases/review-dev-risk-clean/key.md), [probe](controls/risk_test.go.txt) |
| [review-dev-ci-defective v1](cases/review-dev-ci-defective/task.md) | review / defective | development | required-ci-empty | [external key](cases/review-dev-ci-defective/key.md), [probe](controls/ci_test.go.txt) |
| [review-held-effort-clean v1](cases/review-held-effort-clean/task.md) | review / clean | held-out | effort-policy | [external key](cases/review-held-effort-clean/key.md), [probe](controls/config_test.go.txt) |
| [review-held-question-defective v2 / task v1](cases/review-held-question-defective/task.md) | review / defective | held-out | answer-wire | [external key](cases/review-held-question-defective/key-v2.md), [probe](controls/question_test-v2.go.txt) |

All twelve are real historical Orch components, with complete tasks and keys;
they are not invented example IDs or unfinished placeholders. Historical merge
status is provenance only. The two defective review packets contain observed
major defects: empty required-check output breaks RequiredCI, and an unmatched
closing JSON delimiter bypasses DecodeAnswers' EOF promise. These are replay
material, not changes or new defects introduced into current product code.
Clean reviews cover only their declared predicates/APIs. Scouting is source or
reproduction supported; implementation controls execute pinned code behavior.
The cases do not validate whole historical PRs or the entire delivery workflow.

[selection-v2.json](selection-v2.json) freezes six development and six held-out cases,
two of each role per partition, with one clean and one defective review in each.
Implementation difficulty includes mechanical, ordinary and demanding work.
The CI, answer-wire and effort-policy variants stay together: twelve cases cover
nine answer lineages, not twelve independent clusters. Selection used the
availability of source-backed behavioral controls, not any model's performance.

[exposure-v2.json](exposure-v2.json) records the author's complete authoring access,
the coordinator's reported preparation access and the independent review record
requirements. Published history and the dispatch's known candidate exposure
are disclosed; training-unseen is never claimed. No tuning occurred here.
Authoring/validation access is distinct from tuning access. If held-out material
informs later tuning, mark the entire affected lineage exposed, retain the old
results/access log and replace it with newly independently checked cases before
future confirmation. This applies to every actor and held-out lineage.

## Reproduce preparation, not worker execution

Prerequisites: locally available pinned Git objects, Go 1.26+ and Git 2.53+.
Author host: Go 1.26.5 windows/amd64, Git 2.53.0.windows.1. The controls compile
only standard-library code; they need no module download, actual Codex sessions,
model service, authenticated GitHub operation or new dependency.

Run from the maintainer checkout:

```sh
go test -tags=corpus_validation -count=1 ./internal/evalcorpus
```

The tagged test creates new disposable directories under that checkout's ignored
`.orchestrator/worktrees/corpus-v1-*`. `worker-packets/CASE` contains only
allowlisted public bytes; `controller/CASE/CONTROL` separately reconstructs
pinned source and external probes/patches. Existing destinations are refused.
Source commits and file modes are checked, ancestor links/submodules rejected,
and bytes are digested before export. Missing source objects are errors;
nothing fetches history. Process calls are argument vectors, never shell text.
The test removes its own workspace by default. To retain exact output and
reconstructed artifacts for an authorized maintainer, set
`ORCH_CORPUS_RETAIN=1` before the same command; inspect the newly created
workspace's `preparation-results.json` and `control-output.json`.
The author retained outputs below, then removed only their own disposable trees.

Each external command is exactly:

```text
go test -json -count=1 -timeout=30s -run=^TestCorpus$ ./...
```

The recorded relative directory resolves under that attempt's newly created
workspace. The child has `GOWORK=off`, `GOTOOLCHAIN=local`, `GOPROXY=off`,
`GOSUMDB=off`, empty `GOFLAGS` and `CGO_ENABLED=0`. Limits are 60 seconds for
export, 120 for each control including compilation, 30 for its test process,
10 per historical Git read, 5 per fixture Git command, 2 for pipe cleanup, and
540 overall. One invocation per control; no retries or repairs within an
invocation. These preparation limits are not a model-trial budget.

The effort-window case explicitly scopes the shared choice component. Its
controller extracts the exact pinned AST declarations `hostEfforts`,
`maxOfferedEfforts`, `effortsOffered`, `effortOptions`, historical
`effortLabel` where present, and `validEffort`, with their used original
imports and real question types. Source files and projected bytes have separate
digests. This verifies those declarations, not init/configure/local interview
execution or unrelated role/default logic. Other Go probes compile the
declared component files intact. The line-ending case uses Git's actual
hash/index/checkout behavior in a separately created controller fixture.

## Author observations and independent evidence

[evidence/author-results-repair-1.json](evidence/author-results-repair-1.json)
records the revised final
command, prerequisites/environment, all twelve packet digests and all 60
controls with exact commands, limits, expected versus observed failures/passes,
controller artifact digests and raw-output digests.
[evidence/control-output-repair-1.json](evidence/control-output-repair-1.json)
retains the actual
Go JSON event output for every final control. Timestamps, fixture paths and
elapsed fields are ordinary preparation logs, not model measurements; their
raw byte digests will vary on reproduction. Input/output artifacts and normalized
behavioral pass/fail distinctions are the reproducible comparison points.

The revised tagged command passed in 84.727 seconds: twelve packets and **60/60
control outcomes matched**. Expected historical/bad-control failing subtests
remain failures in the raw output; the enclosing suite passes only when the
declared failures occur and all other behavioral subtests pass. Compiler failure,
missing prerequisites, no behavioral subtests or deadlines fail preparation
rather than pretending to catch a defect. The keys retain known-good outcomes,
known-bad outcomes for every required distinction, acceptable independently
constructed alternatives or a reason the fixed source has no different
meaningful outcome. Alternatives were constructed separately by the author;
that does not substitute for fresh independent-person/agent validation.

[evidence/author-history.json](evidence/author-history.json) preserves three
pre-freeze author preparation attempts and their distinct manifest digests.
Attempt 1 matched 50/57 controls and had seven preparation mismatches:
four path variants suffered short/long Windows alias assumptions; the
uniqueness mutant removed only one of two guards; the copy test conflated
ordering with slice ownership; the rejection test conflated domain membership
with six-role coverage. [Initial failing outputs](evidence/initial-failures.json)
are retained with raw digests. Corrections used independent native path
resolution, separate assertions and an actually defective uniqueness contrast.
Attempt 2 matched 57/57. Before freezing v1, an explicit wrong-parent/task
attribution contrast and an illustrative JSON key correction led to the final
58/58 pre-review attempt. These original attempts/results are unchanged.
There were no evaluated outputs or hidden unfavorable trial results to amend
or discard.

Cycle-1 independent review found that the supplied-answer assertion ran only
when the returned map was nonempty: an always-empty non-nil decoder passed.
[Repair history](evidence/repair-history.json) records the finding, review
exposure, original CI failure, invalidated control sufficiency and fourth author
preparation attempt. The two answer-wire cases and external keys/probe advance
to revision 2; the strengthened assertion requires the supplied q=yes entry,
and each case adds an effective always-empty-map bad control. Their v1 keys,
probe and original outputs remain retained, alongside the
[prior manifest](evidence/manifest-12129e4.json). Only the two affected case
versions change: the other ten cases, partition/lineage assignments, worker
tasks/instructions, historical source and all twelve packet digests are unchanged.
The existing manifest schema remains version 1; Load previously accepted only
case version 1 and now accepts positive case revision counters.

The macOS CI fixture failure at the prior head came from the trusted /var alias.
Ordinary test roots now use filepath.EvalSymlinks before export; intentional
source/destination links remain literal and rejected. Export itself is unchanged.
Local ordinary tests passed without skips. Actual repaired macOS CI must be
verified at the pushed head before completion; a Windows pass is not that proof.

Required repository checks run by the author: `go build ./...`,
`go test ./...`, `go vet ./...`, `gofmt -l .`,
`npm test --prefix adapters/opencode`, `git diff --check`, and the tagged
command above. The first npm test lacked the existing plugin dependency;
`npm ci --prefix adapters/opencode` using the existing lockfile restored the
prerequisite and the retry passed 6/6. A focused
`go test -v -run '^TestExportLinks$' ./internal/evalcorpus` passed without a
symlink-privilege skip. Formatter/whitespace checks are recorded over the final
tree after disposable reconstruction cleanup. No dependency or lockfile changed.

Fresh validation of the repaired head must independently inspect all twelve pinned source/key
pairs and reproduce the control suite; check the semantics of every good, bad
and alternative outcome, blocking severity, source/reproduction support, clean
classification and missed/unjustified-blocker examples; and record identity,
date, reviewed head/manifest digest, exact checks actually run, artifact digests,
prior familiarity and unavoidable author-report exposure. The retained issue/PR
engine review must supply those fields. Author conclusions or agent agreement
alone are not validation. A disagreement stays unresolved or goes to a fresh
independent adjudicator with identity/exposure/rationale recorded.

Preparation may be called independently validated **only if** that retained
fresh evidence is complete, every case/rubric/control is independently supported
and no dispute remains. This author report does not assert that condition is
already satisfied. No automatic semantic grader or evaluation runner exists.

## Boundary and unresolved limitations

This maintainer repository contains controller-source keys, checks and reports.
It is explicitly **not protected runtime storage**. None of this controller
source, reconstructed controller artifacts, shared Git objects, memory or
maintainer credentials may be mounted or made reachable by future evaluated
workers. The worker packet/export inspection establishes exact contents and
destination containment only. It proves neither OS isolation nor model-tool
closure. Protected runtime access enforcement remains unimplemented and blocks
execution readiness. Ordinary delivery worktrees and these author fixtures are
not evaluated-worker sessions or isolation proof.

Production `ErrIsolationUnavailable` is unchanged. Both production turn entry
points, `RunSession` and `Session.Resume`, route through
`IsolationPreflight` for every task role. This is not limited to one named
sentinel caller and does not say every native API must refuse: read-only metadata
and bounded command diagnostics retain their existing separate checks.
Deferred native model-tool, shell-containment and Claude Windows work remains
deferred. No evaluation model trial, `orch eval` CLI, measured baseline,
configuration/model-default change, product installation, release, permission
change, adoption or Phase 1 completion is authorized or delivered.

## Blast radius and prior behavior

| Structure touched | Before and after; prior behavior retained? |
| --- | --- |
| `docs/evaluation-contract.md` | Preparation formerly described an absent corpus and a validator as another person, and said "without model execution." Those prior statements remain explicitly historical in the same document. After this change, approved authoring agents and fresh validation agents are allowed; evaluation trials remain prohibited. Comparison, measurement, isolation, approval and Phase 1 rules still hold. |
| `docs/evaluation-workflow.md` | The absent-corpus and "without model calls" statements remain as before/after history in that guide. Maintainer preparation is now documented; every proposed `orch eval` command and protected runtime store remains unimplemented. Existing approval, metrics and adoption behavior still holds. |
| Active `evaluation/reference-v1/manifest.json`, retained `evidence/manifest-12129e4.json`, `public/`, `roles/` and all twelve `cases/*/task.md` | Active manifest advances only the two answer-wire case revisions; the prior manifest is retained. Every public input, task, instruction, source pin and packet digest is unchanged. No product role/configuration policy changes. Historical files are read from pinned objects, not rewritten. |
| All twelve retained `cases/*/key.md`, two active answer-wire `cases/*/key-v2.md`, ten component probe families including retained `controls/question_test.go.txt` and active `controls/question_test-v2.go.txt`, and contrast patches including `question-empty-map.json` | Prior key/probe evidence remains retained. The two v2 keys/probe require supplied-answer preservation and each adds the effective empty-map control. Other case controls are unchanged. Historical source behavior, including replayed defects, is preserved; no product package, runtime grader or worker access permission changes. |
| Retained `selection.json`/`exposure.json` and active `selection-v2.json`/`exposure-v2.json` | Counts, partitions and lineages remain frozen. V2 identifies the two revised cases and review/repair exposure. Held-out tuning/replacement restrictions still apply to every actor and lineage; public/prior exposure is disclosed. |
| `evidence/`, this report and `artifact-digests.json` | New retained preparation evidence and conditional independent-evidence links. No previous metrics schema, runtime registry or protected store is replaced; author failures and unknown independent validation remain visible. |
| `internal/evalcorpus/corpus.go` | New stdlib manifest/digest validation; no product caller. Manifest schema stays v1; `Load` previously allowed only case revision 1 and now accepts positive revisions, including the two v2 answer-wire cases. Partition/instruction/history declarations still hold. It does not judge natural-language answers or infer training exposure. |
| `internal/evalcorpus/export.go` | New maintainer `Export`/`Inspect` with rooted creation, explicit file bytes, path/type/digest and exact-content checks. Every created packet file uses `os.Root`. Official-case selection depends on the validated manifest; direct helpers accept trusted explicit file lists and do not detect semantic answers or enforce global permissions. All former product write/isolation behavior still holds. |
| `internal/evalcorpus/corpus_test.go` | New bounded manifest/export negative checks with local synthetic objects, requiring no historical Orch objects. Trusted fixture roots now resolve OS aliases; deliberate linked-path rejection remains unchanged. Optional local symlink setup is separately visible; it succeeded for the author. Existing repository tests are unchanged. |
| `internal/evalcorpus/validation_test.go` | New opt-in reconstruction/probe suite, exact failure matching and scoped effort extraction. It never calls evaluated models or production GitHub; it does not grade prose. Ordinary production CLI, adapters, native refusal, guard, configuration, schemas, dependencies and deferred work are unchanged. |

The source/key/task table above enumerates every case structure. Files outside
these corpus artifacts, the preparation package and the two guides are not
changed. Existing production behavior remains unchanged. The preparation-only
differences are the explicit case-version acceptance and repaired fixture/grader
checks above.
