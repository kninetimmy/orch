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

Every request method in this connection shares the closed allowlist, not just
`turn/start`. All thread/turn, command/process, login/logout, configuration
mutation and approval methods are unavailable. The only outgoing notification
is `initialized`; there is no arbitrary notification or request API exported.
The connection is private to the preflight and is closed before returning.

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
