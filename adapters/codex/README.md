# Codex CLI adapter

## What this is

This directory is the Codex CLI host adapter for Orch: a Codex plugin
that connects a Codex CLI session to the shared `orch` binary. It is
deliberately thin (PRD §6) — "the detailed state machine must not be
independently reimplemented in Markdown skills or platform-specific
shell scripts." Every policy decision (what is writable, what routing
applies, what a Delivery run's state is) is made by the `orch` binary.
This adapter only translates Codex CLI host events into calls against
that binary, presents native dialogs, and dispatches role agents. It
never re-derives a decision the engine already made.

The Claude Code adapter lives at `adapters/claude/` and passes the same
shared parity suite (`internal/adaptertest`) this adapter's plugin tests
do.

## Artifact map

- `.codex-plugin/plugin.json` — the plugin manifest (name, description,
  version, author). The `skills/` and `hooks/hooks.json` component
  directories are auto-discovered by Codex CLI; there is nothing else to
  declare here yet. Codex plugins cannot bundle agent TOMLs or prompts
  (see Install order below).
- `hooks/hooks.json` — two hooks:
  - `PreToolUse` on `apply_patch` runs `orch guard codex`, which reads
    the tool-call event on stdin and denies the write when Orch's
    policy (Assist read-only, Delivery worktree containment) says so.
    All file mutations on Codex CLI — create, update, delete, rename —
    route through this single tool, so this one matcher covers every
    file mutation the same way Claude Code's four-tool matcher does. An
    allow is silence: guard never bypasses Codex CLI's own approval and
    sandbox prompts.
  - `SessionStart` on `startup|resume` runs
    `orch hook codex session-start`, which injects a short context block
    at session start when the current directory is inside an Orch
    repository with a readable configuration and state: the operating
    mode, the memhub mode, a reminder to load the `orch-architect` skill
    before planning or making a change, and a pointer to the
    `orch-setup` skill for the three setup interviews. Outside an Orch
    repository, or if the repository is unreadable, it injects nothing
    and never blocks the session. (Codex has no `clear`/`compact`
    SessionStart sources to mirror Claude Code's matcher, hence
    `startup|resume` only.)
- `skills/orch-architect/SKILL.md` — the Architect's standing posture:
  never edit tracked files directly, never re-derive engine policy, the
  `orch run status --json` / PROJECT.md / memhub-recall session ritual,
  mode conduct, the five-agent dispatch whitelist and the
  `concurrency.max_subagents` cap, and memhub write discipline.
- `skills/orch-delivery/SKILL.md` — the Delivery wire contract: the
  scratch-file JSON request/response pattern for every `orch run <verb>`
  call, `PlanDoc` construction, the plan gate and its four-option `ask`
  question, activation, the full per-issue dispatch → execute → pr-open
  → review → ci → merge-report → merge gate → merge → cleanup loop,
  completion and the memhub wrap-up cue, escalation, and block/abandon.
- `skills/orch-setup/SKILL.md` — the shared step-loop driver for the
  three `orch <cmd> --step` interviews: the resubmitted-in-full
  `AnswerSet`, one-question-at-a-time presentation of `questions`
  documents via Codex's `ask` primitive, the `summary`/blockers/
  `complete`/`aborted` document kinds, and each interview's terminal
  form.
- `agents/orch-scout.toml`, `agents/orch-implementer.toml`,
  `agents/orch-specialist.toml`, `agents/orch-reviewer.toml`,
  `agents/orch-reviewer-safe.toml` — the five role agents the Architect
  dispatches during Delivery, each pinning its own `model` and
  `model_reasoning_effort`. `orch-reviewer-safe` is the §10 safe-review
  downgrade encoding: since Codex has no per-spawn model override, the
  downgrade the engine computes for a mechanical/low-risk/
  fully-specified/unsurprising issue has to be an installed TOML of its
  own, dispatched by name exactly like `orch-reviewer`.

No `commands/` directory and no prompt files ship with this adapter:
Codex custom prompts (slash commands) are deprecated, so skills are
invoked directly instead of through a command surface (a recorded
decision, not an oversight).

## Install order

1. Install the `orch` binary on `PATH` **first**. Every hook in this
   plugin shells out to it with a bare command; if it is not resolvable,
   both hooks fail (see Known limitations).
2. Install this plugin (`.codex-plugin/plugin.json`, its bundled
   `hooks/` and `skills/`).
