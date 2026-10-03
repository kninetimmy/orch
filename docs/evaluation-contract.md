# P1-C evaluation contract

This is the specification for preparing a historical replay corpus and comparing
Orch changes. Before reference-v1 preparation it stated: "It defines proposed
procedures, not observed results. No corpus, validated grader or measured baseline
is delivered here." After issue #310, the twelve-case
[reference-v1 corpus](../evaluation/reference-v1/preparation-report.md) supplies
versioned inputs, external keys and deterministic preparation controls. Its
report distinguishes author evidence from pending independent validation. No
validated semantic grader, protected runtime store or measured baseline is
claimed. Twelve cases can screen a change; they cannot establish general
superiority across repositories, hosts or tasks. Recovery tests and a later live
pilot supply separate evidence about the complete delivery workflow.

The companion [evaluation workflow](evaluation-workflow.md) defines discovery,
preview, approval, progress and saved results. Before issue #312, its `orch eval`
commands were proposed and unimplemented, absent from `orch help`. After that
increment, only `orch eval preview --plan FILE [--json]` is implemented: bounded
local validation, a frozen schedule and immutable public preparation metadata.
Execution, stop/status/report and protected worker-access enforcement remain
unimplemented. Preparation records and digests grant no approval. Existing
metrics remain available under their own recording and reporting rules.

The [metrics contract](metric-observations.md) governs native usage, provenance,
missingness and timing. The [native session contract](codex-native-protocol.md)
governs execution eligibility and boundaries. This document adds evaluation
requirements without changing either contract, runtime behavior, model/effort
defaults, dependencies, permissions or Phase 1 status.

## Corpus and case records

Prepare twelve historical cases: four scouting, four implementation spanning
mechanical, ordinary and demanding work, and four review comprising two clean
and two defective changes. Choose cases for distinct behavior and failure modes,
not because a favored configuration already handles them. A historical merge
is provenance, never proof that its solution or review verdict was correct.

Before tuning, freeze six development and six held-out cases, with two of each
role in each partition and one clean and one defective review per partition.
Equivalent cases, patched variants and cases sharing an answer must stay in the
same partition; select independent lineages to meet those counts. Do not split a
clean/defective pair across partitions merely to balance them. Development cases
may guide changes. Held-out prompts, outputs and grades must not guide tuning.
Record who accessed held-out material, when, what was exposed and why. Once it
informs tuning, mark it exposed and select a new independently checked held-out
case for future confirmation; retain the old results and exposure history.

Keep a versioned case record with a worker-visible input packet and a separate
controller-only answer key. Each record contains:

| Field | Required evidence |
| --- | --- |
| Identity and selection | Stable case ID, role, difficulty/failure mode, clean/defective review classification, partition and equivalent-case group. |
| Source | Full starting commit ID, exported snapshot digest, upstream issue/PR/commit provenance, and any deliberate defect patch with its digest and rationale. |
| Task and context | Exact task text and version/digest; versions/digests of role instructions and every supplied context artifact. Record sanitization of answer-bearing history or memory. |
| Permitted inputs | Explicit file/artifact list and allowed prior history, tools, commands, permissions, dependencies, host/toolchain versions and finite execution/verification limits. |
| Verification | Exact commands, prerequisites, expected exit/results, grading rubric version and control results; identify public checks versus controller-only checks. |
| Expected behavior | Required scout conclusions, implementation behavior, or review defects/verdict; severity, evidence requirements, acceptable alternatives and exclusions. These belong in the external key. |
| Provenance and revision | Authors and independent validators, dates, source evidence, unresolved disputes, exposure log and changes from the previous version. |

Freeze case, rubric and comparison-plan digests before candidate execution.
Changes to wording, context, checks or expected behavior create a new version;
never silently amend an old result. Compare the same versions on both sides.
Pre-existing model training exposure cannot be proved absent: record known
familiarity and suspected answer recall, and state that held-out means withheld
from this evaluation's tuning, not necessarily from model training.

## Grades and independent validation

The external answer key specifies behavior, not a required code shape. Before
issue #310 this required "A person other than the case author" to check
source/reproduction evidence and the rubric. After the approved clarification,
ordinary approved Orch agents may author corpus artifacts and a fresh person or
agent other than the author may independently validate them. The validator
checks source and control evidence independently, reproduces the controls and
checks the semantic rubric before any candidate is graded. Record the author's
and validator's identities, source access and unavoidable prior exposure. Agent
agreement alone is not validation. Disputes remain unresolved or go to a fresh
independent adjudicator; authoring access is distinct from tuning. This permission
applies to preparation for all three roles, not evaluation model trials.
Validate the grader on the pinned snapshot:

