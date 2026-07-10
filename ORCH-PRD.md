# Orch Product Requirements Document

- Status: Draft for future implementation
- Last updated: 2026-07-10
- Intended implementation context: A new memhub-enabled repository

## 1. Product summary

Orch is a cross-host development orchestrator for Codex CLI and Claude Code CLI. It keeps a
frontier model in the Architect role while routing exploration, implementation, specialist work,
and review to explicitly configured models with appropriate reasoning levels.

Orch reduces unnecessary frontier-model usage without weakening development discipline. Every
tracked repository mutation must use an issue, isolated feature worktree, pull request,
independent review, CI or explicit targeted verification, and human-approved merge.

## 2. Problem

Using the same frontier model for architecture, file lookup, routine implementation, and review
is wasteful. Existing orchestration approaches, including the first
`fable-sonnet-orchestrator-kit` attempt, prove that delegation works, but they have limitations:

- Models and effort levels are hard-coded.
- Provider model names are conflated with task roles.
- Exploration remains in the expensive main context.
- Codex and Claude implementations duplicate workflow logic.
- GitHub and worktree enforcement is shell- and hook-heavy.
- Configuration lacks a native per-repository interview.
- Workflow failure is sometimes fail-open.
- Host-specific implementations can drift.

## 3. Goals

Orch must:

- Keep planning, architecture, routing, and final judgment with a frontier Architect.
- Delegate read-only exploration without polluting the Architect context.
- Delegate implementation according to difficulty and risk.
- Support exact model-version and reasoning-effort selection per role.
- Provide native arrow-key configuration dialogs inside both supported hosts.
- Require the complete Delivery workflow for every tracked-file mutation.
- Enforce Assist mode as read-only.
- Work independently with Codex CLI, Claude Code CLI, or both.
- Maintain behavioral parity through a shared cross-platform core.
- Integrate with memhub when present.
- Recover safely from interrupted sessions without losing work.
- Keep all merge decisions human-controlled.

## 4. Non-goals for v1

- IDE-extension or desktop-app integration.
- GitLab, Bitbucket, or other Git forges.
- Automatic merging.
- Direct edits to `main`.
- Emergency workflow-bypass commands.
- Moving model aliases as canonical configuration.
- Outbound telemetry.
- Recursive worker delegation.
- Transfer of an active Delivery run between Codex and Claude.
- Automated A/B evaluation of write-producing agents.
- Choosing the shared core's implementation language in this PRD.

## 5. Supported environments

- Codex CLI.
- Claude Code CLI.
- Windows, macOS, and Linux.
- GitHub through authenticated `gh`.
- Git repositories with isolated worktree support.

Assist mode may operate without a GitHub remote. Delivery mode fails closed unless GitHub,
authentication, repository state, and worktree prerequisites pass validation.

## 6. Architecture

Orch consists of a shared cross-platform core and two thin host adapters.

### Shared core

The shared core owns:

- Configuration parsing and validation.
- Assist/Delivery state.
- Exclusive Delivery-run locking.
- Safe path and worktree containment checks.
- Git and GitHub operations.
- Issue and PR manifests.
- Routing records and escalation state.
- Resume, abort, recovery, and cleanup.
- Memhub invocation.
- Optional local metrics.
- Cross-host compatibility validation.

### Host adapters

Codex and Claude adapters own:

- Native structured-question dialogs.
- Session lifecycle integration.
- Architect instructions.
- Subagent spawning and monitoring.
- Host-specific model and effort configuration.
- Translation of host events into shared-core transitions.

The detailed state machine must not be independently reimplemented in Markdown skills or
platform-specific shell scripts.

## 7. Operating modes

### Assist mode

Assist is the default state.

- The Architect and Scouts may inspect and explain.
- Scouts may run concurrently.
- Repository mutation is mechanically denied.
- A request that would change any tracked file triggers read-only planning.
- Declining a Delivery plan leaves the repository in Assist.

Tracked files include source, tests, documentation, configuration, generated files, and
orchestration configuration.

### Delivery mode

