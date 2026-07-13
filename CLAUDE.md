# Orch

A cross-host development orchestrator for the Codex CLI and Claude Code CLI.

## Session Continuity

memhub is the source of truth at `.memhub/project.sqlite`. The rendered files
under `.memhub/rendered/` are the local human-readable view — generated from
the DB and gitignored by default. Re-render after `/wrap-up` with
`memhub render`. Read PROJECT.md at session start before acting.

## Build / test / run

Shared core is Go (module `github.com/kninetimmy/orch`, Go 1.26+).

- Build:  `go build ./...`
- Test:   `go test ./...`
- Vet:    `go vet ./...`
- Format: `gofmt -w .` (CI fails on unformatted files)
- Run:    `go run ./cmd/orch status` (or `doctor`, `help`)

Host adapters under `adapters/` are non-Go artifacts (not implemented yet).

<!-- orchestrator:managed:start version=1 -->
This file is partially managed by Orch (see `.orchestrator/config.toml`).
- In **Assist** mode, tracked-file changes are mechanically denied; a mutating
  request triggers read-only planning instead.
- In **Delivery** mode, work happens in an isolated per-issue worktree, never in
  this checkout directly.
- Model/effort routing, concurrency, and host plugin setup live in
  `.orchestrator/config.toml` — edit that file, not this block.
- Orch upgrades this block through Delivery. Do not hand-edit it; a hand edit
  blocks the next install/upgrade until reverted or removed.
<!-- orchestrator:managed:end -->