3. **Approve the plugin-bundled hooks' one-time trust prompt.** Codex
   CLI requires the user to review and trust plugin-bundled hooks before
   they run at all. Until that approval happens, `orch guard codex` and
   `orch hook codex session-start` silently do not run — the same
   fail-open class as a missing binary, not a denial or an error.
4. Copy the five agent TOMLs under `agents/` into the project's
   `.codex/agents/` (or `~/.codex/agents/` for a user-global install).
   Codex plugins cannot bundle agent definitions, so this copy is a
   separate manual step every install of this adapter needs, not
   something the plugin installs for you.
5. Run `orch doctor` to confirm the environment (Git, GitHub, memhub,
   configuration) is healthy.

## Known limitations

These are accepted for this PR and the adapter as a whole, not open
bugs:

- **Bash-mediated writes are not guarded.** The `PreToolUse` hook only
  covers `apply_patch`. A file write made through `Bash` (for example
  `echo > file` or a script) does not go through `orch guard codex`.
  Codex CLI's own approval and sandbox prompts on `Bash` are the
  backstop here. This limitation is shared with the Claude adapter —
  but on Codex, `Bash` itself is a hookable tool_name, which makes the
  shared task-27 core feature (closing this gap for real) more
  tractable on this host than on Claude Code, where no comparable hook
  point exists for shell-mediated writes today.
- **A missing `orch` binary fails open, not closed.** As described
  under Install order: a hook command that cannot be found exits
  non-blocking by the hook protocol's own rules, so both the write
  guard and the session-start context silently stop working rather than
  erroring loudly. The same is true while the one-time hook trust
  approval is outstanding. Install order, `orch doctor`, and the visible
  absence of session-start context are the mitigations; there is no way
  to make either gap fail closed from inside the hook itself.
- **No per-spawn model override.** Unlike Claude Code (which can at
  least prompt-cue an effort it cannot enforce), Codex CLI dispatches an
  agent with whatever `model`/`model_reasoning_effort` its installed
  TOML pins — there is no host mechanism to override either per
  dispatch. `orch-delivery`'s spawn step therefore requires the routed
  `(model, effort)` to match an installed `orch-*` TOML exactly, and
  stops and tells the human rather than silently substituting a
  mismatched agent or reporting the routed selection as if it ran.
  `orch-reviewer-safe` exists specifically so the §10 safe-downgrade row
  has a real installed TOML to dispatch, instead of dead-ending at that
  same stop-and-tell-human rule on every routine downgrade.
- **A configured non-default model requires hand-editing the installed
  TOMLs.** There is no `orch` verb today that renders an agent TOML from
  `config.toml`'s routing configuration; a repository that overrides the
  §10 defaults has to hand-edit the five files under `.codex/agents/` to
  match. A future verb that renders TOMLs from config is a plausible
  follow-up, out of scope for this adapter.
- **An unrecognized future `apply_patch` envelope directive denies,
  fail-closed.** `internal/guard`'s envelope parser treats any `*** `
  directive line it does not already recognize as a malformed envelope
  and denies the write (see `ErrPatchEnvelope`). This is deliberate — an
  upstream Codex CLI format addition must never silently allow an
  unparsed write — but it also means a Codex CLI upgrade that adds a new
  `apply_patch` directive kind can cause spurious denies until `orch`
  itself is updated to parse it.

## Host version

Formal minimum: the first hooks-GA Codex CLI release (2026-05-14).
Rationale: `apply_patch` `PreToolUse` hook events only fire correctly on
builds after openai/codex PR #18391 (2026-04-22, the fix that made
`apply_patch` write events actually reach hooks); a build older than
that silently never sees file-write events at all — no error, no
denial, just a guard hook that never runs. That failure mode has no
visible symptom short of a write landing that should have been denied,
which is why the floor is pinned at the first release confirmed to
include the fix rather than left to a de-facto smoke-tested version, as
the Claude adapter's is.

As defense in depth — complementary to the guard hook, not a
substitute for it, and no configuration is shipped by this adapter to
enforce it — a repository operator adopting this adapter should also
consider running Codex CLI with `sandbox_mode` set to `workspace-write`
and a non-`never` `approval_policy`. The guard hook denies by orch's own
Delivery/Assist policy; Codex CLI's own sandbox and approval settings
are a second, independent layer that catches what the hook cannot (a
missing binary, an unapproved plugin, a write the hook fails to
classify).
