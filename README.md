# Orch

Orch is a cross-host development orchestrator for the Codex CLI and
Claude Code CLI. It keeps a frontier model in the Architect role
(planning, architecture, routing, final review) and delegates
exploration, implementation, specialist work, and review to explicitly
configured models at appropriate reasoning-effort levels — without
weakening delivery discipline. Every tracked-file mutation flows
through an issue, an isolated feature worktree, a PR, independent
review, verification/CI, and a human-approved merge.

The full product definition lives in [ORCH-PRD.md](ORCH-PRD.md).

**Status: pre-alpha.** The repo contains the project skeleton plus one
thin vertical slice: strict parsing/validation of the committed
configuration (`.orchestrator/config.toml`) and stub `orch status` /
`orch doctor` commands. No orchestration behavior exists yet; the
remaining commands fail closed with explicit not-implemented errors.

## Layout

| Path | Purpose |
|---|---|
| `cmd/orch/` | CLI entry point |
| `internal/config/` | Committed-configuration schema, loading, fail-closed validation |
| `internal/cli/` | Command dispatch, `status`, `doctor` |
| `adapters/claude/`, `adapters/codex/` | Future host-adapter artifacts (skills, hooks, templates) |
| `ORCH-PRD.md` | Product requirements — source of truth for v1 |

## Build / test / run

Requires Go 1.26+.

```sh
go build ./...            # build everything
go test ./...             # run the test suite
go vet ./...              # static checks
gofmt -l .                # list unformatted files (CI fails on any)
go run ./cmd/orch status  # or: doctor, help
```

## License

MIT — see [LICENSE](LICENSE).
