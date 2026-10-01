# Metric observations

`orch metrics record` reads one observation JSON document from stdin and writes
`{"schema_version":1,"enabled":true,"recorded":true}`. An exact replay returns
`recorded:false`. With metrics disabled, a valid request returns both booleans
false and creates no metrics storage. Errors return exit 1. This command makes
no lifecycle, approval, review-verdict, git, or GitHub changes.

```json
{
  "schema_version": 1,
  "run_id": "run-20260930T120000Z-12345678",
  "id": "session-a:tokens:2",
  "at": "2026-09-30T12:05:00Z",
  "source": "codex-session-log",
  "issue_number": 281,
  "role": "specialist",
  "attempt": "implementation-1",
  "host": "codex",
  "session": "session-a",
  "requested": {"model": "requested-model", "effort": "high"},
  "observed": {"model": "host-reported-model"},
  "sample": {
    "stream": "native-token-usage",
    "mode": "cumulative",
    "sequence": 2,
    "counters": {"input_tokens": 100, "output_tokens": 0, "total_tokens": 150}
  }
}
```

The run must be the current Delivery run, with a consistent run lock; a supplied
issue must belong to it and a supplied host must match its host. Associations and
the metrics-enabled setting are read inside the same repository mutation lock
used by lifecycle commands. This permits recording failures even when a run is
stopped or an issue is blocked. It does not authorize any recovery decision.
After completion or abort clears run state, late submissions are rejected:
there is no archived-run association registry. Submit evidence before finishing
the run. Previously accepted history remains readable.

`id`, `source`, and `at` are required. Identity is run-scoped, immutable, and
compared by decoded field values, including timestamp strings. An exact replay
does not rewrite the file; changing any field under the same ID is an error.
IDs, sources, sessions, attempts, and stream names are opaque nonempty strings
of at most 512 bytes without whitespace or control characters; only the run ID
becomes a path and it must be a filename-safe token starting with `run-`.
Unknown fields and unsupported versions fail closed.

Optional attribution includes `issue_number`, `role` (architect, scout,
implementer, specialist, reviewer), `attempt`, `review_cycle`, `host` (claude,
codex, opencode), and `session`. Attempt identifiers and positive review-cycle
ordinals require an issue; they are supplied evidence, not inferred from the
run's current routing or review cycle. Requested and observed profiles are
independent partial objects: absent model, effort, or variant remains unknown.
An explicit empty variant means no host variant; effort and variant cannot both
be supplied in one profile. The recorder never fills observed fields from
requested fields or config.

Observation schema 1 remains readable and recordable. Schema 2 adds explicit
missingness and the independent `reasoning_output_tokens` counter. Exactly one
payload is required:

- `sample`: native `input_tokens`, `output_tokens`, `cache_read_tokens`,
  `cache_creation_tokens`, `total_tokens`, and (schema 2) `reasoning_output_tokens`
  are independent optional int64
  counters. Zero is measured zero; absent or null is unknown. At least one must
  be present. All fields inherit the observation's source and sample's stream;
  different definitions or sources need different streams. No input/output/cache
  sum is substituted for an absent aggregate, and fields are never reconciled.
- `interval`: `{"kind":"verification","start":"2026-09-30T12:00:00Z",
  "end":"2026-09-30T12:05:00Z"}` records one explicit measurement. Kinds are
  `active-agent`, `verification`, `ci-waiting`, and `human-waiting`. Timestamps
  use RFC3339 with optional fractional seconds, start <= end <= observation at,
  and duration must fit int64 nanoseconds. Zero duration is valid. Overlapping
  intervals remain separate evidence; the recorder does not invent a union,
  infer missing time from lifecycle gaps, or sum different kinds.
- `outcome`: `implementation-failure`, `infrastructure-failure`,
  `evidence-correction`, `wrong-requirement`, `escalation`, or `approval` records
  a reported result, never a lifecycle verdict or permission.
- `unavailable` (schema 2): `{"reason":"matching-child-unavailable"}` records
  missing evidence. The reason is a nonempty identifier under the same length
  and whitespace rules as source. It contributes no counters or duration and
  asserts no failure outcome. Omit a session that is not known; do not invent
  an empty sample or measured zero. Its ID/time describe the coverage check,
  not an unobserved native event.

