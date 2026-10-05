# Codex native protocol preflight

Current behavior after #322: native Windows amd64 Codex 0.160.0 can pass
model-tool admission only with the reviewed enforcement evidence and all
actual-connection controls described in the final section. `RunSession` and
same-object `Session.Resume` remain caller-bound and fail closed. Evaluation
production attempts still refuse until #323. No subscription trial was run by
this source change; configured identity remains distinct from inference evidence.

**Retained baseline (#292–#321).** Statements below about unconditional model
refusal describe the behavior before #322. Their replacements, every caller and
the exact touched-element accounting are recorded in the #322 section below.

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

After #298, the private read-only discovery and diagnostic subset additionally
admits `config/read` with the approved `cwd` and `includeLayers: false`, and
`experimentalFeature/list` for loaded feature enablement. Metadata connections
owned by `Preflight` still deny both. Discovery cannot execute commands;
`command/exec` is admitted only after the actual diagnostic connection verifies
its effective restrictions, elevated readiness and selected profile. Every
thread/turn, unsandboxed process, approval, MCP/app call and configuration-write
restriction above still applies to these connections, not just `turn/start`.

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
unrecognized, unready, incompatible, missing or denied capabilities. Before
#298, the validated native protocol version was 0.159.2 and `openIsolation`
accepted only that exact version; updating to 0.160.0 refused diagnostics
before checking capabilities. After #298, the version is evidence only:
every parseable version must supply the required RPCs, interpretable fields,
effective restrictions, elevated readiness and an allowed profile. Unfamiliar
versions with equivalent capabilities pass diagnostics, while familiar versions
missing a requirement fail with that requirement. No version-specific exclusion
is implemented or future release claimed as validated. There is no automatic setup,
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

Before #298, transient flags disabled apps, hooks and multi-agent features as
defense in depth. After #298, every diagnostic launch sets process-only
`features.apps`, `plugins`, `remote_plugin`, `hooks`, `multi_agent`,
`multi_agent_v2`, `browser_use`, `browser_use_external`, `computer_use`,
`code_mode_host`, `workspace_dependencies` and `skill_mcp_dependency_install`
to false; `web_search` is disabled and `approval_policy` is `never`.
The feature names after `features.apps` share the same `features.` prefix.
These settings do **not** establish a closed model-tool allowlist. The installed
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

MCP tables merge inherited user/project entries: `mcp_servers={}` does not
disable them. A bounded read-only discovery process obtains all configured
server names at the approved cwd and closes without commands. A fresh child
explicitly sets every discovered server's `enabled=false` using quoted TOML
table keys, including names with dots, quotes or Unicode. There is no server
name allowlist. Missing/malformed server tables, more than 256 entries, invalid
names, missing disable evidence or a changed server set refuse diagnostics.
Names and server credentials are not retained in returned capability evidence.

On the actual connection, `config/read` verifies all required feature disables,
disabled web search, noninteractive approval policy, elevated sandbox, exact
default profile, empty command environment and the complete approved filesystem
and network rules. Inherited profile extensions or workspace roots refuse.
The normalized `multi_agent_v2.enabled=false` object and filesystem scan
metadata are accepted; unrelated additive metadata is ignored. The separate
read-only `experimentalFeature/list` must report every required feature as
unambiguously disabled, across at most 16 pages of 100 entries with bounded,
nonrepeating cursors. A raw config echo or successful initialization alone does
not demonstrate control support. Policy conflicts or unsupported RPCs/controls
return a specific limitation without fallback. All calls retain the caller's
deadline and 1 MiB message bound; the discovery process additionally has a
15-second deadline. Public isolation preflight keeps its overall 15-second bound.

`IsolationCapabilities.ToolsDisabled` and `DisabledMCP` are configuration
observations, alongside host version, readiness and profile availability.
None is containment, inferred model-tool enforcement, entitlement or metrics
evidence. No raw configuration, origins, credentials, account data or native
diagnostics are logged. Persisted settings, installations and authentication
are untouched. The [managed-configuration reference](https://learn.chatgpt.com/docs/enterprise/managed-configuration#configure-network-access-requirements)
also describes why command network restrictions do not cover other surfaces.

## Repeatable no-model validation

Normal CI runs scripted subprocess regressions without an installed host,
credentials, network or model. They cover canonical/literal paths,
case/8.3/junction aliases, overlaps and escapes, shared Git pointers,
environment scrubbing, denied/missing/incompatible capabilities, output bounds
and failure cleanup preserving existing work. Existing transport tests still
cover caller cancellation and blocked read/write deadlines. The test binary's
fixture dispatchers are not production commands or a public batch CLI.

After #298, scripted cases also cover equivalent capabilities with only the
parseable version changed (0.159.2, 0.160.0 and a synthetic unfamiliar version),
missing RPCs on both familiar and unfamiliar versions, inherited MCP merging
and arbitrary names, denied/missing/malformed controls, conflicting feature
enablement, pagination and harmless additive fields. These fixtures do not
validate an actual future release. Every production start and resume role is
separately checked for unchanged model refusal and checkpoint preservation.

Run locally on native Windows with an already configured elevated sandbox and
native `codex.exe` on PATH or in its standard local desktop install location:

```sh
go test -tags=codex_live -run '^TestCodexIsolationSmoke$' -count=1 -v ./internal/codexnative
```

Before #298 this recipe targeted `./internal/...`; after #298 it targets the
exact `./internal/codexnative` package. CI does not run this command. Unsupported/missing hosts or platforms fail
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

Repeated on 2026-10-02 for #298: native Windows amd64, OS 10.0.26300,
Go 1.26.5, installed Codex 0.160.0 and an already ready elevated sandbox.
`TestCodexIsolationSmoke` passed in 43.75 seconds and the parent verified all
46 synthetic commands. Both worker and reviewer connections reported effective
tool restrictions and three dynamically discovered configured MCP servers
disabled; these are configuration observations, reported separately from the
executed containment checks. Worker writes, reviewer read-only checkout,
scratch writes, protected sentinels, shared Git metadata, junction/`..` escapes,
scrubbed environment and timeout/descendant cleanup all passed. Timeout RPC
-32603 arrived in 1.73/1.67 seconds; neither delayed payload nor descendant
marker existed after the parent waited beyond five seconds. No model turns ran.
The 0.159.2 observations above remain historical evidence, not proof for 0.160.0.

The generated 0.160.0 thread start/resume schemas still say `disabledPluginIds`
does not filter plugin capabilities. Configuration observations and synthetic
commands cannot establish model-tool closure; `modelToolBoundary` therefore
continues to refuse every version. Task 210 remains open. Closing it requires
separately approved evidence of native model-tool enforcement on the actual
host/profile, an approved reviewed change to the refusal and the finite one-task
model/tool smoke described below. Native identity/effort, entitlement, usage,
turn interruption and disconnect/resume remain unverified by this diagnostic.

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

Before repair-1, `thread/started` retained only identity/parent fields and
discarded its optional model/effort reports. After repair-1, its Thread payload
shares `nativeThread`/`nativeSettings` with start/resume responses and reaches
the same `settings` validation. Notification-only mismatches stop before a
turn; matching notification-only evidence remains observed. This check applies
to every supported thread/profile carrier, not only one response symbol.

A caller context must have a finite execution deadline. Cancellation/deadline
attempts native `turn/interrupt` for the identified turn, waits at most two
seconds for acknowledgement/completion, then uses the existing bounded stdio
shutdown. The host context has only that bounded cleanup grace; it cannot
extend task execution. Unknown turns cannot be addressed or resubmitted
safely. Failure, successful completion, cancellation, timeout and disconnect
remain distinct `SessionOutcome` values. A successful native turn is neither
implementation verification nor review/merge approval. Cleanup never resets,
cleans, commits or repairs a checkout, and existing dirty work survives.

Before repair-1, execution and cleanup errors were joined before outcome
classification, so an interruption-cleanup deadline could overwrite explicit
execution cancellation as timed-out. After repair-1, `finish` classifies the
primary execution result and returns all cleanup failures alongside it.
Cleanup errors name their step; actual execution deadlines still
classify as timed-out. The existing interrupt/shutdown bounds remain.

Before repair-1, the cancellation fixture acted on server receipt of
`turn/start`, before the client necessarily knew its native turn ID. After
repair-1, its private `Session.turnReady` channel signals only after
`acceptTurn` validates and retains an identified turn. The channel is unset in
production and changes no admission gate. No polling, larger sleep or wider
execution deadline substitutes for that synchronization; unknown-turn
interruption/resume refusal remains.

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
| New `session.go`: `nativeTurn`, `nativeSettings`, `nativeThread`, `nativeThreadResponse`, `execute`, `call`, `receive`, `settings`, `acceptThread`, `acceptTurn`, `notification`; private test-only `Session.turnReady` | No thread/turn exchange or reported execution settings here. | Adds the closed private session subset with native identity, partial configured-profile evidence and mismatch/reroute refusal. Repair-1 shares validation across supported profile carriers and synchronizes identified-turn test cancellation. Every new turn still requires the same gate; requested settings never become observed settings. |
| New `session.go`: `item`, `nativeIdentity`, `taskPath`, `interrupt`, `finish` | No native session item/control/result handling. | Adds input/tool/path/delegation restrictions, native interruption and distinct terminal states. Repair-1 preserves the primary execution classification while returning cleanup failures. All items/roles share the boundary; every scout/reviewer role rejects file changes. Existing work is never cleaned/reset. |
| New `session_observation.go`: `nativeCounters`, `observation`, `tokenUsage`, `appendObservation`, `terminalObservation` | Existing session-log capture and generic schema-2 metrics contract. | Adds a distinct, presence-preserving native stream with same-object replay identity and local receipt time. Existing recorder/storage/legacy behavior remains, no usage submission or inferred timing/root coverage. |
| `preflight_test.go`: `TestMain`; new `session_test.go`: `scriptedSessionConnection`, `sessionTask`, `scriptedSessionServer`, `TestSessionScriptedLifecycle`, `TestSessionThreadStartedProfile`, `TestSessionNativeInterruptionAndBounds`, `TestSessionDisconnectResumeReplay`, `TestSessionProductionGateAndBinding`, `TestSessionUnknownTurnCannotResume`, `TestSessionReadOnlyRolesAndRetentionBounds` | Scripted metadata/isolation and independent tagged native command smoke. | Existing checks remain. Adds ordinary no-model session exchanges and a private test-only seam; repair-1 covers notification-only profile reports, cancellation with stalled cleanup and deterministic client identification. Production refuses even the scripted host through its public entry points. |
| This document; `docs/metric-observations.md` native-session appendix | Documented metadata/diagnostic limits and session-log observation semantics. | Keeps the old restriction and records its exact narrower session exception, new evidence semantics, bounded approved smoke procedure and remaining gaps. Every manual adapter limitation remains. |

`execx.Local`, all of its single-shot callers, metrics recorder/schema/storage,
run/manifest state, approvals, routing, roles, host/plugin setup and manual
Codex/Claude/OpenCode execution remain unchanged. The existing manual adapter
is still the available production Codex execution path. Its instruction-based
reviewer restriction applies to every manual reviewer role, and session source
delivery does not improve that manual restriction or claim model readiness.

## #298 blast radius and compatibility

Historical #292/#293/#294 statements above remain as before-and-after evidence.
The following accounts for every element touched by #298; no lifecycle, routing,
model/effort catalog, managed-subscription authentication or Assist guard changes
are made.

| Touched element | Before #298 | After #298; does prior behavior still hold? |
| --- | --- | --- |
| `isolation.go`: `IsolationCapabilities` | Host version, sandbox readiness and profile availability described prerequisites. | Keeps those fields and adds `ToolsDisabled` and an MCP count as configuration observations only; no containment or model readiness claim. |
| `isolation.go`: `isolationBoundary.args` | Fresh workspace/scratch/protected-path profile, elevated sandbox, scrubbed command environment and three feature disables. | Keeps the entire filesystem/network/environment contract. Adds all required surface disables, disabled web search and noninteractive approval; every diagnostic child uses the same builder. The old narrower flag set is recorded above. |
| `isolation.go`: `openIsolation`, new `startIsolation` | Exact 0.159.2 eligibility, then readiness/profile checks on one connection. | Removes only the exact-version gate, recorded above; parseable host-version evidence remains. Adds bounded no-command discovery, explicit dynamic MCP disables and actual-connection configuration/feature verification before readiness/profile acceptance. Preserves cwd/path checks, owned cleanup and public unconditional model refusal. |
| `isolation.go`: `connection.diagnosticCommand` | Private buffered synthetic commands pinned cwd/profile/environment/timeout. | Keeps those settings and output discard. Requires the connection's verified boundary before submitting any command; applies to every diagnostic call and both roles. |
| New `isolation_config.go`: `restrictedFeatures`, `isolationConfig`, `restrictionError`, `readIsolationConfig`, `isolationConfig.serverNames`, `disableMCP`, `isolationConfig.verify`, `verifyRestrictedFeatures` | No effective inherited-tool verification existed. | Adds only bounded, redacted read-only checking and quoted process-only overrides. Unknown server names are discovered; missing/denied/conflicting requirements fail while harmless additive fields pass. No config-write or tool-call API is added. |
| `transport.go`: `connection.diagnosticReady`, `connection.request` | Isolation connections could submit diagnostic commands after initialization; metadata denylist and session gates already existed. | Removes that early command admission: discovery/unchecked connections cannot submit commands. Adds only private isolation `config/read` and `experimentalFeature/list`; metadata denial, every thread/turn production gate, unsandboxed process/approval/MCP/app refusal, redacted errors and shutdown bounds remain for all connections. |
| `isolation_test.go`: `scriptedIsolationServer`, `TestIsolationEnvironmentsAndModelRefusal`, `TestIsolationCapabilitiesAndFailureCleanup` | Scripted exact-version failure, readiness/profile/output bounds, environment scrubbing and model refusal. | Replaces exact-version fixture rejection with capability compatibility and specific missing-RPC checks. Adds inherited table merging, arbitrary MCP keys, normalized controls, denied/missing/malformed/conflicting restrictions and feature pagination. Environment, output, existing-work preservation, cleanup and model refusal checks remain. |
| `preflight_test.go`: `TestPreflightSelectionAndExecutionBoundary` | Metadata connections denied execution and isolation RPCs. | Same behavior; now explicitly checks denial of the two new private read-only RPCs. Exact routed model/effort and managed-auth checks are untouched. |
| `session_test.go`: new `TestSessionProductionRefusalEveryRole` | Production start refusal was tested for the specialist; shared production code also guarded every role/resume. | Adds public start/resume refusal checks for all five roles without a new execution seam. No thread/turn submission; disconnected checkpoints remain unchanged. Production session code is untouched. |
| `isolation_live_test.go`: `TestCodexIsolationSmoke` | Opt-in 46-command native synthetic containment proof, readiness/profile logs and parent cleanup checks. | Keeps all 46 probes, sentinels, environment and descendant checks. Additionally verifies and separately reports effective tool configuration and MCP count on the observed host. No model trial or new public driver. |
| This protocol document | Recorded 0.159.2 exact eligibility, three defense-in-depth flags, historical native command evidence and model refusal. | Keeps and labels those historical restrictions, records their precise replacements, 0.160.0 configuration/containment evidence, bounded discovery limits and remaining task-210 approval/evidence requirements. |

All unlisted path helpers, credential separation, shared-Git protection,
`modelToolBoundary`, session start/resume implementation, role definitions,
engine/CLI/manual adapters, metrics/schema/storage, manifests, configuration
files, plugin installation, authentication and security policy remain unchanged.
Command restrictions apply to every diagnostic command; metadata and diagnostic
thread/turn restrictions apply to every connection of those kinds. The manual
Assist shell-write loophole remains outside this issue.

## Native tool replay investigation (#320)

Before the bounded follow-up, this increment was **not closed model-tool
isolation evidence**: `TestCodexModelToolIsolationSmoke` ended with an explicit
hook/plugin limitation, and the WIP harness had not been rerun after removing
the unsafe hook source attempt. The follow-up replaces that unconditional
failure with required native source, inventory and dispatch checks described
below. One bounded attempt stopped on an
output-parser mismatch before reviewer dispatch. After the exact parser
correction and code freeze, final native verification passed for both profiles
in 94.52s; the separate 46-command check passed in 56.31s. Missing or ambiguous
evidence still fails explicitly. Production
`RunSession`, `Session.Resume`, `IsolationPreflight` and evaluation execution
still refuse; replay evidence cannot authorize a live trial or a baseline.

The exact native check is:

```text
go test -tags=codex_live -run '^TestCodexModelToolIsolationSmoke$' -count=1 -v ./internal/codexnative
```

Development used installed Windows amd64 `codex-cli 0.160.0`, Go 1.26.5,
and official `openai/codex` source tag `rust-v0.160.0` (inspected ref object
`79b1b666f2e8551f8abbbca34957227f67f3f553`). No downloaded executable ran.
The source for scripted Responses events is
[the native test helper](https://github.com/openai/codex/blob/rust-v0.160.0/codex-rs/core/tests/common/responses.rs).
The actual provider is a task-owned loopback HTTP server replaying those events.
The test's `gpt-5.5` request string and zero usage fields are synthetic protocol
inputs, not model identity, entitlement, inference, routed-selection or usage
observations. No subscription inference ran.

Every replay child launches the installed executable as `app-server --listen
stdio://`. `isolationBoundary.args` supplies the fresh worker/reviewer profile,
elevated mode, protected paths, network disable, approval `never`, disabled web
search and existing feature restrictions. Test-only `replayArgs` adds these
process overrides; no production option exposes them:

```text
cli_auth_credentials_store="ephemeral"
chatgpt_base_url="<owned loopback endpoint>"
openai_base_url="<owned loopback endpoint>/v1"
model_provider="orch_replay"
model="gpt-5.5"
model_providers.orch_replay={name="Synthetic loopback replay",base_url="<owned loopback endpoint>/v1",wire_api="responses",requires_openai_auth=false,http_headers={Authorization="Bearer orch-synthetic-replay"},request_max_retries=0,stream_max_retries=0}
features.enable_request_compression=false
features.responses_websockets=false
features.responses_websockets_v2=false
features.goals=false
features.request_permissions_tool=false
features.exec_permission_approvals=false
features.image_generation=false
ephemeral=true
log_dir="<scratch>/native-log"
sqlite_home="<scratch>/native-state"
```

MCP names are discovered without turns, then explicitly disabled on the actual
fresh child. The synthetic canary's URL is restored after that override because
a parent table assignment replaces its earlier same-layer URL; its disable
remains intact. Only the separate MCP source control enables that task-owned
server through `thread/start.config`. Approval remains `never`.

[Native auth loading](https://github.com/openai/codex/blob/rust-v0.160.0/codex-rs/login/src/auth/manager.rs#L1488)
returns before persistent credential fallback when storage is ephemeral.
[Its storage implementation](https://github.com/openai/codex/blob/rust-v0.160.0/codex-rs/login/src/auth/storage.rs#L459)
is memory only. The harness never reads/copies existing authentication files or
changes persisted user auth/config. The existing Windows sandbox's internal
service-account authentication is infrastructure already used by command
diagnostics; it is distinct from harness access to user/provider credentials.

Recorded development outcomes, in order:

| Observation | Actual result and limit |
| --- | --- |
| First positive scratch patch | Native dispatch rejected an 8.3 temp alias as outside the project. Using the already canonical `b.scratch` fixed it without changing permissions. Both profiles then passed in 20.06s; this proved only a scratch patch. |
| Expanded replay, 41.59s | Filesystem checks and network denial passed; environment validation failed and the initial stdin fixture did not complete. No overall pass. |
| Expanded replay, 43.17s | Real `exec_command` and `write_stdin` both reported `checks_ok=true` and `network_denied=true`. Sensitive environment variables were absent; only `TEMP`, `TMP`, `TMPDIR` lacked scratch pinning. No credential leakage was observed. |
| Filesystem replay after temp repair, 76.71s | Both profiles passed, 37 native outputs per role. Advertised tools were `apply_patch` (custom), `exec_command`, `request_user_input`, `view_image`, `write_stdin`. All four filesystem tools were exercised against synthetic main, sibling, controller evidence, hidden grading, credentials, shared Git metadata and a junction alias. Worker/scratch positives, reviewer write denials, protected read/write/image denials, stdin process identity, escalation refusal, environment and network checks passed; parent hashes/forbidden paths were checked. This predates the final MCP/hook additions. |
| MCP source control, 11.99s | Native `tool_search` discovered `read_synthetic` in the canary namespace. Its named dispatcher returned `MCP tool call requires approval, but approval policy is never`. Server listing occurred; callbacks remained zero. This is native advertisement/discovery/dispatch evidence, not callback execution. The WIP validator now handles its structured text output; that change has not been rerun natively. |
| Removed synthetic-home hook source, 170.14s | Timed out before replay output. Parent inspection found a new `.sandbox` directory in its task-owned synthetic home. No `.sandbox-secrets` or `auth.json` creation was reported, and no setup approval was granted. This control cannot satisfy the no-provisioning requirement and is no longer runnable. |

The removed hook attempt retained elevated mode, used a fresh synthetic home
and only authored `update_plan` replies, with shell/image/snapshot features off
and a trusted task-owned hook. Although the
[plan handler](https://github.com/openai/codex/blob/rust-v0.160.0/codex-rs/core/src/tools/handlers/plan.rs)
only emits session events, that did not establish safety of native thread/turn
startup. The exact retained failure was:

```text
codex app-server read: codex app-server timeout
context deadline exceeded
replay limitation: isolated hook source created forbidden .sandbox
--- FAIL: TestCodexModelToolIsolationSmoke (170.14s)
FAIL github.com/kninetimmy/orch/internal/codexnative 170.679s
```

This is evidence of task-owned sandbox state creation and unverified implicit
provisioning risk, not proof that machine provisioning completed. `consent.exe`
was observed by metadata only; its attribution is unverified and it was not
killed. After the deadline, metadata queries found no owned Go test PID 11376,
its direct children, or native replay processes with the synthetic provider
marker. Not every possible elevated setup descendant could be attributed,
so cleanup of such descendants remains uncertain. At that stop, no further
native attempts ran. The bounded existing-home follow-up below did not repeat
the fresh-home setup.

Before #320, `isolationBoundary.args` used `inherit="none",set={}`. Buffered
diagnostic commands explicitly supplied scratch temp values, while real shell
tools lacked them. After #320, the shared builder sets **only** `TEMP`, `TMP`,
`TMPDIR` to canonical scratch. `isolationConfig.verify` requires exactly those
three values and rejects inherited/additional values. This applies to every
child using that builder and both role profiles, not one named tool. Every
existing filesystem grant/denial, elevated requirement and command-network
restriction is retained. The old empty-set statement above is historical.

At the original WIP stop, scope was: `isolation.go` and `isolation_config.go`
changed only that shared temp
pinning/verification; `isolation_test.go` checks both profiles; `preflight_test.go`
adds one test-binary fixture dispatch. New `tool_replay_test.go` contains the
bounded replay/evidence checks, synthetic MCP and command fixtures; new tagged
`tool_replay_live_test.go` contains the opt-in native probes and explicit final
limitation. No role/routing defaults, corpus/rubrics, user permissions, adapters,
release/install state, dependencies, memhub or production refusal changed.
At the WIP stop, complete per-symbol accounting and the acceptance criteria
were unfinished. The bounded follow-up's accounting appears below.

Focused no-native tests passed for replay missingness/cleanup/protected changes,
isolation profile/config/environment failures and production start/resume refusal
for all five roles. Full `go build ./...`, `go test ./...`, `go vet ./...` and the
final native checks were deliberately not run at that WIP stop after the
Architect stopped work with criterion 3 unsatisfied. Native subscription auth/inference, actual usage,
live interruption, disconnect/resume, evaluation integration and a measured
baseline remain unverified.

### Bounded existing-home capability evidence

The follow-up uses the existing installed home and sandbox. It creates no
home, installer, VM or dependency, writes no user configuration/trust/auth,
and never enables plugins. Two metadata-only children enable hooks solely to
call `hooks/list`; neither starts a thread or turn or executes a hook command.
The first call recognizes a task-owned `PreToolUse` command with matcher `.*`
and obtains its actual key/current hash. The second supplies that exact hash
in one session-flags `hooks` table and requires native `source=sessionFlags`,
`eventName=preToolUse`, `handlerType=command`, `trustStatus=trusted`, enabled
status, matching command and matcher, and an unmanaged, unique identity.
Hashes and source keys are discovered, never hardcoded or persisted.

The same hook table remains configured on every actual replay child with
`features.hooks=false` and `features.plugins=false`. Native `hooks/list` and
`plugin/installed` must return present, empty inventories without errors or
warnings. Effective configuration and loaded feature support are verified
before any turn. The real advertised tool catalog is separately checked on
every replay request; native filesystem positive controls establish dispatch,
and matched dispatch must emit no hook execution notification or hook marker.
Recognition/trust is a source control, not a positive hook execution control.

Plugin provenance uses the existing configured, enabled
`unified-computer-use@openai-bundled` fixture. The actual child must retain that
configuration while the plugin feature is disabled. Parent checks require its
single installed cache version, canonical containment, a manifest identifying
`unified-computer-use`, and a regular nonempty `.mcp.json` source file. Only
manifest identity is decoded; MCP contents, credentials and commands are not
read or executed. An absent, ambiguous or escaped installation fails rather
than silently accepting an empty catalog or inventing a plugin tool name.
This is deliberately an installed-source fixture for this validation host;
other hosts must satisfy it or report that precise limitation.

The pinned native
[plugin loader](https://github.com/openai/codex/blob/rust-v0.160.0/codex-rs/core-plugins/src/manager.rs#L815)
returns no plugin capabilities when plugins are disabled; its independent
[plugin-hook loader](https://github.com/openai/codex/blob/rust-v0.160.0/codex-rs/core-plugins/src/manager.rs#L997)
also returns no sources. Both gates cover every configured plugin routed
through those loaders, including installed sources. The native
[hook engine](https://github.com/openai/codex/blob/rust-v0.160.0/codex-rs/hooks/src/engine/mod.rs#L238)
needs both gates: plugin cleanup hooks can survive the hook feature alone.
The metadata-only
[catalog handler](https://github.com/openai/codex/blob/rust-v0.160.0/codex-rs/app-server/src/request_processors/catalog_processor.rs#L614)
does not execute hooks and only loads plugin hooks when both features are on.
The
[installed inventory handler](https://github.com/openai/codex/blob/rust-v0.160.0/codex-rs/app-server/src/request_processors/plugins.rs)
returns an empty catalog before auth/synchronization when plugins are disabled
and its configuration loads without errors. Source/configuration alone is not
reported as runtime isolation proof: installed provenance, native inventories,
the actual dispatch catalog and the configured MCP rejection control accompany
the source-backed gates. Plugin callbacks and enabled plugin loading are not
positive controls, and no claim about their execution is made.

### #320 blast radius and compatibility

| Touched element | Before #320 | After #320; does prior behavior still hold? |
| --- | --- | --- |
| `isolation.go`: `isolationBoundary.args` | Command tools inherited no environment and had an empty set; buffered diagnostics separately pinned temp paths. | Retains every permission, path, network, feature and elevated-sandbox rule. Pins only `TEMP`, `TMP`, `TMPDIR` to scratch for every child and both profiles; the exact before/after is retained above. |
| `isolation_config.go`: `isolationConfig.verify` | Required an empty command environment set. | Requires exactly those three scratch values. All other configuration restrictions, discovery, loaded-feature checks and error behavior remain. Applies to every child verified with this function. |
| `isolation_test.go`: `TestIsolationPathsAndProfiles` | Checked scrubbed environments, profiles and fail-closed admission. | Checks the new shared scratch-temp contract for both profiles; prior protections and missing/conflicting control failures remain. |
| `preflight_test.go`: `TestMain` | Dispatched existing scripted preflight/session/isolation fixtures. | Adds only the test-binary model-tool fixture entry. Existing branches and ordinary no-native/no-model behavior remain. |
| New `tool_replay_test.go`: `replayTool`, `replayCall`, `toolReplay`, `advertisedReplayTools`, `toolReplay.ServeHTTP`, `toolReplay.validate`, `validateReplayOutput` | No scripted native model-tool replay seam. | Adds test-only bounded Responses replay, actual catalog parsing, native output validation and explicit missingness/errors. No production provider or bypass is exposed. |
| New `tool_replay_test.go`: `replayMCP`, `replayMCP.ServeHTTP`, `replayFixtureRequest`, `modelToolFixture`, `verifyReplayFiles`, `TestToolReplayEvidenceFailsClosed` | Existing command containment fixtures did not prove model-tool dispatch. | Adds one synthetic MCP capability, direct/stdin probes, marker/hash checks and CI failure checks. Existing fixtures, production refusal and dependencies remain unchanged. |
| New tagged `tool_replay_live_test.go`: `TestCodexModelToolIsolationSmoke`, `modelToolReplayPlan`, `replayArgs`, `openToolReplay`, `openReplayChild` | Only the separately opt-in command-isolation smoke existed. | Adds bounded opt-in installed-native dispatch for both profiles and the MCP source control. It uses existing isolation rules, synthetic auth and task-owned replay, with no live inference. The old command smoke remains separately required. |
| Same tagged file: `replayMetadata`, `replayHookInventory`, `replayHookSource`, `replayDisabledCapabilities`, `replayInstalledPluginSource` | The WIP unconditionally failed for hook/plugin controls. | Replaces only that unconditional failure with source/inventory/dispatch requirements. These test-only RPCs do not change the production transport allowlist. Unsupported/missing evidence still fails; hook execution and enabled-plugin positive controls are deliberately absent. |
| This document | Recorded command-only limits and the incomplete replay attempts. | Preserves that history and the removed unconditional-failure before/after, then records the smaller supported evidence route and its limits. |

Every unlisted production symbol retains its behavior. In particular,
`modelToolBoundary`, `RunSession`, `Session.Resume`, `IsolationPreflight` and
evaluation model execution remain refused for all roles, not one specialist
symbol. Every diagnostic command still requires verified restrictions; every
metadata-only connection still denies production thread/turn execution. Native
replay proves synthetic tool behavior only. Live subscription authentication,
inference, usage, interruption/resume and the bounded subsequent trial remain
separate evidence. Frozen corpus/rubrics, controller/evaluation storage,
routing/defaults, user permissions, adapters, releases/install state, manual
execution and deferred Claude/shell-containment work are unchanged.

### Bounded follow-up results

On the same installed Windows 0.160.0 host, the first follow-up native check
failed in 6.08s before any thread/turn: native hook recognition and session-only
trust passed, then the harness incorrectly used a directory-only canonicalizer
on the installed plugin manifest. File canonicalization was corrected with
`filepath.EvalSymlinks`; no host setup or configuration change was involved.

The one corrected attempt began 2026-10-05 17:41:56 UTC and failed in 49.31s.
The installed configured plugin source and empty native hook/plugin inventories
passed. The MCP source control advertised `apply_patch`, `exec_command`,
`list_mcp_resource_templates`, `list_mcp_resources`, `read_mcp_resource`,
`request_user_input`, `tool_search`, `view_image`, `write_stdin`, discovered the
configured `read_synthetic` capability, and reached its named dispatcher.
Approval `never` rejected execution; listing count was one and callbacks zero.
The restricted worker reached native filesystem dispatch and returned the
configured MCP rejection as `unsupported call: mcp__orch_replay_canaryread_synthetic`.
The old validator expected a dot between namespace and name and rejected that
reply. No other call produced a failing-output diagnostic, and cleanup's
parent protected-content/forbidden-path checks reported no failure. These are
partial observations, not a completed worker/reviewer pass.

The validator now accepts that exact observed reply form and has a no-native
regression check rejecting another source's reply. Code was frozen at the
implementation time limit. The final deterministic verification began
2026-10-05 17:46:19 UTC and passed in 94.52s (package 95.071s), including
shutdown. Both worker and reviewer advertised exactly `apply_patch` (custom),
`exec_command`, `request_user_input`, `view_image`, `write_stdin`; each produced
four replay requests and 38 validated native outputs. All permitted
workspace/scratch controls and protected read/write/image/stdin denials,
configured MCP rejection, escalation refusal, command environment and network
checks passed. Parent markers, protected hashes and forbidden new paths were
verified; native hooks emitted no execution event or marker. The configured
installed plugin source remained present while native hook/plugin inventories
were empty on both actual replay children. The MCP source control still listed
once with zero callbacks and reached the approval-never rejection.

The separately required `TestCodexIsolationSmoke` passed in 56.31s (package
56.862s): all 46 parent-verified synthetic commands, both profiles, native
timeout cancellation and descendant markers passed. No subscription inference
or provisioning ran during this follow-up. Earlier fresh-home descendant
uncertainty is not resolved by these cleanly bounded existing-home observations.
Production execution stays refused; an independent review and separately
approved subscription-backed trial remain necessary.

## Verified native admission (#322, current behavior)

Before #322, `modelToolBoundary(version)` returned `ErrIsolationUnavailable`
unconditionally, including after #321's successful synthetic tool replay.
`IsolationPreflight`, every `RunSession` start and every `Session.Resume`
reconnect therefore refused for all five dispatched roles. After #322,
`modelToolBoundary(connection, boundary, capabilities)` admits only the
reviewed Windows amd64 0.160.0 enforcement with its actual connection's controls.
Older, newer, prerelease, missing and otherwise unreviewed native enforcement
still refuses. Version equality is a necessary source-evidence boundary, never
sufficient admission. Private command diagnostics retain their broader
capability compatibility; their success alone does not enable session methods.

The admission checks require the canonical workspace/scratch/protected-path
profile, no inherited profile roots/extensions, elevated sandbox readiness,
allowed profile, network denial, approval `never`, disabled web search, scrubbed
command environment and scratch-pinned `TEMP`, `TMP`, `TMPDIR`. Discovery still
closes without execution, and a fresh child explicitly disables every discovered
MCP server. The server set must remain identical. Effective configuration and
loaded feature support are checked again after managed account/catalog inspection
on the actual execution connection. Missing, enabled, conflicting, malformed or
unsupported controls refuse before `thread/start` or `thread/resume`.

In addition to the existing inherited-surface controls, the shared process-only
builder disables `goals`, `request_permissions_tool`, `exec_permission_approvals`
and `image_generation`. Model admission requires all four in effective config
and loaded features. Command diagnostics require the original controls, so an
unreviewed model feature cannot silently narrow their compatibility. The actual
model connection must return exactly one `hooks/list` entry for its approved cwd,
with present empty hooks/errors/warnings, and present empty `plugin/installed`
marketplaces/load-errors. Both hooks and plugins must be disabled: the reviewed
source shows plugin cleanup hooks are not excluded by the hook flag alone.
Hook/plugin inventory errors or warnings and reported hook execution refuse
progression. Generic advisory notifications remain nonauthoritative: their method
names cannot prove isolation or substitute for concrete control checks. The
pinned thread/session source also emits instruction and development-feature
notices, so a generic notice is not an invented blanket refusal rule. Required
configuration, loaded features, profile/provider/policy/path reports and permission
requests still fail closed on missing, conflicting or unsupported evidence.
These inventory checks reuse #321's verifier; production does not acquire its
installed-plugin source fixture or hook recognition/trust setup.

The pinned [feature registry](https://github.com/openai/codex/blob/rust-v0.160.0/codex-rs/features/src/lib.rs)
and [tool plan](https://github.com/openai/codex/blob/rust-v0.160.0/codex-rs/core/src/tools/spec_plan.rs),
the hook/plugin loaders cited above, and the retained native replay supply the
enforcement evidence. The [thread protocol](https://github.com/openai/codex/blob/rust-v0.160.0/codex-rs/app-server-protocol/src/protocol/v2/thread.rs)
supports explicit `modelProvider` and `runtimeWorkspaceRoots` on start/resume;
`allowProviderModelFallback` and `dynamicTools` belong to start only. Both
start/resume select `openai` and empty runtime roots. Start also supplies empty
dynamic tools, empty environments/capability roots and fallback false. The
response must confirm the OpenAI provider and present empty runtime roots.
Resume can restore only this same object's originally empty dynamic-tool history;
arbitrary externally supplied threads and histories remain unavailable.

All five roles share this gate: implementer/specialist use the worker profile;
scout/reviewer/review_downgrade use the read-only profile. The root-read
prerequisite and explicitly protected read denials remain as documented above.
`modelToolBoundary` is a shared restriction for every start/resume of every
role, not a specialist-only rule. `IsolationCapabilities.ModelToolsVerified`
means reviewed enforcement and observed controls; it is cleared on preflight
cleanup failure and does not set the metadata connection's session flag, approve
a task, prove subscription entitlement or create a metrics observation.

Task IDs, actual Delivery association, role prose, prompt, exact model/effort and
canonical caller paths remain fixed. The session now also retains its implicit
native credential-home and shared-Git protections; a changed binding refuses
reconnect without altering the retained checkpoint or dirty work. All reported
profile/provider/path/policy mismatches stop execution. Model/effort reports are
configured identity, not per-turn inference telemetry. Missing native identity
or usage stays unknown; an unidentified turn cannot be resumed or retried.
Managed ChatGPT account and auth-mode evidence are still required, with no
credential copying, key fallback, login or provider/model fallback API.

The finite caller deadline, 1 MiB message/output bound, 1,024 event/observation
limits, bounded interrupt acknowledgement and stdio shutdown/reaping remain.
Cancellation still preserves its execution outcome even when cleanup fails.
Disconnect recovery reuses the same retained thread/session/turn and deduplicates
usage/output; it never sends another `turn/start`. Completion is neither task
verification nor merge permission. No durable cross-process recovery is added.

### Every start/resume caller

| Caller or entry | Current behavior |
| --- | --- |
| `IsolationPreflight` | Bounded public no-turn evidence check, Windows amd64 only; closes both children and returns separate checked controls. No session method is enabled. |
| `RunSession` → `Session.connect` → `Session.execute(false)` | Validates the exact approved Task, performs metadata and isolation preflights, repeats controls plus managed auth/catalog on the actual execution connection, then submits at most one turn. |
| `Session.Resume` → `checkResume` → `connect` → `execute(true)` | Requires the original caller/task/profile/canonical protection binding and identified disconnected unfinished turn; repeats all admission checks, sends only `thread/resume`, and accepts only that single retained turn. |
| `internal/evalplan/controller.go`: `nativeWorker.execute` | Calls `IsolationPreflight` for eligibility but unconditionally returns `Outcome: refused` even if it succeeds. It does not call `RunSession`/`Resume`, fabricate Delivery IDs or admit evaluation model work. #323 owns integration and the still-historical refusal wording in evaluation preview/report. |
| Ordinary session tests | Scripted subprocess host satisfies or rejects the real gate. Public eligible five-role start/resume runs on Windows; private admitted lifecycle tests and refusal/binding checks run on every CI OS. No installed Codex or inference. |
| `TestCodexIsolationSmoke` | Existing opt-in no-model diagnostic commands; command proof does not enable any session. |
| `TestCodexModelToolIsolationSmoke` | Existing opt-in synthetic Responses/provider/auth replay; test-only session flag admits synthetic dispatch and reuses production inventory/feature checks. No managed subscription inference. |
| `TestCodexSubscriptionTrial` | Separately tagged and explicitly authorized live validation caller described below. Uses the same `connect`/`execute`/`Resume` lifecycle, with no production bypass. Not run by this source delivery. |

No CLI or Delivery-engine model execution caller is added. Search of all
`RunSession`, `Resume`, `IsolationPreflight`, `modelToolBoundary` and direct
thread/turn requests accounts for the entries above. Metadata connections still
deny every execution method, not just `turn/start`. Isolation connections add
only read-only hook/plugin inventory RPCs. Unsandboxed process, fork/steer,
approval/tool responses, delegation, login, configuration writes and arbitrary
RPCs remain unavailable on every connection, including admitted sessions.

### Separately authorized subscription trial (not executed)

The runnable path has the separate build tag `codex_subscription`. It is absent
from ordinary CI and both `codex_live` synthetic commands. It creates no home,
provisions no sandbox, installs no dependency, changes no persisted user
config/trust and probes no credential content. Use only the existing installed
Windows amd64 Codex 0.160.0 and elevated sandbox. Its future execution requires
a separate exact human approval; the #322 source plan does not authorize it.

A trusted caller first obtains a real, separately approved Delivery Task and
profile and confirms the current dispatch/run association through the parent
engine. The harness validates the binding but does not confer engine approval.
Do not manufacture RunID/IssueNumber/attempt identifiers for evaluation work.
Prepare existing, separately approved workspace/scratch/protected directories
outside each other, and small task-owned parent sentinels. No new Codex home is
part of that setup. The caller retains the exact prompt and selects one scenario:
`complete`, `interrupt`, or `recover`. Each authorization consumes at most one
turn and one same-turn reconnect; a later scenario needs a new explicit approval.

Prepare `codex-subscription-authorization.json` with these exact Go JSON fields:

| Field | Required binding |
| --- | --- |
| `Caller` | Exactly `TestCodexSubscriptionTrial`. |
| `Options` | `Executable`: absolute installed native executable; canonical `Dir` exactly equals Task workspace; real `ClientVersion`. |
| `Task` | Full exact `ID`, `RunID`, `IssueNumber`, `Role`, `Attempt`, `ReviewCycle`, `Selection` (`Model`, `Effort`, no variant), `Prompt`, and canonical `Layout` (`Workspace`, `Scratch`, `MainCheckout`, `ControllerState`, `SiblingWorkspaces`, `CredentialPaths`) from the separately approved caller. |
| `Scenario`, `TimeoutSeconds` | Exactly one named scenario and 1–180 seconds for initial execution plus reconnect. Existing bounded shutdown grace is additional; the Go test has a four-minute outer bound. |
| `ApprovedAt`, `ExpiresAt` | RFC3339 timestamps; no future approval, at most 30 minutes between them, and enough remaining validity for the full execution bound. |
| `Preserved`, `Forbidden` | 1–16 canonical protected sentinel paths mapped to exact SHA-256 hex hashes; at most 32 canonical protected outputs that must remain absent. Parent paths must be within approved main/controller/sibling locations and outside every supplied/native credential home. No credential/home contents are read. |
| `Expected` | At most 16 canonical workspace/scratch output paths mapped to exact SHA-256 hashes from the approved bounded tool action. They must be absent before execution; at least one is required for `complete`, so a model saying done without the expected new file fails. Recovered successful completion also checks supplied expected outputs. |
| `Evidence` | A new canonical `.jsonl` path under protected `ControllerState`, distinct from the checked sentinel/forbidden paths. Exclusive creation consumes authorization before any host launch; an existing claim, including a failed trial's claim, cannot be reused. |
| `Assertion` | Exact statement printed by preparation below and separately approved by the human. SHA-256 covers every preceding field, including caller, task/role/profile, executable, paths, scenario, limits and parent checks; only Assertion itself is blanked for hashing. |

Preparation validates the binding and prints the required assertion without
claiming authorization, launching a host or submitting a turn:

```powershell
$env:ORCH_CODEX_SUBSCRIPTION_TRIAL = 'C:\approved\controller\codex-subscription-authorization.json'
go test -tags codex_subscription ./internal/codexnative -run '^TestCodexSubscriptionTrialAuthorization$' -count=1 -v -timeout 20s
```

After the separate human approves that exact statement, the trusted caller
inserts it as Assertion in the file, checks the current parent dispatch again,
and runs exactly:

```powershell
go test -tags codex_subscription ./internal/codexnative -run '^TestCodexSubscriptionTrial$' -count=1 -v -timeout 4m
```

Missing, changed, expired or reused authorization refuses before host launch.
The ordinary `TestSubscriptionTrialAuthorization` checks those paths without
inference. A generic boolean opt-in cannot authorize the test. No trial command
above was run in this source delivery, including preparation against real paths.

Parent sentinel hashes and absent forbidden outputs are checked before and
after the trial. The protected JSONL evidence retains the exact authorization,
receipt times, initial error, before-resume checkpoint, final SessionResult,
requested versus native reported profile, thread/session/turn IDs, output,
source-distinct observations/unknowns, cleanup errors and final parent/expected-check
status. Evidence writes are synced and failures fail the test; evidence and dirty
workspace/scratch are preserved, never reset or automatically deleted.

Completion requires a successful terminal turn, clean shutdown and parent-verified
expected tool-output bytes. Expected paths are revalidated after model work;
`os.Root` confines resolution, file identity is checked before reading, and
symlink/junction escapes, hard links and unavailable link identity refuse. Both
protected and expected file reads are bounded to 1 MiB. The no-native output
regression checks missing files and matching-content protected aliases/hard links.
Interruption
cancels only after the existing `turnReady` seam has retained an identified turn;
it requires a native `interrupted` terminal event and the sole expected caller
cancellation error, with cleanup errors refusing validation. Recovery closes
only the owned output pipe after an identified turn, retains the disconnect and
any cleanup error, and makes one `Resume` call with the same Task/object. It
requires the same native thread/session/turn and a terminal resumed result;
an interrupted terminal result is honest recovery, not task completion. A fast
completion race, unsupported native resume, missing identity or cleanup failure
is retained as a limitation and is not retried with new task input. Owned-server
shutdown remains bounded; this path makes no blanket descendant-cleanup claim.
Interrupt/recover validate lifecycle; tool-action evidence remains unknown unless
the trial reaches successful completion and checks its supplied Expected outputs.

Synthetic success is still not live managed-authentication/inference/usage/
interruption/recovery evidence. Those subscription trials and the measured
evaluation baseline remain unexecuted. The removed fresh-home attempt's possible
elevated-descendant cleanup uncertainty above is unchanged and unresolved.

### #322 blast radius and compatibility

| Touched element | Before #322 | After #322; does prior behavior still hold? |
| --- | --- | --- |
| `isolation.go`: `IsolationCapabilities`, `IsolationPreflight`, `modelToolBoundary` | Configuration prerequisites followed by unconditional model refusal for every role/version. | Adds separate ModelToolsVerified evidence and conditional admission for reviewed Windows amd64 0.160.0 plus actual controls; before/after refusal is retained above. No-turn preflight, cleanup, missingness and unsupported-host refusal remain. |
| `isolation.go`: `isolationBoundary.args`, `openIsolation` | Shared canonical profile/environment/feature rules and discovered MCP disables; diagnosticReady follows verified readiness/profile. | Retains all rules and diagnostic compatibility; pins OpenAI provider, supplies four model-only disables and retains discovered MCP names privately for the later recheck. Every child/profile uses the builder. |
| `isolation_config.go`: `isolationConfig`, `restrictedFeatures`, new `modelRestrictedFeatures`, `verify`, new `verifyFeatures`, `verifyRestrictedFeatures`, new `verifyDisabledCapabilities` | Effective config and original loaded-feature checks; inventories were test-only. | Retains original diagnostic requirements; extracts their shared feature decoder, adds the four model-only requirements and promotes bounded empty hook/plugin inventories. Config echo alone still cannot pass. No user config/trust, plugin provenance or credential probes are added. |
| `transport.go`: package comment, `connection.disabledMCP`, `connection.request` | Metadata denied execution; isolation allowlist had config/features/readiness/profile; generic advisories were not authority. | Keeps execution/approval/process/fork/tool/config-write restrictions for every connection. Adds only isolation inventory RPCs and private discovery binding. Generic notification handling remains unchanged; concrete control checks decide admission. |
| `session.go`: `Session.boundary`, `newSession`, `checkResume`, `connect`, `RunSession` comment | Exact canonical caller Task binding and lifecycle; implicit protection paths were recomputed without retention; every model boundary refused. | Retains task/profile/auth/catalog/deadline/cleanup/dirty-work/replay behavior. Adds implicit protection comparison and actual gate admission; every start and resume uses it. No caller bypass or arbitrary execution API. |
| `session.go`: `nativeSettings`, `nativeThreadResponse`, `execute`, `settings`, `acceptThread`, `notification` | Explicit model/effort/policy/profile; inherited provider was not bound, roots/dynamic-tools were not explicit, hooks were ignored. | Retains mismatch/unknown handling; pins and confirms provider/empty roots, sets empty start dynamic tools and stops hook execution reports. Native lifecycle identities, one turn and no-replay semantics remain. |
| `isolation_test.go`: `TestIsolationEnvironmentsAndModelRefusal`, `scriptedIsolationServer`, new `TestModelToolAdmission` | Synthetic diagnostics, environment/config/feature failures, unconditional refusal. | Retains diagnostic/refusal/cleanup cases; tests eligible controls versus missing/changed/unsupported/inherited inventories, wrong provider and connection/profile mismatch for both profiles. Extends the existing scripted host to the session fixture, without a native inference seam. |
| `session_test.go`: `scriptedSessionConnection`, `scriptedSessionServer`, new `scriptedSessionRequests`, `TestSessionScriptedLifecycle`, `TestSessionDisconnectResumeReplay`, new `TestSessionAdmittedStartAndResumeEveryRole`, new `TestSessionResumeRejectsChangedImplicitProtection` | Private synthetic session-flag bypass and bounded lifecycle/binding/counter tests. | Removes that bypass: ordinary fixtures satisfy the real gate. Retains prior failure/lifecycle/unknown/usage checks; adds provider/roots/hook refusals, public eligible five-role start/resume and changed shared-Git binding rejection. Unreviewed production fixtures still refuse without model work. |
| `preflight_test.go`: `TestPreflightSelectionAndExecutionBoundary` | All metadata execution/isolation methods refused. | Same behavior, with explicit inventory-RPC denial checks. |
| `isolation_live_test.go`: `TestCodexIsolationSmoke` | 46 native synthetic command probes plus assertion of global model refusal. | All probes/parent/descendant/cleanup checks remain. Replaces that assertion with the actual invariant: command evidence alone leaves session admission disabled. No model turn. |
| `tool_replay_live_test.go`: `replayArgs`, `openToolReplay`, `replayMetadata`, `replayDisabledCapabilities` | Test-only four feature disables, ignored process `ephemeral=true`, manual inventory RPC dispatch and duplicated empty-inventory decoder. | Removes duplicate overrides/dispatcher/decoder and the deprecated ignored process ephemeral key; supported thread/start ephemeral=true remains. Reuses production controls/allowlist/verifier. Synthetic provider/auth, hook trust source, installed-plugin provenance and both role replay plans remain test-only. No new fixture infrastructure. |
| New `subscription_trial_test.go`: `subscriptionTrial`, `assertion`, `validate`, `parentPath`, `expectedRoot`, `checkExpected`, `checkTrialHash`, `claim`, `TestSubscriptionTrialAuthorization`, `TestSubscriptionTrialExpectedOutput` | No executable exact live-trial authorization or parent tool-output check. | Adds test-only bounded caller assertion/canonical parent/output validation and exclusive one-use claim; missing/mismatched/expired/reused input cannot launch a host. Completion must leave exact expected tool output; replaced aliases and hard links cannot make the parent read protected sources. No production approval/config API. |
| New `subscription_trial_path_windows_test.go`, `subscription_trial_path_other_test.go`: `trialSingleLink` | No trial-specific link-identity check. | Test-only reuse of Windows handle metadata and Unix stat link counts rejects hard links or unverifiable identity before file reads. No production path-helper or platform compatibility change. |
| New tagged `subscription_trial_live_test.go`: `TestCodexSubscriptionTrial`, `TestCodexSubscriptionTrialAuthorization`, `readSubscriptionTrial`, `runSubscriptionTrial`, `checkParent` | No separately callable managed-subscription lifecycle trial. | Adds explicit unexecuted opt-in preparation/trial using the existing session API and turnReady seam, finite limits, parent checks and retained evidence. No ordinary-CI or codex_live inference. |
| This document | Recorded unconditional model refusal and synthetic-only validation. | Preserves the old statements and fresh-home uncertainty; adds current conditional behavior, all caller/symbol boundaries, source and configured-versus-observed limits, and a separately authorized unexecuted trial recipe. |

Every unlisted production symbol retains its behavior. Evaluation/controller
integration and storage, corpus/rubrics/grading, routing/defaults, metrics
association/persistence, manifests/run schemas, approval/merge gates, manual
adapters/guards, releases/installation, user permissions/config/trust and
dependencies are unchanged. In particular, every evaluation production attempt
still refuses; changing a shared native admission helper does not authorize it.

### #322 verification and retained repair history

Final serial native checks on the same installed Windows 0.160.0 host passed:
`TestCodexIsolationSmoke` in 42.394s (all 46 parent-verified commands), and
`TestCodexModelToolIsolationSmoke` in 83.058s (both role profiles, each with 38
validated native outputs and the configured MCP/source/inventory controls).
Final ordinary package tests passed in 42.666s; the separately tagged
authorization/output tests passed in 0.984s without selecting a subscription
trial. Build, vet, formatting and diff checks passed; gofmt output was empty.
The full Go suite passed earlier, including evalplan in 562.859s. Later localized
trial and warning-handling repairs were exercised with affected package/tagged
and native checks; the unchanged long evaluation suite was not repeated. Final
head-wide CI and independent review remain the Architect's lifecycle steps.

The first focused run failed only because two new provider/root refusal fixtures
correctly retained an unknown session identity while an old assertion expected
a known one; the assertion was corrected. An added generic-warning filter then
caused native failures before any replay output: 6.213s at the hook source,
6.877s at actual-child metadata, and diagnostic runs of 6.796s, 6.718s and 6.786s.
The last run safely characterized a deprecated config `ephemeral` notice without
printing native account/config text. Before this repair, replayArgs included an
ignored top-level `ephemeral=true`; after it, only the supported thread/start
ephemeral parameter remains, with ephemeral credential storage unchanged.
That repair advanced to thread/start but the added blanket filter still failed
in 11.558s on an advisory. Pinned thread/session source confirms that advisory
notifications also carry instruction and development-feature notices. The
Architect removed this unrequested blanket policy and its temporary diagnostic
taxonomy, retaining every concrete enforcement check. The final native pass
above followed that correction. No failed attempt is reported as a pass.

No managed subscription trial, real-path authorization preparation, inference
identity/usage observation, live interruption/recovery or evaluation baseline
ran in this source increment. Existing-home validation did not resolve the
historical fresh-home elevated-descendant uncertainty.
