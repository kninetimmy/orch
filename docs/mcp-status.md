# Local inspections, configuration interviews, bounded evaluation preparation and plan decision gates over MCP

`orch mcp` is an optional local stdio MCP server using the official Go SDK
v1.8.0. This is an unreleased source feature, not part of the v0.15.2 binary.
Build `./cmd/orch` locally with Go 1.26+ to try it; no global installation or
host configuration edits are performed automatically.
Building source does not upgrade an installed MCP server. Verify the binary
your client actually launches and its discovered tools; installation and host
configuration remain separate, explicit work.

**Before item 13:** this guide described an exclusively read-only MCP surface.
**After separately approved item 13:** evaluation preview can persist a bounded
preparation artifact; it is not read-only and grants no execution authority.
The eight earlier tools retain their existing inspection contracts.
With issue #468 also integrated, that source catalog contained **ten tools**:
nine read-only inspections (including issue context) and bounded preview.
**After separately approved item 10:** the catalog contained **eleven tools**,
adding the non-activating plan decision gate. The preceding ten contracts hold;
the gate's existing external memhub probes may have memhub-owned side effects.
Before plan-gate integration, item 12 on the ten-tool baseline exposed **twelve
tools**, including both read-only configuration interviews. With items 10 and 12
integrated, the source catalog contained **thirteen tools**. Preview alone may
persist preparation; the gate retains its separate external side-effect boundary.
**After separately approved item 8:** current source contains **fourteen tools**:
twelve read-only inspections/interviews, adding approval/grant evidence, plus
bounded preview and the plan gate. All thirteen preceding contracts hold.

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
After item 13, `orch_eval_preview` also requires that same launch-time storage
binding; no new flag, implicit storage selection or directory creation is added.

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
**After separately approved item 7:** source discovered exactly **eight**
read-only tools, adding **`orch_eval_status`**, accepting only a nonempty string
`run_id`. Its stricter evaluation-ID syntax is enforced by the existing reader.
The explicit-empty-object restriction still applies to configuration and both
metrics tools, not every tool. Unknown fields and root overrides are rejected
for **all eight tools**; the existing seven contracts are unchanged.
**Before item 13:** that eight-tool catalog contained no preparation operation.
**After item 13:** discovery adds **`orch_eval_preview`**, accepting only a
nonempty repository-relative `plan_file`. It is annotated `readOnlyHint: false`,
`destructiveHint: false`, `idempotentHint: true`, `openWorldHint: false`:
unchanged inputs replay immutable local preparation, not remote execution.
Unknown fields and repository/storage overrides are rejected for this tool too.
The preceding eight tools keep their schemas, annotations and results.
Issue #468 separately adds **`orch_issue_context`**, taking a positive
`issue_number` and remaining read-only and command-free. Together these
increments exposed ten tools before item 12; preview alone may persist preparation.

**Before item 12:** the eleven-tool catalog, including item 10, exposed no configuration
interview. **After item 12:** this increment adds **`orch_configure_step`** and
**`orch_configure_local_step`**, for **thirteen tools** on that baseline. Both accept the
existing schema-2 `AnswerSet`, not an empty-object request or a repository
override. The other eleven contracts remain unchanged. Earlier tool counts
describe their respective increments, not the current cumulative catalog.

**Before item 10:** the integrated issue-context/preview catalog exposed ten
tools, with no plan decision gate. **After separately approved item 10:** source
discovered **eleven** tools, adding **`orch_plan_gate`** with only
`{"plan": {...}}`. The existing ten tools keep their schemas, annotations and
behavior. Unknown outer fields and repository overrides are rejected for
**all eleven tools at that increment**; the gate also uses the engine's strict nested plan decoder.
The new gate is non-activating, but conservatively **not annotated read-only or
idempotent** because of its external memhub probes; see its dedicated section below.
With item 12 integrated, unknown outer fields and repository overrides remain
rejected for **all thirteen tools**; the gate and interviews retain distinct input shapes.

**Before item 8:** the thirteen tools above exposed no combined approval/grant
snapshot. **After separately approved item 8:** source discovers **fourteen** tools,
adding `orch_approval_status` with an explicit empty-object input. The earlier
thirteen schemas and behaviors remain; the appended item-8 section specifies the
new tool's evidence-only meaning and Git read probes. Unknown fields and root
overrides remain rejected for **every tool**, not only `orch_approval_status`.

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
| `orch_issue_context` | `{"issue_number":468}` | None; directly uses shared `run.IssueContext` | 1 |
| `orch_eval_preview` | `{"plan_file":"plans/evaluation.json"}` | `orch eval preview --plan FILE --json` (same preparation; MCP additionally confines the proposal file and binds storage) | Existing proposal version 1, 2 or 3 |
| `orch_plan_gate` | `{"plan":{...}}` | `orch run plan` (submitted plan JSON on stdin) | 3 |
| `orch_configure_step` | `{"schema_version":2,"answers":{}}` | `orch configure --step` (AnswerSet on stdin) | 2 |
| `orch_configure_local_step` | `{"schema_version":2,"answers":{}}` | `orch configure-local --step` (AnswerSet on stdin) | 2 |
| `orch_approval_status` | `{}` | None; directly combines shared `run.Status` and grant observation | 1 |

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
**After item 13:** those no-write/no-command guarantees still hold for the
preceding local-only inspections, initialization, discovery and disconnect.
Preview alone adds the existing local read-only Git queries and bounded storage
publication described below. Thus **not every MCP tool is read-only or
command-free**; no tool gains lifecycle or execution authority.
**After item 12:** both configuration interviews add the existing setup read
probes, without writes or lifecycle authority. Effective-configuration inspection
(`orch_config_effective`) remains command-free; the interviews do not.
**After item 8:** approval status also uses read-only Git probes, with no writes.
All twelve read-only inspections/interviews retain no-write contracts; preview
alone may persist bounded preparation, while the gate keeps its separate external
memhub boundary below. Initialization, discovery, disconnect and local-only calls remain
command-free; no tool gains approval or lifecycle authority.
This server does not replace the CLI, the thin host plugins or their guards,
and it does not change Orch's separate memhub CLI integration.
**Before item 10:** the ten-tool catalog had only the inspection probes and
bounded preview writes described above. **After item 10:** those ten retain
their limits; the new gate runs the CLI gate's existing memhub probes, without
suppressing memhub-owned side effects. It still creates no Orch, Git or GitHub
artifacts and performs no lifecycle transition. Initialization, discovery and disconnect remain local
and command-free; no gate is evaluated merely by connecting.

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
After item 13, the original eight inspections still never write. MCP preview is
the explicit exception for preparation persistence, not approval: the no-session,
no-metrics, no-approval, no-lifecycle and no-Git/GitHub-mutation restrictions hold
for **every MCP tool**, including `orch_eval_preview`.
After item 10, the no-session/no-approval/no-lifecycle/no-Git-or-GitHub-mutation
restrictions also hold for `orch_plan_gate`, hence for **all eleven MCP tools at that increment**.
The blanket no-storage-write guarantee does not extend to the gate's external
memhub implementation; its narrower side-effect boundary is documented below.
With item 12 integrated, the no-session/no-approval/no-lifecycle/no-Git-or-GitHub-
mutation restrictions hold for **all thirteen MCP tools**; the gate's external
memhub exception and preview's bounded preparation writes remain unchanged.
After item 8, these restrictions hold for **all fourteen MCP tools**, including
approval/grant observation; the same two write/side-effect exceptions remain.

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

