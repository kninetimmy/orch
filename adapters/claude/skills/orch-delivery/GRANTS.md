# Working under an autonomy grant

Read this only while an autonomy grant is active (`orch grant` shows it; the
SessionStart hook names it). A grant lets the Architect approve the plan gate,
the merge gate, and its own wrap-up drafts, within terms the user approved.
Without an active grant every gate stays with the user exactly as the
orch-delivery skill describes. Everything else in orch-delivery still applies;
this file only changes who approves and what you do when you must stop.

## What a grant never covers

Abandoning an issue, resolving a block, resuming a stopped run, evaluation-run
approvals, `memhub review accept`, machine-global memhub writes, releases and
tags, and creating or widening a grant. Those stay with the user.

## Setting up a grant

Only when the user asks for one.

1. Draft a Proposal (`schema_version: 1`): `scope` (named items, each
   `{name, description}`), `gates` (any of `plan`, `merge`, `wrap-up`),
   `expires_at`, `run_limit`, `merge_limit`, optional `fix_cycle_limit` and
   `context_threshold_tokens`, and `relay_permission_mode`.
2. Pipe it to `orch grant preview` (writes nothing). It prints the complete
   terms and a `digest`.
3. Show the user every term and the digest, then ask them to approve. Make no
   change to the proposal after previewing; a changed term needs a new preview.
4. Tell the user to add one exact allow rule for the relay to their own
   `~/.claude/settings.json`, under `permissions.allow`:
   `Bash(orch grant relay)`. The permission classifier refuses a session that
   adds its own allow rule, so never add or edit that rule yourself. Ask them
   to confirm it is in place.
5. On approval, pipe a CreateRequest to `orch grant create`:

   ```json
   {"schema_version": 1, "terms": { "...": "the previewed terms, unchanged" },
    "approval": {"grant_digest": "sha256:...", "approved_by": "...",
                 "approved_at": "2026-07-12T00:00:00Z",
                 "statement": "approve-autonomy-grant"}}
   ```

   `grant_digest` is the preview's `digest`; `approved_by` and `approved_at`
   follow the activation rules in orch-delivery. The create must run in the
   session whose `CLAUDE_CODE_SESSION_ID` equals `terms.session_id`.

`orch grant revoke` ends a grant; only the user asks for that.

## Approving inside the grant

Build the plan and gate it as usual. Then, instead of asking the user, approve
yourself only when all of these hold; otherwise take the normal user gate.

- The grant covers the gate, and the plan stays within its scope. Every issue's
  `objective` begins with `Grant scope item: <name>.`, where `<name>` is the
  exact name of the one scope item the issue serves, so the audit record names
  it. An issue that serves no scope item cannot be self-approved.
- No issue declares a risk domain. The grant cannot approve such a plan; it
  waits for the user's plan gate.

Plan activation is the same `ActivationRequest` as a human approval, with:

```json
"approval": {
  "plan_digest": "sha256:...", "approved_by": "grant:<id>",
  "approved_at": "2026-07-12T00:00:00Z",
  "statement": "grant-approve-and-enter-delivery"
}
```

Merge is the same request as a human merge approval, with:

```json
"approval": {"pr_number": N, "head_oid": "...", "approved_by": "grant:<id>",
             "approved_at": "2026-07-12T00:00:00Z",
             "statement": "grant-approve-merge"}
```

`<id>` is the grant's id. Still present the full merge report and judge it
yourself before approving; the approval is fresh for every PR.

A refused grant approval fails with "autonomy grant cannot approve this: ...".
That decision now waits for the user: present stderr verbatim, notify, and
continue with independent issues. Never retry it as a human approval. The same
applies when a third non-approving review blocks an issue as a human decision;
only the user resolves that.

## Judgment stops

Stop on any of these. Each blocks only the affected issue (`orch run block`,
class `human-decision`, the request shape in orch-delivery); independent issues
continue.

- An acceptance criterion that looks wrong or unsatisfiable.
- Work that needs a scope change, a new dependency, or anything the grant does
  not name.
- An architectural choice with more than one reasonable answer.
- Anything you would have asked the user about in a manual run.

## Telling the user

On every stop, when the grant ends, and at each run's completion, send a
PushNotification and give a decision packet: what stopped, why, the options,
and your recommendation. At each run's completion, also list the PRs merged
under the grant.

## Wrap-up and relay

Under a grant you approve your own wrap-up drafts. Facts and decisions stay
staged for the user's `memhub review accept`. Never make machine-global memhub
writes, and leave architecture-narrative updates for the user. Make no git
commit for the wrap-up.

Relay at each run's completion, and whenever the threshold hook says to relay
at the next stopping point (no subagent in flight, no verb half-done). In order:

1. Wrap up.
2. Log a handoff session note recording anything in flight that Orch's run
   state does not hold.
3. Run `orch grant relay`. It hands the grant to a background successor.
4. Your last message says you handed off and this session should not be used
   again. Then stop using the session.

A newly started session that holds the grant reads the latest handoff session
note before acting.
