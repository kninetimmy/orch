# Read-only status, next actions, readiness, doctor, configuration, metrics and evaluations over MCP

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

**Before item 7:** the launch syntax was `orch mcp [--repo <path>]`.
**After item 7:** those forms still work; optionally opt in to retained evaluation
inspection with one explicit, existing absolute local storage root:

```text
orch mcp --repo /absolute/path/to/main-checkout --eval-storage-root /absolute/local/evaluation-storage
```

The two flags may appear in either order, once each. Storage-only argv retains
the cwd repository binding. No store is guessed from configuration, environment,
run IDs or ancestors, and no directory is created. The existing evaluation path
verifier rejects missing/non-directory roots, traversal, network/device paths,
symlinks/junctions, Windows drive/short-name aliases and unverifiable identities.
Resolve trusted OS aliases before selecting the root. Verification is repeated
on each evaluation call, not just at launch. Without this opt-in the server and
its other tools still work; `orch_eval_status` returns an explicit binding error.
No tool request can supply or override **either** root.

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
source discovered exactly **five** read-only tools, adding
**`orch_config_effective`**, accepting only `{}`. Unknown fields, repository
overrides and non-object input (including explicit `null`) are rejected.
Existing tools' input schemas and behavior are unchanged.
**After separately approved item 6:** source discovered exactly **seven**
read-only tools, adding **`orch_metrics`** and **`orch_metrics_report`**. Each
requires an explicit empty object `{}`; omitted arguments, `null`, other
non-objects, unknown fields and repository overrides are errors. Existing five
tool contracts remain unchanged.
**Before item 7:** seven-tool discovery had no evaluation inspection.
**After separately approved item 7:** current source discovers exactly **eight**
read-only tools, adding **`orch_eval_status`**, accepting only a nonempty string
`run_id`. Its stricter evaluation-ID syntax is enforced by the existing reader.
The explicit-empty-object restriction still applies to configuration and both
metrics tools, not every tool. Unknown fields and root overrides are rejected
for **all eight tools**; the existing seven contracts are unchanged.

| Tool | Input | CLI counterpart (from the bound checkout) | Report schema |
| --- | --- | --- | --- |
| `orch_run_status` | `{}` | `orch run status --json` | 4 |
| `orch_run_next` | `{}` | `orch run next --json` | 1 |
| `orch_run_readiness` | `{"issue_number":447}` | `orch run readiness --issue 447 --json` | 1 |
| `orch_doctor` | `{}` or `{"host":"opencode"}` | `orch doctor --json` or `orch doctor --host opencode --json` | 1 |
| `orch_config_effective` | `{}` | None; directly uses shared `config.Load` | 1 |
| `orch_metrics` | `{}` | `orch metrics` (human-readable; shared summary results) | 1 |
| `orch_metrics_report` | `{}` | `orch metrics report` (human-readable; shared rollup results) | 1 |
| `orch_eval_status` | `{"run_id":"eval-aaaaaaaaaaaaaaaaaaaaaaaaaa"}` | `orch eval status --run ID --storage-root ROOT --json` (same retained report; MCP additionally checks repository identity) | 3 |

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
**After item 6:** both metrics tools are also local-only
(`openWorldHint: false`). All seven tools avoid repository writes and lifecycle
mutations. Initialization, discovery, disconnect and status/next/configuration/
metrics calls execute no external commands; readiness and doctor retain their
existing external read probes only.
**After item 7:** evaluation inspection is also local-only (`openWorldHint: false`).
All eight tools avoid repository/storage writes and lifecycle mutations;
initialization, discovery, disconnect and every local-only tool call execute no
external commands. Readiness and doctor keep their existing read probes only.
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
**all five inspection tools and all four existing CLI inspection commands** at
item 5; after item 6 they apply to **all seven tools and all six CLI inspection
commands**, not just
`Next` or `orch_run_next`. Stateful lifecycle verbs retain their existing behavior.
After item 7 these no-write/no-approval restrictions apply to **all eight MCP
tools** and the retained evaluation CLI `status` inspection too. They do not
describe evaluation `preview`, `run`, `stop`, `grade` or report publication, which
remain distinct, explicitly invoked operations with their existing contracts.

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

## Recorded metrics history and cross-run report

