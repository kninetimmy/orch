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

Exactly one payload is required:

- `sample`: native `input_tokens`, `output_tokens`, `cache_read_tokens`,
  `cache_creation_tokens`, and `total_tokens` are independent optional int64
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

## Compatibility and blast radius

Before this change, metrics schema 1 held only lifecycle events and the metrics
command had only a read-only form. Schema 2 adds a separate `observations` array;
the no-argument report remains read-only and still summarizes legacy events.
Observation reporting and native capture are separate follow-up work.

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
| `metrics.Usage`, its JSON methods and `Counters` | Before: omitted and explicit zero decoded to the same integer, and zero was omitted on write. After: existing integer fields/manual callers retain their meaning, decoded explicit zero survives rewrites, and `Counters` exposes omitted fields as unknown. Legacy usage never becomes a native sample or baseline. Existing legacy text reports retain their original display behavior. |
| `metrics.Append`, `Event`, run activation and `recordMetric` callers | Lifecycle events, manual PR/review/executor usage, event ordering, enabled gate, and post-mutation error behavior remain. Append preserves observations. Callers and their Delivery decisions are unchanged. |
| `metrics.write`, `strictDecode`, `validateRunID` | Atomic temp/sync/rename and path validation remain. Strict decoding now checks EOF explicitly. The new document validator additionally checks accepted observations and their stream arithmetic before writes and on reads. |
| `Observation`, `Profile`, `Counters`, `CounterSample`, `Interval`, `ParseObservation`, `Record`, `CounterContributions` | New evidence-only contract; partial observed profiles are intentionally distinct from routing `manifest.Selection`, whose complete-profile restriction remains unchanged for every Selection. All counter fields share presence/nonnegative/overflow rules, not just total tokens. |
| CLI `commands`, `runMetrics`, `cmdMetrics`, `withDeliveryMutation` | Before: metrics rejected every argument. After: `metrics record` adds JSON recording; other arguments still fail. `cmdMetrics` remains read-only. Every metrics storage writer (`Append` and `Record`), not just `Append`, needs external serialization; production record, lifecycle, resume, and abort commands share the existing repository boundary. Direct package callers must supply serialization themselves. |
| `state.Load`, `state.CheckConsistent`, `lockfile.Inspect`, `config.Load` | Reused read-only for current association and enabled checks. State schema, lock ownership, configuration, routing, approval, phase transitions, review verdicts, and GitHub resources retain their prior behavior. No new storage registry or dependencies. |
| Metrics and CLI tests | Existing tests remain; focused checks add process restart/replay, concurrent submissions, failure/retry, presence, legacy reads, invalid associations and disabled storage. The required local race gate is `go test -race ./internal/metrics ./internal/cli`; current CI does not run it. |
