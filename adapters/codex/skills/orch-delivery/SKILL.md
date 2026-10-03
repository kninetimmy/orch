---
name: orch-delivery
description: >-
  Drives an Orch Delivery run from Codex CLI: plan construction, the
  plan gate, activation, and the per-issue dispatch/review/merge loop.
  Load this after orch-architect, once a mutating request has been
  read-only investigated and a plan is ready to propose. References the
  `orch` binary's `orch run <verb>` engine for every decision; never
  reimplements its policy.
---

# Orch Delivery

This skill is the wire contract and presentation layer for a Delivery
run. Every decision (what routes where, what is allowed next, what a
gate result means) comes from `orch run <verb>`; your job is to
construct honest requests, run the verb, and present its result
faithfully.

## The JSON pattern every verb call follows

1. Write the request JSON to a scratch file in the OS temp directory,
   **outside the repository** (never inside the working tree or a
   worktree).
2. Run `orch run <verb> < <scratch-file>` and capture stdout.
   PowerShell (5.1 and pwsh 7) rejects `<` ("The '<' operator is
   reserved for future use."); use the pipe form instead:
   `Get-Content -Raw <scratch-file> | orch run <verb>`. On Windows
   PowerShell 5.1 specifically, that pipe alone silently corrupts
   non-ASCII (em dashes, `§`) into `?`; guard it with
   `$OutputEncoding = New-Object System.Text.UTF8Encoding $false;
   Get-Content -Raw -Encoding UTF8 <scratch-file> | orch run <verb>`.
3. Exit 0: parse stdout as the verb's JSON result.
4. Non-zero exit: the engine refused. Present the stderr message
   **verbatim** — never paraphrase, never retry blind, never work
   around it. Only a revised request (different facts, different
   approval, a resolved precondition) is a valid next step.

`orch run status --json` never reads stdin — call it bare.

Selection-bearing wire versions are closed: StatusDoc `4`, GateDoc `3`, Dispatch `4`, Escalate `2`, Review `3`.
Reject any other `schema_version` before reading or submitting a `Selection`.

## PlanDoc construction

Build a `PlanDoc` (`schema_version: 1`) honestly. Routing is derived
entirely from the facts you declare; there is no field to choose a
model or effort yourself. "Adjust agent routing" at the gate always
means: revise the facts that were wrong and resubmit — never hand-edit
a routed selection.

```json
{
  "schema_version": 1, "host": "codex", "title": "...", "summary": "...",
  "issues": [{
    "id": "issue-slug", "title": "...", "objective": "...",
    "acceptance_criteria": ["..."], "type": "feature",
    "area_labels": ["..."],
    "facts": {
      "read_only": false, "unusually_difficult": false,
      "risk_domains": [],
      "downgrade": {"mechanical": false, "low_risk": false,
                     "fully_specified": false, "unsurprising": false}
    },
    "depends_on": [], "wave": 1, "required_tests": ["..."],
    "tests_ci_does_not_run": ["..."],
    "usage_class": "medium"
  }]
}
```

`facts.read_only` must be `false` for every issue — read-only work
belongs in Assist. `depends_on` names issues by `id`; a dependency's
`wave` must be strictly less than the depending issue's. `usage_class`
is `light`, `medium`, or `heavy`. `area_labels` are repository-defined:
every one you declare must already exist in the repo — activation fails
closed at a read-only preflight if any is missing (create it with
`gh label create <name>` or drop it from the plan).

Write `acceptance_criteria` and `required_tests` as a floor, not a
survey. Each acceptance criterion names one behavior that must hold once
the issue is done — a specific, checkable outcome an executor can
satisfy and a reviewer can confirm without guessing — never an
open-ended instruction like "handle edge cases" or "add tests" that
leaves the bar for "done" to be invented later. Each required test names
the exact command that gates the change (`go test ./...`, `gofmt -l .`);
it is not an invitation for comprehensive coverage, just the check this
change must pass.

When the repository's CI does not run one of those required tests — it
sits behind a build tag no workflow enables, needs a tool or credential
CI has not got, or no workflow invokes it at all — name that exact
command in `tests_ci_does_not_run`. Every entry must match one of the
same issue's `required_tests` strings exactly, or the plan is rejected.
The engine renders the declaration beside the test it names at the plan
gate, in the created issue body, and in the dispatch result, so the human
approves knowing which of the issue's gates CI actually holds and the
executor knows which check nothing but a local run will ever execute.
Omit the field unless you checked the repository's workflows yourself: it
is a claim about that repository, and saying nothing is honest where a
guess is not. Omitting it never asserts that CI runs everything.

An acceptance criterion describes an observable outcome, and never
names a function, a control-flow step, or a validity notion the change
is expected to introduce. Naming one that does not exist yet hands the
executor a design to invent in order to satisfy the wording, and every
later review then grades how completely that invention was built rather
than whether it needed to exist at all — state what must be true once
the issue is done, and leave the mechanism to the executor.

A criterion requiring evidence the repository cannot produce is not a
valid criterion. When nothing in the repository can observe what the
criterion asks about — how an agent routes, what it decides, whether it
follows an instruction it was given — the only thing that could satisfy
it is a check on a proxy, such as a test asserting the instruction's
prose is still present, which pins the words and says nothing about the
behavior. Ask what evidence would settle the criterion before you write
it: if the honest answer is a proxy, write the criterion against
something the repository can actually produce evidence for, or leave it
out of the plan.

When an issue declares at least one entry in `facts.risk_domains`, the
engine contributes one further acceptance criterion of its own. The
`GateDoc`, the created issue body, the audit record, and
`DispatchResult.acceptance_criteria` all carry it, so a risk-domain
issue's criteria list is one entry longer than the list your `PlanDoc`
submitted and the contributed entry is always last. Do not write it into
the `PlanDoc` yourself — a plan that lists it too carries it twice and
costs a second judgment saying the same thing. It reads: Blast radius
(contributed by Orch because this issue declares a risk domain, not by
the plan document): name every element of the structure this change
touches and state, for each, whether the behavior it had before this
change still holds; record a behavior this change removes as a
before-and-after in the same document that stated the old behavior,
rather than deleting that statement; and where a restriction is
attributed to one named symbol, establish whether it holds for that
symbol alone or for every symbol of its kind, and say which. What
settles it is an enumeration in the pull request body naming each
element the change touched and its before-and-after; passing tests do
not settle it. Present it at the plan gate as part of the issue's
criteria, exactly like the ones you wrote — it is part of the standard
the human is approving.

Plan text must never reference machine-local or gitignored paths such
as `.memhub/`, `.orchestrator/state.json`, or
`.orchestrator/config.local.toml` — an executor sees only the committed
tree inside its worktree, and none of these exist there.

Before submitting a `PlanDoc`, verify every fact an acceptance
criterion asserts about the repository — a path, a file, a symbol,
or a count — by running the command that checks it: `git ls-files`,
`git check-ignore`, or a grep whose output you actually read. A
count you have not confirmed this way belongs in the criterion as
"every site" — a form an off-by-one cannot falsify — not a specific
number like "the five sites". Read one issue's acceptance criteria
together as a set, not one at a time, and confirm that a single
implementation can satisfy all of them simultaneously. Symbol
visibility across a package boundary is a concrete way a set
becomes contradictory: a pair of requirements — one criterion
needing a symbol unexported, another criterion needing a different
package to reach it — is unsatisfiable by construction, so prefer a
criterion stating the invariant actually wanted ("normalization
logic has exactly one source in the module") over one prescribing
the mechanism you imagine delivering it ("a single unexported
function"); the invariant stays satisfiable however the executor
structures the code. When a criterion is justified by a claim that
something currently fails silently or passes while broken, run
that failing case once yourself — in a scratch directory or
throwaway clone outside the repository, never in this checkout
where tracked-file changes are mechanically denied in Assist, the
same convention this file already states for verb request JSON —
and read its actual outcome before writing the criterion — a
mechanism that looks fragile can fail loudly on exactly the change
you feared it would miss — and treat a memhub note or a prior
finding you are relying on as a lead to verify, not evidence to
inherit.

## Plan gate

Call `orch run plan` with the `PlanDoc` on stdin. The result is a
`GateDoc` (`schema_version: 3`): `plan_digest`, `plan_title`, `host`,
`config_revision`, `config_overrides`, `merge_strategy`, `execution`, `memhub`
(`{mode, probe, recall, detail}`), `ci` (`{workflows_present, statement}`), and
`issues[]` — each with `id`, `title`, `objective`,
`acceptance_criteria`, `role`, `executor` (`{model, effort}`),
`reviewer` (`{model, effort}`), `reviewer_downgraded`,
`routing_rationale`, `depends_on`, `wave`, `required_tests`,
`tests_ci_does_not_run`, `risk`, `usage_class`, `labels`.

Render the gate in full prose before asking anything: every field of
every issue (name the routed model and effort plainly, and explain a
`reviewer_downgraded` via `routing_rationale`), then the run-level
fields (`plan_title`, `host`, `merge_strategy`, `config_revision` +
`config_overrides` if any, `memhub`, `ci`).

Render each entry of `tests_ci_does_not_run` against the required test it
names, not as a list of its own: the human is approving that command as a
check nothing but a local run will ever execute. An absent field is the
plan saying nothing about CI coverage — never report it as a finding that
CI runs every required test.

Then ask, via Codex's `request_user_input` primitive, **one** question
— header `Plan gate` — offering exactly these four options in order
(one question, nothing to batch):

- `Approve and enter Delivery`
- `Adjust agent routing`
- `Revise scope`
- `Cancel and remain read-only`

"Adjust agent routing" = revise the facts that drove the unwanted
routing and resubmit to `orch run plan` for a fresh gate; routing is
always re-derived, never edited directly. "Revise scope" = change what
the plan covers (issues, objectives, acceptance criteria) and resubmit
the same way. "Cancel and remain read-only" = no activation; return to
Assist conduct.

## Activation

On approval, call `orch run activate` with an `ActivationRequest`
(`schema_version: 2`) carrying the **identical** `PlanDoc` just gated
(same decoded content — the digest is recomputed server-side) plus:

```json
{
  "schema_version": 2,
  "plan": { "...": "the exact gated PlanDoc" },
  "approval": {
    "plan_digest": "sha256:...", "approved_by": "...",
    "approved_at": "2026-07-12T00:00:00Z",
    "statement": "approve-and-enter-delivery"
  }
}
```

`plan_digest` = `GateDoc.plan_digest`. Before GateDoc v3, this bound only the submitted plan; now it binds the effective contract: submitted scope, engine-contributed criteria, routing, and `execution` settings. Present `execution` (all six profiles, concurrency limit, merge strategy, memhub mode, and metrics setting) alongside the issues. Never compute approval from `PlanDoc.Digest` or reuse an old approval. The earlier byte-for-byte instruction is relaxed: JSON whitespace and object-key order do not change approval. Activation v1 is rejected; re-gate and obtain fresh approval with matching engine and adapter versions.

Before state v6, v5 recorded the contract version and effective execution digest. State v6 retains them and adds block causes and resolution history; v4/v5 remain inspectable, but lifecycle verbs and resume refuse to hot-migrate them: finish using the original engine, or abort and re-plan. Effective configuration drift, including local overrides without a revision change, also requires restoring the approved settings or aborting and re-planning. This restriction applies to every lifecycle verb and resume; engine-authorized issue escalation within the approved profiles remains supported.

`approved_by` = `git config
user.name`, falling back to `"human"`. `approved_at` = current time as
UTC RFC3339. `statement` is the exact literal
`approve-and-enter-delivery`.

The result (`ActivationResult`) carries `run_id` and, per issue,
`id`/`number`/`url`/`branch`/`worktree`. The run is now in Delivery.

## Per-issue loop

Work issues in wave order, never more than `concurrency.max_subagents`
in flight at once. For each issue:

### Durable Codex child observations

This path requires an engine supporting capture request/response schema 2
and observation schema 2. The adapter's `0.8` label alone does not establish
support: engine `47f0cbc` predates these contracts and rejects the new request.
Do not upgrade an engine or adapter during an active run. Finish older runs
with their original adapter's legacy path; use this path with a supporting
engine in a new run. Reject unsupported response versions or command failures;
do not silently fall back to delta capture.

Before dispatching a child, retain this Architect session's `CODEX_THREAD_ID`
and the canonical task identity returned by `spawn_agent`; never substitute
a shorthand or another agent's identity. Save the run/issue, actual attempt
identifier, role and review cycle with that task identity in a scratch JSON
request. After `wait_agent` reports that exact child complete, use the
scratch-file pattern above to call `orch hook codex subagent-usage`:

```json
{"schema_version": 2,
 "parent_thread_id": "<CODEX_THREAD_ID>", "task_identity": "<canonical task identity>",
 "run_id": "<ActivationResult.run_id>", "issue_number": 282,
 "role": "specialist", "attempt": "implementation-1",
 "unavailable_id": "issue-282-implementation-1-capture-check-1",
 "unavailable_at": "2026-09-30T12:10:00Z"}
```

Replace the example association with the actual dispatched work. Executor and
reviewer captures require an issue and attempt; reviewers also require a
positive `review_cycle`. Use `implementation-1` for the initial executor,
`repair-1` for its first completed repair, and separate `review-1`, `review-2`
attempts and cycles for fresh reviewers. These are caller-retained identities,
not values inferred from current routing. Persist `unavailable_id` and the UTC
check time `unavailable_at` before capture; retry with the same request after
an uncertain response. A later check gets a new check ID/time.

The response is `{"schema_version":2,"observation":{...}}`, optionally with
the observed `native_source`. Save it, extract only `observation` into the
recording request, and submit it unchanged to `orch metrics record` from the
main checkout. Require recording response schema 1. `recorded:false` with
`enabled:true` means an exact replay; `enabled:false` means storage is disabled.
Record before `complete` or `abort` clears the run. Retry the saved observation
after a storage failure; never alter its ID, time, attempt or cycle to bypass
a conflict. No token baseline belongs in conversational context.

Capture every completed agent separately. A `followup_task` resumes the same
executor rollout: capture its new cumulative sample with the repair attempt
and the cycle that requested that repair. Fresh reviewers retain their own
native sessions and review cycles. The recorder durably subtracts previous
samples in each session/stream; restarting capture or replaying completion
cannot add the same usage again. Omit `usage` and `executor_usage` from
`pr-open` and `review` when using this observation path, including unavailable
capture; never also submit measured usage through the legacy delta path.

Only one persisted child matching both parent and canonical task identity is
accepted. Wrong-parent, sibling, duplicate, malformed or unfinished evidence
stays unavailable, with its reason in an exclusive `unavailable` payload.
Submit that observation too; it has no fake session, counter or failure outcome.
Unidentified unrelated corruption cannot poison the requested child's capture.
An absent counter stays unknown; explicit zero stays zero. Native timestamps
identify samples, not active time. Capture does not report model, effort or
elapsed/activity intervals: never fill observed fields from requested config.

An attributable completed Scout child uses the same path with `role:scout`
(omit issue/attempt/cycle for run-level scouting). Generic `metrics record`
also accepts independently attributable planning/scouting evidence. This
helper cannot capture the Architect/root by substituting parent totals. Record
missing Architect or Scout evidence with observation schema 2, a unique retained
ID/check time, the run, role, source and an explicit `unavailable.reason`, and
omit unknown session/usage. Do not invent host events, active time, or API
authentication fallbacks. See `docs/metric-observations.md` for mappings and limits.

Before this update, the unversioned helper's `{"total_tokens":N}` was the full
exact total, or the delta from `previous_total_tokens`. The adapter kept the
previous captured cumulative total, sent the initial executor full total to
`pr-open`'s `usage`, the fresh reviewer full total to that cycle's `review`
`usage`, and the resumed fix executor delta to the following `review`'s
`executor_usage`; unavailable capture returned `{}` and omitted that field.
After this update, that helper contract remains available to existing callers,
but this adapter uses only the versioned observation path above. Claude and
OpenCode manual usage contracts are unchanged.

1. **Dispatch** — `orch run dispatch` with
   `{"schema_version": 4, "issue_number": N}`. Result
   (`DispatchResult`): `branch`, `worktree`, `executor`, `reviewer`,
   `rationale`, `objective`, `acceptance_criteria`, `required_tests`,
   `tests_ci_does_not_run`.

2. **Dispatch the executor** — dispatch `orch-implementer` or
   `orch-specialist` (per the routed role) by naming the agent in your
   prompt; Codex has no per-spawn model override, so the agent that
   actually runs is whatever its project TOML under `.codex/agents/`
   (`model`, `model_reasoning_effort`) pins. Before dispatching, the
   selection **currently in force** for the routed role — `DispatchResult`'s
   `(model, effort)`, superseded by the most recent `EscalateResult`'s
   `(model, effort)` if the issue has been rerouted since dispatch,
   never the dispatch-time value once superseded — **must match a
   project `orch-*` agent TOML exactly**. The project TOMLs are the
   authority for what that match requires, not this list: by default
   they pin `orch-scout` gpt-5.6-luna/max, `orch-implementer`
   gpt-6.1-sol/xhigh, `orch-specialist` gpt-6.1-sol/max,
   `orch-reviewer` gpt-6-astra/medium, `orch-reviewer-safe`
   gpt-6.1-sol/high, but a repository that overrides
   `hosts.codex.roles` and re-renders with `orch render-agents` gets
   different pins. **If no project TOML matches the routed selection,
   stop and tell the human — never dispatch a mismatched agent, and
   never report the routed selection as if it ran.** Every dispatch
   prompt opens with:

   ```
   Routed selection: <model> @ <effort>
   ```

   Effort is a real host parameter on Codex: `model_reasoning_effort` is
   pinned in the dispatched agent's own project TOML and is what
   actually runs, not layered on afterward, so there is no prompt cue
   standing in for it the way there is on Claude. The host enforces
   whatever TOML was dispatched, not that it matches the routed
   selection — dispatching the TOML matching the routed selection above
   is Architect discipline the engine does not verify. The opening line
   is a statement of fact, not a behavioral nudge. Transcribe
   `DispatchResult.objective`, `.acceptance_criteria`, and
   `.required_tests` into the prompt **verbatim** — this is the text a
   human approved at the plan gate, not the Architect's recollection of
   it — along with the worktree path and branch. Transcribe each entry of
   `.tests_ci_does_not_run` against the required test it names, so the
   executor is told which of its required checks CI will not repeat; drop
   nothing, an unstated one reads as a check CI holds.

   Before dispatching, you (the Architect) perform whatever memhub
   recall is relevant to the issue, with the main checkout as cwd —
   never a worktree. Embed the relevant recall results directly in the
   dispatch prompt; the executor agent never invokes memhub itself.

3. **PR-open** — once the executor reports verification evidence, call
   `orch run pr-open`:

   ```json
   {"schema_version": 1, "issue_number": N,
    "verifications": [{"name": "...", "command": "...", "result": "...", "detail": "..."}]}
   ```

   At least one verification is required. The verification names
   `required-ci`, `merge`, `abandoned`, and `review-cycle-<n>` are
   engine-owned and are rejected with `ErrBadRequest` before any
   mutation when supplied by a caller. A verification whose text
   describes the branch as a whole — commit counts, file counts, diff
   totals, or scope claims — must be named with the `branch-scope:`
   prefix at this first submission, not on a later cycle: the `name`
   is the identity `orch run review`'s replace-by-name upsert matches
   on, so a prefix added later appends a second entry instead of
   replacing the original, and the unprefixed original persists in the
   audit record permanently. This prefix does not collide with the
   engine-owned names `required-ci`, `merge`, `abandoned`, and
    `review-cycle-<n>`. `usage` remains optional for legacy callers;
    this adapter omits it and records the initial executor observation
    through the durable path above. Result
    carries `pr_number`, `pr_url`.

4. **Dispatch the reviewer** — once the PR stops changing, dispatch
   `orch-reviewer` **fresh** (a new instance, not the executor
   continuing), following the same TOML-match rule as the executor
   above. Base the choice on the reviewer selection **currently in
   force** — `DispatchResult.reviewer`, superseded by the most recent
   `EscalateResult.reviewer` if the issue has been rerouted since
   dispatch, never the dispatch-time value once superseded. If that
   selection names the §10
   safe-downgrade profile instead of the standard reviewer profile,
   dispatch `orch-reviewer-safe` by name — it is the project TOML that
   encodes that downgrade, since Codex has no per-spawn model override
   to apply it ad hoc. `reviewed_head_oid` must be the PR's **live**
   head OID at review time (e.g. via `gh pr view`), never a cached
   value.

5. **Review** — the reviewer produces **one consolidated report**
   (acceptance criteria, scope, correctness, tests, CI, security,
   manifest accuracy). Call `orch run review` with `reviewer` set to
   the selection **currently in force**: the most recent
   `EscalateResult.reviewer` when an escalation has rerouted the issue
   since dispatch, or `DispatchResult.reviewer` otherwise.
   `orch run review` compares the submitted `reviewer` against the
   issue's current routing decision and refuses a mismatch;
   `orch run dispatch` cannot be re-run to refresh a stale value — it
   accepts only the `worktree-ready` phase, which a dispatched issue has
   already left, so you must track whichever value is current yourself:

   ```json
   {"schema_version": 3, "issue_number": N, "reviewed_head_oid": "...",
    "verdict": "approve|request-changes", "summary": "...",
    "reviewer": {"model": "...", "effort": "..."},
    "judgments": [{"criterion": 1, "judgment": "satisfied|unsatisfied|wrong", "reason": "..."}],
    "verifications": [{"name": "...", "command": "...", "result": "...", "detail": "..."}]}
   ```

   `judgments` is required and carries exactly one entry per acceptance
   criterion the issue holds: `criterion` is the criterion's 1-based
   position in the issue's approved acceptance criteria, `judgment` is
   one of `satisfied`, `unsatisfied`, `wrong`, and `reason` is the
   reviewer's own reason for that call, which the audit record keeps as
   an engine-owned `acceptance-criterion-<n>` verification you never
   supply yourself. The engine counts the criteria from its own state,
   so the request cannot decide how many it is answering — a missing,
   duplicated, or out-of-range criterion is refused before any mutation.
   On a risk-domain issue that count includes the engine-contributed
   blast-radius criterion, last in the list and judged on the same terms
   as every criterion the plan document supplied.
   A `verdict` of `approve` is accepted only when every criterion is
   judged `satisfied`; record `request-changes` otherwise. Transcribe
   the reviewer's per-criterion calls, never your own reading of its
   summary.

   `usage` remains optional for legacy callers. This adapter omits it:
   every fresh reviewer has its own recorded native session observation.
   A criterion judged `wrong` is the needs-human outcome, and this one
   `orch run review` call makes it: the engine blocks the issue and
   flags it needs-human itself, and the result says so in `phase`,
   `wrong_criteria`, and `blocked_reason`. There is no second verb call
   to make — do not call `orch run escalate` for it and do not resume
   the executor. Instead surface the rejected criterion and reason to
   the human, together with the returned `blocked_reason`, and stop
   working the issue. A `request-changes` based on
    a code finding resumes the same executor with `followup_task` in the
    **same worktree** on the same branch: it fixes and pushes, then a
    **fresh** reviewer is dispatched (step 4) and `orch run review` is
    called again.
   `executor_usage` retains its optional delta contract for legacy callers.
   This adapter omits it and records the resumed executor's cumulative sample
   under its repair attempt/cycle before recording the fresh reviewer.
   `orch run pr-open` is not reachable a second time, so
   `verifications` is optional and takes pr-open's input shape — use
   it to carry evidence re-run on the fix commit (e.g. tests re-run
   before requesting the next review) into the audit record. The same
   verification names as pr-open — `required-ci`, `merge`, `abandoned`,
   and `review-cycle-<n>` — are engine-owned and are rejected with
   `ErrBadRequest` before any mutation when supplied by a caller.
   A verification whose text describes the branch as a whole —
   commit counts, file counts, diff totals, or scope claims —
   becomes false as soon as the executor pushes a fix commit, and
   must be resubmitted on every subsequent review cycle under the
   same `branch-scope:`-prefixed `name` chosen at pr-open. This
   works because caller-supplied verification entries are
   replace-by-name upserts: submitting the same `name` again on
   `orch run review` re-stamps that entry at the live head and
   supersedes its stale text; the engine's own `review-cycle-<n>`
   entries are appended, not replaced, each cycle. A reviewer's
   non-blocking findings default to the backlog rather than into
   the current fix cycle, and are folded into the current cycle
   only when they sit inside text the blocking fix already
   touches. `approve` continues.

6. **CI** — `orch run ci` with
   `{"schema_version": 1, "issue_number": N}` records the honest
   tri-state required-CI result (never conflate no-checks with
   passing).

7. **Merge-report** — `orch run merge-report` with
   `{"schema_version": 1, "issue_number": N}` requires an approving
   last review and mergeable CI, and pins the PR's live head as the
   approved head. Result: `pr_number`, `pr_url`, `head_oid`,
   `merge_strategy`, `ci` (`{state, required, total}`),
   `review_cycles`, `config_revision`, and `no_ci_statement` (present
   only when no required CI checks exist — show it plainly so "no CI
   gates this merge" is never silently implied).

8. **Merge gate** — present the full merge report, then ask, via
   Codex's `request_user_input` primitive, **one** question — header
   `Merge gate` — offering exactly `Approve merge` / `Not yet`. This
   approval is **fresh for every PR**, never inherited.

9. **Merge** — on approval, call `orch run merge`:

   ```json
   {"schema_version": 1, "issue_number": N,
    "approval": {"pr_number": N, "head_oid": "...", "approved_by": "...",
                  "approved_at": "2026-07-12T00:00:00Z", "statement": "approve-merge"}}
   ```

   `pr_number` and `head_oid` are pinned to exactly what `merge-report`
   returned (drift is rejected — re-run `merge-report`). `statement` is
   the exact literal `approve-merge`.

10. **Cleanup** — `orch run cleanup` with
    `{"schema_version": 1, "issue_number": N, "statement": "cleanup-issue"}`
    removes the remote branch, worktree, and local branch as one act.

11. **Complete** — once every issue is cleaned, call `orch run
    complete` with `{"schema_version": 1}` (run-level, no issue
    number). Result carries `run_id`, `merged`, `abandoned`,
    `returned_to`, `memhub_wrapup_due`. When `memhub_wrapup_due` is
    `true`, wrap up memhub (orch-architect: main checkout as cwd,
    Architect-only writes) before announcing the return to Assist.

## Escalation

On unusual difficulty, reviewer uncertainty, or repeated weak-model
failure, call `orch run escalate`:

```json
{"schema_version": 2, "issue_number": N, "trigger": "...", "detail": "..."}
```

`trigger` ∈ `scout-uncertainty`, `implementer-hard-execution`,
`weak-model-failure`, `reviewer-uncertainty`, `architectural-ambiguity`.
Result `kind`:

- `reroute` — carries a new `executor`/`reviewer` and `rationale`; on a
  chain of two or more reroutes on the same issue, each call's result
  fully replaces the routing decision, so this result is now the most
  recent `EscalateResult` and the only one describing the routing in
  force, for both roles even if only one changed. Before dispatching
  either into the **same worktree** (never a new one), confirm the new
  selection's `(model, effort)` against a project `orch-*` agent
  TOML under the same match rule as the dispatch steps above — **if no
  project TOML matches the new selection, stop and tell the human —
  never dispatch a mismatched agent, and never report the routed
  selection as if it ran.**
- `return-to-architect` — the issue is blocked for human design work;
  report `reason` and do not push it forward yourself.

## Block and abandon

On a secret in the working tree, a hook failure, an auth problem, a
GitHub API failure, a validation failure, or anything else that stops
progress, call `orch run block`:

```json
{"schema_version": 2, "issue_number": N,
 "class": "secret|hook|auth|github|validation|other|human-decision", "detail": "..."}
```

A `secret` class **stops the entire run** (`run_stopped: true`): every
mutating verb but `block` itself is refused until the human runs
`orch abort` or `orch resume`. Report a secret-class block immediately
and prominently, and make no further verb calls for the run.

Before block v2 / resume v2, a later block replaced the reason and healthy
artifacts could recover every blocked issue. Now review wrong-criterion,
return-to-architect, secret, validation, other, and human-decision blocks stay
blocked until an explicit decision; only hook/auth/github and reconciliation
operational failures recover from observations. Generic failures never replace
an unresolved decision. Clearing a run stop with `orch resume --resume-stopped-run`
does not resolve an issue decision.

Read StatusDoc v4 for the issue's current `blocks` entry (its issue-local `id`,
`cause`, original `reason`, and optional `resolution`). Present the original
reason. Only after a human/Architect explicitly resolves that exact block while
keeping approved scope and criteria unchanged, call `orch run resolve-block`:

```json
{"schema_version": 1, "run_id": "...", "issue_number": N, "block_id": 1,
 "resolved_by": "...", "detail": "Decision and why approved work remains unchanged",
 "statement": "resolve-block-without-scope-change"}
```

Reject unsupported result versions: resolve-block v1, block v2, resume v2.
Resolution is recorded against the original block and leaves its phase blocked;
run `orch resume` afterward to reconcile artifacts. It does not reset attempts,
change criteria, authorize a merge, or bypass a stopped run. Changed scope or
criteria require aborting and returning through the plan gate for fresh approval.
Before resume v2, audit text could repopulate approved work; now edited GitHub
text cannot replace it. Legacy blocks without a cause require fresh plan approval,
not a guessed resolution. Preserve branches, worktrees and evidence throughout.

To abandon an issue without merging (closes its PR and issue, keeps
branch/worktree for cleanup), call `orch run abandon`:

```json
{"schema_version": 1, "issue_number": N, "reason": "...", "statement": "abandon-issue"}
```

`statement` is the exact literal `abandon-issue`. An abandoned issue
still needs `orch run cleanup` before `orch run complete` can succeed.