- Known-good reference outcomes must pass every required check.
- Known-bad controls must fail for their intended defect, not an unrelated
  setup error. Cover each required behavioral distinction, including a missed
  blocking defect and an unjustified blocking finding in review.
- Independently constructed, reasonable alternative solutions must pass. If
  there is no meaningful alternative, document why; do not equate textual
  agreement with correctness.

Scouting passes only when every required conclusion is supported by pinned
file/line or reproduction evidence, with uncertainty stated where the source
cannot settle it. Plausible unsupported claims do not pass. Implementations pass
only when the required behavior and regression checks pass within scope. Native
completion, an agent's success claim and a historical merge are not verification.

For review, match each finding to a distinct keyed defect using its trigger,
impact and source/reproduction evidence. Deduplicate descriptions of the same
defect. An additional real defect is adjudicated and versioned before being
counted; it is not automatically a false positive because the key omitted it.
The review passes only with the correct verdict, all required blocking defects
identified and no unjustified blocker. Advisory suggestions are reported
separately and never counted as blocking false positives or missed blockers.

Assign severity before candidate runs: **critical** means a security boundary,
authorization or data-integrity failure requiring rejection; **major** means
required behavior is materially wrong; **minor/advisory** means the change can
still meet the acceptance requirements. The key records the concrete trigger
and impact behind each assignment. A blocking defect is critical or major.
Missing a blocker is a missed defect even when another finding correctly blocks
the change. Explicitly approving a change containing a blocker is also a false
approval. Withholding approval without supporting the blocker is not a catch.

Disputed grades go to an independent adjudicator with anonymized outputs,
configuration labels hidden and evidence presented in balanced order where
feasible. Record any unavoidable identity exposure, decision and rationale.
Unresolved grades remain unknown and cannot support an improvement claim.
A faulty grader invalidates affected endpoints and comparisons. Version the
repair, revalidate controls and regrade all affected baseline and candidate
artifacts, not just the unfavorable ones; preserve original grades. If retained
artifacts cannot support regrading, new matched execution needs a new frozen
plan. A grader dispute never erases a substantiated safety violation.

## Measures and missing evidence

An evaluation unit is one case, repetition and configuration, including its
initial attempt and every permitted retry or repair. Report initial performance
before feedback separately from final acceptance after assistance. A repaired
outcome can become accepted but cannot replace the initial grade. Grade and
retain the initial artifact before repairs; feedback is limited by the frozen
plan and cannot reveal hidden keys or checks to a worker.

Report each role and partition separately, then any explicitly declared combined
view. Always show counts with rates. Repetitions estimate variability on the
same cases; three attempts on a case still provide one case of task coverage.
Eligible units for an endpoint include every scheduled unit run under its
declared conditions, including observed failures and timeouts. Identify invalid
and unrun units separately against the full schedule. An unavailable grade stays
unknown within eligible coverage. Native terminal success and evaluated task
correctness are distinct; retry/repair counts are events, not terminal states.

| Measure | Unit and denominator | Missing-data rule |
| --- | --- | --- |
| Correctness | Initial passes / eligible evaluation units, plus final accepted units / eligible units. Scout and implementation grades use the required behavior; review grades use the verdict and findings above. Report failures by case and role. | Ungraded units remain unknown. Show grade coverage and bounds: with P passes, U unknowns and N eligible units, the pass fraction is between P/N and (P+U)/N. Never drop unknowns to inflate success. |
| Review quality | False approvals / defective reviews; missed blockers / keyed blocking-defect opportunities, split by severity; unjustified blocking findings as a count and per review; clean reviews wrongly blocked / clean reviews. A defect repeated across trials creates repeated opportunities, not new independent defects. | Show known verdict/finding coverage and unresolved classifications. No opportunities means not applicable, not a measured zero rate. Report substantiated and unsupported advisory findings separately. |
| Human intervention | Counts of clarifications, corrections, escalations and repair events per evaluation unit; units needing intervention / eligible units. Measured human work minutes per unit and per final accepted unit, separately from waiting. | No logged activity is unknown unless an explicit completed observation records none. Unmeasured work duration is unknown even if event counts are complete. Retain intervention text/provenance and avoid double-counting the same event. |
| Efficiency | Each comparable native counter in tokens per final accepted unit, plus measured elapsed seconds per unit and per accepted unit. Report verification, CI waiting, human waiting and active-agent intervals by their existing definitions, separately. | Unknown usage or time stays unknown. Report coverage by unit, attempt, actor and counter; a partial subtotal is not complete cost. Zero accepted units makes cost per accepted outcome undefined, while all consumed resources remain reported. |
| Reliability | Terminal success, task failure, infrastructure failure, timeout, interruption, disconnect and retry/repair counts per eligible unit; terminal-state coverage / eligible units. Show clean shutdown and unchanged protected resources as observed checks. | Unknown terminal/cleanup state is unknown, not success. Keep infrastructure failures distinct from task failures; both consume resources and can prevent acceptance. |