Call `orch_metrics` or `orch_metrics_report` with an explicit `{}`. Their CLI
counterparts remain human-readable, without a new JSON flag:

```sh
orch metrics
orch metrics report
```

Each tool returns `schema_version: 1`, current `enabled`, and
`history_scope: "recorded-metrics-only"`, plus `runs` (history) or `report`
(rollup). MCP structured content and JSON text are equivalent. Both CLI and MCP
use the same read-only inspectors, `summarizeRun` and `buildMetricsReport`;
review-cycle means, per-merged-issue counters, architect shares and elapsed sums
also share their calculations. Explicit JSON encoders expose these existing
private results; they do not parse CLI output or implement a second metrics
engine. The previous CLI-only loading loops are replaced by the shared paths.
Human CLI formatting, success/error meanings and legacy storage stay unchanged.

### History schema

`runs` contains one summary per recorded metrics document, in the filename order
returned by `metrics.LoadAll`:

| Summary members | Meaning |
| --- | --- |
| `run_id`, `event_count`, `first_at`, `last_at` | Recorded run identity and event bounds; bounds are empty strings with no events and may be unparseable legacy timestamps. They do not measure active time. |
| `issues_seen`, `merged`, `abandoned`, `blocked`, `block_classes`, `escalations` | Existing issue and event counts; repeated blocks count again, while merged/abandoned counts use distinct issue numbers. |
| `review_cycles`, `reviewed_issues`, `first_pass_approve`, `ci_by_state` | Verdict-bearing review counts (not executor usage siblings), first-review approvals and the last recorded CI state per issue. |
| `legacy_usage` | Separate lifecycle-event rows: `index` (1-based), `verb`, `issue_number`, `role`, `review_cycle` and original `usage`. Missing role is empty, not inferred except the existing reviewer fallback. Review cycle 0 means no recorded review cycle. Legacy usage retains absent fields versus explicit zero, including unclassified `duration_ms`. |
| `usage_samples` | Original `observation` attribution, requested/observed profiles and source evidence, plus its contributed `counters` (not necessarily the original cumulative values). Missing counters are omitted; measured zero is 0. |
| `usage_groups` | Compatible `key: {host, source, stream}`, `samples` and named `counters`. Every counter has `total` (integer or null) and `measured` (number of contributing samples). |
| `recorded_usage_roles`, `missing_usage_roles`, `usage_sessions`, `missing_usage_sessions`, `unknown_role_records`, `incomplete_session_records` | Existing coverage evidence. Session labels are host/session; missing sessions include only known recorded identities. The complete native session count is unknown. |
| `unavailable` | Recorded observations with explicit unavailable reasons, not zero usage or failure outcomes. |
| `timing` | Present interval kinds only: `intervals`, `incomplete_sessions`, `session_effort_ns` and `wall_clock_ns`. Session effort unions intervals per complete host/session and sums sessions; wall clock unions all intervals. Session effort is null if no complete identity was measured, a known-session subtotal if some identities are incomplete. An absent kind is unknown; a measured zero duration is 0. |
| `reported_outcomes`, `engine_outcomes` | Separate reported outcome counts and engine approval/escalation counts; other historical events remain unclassified. |

The six counter names are `input_tokens`, `output_tokens`, `cache_read_tokens`,
`cache_creation_tokens`, `total_tokens` and `reasoning_output_tokens`. Total and
reasoning counters are independent, never synthesized from splits. The existing
`CounterContributions` replay scopes baselines by run, host, session, source and
stream. Cumulative samples contribute only increases; omission contributes
nothing and retains the baseline. No native total combines different groups or
legacy lifecycle usage. A counter with `measured: 0` has `total: null`, not 0.
Empty optional collections can be `null`; this means no records, not measured
zero or complete coverage. The top-level empty history is `runs: []`.

### Rollup schema

`report` contains the existing cross-run results:

| Member | Meaning |
| --- | --- |
| `runs` | Number of recorded metrics documents, not all Delivery runs. |
| `routes` | Groups with `key: {role, executor, reviewer, downgraded, approval}` and `outcomes`. Profile labels retain explicit bare variants; unrecorded dimensions are `"unknown"`, never guessed approvals or false routing facts. |
| `dimensions` | Ordered `title`/`groups` breakdowns by host, any risk domain declared, unusually difficult, and downgrade facts mechanical, low risk, fully specified and unsurprising. Each group has `value` and `outcomes`. |
| `usage` | Separate observed host/source/stream and `lifecycle: true` legacy groups, with `key`, `samples`, contributing `runs`, `merged_runs`, named `counters` and `architect_counters`. Legacy groups have empty key fields because those identities were not recorded. `per_merged_issue` and `architect_share_percent` map counter names to numbers or null. No measurements or no merged denominator means no mean; no architect evidence or a zero total means no share (CLI prints n/a for a measured zero denominator). |
| `runs_without_arch_use` | Runs without recorded architect-role counters; this does not mean the architect used zero tokens. |
| `elapsed` | Ordered `{run_id, elapsed_ns, measured}` rows. Elapsed time is first-to-last recorded event wall clock, not active time; missing/unparseable/reversed bounds yield null and false. A parseable zero span is 0 and true. |
| `elapsed_total_ns`, `elapsed_measured` | Existing sum of measured run wall-clock times and its coverage count; total is null with no measured runs. This sum is not a union across runs or total active time. |
| `escapes`, `unattributed` | Distinct merged PRs declared fixed by plan issues, attributed through recorded merge events; unrecognized PR numbers remain unattributed. Escape rows retain `pr`, fixer/origin run and issue, plus `origin.route`, first verdict, review cycles (null if unreviewed) and merge approval. Repeated declarations count once; the first recorded run/issue declaration names the fixer. |

Each `outcomes` has `issues`, `merged`, `abandoned`, `blocked`, `escalations`,
`reviewed`, `first_approved`, `review_cycles`, `merge_approval`, `fix_cycles`,
`review_blocks`, `cause_unrecorded` and `escaped_defects`.
`review_cycles_per_reviewed_issue` is null with no reviewed issues. Request-change
fix cycles, wrong-criterion/fix-cycle-limit blocks and legacy unrecorded causes
remain distinct. Escaped defects are **declared attribution**, not independent
defect discovery or proof of causation.

### History and inspection limits

These are **recorded metrics**, not a complete run-history archive. Only existing
`.orchestrator/metrics/run-*.json` documents are loaded; disabled periods,
unrecorded sessions, removed files and never-recorded runs cannot be recovered
from current state, Git or GitHub. Currently disabled metrics can still have
visible recorded history. Empty history is successful whether enabled or
disabled; the rollup then has zero runs and unknown/unmeasured values. Schema-1
and schema-2 history remain readable without migration. Invalid configuration,
corrupt/unsupported/mismatched history, invalid observations and aggregation
overflow are explicit tool errors, with no partial structured report, matching
the CLI's operational failure meaning rather than fabricated empty history.

Each call reloads configuration and history in the fixed launch-bound repository
and can recover on the same connection after errors. Reads are unlocked and not
an atomic snapshot. Neither inspector reads run state/Delivery locks nor creates
metrics, history, mutation locks, sessions, approvals or lifecycle records. No
subprocess runs, even with metrics enabled or a host session identifier present.
No dependency, host plugin or installed binary is changed by this source feature.
Before item 7 this was seven-tool source; after item 7 it is eight-tool source.
Building it does not upgrade or verify an existing five-tool
installation; discover the tools from the binary the client actually launches.

## Retained evaluation inspection (explicit storage opt-in)

Launch with `--eval-storage-root` as above, then call `orch_eval_status` with only
`{"run_id":"eval-aaaaaaaaaaaaaaaaaaaaaaaaaa"}` (replace the example with a retained
evaluation ID). The ID must match the existing `eval-` plus 26 lowercase base32
characters (`a-z`, `2-7`), not a Delivery run ID, plan digest or path. Missing,
empty, wrong-type and invalid IDs, non-object input, unknown fields and attempted
storage/repository overrides are errors. Discovery is available without a store;
calling the tool without the launch binding is not an empty or successful report.

The tool reuses `evalplan.Inspect`, the report inspection underlying CLI `status`,
through `InspectRepository`; it does not parse CLI text or create a second
evidence/grading/measurement engine. Success returns the existing schema-3
`Report` as equivalent MCP structured content and JSON text, preserving:

- `snapshot_sha256`, the proposed report `destination`, evaluation/plan identities
  and hashes, frozen scope/limits/schedule and retained approval **assertions**;
