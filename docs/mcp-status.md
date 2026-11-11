# Read-only status, next actions, readiness, doctor and effective configuration over MCP

`orch mcp` is an optional local stdio MCP server using the official Go SDK
v1.8.0. This is an unreleased source feature, not part of the v0.15.2 binary.
Build `./cmd/orch` locally with Go 1.26+ to try it; no global installation or
host configuration edits are performed automatically.
Building source does not upgrade an installed MCP server. Verify the binary
your client actually launches and its discovered tools; installation and host
configuration remain separate, explicit work.

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
**`orch_run_next`**. At that point the empty-object input restriction applied
to **every tool this server exposed**, not only `orch_run_status`.
**After trial item 3:** discovery returns exactly three read-only tools, adding
**`orch_run_readiness`**. Status and next still accept only `{}`; readiness
accepts only `{"issue_number":447}` with a positive integer. Unknown fields,
including repository overrides, are rejected for **all three tools**. The
positive-issue argument is readiness-only, not a change to status or next.
**After trial item 4:** source discovered exactly **four** read-only
tools, adding **`orch_doctor`**, which accepts `{}` or only an optional `host`
from `claude`, `codex` or `opencode`. Unknown fields and repository overrides
were rejected for **all four tools**. **After separately approved item 5:**
current source discovers exactly **five** read-only tools, adding
**`orch_config_effective`**, accepting only `{}`. Unknown fields, repository
overrides and non-object input (including explicit `null`) are rejected.
Existing tools' input schemas and behavior are unchanged.

| Tool | Input | CLI counterpart (from the bound checkout) | Report schema |
| --- | --- | --- | --- |
| `orch_run_status` | `{}` | `orch run status --json` | 4 |
| `orch_run_next` | `{}` | `orch run next --json` | 1 |
| `orch_run_readiness` | `{"issue_number":447}` | `orch run readiness --issue 447 --json` | 1 |
| `orch_doctor` | `{}` or `{"host":"opencode"}` | `orch doctor --json` or `orch doctor --host opencode --json` | 1 |
| `orch_config_effective` | `{}` | None; directly uses shared `config.Load` | 1 |

Each `orch_run_status` invocation reads fresh local
state through the same engine as `orch run status --json` and returns that
entire document as both `structuredContent` and equivalent JSON text content.
The status document currently has `schema_version: 4`. Mode, consistency
warnings, lock, active run, issue phases and block/resolution histories are
preserved whenever present. Inconsistent state/lock pairs remain inspectable
with warnings. Corrupt state is an explicit MCP tool error (`isError: true`),
not a fabricated Assist result.

**Before item 3:** initialization, discovery, calls (including failures) and
disconnect launched no external commands. **After item 3:** that remains true
for initialization, discovery, disconnect and every status/next invocation;
only readiness adds the external read probes documented below. All three tools
still avoid repository writes and lifecycle mutations, including on failure.
**After item 4:** doctor also uses external read probes, documented in its own
section below. Initialization, discovery, disconnect and status/next remain
local-only; readiness and doctor have `openWorldHint: true`. All four tools
still avoid repository writes and lifecycle mutations, including on failure.
**After item 5:** effective configuration is also local-only
(`openWorldHint: false`). Initialization, discovery, disconnect and all
status/next/configuration calls, including failures, execute no external commands.
All five tools avoid repository writes and lifecycle mutations; readiness and
doctor retain only their existing external read probes.
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
The launch-time MCP binding described above remains fixed for every tool.

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

**Before item 3:** neither of the two inspection tools recorded a session,
metrics, an approval, a decision resolution or lifecycle advancement, nor
mutated git/GitHub. **After item 3:** none of the three inspection tools does
so. **After item 4:** the same holds for all four tools. **After item 5:** the
same holds for all five tools. No inspection records
a session, metrics, an approval, a decision resolution or lifecycle advancement,
nor mutates git/GitHub. **A tool call is
never human approval.** Existing separately authorized grant contracts are
unchanged, but guidance does not inspect, validate or spend a grant; it cannot
claim a grant authorizes any suggested action. These restrictions apply to
**all five inspection tools and all four existing CLI inspection commands**, not just
`Next` or `orch_run_next`. Stateful lifecycle verbs retain their existing behavior.