Samples require host, native session, stream, and a positive sequence. A stream
is the tuple (run, host, session, source, stream). Its mode is fixed as either
`cumulative` or `delta`; use a new stream for a different counter definition.
Sequence must strictly increase and observation time must not decrease. A
cumulative field starts with a zero baseline and thereafter contributes only
its increase; an omitted field contributes nothing and leaves its previous
baseline intact. Thus 100, 150, and replayed 150 contribute 150 once, while a
new session starts its own baseline. A fresh ID/sequence with the same cumulative
value contributes measured zero. Delta samples contribute their supplied values.
Negative counters, regressions, reordered samples, conflicting identities, and
int64 overflow of a stream's counters fail before any write.

`metrics.CounterContributions` replays accepted observations and exposes these
per-observation deltas for future consumers. It never folds in legacy events
or combines different streams into a native total. The history itself is the
baseline store; temp-file write, sync, and atomic replacement commit evidence
and baselines together. Retry the identical request after a storage failure or
an uncertain response. There is no separate checkpoint to advance or repair.

## Read-only report and P1-A boundary

`orch metrics` reports the evidence already present in every supported schema-1
or schema-2 run document. It does not create or rewrite metrics storage. A
corrupt document, an unsupported document or observation version, or invalid
counter history still fails clearly instead of producing a partial report.

Legacy lifecycle usage remains readable per event. Its host, native source and
session semantics were never recorded, so the report does not publish a
combined legacy total or compare it with native observations. Each legacy
counter prints its decoded presence: an explicit zero is `0`, an omitted value
is `unknown`, and `duration_ms` is labeled `unclassified reported duration`.
Legacy duration is not assigned to an activity category.

Native sample rows identify run, issue, role, native session, attempt and review
cycle. Requested and observed profiles print separately; neither fills gaps in
the other. The report uses `CounterContributions` for cumulative/delta replay,
including repeated samples that contribute a measured zero. Comparable totals
are grouped only by host, source and stream, the contract's counter-definition
boundary. Every independent counter has its own total and measured-sample count;
aggregate and split counters are never added together. Different sources or
streams remain separate, and cross-session total overflow fails instead of
wrapping.

Coverage lists roles with recorded counter samples, roles with execution or
observation evidence but no counters, known native sessions with and without
counter samples, incomplete host/session attribution, and explicit unavailable
records. Architect is always named in role coverage because root usage
participates in the run but has no automatic capture path. Recorded sessions
are evidence, not a complete session census; the report always says the complete
native session count is unknown. Lifecycle-event count is not used as a usage
denominator.

Measured timing is separate for `active-agent`, `verification`, `ci-waiting`
and `human-waiting`. Each session/category uses an interval union, so replayed
or overlapping intervals count once. Session unions are summed for agent
effort, while a second union across sessions reports wall-clock coverage without
double-counting concurrent work. Intervals without complete host/session
identity contribute to wall-clock coverage but are excluded and named in the
session-summed value; when all intervals lack complete identity, session-summed
effort is `unknown` rather than zero.
No interval for a category means `unknown`; a recorded zero-length interval is
measured `0s`.

Explicit observation outcomes report implementation failure, infrastructure
failure, evidence correction, wrong requirement, escalation and approval as
separate categories. Unambiguous engine escalation and review-approval events
print separately from reported outcomes. Other historical events remain
unclassified, and all observation evidence remains read-only: it cannot change
a review verdict, approval or merge decision.

This closes P1-A's durable evidence and local reporting boundary. Native Windows
Claude isolation/bridge work remains in follow-up #208 (P1-B), and live
automation/evaluation remains P1-C. This delivery adds no Claude/OpenCode
capture path, host installation, live model/API call, dashboard, exporter or
telemetry.

## Compatibility and blast radius

Before this change, metrics schema 1 held only lifecycle events and the metrics
command had only a read-only form. Schema 2 adds a separate `observations` array;
the no-argument report remains read-only, keeps legacy events readable and now
reports observation usage, coverage, timing and outcomes. Native Codex child
capture is implemented below.

