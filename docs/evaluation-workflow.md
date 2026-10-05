# P1-C evaluation preview and proposed execution workflow

**Status: preview, bounded controller, explicit approval, public CLI, retained
reports and evidence-backed grading implemented; verified worker-access/model-tool
protection, live trials
and a measured baseline remain pending. Phase 1 is open.**
Before issue #316 this guide stated: "execution CLI, reports and verified
worker-access protection remain pending." After that increment the CLI and
reports exist under the [run 2 contract](#delivered-cli-approval-and-reports-issue-316-run-2-of-2);
the access/model boundary remains unverified and production attempts refuse.
Before issue #314, this guide stated: "local preview and maintainer preparation
implemented; runner and protected storage unimplemented." After that increment,
the internal controller prepares fresh packets and retains bounded progress and
evidence, while every production attempt still refuses model execution. Guarded
placement and file identity checks do not establish a protected model/OS boundary.
Before issue #312, every `orch eval` command,
plan format, storage location and output here was proposed and unimplemented;
`orch help` did not list this family. Now help exposes
`orch eval preview --plan FILE [--json]`. It validates and retains public local
preparation metadata with a frozen bounded schedule and explicit blockers. It
never starts evaluation work or grants approval. Before issue #316, "Every other
evaluation verb still fails explicitly." After that increment `run`, `status`,
`stop` and `report` use the strict explicit selectors below. Existing `orch metrics` reporting and `orch metrics
record` observations are available under the [metrics contract](metric-observations.md);
they do not supply evaluation grades or a comparison runner. Before issue #310
this guide stated: "There is no prepared reference corpus, validated grader or
measured P1-C baseline delivered here." After that increment,
[reference-v1](../evaluation/reference-v1/preparation-report.md) contains twelve
scoped historical cases and deterministic preparation controls. Independent
semantic validation is separately retained; no validated grader, protected
runtime store or measured P1-C baseline is claimed by author checks.
Before issue #318, semantic grades and disputes remained unknown and `grade`
was unsupported. After that increment, the
[exact grading input, storage, trust limits and blast radius](evaluation-grading.md)
define `orch eval grade --run ID --storage-root ROOT --unit N --attempt N
--submission FILE [--json]`. It imports evaluator assertions and data, preserves
execution bytes and prior reports, and exposes initial/final coverage. The
fresh independent reviewer must check mappings and reproduce controls; author
agreement and scripted preparation are not validation or trial performance.

This v1 experience is for maintainers and advanced users screening Orch changes
against a versioned **Orch reference corpus**. It does not establish tuning
benefits for a user's own repository. Project-specific corpus authoring and
tuning need a separately scoped extension. The [evaluation contract](evaluation-contract.md)
governs cases, grading, matching, measurements and decisions; the
[native contract](codex-native-protocol.md) governs execution eligibility.
Existing engine approval, routing, canonical roles and permissions remain
authoritative. This guide grants no trial approval or alternate execution path.

## Maintainer preparation available now

Ordinary approved Orch agents may author corpus artifacts and independently
validate them without launching evaluation model trials. A fresh person or agent
other than the author checks pinned source and reproduces control evidence,
checks the semantic rubric and records identity and unavoidable prior exposure.
Agreement between agents alone is insufficient. Disputes stay unresolved or go
to an independent adjudicator. This applies to preparation of scout,
implementation and review cases alike. The earlier "without model calls"
wording is replaced by this specific prohibition on evaluation model trials;
it does not prohibit approved authoring/review agents.

Read the preparation report and frozen manifest/exposure register, ensure its
historical Git objects and documented Go/Git prerequisites exist locally, then
run the existing test entry point:

```sh
go test -tags=corpus_validation -count=1 ./internal/evalcorpus
```

This reconstructs worker packets from only declared bytes in new disposable
directories, separately reconstructs controller code/probes, and verifies
known-good/bad/alternative control results under finite limits. It makes no model
calls or production GitHub writes and is not an `orch eval` command. Ordinary
package tests check manifest and export rejection without requiring historical
Orch objects. Follow the report to retain exact outputs and link fresh independent
review evidence; passing author controls alone cannot declare preparation
validated. Source/controller material in the repository must never be reachable
by evaluated workers. Export inspection is not OS or model-tool isolation;
protected runtime access enforcement remains unimplemented and blocks execution.

## Choose the question and scope

| Run | Purpose and coverage |
| --- | --- |
| Baseline only | Establish observations for one pinned reference configuration: twelve cases, three repetitions each, 36 units. No candidate or improvement verdict is required. Incomplete coverage must remain visible. |
| Smaller screen | Declare four cases separately: scout, implementation, defective review and clean review. Freeze partitions, repetitions and baseline-only or matched scope. This can justify further validation; it does not complete the twelve-case baseline. |
| Matched comparison | Run the pinned baseline and candidate contemporaneously on the same twelve cases and three repetitions: 36 units per side. An earlier baseline is historical context, never a replacement for these matched baseline units. |

An evaluation unit is one case, repetition and configuration, including allowed
retries/repairs. Repeats do not add independent case coverage. Freeze the six
development/six held-out partition and equivalence groups before tuning. Use
development evidence to choose changes; held-out material must not guide tuning.
Record exposure and replace exposed held-out cases for future confirmation as
the evaluation contract requires.

Select baseline and candidate by full Orch commit IDs and digested profile
artifacts, including every requested role model/effort or variant and effective
configuration revision. Declare **one intervention**, such as one instruction
change or one requested profile change. Keep snapshots, inputs, checks, tools,
host/toolchain, permissions and finite limits equal otherwise. Inseparable
changes support only a claim about their combined effect. Profiles are inputs
for engine validation/routing, never a worker's authority to choose a model.

## One CLI for terminal and agent use

The agent invokes the same family and presents its output; it does not
create a separate execution or approval policy.

| Operation | Reads, saves or executes |
| --- | --- |
| `orch eval preview --plan FILE [--json]` — implemented | Reads bounded local inputs, validates manifest structure and pinned artifact bytes, reads effective configuration, displays scope/limits/schedule and saves one immutable normalized plan plus preview evidence. The explicit external storage root is a maintainer preparation area with worker-access protection **unverified**. Starts no model work, changes no Delivery state and grants no approval. |
| `orch eval run --plan sha256:DIGEST --storage-root ROOT [--approval FILE] [--json]` | Returns frozen scope; loads and revalidates that exact plan. Requires the evaluation-specific single-use human assertion below. Retains the schedule, approval, native refusal and reports; no production model turn is available. |
| `orch eval status --run ID --storage-root ROOT [--json]` | Read-only retained snapshot with limits, consumed attempts/repairs, schedule, coverage, blockers and cleanup unknowns. Source artifacts and live hosts are unnecessary. |
| `orch eval stop --run ID --storage-root ROOT [--json]` | Durably requests a stop only for that evaluation. Request receipt and observed controller acknowledgement remain distinct. Bounded controller interruption/cleanup prevents later units. |
| `orch eval report --run ID --storage-root ROOT --format text\|markdown\|json` | Validates retained evidence and publishes all three immutable formats for that snapshot, returning the requested format. Starts no work and can explicitly retain incomplete snapshots. |

Before issue #316 these four verbs were proposed with no storage-root selector
or implemented approval/report wire contract. After that increment every selector
is explicit; no registry lookup, guessed path or implicit approval is supported.

### Implemented version 1 preview format

Before issue #312 the screen example used illustrative, unimplemented field
names and invented case IDs. The following is now the exact version 1 JSON
shape, with four delivered development-case IDs. Uppercase artifact paths,
digests and revisions are placeholders: replace them with existing local paths,
64 lowercase SHA-256 hex digits and full 40-character lowercase commit OIDs.
The numeric example is illustrative preparation, **not an approved trial
budget**. Run preview from the initialized Git checkout's root; artifact paths
are relative to the plan file, while storage/worker/scratch roots must be absolute.

```json
{
  "version": 1,
  "scope": "screen",
  "intervention": "orch-revision",
  "corpus": {"path": "/ABS/ORCH/evaluation/reference-v1/manifest.json", "sha256": "MANIFEST_SHA256"},
  "cases": ["scout-dev-paths", "implement-dev-ci-empty", "review-dev-ci-defective", "review-dev-risk-clean"],
  "partitions": ["development"],
  "baseline": {
    "orch_revision": "BASELINE_FULL_OID",
    "profile": {"path": "baseline-profile.toml", "sha256": "BASELINE_PROFILE_SHA256"}
  },
  "candidate": {
    "orch_revision": "CANDIDATE_FULL_OID",
    "profile": {"path": "candidate-profile.toml", "sha256": "CANDIDATE_PROFILE_SHA256"}
  },
  "repetitions": 1,
  "limits": {
    "overall_seconds": 3600,
    "attempt_seconds": 300,
    "verification_seconds": 120,
    "cleanup_seconds": 30,
    "max_attempts_per_unit": 1,
    "max_repairs_per_unit": 0
  },
  "measurement": {"source": "codex-app-server", "scope": "task-agents"},
  "decision_rule": {"path": "decision-rule.txt", "sha256": "DECISION_RULE_SHA256"},
  "readiness": {
    "independent_validation": {"path": "independent-validation.txt", "sha256": "VALIDATION_SHA256"},
    "exposure": {"path": "exposure.json", "sha256": "EXPOSURE_SHA256"},
    "native_execution": {"path": "native-readiness.txt", "sha256": "NATIVE_SHA256"}
  },
  "storage_root": "/ABS/LOCAL/PREVIEW",
  "worker_roots": ["/ABS/WORKERS"],
  "scratch_roots": ["/ABS/SCRATCH"]
}
```

On Windows use absolute drive paths, escaping backslashes in JSON, or forward
slashes such as `C:/Local/Preview`. UNC/device paths are unsupported. The
storage root must already exist. It must neither contain nor lie within any
checkout reported by local Git, the verified current checkout, the actual Git
directory/common directory, or any declared worker/scratch location. Declare
`worker_roots` with 1..32 entries and `scratch_roots` explicitly with 0..32;
future worker/scratch directories may be absent, but their existing ancestors
must be verifiable. Links, Windows reparse points/junctions, file hard links,
traversal, reserved devices, alternate streams, short-name and drive aliases and
unverifiable paths fail closed. Resolve trusted OS aliases before choosing paths.

Before the review-cycle-1 repair, a Windows `SUBST` mapping could present an
excluded worker directory under a different drive letter, pass lexical placement
comparisons and receive a saved preview record. After the repair, shared
directory validation compares the handle-resolved Windows drive-root path with
the declared DOS root and rejects aliases or unavailable identity evidence.
This covers storage roots, every artifact parent, repository/Git locations and
worker/scratch exclusions, including absent descendants and aliases on either
side of an exclusion. Standard local drives retain their behavior. Unix path
rules and other existing `internal/paths` consumers are unchanged; this added
placement check does not verify worker-access protection.

Version 1 rejects unknown or duplicate JSON fields, non-lowercase field names,
nulls, trailing documents, malformed UTF-8 and nesting beyond 32 levels. Optional
fields must be omitted rather than null. The plan is at most 64 KiB; each supplied
artifact is at most 2 MiB. Local paths are at most 4096 characters. No supplied
command is executed and missing artifacts/commit objects are never fetched.
The only subprocesses are fixed read-only Git metadata/commit queries, with
lazy fetching and replace objects disabled. No host CLI or GitHub is called.

| Field | Exact meaning and validation |
| --- | --- |
| `version` | Integer `1`. All unsupported versions fail. |
| `scope`, `cases`, `partitions` | `baseline-only` requires all twelve manifest cases and no candidate; `matched` requires all twelve and a candidate. `screen` requires exactly four cases: one scout, implementation, defective review and clean review, with optional candidate. Cases must be unique known IDs; partitions must exactly name their coverage using `development` and/or `held-out`. The existing `evalcorpus.Load` reference-v1 rules validate manifest coverage, lineage, versions and declared packet digests. |
| `intervention` | `none` without a candidate; otherwise `orch-revision`, `requested-profile` or `combined`. This public classification does not prove that differences are limited to the declared intervention. |
| `corpus`, `decision_rule` | Required `{path, sha256}` references. The manifest is structurally validated. The decision rule is opaque: only its bytes/digest are checked, not tolerances, endpoint definitions or stop/invalidity semantics. |
| `baseline`, `candidate` | Each has a required full local commit OID and `{path, sha256}` profile. Profiles are complete Orch configuration TOML snapshots, parsed with existing unknown-key/default/host-profile rules; include all six requested roles for each enabled host. No profile is installed or granted authority. Omit `candidate` for baseline only. |
| `repetitions` | Integer 1..1000. Three on all twelve cases matches the proposed initial 36-unit baseline schedule; declared repetitions never imply observed coverage. |
| `limits` | All six fields are required. Four duration bounds are positive integer seconds representable as Go durations. `overall_seconds` covers at least one attempt + verification + cleanup. `max_attempts_per_unit` is initial attempt plus retries, at least 1; `max_repairs_per_unit` explicitly allows additional repair attempts, at least 0. Checked arithmetic rejects overflow. |
| `measurement` | Required public `source` identifier and `scope` of `task-agents` or `whole-orch`. Source compatibility/coverage and observed identity remain unverified, not promised by the identifier. |
| `readiness` | Optional object containing only the three optional artifact references shown. Supplied files must exist and match their digests. Contents are neither interpreted nor copied. Caller readiness/approval booleans are unknown fields and fail. Missing documents remain blockers; present documents do not establish readiness. |
| Public identifiers | Profile model/effort/variant, configuration revision and measurement source use 1..128 characters: initial ASCII letter/digit, then ASCII letters/digits or `._:/@+-`. Case IDs/lineage use at most 96 lowercase letters/digits/hyphens, beginning with a letter/digit. These are public metadata, never fields for credentials or private prose. |

Each unit is case/version, repetition and side. Preview sorts case IDs, walks
repetitions in increasing order and alternates baseline-first/candidate-first
by matched pair ordinal across case boundaries. It reports excluded cases,
baseline/candidate units, pairs, maximum retries, repairs and attempts including
repairs. Maximum attempts are
`units * (max_attempts_per_unit + max_repairs_per_unit)`; maximum scheduled
seconds multiply those attempts by attempt + verification + cleanup bounds.
The overall cutoff can be smaller than that ceiling. Before issue #314, retaining
unrun slots after cutoff was a future-runner requirement; the core now retains
them and consumed budgets, including interrupted preparation. Preview still
invents no attempts, successes, grades or resource observations. This example
schedules eight units, eight maximum
attempts and a 3600-second ceiling, with no retries or repairs.

The prior hypothetical decision rule remains an example for future execution:
no increase in initial failures, missed blockers, false approvals, unjustified
blockers, human work or reliability failures; at least 10% lower comparable
task-agent tokens per final accepted outcome; no case more than 10% worse;
critical misses and safety violations reject unconditionally. This is a bounded
screening rule, not statistical confidence or a recommended production tolerance.
Freeze required measurement coverage and unknowns with it; missing comparable
tokens makes its cost claim inconclusive. Controller usage
and unmeasured active time remain unknown. Preview pins the rule's artifact
without interpreting or approving that rule.

Preview explicitly reports source-byte/export checks, independent semantic
validation/control reproduction, exposure review, decision-rule interpretation,
measurement compatibility, native containment and protected worker access as
unperformed or unavailable. A readiness document claiming success cannot
change these statuses. The frozen preparation report remains unchanged; this
preview does not reinterpret its author evidence or separately retained review.

### Immutable local preparation records

Successful preview returns exit 0 **even with execution blockers**. Argument and
unsupported-verb mistakes return 2; invalid inputs, unverifiable paths or corrupt
storage return 1. Text and `--json` render the same normalized plan, schedule,
counts, limits, digests, effective configuration identity, exclusions, unknowns,
canonical destination and blockers. Text includes both structured sections
verbatim as readable JSON. The schema 1 saved record contains `kind`,
`plan_digest`, `storage_destination`, `plan` and `preview`; its kind is
`maintainer-preparation-record`. Execution/approval are always false, and
`worker_access_protection` is always `unverified`.

The digest is `sha256:` plus the full SHA-256 of compact JSON for the normalized
`plan`: sorted cases/partitions/path arrays, canonical local paths, pinned
profiles/artifacts and effective configuration after the existing local overlay
rules. The full configuration SHA-256 covers canonical `config.Render` bytes;
the declared revision and applied overlay keys remain separate metadata.
Reordered cases/JSON fields or whitespace produce the same normalized digest;
changed paths, pinned bytes or effective configuration produce a different one.
The storage destination is exactly `STORAGE_ROOT/HEX_DIGEST.json`, containing the
normalized plan and preview evidence together. It is not a Delivery plan/run ID,
metrics association, execution registry or approval token.

Writes use `os.Root` with checked directory identities. A new exclusive
`.pending-*` file is fully written, synced and closed, then atomically published
using a non-replacing hard link; filesystems without that operation fail closed.
The pending link is removed before successful verification. Repeated/concurrent
submissions compare the entire completed record byte for byte, preserving
existing records. Conflicting, corrupt, partial or persistently aliased records
fail without replacement. Interrupted pending files are not complete records
and are preserved for maintainer inspection; preview does not sweep unrelated
files. No folder mode, filename, digest or caller assertion proves worker
containment, and privileged filesystem mutation is not prevented by this API.

Retained content is typed public provenance (case/version/role/partition/lineage,
source commit and packet digest), public requested profiles, configuration
identity, schedule/limits and artifact references/digests. It never copies task
text, grading keys, hidden probes, solution/control bytes, profile comments,
credentials or private readiness/decision documents. External roots provide
local metadata placement only; a protected runtime store needs separately
reviewed enforcement and worker denial evidence.

After independently preparing and validating those inputs, the hypothetical
terminal sequence is:

```sh
# IMPLEMENTED: save version 1 inputs with real pinned paths/digests.
orch eval preview --plan ./eval-plan.json
orch eval preview --plan ./eval-plan.json --json
# Inspect coverage, readiness, budgets and rules; use the returned plan digest.
# REMAINING COMMANDS ARE PROPOSED AND UNIMPLEMENTED; they fail explicitly today.
orch eval run --plan PLAN_DIGEST
# Explicit approval of that exact plan is requested before any model work.
# Use the evaluation ID returned by run, not a Delivery run ID.
orch eval status --run EVALUATION_ID
# Optional, while active:
orch eval stop --run EVALUATION_ID
# After completion or stopping, read/save results from retained evidence:
orch eval report --run EVALUATION_ID --format text
orch eval report --run EVALUATION_ID --format markdown
orch eval report --run EVALUATION_ID --format json
```

The equivalent agent-guided request is:

> Preview eval-plan.json for the four development-case matched screen, with the
> pinned baseline/candidate and the single reviewer-instruction intervention.
> Show readiness, coverage, finite budgets and decision rules. Start no model
> work until I approve the exact plan through the applicable gates. If approved
> and eligible, run it, show progress, honor my stop request and retain local
> text, Markdown and JSON reports. Bring any adoption decision back separately.

Before issue #312 an agent had to explain that the entire CLI workflow was
unimplemented. Now it can preview and retain preparation metadata, then report
the remaining readiness/execution gaps. It cannot substitute manual model
execution to fulfill the proposed run/status/stop/report workflow.

### Preview change blast radius

| Element touched | Before / after and retained behavior |
| --- | --- |
| `internal/cli/cli.go`: command table, `Run`/help consumers | Before, `eval` was unknown and absent from help. After, its table entry dispatches preview and advertises its syntax. Existing commands, exit-code mapping and adapter plumbing keep their behavior. |
| `internal/cli/eval.go`: `runEval` | New preview-only parser. Every unsupported evaluation verb, not merely `run`, fails explicitly. Duplicate/unknown flags and missing plan paths fail before reads or writes. No stdin policy, approval, model invocation or lifecycle mutation is added. |
| `internal/evalplan/plan.go`: proposal/normalized/public-record types, `Preview`, strict decoder, configuration/profile pins, counts/schedule, `WriteText` | New bounded local preparation path. Reuses corpus/config rules and projects typed public metadata; no evaluator, observations, automatic verdict or private artifact copying. Text/JSON share the same facts. Readiness is never authority. |
| `internal/evalplan/storage.go`: local reads, effective overlay identity, fixed Git queries, root exclusions, `save`/replay | New metadata retention outside current/listed checkouts, actual Git/common directories and declared worker/scratch roots. Every artifact reference and destination uses checked local paths; this is not a restriction on one file alone. Existing records are never replaced; pending writes are not complete. Existing Delivery serialization/state/lock/association behavior is untouched. |
| `internal/evalplan/path_windows.go`, `path_unix.go`, `path_other.go` | New Windows native drive-root identity/reparse/link-count and Linux/macOS file-link checks; other platforms fail closed. The drive check is Windows-only; Unix behavior is preserved. These checks apply through shared directory validation to every preview path kind and on both sides of exclusions, not to only one named artifact. They do not establish worker-access protection or change existing `internal/paths` consumers. |
| `internal/cli/eval_test.go`, `eval_windows_test.go`, `internal/evalplan/storage_test.go` | New deterministic synthetic-artifact, real CLI process, schedule, strict-input/limit, path/Git exclusion, Windows junction/SUBST, immutable/concurrent and interrupted-record checks. The SUBST regression reproduced six accepted aliases before repair and now requires refusal with no saved/pending file. Existing tests and CI matrix remain; no normal test invokes evaluation models. |
| `README.md` | Help and feature status now describe implemented preview; the previous whole-family unimplemented behavior is retained as before/after context in the same section. Other CLI/install/Delivery behavior holds. |
| `docs/evaluation-workflow.md` | The former illustrative preview syntax is retained as historical context and replaced by the exact implemented version 1 format. Preview's prior proposed verified controller store is distinguished from the implemented unverified public preparation store. Proposed execution, approval, status, stop, report and adoption behavior remains proposed. |
| `docs/evaluation-contract.md` | Only the whole-family implementation-status statement changes, with its old behavior preserved as before/after. Corpus, grading, matching, measures, decisions, isolation and approval requirements still hold. |

Unmodified boundaries: frozen `evaluation/reference-v1` artifacts and
`internal/evalcorpus` preparation/export rules; `internal/config` loading,
overlay restrictions, routing and canonical roles; native protocols and
`IsolationPreflight`; Delivery approval/recovery/merge rules; metrics recording
and disabled-metrics behavior; dependencies, adapter manifests, CI, releases and
installation. In particular, the native restriction is shared by **both**
production turn entry points, `RunSession` and `Session.Resume`, for **every**
task role through `IsolationPreflight`. It is not confined to one caller of
`ErrIsolationUnavailable` and is not a prohibition on all native APIs: metadata
and command diagnostics retain their separate checks. Preview adds no route
around this refusal, no live model/tool trial and no measured baseline.

## What the pre-run preview must show

| Visible item | Required content before approval |
| --- | --- |
| Scope | Purpose, declared intervention, both revisions/profiles (or baseline only), artifact/plan digests, case IDs/versions, roles, partitions, exposure/equivalence groups, repetitions and scheduled unit counts per side. Show excluded cases and screen/full-baseline distinction. |
| Corpus/grader readiness | Independent validation, known-good/bad/alternative control results, exact public verification prerequisites, protected key/check boundary, unresolved grades/disputes and the checks actually performed. |
| Native execution readiness | Exact host/version/profile capability and model-tool containment evidence, fresh snapshot/session boundaries, protected-resource and credential denial, tool closure and applicable engine eligibility. Report this separately from corpus readiness. |
| Finite limits | Overall comparison cutoff, per-attempt execution and verification deadlines, interrupt/shutdown/cleanup deadlines, maximum attempts/retries/repairs and feedback allowance. Show the maximum scheduled work including failures and repairs; zero retries/repairs is explicit. No unset or unlimited limit. |
| Measurement | Endpoint denominators and required coverage by unit/attempt/actor/counter; chosen compatible capture source, requested versus observed identity limits, measured time categories, known missingness and claims those gaps prevent. |
| Results and decisions | Controller-only storage/report destination, retained evidence/access policy, quality/safety/human-effort/reliability tolerances, efficiency target, bounded rule or preselected uncertainty method, stopping/invalidity rules and separate adoption gate. |

Preview starts no model work. `run` must refuse missing readiness or limits and
show the failed check, evidence and concrete prerequisite to resolve it. For
example, “native execution unavailable: model-tool containment unverified;
complete the separately approved native validation and reviewed refusal change.”
Today's production `ErrIsolationUnavailable` remains binding; synthetic command
success or configuration flags do not establish model-tool containment.
Both production turn entry points, `RunSession` and `Session.Resume`, require
`IsolationPreflight` for every task role. This is broader than one sentinel's
caller and narrower than all native APIs: metadata and command diagnostics do
not become model execution permission.

Approval binds the exact frozen scope, digests, effective profiles/routing,
budgets, measurement and decision rules. Revalidate before execution; changes
require a fresh preview and explicit approval, rather than widening the old plan.
Evaluation approval never substitutes for engine plan/configuration/merge gates
or authorizes a native trial whose prerequisites have not passed.

## Progress, stopping and incomplete work

Visible progress identifies plan/run, case/version, partition, repetition,
baseline/candidate, current attempt and phase: preparing, executing, verifying,
grading or cleaning up. Show elapsed/remaining cutoffs, attempts/repairs used,
scheduled/completed/failed/interrupted/invalid/unrun counts, available measurement
coverage, last retained evidence and actionable blockers. Native completion and
graded correctness appear separately; neither is inferred from the other.

Use a fresh disposable snapshot/session per unit. Match by case/version/repeat,
run pairs in frozen case-ID/repetition order and alternate which side runs first.
Save the actual order. Retain the initial artifact/grade before any permitted
repair; final acceptance after assistance never replaces initial performance.

Run outcomes are completed, stopped by user, cut off, refused or failed. Retain
unit outcomes distinguishing task failure, infrastructure failure, timeout,
interruption and disconnect, with protocol invalidity and unknown grade/cleanup
recorded separately. Completion means the schedule ended, not that every task
passed. A baseline-only report records observations; a comparison still needs a
separate improved/regressed/inconclusive verdict.

At an attempt deadline, end that attempt and retain its outcome/resources.
Continue the remaining schedule only within the approved limits. User stop,
safety violation, native refusal or overall cutoff stops new units and requests
bounded interruption and cleanup. Stop never erases failures or scheduled slots;
unstarted slots remain explicitly unrun. Do not stop because interim averages
look favorable. Safety violations reject an attributable candidate; other
incomplete comparisons remain inconclusive.

Cleanup is limited to controller-owned disposable resources under the declared
deadline; retain outputs, dirty work and evidence. Report interruption/shutdown
acknowledgement, descendant checks, protected-resource checks, unfinished cleanup
and unknowns. A cleanup failure becomes an actionable blocker, never an assumed
success or permission to delete unrelated resources.

Before issue #316, "`orch eval stop` would stop an evaluation." It now durably
requests that stop with the explicit selectors above. Existing `orch abort` returns a
Delivery run to Assist, and `orch resume` reconciles Delivery artifacts; neither
is evaluation stop/recovery. Saved reports do not promise durable evaluation
resume. Native session checkpoints are currently in memory only. After loss,
stop or cutoff, further execution needs revalidation and the applicable explicit
approval of a new frozen plan with finite repeat/replacement rules. Preserve and
publish both result sets; never silently replay or replace unfavorable units.

## Retained results and deliberate adoption

This section describes the **future protected runtime store and results**.
Before issue #312 preview was proposed to display a verified access boundary
and save into that controller area. The implemented preview instead displays
an explicit external preparation root and **unverified** worker-access
protection. Before issue #314, this section stated: "It retains public metadata
only; the protected runtime store, execution evidence and result reports
described below remain unimplemented." Preview still retains public metadata
only. The core now retains separately rooted controller bytes and execution
outcomes. Before issue #316, "verified worker-access enforcement and result
reports remain pending." Reports now exist under the run 2 contract below;
verified worker-access enforcement remains pending.

Propose a controller-owned local artifact root **outside every worker-readable
checkout, scratch area and shared Git store**. Preview must display its canonical
location and verified access boundary. Keep plans, progress, evaluator annotations,
protected keys/checks, exposure logs and retained evidence there. Workers receive
only permitted input packets; a hidden filename in their checkout is insufficient.
Reports may link protected evidence for authorized maintainers, but must not copy
hidden grading material into worker-visible output. Log held-out access.

At termination, including failure/stop, save `report.txt`, `report.md` and
`report.json` in the approved destination with stable evidence IDs/paths and
digests. The terminal-readable summary and Markdown explanation share the JSON
facts. Each contains:

- Frozen scope/provenance, revisions, corpus/rubric versions, requested/observed
  profiles and identity limits, execution order, budgets and readiness evidence.
- Full scheduled units/attempts including failures, interrupted, invalid and
  unrun work; initial/final grades, review defects/verdicts, checks, interventions
  and measured human work, terminal/cleanup evidence, disputes/regrades/exclusions.
- Usage/time coverage and missingness by endpoint, unit, attempt, actor and
  counter; raw metric links, compatible-source calculations, all consumed failure/
  retry/repair/check resources and paired per-case values/repeat ranges.
- Predeclared rule, endpoint validity, limitations, decision rationale and
  reproduction steps. Unknowns remain unknown; partial totals are not complete
  cost, zero accepted outcomes makes cost per acceptance undefined, and missing
  controller usage prevents a whole-Orch cost claim.

This proposed artifact store is not an existing evaluation registry or a new
metrics association API. `metrics record` still requires the current Delivery
run and its existing lock/association/version rules. Save and submit eligible
unchanged observations before that run terminates; disabled metrics stay disabled
and late submissions stay rejected. The future runner must resolve valid
association and retention through the existing boundary before claiming coverage.

Interpret **regressed** as a substantiated candidate safety violation, newly
missed critical defect or demonstrated tolerance failure. **Improved** needs
valid evidence meeting all gates and the declared target in the stated scope.
**Inconclusive** covers incomplete/invalid evidence, disputed grades or a target
not established. Keep separate quality, safety, human-effort, efficiency and
reliability results; a usable grade can survive an invalid cost endpoint.
A screen improvement supports further validation only.

No result automatically applies a profile, edits configuration or merges.
Adoption is a separate explicit decision: use existing `orch configure` and its
Delivery/merge approval for shared configuration, or `orch configure-local` for
authorized machine-local changes, then `orch render-agents` as required. Source
changes follow the existing approved Delivery workflow and separate merge gate.

## Implemented controller core (issue #314, run 1 of 2)

The internal `evalplan` APIs in issue #314 were `Load`, `Prepare`, `Run`, `Status`
and `Stop`. In that increment they added no execution CLI. Issue #316 adds the
CLI and approval/report helpers below; `Run` now requires its retained assertion.
`Load(ctx, repo, storageRoot, digest)` reads the existing
schema-1 preview without rewriting it. It checks the complete generated wire
shape, normalized plan digest, regenerated schedule/counts, current effective
configuration and profile/artifact bytes, local commits and current repository,
Git, worker and scratch exclusions. Version-1 config role fields keep their
existing `Architect`/`Model`/`Effort` JSON names and digest semantics; arbitrary
field casing, omissions, duplicates, nulls and unknown fields are rejected.
Readiness/decision references remain opaque and confer no authorization.

`Prepare` also requires existing, disjoint worker and scratch parents and verifies
the selected corpus's public and controller source bytes. Historical blobs use
fixed, bounded Git reads with lazy fetching and replacement objects disabled.
Authored bytes use the same anchored, bounded reader as preview. No source code
or supplied command is executed. Credential locations and known credential
filenames cannot become public packet sources. Frozen reference-v1 bytes remain
unchanged. A preview with absent scratch parents remains a valid preview, but
cannot prepare a controller evaluation until the separate parents exist.

### Exact retained schema 1

Each preparation creates `STORAGE_ROOT/eval-BASE32/`, with a random 26-character
lowercase base32 suffix. The evaluation is distinct from its immutable plan and
from Delivery run/issue/metrics identifiers. Generated JSON uses two-space
indentation and a final newline; hashes below cover those exact retained bytes.

| Relative name | Complete meaning |
| --- | --- |
| `evaluation.json` | `schema_version: 1`, `id`, canonical `repository`, controller `prepared_at` receipt and the exact schema-1 `preparation` record (plan, limits, frozen schedule, counts and preview blockers). No approval or verified isolation claim. |
| `progress-NNNNNN.json` | A full snapshot: schema, evaluation ID/hash, plan digest, sequence, previous snapshot hash (omitted for sequence 0), state, receipt timestamp, optional reason and every scheduled slot. Slots retain the original unit identity, status, `grade: unknown`, separate attempts/repairs consumed and ordered attempt number/kind/final-evidence hashes. A missing final hash is incomplete execution/cleanup, never success. Initial sequence 0 is `prepared`. |
| `execution/owner.json` | An exclusive, permanent execution claim and its schema, evaluation/plan identity and start receipt. The directory is created exclusively before running progress. An empty or interrupted claim remains inspectable and prevents concurrent execution, takeover and replay. It is never a Delivery lock. |
| `stop/request.json` | One idempotent schema-1 `stop` request bound to evaluation/plan identity. An exclusive stop directory has one writer; concurrent callers read its complete publication. An interrupted/invalid stop claim fails closed and is preserved. Stop has reserved byte/entry capacity. |
| `unit-NNNNNN-attempt-NNNNNN/begun.json` | Attempt identity, case/version, repetition, side, number, initial/retry/repair kind, case/packet digests, packet/scratch names and preparation receipt. It records started preparation without inventing execution or cleanup evidence. |
| `unit-NNNNNN-attempt-NNNNNN/case.json` | Exact controller-side corpus case declaration, digest-linked from the final attempt. It includes controller references and never enters the worker packet. |
| `initial/`, `private/key/`, `private/probe/`, `private/control-NNN/files/` and optional `private/control-NNN/patch/` inside an attempt | Original public bytes and separately rooted key/probe/control bytes. Each retained file's relative name and SHA-256 appear in the attempt's `initial` list. Initial bytes/outcomes survive every fresh retry/repair. |
| `worker-output/`, `scratch-output/`, optional `output.txt` inside an attempt | Bounded retained worker/scratch files and available textual output, treated solely as data. File names/digests are in `artifacts` and optional `output`. No worker code or hidden probe is run with controller privileges. |
| Optional `invalid-native.json` inside an attempt | Exact bounded malformed native payload, linked by `invalid_native` path/digest. It is retained as data with an `invalid-evidence` outcome and unknown grade, never decoded as usable native observations. If it cannot fit the existing artifact/storage limits, the attempt stays explicitly incomplete. |
| `unit-NNNNNN-attempt-NNNNNN/attempt.json` | Final schema/identity, frozen unit, number/kind, case/packet hashes, packet/scratch names, controller receipt timestamps, outcome/detail, execution source, unknown grade, artifact-only verification, initial/output links, optional native/eligibility evidence and explicit cleanup observations. Its byte hash is linked from progress. |
| `.pending-*` anywhere in the controller area | Unpublished/interrupted evidence. Never a complete record, never swept or overwritten. |

Worker and scratch directories are fresh exclusive children of the first declared
parents, named `EVALUATION_ID-unit-NNNNNN-attempt-NNNNNN`. Worker packets contain
only declared public inputs, with no `.git` pointer, inherited repository history,
controller keys/probes/solutions or copied credentials. All source checkouts,
shared Git locations, corpus/controller artifacts, other declared parents,
sibling entries and credential locations are included in the native protected
layout. Every production attempt invokes `IsolationPreflight` and remains
`refused`, including if a future diagnostic returns success: no scope approval
integration or model-turn path is supplied here. The existing `RunSession` and
`Session.Resume` restrictions remain binding for every role.

The only successful executor is compiled in `_test.go`. Its evidence is labeled
`no-model-test-script`; production evidence is `native-eligibility-only`.
Scripted `native-completed` outcomes simulate that execution observation and
prove no native model/OS isolation, observed inference profile or task acceptance.
No exported callback, backend selector, environment flag or alternative native
execution route exists. Native session IDs/profiles/observations, when available,
retain their reported meanings and absent fields; no totals, grades, human work,
active-agent time or native cleanup acknowledgements are inferred. The core never
calls the current-Delivery metrics recorder with evaluation or fabricated IDs.

States are `prepared`, `running`, `completed`, `stopped`, `overall-cutoff`,
`refused`, `safety-failure` and `incomplete`. Slot outcomes distinguish
`native-completed`, `task-failure`, `infrastructure-failure`, `timeout`,
`interrupted`, `disconnected`, `refused`, `protocol-invalid`, `invalid-evidence`
and `safety-failure`; untouched slots remain `unrun`. Completed means the schedule
ended, not graded correctness. Before review-cycle-1 repair, value/hash checks
could accept `completed` with every slot unrun, and the controller could publish
native observations rejected by its own reader. After repair, publication and
inspection share schedule/state consistency checks: completion requires closed
attempt evidence for every slot, preparation/refusal require their corresponding
slot states, and units cannot advance past unrun, unfinished or stopping work.
Native observation semantics and version-specific wire decoding are checked
before progression using the same validator as inspection. Invalid payloads
stop progression and remain digest-linked as quarantined data, so the retained
invalid outcome is inspectable without claiming usable observations.
Infrastructure failures/timeouts may consume
remaining retry attempts; task failures may consume separate repair allowances.
Disconnect/interruption never launches durable resume or replays an unidentified
turn. Stop, refusal, safety failure and overall cutoff prevent new units.

Attempt execution, verification and cleanup have finite frozen deadlines.
Interruption/shutdown and final local cleanup share one cleanup allowance;
cancellation does not grant it twice. Overall cutoff/caller cancellation during
preparation retains the consumed attempt and incomplete artifact observations.
An unavailable worker return, verification or cleanup observation is incomplete,
not automatically a safety violation. Local cleanup removes only an unchanged
controller-created packet and empty scratch, using anchored, nonrecursive removes.
Dirty, aliased, oversized, incomplete or unacknowledged work remains inspectable.
Local removal is recorded separately from unavailable native acknowledgement.

The controller caps the frozen maximum at 1,024 attempts, each artifact/output at
2 MiB, each record at 16 MiB, each selected case at 256 source files / 16 MiB,
all selected source bytes at 64 MiB, each worker/scratch inspection at 256 entries
/ 16 MiB / 16 directory levels, and the evaluation at 65,536 entries / 256 MiB /
32 directory levels. It permits at most 2,051 progress records. These are core
capacity limits, not new preview syntax or configuration defaults. Every write
charges a shared monotonic byte/entry budget; failed/pending writes do not refund
capacity. Stop has a reserved quota. Complete snapshots have bounded quadratic
metadata cost; controller writes avoid repeatedly scanning old evidence. Status
checks the entire bounded inventory, snapshot chain and all referenced bytes.
Caps fail visibly and preserve partial/dirty work rather than silently truncating
evidence or declaring success. Filesystem modes and placement remain local
hygiene, not verified worker denial or protection against privileged mutation.

Runnable no-model verification (ordinary tests use synthetic local Git/corpus
artifacts; tagged controls still require the documented historical objects):

```sh
go test -count=1 ./internal/evalplan
go test -tags=corpus_validation -count=1 ./internal/evalcorpus
go build ./...
go test ./...
go vet ./...
gofmt -l .
npm test --prefix adapters/opencode
git diff --check
```

Tests cover baseline/matched order, artifact separation, separate retries/repairs,
stop/cutoff/refusal, concurrent start/stop/readers, tampering/aliases, interrupted
publication, unknown grades/observations and bounded evidence/cleanup. Windows
tests construct disposable SUBST and junction aliases. These checks perform no
evaluation model trial and do not validate the deferred native boundary.
Heavy controller scenarios execute in the existing test binary as exact named
subprocesses with finite deadlines and propagated output/exit/assertions. This
avoids Go 1.26's repeated outside-module filesystem testlog/cache replay; it
changes no production execution seam, test assertions, toolchain or CI command.

### Controller change blast radius

| Structural element | Before / after and preserved behavior |
| --- | --- |
| `evalplan.Preview`, normalized `Plan`/`Record`, digest and text/JSON preview | Previously preparation-only; still the same schema/digests/output and immutable replay. Normalization is shared with saved-record validation. Preview still grants no approval and starts no host/model work. |
| `evalplan.Load` and strict stored decoding | No saved loader existed. Now the exact generated v1 schema and current inputs/exclusions are rechecked without rewriting. Existing capitalized config role/profile fields remain compatible; proposal JSON keeps its existing lowercase-only rules. |
| `evalplan` path, drive/reparse/link checks, `readRoot`, `openDirectory`, storage publication and new `guardedDir` | Original alias/traversal/identity/no-replace restrictions still hold for every preparation/controller read/write and child directory. New retained handles also reject directory replacement. Previously failed own pending writes could be removed; now failed pending files are preserved for inspection. Complete concurrent immutable replay still holds. |
| `evalplan.gitRead` production runner | Existing fixed read-only Git operations, no lazy fetch/replace objects and finite command context remain. Production stdout/stderr are now bounded during capture; injected preview test runners retain their API. No runtime worker injection is added. |
| `evalplan.Prepare`, `Run`, `Status`, `Stop`, evaluation/progress/attempt/cleanup records | No core existed. Now bounded preparation, one-time scheduling, journal validation, durable stop and explicit unknowns exist under separate external roots. Model execution still refuses; no CLI, takeover, replay or native-session recovery is added. |
| Review-cycle-1 `validateProgressState`, `validateNative`, publication/inspection and optional `invalid_native` artifact | Before repair, logically impossible completion could pass and native observation validation was reader-only. Both paths now share those checks; invalid native bytes are retained separately and never labeled usable. Existing valid schema-1 records remain readable; the new digest link is optional. All production refusal, bounds, schedule/attempt identity, unknown-grade, no-replay and local-cleanup restrictions still hold. |
| `evalcorpus.ReadHistoricalFile`, `gitRead`, `historicalFile` | Existing declared-file/tree/link/submodule/digest restrictions still hold. A single-file reusable reader now serves runtime preparation; historical stdout/stderr are bounded and lazy fetching is disabled. Existing local `Export`/`Inspect` preparation behavior remains; their weaker local hygiene is not used as runtime protection. |
| Controller no-model and Windows tests | New real-core checks; scripted success exists only in test files. Existing preview, corpus and native tests retain their contracts. |
| `docs/evaluation-workflow.md`, `docs/evaluation-contract.md` | Previous unimplemented core/evidence claims are retained as before/after context and the new schema/refusal/limitations are explicit. Proposed public CLI/report/approval and live validation/baseline remain pending. |

Unchanged: frozen corpus/reference-v1 versions/bytes; config loading/overlays,
routing/defaults and canonical roles; both native model-turn APIs, isolation
diagnostics and their separate restrictions; Delivery locks/state/lifecycle and
merge approval/recovery; metrics association/recording; dependencies, adapters,
CI, releases and installation. `RunSession` is not the sole restricted symbol:
**both** `RunSession` and `Session.Resume` require the native model gate for
**every** role. Metadata/command diagnostics retain their separate eligibility.
The new controller always refuses and supplies no bypass for either entry point.

## Delivered CLI, approval and reports (issue #316, run 2 of 2)

The public syntax is the table above and `orch eval help`. Flags are separate
arguments; duplicate/unknown flags, extra positional values, missing selectors
and unsupported formats exit **2**. Operational, validation, approval, refusal,
capacity and publication failures exit **1**. Successful preview, status, stop
receipt or report publication exits **0**, including a clearly incomplete
snapshot. An approved production run exits **1** after retaining its refusal.
No flag selects a fake backend, worker callback, model bypass or automatic resume.

### Exact approval assertion

First invoke `run` without `--approval`. It revalidates the saved plan, returns
the exact `ApprovalScope` and exits 1 without creating an evaluation. Review its
entire frozen plan: selected/excluded cases and partitions, repetitions, pinned
revisions/profiles/effective configuration, finite budgets, opaque measurement,
decision/readiness references, storage exclusions and report destination template.
Preview's original schema-1 evidence wording stays frozen for digest/replay
compatibility; current blockers in snapshots describe the delivered runner and
the still unavailable access/model boundary.

After a human explicitly approves that finite evaluation, the terminal or agent
caller supplies a local UTF-8 JSON assertion file. The engine cannot authenticate
the human; this follows the existing digest-bound approval convention and never
manufactures approval from a digest, readiness file or Delivery decision:

```json
{
  "schema_version": 1,
  "plan_digest": "sha256:<64 lowercase hex digits from the saved preview>",
  "approved_by": "human-identifier",
  "approved_at": "<actual RFC3339 timestamp of the human decision>",
  "statement": "approve-evaluation"
}
```

Every field is required. Duplicate keys, nulls, unknown/noncanonical fields,
overlong/nonpublic identifiers, wrong statement/digest, malformed timestamps,
future approval and approval older than **24 hours** fail closed. Assertion files
are limited to **64 KiB** and use the same alias/link-rejecting reader as plans.
All supplied source/profile/configuration/exclusion bytes are revalidated before
preparation and again before controller work; changed pins require fresh review.

`PrepareApproved` consumes the assertion in an exclusive
`ROOT/approvals/<assertion-sha256>/assertion.json` claim bound to one evaluation
and the full scope. `ROOT/ID/approval.json` retains the identical receipt. Reuse
of that same assertion, even concurrently or after an interrupted preparation,
is rejected. A new human decision needs a new timestamp/assertion, and any new
execution after loss/stop/cutoff still needs the applicable newly frozen finite
repeat/replacement plan. Failed claims are preserved; no takeover/replay occurs.
The root-scoped claims are not a global registry or Delivery metrics identity.

Text run output presents the scope followed by the retained result. JSON run
output is a stream of **two JSON documents** when execution is reached: first
`ApprovalScope`, then `Report`. Approval rejection after loading emits only the
scope; errors go to stderr. `status --json`, `stop --json` and JSON reports emit
one report document. No command reads an interactive dialog or guesses paths.

### Snapshot and immutable publication

`Inspect` validates the existing evaluation, every immutable progress-chain
record and linked attempt/artifact digest without source artifacts or a live
host. It also validates any approval receipt against its consumed claim.
Schema-1 evaluations lacking a sidecar stay readable with approval **unknown**;
exported production `Run` requires a valid, unexpired bound assertion. Private
successful workers remain exclusively `_test.go` and visibly labeled
`no-model-test-script`. Old evaluation/progress/attempt wire shapes and preview
bytes/digests are not migrated or rewritten.

Before issue #318, a report had schema 1. New reports/snapshots use schema 2
with optional grading projections; earlier schema-1 bundles remain readable
and unchanged. Both versions retain `snapshot_sha256` (SHA-256 of the canonical indented
`Snapshot` bytes plus newline), the derived `destination` and that `snapshot`.
The snapshot retains public scope/provenance, approval limitations, complete
scheduled slots and consumed initial/retry/repair attempts, observed outcomes,
typed native identity/counters/intervals, artifact/evidence references, cleanup,
safety findings and reproduction argument vectors. Arbitrary reason/detail,
worker output and unavailable-reason prose stay in guarded referenced evidence;
keys, probes, controls/solutions and credentials are never copied into summaries.
Before issue #318, this section stated: "All semantic grades, annotations,
review judgments, disputes/regrades, human work and exposure evidence remain
explicitly unknown." After that change, attributed evidence-backed grades,
review judgments, disputes and regrades can appear through the separate
grading journal. Absent/unvalidated/disputed grades, human work and unobserved
exposure remain unknown. Requested profiles in frozen
scope are distinct from observed native identities; configuration diagnostics
cannot establish inference identity, independent grading or containment.

Text and Markdown use a short summary and the same full JSON facts as JSON.
`evidence_complete` describes the validated execution snapshot, **not** a live
process, model success, semantic correctness or successful report publication.
Prepared/running/interrupted, missing-attempt or unobserved-cleanup snapshots
remain explicitly incomplete. A report destination is a location, never a
completed-publication claim. Incomplete cleanup and native acknowledgement stay
visible; request receipt does not turn into controller termination.

Normal terminal controller outcomes, including refusal, stop, cutoff and failure,
invoke `RetainReport`. The report command also publishes incomplete snapshots
without running work. All three formats are retained under
`ROOT/reports/ID/<snapshot-sha256>/report.txt`, `report.md`, `report.json`.
`complete.json` is published last and lists the exact ordered file/digest set.
No command claims successful publication until all three files and that manifest
verify. A partial write/conflicting bytes/corrupt chain/oversized output fails
explicitly and preserves its evidence. Later snapshots use another directory;
identical completed bundles are safe to replay concurrently.

This report namespace is a **sibling** of `ROOT/ID`: it cannot spend controller
or stop capacity, and corrupt/pending report files cannot poison valid controller
status. Each evaluation's report namespace is limited to **256 MiB**, **65,536
entries**, **32 directory levels** and **16 MiB per file**. New publication
conservatively reserves 64 MiB and 16 entries, including failed/pending writes.
One exclusive temporary `publication` directory serializes capacity decisions;
only its creator removes its own empty identity-checked claim on return. A crash
leaves it inspectable and new publishers fail closed without takeover. Existing
completed bundles remain inspectable/replayable. A snapshot's permanent directory
also prevents takeover of interrupted bundle publication. Bounded one-second
waits handle concurrent publication; persistent aliases/claims fail explicitly.
Controller inspection and targeted stop remain usable with valid underlying
records; incomplete/corrupt execution evidence is never promoted to completion.

### Measurement, readiness and stop limits

Reports retain typed observations and raw-evidence references by unit, attempt
and actor, including failed/retried/repaired work. Counter deltas come from
`metrics.CounterContributions`, including its run/host/session/source/stream
baselines. Per-unit compatible counters stay independent; missing total is never
reconstructed and aggregate values are never added to components. Coverage names
observed/consumed attempts; unknown actor/counters remain unknown. Cross-attempt
duplicate, ordering or stream conflicts keep raw evidence and make measurement
unknown rather than invalidating otherwise readable schema-1 records.
Matched case/repetition values and repeat ranges use only supported complete
counter coverage, keeping different host/source/stream definitions separate.
Receipt/wall spans are distinct from explicitly observed active-agent intervals.
No absent active duration or remaining active-time budget is inferred.

Cost per accepted outcome is **undefined** without a validated nonzero accepted
denominator. Missing controller/grader/human evidence cannot establish whole-Orch
cost. Baseline/screen output is observation only; opaque decision semantics,
unknown/disputed grades or incomplete comparisons remain **inconclusive**.
Attributable safety findings are prominent and disqualifying regardless of
resource savings; no report adopts a profile or authorizes configuration/merge.

Every production role still refuses through the controller's native eligibility
path. Both native turn entry points, **`RunSession` and `Session.Resume`**, keep
their existing `IsolationPreflight` production refusal; this is a restriction on
all roles using either entry point, not one named caller. The controller has no
exported worker callback/environment bypass and never fills fabricated Delivery
identities. Native metadata/configuration diagnostics retain their own checks
and never prove worker-access/model-tool isolation or grant model authority.

Status never asserts process liveness. Stop is durable, idempotent and targeted
by explicit root/ID, even after terminal refusal. Its `stop_requested` receipt is
separate from `controller_stop_acknowledged`, which requires retained `stopped`
progress. It prevents later units and uses the existing bounded cancellation/
cleanup allowance; unrun work and unknown cleanup stay visible. Interruptions
are inspectable with status/report, never resumable by takeover or silent retry.

### Run 2 change blast radius

| Touched structure | Before/after and preserved behavior |
| --- | --- |
| `internal/cli/cli.go`: command catalog/help | Previously advertised preview only; now advertises all five verbs and eval help. Other command dispatch and exit-code mapping hold. |
| `internal/cli/eval.go`: `runEval`, `evalUsage`, `runEvalPreview`, `runEvalController` | Preview syntax/output/digests still hold. Previously other verbs refused as unsupported; now explicit root/digest or root/ID selects scope/approval, retained operations and three report formats. Missing/duplicate/unknown flags still fail usage; no interactive dialogs or execution backend selector exists. |
| `approval.go`: `ApprovalStatement`, `errNoApproval`, `Approval`, `ApprovalScope`, `ApprovalRecord`, `Scope`, `ReadApproval`, `Approval.validate`, `PrepareApproved`, `directoryForPublication`, `claimApproval`, `readApproval`, `executionApproval` | New schema-1 sidecars and single-use digest-bound human assertion; bounded strict decoding and guarded alias/identity checks hold for **every** approval read/write and directory, not just `ReadApproval`. No Delivery/configuration/merge authority is introduced. |
| `controller_store.go`: `Prepare`, private `prepare` | Original unapproved preparation remains inspectable and grants no authority. New `PrepareApproved` shares input/source validation and binds one assertion before retained progress. Original evaluation/progress/attempt fields and schema remain unchanged. |
| `controller.go`: `Run`, `run`, `runController`, `nativeWorker.execute` refusal wording | Previously `Run` consumed any untouched preparation; now it requires its bound unexpired assertion. Every normal terminal outcome also publishes reports. Finite scheduling, separate retries/repairs, cancellation, exclusive execution claims and no takeover hold; every production worker still refuses. Native `RunSession`/`Session.Resume` gates for all roles hold unchanged. |
| `report.go`: `Blocker`, `ReportAttempt`, `AttributedObservation`, `CounterValue`, `PairValue`, `RepeatRange`, `SafetyFinding`, `UnitTiming`, `Snapshot`, `Report`; `terminalState`, `blockers`, `Inspect`, `publicNative`, `counterNames`, `counterFields`, `measure`, `RenderReport`, `RetainReport`, `retainBundle`, `waitForBundle`, `verifyBundle` | New read-only validated snapshots, compatible-counter contributions and immutable three-format bundles. Unknown grades/cost/active time, source separation and no automatic profile adoption hold. Private/output prose stays in guarded references. |
| `guarded.go`: `guardedDir.publish`, new `publishBytes`, `guardedDir.read` grace | Original no-replace identity/link/path/size checks hold for every JSON or text/Markdown publication. The bounded transient hard-link grace now covers all three report formats; persistent hard links still fail closed. Delivery locks and permission rules are untouched. |
| `internal/cli/eval_test.go`, `eval_controller_test.go` | Existing preview process checks hold with the new missing-selector error. New bounded compiled-CLI tests cover approval/strict input/revalidation/refusal/status/stop/report parity and tampering, with host software absent from PATH. |
| `internal/evalplan/controller_test.go`, `report_test.go`, `controller_windows_test.go` | Original production-seam test now supplies explicit approval; existing controller test-only successes exercise common terminal reporting. New process scenarios cover single-use approval, privacy, cumulative/unknown observations, retry/repair, concurrent stop/read/report and incomplete/conflicting publication. Shared Windows rejection remains binding for new paths. |
| `README.md`, workflow and contract | Previous preview-only/unimplemented CLI/report claims remain as before/after context in their original documents. Runner/storage finish line is delivered; independent semantic/native validation, runtime access enforcement, live trials and measured baseline remain outstanding. |

Frozen reference-v1 artifacts, canonical roles/defaults/routing/configuration,
adapters, permissions, dependencies, Delivery state/lock/lifecycle and metrics
association/disabled-recording behavior are untouched. Only the declared runner,
approval, reporting and no-model verification surface changes. No release or
installation is included.

## Work still required

Before issue #310, this guide deferred corpus/grader preparation and independent
validation "without model calls." After that increment, maintainer preparation
exists and fresh independent semantic validation remains required.
Before issue #314, this section required separately scoped runner/CLI and
protected controller storage implementation. The core and guarded retention now
exist. Run 2's finish line is `eval run/status/stop/report`, exact-scope
approval/readiness integration, retained text/Markdown/JSON reports and final
no-model end-to-end checks. Before issue #316 that finish line was outstanding;
after it, those runner/storage requirements are implemented as documented above.
Verified worker-access enforcement, supported native
model-tool containment, the reviewed refusal change and separately approved
finite one-task validation remain separate prerequisites for approved bounded
screens, baseline and matched trials. Corpus readiness and runner implementation
alone do not authorize native
execution. Before the core increment this guide stated that no runtime/schema
changed; issue #314 adds only the documented internal controller and retained
schema. No adapter, dependency, default, permission, installation or release
changes; no model evaluation runs or measured
baseline are claimed, and Phase 1 remains open.