For a resource R in the declared measurement scope, cost per accepted outcome is
`sum(R for all initial attempts, failures, retries, repairs and required checks)
/ final accepted units`. Include resources spent on units that never succeed.
Deduplicate cumulative samples using the metrics contract; do not average only
successful attempts. Per-unit elapsed time spans initial start through the
terminal result after allowed repairs; sum those durations for resource per
accepted outcome. Separately report total comparison wall time, which is not
the sum of concurrent task durations. Record the clock and start/end definition.

Counters retain host/source/stream definitions. Never add aggregate tokens to
their components, reconstruct an absent total, combine session-log and
app-server captures for one execution, or turn lifecycle gaps/token timestamps
into active time. Subscription cost is not inferred from tokens. Requested and
observed profiles stay distinct; configured-profile evidence is not proof of
per-turn inference identity. Root/Architect coverage can remain unavailable.
A claim about covered worker usage must name that scope; it cannot establish
whole-Orch cost when controller, grading or other relevant usage is unknown.

Existing metrics provide attributed observations, independent counters, explicit
missingness, measured interval categories and reported outcomes. They do not
capture this corpus's grades, seeded defects, false positives, task-bound elapsed
time, human work, exposure or comparison validity automatically. Keep those
additional evaluator annotations and artifacts outside worker access, linked by
case/version, unit, attempt and native identity to retained metric evidence.
Use the recorder's existing current-Delivery association and version rules;
this specification creates no standalone recording API or archived-run registry.
Save accepted evidence before run termination and preserve it for the report.

## Matched comparison protocol

Before execution, freeze a comparison plan naming the baseline, candidate and
one intended intervention. Pin Orch revisions, task snapshots, prompts/context,
role instructions, host/toolchain, tools, permissions, checks, requested profiles
and finite limits. All remain equal except the declared intervention; multiple
inseparable changes support only a claim about their combined effect. Record
requested model/effort and actual reported settings independently, with native
host/version, session/thread/turn identities and identity-evidence limitations.
If the claim requires inference identity that is unavailable, that claim is
inconclusive even if configured settings match.

Declare before results: included cases/partitions, endpoint and scope, quality
tolerances, efficiency target, evidence requirements, repetitions, maximum
attempts/repairs, execution/verification/cleanup deadlines and stopping rules.
No unset or unlimited cutoff is allowed. The proposed initial full baseline
observes one reference configuration three times on every case: 36 units, with
no candidate required. A subsequent matched comparison runs both configurations
under the same frozen plan: 36 baseline and 36 candidate units. The earlier
baseline remains a historical reference, not a substitute for contemporaneous
matched baseline units. A smaller four-case screen (scout, implementation,
defective review and clean review) must be declared separately; its results do
not count as completing the twelve-case baseline. Numeric budgets and any repair
allowance still require selection in the later trial plan, not during a run.

Each unit starts from a fresh disposable snapshot and a fresh session with only
its permitted input packet. No inherited conversation, prior answer, model
memory, repair history or other unit's output is supplied. Baseline and candidate
units are matched by case, version and repetition; native randomness need not
match and must not be described as controlled if unavailable. Execute pairs in
the frozen case-ID/repetition order, alternating baseline-first and
candidate-first by pair ordinal. Record actual order and host conditions.
Changing conditions that compromise a declared endpoint invalidate that endpoint.

Retain every scheduled unit, attempt, prompt, output, diff, verification result,
grade, intervention, resource observation and terminal/cleanup result. Stop on
safety violations, native refusal, or the overall comparison cutoff. End each
attempt at its own cutoff, record its outcome and continue the remaining schedule
only within the frozen plan. Never stop early because the current average looks
favorable. A safety stop rejects the candidate when attributable to it. Other
incomplete comparisons remain inconclusive. Do not
choose the best repetition, replace poor attempts silently, or count repeats
as independent case coverage. Any further execution needs another frozen plan
with its finite repeat/replacement rule; publish both sets of results.