- progress, consumed attempts/repairs, unrun units, completed evidence counts,
  snapshot chain and digest-linked artifact/evaluator references;
- existing initial/final grading, disputes/coverage/trust limits and typed native
  evidence; retained evaluator identities/independence are not authentication;
- separate compatible counters, coverage, matched values/repeat ranges and receipt
  wall spans, with absent values/identities remaining unknown and measured zero
  remaining zero; task-agent subtotals do not establish whole-Orch cost;
- blockers, safety findings, unknowns, inconclusive decisions and undefined cost
  per acceptance when no validated nonzero denominator exists.

The existing public projection suppresses raw worker output, private source/key/
probe bytes, arbitrary controller reasons, native error prose and evaluator
citations/reason prose. References and digests are returned, **not file contents**.
Reading referenced bytes to verify integrity does not execute them. An inspection
does not publish a report, submit a grade, execute work, stop a controller, inspect
process liveness, resume, spend approval or adopt a profile. `destination` and
`reproduction_argv` are retained metadata, not proof of publication or permission
to execute the mutating `report` command. Process liveness stays explicitly unknown;
stop receipt and recorded controller acknowledgement remain distinct.

`InspectRepository` verifies that the stored repository is an existing guarded
local directory, matches the launch-bound checkout's canonical path **and opened
filesystem identity**, and belongs to the frozen preparation's repository roots.
A different checkout (including a sibling worktree), missing/unverifiable stored
repository or alias is an explicit tool error, with no partial report. This added
binding applies to **MCP evaluation inspection**, not all evaluation APIs:
`Inspect`/CLI `status` still inspect retained evidence without needing the original
checkout, source artifacts, effective configuration or live hosts. Other MCP
tools retain their existing fixed repository binding and validation semantics.
Filesystem identity checks establish the current local directory match, not
authenticated historical provenance or protection against a privileged actor
consistently rewriting all retained records.

The existing guarded reader validates the bounded inventory, generated wire
shapes/versions, progress chain, approval claims, grading journal and referenced
hashes. Malformed/corrupt/unsupported data, missing required referenced bytes,
persistent hard links, linked directories and identity failures are explicit
errors, without repairs. A valid prepared/running snapshot, unfinished attempt,
unpublished pending file or interrupted grading publication instead retains the
reader's existing incomplete/unknown status; incomplete evidence never becomes
successful execution or measured zero. Reads are unlocked observations of retained
immutable records, not atomic live-host guarantees. No storage, report bundle,
lock, approval claim, session, metric or lifecycle record is created, on success
or failure. `evalplan.Status` (core progress-only inspection), CLI status, immutable
report publication and grading/execution/stop remain useful distinct surfaces.

## Reproducible smoke test and trial checkpoint

From the source checkout, run:

```sh
go test ./internal/cli -run '^TestMCPStdio$' -v -count=1
```

This builds a temporary real `orch` binary and uses an SDK client over its
stdio pipes to initialize, discover and call all eight tools, then disconnect and
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
Metrics fixtures compare human CLI output with shared-result formatting and
shared JSON with both MCP representations, across empty/enabled/disabled,
legacy, observed and corrupt histories on one connection. They cover omitted
cumulative fields, measured zeros, independent totals/reasoning, separate
hosts/sources/streams/sessions, coverage, interval unions, routing/outcome
dimensions, elapsed unknowns and deduplicated/unattributed escaped defects.
Configuration/history/replay/overflow failures retain CLI/shared error parity.
Raw stdio additionally checks omitted arguments (the SDK client substitutes an
empty object for nil arguments), unchanged files and real process shutdown.
`TestMCPLocalToolsNeverRunExternalCommands` additionally rejects any executable
lookup or runner call during initialization, discovery, local-tool success or
failure, and shutdown. These source checks neither install a binary nor prove
which binary an existing client launches.
Evaluation fixtures compare `CLI = shared report = structured = JSON text` for
prepared, running/unfinished, refused and graded snapshots. They verify snapshot
hashes, approval assertions, private-output suppression, unknown versus measured
zero counters, explicit/absent storage binding, both flag orders and cwd binding,
strict IDs/inputs, cross-repository rejection, unsafe paths/links, corrupt/partial/
unsupported evidence and recovery on the same connection. Repository/storage
names, kinds, modes, regular-file modification times and bytes are unchanged
across reads, including failures; no executable is available on PATH. Synthetic
retained fixtures and evaluator assertions are not model-trial performance.

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
implements effective configuration. **After separately approved item 6:** source
also implements recorded metrics history and rollups. No source increment is claimed installed
by this guide. The checkpoint does not authorize subsequent roadmap scope;
each expansion and any installation still require separate approval.
After separately approved item 7, source also exposes retained evaluation
inspection with explicit storage opt-in. It adds no installation, remote access,
evaluation execution or report-publication authority.

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