## Review and merge readiness

Call `orch_run_readiness` with `{"issue_number":447}`, or from the bound checkout:

```sh
orch run readiness --issue 447 --json
```

The CLI accepts exactly that argument shape (substitute a positive integer),
reads no stdin and takes no mutation serializer. Neither entry point accepts a
repository override. Both return the same `ReadinessDoc`, `schema_version: 1`,
as MCP structured content and JSON text. Unknown/ambiguous issues, no active
Delivery run, corrupt state/locks and inconsistent state/lock pairs are explicit
errors, not Assist or ready reports. Invalid tool input is an MCP tool error;
CLI usage errors exit 2, operational errors exit 1. A successfully inspected
but unmet requirement returns a report, not an exit-code assertion of readiness.

Read the three stages separately:

| Evidence | Meaning |
| --- | --- |
| `issue_number`, `run_id`, `phase`, `pr_number`, `pr_url`, `pr_state`, `head_oid` | Recorded issue identity plus live PR identity/head. A missing recorded PR is an unmet prerequisite; no GitHub probes are needed in that case. |
| `review.cycles`, `review.verdict`, `review.evidence` | State's cycle count/verdict plus the exact latest `review-cycle-N` entry from the **issue's durable audit record**, including its `commit_oid` and timestamp when present. Never inferred from the merge-report pin. |
| `review.current`, `review.requirements` | Current means the record agrees with approved work/routing/configuration, cycle history, criterion results/commits and the open PR mirror, at the live head. It does not mean approval: a current request-changes verdict still blocks merge-report. Missing, stale, malformed, duplicate or contradictory evidence is explicitly unverified/not ready. Detail text legitimately rotated out under the body cap is not treated as contradiction. |
| `ci` | Existing GitHub required-check semantics: `passing`, `pending`, `failing` or `no-checks`, with required check names/states/buckets/links and total rollup count. No-checks is not passing; it can mean no CI at all or CI with no required subset. |
| `merge_report.preview` | Existing merge-report fields (schema 1, strategy, head, CI, cycles, configuration revision and explicit no-CI statement), formatted **without invoking `MergeReport`**. Present when effective configuration and live probes support it, even if other requirements fail; its presence is not readiness or a gate. |
| `merge_report.recorded`, `pinned_head_oid`, `head_current` | Whether run state already contains a merge-report pin, its exact head, and whether that pin matches the live head. A pin alone proves neither a valid phase nor approval. `head_drift` flags a differing review or pinned head. |
| `requirements`, `review.requirements`, `merge_report.requirements`, `merge_report.prerequisites_met` | Outstanding local/run, review and merge-report conditions. Prerequisites-met is conservative evidence inspection in `in-review` or an already pinned `awaiting-merge` phase; it is **not** permission to invoke a mutator or a promise it will pass later. Configuration/contract drift, stopped/blocked runs and missing approved work cannot become ready. |
| `merge.authorized`, `merge.requirements`, `merge.approval_required` | Authorized is **always false**. A recorded, current awaiting-merge gate and separate fresh human approval (`approve-merge`) for that PR/pinned head remain necessary, followed by normal merge-time checks. The call cannot pre-approve a future head. Existing grant contracts remain unchanged and are not inspected, validated or spent. |

### External read probes and limits

Readiness, not status/next, opens the existing `ghops` handle in the bound
repository and runs this read-only allowlist through the existing runner:

- `gh auth status`
- `gh pr view <pr> --json number,state,title,url,headRefName,baseRefName,headRefOid,mergeStateStatus,mergedAt,body`
- `gh issue view <issue> --json number,state,title,url,labels,body`
- Existing `RequiredCI`: `gh pr view <pr> --json statusCheckRollup` and, if CI
  exists, `gh pr checks <pr> --required --json name,state,bucket,link`.
- A second PR read brackets the audit/CI probes. Head, state, identity or body
  changes during inspection cause an explicit error and require re-inspection.

