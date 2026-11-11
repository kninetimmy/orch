# Read-only status, next actions and review/merge readiness over MCP

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
**`orch_run_next`**. At that point the empty-object input restriction applied
to **every tool this server exposed**, not only `orch_run_status`.
**After trial item 3:** discovery returns exactly three read-only tools, adding
**`orch_run_readiness`**. Status and next still accept only `{}`; readiness
accepts only `{"issue_number":447}` with a positive integer. Unknown fields,
including repository overrides, are rejected for **all three tools**. The
positive-issue argument is readiness-only, not a change to status or next.

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
so. No inspection records a session, metrics, an approval, a decision
resolution or lifecycle advancement, nor mutates git/GitHub. **A tool call is
never human approval.** Existing separately authorized grant contracts are
unchanged, but guidance does not inspect, validate or spend a grant; it cannot
claim a grant authorizes any suggested action. These restrictions apply to
**all three inspection tools and all three CLI inspection commands**, not just
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

Only readiness, not status/next, opens the existing `ghops` handle in the bound
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
approval. The snapshot is unlocked and GitHub probes are not atomic. Manifest
parsing checks canonical JSON/markdown consistency, **not cryptographic
provenance**; consistently hand-edited records remain undetectable. No new
signing or trust mechanism is claimed.

Readiness never calls the session-writing lifecycle loader, audit-writing `ci`,
state/status/metrics-writing `merge-report`, `merge`, git mutations or a grant
store. It performs no repairs, tests, worktree or host preflights. Existing
review, merge-report, merge and grant enforcement is unchanged; the stricter
evidence inspection does not replace those verbs' own gates.

## Reproducible smoke test and trial checkpoint

From the source checkout, run:

```sh
go test ./internal/cli -run '^TestMCPStdio$' -v -count=1
```

This builds a temporary real `orch` binary and uses an SDK client over its
stdio pipes to initialize, discover and call all three tools, then disconnect and
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

**Before trial item 2:** This increment implements roadmap item 1 (live status)
only. **After trial item 2:** source implements live status and read-only
next-action guidance, not the subsequent readiness/doctor items. **After trial
item 3:** source also implements read-only readiness, but not item 4 doctor.
No source increment is claimed installed by this guide. The trial
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

## Trial item 3 blast radius

The item-2 table above records that increment, not a claim that item 3 is absent
now. The before/after changes to discovery, inputs and external-command limits
are recorded in their original sections above; status and next retain their
previous contracts. Restrictions on no writes/no approval and fixed repository
binding apply to **every inspection entry point**, not just `Readiness`.

| Touched element | Does its previous behavior still hold? |
| --- | --- |
| Run inspection (`readiness.go`: `Readiness`, `ReadinessDoc`, `ReadinessReview`, `ReadinessMergeReport`, `ReadinessMerge`, `ReadinessSchemaVersion`, `readinessReviewEvidence`, `sameReadinessAudit`, `finishReadiness`) | New schema-1 evidence inspection only. It does not replace next-action policy or any lifecycle verb; only this inspection adds GitHub reads. |
| Merge-report (`mergereport.go`: `MergeReport`, extracted `mergeReportResult`, existing `checkApprovingReview`/`requireMergeableCI`) | Yes. Pure result formatting is shared with the preview; existing ordering, PR/CI gates, head pinning, phase/status/metrics writes remain in `MergeReport`. The formatter never records a gate, for **any caller**, not just readiness. |
| Reused local prerequisites (`state.Load`, `lockfile.Inspect`, `state.CheckConsistent`, `loadExecutionConfig`, `checkApprovedWork`, `phaseAllowed`, `sameWork`, `headOIDPattern`) | Yes; implementations unchanged. Inspection uses them without `loadVerb` or session attribution. Full-OID validation is also used for readiness evidence; existing review-worktree validation is not weakened. |
| Audit path (`readIssueManifest`, `manifest.Parse`, `manifest.Verification`, engine-owned cycle/criterion name patterns) | Yes; implementations/schema unchanged. Readiness checks existing evidence and the open PR mirror, ignoring only independently rotated verification detail text; it never repairs or writes a record. Canonical consistency still does not prove provenance. |
| GitHub/CI (`openGitHub`, `ghops.Open`, `GH.PR`, `GH.RequiredCI`, `ciChecks`, `MergeReportCI`/`MergeReportResult`/`CICheck`) | Yes; unchanged commands, authentication and four-state CI semantics. Readiness adds consumers, preserves required-check evidence, and explicitly fails/unverifies failed reads. No mutation method is called. |
| CLI (`run.go`: `runUsage`, `runRunVerb`; `cli.go`: `commands`; run package command/concurrency comments) | Yes for existing commands. Before: only status/next bypassed stdin/serialization among inspections. After: readiness joins them with strict issue argv; all mutators retain serialization and JSON-input handling. Help exposes readiness and clarifies inspection versus mutating plumbing. |
| MCP (`mcp.go`: `runMCP`, SDK registration/handler) | Before: two empty-input local-only tools. After: three read-only tools; readiness alone takes a positive issue and makes external reads (`openWorldHint: true`). Canonical fixed launch binding, status schema 4, next schema 1, strict existing inputs, structured/text parity, diagnostics and clean disconnect remain. |
| Engine tests (`readiness_test.go`) | New scripted read-only/failure/evidence/CI coverage; existing lifecycle/review/merge/grant tests remain unchanged. Snapshots include repository names, kinds, modes, regular-file modification times and bytes, with metrics/session context enabled. |
| CLI/MCP tests (`run_test.go`, `mcp_test.go`, `mcp_readiness_test.go`, `testdata/readiness-gh/main.go`) | Existing status/next parity, binding, local-only calls, shutdown and serialization tests remain. Discovery now expects three tools; real readiness stdio comparisons add a command allowlist and unchanged bound/launching repositories, including failures. The fake executable writes only its external test transcript, never the repository. |
| Guides (`docs/mcp-status.md`, `README.md`) | Source-only setup, optional use, host enforcement and four-item checkpoint remain. Historical status-only/empty-input/no-exec/plumbing claims are retained as before/after where originally stated, rather than silently removed. |
| Lifecycle loading/review/merge/grants, next/status algorithms, persistent schemas, dependencies, adapters/guards, installed binaries and CI workflows | Untouched. No new authorization, automatic action, schema migration, dependency, host redesign, installation or item-4 doctor feature. All existing CI requirements remain. |
