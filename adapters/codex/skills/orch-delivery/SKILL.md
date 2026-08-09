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
`GateDoc` (`schema_version: 1`): `plan_digest`, `plan_title`, `host`,
`config_revision`, `config_overrides`, `merge_strategy`, `memhub`
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
(`schema_version: 1`) carrying the **identical** `PlanDoc` just gated
(byte-for-byte — the digest is recomputed server-side) plus:

```json
{
  "schema_version": 1,
  "plan": { "...": "the exact gated PlanDoc" },
  "approval": {
    "plan_digest": "sha256:...", "approved_by": "...",
    "approved_at": "2026-07-12T00:00:00Z",
    "statement": "approve-and-enter-delivery"
  }
}
```

`plan_digest` = `GateDoc.plan_digest`. `approved_by` = `git config
user.name`, falling back to `"human"`. `approved_at` = current time as
UTC RFC3339. `statement` is the exact literal
`approve-and-enter-delivery`.

The result (`ActivationResult`) carries `run_id` and, per issue,
`id`/`number`/`url`/`branch`/`worktree`. The run is now in Delivery.

## Per-issue loop

Work issues in wave order, never more than `concurrency.max_subagents`
in flight at once. For each issue:

### Exact Codex child usage

Before dispatching a child, retain this Architect session's
`CODEX_THREAD_ID`. For every completed child, retain the canonical task
identity returned by `spawn_agent`; never substitute a task-name
shorthand or reuse an identity from another agent. After `wait_agent`
reports that exact child complete, use the scratch-file JSON pattern
above to call `orch hook codex subagent-usage` with:

```json
{"parent_thread_id": "<CODEX_THREAD_ID>", "task_identity": "<canonical task identity>"}
```

Without `previous_total_tokens`, `{"total_tokens": N}` is the child's
full exact cumulative total. A `followup_task` resumes the same executor
rollout, so save that initial full total and pass it back for the next
completion:

```json
{"parent_thread_id": "<CODEX_THREAD_ID>", "task_identity": "<same executor task identity>", "previous_total_tokens": N}
```

That response's `total_tokens` is the exact non-negative delta from the
previous captured cumulative total. Update the stored cumulative total to
`previous_total_tokens + N` before any later resume. `{}` means capture
is unavailable: do not retry with a different task, inspect a parent
total, estimate, or update the stored total. The helper accepts only one
persisted child rollout whose parent thread and canonical task identity
both match, so a parent session, sibling child, or different parent's
child is never a candidate. Capture every completed agent separately:

- Send the initial executor full total to `pr-open`'s `usage`.
- Send the fresh reviewer full total to that cycle's `review` `usage`.
- Send the resumed fix executor delta to the following `review`'s `executor_usage`.

When capture returns no total, omit the corresponding optional field.

1. **Dispatch** — `orch run dispatch` with
   `{"schema_version": 3, "issue_number": N}`. Result
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
   gpt-5.6-terra/max, `orch-specialist` gpt-5.6-sol/max,
   `orch-reviewer` gpt-5.6-sol/xhigh, `orch-reviewer-safe`
   gpt-5.6-sol/high, but a repository that overrides
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
    `review-cycle-<n>`. `usage` is optional (PRD §21) and is the
     initial executor's own full cost: add only its captured
     `{"total_tokens": N}` when available, never an estimate. Result
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
   {"schema_version": 2, "issue_number": N, "reviewed_head_oid": "...",
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

     `usage` is optional (PRD §21), same rule as PR-open: every reviewer
     is fresh, so add only its full exact `{"total_tokens": N}` when
     available.
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
   Because this is the only verb call on that cycle, `executor_usage`
   (same optional shape as `usage`) carries the executor's
     fix-and-push delta for the cycle just finished — pass that
     executor's previous captured cumulative total to the helper and
     report only its exact `{"total_tokens": N}`, never an estimate,
     and omit it when there was no fix cycle (the first review after
     pr-open).
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
{"schema_version": 1, "issue_number": N, "trigger": "...", "detail": "..."}
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
{"schema_version": 1, "issue_number": N,
 "class": "secret|hook|auth|github|validation|other", "detail": "..."}
```

A `secret` class **stops the entire run** (`run_stopped: true`): every
mutating verb but `block` itself is refused until the human runs
`orch abort` or `orch resume`. Report a secret-class block immediately
and prominently, and make no further verb calls for the run.

To abandon an issue without merging (closes its PR and issue, keeps
branch/worktree for cleanup), call `orch run abandon`:

```json
{"schema_version": 1, "issue_number": N, "reason": "...", "statement": "abandon-issue"}
```

`statement` is the exact literal `abandon-issue`. An abandoned issue
still needs `orch run cleanup` before `orch run complete` can succeed.