Authentication, PR and CI retrieval failures are explicit tool/CLI errors;
unavailable or unparsable issue audit evidence is explicitly unverified in the
report and prevents prerequisites-met. No failed probe becomes passing CI or
approval. The shared `GH.RequiredCI` helper, not readiness-specific parsing,
accepts check arrays with known buckets on exits 0, 1 or 8. A successful empty
array, an empty rollup, or exit 0 with blank/whitespace stdout and the single
`no required checks reported on the '<branch>' branch` stderr notice still
reports `no-checks`. Blank output without that successful notice, nonzero exits
without checks, JSON `null`, and missing/unknown buckets are explicit errors.
Passing/skipping, pending and failing/cancelled check data keep their existing
meanings; a human merge still allows explicit no-checks while a grant merge
still requires passing required CI.

The snapshot is unlocked and GitHub probes are not atomic. Manifest
parsing checks canonical JSON/markdown consistency, **not cryptographic
provenance**; consistently hand-edited records remain undetectable. No new
signing or trust mechanism is claimed.

Readiness never calls the session-writing lifecycle loader, audit-writing `ci`,
state/status/metrics-writing `merge-report`, `merge`, git mutations or a grant
store. It performs no repairs, tests, worktree or host preflights. Existing
review, merge-report, merge and grant enforcement for valid evidence is
unchanged; the stricter evidence inspection does not replace those verbs' own
gates. The shared failed-probe correction described below applies to every
`RequiredCI` caller, including lifecycle commands.

## Doctor diagnostics

Call `orch_doctor` with `{}` or `{"host":"opencode"}`, or run:

```sh
orch doctor --json
orch doctor --host opencode --json
```

`--json` and `--host` may appear in either order, once each. The tool accepts
only the optional host string; empty/invalid hosts, wrong types, unknown fields
and repository overrides are explicit input errors. Selecting a valid but
unconfigured host is also an error (CLI exit 2), as before. Configuration load
failures themselves remain diagnostic failures, not input errors.

Both return `DoctorReport`, `schema_version: 1`, with the engine `version`,
optional `selected_host`, overall `healthy`, and ordered `checks`. Each check
has its existing `name`, a `status` of `success`, `failure` or `advisory`, and
`detail` when present. Informational and skipped-check notes are advisories;
`healthy` means **no failure**, not that every advisory passed. Prerequisite
failures still skip dependent probes rather than inventing successful checks.
Names can recur (for example a successful lock read plus a dead-acquirer note).

One collector runs the existing doctor checks for human-readable CLI output,
JSON and MCP; no rendered text is parsed to recover results. Without `--json`,
the CLI retains its `ok`, `FAIL` and `note` output and repair suggestions.
**Ordinary failed checks return the full report**, with `healthy: false` and
MCP `isError` unset/false; structured content and JSON text agree. The CLI
prints JSON to stdout before exiting 1, with its failure summary on stderr.
Consumers must inspect `healthy`, not equate successful MCP transport with a
healthy environment. Malformed inputs remain tool errors, without a report.

With no host selected, failures for every configured host remain fatal. With
a selected host, its adapter/model/agent-file failures and all common failures
remain fatal; other configured hosts' failures become advisories naming the
selection. Unconfigured hosts are not probed. Authentication still gates the
GitHub repository and release probes. Missing GitHub association remains an
Assist advisory, not permission to bypass Delivery's fail-closed preflight.
Release checks are advisory and skipped on `dev` builds. Memhub `off` skips
probing; `best-effort` failures are advisory and `required` failures are fatal;
recall runs only after a successful health probe.

Configuration/local overrides, adapter/runtime and model selections, generated
agent freshness, managed-block-only project conventions, enabled metrics'
ignore rule, state/lock consistency and dead-acquirer notes retain their existing
semantics. Doctor never performs its suggested repairs or initializes missing
files. It does not record a session, approval, grant use, metrics or lifecycle
advancement, nor mutate git/GitHub. A healthy report is not a Delivery, review
or merge authorization and does not replace the normal policy checks.

### Doctor external read probes

