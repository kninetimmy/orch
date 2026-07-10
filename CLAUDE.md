# Orch

A cross-host development orchestrator for the Codex CLI and Claude Code CLI.

## Session Continuity

memhub is the source of truth at `.memhub/project.sqlite`. The rendered files
under `.memhub/rendered/` are the local human-readable view — generated from
the DB and gitignored by default. Re-render after `/wrap-up` with
`memhub render`. Read PROJECT.md at session start before acting.

## Build / test / run

None yet — the shared-core language/runtime is undecided (PRD §24).
See Architecture in `.memhub/rendered/PROJECT.md`.
