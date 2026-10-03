# P1-C evaluation preview and proposed execution workflow

**Status: local preview and maintainer preparation implemented; runner and
protected storage unimplemented.** Before issue #312, every `orch eval` command,
plan format, storage location and output here was proposed and unimplemented;
`orch help` did not list this family. Now help exposes
`orch eval preview --plan FILE [--json]`. It validates and retains public local
preparation metadata with a frozen bounded schedule and explicit blockers. It
never starts evaluation work or grants approval. Every other evaluation verb
still fails explicitly. Existing `orch metrics` reporting and `orch metrics
record` observations are available under the [metrics contract](metric-observations.md);
they do not supply evaluation grades or a comparison runner. Before issue #310
this guide stated: "There is no prepared reference corpus, validated grader or
measured P1-C baseline delivered here." After that increment,
[reference-v1](../evaluation/reference-v1/preparation-report.md) contains twelve
scoped historical cases and deterministic preparation controls. Independent
semantic validation is separately retained; no validated grader, protected
runtime store or measured P1-C baseline is claimed by author checks.

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
| `orch eval run --plan DIGEST` — proposed | Loads that exact saved plan, revalidates eligibility and requests explicit approval through the applicable existing gates. Only then may the future controller execute its frozen schedule and save progress, evidence and results. A digest identifies a plan; it is not approval. |
| `orch eval status --run ID` — proposed | Reads saved progress and reports current work, remaining limits, coverage and blockers. Starts no execution. |
| `orch eval stop --run ID` — proposed | Requests a stop for that evaluation, prevents new units and interrupts active work with bounded cleanup. Saves the reason and observed terminal/cleanup evidence. |
| `orch eval report --run ID --format text\|markdown\|json` — proposed | Reads retained evidence and renders the selected local result format. Saves it under the approved report destination; it does not rerun tasks or infer missing data. |

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
The overall cutoff can be smaller than that ceiling: a future runner would
retain unrun slots after cutoff. No attempts, successes, grades or resource
observations are invented. This example schedules eight units, eight maximum
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

`orch eval stop` would stop an evaluation. Existing `orch abort` returns a
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
protection. It retains public metadata only; the protected runtime store,
execution evidence and result reports described below remain unimplemented.

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

## Work still required

Before issue #310, this guide deferred corpus/grader preparation and independent
validation "without model calls." After that increment, maintainer preparation
exists and fresh independent semantic validation remains required. Separately
scope runner/CLI and protected controller storage implementation; supported native
model-tool containment, the reviewed refusal change and separately approved
finite one-task validation; then approved bounded screens, baseline and matched
trials. Corpus readiness and runner implementation alone do not authorize native
execution. No runtime, adapter, schema, dependency, default, permission,
installation or release is changed here; no model evaluation runs or measured
baseline are claimed, and Phase 1 remains open.