All process working directories and OpenCode location queries use the fixed
bound repository, **not the server's launching directory**:

- PATH lookups for Git, `gh` and configured host executables.
- `git rev-parse --show-toplevel` verifies repository containment.
- `gh auth status`, then `gh repo view --json nameWithOwner,defaultBranchRef,url`.
- For authenticated, non-`dev` builds only,
  `gh api repos/kninetimmy/orch/releases/latest --jq .tag_name` (fixed upstream,
  not the working repository). Network/release errors remain advisory.
- Configured Claude/Codex: `claude plugin list --json` /
  `codex plugin list --json`; Claude also uses `claude --version`.
- Configured OpenCode: `opencode2 --version` (with existing `opencode` V2
  fallback), then the resolved executable's `api get /api/plugin`,
  `api get /api/skill` and `api get /api/model`, each with the existing encoded
  `location[directory]` query. Runtime warnings stay advisory; required plugin,
  live skill-contract and selection checks still run. Raw skill/model settings
  are not included in the report.
- When memhub is not off: `memhub status`, then on success
  `memhub recall "orch memhub recall probe" --json --max-results 1`. These are
  the existing read-only health/retrieval probes, not memhub writes or rendering.

The other checks read local files and process liveness. These are unlocked
observations, not atomic guarantees; external tools may be unavailable or slow.
No tool invocation grants permission to execute a repair from a detail string.

## Effective configuration

Call `orch_config_effective` with `{}`. Each call runs the existing
`config.Load` for the fixed launch-time repository, without loading run state or
locks, taking a mutation serializer, executing commands or performing repairs.
Initialization and discovery do not load configuration, so invalid configuration
does not prevent connection or discovery. Successive calls on one connection
observe file changes and recover after configuration errors.

The report has exactly two members: `schema_version: 1` and `configuration`.
MCP structured content and JSON text contain the same report. `configuration`
is the existing serialized `Config`, **not a new TOML-shaped JSON schema**:

| Configuration member | Meaning |
| --- | --- |
| `SchemaVersion`, `ConfigRevision` | Configuration schema and revision as read by the loader; the tool does not recompute or attest the revision. |
| `Concurrency.MaxSubagents`, `Merge.Strategy`, `Memhub.Mode`, `Metrics.Enabled` | Resolved settings, including existing defaults and permitted local preferences. |
| `Hosts.Claude`, `Hosts.Codex`, `Hosts.OpenCode` | Enabled hosts' `Roles`, with existing role names such as `Architect` and `ReviewDowngrade`; disabled hosts are `null`. Profiles retain `Model`, `Effort` and optional `Variant`. |
| `Protected` (when configured) | `MethodologyVersion` and `Paths`, each with `Path` and `Reason`. This is the configured repository list, not the built-in protected-path floor or a grant authorization. |
| `Overrides` | Sorted dotted keys applied from `config.local.toml`. `null` means no keys applied; it cannot distinguish an absent local file from an empty one. |

These existing JSON names and omission rules are preserved; adding this access
path changes neither configuration revisions nor approval fingerprints.
OpenCode's legacy `Effort` remains the loader's compatibility spelling for the
same-named effective variant. Native `Variant` is not independently synthesized
from legacy `Effort`. For native profiles, an empty variant selects the bare
model and is omitted from JSON. Claude/Codex keep their required `Effort` semantics.

Missing, malformed, unsupported or invalid committed configuration, malformed
or invalid local configuration, and forbidden local policy overrides yield an
explicit tool error (`isError: true`) with the loader's error text and **no
partial report**. Corrupt run state or locks do not prevent valid configuration
inspection. Unlike doctor, a configuration failure is not an unhealthy success
report.

This is an unlocked observation of **current file contents**, including
uncommitted edits and local overrides, not an atomic snapshot, Git HEAD
attestation, health check, or an active run's approved execution configuration.
It does not validate installed hosts/models, survey currency, grants or approval
eligibility. Re-inspect after changes; ordinary lifecycle gates still enforce
their own configuration and approval checks. No inspection records sessions,
metrics, approvals or lifecycle advancement.

