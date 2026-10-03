# Proposed P1-C evaluation workflow

**Status: proposed runner; maintainer corpus preparation implemented.**
Every `orch eval` command, plan format, storage location
and output described below is proposed and unimplemented. Current `orch help`
does not list this family. Existing `orch metrics` reporting and `orch metrics
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

## One proposed CLI for terminal and agent use

The agent invokes the same proposed family and presents its output; it does not
create a separate execution or approval policy.

| Proposed operation | Reads, saves or executes |
| --- | --- |
| `orch eval preview --plan FILE` | Reads the proposed plan, pinned artifacts, effective configuration and readiness evidence. Validates and displays scope/limits, then saves an immutable normalized plan with a digest and any blockers in the controller area. Starts no model work and changes no Delivery state. |
| `orch eval run --plan DIGEST` | Loads that exact saved plan, revalidates eligibility and requests explicit approval through the applicable existing gates. Only then may the future controller execute its frozen schedule and save progress, evidence and results. A digest identifies a plan; it is not approval. |
| `orch eval status --run ID` | Reads saved progress and reports current work, remaining limits, coverage and blockers. Starts no execution. |
| `orch eval stop --run ID` | Requests a stop for that evaluation, prevents new units and interrupts active work with bounded cleanup. Saves the reason and observed terminal/cleanup evidence. |
| `orch eval report --run ID --format text\|markdown\|json` | Reads retained evidence and renders the selected local result format. Saves it under the approved report destination; it does not rerun tasks or infer missing data. |

### Illustrative end-to-end screen

The following is **proposed, unimplemented syntax**, not a runnable recipe or
approved budget. Field names illustrate required content, not an existing plan
schema. All uppercase values must be replaced by verified pinned values in a
later trial plan. Case IDs below are invented examples of four development cases;
they are not delivered corpus records.

```json
{
  "purpose": "matched-screen",
  "corpus_manifest": "./reference-corpus/manifest.json",
  "corpus_digest": "CORPUS_DIGEST",
  "cases": ["scout-dev-1", "implement-dev-1", "review-defective-dev-1", "review-clean-dev-1"],
  "partition": "development",
  "baseline": {"orch_revision": "BASELINE_FULL_OID", "profile_digest": "REFERENCE_PROFILE_DIGEST"},
  "candidate": {"orch_revision": "CANDIDATE_FULL_OID", "profile_digest": "REFERENCE_PROFILE_DIGEST"},
  "intervention": "one reviewer-instruction change between the pinned Orch revisions",
  "repetitions": 1,
  "limits": {
    "attempt_seconds": 300,
    "verification_seconds": 120,
    "cleanup_seconds": 30,
    "comparison_seconds": 3600,
    "max_attempts_per_unit": 1,
    "max_repairs_per_unit": 0
  },
  "measurement": {"source": "codex-app-server", "scope": "task agents"},
  "decision_rule": "DECISION_RULE_ARTIFACT_DIGEST",
  "readiness": {"corpus_grader": "CORPUS_READINESS_DIGEST", "native_execution": "NATIVE_READINESS_DIGEST"},
  "report_root": "CONTROLLER_ONLY_ABSOLUTE_PATH"
}
```

The referenced manifest must resolve case versions, snapshots, prompts/context,
role instructions, checks and prerequisites to digests; the profile artifact
must resolve exact requested selections and host/toolchain/configuration.
Readiness records and the decision rule are also required retained inputs.
This schedules eight units with no retries, repairs or worker feedback. The
30-second cleanup bound includes interruption and shutdown; the overall cutoff
includes execution, verification and cleanup.
For this hypothetical rule, require no increase in initial failures, missed
blockers, false approvals, unjustified blockers, human work or reliability
failures; at least 10% lower comparable task-agent tokens per final accepted
outcome; and no case more than 10% worse. Critical misses and safety violations
reject unconditionally. This is a bounded screening rule, not statistical
confidence or a recommended production tolerance. Freeze required measurement
coverage and unknowns with it; missing comparable tokens makes its cost claim
inconclusive. Controller usage and unmeasured active time remain unknown.

After independently preparing and validating those inputs, the hypothetical
terminal sequence is:

```sh
# PROPOSED AND UNIMPLEMENTED: save the illustrative inputs as eval-plan.json.
orch eval preview --plan ./eval-plan.json
# Inspect coverage, readiness, budgets and rules; use the returned plan digest.
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

Today an agent must explain that this workflow is unimplemented and report
readiness gaps; it cannot substitute manual model execution to fulfill it.

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