A mutation request is investigated read-only before activation. The plan gate simultaneously
approves the plan and enters Delivery.

Delivery is task-scoped. Orch returns automatically to Assist when all associated PRs are merged
or abandoned.

## 8. Human gates

The normal workflow has two required human gates.

### Plan and activation gate

The plan gate shows, for every proposed issue:

- Objective and acceptance criteria.
- Executor role.
- Exact executor model and effort.
- Routing rationale.
- Exact reviewer model and effort.
- Dependencies and dispatch wave.
- Required tests.
- Risk classification.
- Relative usage class.

Native choices:

- Approve and enter Delivery.
- Adjust agent routing.
- Revise scope.
- Cancel and remain read-only.

Even a one-line tracked-file change receives a compact one-issue plan.

### Merge gate

Every PR requires explicit human merge confirmation. Plan approval, review approval, or prior
merge approval never authorizes another merge.

Exceptional blockers may require clarification. Material changes to scope, architecture, issue
decomposition, or routing return to the plan gate.

## 9. Roles

### Architect

Owns:

- Requirements discovery.
- Architecture and design.
- Issue decomposition.
- Routing and escalation.
- Plan-gate presentation.
- Final review judgment.
- Human communication.
- Memhub write workflows.

The Architect never edits tracked repository files.

### Scout

Owns read-only work:

- File and symbol discovery.
- Code-path tracing.
- Documentation lookup.
- Evidence collection.
- Concise summaries for the Architect.

### Implementer

Executes narrow, approved, normally difficult changes. It follows the approved architecture and
works only in its assigned worktree.

### Specialist

Executes approved changes where implementation itself remains unusually difficult:

- Ambiguous cross-system debugging.
- Concurrency and synchronization.
- Authentication and authorization.
- Sensitive data handling.
- Database migrations and rollback.
- Performance and algorithmic work.
- Cross-cutting refactors with hidden invariants.
- Runtime or platform-specific behavior.

A Specialist does not silently redesign the approved contract.

### Reviewer

Independently verifies:

- Acceptance criteria.
- Scope discipline.
- Correctness and regression risk.
- Test evidence.
- CI state.
- Security and data-safety concerns.
- PR-manifest accuracy.

Review is a distinct role, not an implementation tier.

## 10. Default model profiles

All entries use exact model versions.

### Codex

| Role | Model | Effort |
|---|---|---|
| Architect | `gpt-5.6-sol` | `high` |
| Scout | `gpt-5.6-terra` | `low` |
| Implementer | `gpt-5.6-terra` | `high` |
| Specialist | `gpt-5.6-sol` | `medium` |
| Reviewer | `gpt-5.6-sol` | `medium` |
| Safe review downgrade | `gpt-5.6-terra` | `high` |

### Claude Code

| Role | Model | Effort |
|---|---|---|
| Architect | `claude-opus-4-8` | `xhigh` |
| Scout | `claude-sonnet-5` | `low` |
| Implementer | `claude-sonnet-5` | `xhigh` |
| Specialist | `claude-opus-4-8` | `high` |
| Reviewer | `claude-opus-4-8` | `high` |
| Safe review downgrade | `claude-sonnet-5` | `high` |

While available through subscription, a local ignored override may select:

```toml
architect.model = "claude-fable-5"
architect.effort = "xhigh"
```

If Fable 5 becomes durably available, changing the committed default requires a normal Delivery
PR.

## 11. Routing and escalation

- Scout uncertainty escalates to the Specialist model in read-only mode.
- An Implementer encountering unusually hard execution transfers the worktree to a fresh
  Specialist.
- Unresolved architectural ambiguity returns to the Architect.
- One meaningful weak-model failure triggers escalation; Orch does not repeatedly retry an
  underpowered model.
- A downgraded reviewer reporting uncertainty triggers a complete strong-model review.
- Security, authentication, authorization, secrets, cryptography, migrations, concurrency, data
  integrity, and destructive operations route directly to Specialist plus strong Reviewer.
- Uncertainty favors the stronger route.

Reviewer downgrade is permitted only when a change is affirmatively mechanical, low-risk, fully
specified, and unsurprising.