The tables below record the earlier increments; the current eight-tool contract,
doctor checks, effective configuration and metrics are documented above.

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

## Item 7 blast radius

The before/after discovery, launch and local-command statements remain in their
original sections above. No existing implementation is superseded by this tool.

| Touched element | Does its previous behavior still hold? |
| --- | --- |
| CLI command/help table (`cli.go`: `commands`) | Yes for dispatch and every existing command. MCP help now advertises retained evaluations and optional storage argv; exit-code mapping and all mutators are unchanged. |
| MCP launch (`mcp.go`: `runMCP`, strict argv parsing, repository binding) | Existing cwd/`--repo` forms and canonical repository/real-metadata checks remain. Before: only optional `--repo` was accepted. After: one optional `--eval-storage-root` is also accepted, in either flag order, verified without creation; requests cannot alter either binding. No implicit store, config/environment lookup or ancestor discovery is added. |
| MCP registration, handler and SDK transport/output | Seven-tool-only discovery changes to eight as recorded above. New `orch_eval_status` takes only nonempty `run_id`, is read-only/local-only and returns the existing report; the other seven tools' schemas/annotations/results, protocol-only stdout, stderr diagnostics and clean disconnect hold. No mutation or remote transport is exposed. |
| Evaluation path boundary (`guarded.go`: new `CheckStorageRoot`; reused `openGuarded`, `guardedDir.check`; `localPath`, `directory`, `openDirectory`, platform drive/reparse/link checks) | Existing validation/anchoring holds for **every guarded evaluation read/write**, not just the new launch check. `CheckStorageRoot` only opens/verifies/closes an existing root; each inspection repeats the existing checks. No publication, storage creation or weaker alias fallback is introduced. |
| Shared report inspection (`report.go`: `Inspect`, extracted `inspect`, new `InspectRepository`) | Original CLI/`Inspect` behavior, including source/checkout-independent retained inspection, holds. MCP adds guarded canonical-path/opened-directory identity and frozen-root membership checks before report construction; this restriction belongs to `InspectRepository`, not every evaluation API. Both entry points share one report implementation and unchanged schema/hash semantics. |
| Reused report/evidence types and readers (`Report`, `Snapshot`, `openEvaluation`, `readProgress`, `readApproval`, `inspectGrading`, `publicNative`, `measure` and their guarded record/artifact readers) | Existing wire shapes, ID rules, bounds, chain/hash/approval/grading validation, public projection, counters/missingness, blockers and unknowns hold. No second parser, raw-output exposure, grade submission, report publication or liveness inference is added. |
| MCP tests (`mcp_test.go`: `TestMCPStdio`, `mcpDiscover`, local no-command regression; new `mcp_eval_test.go` fixtures/parity/error/launch checks) | Existing seven-tool parity/binding/no-write/shutdown and probe allowlists hold; discovery now expects eight and checks the evaluation-only schema/local annotation. Deterministic real-stdio cases add report parity, binding/identity/path/evidence/unknown/privacy/no-write coverage. Test-only retained data/grade setup is not production execution or trial approval. |
| Guides (`docs/mcp-status.md`, `README.md`) | Optional source-only setup, explicit installation boundary, host enforcement and prior checkpoint hold. Previous launch/discovery limits are retained as before/after in the same sections; storage opt-in, identity scope and report limitations are explicit. |
| Core progress `Status`, CLI evaluation parser/status/run/stop/grade/report, publication, lifecycle/state/lock/approval/metrics/grant algorithms, persistent schemas, dependencies, adapters/guards, installed binaries and CI | Unchanged. No useful CLI surface is removed, no execution/approval rule is relaxed and no installation, release, schema migration or remote access is added. Required existing checks still apply. |