Schema-1 history remains readable without rewriting it. A lifecycle append to
an existing schema-1 document keeps that version; the first successful new
observation upgrades only that metrics document to schema 2, preserving its
legacy events. Newly created documents use schema 2. Old binaries cannot read
schema 2: use this or a newer compatible reader for that history. No active-run
state, audit manifest, or approval migration is required or performed. Do not
downgrade a metrics document by relabeling its version.

| Element | Previous behavior and status after this change |
| --- | --- |
| `metrics.Document`, `SchemaVersion`, `load`, `save`, `LoadAll` | Before: schema 1 only. After: read 1/2, write fresh documents as 2, upgrade existing 1 only on recording an observation. Corrupt/unsupported history still fails closed; reports never write. |
| `metrics.Usage`, its JSON methods and `Counters` | Before: omitted and explicit zero decoded to the same integer, and zero was omitted on write. After: existing integer fields/manual callers retain their meaning, decoded explicit zero survives rewrites, and `Counters` exposes omitted fields as unknown. Legacy usage never becomes a native sample or baseline. Reports label its missing attribution and duration limits explicitly. |
| `metrics.Append`, `Event`, run activation and `recordMetric` callers | Lifecycle events, manual PR/review/executor usage, event ordering, enabled gate, and post-mutation error behavior remain. Append preserves observations. Callers and their Delivery decisions are unchanged. |
| `metrics.write`, `strictDecode`, `validateRunID` | Atomic temp/sync/rename and path validation remain. Strict decoding now checks EOF explicitly. The new document validator additionally checks accepted observations and their stream arithmetic before writes and on reads. |
| `Observation`, `Profile`, `Counters`, `CounterSample`, `Interval`, `ParseObservation`, `Record`, `CounterContributions` | New evidence-only contract; partial observed profiles are intentionally distinct from routing `manifest.Selection`, whose complete-profile restriction remains unchanged for every Selection. All counter fields share presence/nonnegative/overflow rules, not just total tokens. |
| CLI `commands`, `runMetrics`, `cmdMetrics`, `withDeliveryMutation` | Before: metrics rejected every argument. After: `metrics record` adds JSON recording; other arguments still fail. `cmdMetrics` remains read-only and reports observations without mixing incompatible evidence. Every metrics storage writer (`Append` and `Record`), not just `Append`, needs external serialization; production record, lifecycle, resume, and abort commands share the existing repository boundary. Direct package callers must supply serialization themselves. |
| `state.Load`, `state.CheckConsistent`, `lockfile.Inspect`, `config.Load` | Reused read-only for current association and enabled checks. State schema, lock ownership, configuration, routing, approval, phase transitions, review verdicts, and GitHub resources retain their prior behavior. No new storage registry or dependencies. |
| Metrics and CLI tests | Existing tests remain; focused checks add process restart/replay, concurrent submissions, failure/retry, presence, legacy reads, invalid associations and disabled storage. The required local race gate is `go test -race ./internal/metrics ./internal/cli`; current CI does not run it. |

## Codex capture and recording

`orch hook codex subagent-usage` remains read-only. An unversioned request
retains its original exact aggregate or `previous_total_tokens` delta response,
including `{}` when unavailable. Explicit `schema_version:2` selects a different
closed request/response shape; all other explicit versions are rejected.

```json
{
  "schema_version": 2,
  "parent_thread_id": "native-parent-thread",
  "task_identity": "/root/issue_282_executor",
  "run_id": "run-20260930T120000Z-12345678",
  "issue_number": 282,
  "role": "specialist",
  "attempt": "implementation-1",
  "unavailable_id": "issue-282-implementation-1-check-1",
  "unavailable_at": "2026-09-30T12:10:00Z"
}
```

The response has `schema_version:2`, an `observation` ready for `metrics record`,
and, when identified, the native session metadata's `native_source`. Save the
request and response, then send only the observation unchanged to the recorder.
Recording still returns response schema 1. The request's run/issue, role,
attempt and cycle are caller-supplied associations, not proof of an execution
profile. Executors/reviewers require an issue and attempt; reviewers require a
positive `review_cycle`. Scouting may be run-level without an issue/attempt.
The recorder still checks current run/issue/host association under its lock.

