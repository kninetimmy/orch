# Claude Code adapter

## What this is

This directory is the Claude Code host adapter for Orch: a Claude Code
plugin that connects a Claude Code session to the shared `orch` binary.
It is deliberately thin (PRD §6) — "the detailed state machine must not
be independently reimplemented in Markdown skills or platform-specific
shell scripts." Every policy decision (what is writable, what routing
applies, what a Delivery run's state is) is made by the `orch` binary.
This adapter only translates Claude Code host events into calls against
that binary, presents native dialogs, and spawns role subagents. It
never re-derives a decision the engine already made.

## Artifact map

- `.claude-plugin/plugin.json` — the plugin manifest (name, description,
  version, author). Component directories (`commands/`, `agents/`,
  `skills/`, `hooks/`) are auto-discovered by Claude Code; there is
  nothing else to declare here yet.
- `hooks/hooks.json` — two hooks:
  - `PreToolUse` on `Write|Edit|MultiEdit|NotebookEdit` runs
    `orch guard claude`, which reads the tool-call event on stdin and
    denies the write when Orch's policy (Assist read-only, Delivery
    worktree containment) says so. An allow is silence: guard
    never bypasses Claude Code's own permission prompts.
  - `SessionStart` on `startup|resume|clear|compact` runs
    `orch hook claude session-start`, which injects a short context block
    at session start when the current directory is inside an Orch
    repository with a readable configuration and state: the operating
    mode, the memhub mode, a reminder to load the `orch-architect` skill
    before planning or making a change, and the three setup interviews
    (`/orch:init`, `/orch:configure`, `/orch:configure-local`). Outside an
    Orch repository, or if the repository is unreadable, it injects
    nothing and never blocks the session.
- `skills/orch-architect/SKILL.md` — the Architect's standing posture:
  never edit tracked files directly, never re-derive engine policy,
  the `orch run status --json` / PROJECT.md / memhub-recall session
  ritual, mode conduct, the four-subagent whitelist and the
  `concurrency.max_subagents` cap, and memhub write discipline.
- `skills/orch-delivery/SKILL.md` — the Delivery wire contract: the
  scratch-file JSON request/response pattern for every `orch run <verb>`
  call, `PlanDoc` construction, the plan gate and its four-option
  `AskUserQuestion`, activation, the full per-issue dispatch → execute →
  pr-open → review → ci → merge-report → merge gate → merge → cleanup
  loop, completion and the memhub wrap-up cue, escalation, and block/
  abandon.
- `skills/orch-setup/SKILL.md` — the shared step-loop driver for the
  three `orch <cmd> --step` interviews: the resubmitted-in-full
  `AnswerSet`, batched `AskUserQuestion` presentation of `questions`
  documents, the `summary`/blockers/`complete`/`aborted` document kinds,
  and each interview's terminal form.
- `commands/init.md`, `commands/configure.md`,
  `commands/configure-local.md` — the `/orch:init`, `/orch:configure`,
  and `/orch:configure-local` slash commands. Each runs its command's
  bare human report first, then follows `orch-setup` for the interactive
  interview and terminal form; none duplicates `orch-setup`'s protocol.
- `agents/orch-scout.md`, `agents/orch-implementer.md`,
  `agents/orch-specialist.md`, `agents/orch-reviewer.md` — the four
  role subagents the Architect spawns during Delivery, each with its own
  model and tool whitelist (scout and reviewer carry no write tool; no
  agent carries `Task` or any `mcp__` tool, so subagents have no memhub
  write surface of their own).

## Install order

Install the `orch` binary on `PATH` **before** installing this plugin.
PRD §18 states this ordering directly: "Global host plugins are
installed before repository initialization," and the binary is what
every hook in this plugin shells out to.

This ordering matters mechanically, not just procedurally. Both hooks
above are bare commands (`orch guard claude`, `orch hook claude
session-start`) with no shell interposed. If `orch` is not resolvable on
`PATH`, invoking either command fails with a shell "command not found"
exit. Claude Code's PreToolUse hook protocol treats a hook's non-zero,
non-JSON exit as **non-blocking** — i.e. fail-open. A missing binary does
not produce a denial or an error dialog; it silently stops enforcing and
stops injecting session context. Installing the binary first, and
running `orch doctor` to confirm the environment is healthy, closes that
gap before it can matter. `orch doctor` is the health check for the
locally installed environment (Git, GitHub, memhub, and configuration);
run it after installing the binary and again after `orch init`.

## Known limitations

These are accepted for this PR and the adapter as a whole, not open
bugs:

- **Bash-mediated writes are not guarded.** The `PreToolUse` hook only
  covers the four file-editing tools (`Write`, `Edit`, `MultiEdit`,
  `NotebookEdit`). A file write made through `Bash` (for example `echo >
  file` or a script) does not go through `orch guard claude`. Claude
  Code's own permission prompts on `Bash` are the backstop here. This
  limitation is shared with the future Codex adapter and is a candidate
  for a core feature later, not something this adapter alone can close.
- **A missing `orch` binary fails open, not closed.** As described under
  Install order: a hook command that cannot be found exits non-blocking
  by the hook protocol's own rules, so both the write guard and the
  session-start context silently stop working rather than erroring
  loudly. Install order, `orch doctor`, and the visible absence of
  session-start context are the mitigations; there is no way to make a
  missing binary fail closed from inside the hook itself.
- **Reasoning effort has no per-subagent parameter on this host.** Claude
  Code subagent spawns do not accept an effort knob, so the routed effort
  (low/high/xhigh) is approximated by a prompt cue in the subagent's
  instructions rather than an actual host parameter. The exact routed
  effort is still recorded in the engine's audit trail; only the
  in-session behavior is an approximation. Parity tests (task 21) assert
  the prompt cue is present, not that it changed model behavior.
- **`orch guard`'s `--role` narrowing is unused by this adapter.** Hooks
  in Claude Code are plugin-global, not scoped per subagent, so the
  adapter never passes `--role` when invoking guard. Read-only role
  enforcement (scout and reviewer must never write) instead comes from
  each subagent's own tool whitelist in its agent definition (PR 2), not
  from guard's role-narrowing flag.

## Host version

Formal minimum: Claude Code >= 2.1.207 (the version the task-18
end-to-end smoke validated: plugin component auto-discovery for
`commands/`, `agents/`, `skills/`, and `hooks/hooks.json`, `SessionStart`
hooks, the `AskUserQuestion` tool, and the `PreToolUse`
`permissionDecision` protocol), formalized at task 21 alongside the
Codex CLI floor.

The Codex CLI adapter lives at `adapters/codex/` and passes the same
shared parity suite (`internal/adaptertest`) this adapter's plugin tests
do.
