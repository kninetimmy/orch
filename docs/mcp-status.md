# Read-only live status and next-action guidance over MCP

`orch mcp` is an optional local stdio MCP server using the official Go SDK
v1.8.0. This is an unreleased source feature, not part of the v0.15.2 binary.
Build `./cmd/orch` locally with Go 1.26+ to try it; no global installation or
host configuration edits are performed automatically.

## Launch and repository binding

Point an MCP client at an absolute path to your built binary with these arguments:

```text
orch mcp --repo /absolute/path/to/main-checkout
```

Without `--repo`, Orch binds to the process's current working directory. The
selected directory must already exist and contain a real `.orchestrator/`
directory, not a metadata file or symlink. Orch canonicalizes the selected path
using its existing path checks and fails clearly if it cannot verify it. It
never searches ancestors or initializes a repository. Tool arguments cannot
change this binding; launch another server to inspect another checkout.

**Bind the main checkout explicitly to inspect its live run.** Delivery
worktrees contain committed metadata but do not share the main checkout's
machine-local state or Delivery lock. Launching there without `--repo` can
legitimately report Assist even while the main checkout has an active run.

The client owns the child process and its stdin/stdout pipes. Stdout contains
only MCP protocol messages; diagnostics go to stderr. Closing the client's
session closes stdin and lets the server exit cleanly. Running the launch
command alone in a terminal waits for MCP input; it does not print a status.

## Client configuration and tools

Minimal configuration for clients that use an `mcpServers` object (adapt the
paths and the configuration location for your client):

```json
{
  "mcpServers": {
    "orch": {
      "command": "/absolute/path/to/local/orch",
      "args": ["mcp", "--repo", "/absolute/path/to/main-checkout"]
    }
  }
}
```

On Windows, use `orch.exe` and JSON-escaped absolute paths such as
`C:\\work\\orch.exe` and `C:\\work\\project`. Review and apply configuration
yourself; the server does not edit client or host settings.

**Before trial item 2:** Tool discovery returns exactly one tool,
**`orch_run_status`**, annotated read-only. **After trial item 2:** discovery
returns exactly two read-only tools: **`orch_run_status`** and
**`orch_run_next`**. Call either with `{}`. The empty-object input restriction,
including rejection of repository overrides, applies to **every tool this
server exposes**, not only the symbol `orch_run_status`.

Each `orch_run_status` invocation reads fresh local
state through the same engine as `orch run status --json` and returns that
entire document as both `structuredContent` and equivalent JSON text content.
The status document currently has `schema_version: 4`. Mode, consistency
warnings, lock, active run, issue phases and block/resolution histories are
preserved whenever present. Inconsistent state/lock pairs remain inspectable
with warnings. Corrupt state is an explicit MCP tool error (`isError: true`),
not a fabricated Assist result.

Initialization, discovery, calls (including failures) and disconnect do not
write repository files, run lifecycle mutations or launch external commands.
This server does not replace the CLI, the thin host plugins or their guards,
and it does not change Orch's separate memhub CLI integration.

## Next-action guidance and approval boundaries

Call `orch_run_next` with `{}`, or run this CLI command from the bound checkout:

```sh
orch run next --json
```

Both return the Go engine's `NextDoc`, currently `schema_version: 1`. MCP
`structuredContent` and JSON text contain the same document as the CLI. The
CLI accepts exactly `run next --json`, reads no stdin and takes no mutation
serializer; neither CLI nor tool arguments provide a repository override.
The launch-time MCP binding described above remains fixed for both tools.

The document contains `mode`, optional `run_id`, ordered `actions`, and
`limitations`. Each action has an `action`, `reason` and outstanding
`prerequisites`; issue actions identify `plan_id`, `issue_number` and `phase`.
`blockers` states recorded obstacles, and `approval_required`, when present,
describes a **separate** approval or explicit confirmation still needed.
Actions retain recorded issue order; this is not a new scheduler or agent
dispatcher. In particular:

| Recorded state | Guidance |
| --- | --- |
| Assist | Investigate and prepare a plan; present its effective-contract gate for explicit human approval before activation. No tracked-file implementation. |
| Planned / issue-created | Investigate interrupted activation; resume can adopt existing artifacts, never create missing ones. Abort and re-activation may be necessary. |
| Worktree-ready | Dispatch only after recorded dependencies are merged/cleaned and approved work/routing exist. Outstanding dependencies produce waits; an abandoned dependency requires the plan gate. |
| Dispatched | The routed executor implements and verifies approved work in its issue worktree; PR-open still needs evidence and live preflights. |
| PR-open / in-review | Routed review, or executor fixes and re-review after request-changes. An approving recorded review points to merge-report, not directly to merge. |
| Awaiting-merge | Present the report and obtain fresh human merge approval pinned to that PR/head (`approve-merge`); then the normal merge verb checks the live head and CI. |
| Merged / abandoned | Cleanup remains an explicit confirmed deletion (`cleanup-issue`), including disposable review worktrees. |
| All issues cleaned | Complete still checks the primary checkout, fast-forwards it and returns to Assist. Its result, not guidance, supplies any memhub wrap-up cue. |
| Blocked | Operational or explicitly resolved blocks point to resume after remediation. Pending decisions require human/Architect resolution (`resolve-block-without-scope-change`) only if approved scope/criteria are unchanged; changed scope/criteria require fresh plan approval. Unknown legacy blocks cannot be resolved in place. |

For next-action advice, approved-work/routing and dispatch's recorded dependency
rules are applied conservatively to every active issue phase (worktree-ready
through awaiting-merge), not only the symbol `Dispatch`. Thus a later phase
cannot make guidance ignore an abandoned dependency. Existing lifecycle verbs'
own prerequisite enforcement is unchanged. Identity/reference sanity checks
belong to `Next` only; `Status` retains its existing inspection behavior.

Guidance **does not run git, GitHub, CI, tests, host-availability checks or
worktree preflights**. It cannot establish that required CI passes, a PR is
open, a review still covers its live head, cleanup is safe, or recovery will
succeed. Even `merge-report` is a separate **mutating** lifecycle verb, not
something inspection performs. The snapshot is unlocked and may become stale;
re-inspect after changes and let normal verbs enforce all their preconditions.

Inconsistent state/lock pairs yield recovery blockers, not proceed advice.
Unreadable, corrupt or unsupported state/locks, ambiguous issue identities and
unresolvable dependency references are explicit tool errors, never
fabricated Assist guidance. Legacy execution contracts and effective
configuration drift block lifecycle advice until the engine's stated recovery
or fresh-plan path is followed. A stopped run blocks all ordinary issue advice:
remediate the stop and separately authorize recovery with `resume-stopped-run`,
or abort. That statement clears neither pending decisions nor changed scope.

Neither inspection tool records a session, metrics, an approval, a decision
resolution or lifecycle advancement, nor mutates git/GitHub. **A tool call is
never human approval.** Existing separately authorized grant contracts are
unchanged, but guidance does not inspect, validate or spend a grant; it cannot
claim a grant authorizes any suggested action. These restrictions apply to
**both inspection tools and both CLI inspection commands**, not just `Next`
or `orch_run_next`. Stateful lifecycle verbs retain their existing behavior.

## Reproducible smoke test and trial checkpoint

From the source checkout, run:

```sh
go test ./internal/cli -run '^TestMCPStdio$' -v -count=1
```

This builds a temporary real `orch` binary and uses an SDK client over its
stdio pipes to initialize, discover and call both tools, then disconnect and
verify a successful process exit. No global install or client configuration
is needed. The client stays connected while test fixtures change from Assist
to blocked Delivery and inconsistent state/lock pairs. Output reports
`CLI = structured = JSON text` for each decoded status comparison and the
explicit corrupt-state tool error. It also checks strict arguments, repository
binding and unchanged repository files. These tests run in `go test ./...`.
Next-action fixtures additionally cover dependency waits, implementation,
review, merge-report, human merge approval, abandonment, decision resolution
versus recovery, stopped runs, completion and effective configuration drift.
Each successful comparison reports `CLI = structured = JSON text`, with no
external executables available to the server and repository names, file kinds,
modes, modification times and bytes unchanged across inspection. Separate
engine tests cover legacy/unsupported state, invalid configuration, all block
causes and phase prerequisites, including inspection with metrics enabled and
a Claude session id present.