### Reuse and retained surfaces

`config.Load` owns parsing, defaults, validation and local overlay policy;
`Config` owns the serialized configuration representation. MCP reuses both
directly and the SDK supplies structured/text output. No CLI output is parsed,
new command or dependency added, or second resolver retained. No existing
implementation is superseded: bare `configure` summarizes committed settings,
`configure-local` summarizes local overrides and interview guidance, `status`
combines mode/state with a configuration summary, and `doctor` probes health.
Those distinct surfaces and the existing revision/fingerprint rules remain.

## Reproducible smoke test and trial checkpoint

From the source checkout, run:

```sh
go test ./internal/cli -run '^TestMCPStdio$' -v -count=1
```

This builds a temporary real `orch` binary and uses an SDK client over its
stdio pipes to initialize, discover and call all five tools, then disconnect and
verify a successful process exit. No global install or client configuration
is needed. The client stays connected while test fixtures change from Assist
to blocked Delivery and inconsistent state/lock pairs. Output reports
`CLI = structured = JSON text` for each decoded status comparison and the
explicit corrupt-state tool error. It also checks strict arguments, repository
binding and unchanged repository files. These tests run in `go test ./...`.
Next-action fixtures additionally cover dependency waits, implementation,
review, merge-report, human merge approval, abandonment, decision resolution
versus recovery, stopped runs, completion and effective configuration drift.
Each successful comparison reports `CLI = structured = JSON text`. Status and
next still run with no external executables available; readiness uses a compiled
scripted `gh` executable that only accepts the read allowlist, covering current
and stale review/pinned heads, missing/contradictory audit evidence, CI states,
failed probes, missing PRs and unknown issues. Bound and launching repository
names, file kinds, modes, modification times and bytes remain unchanged across
inspection. Separate
engine tests cover legacy/unsupported state, invalid configuration, all block
causes and phase prerequisites, including inspection with metrics enabled and
a Claude session id present.
Doctor uses compiled scripted executables with an exact read allowlist, not
live credentials or host services. Its healthy, failed, advisory, selected-host,
configuration-failure and absent-tool reports are compared across CLI JSON, MCP
structured content and JSON text; invalid input is rejected before probes.
It also checks OpenCode warnings cannot leak human text into protocol stdout,
unchanged bound/launching repository bytes, and a clean disconnect. Deterministic
doctor tests compare human output with the same decoded report and cover the
existing configuration, memhub, state/lock and advisory behavior.
Effective-configuration fixtures compare `config.Load = structured = JSON text`,
covering defaults, fresh committed/local values, all host profiles, native and
legacy OpenCode spellings, bare-model overrides, configured protected entries
and sorted override keys. The same connection observes explicit missing,
malformed, unsupported, invalid and forbidden-policy errors and recovery despite
corrupt state/lock data. Strict input, fixed binding, unchanged bound/launching
repositories and clean shutdown are checked with no executables on PATH.
`TestMCPLocalToolsNeverRunExternalCommands` additionally rejects any executable
lookup or runner call during initialization, discovery, local-tool success or
failure, and shutdown. These source checks neither install a binary nor prove
which binary an existing client launches.

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
For readiness, select an issue already in the active run, call
`orch_run_readiness` with its positive `issue_number`, and compare with
`run readiness --issue <number> --json` from the same bound checkout. Review the
exact audited commit against the live head and inspect unmet requirements; do
not call merge-report merely to obtain a read-only preview.
For diagnostics, call `orch_doctor` with `{}`, compare with `orch doctor --json`,
then repeat with the same configured host in `{"host":"opencode"}` and
`orch doctor --host opencode --json`. Inspect `healthy`, individual outcomes and
details even when the tool call succeeds. CLI exit 1 must still carry JSON.
For effective configuration, discover `orch_config_effective` and call `{}`.
Inspect its resolved settings and applied keys, not CLI summary text as though
it were the same report. There is intentionally no new CLI counterpart; shared
loader parity is exercised by the automated test above.

