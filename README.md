# Orch

Orch is a development orchestrator for AI coding agents — currently
the Claude Code CLI and the Codex CLI. You keep working in your agent
of choice the way you already do; Orch sits underneath it and does two
things:

1. **Spends your tokens where they matter.** A strong "frontier" model
   stays in charge of planning, architecture, and final review, while
   routine work — exploring the codebase, writing the code, reviewing
   diffs — is delegated to cheaper, faster models at tuned
   reasoning-effort levels. You choose exactly which model does which
   job; Orch enforces that choice mechanically instead of hoping the
   agent remembers.

2. **Keeps changes disciplined.** No matter which model wrote the
   code, every change to tracked files goes through the same pipeline:
   a GitHub issue, an isolated git worktree, a pull request, an
   independent review by a different model, CI, and finally a merge
   that only a human can approve. An agent can never quietly edit your
   working tree — writes outside the pipeline are blocked at the tool
   level, not by prompt instructions.

The full product definition lives in [ORCH-PRD.md](ORCH-PRD.md).

**Status: pre-alpha.** The shared core (a single Go binary) is
complete and tested; the host adapters that plug it into Claude Code
and Codex are the current phase of work. Until an adapter lands you
can install the binary, initialize a repository, and manage
configuration — but the end-to-end orchestrated workflow is not yet
drivable from an agent session.

## How it works, in short

Orch has two operating modes:

- **Assist** (the default): the agent can read anything and answer
  questions, but writes to tracked files are blocked. This is the safe
  everyday mode — investigation, explanation, planning.
- **Delivery**: entered only for an approved, task-scoped plan. Each
  task in the plan becomes a GitHub issue and an isolated worktree;
  an implementer model does the work there; a reviewer model reviews
  it; CI runs; you approve the merge on GitHub. When every task is
  merged or abandoned, Orch returns to Assist automatically.

Work inside Delivery is divided among five **roles**, each pinned to a
model and effort level you configure:

| Role | What it does |
|---|---|
| Architect | Plans, routes work, makes final calls — your strongest model |
| Scout | Read-only exploration and fact-finding — cheap and fast |
| Implementer | Writes the code in the isolated worktree |
| Specialist | Takes over risky or hard tasks (security, concurrency, migrations…) |
| Reviewer | Independently reviews every PR — never the model that wrote it |

Routing is deterministic: risky domains or hard tasks force the
Specialist and a strong Reviewer, and when a model fails at a task it
is retired for that task permanently and the next candidate steps up —
escalating back to the Architect if everything else is exhausted.

## Install