## Bounded evaluation preview/preparation (item 13)

With the existing `--eval-storage-root` launch opt-in, call only:

```json
{"plan_file":"plans/evaluation.json"}
```

`orch_eval_preview` returns the existing `evalplan.Record`: `schema_version`
(the proposal's supported version), `kind: "maintainer-preparation-record"`,
`plan_digest`, `storage_destination`, normalized `plan`, and `preview` evidence
including the complete schedule, finite counts, checks, unknowns and blockers.
MCP structured content and JSON text are equivalent to CLI/shared preparation.
Private corpus/profile/decision/readiness contents remain references and digests,
not returned bytes. Preparation does not validate opaque readiness claims,
decision semantics, historical worker source bytes or native isolation.
`execution_available` and `approval_granted` remain false. A successful blocked
preview is preparation, **not execution eligibility or human approval**.

The proposal must already exist under the fixed checkout. `PreviewRepository`
requires a portable slash-separated relative path (for example
`plans/evaluation.json`), not an absolute path, `.`/`..`, a backslash alias or a
network/device/ambiguous path. The existing guarded reader anchors every parent
directory and checks opened identities, reparse/symlink status, regular-file
identity, persistent hard links and the 64-KiB plan bound. Missing, linked,
unverifiable and escaping paths fail closed. Both repository and store are
guarded again on every call; identity changes before publication refuse.

Only the **proposal-file** restriction is new and entry-point specific:
`PreviewRepository`/MCP enforce it, not CLI `Preview` or every evaluation API.
Referenced artifacts retain existing preview rules, including explicit absolute
local paths and nontraversing proposal-relative paths, guarded regular files,
size/digest checks and existing exclusion rules. They are not implicitly confined
to the checkout. CLI `orch eval preview --plan FILE [--json]` still accepts its
existing absolute/external proposal paths and proposal-selected storage.

Both entry points share `preparePreview`, strict JSON/version decoding and
`normalize`; no second planner, artifact validator, source-pinning algorithm,
schedule, digest or evidence generator is introduced. After normalization and
**before publication**, the proposal's canonical `storage_root` must match the
launch-bound guarded root in both canonical path and opened filesystem identity
(case folding alone cannot equate distinct case-sensitive directories).
Repository roots (including registered worktrees),
Git/common metadata and declared worker/scratch roots retain their existing
nonoverlap checks. Publication uses the already opened bound storage handle,
never a destination supplied by the tool request or an unchecked proposal path.

### Exact write footprint and interruption limits

This tool is **not read-only**, despite the word preview. Its only writes are
the existing immutable preview publication footprint directly in the existing
bound root: one `<64-lowercase-hex-plan-digest>.json` public record (at most
16 MiB), a fresh exclusive `.pending-<random>` file while publishing, and removal
of that invocation's own pending name after publication or a lost publication
race. It creates no subdirectory or root. Bytes are written, synced and closed
before atomic no-replace linking. Identical existing records succeed without
rewriting bytes or mtimes; conflicting, corrupt or unverifiable records fail
without replacement. Interrupted writes may leave a partial/complete pending
file, or a completed digest name with its pending hard link if cleanup failed.
Such failures are errors, not successful partial preparation. Unknown pending
files are preserved, never swept; a persistent alias at the completed name fails
closed after the existing bounded publisher grace period.

No evaluation ID, execution/progress record, worker/scratch directory, controller,
approval/assertion, grade, report bundle, Delivery run, configuration, session,
metric, Git mutation or GitHub artifact is created. Existing CLI execution and
approval gates remain separate and unchanged. A returned preparation digest is
neither approval nor an instruction to run a model. Path/identity checks provide
local binding, not authenticated historical provenance, protection from a
privileged filesystem actor or an atomic snapshot of concurrently edited inputs.

### Existing read-only Git probes

Preview uses only these existing local Git argument vectors in the fixed repo:

- `git --no-optional-locks --no-pager -c core.fsmonitor=false cat-file -e <full-OID>^{commit}` for each pinned baseline/candidate revision;
- `git --no-optional-locks --no-pager -c core.fsmonitor=false rev-parse --path-format=absolute --show-toplevel --git-common-dir --git-dir`;
- `git --no-optional-locks --no-pager -c core.fsmonitor=false worktree list --porcelain -z`.

They retain `GIT_TERMINAL_PROMPT=0`, `GIT_NO_REPLACE_OBJECTS=1`,
`GIT_NO_LAZY_FETCH=1`, `LC_ALL=C`, the existing timeout/output bounds and explicit
failure handling. No fetch, source export, hook, `gh`, memhub, host executable or
supplied artifact command runs. The no-command restriction on `orch_eval_status`
still holds for that inspection, not every evaluation tool after item 13.

## Configuration interview steps (item 12)

Start either `orch_configure_step` or `orch_configure_local_step` with
`{"schema_version":2,"answers":{}}`. Present the returned questions using the
existing native-question and pagination contract, then resubmit **all** collected
answers, keyed by question ID, on each call. These stateless tools return the
entire existing schema-2 `question.Document` as equivalent structured content and
JSON text: questions, progress, catalog choices/defaults, proposed file contents
and diffs, summary blockers, completion or abort. Pagination navigation is never
an answer. Removing or revising answers re-derives the sequence; unknown,
unreachable, stale and invalid answers remain explicit errors.

The strict `question.DecodeAnswers` decoder is reused. Unknown outer fields,
unsupported versions, non-object requests and non-string answer values are
errors; omitted/null `answers` retains the decoder's empty-map normalization.
Clients should always send the explicit initial shape above. Answers cannot
supply detection facts, completed documents, paths or repository overrides.
The AnswerSet input restriction applies to **both new tools**, not all MCP tools;
the existing tools keep their individual schemas. Fixed launch binding and
rejection of unknown outer fields continue to apply to every tool.

Every call rechecks the existing active-Delivery/held-lock refusal **before
probes**, decodes the answers, re-detects host facts and calls
`interview.NextConfigure` or `NextConfigureLocalWithFacts`. No CLI subprocess or
stdin/stdout handler is invoked or parsed. Configuration is re-read each time:
configure defaults use committed configuration alone; configure-local uses the
existing committed-plus-valid-local-preferences repair semantics. In particular,
malformed local TOML remains repair data, not a newly introduced fatal loader
error. Missing/invalid committed configuration and catalog failures keep the
existing interview errors. Catalog availability is required for OpenCode role
edits, not unrelated edits. Native variants, bare models, legacy profile
compatibility and retained unavailable committed selections keep their current
question/default/materialization rules.

### Binding, probes and authority

The server still requires the selected checkout's real `.orchestrator/` directory
at launch. It does not search ancestors or bootstrap an uninitialized repository.
**First-time `init` remains with the existing CLI and host setup workflow**; this
increment exposes only the configure and configure-local interviews, not init,
`configure --deliver`, `configure-local --apply` or any application endpoint.

The two interviews have `readOnlyHint: true` and `openWorldHint: true`. Their
external probes are the existing detection allowlist, in the bound repository:

- PATH lookups for Claude, Codex, OpenCode V2, Git, `gh` and memhub.
- If Git is available, `git rev-parse --show-toplevel`.
- If memhub is available, `memhub status` (even when configuration says off,
  matching setup detection); no recall, render or memory write.
- If OpenCode is present, `opencode2 --version` with the existing `opencode` V2
  fallback, then the resolved executable's `api get /api/model` with the existing
  encoded `location[directory]` query. Runtime warnings remain document data,
  never human text on protocol stdout.

`DetectBound` shares detection with ordinary `Detect`, but verifies any reported
Git root has the bound directory's filesystem identity **before** memhub,
catalog or instruction-file probes. Different, relative, empty, missing or
unverifiable reported roots fail explicitly; equivalent directory spellings
are accepted while subsequent probes still use the bound root. Missing Git or
a failed Git probe remains a false readiness fact, never permission to leave
the binding. Ordinary `Detect` still follows the detected root for existing
CLI/init workflows; the new fixed-root restriction belongs to **both MCP
interviews**, not every detection caller or every interview-engine function.

Other probes read the existing configuration, root/nested instruction files and
ignore rules needed for proposals. Observations are unlocked, not an atomic
snapshot or a held reservation against a new Delivery run. Active Delivery,
held locks and unreadable state/locks retain their fail-closed refusal. That
shared CLI refusal is reused by **both tools**; pure `NextConfigure` and
`NextConfigureLocalWithFacts` do not independently enforce lifecycle preconditions.

Answering the summary's existing `approval` question is **interview data only**.
It can yield a complete document, but neither that answer, `bootstrap_ready`,
nor a successful tool call applies/removes local configuration, delivers
committed changes, initializes a repository, records a run/merge approval,
registers a session, consumes a grant or advances lifecycle state. This applies
to both MCP interviews and both CLI `--step` forms, not to separately invoked
application workflows, which retain their own authorization and re-derivation
checks. The earlier local-only "configuration" tool statements refer to
`orch_config_effective`; these two interviews add only the read probes above.
All pre-existing no-write/no-approval guarantees still hold for the nine prior
inspections and also hold for these two tools. Bounded preview retains its
separately documented preparation writes, never configuration application.
The integrated plan gate retains its separately documented external memhub
side-effect boundary and non-activating contract; neither interview invokes it.

### Interview verification and blast radius

`TestMCPStdio/configuration_interviews` walks both CLI/MCP interviews from initial
questions through summary, completion, retry and abort with native and legacy
OpenCode profiles. Scripted real executables accept only the detection allowlist;
catalog/configuration changes on the same connection, bare-model/stale-variant
answers, malformed requests, invalid/absent catalogs and configuration, local
repair defaults, Delivery/lock/state refusals and Git-root mismatch are covered.
CLI JSON, MCP structured content and JSON text agree. Bound and launching
repository names, kinds, modes, regular-file modification times and bytes stay
unchanged on success/failure, including approved completion. Discovery and
shutdown run no probes; warnings cannot pollute protocol stdout. Detection tests
also prove bound-root equivalence/refusal and retained ordinary root discovery.
Run this focused real-stdio check without installation:

```sh
go test ./internal/cli -run '^TestMCPStdio$/configuration_interviews' -v -count=1
```

| Touched element | Does its previous behavior still hold? |
| --- | --- |
| MCP registration (`mcp.go` hook; new `mcp_interview.go`: `addMCPInterviewTools`, handlers, schemas/annotations) | Eleven-tool-only discovery becomes thirteen as recorded above; the earlier ten-to-twelve increment remains historical. Existing launch checks, bindings, eleven schemas/results/annotations (including plan gate, context and bounded preview), SDK structured/text transport, protocol-only stdout and clean disconnect hold. New handlers only decode/probe/derive; no application path is exposed. |
| Integrated gate/context/preview (`addPlanGateTool`, `addEvalPreviewTool`, issue-context registration; `mcpPlanGateExercise`, `mcpPlanGateSchema` and existing context/preview fixtures) | Yes. The gate's strict plan/typed output and conservative annotations, existing engine/memhub probes and separate approval handoff remain unchanged. Context remains command-free; preview alone retains bounded preparation persistence. Discovery and stdio coverage compose all thirteen tools without duplicating policy or changing these handlers. |
| Detection (`detect.go`: `Detect`, extracted `detect`, new `DetectBound`; reused `Deps`, `Facts`, Git/host/memhub/catalog/instruction probes) | Ordinary `Detect` still never returns an error and retains discovery/failure-as-facts behavior for every existing caller. Only `DetectBound` adds the pre-read identity refusal; both paths share the same probes. Facts and detection audit fields are unchanged. |
| Reused refusal (`requireNoActiveDelivery`, `state.Load`, `lockfile.Inspect`) | Implementations and errors unchanged; both MCP tools call the same read-only refusal as CLI steps, without creating locks or acquiring a mutation serializer. This is not a new precondition inside all pure interview functions. |
| Reused question/interview/configuration surfaces (`AnswerSet`, `DecodeAnswers`, `Document`, schema 2, `NextConfigure`, `NextConfigureLocalWithFacts`, catalog/default/validation/materialization/summary helpers, configuration readers and instruction/ignore planners) | Unchanged. Fresh reads, committed-versus-local defaults, local repair, native/legacy/bare profiles, pagination, blockers, completion/abort and explicit failures hold. Proposed bytes remain data for every step caller; completion does not write for either MCP or CLI steps. |
| Tests (`mcp_test.go`: discovery and stdio exercise; new `mcp_interview_test.go`, `detect_bound_test.go`; reused scripted doctor probe) | Previous eleven-tool schema/parity/shutdown coverage, inspection no-write/no-command checks, gate schema/annotation checks and bounded-preview publication checks remain. Discovery now includes the two read-only/open-world AnswerSet tools. New deterministic transcript, freshness, failure, binding and no-write checks exercise both tools; no live host, credentials or memhub database is used. |
| Guide (`docs/mcp-status.md`) | Source-only setup, host enforcement and separate installation/checkpoint boundaries hold. Historical eight/ten/eleven/twelve-tool, no-interview and local-only configuration statements remain with explicit before/after and symbol scope. Gate side-effect limits remain distinct. This increment adds no claim of installed capability. |
| CLI steps/reports/application, init/bootstrap, lifecycle/approval/grant/state/lock algorithms, schemas, dependencies, adapters/guards, installed binaries, release and CI | Untouched. CLI detection and application behavior is not replaced, no first-time init or new authorization is exposed, and all existing required checks still apply. |

## Reproducible smoke test and trial checkpoint

From the source checkout, run:

```sh
go test ./internal/cli -run '^TestMCPStdio$' -v -count=1
```

Before item 13, this built a temporary real `orch` binary and used an SDK client
over its stdio pipes to initialize, discover and call all eight tools, then
disconnect and verify a successful process exit. After item 13 and issue #468,
the same test also exercises bounded preparation and issue context. After item
10, it covered all eleven tools, including the full plan gate. With item 12 also
integrated, it covers all thirteen tools and both configuration interviews.
After item 8, it covers all fourteen tools, including approval/grant evidence.
The earlier interview-only integration covered twelve tools. No global install
or client configuration is needed. The client stays connected while test fixtures
change from Assist to blocked Delivery and inconsistent state/lock pairs. Output reports
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
Before item 13 that smoke test discovered only the eight inspection tools.
After item 13 it also discovers and exercises bounded preview preparation.
Preview fixtures compare versions 1/2/3 across shared preparation, real CLI,
MCP structured content and JSON text, including CLI external-plan compatibility.
They verify immutable idempotence, preserved interrupted pending files,
conflicting/partial records and persistent post-link aliases, strict inputs,
unsafe/missing/linked/reparse plans, runtime binding aliases, destination overrides,
artifact/limit/pin/exclusion failures and recovery on the same connection.
Only the exact scripted Git read allowlist is on PATH; no live host runs.
Repository/Git/artifact/worker/scratch/launching files remain unchanged; successful
storage changes are exactly the preparation's digest record. Direct deterministic
tests also replace opened repository/storage identities during normalization and
verify refusal before any publication into either original or replacement roots.

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
After separately approved item 13, source additionally exposes bounded preview
persistence without execution approval. The CLI-only preparation limit is removed;
the source-only installation boundary and separate live-trial approval remain.

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

The tables below record the earlier increments; the eight-tool contract at item 7,
doctor checks, effective configuration, metrics and later preview are documented above.

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

## Item 13 blast radius

Historical read-only-only discovery, no-command/no-write and CLI-only preparation
limits are retained as before/after in the sections that stated them above. The
item-2/3/7 tables describe their increments, not today's entire catalog.
Integrating issue #468 preserves its report and shared status/next snapshot
behavior unchanged. At item 13, discovery and real-stdio coverage composed both
features as ten tools; item 12 adds two interviews without changing either
feature. Context remains a read-only, command-free inspection.

| Touched element | Does its previous behavior still hold? |
| --- | --- |
| CLI help (`cli.go`: `commands`; README command row) | Dispatch, argv and exits are unchanged. Before: MCP help advertised read-only inspections only. After: help also advertises bounded preparation; it does not advertise execution. |
| MCP registration (`mcp.go`: `runMCP`; `mcp_eval_preview.go`: `addEvalPreviewTool`, SDK schema/annotations/handler) | Existing launch/storage parsing, fixed roots, nine inspection tools including integrated issue context, schemas/results/probes, diagnostics and clean transport shutdown hold. Before: no preparation tool and every exposed tool was read-only. After: one strict `plan_file` tool may persist preparation only; structured/text parity still holds. Discovery itself performs no probes/writes. |
| Preview core (`plan.go`: `Preview`, extracted `preparePreview`; new `preview.go`: `PreviewRepository`) | CLI `Preview` keeps its absolute/external-plan and proposal-storage behavior, decode/error ordering and save semantics. Both entry points share strict decoding, supported versions and `normalize`. Only `PreviewRepository` adds portable repository-relative proposal confinement, opened repository/storage identities and canonical-path plus opened-identity destination matching before publication; this restriction is not attributed to every evaluation function. |
| Shared preparation (`strictJSON`, `normalize`, `readArtifact`, `pinSelection`, `plannedCounts`, `selectCases`, `effectiveConfiguration`, `storagePaths`, `evidence`; `Record`, `Plan`, `Evidence`, `Unit`) | Yes; implementations/wire shapes, versions, configuration/source pins, bounds, exclusions, schedule/digest, privacy projection, false execution/approval and unknown/blocker semantics hold for both previews and existing saved-record revalidation. No second policy pipeline or planner is retained. |
| Guarded filesystem (`relativeName`, `openGuarded`, `guardedDir.read/parent/child/check/publish/publishBytes`, `localPath`, `directory`, `openDirectory`, `readRoot`, `samePath`, platform drive/reparse/link checks) | Yes; implementations unchanged. The new entry point reuses portable names and anchored reads, checks both opened bindings before publication and publishes through the bound handle. Existing validation applies to every guarded evaluation operation that uses these helpers, not only the new wrapper; repository-relative proposal/storage-match restrictions are wrapper-only. |
| Immutable publication (`save`, `publishRoot`, `existingRecord`) | Yes; CLI still uses `save`; bounded MCP uses guarded publication with the same serialization and `publishRoot`/`existingRecord`. Size ceiling, exclusive pending files, atomic no-replace link, idempotent bytes, conflict/alias refusal, interrupted-artifact preservation and bounded grace hold for all users of this publisher, including controller records. No sweep/repair/overwrite behavior is added. |
| Git boundary (`gitRead`, existing runner and fixed query vectors) | Yes; read-only allowlist, fixed repository, no lazy fetch/replacement, timeout/output bounds and errors hold. MCP preview now reaches these existing probes; retained evaluation status remains command-free. Neither statement is generalized to every evaluation entry point. |
| Tests (`mcp_test.go`: `mcpDiscover`, `TestMCPStdio`, `TestMCPLocalToolsNeverRunExternalCommands`; new `mcp_eval_preview_test.go`: `mcpPreviewFixture`, `mcpPreviewInventory`, `mcpPreviewResult`, `mcpEvalPreviewExercise`, `previewIdentityRunner.Run`, `TestEvalPreviewRepositoryRechecksOpenedIdentities`; reused doctor-probe executable) | Prior tool coverage remains, including issue context's parity and no-command checks; discovery expects ten tools and adds the preview-only mutating/idempotent annotation and strict schema. New isolated tests exercise preparation persistence, bounded failures/replay/interruption, unchanged repositories, exact Git reads and opened-root replacement refusal. Existing synthetic controller/CLI/publication tests still run; no paid/native host or live evaluation is launched. The probe implementation is unchanged. |
| Guides (`docs/mcp-status.md`, `README.md`) | Source-only setup, prior checkpoints and separate installation/approval boundaries hold. Before/after limits remain in the same documents; persistence footprint, entry-point-specific restrictions, exact probes and preparation-versus-execution limits are explicit. |
| CLI evaluation parsing/run/status/stop/grade/report, evaluation controllers/workers/approval/grading/report algorithms, Delivery lifecycle/state/locks/grants/metrics, persistent schemas, configuration, dependencies, adapters/hooks, installations/releases and CI workflows | Unchanged. No useful command, authority boundary or existing check is removed or weakened; no new dependency, schema migration, host execution, configuration application, GitHub artifact or remote transport is added. |

## Issue context (issue #468)

**Before issue #468:** the eight-tool discovery/table and smoke-test count above
described the source surface, with no focused issue-context report. Identity and
dependency-reference sanity checks belonged to `Next` only, not `Status`.
**After issue #468:** source adds **`orch_issue_context`**, a ninth read-only tool
in this increment. Existing eight tools, launch flags and report schemas retain
their contracts. The same next-action classifier now also serves issue context;
its identity/reference restrictions apply to both guidance entry points, not
`Status` or every inspection function. Later separately approved increments may
add other tools; building source still does not update an installed server.
After integrating item 13, discovery returned ten tools: these nine read-only
inspections plus bounded preview. Context's schema and no-command/no-write
contract are unchanged; preview alone may persist a preparation artifact.
After item 12 on the earlier baseline, discovery returned twelve tools, also including the two read-only
interviews and their separate setup-probe allowlist. Context remains command-free.
With item 10 also integrated, discovery returned thirteen tools, including the plan
gate with its separate external memhub side-effect boundary. Context is unchanged.
After item 8, discovery returns fourteen tools, adding read-only approval/grant
evidence without changing context, preview, the plan gate or either interview.

Call with only a positive integer issue number, for example:

```json
{"issue_number":468}
```

Missing/zero/negative/wrong-type numbers, non-object input, unknown fields and
repository overrides are tool errors. The tool is local-only
(`readOnlyHint: true`, `openWorldHint: false`). There is no new CLI command;
`run.IssueContext` is the shared engine report path for this observation.
Success returns `schema_version: 1` as equivalent structured content and JSON text:

| Member | Source and meaning |
| --- | --- |
| `mode`, `consistent`, optional `warning` and `lock` | Existing status representation, preserving inconsistent state/lock warnings. |
| `run` | Status run identity, host, start time, approved plan metadata and any whole-run stopped reason; no unrelated issues. |
| `issue` | The exact recorded `state.Issue` from status: current phase, objective, acceptance criteria, required tests, optional `tests_ci_does_not_run`, branch/worktree references, current decision/executor/reviewer, escalation attempts, PR metadata and block/resolution histories. Missing work or routing stays missing. |
| `dependencies` | Matching direct dependency issue records from status in recorded issue order, with the same case-insensitive plan-ID matching as the engine. Declared IDs remain in `issue.depends_on`; missing records are never invented. |
| `guidance` | Existing schema-1 next-action report, narrowed to this issue **plus every run-level action**, with unchanged reasons, prerequisites, blockers, approval requirements and limitations. Run-level recovery, configuration/contract drift, stopped-run and all-cleaned guidance has neither an issue number nor a plan ID and is never filtered away; unrelated unnumbered planned issues are excluded. |
| `limitations` | Explicit observation/dispatch/readiness boundaries. |

Local-only test declarations are transcribed, not inferred. An absent or empty
`tests_ci_does_not_run` does **not** establish that CI runs every required test.
Current selections include escalations; unresolved decisions and their original
causes remain visible. Recorded PR fields do not establish live PR readiness.

No active run (including ordinary Assist), unknown/ambiguous requested issue
numbers and unreadable/corrupt/unsupported state or locks are explicit errors,
with no partial report. Next-action identity/reference errors propagate.
Inconsistency and configuration/contract drift preserve the existing engine's
recovery/blocker precedence, never proceed advice. Missing approved work/routing
uses the existing re-plan guidance, not an issue-body reconstruction.

Status projection and next-action classification share **one loaded state/lock
pair**, rather than independently loading and combining different runs, phases
or routing selections. Reads of that pair and effective configuration remain
unlocked and non-atomic; re-inspect after changes. A mismatch observed between
state and lock remains a consistency warning plus recovery guidance.

**Context is an observation, not dispatch authorization or a dispatch receipt.**
It does not dispatch, fetch, run external commands, probe GitHub/CI/hosts or
worktree existence, save lifecycle state, attribute a session, write metrics,
spend a grant or record approval. These no-command limits apply to every
local-only inspection tool, not just `orch_issue_context`; readiness and doctor
retain their separately documented external read probes. Every lifecycle verb
keeps its normal live preflights, dependency checks and separate approval gates.
Preview's separately documented Git reads and bounded preparation writes do not
change these inspection limits.

Deterministic engine tests cover phases, dependency waits/abandonment, native and
escalated selections, pending/resolved blocks, missing work/routing, legacy and
drifted contracts, inconsistency, identity/reference failures and corrupt or
unreadable state. They compare recorded work and guidance with status/next,
forbid commands and verify unchanged file names, kinds, modes, modification times
and bytes with metrics and session context enabled. Real stdio on one connection
adds fresh routing/phase changes, strict inputs, fixed binding, structured/text
parity, explicit errors/recovery, protocol-only stdout and clean shutdown with
no external executables on PATH. These checks run in `go test ./...`; a focused
smoke command is:

```sh
go test ./internal/cli -run '^TestMCPStdio$/^issue_context$' -v -count=1
```

### Issue #468 blast radius

| Touched element | Does its previous behavior still hold? |
| --- | --- |
| Status (`status.go`: `Status`, new `inspectStatus`, reused `StatusDoc`/`RunView`) | Yes. One read-only loader now supplies the existing schema-4 status projection plus its loaded state. Config-free inspection, load errors, consistency warnings and full recorded issues remain unchanged; status gains no identity/routing policy. |
| Next-action engine (`next.go`: `Next`, extracted `next`, reused `NextDoc`/`NextAction`/`NextSchemaVersion`) | Yes. Snapshot loading delegates to `inspectStatus`; the same classifier, ordering, error/blocker precedence, schema 1, limitations and prerequisites serve `Next` and context. No new scheduler or lifecycle rule is introduced. The before/after scope of identity checks is recorded above. |
| New context engine (`issue_context.go`: `IssueContext`, `IssueContextDoc`, `IssueContextRun`, `IssueContextSchemaVersion`) | Additive schema-1 projection only; exact status issue/dependency records and filtered existing guidance replace no prior API. Requested-number ambiguity is an explicit error even when the run is stopped or drifted. |
| Reused state/configuration/prerequisite structure (`state.Issue`, `Decision`, `Attempt`, `Block`/`BlockResolution`, `PlanRef`, `state.Load`, `lockfile.Inspect`, `state.CheckConsistent`, `loadExecutionConfig`, `checkApprovedWork`, `checkDependencies`, `Issue.DecisionPending`, `allCleaned`) | Yes; implementations and persistent schemas are untouched. Missing fields, current selections and unresolved history are observed, not repaired, re-routed or authorized. |
| MCP (`mcp.go`: `runMCP`, new registration/handler, existing SDK transport/output and binding) | Existing eight input/output contracts, fixed launch binding, diagnostics and shutdown hold. Eight-only discovery becomes nine in this increment, as recorded above. The new strict issue-number tool is read-only/local-only and returns no dispatch result. |
| Tests (`issue_context_test.go`, `mcp_issue_context_test.go`; `mcp_test.go`: `TestMCPStdio`, `mcpDiscover`, `TestMCPLocalToolsNeverRunExternalCommands`) | Existing parity, schemas, annotations, no-write/no-command, binding and disconnect coverage remains; discovery adds context and feature-specific fixtures exercise both shared sources and real stdio. The shared-snapshot regression verifies advice does not reload replacement run/phase/routing state. |
| Guide (`docs/mcp-status.md`) | Existing launch, optional source-only use, installation boundary and approval checkpoint hold. Earlier eight-tool-only and `Next`-only statements remain as explicit before/after in this same document; the new report and restriction scopes are documented here. |
| Lifecycle/dispatch/readiness/review/merge/grant behavior, CLI commands, dependencies, adapters/guards, CI workflows and installed binaries | Untouched. No dispatch receipt, new readiness claim, approval, mutation capability, migration, install or release is added. All existing checks and gates remain required. |

## Effective-contract plan decision gate (item 10)

Call `orch_plan_gate` with exactly one outer member, `plan`, containing the
submitted schema-1 `PlanDoc` object. For example, a complete minimal request is:

```json
{
  "plan": {
    "schema_version": 1,
    "host": "opencode",
    "title": "Fix a status defect",
    "issues": [{
      "id": "fix-status",
      "title": "Fix status reporting",
      "objective": "Report the recorded mode accurately",
      "acceptance_criteria": ["Status matches the recorded mode"],
      "type": "bug",
      "facts": {"read_only": false},
      "wave": 1,
      "required_tests": ["go test ./..."],
      "usage_class": "light"
    }]
  }
}
```

The host must be enabled in the bound repository. The equivalent CLI invocation
is `orch run plan`, with **the inner plan only** on stdin. Missing/null/non-object
arguments or plans, extra outer members (including roots or approval assertions),
unknown nested plan/issue/facts/downgrade fields, invalid types and unsupported
schema/host values fail explicitly. The MCP schema checks the wrapper and plan
object shape; raw nested JSON goes straight to `run.Plan` and its existing
`DecodePlan`/`PlanDoc.Validate`, not a second plan decoder or validator.

Success returns the **entire existing `GateDoc`, schema 3**, as equivalent
`structuredContent` and JSON text. Its discovered output schema is inferred from
that same Go type, including execution profiles, issues, memhub and CI reports.
No adapter projection, legacy `PlanDoc.Digest` substitution or MCP approval hash
is involved. The document preserves:

- `plan_digest`: the engine's effective-contract digest, binding the submitted
  plan, resolved execution configuration and effective issues, including
  engine-contributed acceptance criteria;
- title, host, configuration revision, sorted applied override keys and merge
  strategy; `execution` includes all six selected-host profiles (effort, variant
  or explicit `no_variant`), concurrency, memhub/metrics policy and blast-radius
  criterion;
- issue objective/criteria, role, executor/reviewer, downgrade/rationale, dependency
  waves, required tests, declared local-only tests, fixes, risk, usage class and
  labels, with the engine's original ordering and omission rules;
- memhub probe/recall outcomes and failure detail, plus the CI workflow-presence
  statement. CI is still a **pre-PR heuristic**, not required-check evidence.
  Omitted `tests_ci_does_not_run` is no declaration, not proof CI runs every test.

### Preconditions, external probes and side effects

Every call freshly invokes `run.Plan` for the fixed launch-bound root. The engine
loads and validates committed/local configuration, requires consistent Assist
state with no Delivery lock, decodes/validates the plan, builds the effective
contract/digest, observes workflow presence and applies memhub policy, in the
same order as CLI. Invalid configuration, active Delivery, orphan/missing/corrupt
locks or state, invalid plans and required-memhub failures return explicit tool
errors with no partial gate. The MCP layer neither repairs nor changes that
policy, and recovery is possible on the same connection after remediation.

The **only subprocesses** are the existing gate's memhub commands in the bound
root (bind the primary checkout, never a worktree with no memhub DB):

1. `memhub status` when mode is not `off`.
2. Only after status succeeds,
   `memhub recall "orch memhub recall probe" --json --max-results 1`.

`off` skips both. `best-effort` records health or recall failures and continues;
`required` fails closed on either, including spawn errors, nonzero exits and
invalid recall JSON. There are no Git, GitHub, host/model-catalog or test probes.
Returned probe health is not approval or a promise activation preflights pass.

**Side-effect boundary:** Orch invokes only status and recall, not a memhub write,
render, reindex or sync command. That does **not** guarantee the external memhub
process leaves its own environment untouched: depending on its implementation
and configuration, opening its database or serving recall may update memhub-owned
storage, caches or retrieval bookkeeping and contact configured providers. Those
effects are not suppressed, sandboxed or rolled back by MCP, just as in CLI.
The tool therefore advertises `readOnlyHint: false`, `idempotentHint: false` and
`openWorldHint: true`, retaining the conservative default destructive hint rather
than promising external side-effect safety. No destructive Orch operation is
invoked. These annotations are not permission or an approval record.

The gate itself takes no mutation serializer and creates no state, Delivery or
mutation lock, run/history record, session, metrics event, approval, grant use,
issue, PR, branch or worktree. Tests isolate injected probe fixtures and empty
PATH processes; they never open the user's actual memhub store. Unchanged
Orch/Git artifacts in these tests are not proof that arbitrary live memhub
implementations are side-effect-free.

### Separate human approval handoff

Present the **full returned gate** to the human, including effective scope,
contributed criteria, routing profiles/rationale, waves, local-only test
declarations, overrides and probe/CI limitations. The choices remain approve and
enter Delivery, adjust routing facts, revise scope, or cancel and stay in Assist.
Validation alone, a digest, successful MCP transport and a successful gate call
are **never approval**. MCP adds no approval or activation tool here.

Only after explicit approval of this exact effective contract may the normal
separately invoked activation path submit the unchanged plan and its digest-bound
human assertion (`approve-and-enter-delivery`, approver and timestamp) through
`orch run activate`. Activation still recomputes and checks the effective digest
and enforces its own preconditions/preflights; changed scope or effective policy
requires a fresh gate and human approval. Existing autonomy-grant authorization
contracts remain separate and unchanged; this call inspects or spends no grant.
The gate is an unlocked observation, not a reservation or atomic future promise.

### Verification and blast radius

`go test ./internal/cli -run '^(TestMCPPlanGateMemhubParity|TestMCPStdio)$' -v -count=1`
compares complete CLI/MCP documents and effective digests. Real stdio covers all
three host profiles, OpenCode variants/bare models, reviewer downgrade, contributed
criteria, local preferences/override keys, waves/tests/labels/fixes, workflow
presence, strict wrapper/nested errors, configuration errors, active runs,
inconsistent/corrupt locks/state, required missing memhub, best-effort failure and
same-connection recovery. Injected ordered runners additionally exercise healthy,
failed and malformed recall, required failures and off-mode zero probes. Snapshots
compare names, kinds, modes, regular-file modification times and bytes, including
real `.git` metadata, with metrics/session context enabled and clean shutdown.
All these checks run under `go test ./...`; no CI-coverage inference is made from
an omitted plan declaration.

| Touched element | Does its previous behavior still hold? |
| --- | --- |
| MCP registration (`mcp.go`: `runMCP`; new `mcp_plan_gate.go`: `addPlanGateTool`) | The ten-tool-only surface changes to eleven as recorded above. Existing tools, including issue context and bounded preview, canonical fixed-root binding, launch argv, protocol-only stdout, stderr diagnostics and clean disconnect hold. The new wrapper delegates directly to the engine without staging or activation. |
| Input/output boundary (SDK `AddTool`, raw `plan`, typed `GateDoc`) | Existing ten schemas remain. The gate adds a strict outer object and preserves the engine's nested decoder/validator; typed output exposes the complete engine schema without translating or hashing it. Root overrides are rejected for **every tool**, not only `orch_plan_gate`; only this tool accepts `plan` in this increment. |
| Reused gate (`run.Plan`, `GateDoc`, `GateIssue`, `GateSchemaVersion`, `DecodePlan`, `PlanDoc.Validate`, `requireAssistNoLock`, `effectiveContract`, `gateIssues`, `contentDigest`, `executionConfig`, `detectCI`, `memhubGate` and memhub client) | Yes; implementations, schemas, ordering, digest/routing/contribution rules, errors and probes are untouched. Assist/no-lock is the existing shared **Plan/Activate** precondition, not a blanket rule for every inspection or lifecycle verb. The legacy submitted-plan digest remains available but is not used as approval identity. |
| Approval/activation/lifecycle/grant boundaries | Yes; no implementation is changed or invoked. No gate caller, CLI or MCP, acquires human approval merely by successfully receiving a document. Existing activation, grant and merge checks remain separate. |
| CLI help (`cli.go`: `commands`) and README help/guide | Command dispatch/argv and existing CLI gate behavior hold. Before: help described inspections and bounded evaluation preparation. After: it also advertises plan decision gates, with the external memhub exception stated here and in README. No useful surface is removed. |
| Tests (`mcp_test.go`: `TestMCPStdio`, `mcpDiscover`; new `mcp_plan_gate_test.go`: `mcpGatePlan`, `mcpGateJSON`, `mcpPlanGateSchema`, `mcpPlanGateExercise`, `mcpPlanGateParity`, `mcpGateResultParity`, `TestMCPPlanGateMemhubParity`) | Existing ten-tool tests and annotations remain asserted, including context and preview; discovery expects eleven tools and adds the gate's conservative annotations/output schema. New isolated tests exercise full-document parity, strict errors, freshness and unchanged Orch/Git artifacts, without live memhub writes. |
| Source guide (`docs/mcp-status.md`) | Optional/source-only setup and prior checkpoint hold. Before: the title and discovery/smoke/no-write descriptions covered inspections and bounded preparation only. After: the same document preserves those historical statements and explicitly adds the non-activating gate, memhub-owned side-effect exception and separate human approval handoff. Earlier item-7/13/context sections remain historical records, not current catalog totals. |
| Dependencies, persistent schemas, engine algorithms, adapters/guards, configuration, CI workflows, installed binaries and releases | Untouched. No migration, new dependency, broader authorization, installation, release or remote transport is added; existing required checks still apply. |

## Item 8: recorded approval evidence and active-grant observation

Call `orch_approval_status` with an explicit `{}`. Omitted arguments, `null`,
non-objects, unknown fields, grant/issue selectors and repository overrides are
errors, before any probes. The server remains bound to its launch-time checkout.
There is no new CLI command: the run portion is exactly the existing
`orch run status --json` document; grant observation reuses the grant store.

The snapshot has `schema_version: 1` and these members, returned identically as
MCP structured content and JSON text:

| Member | Meaning |
| --- | --- |
| `status` | The full existing schema-4 `StatusDoc`. `run.plan`, when present, retains recorded title, digest, approver/time, configuration revision, contract version and execution digest. `run.issues[].approved_head_oid`, when present, is the recorded merge-report pin, alongside its issue/PR identity and phase. Mode, lock, consistency warning, stopped reason and all other status fields remain intact. |
| `active_grant` | The full existing schema-1 `Grant` record, or explicit `null` when no record is active (including absent storage, empty storage, expired or revoked grants). Terms retain scope, covered gates, expiry, all limits, original session and relay permission mode. The record retains its current holder, approval identity/time/digest, consumed `runs`/`merges`, recorded gate `approvals` and relay history. No remaining-budget or authorization calculation is synthesized. |
| `limitations` | Evidence-only meaning, unlocked/non-atomic concurrency limits and the checks deliberately not performed. |

A merge-report pin is **not a recorded human merge approval**. An active grant
means only not revoked and not yet expired at observation time; even exhausted
limits or a different current holder remain visible. Neither a pin nor an active
record authorizes a future action. The call does not validate scope eligibility,
session ownership, remaining budget, protected paths, live PR heads, reviews or
CI. Normal plan/merge gates and separate human approvals remain required outside
the existing separately authorized grant contract. A tool call is never approval
and never creates, renews, relays, revokes, validates authority under or spends a
grant. These no-approval/no-write restrictions apply to **all twelve read-only
inspection/interview tools**, not only this symbol. The no-approval restriction
also applies to bounded preview and the plan gate, whose preparation writes and
external memhub side effects remain separately documented; lifecycle and
grant mutation APIs retain their own distinct enforcement contracts.

### Read probes, freshness and concurrency

Each call reloads status through `run.Status`, then opens the clone's shared grant
store through `grant.Open`. It uses exactly these read-only Git probes, with the
fixed bound checkout as cwd and the existing noninteractive Git environment:

- `git rev-parse --show-toplevel` (existing top-level binding check)
- `git rev-parse --path-format=absolute --git-common-dir` (clone-shared store)

No fetch, GitHub, host, memhub or other subprocess is invoked. Initialization,
discovery, invalid arguments and disconnect launch no probe. The tool is annotated
read-only with `openWorldHint: true`, because it invokes Git. The other tools keep
their previous probe/local-only contracts.

`Store.ObserveActive` shares `all`/`readRecord` and `active`/`Grant.Active` with the
existing locked `Store.Active` path. Every JSON record is read strictly, including
ended records; malformed, unreadable, unsupported or filename-mismatched records
and duplicate-active grants are explicit errors, not absence. Failed Git probes,
unverifiable common directories and corrupt state/locks also return tool errors
without a partial snapshot. Existing inconsistent state/lock pairs remain
inspectable with their original `Status` warning. Configuration is not loaded.
Successive calls on the same connection observe changes and can recover after
errors; no cached approval or grant is reused.

This is an **unlocked, non-atomic observation**, not a cross-file transaction:
state, Delivery lock and individual grant records can change between reads or
immediately afterward. Concurrent replacement can yield a mixed observation;
concurrent removal can yield an explicit read error. No retry can turn it into an
authorization. Re-inspect after changes and let normal lifecycle verbs recheck
under their existing locks. Observation creates no grant directory or lock file,
rewrites no record, consumes no limit, registers no session and records no approval,
metrics or lifecycle advancement, including on failure.

### Item 8 verification and blast radius

`TestObserveActive*` pins absence, empty/expired/revoked/active/exhausted and
duplicate-active selection, strict malformed/unsupported records, freshness and
lock-free/no-lock-file behavior. `TestOpenGrantStoreGitFailures` pins the two-probe
allowlist, binding and failed/invalid common-directory results. `TestApprovalStatus*`
compares the full status view and recorded approval/pin evidence, legacy inspection,
warnings, failures and unchanged files with metrics/session context present.

`go test ./internal/cli -run '^TestMCPStdio$/approval_evidence' -v -count=1`
builds a real stdio server and reuses the scripted probe executable with only the
two Git reads allowed. It compares shared/structured/text snapshots and nested
status against CLI JSON, checks fresh grants and errors on one connection, fixed
cwd binding, strict input, missing Git, clean shutdown and unchanged bound/launching
repositories **and the separate clone-common store**, including lock-file absence.
The existing raw-stdio omitted-arguments regression now also covers this tool.
These tests are included in `go test ./...`; they are not an installation, release
or claim that every hosted CI check has run.

| Touched element | Does its previous behavior still hold? |
| --- | --- |
| Grant store opening (`store.go`: `Open`, `DirName` documentation) | Valid clone/worktree resolution and the same two Git probes hold. Before: a successful but blank/relative/missing/non-directory common-dir result could become an unchecked store path. After: `Open` explicitly rejects those results. This fail-closed check applies to **every `grant.Open` caller**, not just MCP; no valid grant's locking or authorization changes. The old comment's every-access lock claim is retained as before/after beside `DirName`. |
| New observation (`observe.go`: `Store.ObserveActive`) | Adds unlocked read-only observation only. Before: every existing store read used the lifecycle lock except the missing-store fast path. After: those methods still do; only `ObserveActive` bypasses locking and never creates the directory/lock. This no-lock guarantee belongs to **that method**, not every grant API. |
| Reused grant representation/parser/selection (`Grant`, `Terms`, `Use`, `RecordedApproval`, `Relay`, `empty`, `all`, `readRecord`, `decodeStrict`, `active`, `Grant.Active`) | Yes; implementations, strict schema/unknown-field/filename failures, expiry/revocation and duplicate-active rules are unchanged. Full records are returned without a second parser, semantic validator or eligibility calculation. |
| Existing grant lifecycle (`withLock`, `Active`, `Get`, `Create`, `Revoke`, `RecordRun`, `RecordMerge`, `RecordApproval`, `record`, `update`, `Store.Relay`) and run grant gates | Yes, for valid Git results; code and cross-process locking, holder/limit/scope checks, spend/approval recording and relay behavior are unchanged. No lifecycle caller switches to unlocked observation. |
| Existing `grant.Open` consumers (`runGrant` create/relay/status/revoke; `activeGrantOrNil` through `grantSessionContext`, `hookContextCheck`, `hookPreCompact`; `planGrant`, `mergeGrant`, `fixCycleCheck`) | Valid clone/worktree behavior holds. Invalid common-directory results now fail before store access for every caller; CLI and run-gate errors propagate, while existing notification hooks deliberately keep their fail-open behavior. Preview never opens a store. No caller's authorization policy or locked read is replaced. |
| New run snapshot (`approval_status.go`: `ApprovalStatus`, `ApprovalStatusDoc`, `ApprovalStatusSchemaVersion`) | Adds a schema-1 evidence wrapper with explicit grant absence and limitations; no authorization boolean, new status parser or CLI verb. |
| Reused status/time/path/Git (`Status`, `StatusDoc`, `RunView`, `PlanRef`, `Issue`, `state.Load`, `lockfile.Inspect`, `CheckConsistent`, `Env.now`, `gitops.Open`/`CommonDir`, `paths.Canonical`/containment) | Yes; implementations and nested schemas remain. The snapshot preserves full approval/pin data and existing inconsistent-state warnings; it never calls `loadVerb`, readiness or grant authorization methods. |
| MCP (`mcp_approval.go`: `addApprovalStatusTool`; `mcp.go`: registration) | Existing launch binding, transport, thirteen tool contracts (including issue context, bounded preview, `addPlanGateTool` and both `addMCPInterviewTools` entries), structured/text output, protocol-only stdout, stderr diagnostics and clean disconnect hold. Thirteen-only discovery becomes fourteen as recorded in the original discovery section above; the added strict-empty-input tool is read-only with Git probes. Preview alone retains preparation writes; the gate's conservative annotations/external memhub boundary and interviews' read-only setup probes are not weakened. |
| Tests (`grant/observe_test.go`, `run/approval_status_test.go`, `cli/mcp_approval_test.go`, `mcp_test.go`: `TestMCPStdio`, `mcpDiscover`, no-command contract comment; `mcp_metrics_test.go`: `mcpMetricsOmittedArguments`) | Existing coverage remains, including issue context's local-only checks, bounded preview's preparation/schema/annotations, the plan gate's typed schema/conservative annotations and both interviews' schemas/probe contracts. Discovery now expects fourteen tools and includes the new schema/annotation; real stdio adds approval/grant/binding/error/no-write parity, while the shared raw-input loop adds this tool's omitted-argument refusal. The existing `testdata/doctor-probe` is reused unchanged; its test-only transcript stays outside inspected repositories/stores. |
| Guide (`docs/mcp-status.md`) | Source-only launch/storage binding, prior trial checkpoint and installation boundaries hold. Previous discovery and store/probe limits are retained as explicit history; new snapshot/approval/concurrency limits are documented, not authority to perform later actions. |
| Persistent state/status/grant schemas, lifecycle/session/approval algorithms, dependencies, CLI commands/help, host adapters/guards, installed binaries and CI workflows | Unchanged. No migration, dependency, mutator, automatic approval, installation, release or remote access is added. Existing required checks remain required. |