**Before trial item 2:** This increment implements roadmap item 1 (live status)
only. **After trial item 2:** source implements live status and read-only
next-action guidance, not the subsequent readiness/doctor items. **After trial
item 3:** source also implements read-only readiness, but not item 4 doctor.
**After trial item 4:** source implements all four trial tools, including doctor.
The trial checkpoint was to **finish roadmap items 1 through 4, then stop and
assess the benefit against CLI usage before approving later roadmap items**.
**After that checkpoint and separately approved item 5:** current source also
implements effective configuration. No source increment is claimed installed
by this guide. The checkpoint does not authorize subsequent roadmap scope;
each expansion and any installation still require separate approval.

### Original trial benefit checklist — assess with the user before expansion

Use a real client bound to the intended checkout and record observations, not
assumptions or a tool-discovery-only success:

- [ ] **Correct structured evidence:** compare each of the four tool reports
  with its CLI counterpart. Check live status/blocks, next-action prerequisites,
  exact review/live heads and CI evidence, and doctor success/failure/advisory
  details and host selection. Record discrepancies or missing evidence.
- [ ] **Reduced shell construction:** for the same inspection tasks, record
  tool calls versus CLI commands, quoting/cwd/argument construction avoided,
  manual output parsing still needed, and any extra client setup or friction.
  Decide whether the MCP path actually helped rather than claiming savings.
- [ ] **Preserved policy checks:** confirm overrides/malformed input fail,
  failed mandatory probes never become approval or successful checks, repository
  bytes stay unchanged, and separate guard, review, merge and human approval
  gates still apply. Do not
  invoke a mutator merely to test an inspection or treat a call as approval.
- [ ] **User decision:** review these results with the user after items 1–4.
  Stop here unless the user judges the benefit worthwhile and separately
  approves later roadmap scope. Installation, release and later tools are not
  authorized by completing this source change.

## Trial item 2 blast radius

The tables below record the earlier increments; the current five-tool contract,
doctor checks and effective configuration are documented above.

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

## Trial item 3 blast radius

The item-2 table above records that increment, not a claim that item 3 is absent
now. The before/after changes to discovery, inputs and external-command limits
are recorded in their original sections above; status and next retain their
previous contracts. Restrictions on no writes/no approval and fixed repository
binding apply to **every inspection entry point**, not just `Readiness`.

**Before the fix-cycle repair:** the GitHub/CI entry stated "Yes; unchanged
commands, authentication and four-state CI semantics." Initial readiness reused
the helper unchanged, but it accepted blank check output on exits 0, 1 and 8 as
zero required checks, including an exit-1 network/API failure. JSON `null` also
became no-checks, and missing/unknown buckets could fold to passing. Thus a failed
probe could incorrectly satisfy readiness and human lifecycle CI gates despite
the failed-read guarantee above.

**After the repair:** commands, authentication and valid four-state semantics
remain; the shared helper rejects those unsupported/invalid responses as
described under external probes. No readiness-specific CI parser is added.
`Readiness`, `CI`, `MergeReport`, `Merge` (human and grant) and `observeIssue`
through `Resume` all propagate the error before their downstream actions. This
corrects probe handling, not the approval policy: no-checks remains distinct
from passing, allowed for human merges, and insufficient for grant merges.