Download the static binary for your OS and architecture from
[GitHub Releases](https://github.com/kninetimmy/orch/releases)
(`orch_<os>_<arch>`, Windows binaries end in `.exe`), then verify its
SHA-256 against the `SHA256SUMS` file published with the release
**before running it** — don't skip this:

```sh
# Linux / macOS
sha256sum --check --ignore-missing SHA256SUMS

# Windows (PowerShell): compare against the matching SHA256SUMS line
(Get-FileHash orch_windows_amd64.exe -Algorithm SHA256).Hash
```

Rename it to `orch` (or `orch.exe`) and place it on your PATH.
`orch status` and `orch doctor` print the binary's release version on
their first line; adapters and docs pin a release version.

Alternatively, build from source with Go 1.26+ (source builds report
version `dev`):

```sh
git clone https://github.com/kninetimmy/orch.git
cd orch
go build ./cmd/orch        # produces ./orch (orch.exe on Windows)
```

or install straight onto your PATH:

```sh
go install github.com/kninetimmy/orch/cmd/orch@latest
```

Delivery mode additionally needs `git` and the
[GitHub CLI](https://cli.github.com/) (`gh`) authenticated against the
repository's remote. Assist mode works without a remote.

## Using it

Run `orch init` in the repository you want to orchestrate. It detects
your environment (hosts, git, gh, memhub), interviews you about which
hosts to enable and which models to use — sensible defaults are
offered for every question — and then bootstraps everything through
its own pipeline: the configuration lands as a pull request you review
and merge, not as a silent write to your working tree.

Day-to-day commands:

```text
orch status           Show mode and configuration summary
orch doctor           Check environment and configuration health
orch configure        Change committed settings (delivered as a PR)
orch configure-local  Change machine-local overrides (applied directly)
orch resume           Reconcile an interrupted Delivery run and continue
orch abort            Stop a Delivery run and return to Assist
orch metrics          Local usage metrics (not implemented yet)
```

`orch run` and `orch guard` also exist but are plumbing — the host
adapters drive them; you never call them by hand.

## Settings — what you can tune

Configuration lives in two TOML files under `.orchestrator/`:

- **`config.toml`** — committed to git, shared by everyone on the
  repo. Changed via `orch configure`, which delivers the edit as a
  reviewable PR.
- **`config.local.toml`** — machine-local and gitignored, for personal
  preferences. Changed via `orch configure-local`, applied directly.

Policy decisions can only live in the committed file — a local
override can never weaken the shared workflow rules. What goes where:

| Setting | Values (default) | Local override? |
|---|---|---|
| `hosts.claude` / `hosts.codex` | enable a host by giving it a role table | no (committed) |
| `hosts.<host>.roles.<role>.model` | exact model version string | yes |
| `hosts.<host>.roles.<role>.effort` | `low` `medium` `high` (+ `xhigh` on claude) | yes |
| `concurrency.max_subagents` | integer ≥ 1 (`3`) | yes |
| `metrics.enabled` | `true` / `false` (`false`) | yes |
| `merge.strategy` | `squash` `rebase` `merge-commit` (`squash`) | no (committed) |
| `memhub.mode` | `required` `best-effort` `off` (no default — you choose) | no (committed) |

The six roles per enabled host are `architect`, `scout`,
`implementer`, `specialist`, `reviewer`, and `review_downgrade` (the
cheaper reviewer Orch is allowed to use only when a task is provably
low-risk). Every role pins an exact model version — never an alias —
so what ran is always auditable. The defaults `orch init` offers:

| Role | Claude Code | Codex |
|---|---|---|
| Architect | `claude-opus-4-8` / xhigh | `gpt-5.6-sol` / high |
| Scout | `claude-sonnet-5` / low | `gpt-5.6-terra` / low |
| Implementer | `claude-sonnet-5` / xhigh | `gpt-5.6-terra` / high |
| Specialist | `claude-opus-4-8` / high | `gpt-5.6-sol` / medium |
| Reviewer | `claude-opus-4-8` / high | `gpt-5.6-sol` / medium |
| Review downgrade | `claude-sonnet-5` / high | `gpt-5.6-terra` / high |

Typical tuning: point a role at a bigger/smaller model on one machine
via `configure-local` (e.g. run the Architect on a frontier model only
where you have the subscription), raise `max_subagents` on a beefy
machine, or enable local metrics while experimenting. Every applied
override is listed by `orch status` and `orch doctor`, so the exact
models in effect are always visible.

---

## Under the hood

This section is for contributors and the curious; nothing here is
needed to use the tool.

### Enforcement, not convention

The mode rules are enforced mechanically. Host adapters wire the agent
CLI's pre-write hooks to `orch guard`, which resolves a closed
decision table: in Assist, any write to a tracked file inside the repo
is denied (git-ignored paths are allowed); in Delivery, writes are
allowed only inside a worktree registered to the active run, in a
writable phase, with the worktree's HEAD on the registered branch.
`.orchestrator/` state and anything under `.git` are never writable.
The guard fails closed: if it cannot determine the facts, it denies.

Delivery is exclusive across hosts and machines: a lock file
(`.orchestrator/delivery.lock`, created `O_CREATE|O_EXCL`) is the
lock, and there is no automatic staleness takeover — recovery is
always an explicit `orch abort` or `orch resume`. Run state is
schema-versioned JSON at `.orchestrator/state.json` (machine-local,
atomic writes, fail-closed loads), persisted after every sub-step so a
crash at any point is recoverable.

### The Delivery pipeline

A run starts at the **plan gate**: a schema-versioned plan document
(issues, dependency waves, risk facts) is validated fail-closed, and a
gate document derives each issue's executor and reviewer from facts
alone via the routing table — the model never picks its own reviewer.
Activation then creates the GitHub label taxonomy, one issue per task
carrying a structured **audit record** (rendered markdown plus
canonical JSON in a managed body region — exact model, effort, and
routing rationale, mirrored onto the PR), and one branch + isolated
worktree per issue under `.orchestrator/worktrees/`.

Each issue then walks a closed lifecycle driven by plumbing verbs:
`dispatch` (dependencies must be merged; branch fast-forwarded onto
the default branch) → `pr-open` (clean, strictly-ahead, orphan-PR
guarded) → `review` (routed reviewer verified against the live PR
head) → `ci` → `merge-report` (pins the approved head SHA) → `merge`
(human-approved, re-checked against the live PR, pinned with
`--match-head-commit`) → `cleanup` → `complete` (fast-forward the
primary checkout, auto-return to Assist). Failures route through
`escalate` (the routing ladder), `block` (closed failure classes; a
secret found stops the whole run), or `abandon`. Errors never mutate
state; state advances only on success.

`orch resume` reconciles an interrupted run against GitHub reality in
three strict stages — observe (all reads up front), classify (a pure
30-row decision table), apply (one state write, skipped when already
converged). It never fabricates approval, never advances past the
human merge gate, and never deletes or recreates anything.

### Routing and escalation

`internal/routing` is pure and deterministic: a first-match-wins table
over a closed nine-domain risk enum plus difficulty/consensus facts.
Reviewer downgrade requires four affirmative facts; any conflict
silently takes the stronger route and says so in the rationale. On
failure, escalation retires the failed model permanently for that
issue (an effort bump is still a retry; a model swap is not),
restores the strong reviewer on any reroute, and resolves exhaustion
to an explicit return-to-Architect — the code never ranks model
strength on its own.

### Package layout

| Path | Purpose |
|---|---|
| `cmd/orch/` | CLI entry point |
| `internal/cli/` | Command dispatch: human commands plus the `run`/`guard` plumbing verbs |
| `internal/config/` | Committed-config schema, fail-closed validation, local-override overlay, canonical TOML writer |
| `internal/state/` | Assist/Delivery mode and per-issue run state (schema-versioned JSON, atomic writes) |
| `internal/lockfile/` | Exclusive cross-host Delivery lock |
| `internal/paths/` | Safe-path primitives: canonical paths, containment, repo-root discovery |
| `internal/execx/` | Injectable external-command runner shared by the git/gh callers (+ scripted test fake) |
| `internal/gitops/` | Delivery git mechanics: branches, worktrees, push, fast-forward — policy-free |
| `internal/ghops/` | GitHub mechanics via the `gh` CLI: labels, issues, PRs, gated merge, CI state |
| `internal/manifest/` | The issue/PR audit record — lossless render/parse over a managed body region |
| `internal/routing/` | Pure role routing and the escalation ladder |
| `internal/guard/` | Mechanical pre-write enforcement behind host PreToolUse hooks |
| `internal/run/` | The Delivery run engine: plan gate, activation, per-issue lifecycle, resume |
| `internal/instructions/` | Managed instruction-block engine for AGENTS.md/CLAUDE.md |
| `internal/question/` | Host-neutral native question contract (documents out, answer sets back) |
| `internal/interview/` | Pure question engines for `init`, `configure`, and `configure-local` |
| `internal/bootstrap/` | Mechanical PR-flow executors behind `init --bootstrap` and `configure --deliver` |
| `adapters/claude/`, `adapters/codex/` | Host-adapter artifacts (skills, hooks, templates) — in progress |
| `ORCH-PRD.md` | Product requirements — source of truth for v1 |

### Design principles

- **Fail closed.** Unknown config keys, schema drift, unreadable
  locks, indeterminate checks — everything unprovable is denied with a
  named remediation.
- **Mechanics are policy-free.** `gitops`/`ghops`/`manifest` know how;
  `internal/run` alone decides when and why.
- **Humans gate merges.** Orch pins the approved head SHA and refuses
  if the PR moved after approval; the merge itself happens on GitHub.
- **Everything auditable.** Exact model versions and routing
  rationale live in the issue/PR audit record and in `orch status`.

### Build / test

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
