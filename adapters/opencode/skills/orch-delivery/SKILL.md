---
name: orch-delivery
description: Drive an Orch Delivery run from OpenCode V2 through plan, dispatch, review, CI, merge, cleanup, and completion.
---

# Orch Delivery

The `orch run` engine makes every policy decision. Put each JSON request in an
OS temporary file outside the repository and pipe it to the verb. Parse stdout
only on exit 0; otherwise present stderr verbatim and stop.

## Plan and activation

Build an honest schema-1 plan naming host `opencode`. Routing comes only from
facts, never hand-picked model fields. Every issue includes objective,
checkable acceptance criteria, type, existing area labels, routing facts,
dependencies, wave, exact required checks, and usage class. Dependencies must
be in earlier waves. Declare a required check as absent from CI only after
inspecting workflows.

Call `orch run plan`, then present every gate field: digest, host, config
revision and overrides, merge strategy, memhub result, CI statement, and each
issue's objective, criteria, role, executor, reviewer, rationale, dependencies,
wave, tests, risk, labels, and usage class. Ask exactly one native `question`
with these choices in order: `Approve and enter Delivery`, `Adjust agent
routing`, `Revise scope`, `Cancel and remain read-only`.

On approval, call `orch run activate` with the exact gated plan plus schema 1
approval containing the gate digest, approver, current UTC RFC3339 timestamp,
and statement `approve-and-enter-delivery`.

## Issue loop

Process issues in wave order within the configured concurrency cap.

1. Call `orch run dispatch` with schema 3 and the issue number.
2. Match the current routed `(model, effort)` to the generated project
   `.opencode/agents/orch-*.md` definition. Dispatch the routed implementation
   role with OpenCode's native `subagent` tool. Put the routed selection first
   in the prompt, then copy objective, criteria, tests, worktree, and branch
   verbatim. Stop rather than dispatching a mismatched definition.
3. After the executor commits, pushes, and reports checks, call `orch run
   pr-open` with schema 1 and those exact verifications. Prefix branch-wide
   evidence names with `branch-scope:`.
4. At the live PR head, dispatch a fresh `orch-reviewer` or
   `orch-reviewer-safe` with `subagent`, again matching the current routed
   model and effort to its generated definition.
5. Submit one schema-2 `orch run review` request containing the live head OID,
   current reviewer selection, verdict, summary, and exactly one numbered
   judgment per acceptance criterion. An approve verdict requires every
   judgment satisfied. A wrong criterion becomes needs-human; report it and
   stop. On code changes, send a new prompt to the same implementation
   subagent session, then dispatch a fresh reviewer after the fix.
6. Call schema-1 `orch run ci`; preserve the engine's passing/failing/no-checks
   distinction.
7. Call schema-1 `orch run merge-report` and present every field, including
   pinned head, strategy, CI, review count, config revision, and any no-CI
   statement.
8. Ask one native `question`: `Approve merge` or `Not yet`.
9. On approval, call schema-1 `orch run merge` with the reported PR and head,
   approver, current UTC timestamp, and statement `approve-merge`.
10. Call schema-1 `orch run cleanup` with statement `cleanup-issue`.
11. After all issues are cleaned, call schema-1 `orch run complete`.

OpenCode child-session usage is not available as an exact engine value. Omit
all optional usage fields; never estimate or substitute parent-session usage.

For escalation, call schema-1 `orch run escalate` with the issue, trigger, and
detail. A reroute fully replaces current executor and reviewer selections;
match and dispatch the new project agent. A return-to-architect result stops
the issue. For a blocking failure call schema-1 `orch run block`; a secret
class stops the whole run. Abandon only with schema 1 and statement
`abandon-issue`, then clean up normally.