The capture uses the existing session directory and JSONL parser. Both
`TotalTokens` and `Completed` accept only an unambiguous child whose persisted
parent references and canonical task identity match. Parent totals, siblings,
wrong-parent children, duplicate matching files, identified malformed files and
unfinished rollouts are never substitutes. Unidentified/unrelated corruption
cannot suppress another child's valid result. `Completed` additionally requires
a canonical child path, agreement with any source-level agent path, valid native
counters, and the terminal token event's RFC3339 timestamp. Those extra checks
apply to the new capture only; the legacy aggregate helper still accepts its
previous fixtures without timestamps or optional split counters.

The supported terminal pair is `token_count` followed by `task_complete`.
`total_token_usage` fields map as follows; a missing or null field stays unknown
and zero stays measured zero:

| Native field | Observation counter |
| --- | --- |
| `input_tokens` | `input_tokens` |
| `cached_input_tokens` | `cache_read_tokens` |
| `cache_write_input_tokens` | `cache_creation_tokens` |
| `output_tokens` | `output_tokens` |
| `reasoning_output_tokens` | `reasoning_output_tokens` |
| `total_tokens` | `total_tokens` |

These preserve the host's definitions as independent cumulative counters: cached
input and reasoning output are not added to input/output, and no component sum
replaces the native total. Counter field names and timestamp presence were
confirmed from local persisted host metadata; fixture values are synthetic.
No additional host event or execution-profile format is assumed. Models,
effort/variant, elapsed time and active intervals remain unknown in this capture.
The native token timestamp is an observation time, never an activity estimate.

Samples use source `codex-session-log`, stream `codex-total-token-usage`, actual
session metadata `id`, and the token event's position among nonempty JSONL
records as sequence. The deterministic observation ID is
`codex:<session>:tokens:<sequence>`. Repeated reads, including after process
restart, produce the same observation. A resumed executor has the same stream
and a later sample, so the durable recorder contributes only increases. Fresh
reviewers use their own sessions; attempts/cycles never partition the baseline.
Changing an existing sample's association conflicts rather than double-counts.
Replacing, truncating or reordering a rollout can cause identity/sequence or
counter conflicts: surface those errors, never reset a baseline to evade them.

Unavailable capture returns observation schema 2 with only `unavailable` as its
payload, the caller's retained check ID/time, and no invented native session.
Reasons distinguish storage, missing identity/match, duplicate matches, invalid
or unfinished evidence, absent counters and missing native timestamps. Save and
retry that exact observation after uncertain recording; a new coverage check
gets a new ID/time. Missingness never advances a token baseline.

An exact Scout child can use this path. Independently attributable planning or
scouting evidence can use the generic recorder, including run-level architect
or scout roles. Root/Architect usage has no supported automatic capture path
here; absent coverage must be explicitly recorded as unavailable with its reason,
not estimated or copied from child/parent totals. No API fallback, live model
call, requested-profile proof, or permission bypass is involved. Submit before
run completion/abort; the existing current-run association limit still applies.

## Codex compatibility and blast radius

Before: the Codex adapter retained previous totals conversationally and submitted
full/delta usage on lifecycle verbs. After: it records cumulative observations
and omits both `usage` and `executor_usage` on those verbs. The same before/after
is retained in the adapter document that originally prescribed the old behavior.
Legacy helper callers and Claude/OpenCode manual workflows remain unchanged.

Observation schema 2 still lives in metrics document schema 2. The prerequisite
engine at `4097c4d` understands document schema 2 and observation schema 1, but
rejects schema-2 observations; engine `47f0cbc` predates both native observation
contracts. This engine reads observation 1/2 and document 1/2. Unknown fields
and unsupported versions still fail closed, including new fields relabeled as
observation 1. Never relabel history to make an older binary accept it. The
adapter's `0.8` label alone is not a capability check; the new adapter requires
the supporting engine. Keep an active run on its original engine/adapter, and
adopt the new pair for a later run. No installation or active-run migration is
part of capture. The unversioned helper remains available to older adapters.