| Touched element | Does its previous behavior still hold? |
| --- | --- |
| Run inspection (`readiness.go`: `Readiness`, `ReadinessDoc`, `ReadinessReview`, `ReadinessMergeReport`, `ReadinessMerge`, `ReadinessSchemaVersion`, `readinessReviewEvidence`, `sameReadinessAudit`, `finishReadiness`) | New schema-1 evidence inspection only. It does not replace next-action policy or any lifecycle verb; only this inspection adds GitHub reads. |
| Merge-report (`mergereport.go`: `MergeReport`, extracted `mergeReportResult`, existing `checkApprovingReview`/`requireMergeableCI`) | Yes. Pure result formatting is shared with the preview; existing ordering, PR/CI gates, head pinning, phase/status/metrics writes remain in `MergeReport`. The formatter never records a gate, for **any caller**, not just readiness. |
| Reused local prerequisites (`state.Load`, `lockfile.Inspect`, `state.CheckConsistent`, `loadExecutionConfig`, `checkApprovedWork`, `phaseAllowed`, `sameWork`, `headOIDPattern`) | Yes; implementations unchanged. Inspection uses them without `loadVerb` or session attribution. Full-OID validation is also used for readiness evidence; existing review-worktree validation is not weakened. |
| Audit path (`readIssueManifest`, `manifest.Parse`, `manifest.Verification`, engine-owned cycle/criterion name patterns) | Yes; implementations/schema unchanged. Readiness checks existing evidence and the open PR mirror, ignoring only independently rotated verification detail text; it never repairs or writes a record. Canonical consistency still does not prove provenance. |
| GitHub/CI (`openGitHub`, `ghops.Open`, `GH.PR`, `GH.RequiredCI`, `deriveCIState`, `ciChecks`, `MergeReportCI`/`MergeReportResult`/`CICheck`) | Commands, authentication and valid four-state semantics still hold. The shared helper's failed/invalid-probe behavior changes as recorded immediately above; bucket folding itself is unchanged. Readiness preserves check evidence and calls no mutation method. |
| Shared CI consumers (`CI`, `MergeReport`, `Merge`, `observeIssue`/`Resume`, `Readiness`) | Valid-response policies and lifecycle implementations remain. Every caller now receives the same probe error: no CI audit write, merge-report pin/phase/status write, merge/grant spend, resume classification/apply or ready preview follows that error. No approval or grant contract changes. |
| CLI (`run.go`: `runUsage`, `runRunVerb`; `cli.go`: `commands`; run package command/concurrency comments) | Yes for existing commands. Before: only status/next bypassed stdin/serialization among inspections. After: readiness joins them with strict issue argv; all mutators retain serialization and JSON-input handling. Help exposes readiness and clarifies inspection versus mutating plumbing. |
| MCP (`mcp.go`: `runMCP`, SDK registration/handler) | Before: two empty-input local-only tools. After: three read-only tools; readiness alone takes a positive issue and makes external reads (`openWorldHint: true`). Canonical fixed launch binding, status schema 4, next schema 1, strict existing inputs, structured/text parity, diagnostics and clean disconnect remain. |
| Shared CI tests (`ghops/checks_test.go`) | Existing four-state, evidence, precedence and legitimate no-required-check coverage remains. The whitespace fixture now includes the supported successful notice, retaining the former acceptance rule as before/after in its comment. New regressions reject missing/invalid data and preserve all five valid buckets on exits 0, 1 and 8. |
| Engine tests (`readiness_test.go`, `ci_probe_test.go`) | New scripted read-only/failure/evidence/CI coverage and regressions for every shared helper caller, including human/grant merge and resume's two CI-reading phases. Existing lifecycle/review/merge/grant success coverage remains. Snapshots include repository names, kinds, modes, regular-file modification times and bytes; readiness also enables metrics/session context. |
| CLI/MCP tests (`run_test.go`, `mcp_test.go`, `mcp_readiness_test.go`, `testdata/readiness-gh/main.go`) | Existing status/next parity, binding, local-only calls, shutdown and serialization tests remain. Discovery now expects three tools; real readiness stdio comparisons add a command allowlist and unchanged bound/launching repositories, including failures. Before the fix cycle the fake probe failed with exit 2; now it uses exit 1 with empty stdout and asserts explicit CLI/MCP errors instead of a ready result. The fake executable writes only its external test transcript, never the repository. |
| Guides (`docs/mcp-status.md`, `README.md`) | Source-only setup, optional use, host enforcement and four-item checkpoint remain. Historical status-only/empty-input/no-exec/plumbing claims are retained as before/after where originally stated, rather than silently removed. |
| Lifecycle loading/review/merge/grant implementations, next/status algorithms, persistent schemas, dependencies, adapters/guards, installed binaries and CI workflows | Unchanged apart from the shared probe-error propagation and pure merge-report formatting described above. No new authorization, automatic action, schema migration, dependency, host redesign, installation or item-4 doctor feature. All existing CI requirements remain. |