A wrong answer, observed timeout or execution failure under the declared
conditions is a poor outcome, not an invalid measurement. Wrong snapshots,
answer leakage, unintended profiles or corrupted measurement invalidate affected
evidence. Missing usage can invalidate a cost claim while leaving an independently
verified correctness grade usable. Show protocol-invalid units against the full
scheduled count, explain every exclusion and retain their consumed resources
separately; invalid resource samples cannot be treated as complete totals.
Known failures stay failures when another endpoint is invalid. Unrun units after
a stop are explicitly unrun, never invented attempts or zero-cost successes.

## Decisions and hypothetical arithmetic

Use separate quality, safety, human-effort, efficiency and reliability results;
do not combine them into one score. The trial plan must declare acceptable
quality differences and resource targets, with the evidence needed to establish
them, before candidate runs. Report paired per-case values and repeat ranges,
not merely a pooled average. Predeclare whether the decision uses a bounded
screening rule or an uncertainty method; select any method and margins before
results, and do not claim statistical confidence from a threshold alone.

Reject as **regressed** on a substantiated candidate safety violation or newly
missed critical defect, regardless of savings. Also reject on a demonstrated
violation of a declared quality/reliability tolerance. **Improved** requires
valid evidence meeting all gates and the declared efficiency or quality target,
within its stated scope. **Inconclusive** covers incomplete evidence, unresolved
grades, noisy results that do not meet the declared rule and improvements too
small for the target. It retains the existing configuration. A screening
improvement justifies further validation, not automatic deployment or merge.

The following comparisons are entirely **hypothetical**, with fabricated numbers
only to check the rules. They are not Orch measurements, recommendations for
production tolerances, or an approved trial plan. Assume twelve cases and three
repeats, a frozen validated grader, complete task-agent total-token coverage from
one comparable source, and full grade/terminal coverage. Controller usage is
unknown, so cost claims cover task agents only. The illustrative rule requires
no increase in initial failures, missed blockers, false approvals, unjustified
blockers, measured human work or infrastructure/timeout failures; at least 10%
lower tokens per accepted outcome; savings on at least nine of twelve case
aggregates; and no case aggregate more than 10% worse. Critical misses and safety
violations are unconditional rejection grounds. These are descriptive screening
thresholds, not population confidence bounds.

The shared hypothetical baseline has 34/36 initial passes and 36/36 final
acceptances after two repairs. Each side's normal-quality pattern is twelve
scout passes, ten implementation passes plus two repaired implementations, and
twelve correct reviews. The six defective review units each contain one keyed
blocking defect; all six are caught. False approvals, missed blockers,
unjustified blockers, infrastructure failures and timeouts are zero. There are
two human repair events and six measured work minutes. All repair resources
are included in 360,000 task-agent tokens: `360,000 / 36 = 10,000` tokens per
accepted outcome. Each case aggregate consumes 30,000 tokens.

| Hypothetical candidate | Calculation and decision |
| --- | --- |
| Improved | Same grades, interventions and reliability as the baseline; 306,000 tokens including repairs. `306,000 / 36 = 8,500`; reduction `(10,000 - 8,500) / 10,000 = 15%`. Each of twelve case aggregates is 25,500 tokens, 15% lower. Every gate passes: improved for this screen and scope. |
| Regressed | 33/36 initial passes, 35/36 final acceptances: one review approves a keyed critical defect the baseline caught. False approvals are 1/6 and critical misses are 1/6 critical opportunities, assuming all six blockers here are critical. Despite `216,000 / 35 = 6,171.43` tokens per acceptance and about 38.29% savings, the new critical miss rejects the candidate. |
| Inconclusive | Same grades, interventions and reliability as baseline; 342,000 tokens including repairs. `342,000 / 36 = 9,500`; savings are 5%, with each case aggregate also 5% lower. It misses the predeclared 10% target, so retain the baseline. |
| Invalid cost evidence | Same verified grades; 24 of 36 candidate units have complete token evidence totaling 204,000; twelve lack it. Coverage is `24 / 36 = 66.67%`. `204,000 / 36 = 5,666.67` is an incomplete subtotal per acceptance, not comparable cost. Even `204,000 / 24 = 8,500` describes only the covered subset. Cost improvement is inconclusive; preserve the usable grades and missingness. |

## Preparation, isolation and execution gates

Corpus and grader preparation uses no model calls. Trusted maintainers export
pinned inputs, construct controls, run deterministic checks and validate rubrics.
The worker packet contains only permitted source/context. Grading and reference
solutions stay in a protected controller area, never merely a hidden filename
in a worker-readable checkout. Historical tasks may depend on permitted earlier
history; exclude every future/solution object and reference from the task copy.