| Touched element | Previous behavior and status after this change |
| --- | --- |
| `codexusage.TotalTokens`, shared rollout discovery/parser, terminal validation | Exact aggregate/delta and unrelated-corruption isolation remain. Discovery now also retains native metadata, terminal records and position for the new capture; legacy optional-counter/timestamp behavior remains. |
| `Completed`, `Capture`, `NativeCounters`, `SampleID` | New read-only cumulative evidence path. No parent/root capture, timing inference or requested-profile proof. Child-only attribution is shared with `TotalTokens`; additional native field/path/timestamp checks apply to `Completed`, not every legacy aggregate call. |
| CLI `runCodexSubagentUsage`, versioned request/response and `runCodexObservation` | Unversioned shape remains. Explicit v2 returns an observation or exclusive unavailable payload; unsupported versions/fields fail. Session-root lookup is reused unchanged. Capture never writes metrics or lifecycle state. |
| `Observation`, `Unavailable`, `ParseObservation`, JSON decoding and validation | Before: schema 1 allowed exactly sample/interval/outcome. After: schema 1 retains its closed shape; schema 2 adds exclusive missingness and reasoning output. Partial profiles retain their previous meaning. |
| `Counters`, `CounterContributions`, `Record`, metrics document validation | Five independent counters become six in v2; every counter, including reasoning output, follows the same presence, nonnegative, monotonic and overflow rules. Replay, immutable identities, atomic history, document version, stream boundaries and externally supplied serialization remain. Missingness contributes nothing. |
| `Usage.Counters`, `legacyUsageWire`, legacy JSON methods | Internal wire is separated from the extended native counter type. Existing lifecycle fields, explicit zeros, unknown-field rejection and duration behavior remain; reasoning counters are not accepted as legacy usage. |
| CLI `runMetrics` | Recording result remains schema 1 despite the new observation version. Current-run/issue/host validation, lock and enabled gate remain unchanged. The read-only report now presents native evidence while keeping it separate from legacy usage. |
| Codex Delivery instructions and contract test | Before/after usage mapping is explicit in the original document. New instructions require supported versions, saved associations and requests, recording before completion, honest missingness and no duplicate legacy submission. All other manual Delivery mechanics remain. |
| Metrics/CLI tests and this compatibility document | Focused deterministic checks add capture-to-recorder subprocess restart/replay, repair and fresh-reviewer identities, malformed/unavailable evidence, zero/absent counters, planning/scout recording and closed version shapes. No live host sessions or dependencies are introduced. |

No Claude/OpenCode adapter, lifecycle verb, routing `Selection`, configuration,
approval, guard, GitHub resource or active-run state format changes. This applies
to every lifecycle verb, not just `pr-open`/`review`; capture's new schema is not
an authorization or an observed execution-profile guarantee.

## Bounded native Codex sessions (#294)

The dormant internal `codexnative` session API returns schema-2 observations;
it never records them or submits legacy lifecycle usage. Its production entry
points require capability and isolation preflight, and the unresolved native
inherited-tool boundary still refuses every model turn. Ordinary tests exercise
only a private scripted host. See the [native session contract and approved
smoke procedure](codex-native-protocol.md#bounded-single-task-sessions-294).

Native source `codex-app-server` and cumulative stream
`codex-app-server-thread-total-token-usage` retain the installed camelCase
thread-total counters independently, including absent/null fields and explicit
zero. Metrics `session` is the executed native thread ID; the separate native
session-tree ID remains in the session result and does not combine child usage.
This source is distinct from session-log capture. Pick one capture source for
the task and omit legacy `usage`/`executor_usage`; adding the two sources cannot
establish a comparable total and would double-count execution.

The native notification has no event ID, sequence or timestamp. The retained
session derives identity from task/thread/turn and decoded counter presence and
values, and retains the first local receipt time and local positive sequence.
Same-object reconnect replay returns the original observation unchanged. This
is local receipt provenance, not a native event timestamp or active-agent
interval. The existing recorder validates profiles, counters, replay and
cumulative arithmetic unchanged. A fresh process cannot reconstruct this
in-memory verified binding/history and has no restore API here.

Reported thread settings remain partial configured-profile evidence, separate
from requested settings and from per-turn inference proof. No missing field,
counter, total, active time or Architect coverage is inferred. Incomplete
streams retain actual partial samples and explicit terminal missingness; failed
sessions report infrastructure-failure evidence. Successful native completion
records `native-completion-not-verification`, never review/merge approval.
Callers save and submit unchanged evidence through the existing current-run
recorder before ending the run. Recorder/storage/lifecycle behavior and every
existing capture source remain unchanged.