For a manual trial, use the configuration above, connect your client, discover
`orch_run_status`, and call it with `{}`. Compare its decoded JSON with
`/absolute/path/to/local/orch run status --json` run **from the bound checkout**.
After a separately approved normal workflow changes that checkout's state,
call again in the same session and compare again. Disconnect the client and
confirm its server child exits. Do not edit live state files to create a trial;
the automated smoke test supplies isolated fixtures for that purpose.
Also discover `orch_run_next`, call it with `{}`, and compare with
`/absolute/path/to/local/orch run next --json` from that same checkout. Treat
its prerequisites as outstanding, not as evidence or approval.

**Before trial item 2:** This increment implements roadmap item 1 (live status)
only. **After trial item 2:** source implements live status and read-only
next-action guidance, not the subsequent readiness/doctor items. The trial
checkpoint is to **finish roadmap items 1 through 4, then stop and assess the
benefit against CLI usage before approving later roadmap items**. This status
server does not approve or implement those later increments.

## Trial item 2 blast radius

| Touched element | Does its previous behavior still hold? |
| --- | --- |
| Go run-engine surface (`run.Next`, `NextDoc`, `NextAction`, `NextSchemaVersion`) | New schema-1 local-state guidance only; no lifecycle or approval contract is replaced. Package command/concurrency documentation now includes `next`. |
| Effective configuration checks (`loadExecutionConfig`, `checkExecutionConfig`) | Yes. The existing revision, state/contract-version and effective-fingerprint checks are shared without changing their ordering, errors or authorization meaning. The new loader is read-only for every caller, not only `Next`. |
| Lifecycle loading (`loadVerb`, all lifecycle callers) | Yes. Configuration checks delegate to the shared loader; stopped-run/phase/issue checks and existing session attribution stay in place. No lifecycle verb is converted to an inspection command. |
| Resume loading (`resumeLoad`, `Resume`) | Yes. Same consistency/configuration gates, no stopped-run gate or new session write, and unchanged observe/classify/apply and explicit recovery contracts. |
| Merge-report (`checkApprovingReview`, `MergeReport`) | Yes. Its approving-review check is shared with guidance; live PR/CI checks, head pinning, state/status/metrics writes remain exclusively in the normal lifecycle path. |
| Reused prerequisites (`checkApprovedWork`, `checkDependencies`, `Issue.DecisionPending`, `allCleaned`) | Yes; unchanged implementations. Guidance uses the same missing-work, merged/cleaned dependency, abandoned-dependency, decision-versus-operational and all-cleaned rules. It does not weaken dispatch, review, recovery or completion. |
| CLI dispatch/help (`runRunVerb`, `runUsage`, `commands`, package command documentation) | Existing stdin-taking/mutating routing and serialization remain. Before: status alone was the no-stdin inspection command. After: `next --json` joins it; both bypass stdin and mutation serialization. Help now describes both MCP tools. |
| MCP server (`runMCP`, SDK registration, transport and launch binding) | One-tool-only discovery is removed as recorded above; two strict empty-input tools now call their respective engine functions. Status schema 4, fresh reads, structured/text parity, diagnostics, canonical launch binding and clean disconnect remain. No mutation tool is exposed. |
| Inspection tests (`next_test.go`, `mcp_test.go`, `run_test.go`) | Existing status/binding/no-write/no-exec/shutdown and mutator-serialization coverage is retained; shared parity/discovery helpers now cover both tools and next adds safety/phase fixtures. |
| Source guide (`docs/mcp-status.md`) | Launch instructions, optional source-only installation and trial checkpoint remain. The previous one-tool/item-1-only claims are retained explicitly as before/after, with new guidance limitations and approval boundaries. |
| Dependencies, persistent schemas, host adapters/guards and CI workflows | Untouched. No dependency, state/status/approval schema migration, installation, readiness/doctor feature, automatic approval or new lifecycle capability is added. Existing cross-OS Go checks and lint/adapter CI remain required. |