Disposable execution copies must have no access to future Git objects, shared
Git metadata, alternates, solution patches, hidden checks, answer-bearing memhub
databases/renderings, previous sessions, controller state or credentials.
Inspect exports, submodules, links/junctions, Git pointers, inherited environment,
configuration, caches and tool capabilities for paths back to those resources.
Removing a remote does not remove object history or push credentials. Ordinary
shared worktrees do not provide this containment. Use independent approved
checkout/scratch locations and verified denial of protected reads/writes.
Production push must be unavailable, including through credentials, other
remotes, inherited tools and network capabilities. Workers never receive the
controller's GitHub access or approval/lifecycle capability. Required trusted
grading/Git operations happen outside the worker boundary.

Native authentication remains with the trusted host under the native contract;
do not copy credentials into a task copy. Enforce and verify tool closure across
shell, apps/MCP/connectors, web, hooks, plugins, memory and additional agents.
A filesystem profile, disabled configuration flags or successful synthetic
command probes alone do not prove model-tool closure. Canonical role restrictions
and requested profiles remain authoritative; failure to verify containment
refuses execution rather than widening permissions or substituting a manual run.

The stages are separate:

1. Prepare and independently validate the corpus/grader without evaluation model
   trials. Ordinary approved authoring/review agents may do this work. The prior
   wording "without model execution" did not distinguish those agents from
   evaluated workers; after issue #310 the prohibition is on evaluation model
   trials, not approved preparation agents.
2. Obtain separate approval for the finite one-task model/tool isolation smoke
   in the [native protocol](codex-native-protocol.md#separately-approved-live-modeltool-smoke).
   Its prerequisites include supported native enforcement, an approved reviewed
   change to the refusal, and exact-host/profile command containment checks.
   The existing `ErrIsolationUnavailable` production refusal remains binding;
   approval of this document does not satisfy those prerequisites.
3. Only after those prerequisites pass, obtain approval of the frozen finite
   screening/baseline plan and execute it under the same verified boundary.
   Smoke success alone does not authorize further tasks or unattended delivery.

The production turn restriction covers both `RunSession` and `Session.Resume`
through `IsolationPreflight`, for every task role using those entry points. It
is not limited to one caller of `ErrIsolationUnavailable`, nor a claim that every
native API returns that sentinel: read-only metadata and bounded command
diagnostics have their own checks and do not establish model-tool readiness.

The deferred native model-tool validation and other isolation follow-ups are
not reopened here. No model/tool smoke, replay trial, runtime change or permission
change is authorized or performed by this specification.

## Readiness and baseline deliverables

The retained [preparation report](../evaluation/reference-v1/preparation-report.md)
links all twelve versioned input declarations, external keys/controls, frozen
partition/lineage/exposure records, reproducible commands, artifact digests,
author outputs and independently retained review evidence. It can declare
preparation validated only when the fresh validator's evidence is complete and
disputes are resolved. Controller-source material in `evaluation/reference-v1`
is in the maintainer repository, not a protected runtime store. It must never be
mounted or made reachable by evaluated workers. Reconstructed controller
artifacts and worker packets are separate. Export inspection and negative tests
establish contents and destination containment only. Protected runtime access
enforcement remains unimplemented and blocks execution readiness.

Before this increment, a later corpus-readiness record was required to contain
"all twelve versioned case packets and protected keys" with independent/control
evidence and access-boundary inspection. After this increment, controller-source
keys and preparation evidence exist, while the protected runtime copy and its
access boundary are still required separately. Corpus readiness does not imply
native execution readiness.

A later baseline report must preserve the frozen plan and revisions; case and
partition coverage; requested and observed profiles with evidence limitations;
execution order and limits; every unit/attempt and unrun slot; initial and
assisted grades; defect-level review results; human interventions; terminal and
cleanup results; raw metric references and per-counter/actor coverage; comparable
per-case/repeat resource calculations; invalidations, regrades and exclusions;
paired decisions against predeclared thresholds; and reproduction instructions.
It must name unknowns, exposed cases, limited task/host coverage and separate
recovery/live-pilot evidence. A baseline records observations even if it cannot
support an improvement claim; it must never disguise incomplete coverage as
completed validation.

Before reference-v1 preparation, the actual cases, lineage groups, concrete
checks/controls and artifact locations were undecided, and "No corpus control
checks, grader validation, isolation validation or model trials have been
executed for this document." After issue #310, the preparation report records
the selected cases and checks actually performed. Independent semantic
validation/adjudication, protected runtime storage/access enforcement, trial
profiles/resource scopes, numeric execution/retry budgets, quality targets and
the uncertainty rule still require their own evidence or approved work. There
is no measured P1-C baseline. Phase 1 remains open until its baseline/report exit
requirements are satisfied with coverage limitations stated; preparation does
not close it or authorize models.
