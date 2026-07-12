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

**Status: pre-alpha — the shared Go core is complete; host adapters
are next.** The core implements the full Delivery run engine: the plan
gate, activation (issues, labels, branches, isolated worktrees), the
complete per-issue lifecycle (dispatch → PR → independent review →
escalation → CI → human-gated merge → cleanup → auto-return to
Assist), crash recovery via `orch resume`, mechanical write
enforcement via `orch guard`, and the init/configure interview flows.
Every human command works except `orch metrics` (still a fail-closed
stub):

```text
init             Interview and bootstrap this repository
status           Show mode and configuration summary
doctor           Check environment and configuration health
configure        Interview and deliver committed configuration changes
configure-local  Interview and apply machine-local overrides
resume           Reconcile an interrupted Delivery run against GitHub and continue
abort            Stop dispatch and return to Assist
```

`orch run` and `orch guard` are adapter plumbing (JSON stdin/stdout),
not human commands. The host adapters under `adapters/` are not
implemented yet, so there is no end-to-end orchestration from a host
CLI session yet — that is the current phase of work.

## Layout

| Path | Purpose |
|---|---|
| `cmd/orch/` | CLI entry point |
| `internal/cli/` | Command dispatch: the human commands plus the `run`/`guard` plumbing verbs |
| `internal/config/` | Committed-config schema, fail-closed validation, the `config.local.toml` overlay, and the canonical TOML writer |
| `internal/state/` | Assist/Delivery mode and per-issue run state (schema-versioned JSON, atomic writes, fail-closed loads) |
| `internal/lockfile/` | Exclusive cross-host Delivery lock; recovery is always explicit `orch abort` |
| `internal/paths/` | Safe-path primitives: canonical paths, containment checks, repo-root discovery |
| `internal/execx/` | Injectable external-command runner shared by the git/gh callers (+ scripted test fake) |
| `internal/gitops/` | Delivery git mechanics: branches, worktrees, push, fast-forward — policy-free |
| `internal/ghops/` | GitHub mechanics via the `gh` CLI: label taxonomy, issues, PRs, gated merge, CI state |
| `internal/manifest/` | The issue/PR audit record — lossless render/parse over a managed body region |
| `internal/routing/` | Pure role routing and the escalation ladder (PRD §9–§11) |
| `internal/guard/` | Mechanical pre-write enforcement behind host PreToolUse hooks |
| `internal/run/` | The Delivery run engine: plan gate, activation, per-issue lifecycle, resume |
| `internal/instructions/` | Managed instruction-block engine for AGENTS.md/CLAUDE.md (PRD §19) |
| `internal/question/` | Host-neutral native question contract (documents in, answer sets back) |
| `internal/interview/` | Pure question engines for `init`, `configure`, and `configure-local` |
| `internal/bootstrap/` | Mechanical PR-flow executors behind `init --bootstrap` and `configure --deliver` |
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