## 12. Delivery workflow

1. Receive a mutation request in Assist.
2. Read memhub context when required.
3. Investigate using read-only Scouts.
4. Prepare the Delivery plan.
5. Present the native plan gate.
6. On approval, create GitHub issues.
7. Create one feature branch/worktree per issue.
8. Dispatch one executor per issue.
9. Commit, push, open one PR, and record verification evidence.
10. Run independent review.
11. Consolidate all review findings into one response per fix cycle.
12. Escalate or resume as required.
13. Wait for required CI.
14. Present a ready-to-merge report.
15. Wait for explicit human approval.
16. Merge using the configured strategy.
17. Confirm issue closure and record the merge result.
18. Delete the merged remote branch.
19. Remove and prune the worktree.
20. Fast-forward the primary checkout.
21. Run memhub wrap-up when appropriate.
22. Return to Assist.

One issue maps to one branch/worktree and one PR.

## 13. GitHub model

### Labels

Every issue carries:

- One status: `ready`, `in-progress`, `blocked`, `needs-human`, or `awaiting-review`.
- One type: `feature`, `bug`, `chore`, `infra`, `docs`, or `research`.
- One role: `implementer` or `specialist`.
- One risk: `standard` or `critical`.
- Optional repository-defined area labels.

### Audit record

Every issue and PR records:

- Selected role.
- Exact executor model and effort.
- Routing rationale.
- Exact reviewer model and effort.
- Escalations or substitutions.
- Configuration revision.
- Named verification commands and results.

Models do not become GitHub labels.

## 14. Concurrency

- One active Delivery coordinator per repository across both hosts.
- The active run may contain multiple issues.
- Default maximum: three concurrent subagents.
- Initialization may configure another cap.
- Potentially conflicting writes serialize.
- Read-only Scouts may run concurrently within the cap.
- Review begins only after the reviewed PR stops changing.
- Unaffected issues continue while another issue is blocked.
- A second host may inspect status but cannot mutate or dispatch.

## 15. Enforcement and recovery

Orch fails closed.

- Assist denies tracked-file mutation.
- Only the registered executor may write in Delivery.
- Writes must remain inside the registered worktree.
- The active branch must not be `main`.
- Completion requires a commit, pushed branch, PR, targeted-test evidence, and explicit CI state.
- Reviewers are mechanically read-only.
- No agent may merge.
- Hook, authentication, GitHub, or validation failures produce a blocked run.
- Blocked worktrees and branches are preserved.
- `orch abort` stops dispatch and returns to Assist without deleting work.
- Cleanup or abandonment deletion requires explicit confirmation.
- No bypass command exists.

## 16. CI, dependencies, and merge policy

- Squash merge is the default repository strategy.
- Initialization may select squash, rebase, or merge-commit.
- Required CI must be green.
- If no CI exists, the plan gate states that explicitly.
- A failure is pre-existing only when reproduced on the base branch.
- New dependencies require human approval.
- Material dependency-driven scope changes return to the plan gate.
- Finding a secret stops the entire Delivery run.
- Cleanup failures keep the run incomplete.

## 17. Configuration

Committed canonical configuration:

```text
.orchestrator/config.toml
```

Ignored machine-local overrides:

```text
.orchestrator/config.local.toml
```

Requirements:

- Exact model versions.
- Per-host independent profiles.
- Configuration revision identifier.
- Schema version.
- Concurrency and merge settings.
- Memhub mode.
- Metrics setting.
- No secrets.
- Local overrides never weaken mandatory workflow policy.

Canonical configuration changes use Delivery. Local override changes use native confirmation but
do not require a tracked PR.

## 18. Initialization

Global host plugins are installed before repository initialization.

`orch init`:

1. Detects installed hosts.
2. Detects Git, GitHub, and memhub.
3. Explains Assist and Delivery.
4. Explains every role.
5. Collects exact model and effort choices through native dialogs.
6. Collects concurrency, merge, memhub, and metrics settings.
7. Shows the resulting configuration.
8. Shows proposed `AGENTS.md` and `CLAUDE.md` changes.
9. Presents final approval.
10. Creates a bootstrap issue.
11. Dispatches a bundled bootstrap executor.
12. Writes configuration in an isolated worktree.
13. Validates the installation.
14. Opens and reviews a bootstrap PR.
15. Waits for human merge approval.

Codex and Claude Code may be enabled independently. Adding another host later uses Delivery.

## 19. Managed instruction blocks

Orch creates or updates root `AGENTS.md` and `CLAUDE.md` as applicable.

```md
<!-- orchestrator:managed:start version=1 -->
...
<!-- orchestrator:managed:end -->
```

Rules:

- Show exact diffs before approval.
- Preserve user-authored content and formatting.
- Never modify global instruction files.
- Stop on conflicting instructions.
- Report nested-file conflicts without silently editing them.
- Detect stale managed-block versions.
- Upgrade blocks only through Delivery.
- Remove only the managed block during deinitialization.
- Delete an entire tool-created file only when empty otherwise and explicitly approved.

The block stays concise and points to the canonical configuration and plugin workflow.

## 20. Memhub integration

Memhub is an optional product adapter but required in the intended target repository.

When required:

- Delivery planning blocks if memhub health or recall fails.
- The Architect reads rendered `PROJECT.md` once at session start.
- The Architect performs relevant recall before planning.
- Scouts and executors may perform read-only recall.
- Memhub commands execute with the main checkout as an explicit process working directory.
- Worktrees never receive copied databases.
- Subagents never write, render, reindex, sync, or accept memhub state.
- Only the Architect initiates memory writes.
- Memhub's approval gates remain intact.
- Wrap-up occurs after merges and durable decisions.
- GitHub remains operational state; memhub remains durable knowledge.
- Orch creates no competing rolling-memory ledger.
- Reindexing always requires human approval.

Initialization supports `required`, `best-effort`, and `off`. The intended repository selects
`required`.

## 21. Metrics and evaluation

Metrics are off by default.

When explicitly enabled, local gitignored storage may record:

- Role, model, and effort.
- Routing rationale.
- Available token and cache counts.
- Duration.
- Success, block, retry, and escalation.
- First-pass review outcome.
- Reviewer model and verdict.
- CI state.
- Model fallback.

No data is transmitted externally.

Future evaluation may compare two configurations on the same read-only Scout or Reviewer task.
Write-producing A/B evaluation is out of scope for v1.

## 22. Command surface

Logical commands are consistent across adapters:

```text
orch init
orch status
orch doctor
orch configure
orch configure-local
orch resume
orch abort
orch metrics
```

There is no manual Delivery command. Plan approval activates Delivery, and completion returns to
Assist.

## 23. Acceptance criteria

V1 is acceptable when:

- Both CLIs can initialize independently using native question dialogs.
- Initialization creates a reviewed bootstrap PR without editing the primary checkout.
- Assist reliably blocks tracked-file changes.
- Delivery cannot write outside registered worktrees.
- Every mutation receives an approved issue and PR.
- Exact model and effort selections appear in issues and PRs.
- Routing and conservative escalation behave as configured.
- Only one cross-host Delivery run may own a repository.
- Interrupted runs resume without losing branches or worktrees.
- No merge occurs without explicit human approval.
- CI and targeted verification are reported honestly.
- Managed instruction blocks preserve existing content.
- Required memhub failure blocks Delivery planning.
- Disabled metrics create no metrics storage.
- Successful merge leaves the primary checkout current and no stale worktree behind.
- Behavior is validated on Windows, macOS, and Linux.
- Codex and Claude adapters pass shared parity tests.

## 24. Deferred implementation decisions

The implementation plan must later choose:

- Shared-core language and runtime.
- Packaging and update mechanism.
- Cross-platform process and locking primitives.
- Host minimum versions.
- Native question-tool compatibility handling.
- State-storage format.
- Test harness and fixture repositories.
- Whether Fable 5 becomes the committed Architect default.
- When to evaluate lower Implementer effort and Scout alternatives.
