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

## Plan gate

Call `orch run plan` with the `PlanDoc` on stdin. The result is a
`GateDoc` (`schema_version: 1`): `plan_digest`, `plan_title`, `host`,
`config_revision`, `config_overrides`, `merge_strategy`, `memhub`
(`{mode, probe, recall, detail}`), `ci` (`{workflows_present, statement}`), and
`issues[]` — each with `id`, `title`, `objective`,
`acceptance_criteria`, `role`, `executor` (`{model, effort}`),
`reviewer` (`{model, effort}`), `reviewer_downgraded`,
`routing_rationale`, `depends_on`, `wave`, `required_tests`, `risk`,
`usage_class`, `labels`.

Render the gate in full prose before asking anything: every field of
every issue (name the routed model and effort plainly, and explain a
`reviewer_downgraded` via `routing_rationale`), then the run-level
fields (`plan_title`, `host`, `merge_strategy`, `config_revision` +
`config_overrides` if any, `memhub`, `ci`).

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

1. **Dispatch** — `orch run dispatch` with
   `{"schema_version": 1, "issue_number": N}`. Result
   (`DispatchResult`): `branch`, `worktree`, `executor`, `reviewer`,
   `rationale`.

2. **Dispatch the executor** — dispatch `orch-implementer` or
   `orch-specialist` (per the routed role) by naming the agent in your
   prompt; Codex has no per-spawn model override, so the agent that
   actually runs is whatever its installed TOML (`model`,
   `model_reasoning_effort`) pins. Before dispatching, `DispatchResult`'s
   `(model, effort)` for the routed role **must match an installed
   `orch-*` agent TOML exactly** — `orch-scout` gpt-5.6-terra/low,
   `orch-implementer` gpt-5.6-terra/high, `orch-specialist`
   gpt-5.6-sol/medium, `orch-reviewer` gpt-5.6-sol/medium,
   `orch-reviewer-safe` gpt-5.6-terra/high. **If no installed TOML
   matches the routed selection, stop and tell the human — never
   dispatch a mismatched agent, and never report the routed selection as
   if it ran.** Every dispatch prompt opens with:

   ```
   Routed selection: <model> @ <effort>
   ```

   Effort is a real host parameter on Codex, pinned in the dispatched
   agent's own TOML — there is no prompt cue to layer on top of it; the
   opening line is a statement of fact, not a behavioral nudge. Include
   the worktree path, branch, objective, acceptance criteria, and
   required tests in the prompt.

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

   At least one verification is required. Result carries `pr_number`,
   `pr_url`.

4. **Dispatch the reviewer** — once the PR stops changing, dispatch
   `orch-reviewer` **fresh** (a new instance, not the executor
   continuing), following the same TOML-match rule as the executor
   above. If `DispatchResult.reviewer` names the §10 safe-downgrade
   profile instead of the standard reviewer profile, dispatch
   `orch-reviewer-safe` by name — it is the installed TOML that encodes
   that downgrade, since Codex has no per-spawn model override to apply
   it ad hoc. `reviewed_head_oid` must be the PR's **live** head OID at
   review time (e.g. via `gh pr view`), never a cached value.

5. **Review** — the reviewer produces **one consolidated report**
   (acceptance criteria, scope, correctness, tests, CI, security,
   manifest accuracy). Call `orch run review`, echoing
   `DispatchResult.reviewer` **verbatim**:

   ```json
   {"schema_version": 1, "issue_number": N, "reviewed_head_oid": "...",
    "verdict": "approve|request-changes", "summary": "...",
    "reviewer": {"model": "...", "effort": "..."}}
   ```

   `request-changes` loops the same executor in the **same worktree**,
   then repeats from PR-open. `approve` continues.

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

- `reroute` — carries a new `executor`/`reviewer` and `rationale`;
  dispatch the new selection into the **same worktree**, never a new
  one.
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
