# Codex native protocol preflight

`internal/codexnative` adds an internal, read-only capability check alongside
the manual Codex adapter. Nothing calls it from the CLI or Delivery engine yet.
It starts the installed executable with the argument vector `app-server
--listen stdio://`, an explicit absolute working directory and inherited native
authentication. It never copies credentials or changes login, persisted config,
providers, role selections, approvals or sandbox settings.

## Supported subset

The [native app-server protocol](https://learn.chatgpt.com/docs/app-server)
uses newline-delimited JSON over stdio, omitting the JSON-RPC version header.
This implementation intentionally supports only:

| Direction | Method | Meaning |
| --- | --- | --- |
| Request | `initialize` | Identify Orch by client name/version; record the native host version from the returned user agent and optional platform metadata. |
| Notification sent | `initialized` | Acknowledge the one initialization exchange. |
| Request | `account/read` | Inspect account type and `requiresOpenaiAuth`, always with `refreshToken: false`. Account emails and plan details are not retained. |
| Notification received | `account/updated` | Require explicit `authMode: "chatgpt"` evidence for managed ChatGPT authentication. External tokens, API keys, null and other modes fail. |
| Request | `model/list` | Request 100 entries per page with `includeHidden: true`; follow every cursor, including pages after a matching model. |
| Notification received | Other methods | Validate the notification envelope, then discard it without retaining its payload. It cannot satisfy a pending request. |

Only one request is outstanding at a time. String request IDs are generated
locally and must be echoed exactly. Unknown/unsolicited response IDs, server
requests, conflicting message fields, invalid JSON, missing capability fields,
unterminated lines and RPC rejection fail the check. RPC errors report the
method and numeric code without echoing server messages or diagnostics.

Before #293, the following restriction applied to every connection in the
package:

Every request method in this connection shares the closed allowlist, not just
`turn/start`. All thread/turn, command/process, login/logout, configuration
mutation and approval methods are unavailable. The only outgoing notification
is `initialized`; there is no arbitrary notification or request API exported.
The connection is private to the preflight and is closed before returning.

After #293 (before #294), that complete restriction still applies to every metadata
connection owned by `Preflight`. A separate private isolation connection adds
only `windowsSandbox/readiness`, `permissionProfile/list` and buffered
`command/exec` for no-model synthetic diagnostics. All request methods in
both kinds of connection go through the closed allowlist in `call`, not
only the named `turn/start` symbol. Every thread/turn, unsandboxed `process/*`,
login/logout, setup, configuration mutation and approval method remains
unavailable in both kinds. Neither public preflight exports a command or
connection API; the new launch overrides below are process-only.

After #294, those restrictions still apply to every metadata and diagnostic
connection. A private session connection adds only `thread/start`,
`thread/resume`, `turn/start` and `turn/interrupt` after successful capability
and isolation preflight. This removes the package-wide prohibition on those
four methods in session machinery; it does not remove the production refusal:
the isolation check still fails for every host/version and every dispatched
role. There is no public bypass, callback or test flag. Unsandboxed processes,
fork/steering, approval/tool responses and arbitrary RPCs remain unavailable
on **every** connection, including sessions.

## Bounds and evidence

The check has a 15-second deadline, further shortened by caller cancellation or
an earlier caller deadline. Reads and writes have a 1 MiB message bound.
Context cancellation closes the owned pipes and terminates the native process
so blocked I/O cannot wait on an inherited descendant pipe. Stdio shutdown
closes stdin, allows two seconds for exit, then kills and allows two seconds
for reaping. There is no invented shutdown RPC. Distinct errors identify
malformed messages, process exit, interrupted transport and deadline/shutdown
timeout; cancellation also preserves `context.Canceled`.

The entire catalog must finish within 100 pages. Empty, oversized or repeated
cursors fail closed. Both the catalog `id` and host-facing `model` must match
the requested model exactly, with one unambiguous entry. The requested effort
must appear in that entry's `supportedReasoningEfforts`. Defaults, upgrades,
aliases, alternate versions and alternate providers never recover a failure.
Hidden entries are included because picker visibility is not capability support.

`Capabilities` retains the native version when a later check fails, but sets
`Selection` only after authentication and every catalog page pass. This is
native catalog evidence; it does not prove inference entitlement, the model
actually used by a turn, filesystem containment or observed token usage. It is
not a `metrics.Observation`, and it never writes metrics or run state. A later
execution change must establish its own actual execution evidence.

## Version-sensitive differences

The reference schema and no-model diagnostics for this subset came from native
Codex 0.159.2 on Windows. Its user agent reports `Codex Desktop/0.159.2`;
`codex_cli_rs/<version>` and `codex-cli/<version>` banners are also recognized.
Before #293, these were the only recognized prefixes. After #293, the shared
`hostVersion` parser also recognizes the observed `orch/0.159.2` prefix: a
native stdio launch identifies the client as Orch and returns
`orch/0.159.2 (Windows 10.0.26200; x86_64) unknown (orch; isolation-smoke)`.
The native version is the first version, not the caller's client version.
An unrecognized/missing version or missing managed authentication notification
fails closed. The generated schema from the installed native executable is the
version-specific reference; current documentation may describe later fields.
Additional metadata fields are ignored, while required evidence is checked.

On this version, `account/read.account.type: "chatgpt"` alone does **not**
distinguish managed OAuth from experimental external tokens. The separate
`account/updated.authMode` evidence is required. The client does not opt into
experimental methods, force a token refresh or expose any login method.

Windows diagnostic results are relevant to the later isolation change, and
are not implemented or repeated by this preflight:

- `windowsSandbox/readiness` returned ready. Readiness says the helper is
  available; it does not prove the requested worker/controller boundary.
- `command/exec` with `outputBytesCap: 4000` rejected the request with
  `custom outputBytesCap is not supported with windows sandbox`. Omitting the
  field worked. This native command-output restriction is specific to Windows
  sandboxed command execution; it is not a limit on every RPC or on this
  client's own bounded stdio messages.
- The installed legacy `SandboxPolicy` has no `readOnlyAccess` field, even
  though a newer protocol description can show it. A synthetic
  `workspaceWrite` probe allowed sibling reads/writes despite exclude-temp
  flags. It therefore did not establish containment.
- A named profile with `:minimal` read access failed because the elevated
  Windows sandbox requires effective `:root` read access. A named profile
  granting `:root` reads and worker-path writes, with explicit controller-path
  denial, allowed a worker write and denied controller reads/writes in the
  synthetic probe. Those observations are not a general isolation guarantee.

The [Windows sandbox documentation](https://learn.chatgpt.com/docs/windows/windows-sandbox)
describes elevated/unelevated modes. Neither readiness, a mode name nor
successful initialization substitutes for testing the exact requested access
boundary. The preflight exposes no sandbox readiness or command execution API.

## Blast radius and compatibility

| Touched element | Before #292 | After #292; does prior behavior still hold? |
| --- | --- | --- |
| `internal/codexnative/transport.go`: `connection`, `message`, `start`, `contextError`, `exitError`, `ioError`, `send`, `read`, `call`, `initialized`, `requireManagedAuth`, `close`; `ErrMalformedMessage`, `ErrProcessExit`, `ErrTransportInterrupted`, `ErrTimeout`; `maxMessageBytes`, `preflightTimeout`, `shutdownTimeout` | No native streaming connection existed. | Adds only the private, bounded stdio metadata subset; no prior behavior is removed. All request methods share the allowlist and deadline/message bounds. |
| `internal/codexnative/preflight.go`: `Options`, `Capabilities`, `hostVersion`, `Preflight`, `inspect` | No native capability preflight existed. | Adds explicit native version/authentication/catalog evidence without execution or persistence; existing routed selections remain authoritative. |
| `internal/codexnative/preflight_test.go`: `TestMain`, `scriptedServer`, `scriptedPreflight`, `TestPreflightHandshakeInterleavingAndAllPages`, `TestPreflightFailsClosed`, `TestPreflightMalformedAndInterruptedTransport`, `TestPreflightReadWriteAndAuthDeadlines`, `TestPreflightSelectionAndExecutionBoundary` | No native protocol tests existed. | Adds deterministic no-model subprocess checks using the test binary on every CI OS. Existing tests still run unchanged. |
| This protocol document | No supported native subset or Windows diagnostic contract was documented here. | Adds the subset, limits, version caveats and touched-element accounting; no prior statement is removed. |
| `adapters/codex/README.md`: native preflight link | Documented the manual plugin, dispatch, guard and sandbox limitations. | Adds a link to the dormant internal preflight; every existing manual workflow and limitation still holds. |

No existing behavior is removed in this change. Before, the manual adapter was
the available Codex execution path; after, it remains the available execution
path, with a separate internal metadata preflight that cannot start model work.
`execx.Local` still collects single-shot output, and its callers are unchanged.
Metrics observations, all role definitions/selections, run/manifest schemas,
approval gates, guard coverage and sandbox configuration are unchanged. In
particular, the README's per-spawn override and role-read-only restrictions
still apply to every manual dispatched Codex role, not only one named agent.

## Native workspace permission contract (#293)

`IsolationPreflight` validates `IsolationPaths`, owns its native connection
within the same 15-second transport bound, and returns configuration evidence
separately from its error. It **always refuses model execution**.
`SandboxReady` and `ProfileAllowed` describe prerequisites only; neither means
containment or model-tool readiness. No execution CLI or Delivery lifecycle
caller is added.

Every workspace, scratch and protected path must be an absolute literal
path. Workspace/scratch must already exist. The code reuses `paths.Canonical`
and segment-aware containment, then on Windows resolves the final filesystem
handle with `GetFinalPathNameByHandleW`. That additional step covers junctions
and short 8.3 aliases that Go 1.26.5's `EvalSymlinks` alone did not fully
resolve in these fixtures. Missing protected targets resolve their deepest
existing ancestor; failures deny execution. Device/UNC namespaces, alternate
streams, glob characters and trailing-dot/space aliases are refused. These
extra restrictions apply to every isolation path, not to the unchanged
general-purpose `paths.Canonical` helper.

Workspace and scratch cannot overlap each other or any protected location.
Main checkout, controller state, sibling workspaces and credential locations
receive explicit denials. Native authentication homes from `CODEX_HOME`,
`HOME/.codex` and `USERPROFILE/.codex` also receive denials. The caller must
enumerate remaining credential locations; an empty credential list refuses.
Existing Orch worktrees nested under the main checkout are unsuitable for
this native contract: they are refused, never moved or reset. The trusted
controller must supply a separate approved checkout and scratch.

The parent inspects `.git` and `commondir` pointers with bounded file reads.
External metadata receives explicit denials, including the common directory
shared with the main checkout. Shared Git metadata is never a worker-access
exception. A worker using such metadata cannot commit through it; trusted
host Git operations remain outside this sandbox. Invalid or unverifiable
pointers refuse execution. No user's checkout is reset, cleaned or repaired.

Before the cleanup repair, Git-pointer file closes and Windows final-path
handle closes ignored errors; after it, either close failure refuses path
validation. Before the fixture repair, the cancellation helper's executable
lookup shadowed its final marker-write error; after it, that write failure
reaches the shared filesystem-error exit handling. This applies to every
call of these helpers, including worker and reviewer checks.

Each launch uses a fresh random profile name to avoid inherited extensions or
workspace roots being merged into it. Profiles named `orch_worker_<nonce>`
grant `:root` reads, workspace and scratch writes, and protected-path denials.
Profiles named `orch_reviewer_<nonce>` grant checkout
and `:root` reads, scratch writes, and the same protected-path denials. These
rules apply to every diagnostic command under each profile, not one named
worker/reviewer agent. Root reads are an elevated-Windows prerequisite, not
a claim of exclusive workspace reads. Protected reads are explicitly denied;
network access is disabled. The [permission-profile reference](https://learn.chatgpt.com/docs/config-file/config-reference)
describes these rules; [Windows sandbox prerequisites](https://learn.chatgpt.com/docs/windows/windows-sandbox)
must already be satisfied by the operator.

The launch pins `windows.sandbox="elevated"` and refuses unavailable,
unrecognized, unready, incompatible, missing or denied capabilities. The
validated native protocol version is 0.159.2. There is no automatic setup,
permission widening, unelevated/WSL fallback, blanket bypass flag or
unsandboxed command API. Readiness alone cannot prove effectiveness; the
tagged synthetic smoke supplies separate command-containment evidence.

The trusted server keeps only system/runtime paths and native home locations
needed to resolve existing authentication; controller credential variables
are excluded. Temp locations point at scratch. Tool environments inherit
nothing through `shell_environment_policy` and explicitly unset controller
variables and host auth/home locations, retaining only Windows runtime values
and scratch temp locations. No credentials are copied into a workspace,
child environment, fixture, report or log. Native output is bounded and
discarded; RPC errors retain only method and numeric code.

Transient flags disable apps, hooks and multi-agent features as defense in
depth. They do **not** establish a closed model-tool allowlist. The installed
0.159.2 generated `ThreadStartResponse`, `ThreadResumeResponse`,
`ThreadForkResponse` and `ThreadSettingsUpdatedNotification` schemas describe
`disabledPluginIds` as a saved list that does not yet filter plugin
capabilities. A command profile cannot prove restrictions on every inherited
connector, plugin, hook or additional agent tool. The [configuration reference](https://learn.chatgpt.com/docs/config-file/config-reference)
also distinguishes command network rules from apps/MCP/web tools. Therefore
`modelToolBoundary` returns `ErrIsolationUnavailable` for every host/version,
with a specific 0.159.2 explanation. Both worker and reviewer model execution
remain unavailable after a successful command smoke. No tool-filter mechanism
or future compatibility is invented.

## Repeatable no-model validation

Normal CI runs scripted subprocess regressions without an installed host,
credentials, network or model. They cover canonical/literal paths,
case/8.3/junction aliases, overlaps and escapes, shared Git pointers,
environment scrubbing, denied/missing/incompatible capabilities, output bounds
and failure cleanup preserving existing work. Existing transport tests still
cover caller cancellation and blocked read/write deadlines. The test binary's
fixture dispatchers are not production commands or a public batch CLI.

Run locally on native Windows with an already configured elevated sandbox and
native `codex.exe` on PATH or in its standard local desktop install location:

```sh
go test -tags=codex_live -run '^TestCodexIsolationSmoke$' -count=1 -v ./internal/...
```

CI does not run this command. Unsupported/missing hosts or platforms fail
with a validation limitation; they never skip as a successful proof. Confirm
`=== RUN   TestCodexIsolationSmoke`, its `PASS`, and the parent-verification
count. The test asserts 46 native commands actually ran. Every read/write
probe targets task-owned synthetic fixtures; real credential homes are denied
by configuration but never read by the probe. Parent verification checks
allowed files, unchanged protected sentinels and absent forbidden outputs
after both owned app-servers close.

The smoke proves worker writes; reviewer checkout reads and denied existing/new
writes; scratch access; denied main/controller/sibling/synthetic credential/
shared-Git reads and writes; junction escapes; and a write to an **unlisted**
sibling using `..`. The unlisted sibling verifies the default write restriction
independently of explicit denials. Synthetic controller/API-key values check
environment removal.

Observed on 2026-09-30: native Windows amd64, OS 10.0.26200, Go 1.26.5,
Codex 0.159.2, elevated sandbox already ready. Native `command/exec` streaming
was rejected with RPC -32600; generated schema fields do not prove Windows
implementation support. The earlier custom `outputBytesCap` rejection above
still holds. Buffered diagnostics omit unsupported capture/stream/process-id
fields, preserve the client's 1 MiB message bound and never disable native
timeout/capture limits.

With `timeoutMs: 1000`, native cancellation returned RPC -32603 within the
five-second assertion bound. Parent-visible markers prove the payload and a
spawned descendant actually started. After waiting beyond the descendant's
five-second delayed write, neither delayed marker existed. This proves the
synthetic native timeout behavior, not `command/exec/terminate`, streamed
process control or live turn interruption. Missing timeout behavior or a
surviving descendant fails validation.

No live inference, model-tool calls, model identity/effort observations or token
usage are validated here. Those remain #294 work and cannot proceed through
this boundary until native tool enforcement is supported and verified.
Authentication/catalog evidence remains the separate metadata preflight;
neither that evidence nor synthetic command proof establishes entitlement.

## #293 blast radius and compatibility

The #292 accounting above is historical and remains intact. Every element
touched by #293 is accounted for below.

| Touched element | Before #293 | After #293; does prior behavior still hold? |
| --- | --- | --- |
| `transport.go`: package comment, `connection.isolation`, `start`, new `startWithEnv` | Every connection inherited its environment and had the same metadata-only allowlist. | Metadata `start` preserves that behavior. Private isolation launches use exact scrubbed environments and a diagnostic allowlist. The package-wide restriction is narrowed in the before/after in Supported subset; public `Preflight` remains nonexecuting. |
| `transport.go`: `call`, new `rpcRejection`, `rpcRejection.Error` | Sequential requests, matching IDs and redacted method/code RPC errors. | Yes, for all requests. Shared checking adds no multiplexing, arbitrary method API or unbounded I/O. Private numeric error typing distinguishes synthetic native rejection from transport failure. |
| `preflight.go`: `hostVersion` | Desktop/CLI banners recognized; others rejected. | Existing prefixes and unknown-banner refusal remain. Only rejection of the observed `orch/<native-version>` prefix is removed; the before/after is in Version-sensitive differences. Auth/catalog evidence and deadlines remain unchanged. |
| `preflight_test.go`: `TestMain`, `scriptedServer`, `TestPreflightHandshakeInterleavingAndAllPages`, `TestPreflightSelectionAndExecutionBoundary` | Scripted metadata test server, version checks, metadata execution refusal. | Yes. Adds test-only isolation/fixture dispatch, the Orch banner regression and metadata refusal of readiness/profile/command/unsandboxed methods. Existing cancellation scenarios remain. |
| New `isolation.go`: `ErrIsolationUnavailable`, `IsolationPaths`, `IsolationCapabilities`, `isolationBoundary`, `IsolationPreflight`, `modelToolBoundary` | No native workspace contract/readiness API. | Adds a dormant, nonexecuting preflight with model refusal for both roles and all hosts. No prior execution workflow changes. |
| New `isolation.go`: `isolationPath`, `overlap`, `prepareIsolation`, `readGitPointer`, `sharedGitPaths`, `isolationBoundary.profile`, `quoteTOML`, `isolationBoundary.args` | No native layout/profile checks. | Adds canonical layout and shared-Git checks plus transient profiles. Existing checkouts/configuration are preserved and general path helpers reused. |
| New `isolation_path_windows.go`: `finalPathName`, `finalIsolationPath`; new `isolation_path_other.go`: `finalIsolationPath` | No final-handle normalization here. | Adds Windows junction/8.3 resolution for every isolation path. Other platforms retain canonical processing but cannot pass native Windows validation. General path helpers are unchanged. |
| New `isolation.go`: `serverEnvironment`, `isolationBoundary.commandEnvironment`, `openIsolation`, `connection.diagnosticCommand` | No native sandbox command connection. | Adds private no-model diagnostics with scrubbed credentials, elevated profiles, capability refusals, bounded buffered commands and owned cleanup. CLI/Delivery do not call it; no public execution API. |
| New `isolation_test.go`: `isolationLayout`, `configOverrides`, `linkIsolationDirectory`, `scriptedIsolationServer`, `isolationFixture`; `TestIsolationPathsAndProfiles`, `TestIsolationRejectsUnsafeLayouts`, `TestIsolationPathAliasesAndSharedGit`, `TestIsolationEnvironmentsAndModelRefusal`, `TestIsolationCapabilitiesAndFailureCleanup`, `TestIsolationFixtureCancellationWriteError` | No isolation regressions. | Adds focused CI checks and task-owned subprocess/descendant fixtures. Existing tests remain; no dependency or framework added. |
| New `isolation_live_test.go`: `TestCodexIsolationSmoke` | No repeatable native command proof. | Adds opt-in 46-command synthetic validation, parent verification and unsupported-platform failure. No model work or automatic setup. |
| This document and `adapters/codex/README.md` native-check link | Metadata-only package contract and manual workflow limitations. | Adds isolation contract, native limitations, validation and before/after scope. Every manual role/guard limitation still holds; native proof does not upgrade manual roles. |

`execx.Local`, CLI/run lifecycle, metrics/schema, manifests, approvals,
role/profile selections, configuration files, installed plugins/hooks and host
setup are untouched. The manual adapter remains the available execution path;
its instruction-based reviewer restriction still applies to every manual
reviewer role, not just `orch-reviewer`. The native reviewer profile's command
read-only behavior is a separate validated boundary.

## Bounded single-task sessions (#294)

`RunSession` represents one caller-approved `Task`, using its task ID, exact
prompt, run/issue attribution, role, canonical complete layout and routed
`manifest.Selection`. It does not approve tasks, select models or evaluate
issue eligibility. A future trusted caller must obtain and revalidate those
decisions from the engine before calling it or `Session.Resume`. There is no
CLI, evaluation/batch driver, unattended lifecycle caller or automatic merge.

Canonical developer instructions come from `agents.CodexInstructions`, which
decodes the existing shipped Codex role definition, without copying its default
model/effort into the requested selection. All five dispatched roles are
supported; `review_downgrade` keeps its exact role binding and records metrics
as reviewer. Architect has no dispatched definition and is refused. This is
true for every canonical role lookup, not just the specialist symbol. Scout
and both reviewer roles use the existing read-only native profile.

Capability and isolation checks must pass before any thread or turn request.
The actual isolated connection also rechecks authentication/catalog and its
model-tool boundary. `IsolationPreflight` still **always refuses model
execution**, including 0.159.2 after a successful command smoke. Only the
private test-binary seam exercises session requests. No source-delivery test
here establishes P1-B model readiness or a verified inherited-tool boundary.

When that boundary becomes available through a separately approved change,
the session machinery pins canonical cwd, fresh native permission profile,
`approvalPolicy: "never"`, requested model/effort and canonical role prose.
Provider model fallback is disabled. It accepts no caller-supplied dynamic
tools, capability roots, steering, fork, approval or replacement task input.
Server requests fail closed. Additional native threads/turns, delegation,
unsupported tool items, changed task input, tool paths outside workspace/scratch
and file changes by a read-only role stop progression. Every native item shares
these restrictions; none is an exception because its tool has another name.
These protocol checks do not detect arbitrary semantic drift inside prose or
make instruction-based tool restrictions into native enforcement. That is why
the unresolved inherited-tool boundary remains a production refusal.

`SessionResult` retains native thread ID, session-tree ID and turn ID separately.
The metrics session is the executed **thread** ID; the native tree ID can be
shared with other threads and is not a usage aggregation key. Requested
settings stay separate from reported model/effort. Thread responses and
`thread/settings/updated` report configured profiles, not per-turn inference
telemetry, as the installed schema explicitly says. Missing fields remain
unknown. Any present mismatch or `model/rerouted` event stops work, even if the
reroute names the requested model again. No catalog or request field fills an
observed field, and no reported setting is claimed as inference proof.

A caller context must have a finite execution deadline. Cancellation/deadline
attempts native `turn/interrupt` for the identified turn, waits at most two
seconds for acknowledgement/completion, then uses the existing bounded stdio
shutdown. The host context has only that bounded cleanup grace; it cannot
extend task execution. Unknown turns cannot be addressed or resubmitted
safely. Failure, successful completion, cancellation, timeout and disconnect
remain distinct `SessionOutcome` values. A successful native turn is neither
implementation verification nor review/merge approval. Cleanup never resets,
cleans, commits or repairs a checkout, and existing dirty work survives.

`Session.Resume` accepts only a disconnected, identified, unfinished turn on
the same retained session object. It canonicalizes and compares the entire
approved binding, re-runs both preflights and resumes by native thread ID,
never by a caller-supplied rollout path or history. The native tree/thread IDs,
profile, workspace and complete single-turn history must agree. An already
completed turn is consumed as completion, without a second `turn/start`; an
in-progress turn is observed until completion. Changed identities, additional
turns, incomplete paginated history and unknown submitted turn IDs refuse.
Cancelled/timed-out/failed sessions cannot be resumed through this API.

The session keeps its checkpoint and immutable observation history in memory;
`Result` returns independent snapshots. It adds no persistence subsystem or
process-restart restore API. Losing this retained object also loses the
verified binding/replay history: a fresh process must not reconstruct it from
untrusted native history or replay the prompt. Each retained session admits
at most 1,024 notifications/observations and 1 MiB of final output in addition
to the existing per-message bound. Calls are sequential; callers interrupt via
their context, not concurrent session mutation.

## Native observation semantics

`thread/tokenUsage/updated.tokenUsage.total` is a cumulative thread stream.
The installed camelCase fields map independently to the existing schema-2
observation counters: `inputTokens`, `outputTokens`, `cachedInputTokens`,
`cacheWriteInputTokens`, `totalTokens` and `reasoningOutputTokens`. Null/absent
fields remain unknown, including the schema-defaulted cache-write field. No
sum, default or `last` sample substitutes for an absent total field.

Source is `codex-app-server` and stream is
`codex-app-server-thread-total-token-usage`, deliberately distinct from the
existing `codex-session-log` / `codex-total-token-usage` pair. The producer
cannot establish that different capture sources have interchangeable counter
definitions. Choose one capture source for a task; never add both captures or
legacy `usage`/`executor_usage` for this native execution.

Native notifications contain no event ID, sequence or timestamp. Identity is
a SHA-256 key of the retained task/thread/turn and decoded total-counter
presence/values. Identical events in the same turn retain the original
observation, first receipt timestamp and positive local sequence. New totals
advance that sequence; the existing `metrics.CounterContributions` validation
rejects regressing counters, overflow or invalid evidence. Receipt time is
labelled as local evidence time, never native time or active-agent duration.
No intervals, inferred missing counters or Architect/root coverage are created.

Incomplete streams retain their actual partial counters and a terminal
missingness record. Failed sessions produce infrastructure-failure evidence;
cancelled/timed-out/disconnected sessions produce explicit missingness, and
successful completion produces `native-completion-not-verification`, never
`approval`. Events with no counters produce `native-counters-unavailable`.
The session returns evidence without recording it. A future trusted caller
saves and submits the unchanged observations through the existing recorder,
under its current-run association/serialization boundary, before ending the
run. Exact recorder retries and same-object stream replay count once.

## Separately approved live model/tool smoke

This batch executes **no native model trial**. Ordinary tests use the scripted
test binary for start/completion, mismatch/reroute, interruption, disconnect,
resume, missing counters, duplicate/replayed events, dirty-work preservation
and capture-to-recorder replay. The tagged `TestCodexIsolationSmoke` remains
the independent 46-command, no-model containment proof described above.

Before any model/tool smoke, obtain separate approval for the exact one-task
native trial and finite cutoff. First establish a supported native closed
inherited-tool boundary, update its refusal only through an approved reviewed
change, and repeat command containment on that exact host/profile. Approval
alone cannot bypass today's `ErrIsolationUnavailable`; until that work is
verified, this procedure stops before any turn.

The approved caller must supply a separate checkout/scratch outside protected
main/controller/sibling/credential/shared-Git locations, keep the exact routed
profile and canonical role, and revalidate engine eligibility. Use existing
managed ChatGPT subscription authentication; no login/token RPC, API-key
fallback, paid alternative or role-default change is part of this procedure.
Start one task that performs one bounded tool action on task-owned fixtures,
with parent checks of the expected result and unchanged protected sentinels.
Reviewer trials must additionally show denied checkout writes. Do not probe
actual credential contents.

Retain native host/version, task/workspace/profile binding, thread/tree/turn
identities, reported settings with their configured-profile limitation, any
independent per-turn identity evidence, native counters with presence and
local timestamp provenance, interrupt acknowledgement/terminal status, bounded
shutdown and parent-visible descendant/dirty-work evidence. Separately approved
disconnect/resume evidence must show the same turn is observed rather than
replayed, with unchanged original observations and monotonic cumulative deltas.
Any mismatch/reroute, task expansion, permission request, incomplete identity
or isolation failure stops the trial. Record remaining unknown counters,
Architect coverage and active time as unavailable, without estimates.

The installed turn schemas support interruption, but ordinary scripted proof
does not validate native Windows model interruption or descendants. The
existing native command timeout proof is not turn-interruption proof. Live
inference entitlement, per-turn model/effort identity, inherited tool closure,
native usage delivery/replay and interruption behavior remain evidence gaps.
Neither smoke completion nor source merge authorizes unattended execution,
further tasks, lifecycle progression or automatic merge.

## #294 blast radius and compatibility

The #292/#293 statements above remain as historical before-and-after evidence.
Every touched structure is named below. This changes no engine policy, schema,
host installation or role default.

| Touched element | Before #294 | After #294; does prior behavior still hold? |
| --- | --- | --- |
| `agents.go`: new `CodexInstructions`; existing embedded definitions/`roleFiles` reused; `agents_test.go`: `TestCodexInstructionsUseCanonicalProse` | Canonical definitions rendered only as host files; Architect had no dispatched definition. | Adds read-only role-prose extraction for all five definitions. Rendering/defaults remain; Architect still has no definition. |
| `transport.go`: package comment, `connection.session`/`.profile`, new `request`, `decodeResponse`; changed `call` | Every connection denied all thread/turn methods; sequential requests had closed metadata/diagnostic allowlists and redacted errors. | Metadata/diagnostic behavior remains. Only a privately gated session admits the four named methods; this removed package-wide restriction is recorded in Supported subset. Shared decoding, request IDs, message bounds, server-request refusal and bounded shutdown remain for every connection. |
| `preflight.go`: `inspect`, new `inspectCatalog` | Initialization and managed-auth/all-page catalog checking lived in `inspect`. | Same public metadata preflight and evidence. The extracted checker additionally revalidates an actual isolated connection; no new auth methods. |
| `isolation.go`: `openIsolation`, `diagnosticCommand` comment | Fresh pinned profiles, scrubbed environments, private no-model command diagnostics and unconditional model refusal. | All remain. The connection additionally retains its own profile ID for session-response checks; no profile widening or diagnostic execution change. The old all-connection turn prohibition becomes the gated session exception documented above. |
| New `session.go`: `Task`, `SessionResult`, `Session`, `sessionMessage`, `SessionOutcome`, `SessionSuccessful`, `SessionFailed`, `SessionCancelled`, `SessionTimedOut`, `SessionDisconnected`, `ErrProfileMismatch`, `ErrTaskBoundary`, `maxSessionEvents` | No bounded native task representation/checkpoint. | Adds one in-memory, caller-approved task and distinct results/evidence; no storage, policy engine, driver or prior workflow removed. |
| New `session.go`: `RunSession`, `Resume`, `Result`, `sessionDeadline`, `bindTask`, `newSession`, `metricRole`, `checkResume`, `connect`, `sessionContext` | No native single-task caller API. | Adds canonical immutable binding, mandatory deadlines, snapshot copying and revalidation. Every production entry still refuses the unsupported isolation boundary; no public bypass. |
| New `session.go`: `nativeTurn`, `nativeSettings`, `nativeThreadResponse`, `execute`, `call`, `receive`, `settings`, `acceptThread`, `acceptTurn`, `notification` | No thread/turn exchange or reported execution settings here. | Adds the closed private session subset with native identity, partial configured-profile evidence and mismatch/reroute refusal. Every new turn still requires the same gate; requested settings never become observed settings. |
| New `session.go`: `item`, `nativeIdentity`, `taskPath`, `interrupt`, `finish` | No native session item/control/result handling. | Adds input/tool/path/delegation restrictions, native interruption and distinct terminal states. All items/roles share the boundary; every scout/reviewer role rejects file changes. Existing work is never cleaned/reset. |
| New `session_observation.go`: `nativeCounters`, `observation`, `tokenUsage`, `appendObservation`, `terminalObservation` | Existing session-log capture and generic schema-2 metrics contract. | Adds a distinct, presence-preserving native stream with same-object replay identity and local receipt time. Existing recorder/storage/legacy behavior remains, no usage submission or inferred timing/root coverage. |
| `preflight_test.go`: `TestMain`; new `session_test.go`: `scriptedSessionConnection`, `sessionTask`, `scriptedSessionServer`, `TestSessionScriptedLifecycle`, `TestSessionNativeInterruptionAndBounds`, `TestSessionDisconnectResumeReplay`, `TestSessionProductionGateAndBinding`, `TestSessionUnknownTurnCannotResume`, `TestSessionReadOnlyRolesAndRetentionBounds` | Scripted metadata/isolation and independent tagged native command smoke. | Existing checks remain. Adds ordinary no-model session exchanges and a private test-only seam; production refuses even the scripted host through its public entry points. |
| This document; `docs/metric-observations.md` native-session appendix | Documented metadata/diagnostic limits and session-log observation semantics. | Keeps the old restriction and records its exact narrower session exception, new evidence semantics, bounded approved smoke procedure and remaining gaps. Every manual adapter limitation remains. |

`execx.Local`, all of its single-shot callers, metrics recorder/schema/storage,
run/manifest state, approvals, routing, roles, host/plugin setup and manual
Codex/Claude/OpenCode execution remain unchanged. The existing manual adapter
is still the available production Codex execution path. Its instruction-based
reviewer restriction applies to every manual reviewer role, and session source
delivery does not improve that manual restriction or claim model readiness.
