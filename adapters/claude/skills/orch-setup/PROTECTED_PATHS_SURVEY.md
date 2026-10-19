# Protected-paths survey

Survey methodology version: 1

Read this when `orch-setup` reaches the `protected.paths` question. The
version above is the one this `orch` binary approves lists under; a repository
whose committed `[protected]` section records another version has no current
list, and autonomy grants refuse there until it is re-surveyed and approved.

## What the list is

Repository-relative paths a grant can never merge a change to, each with a
one-line reason. A directory entry covers everything under it. The list sits
on top of a built-in floor every repository already gets. The floor is a fixed
set, not a category, so it covers only these exact entries and nothing similar
to them.

File names, matched against a path's file name at any depth in the repository,
ignoring case (a `*` matches any run of characters):

- `CLAUDE.md`, `AGENTS.md`, `.gitignore`, `.gitattributes`
- `go.mod`, `go.sum`
- `Cargo.toml`, `Cargo.lock`, `build.rs`
- `package.json`, `package-lock.json`, `npm-shrinkwrap.json`, `pnpm-lock.yaml`,
  `yarn.lock`
- `*.csproj`, `*.fsproj`, `*.vbproj`, `Directory.Packages.props`,
  `Directory.Build.props`, `packages.lock.json`, `NuGet.config`
- `pyproject.toml`, `setup.py`, `setup.cfg`, `Pipfile`, `Pipfile.lock`,
  `poetry.lock`, `uv.lock`, `requirements*.txt`
- `install.sh`, `install.ps1`, `Makefile`, `justfile`, `.goreleaser.yml`,
  `.goreleaser.yaml`
- `.mcp.json`, `opencode.json`, `opencode.jsonc`
- `go.work`, `go.work.sum`, `Gemfile`, `Gemfile.lock`, `pom.xml`,
  `build.gradle`, `build.gradle.kts`, `settings.gradle`, `settings.gradle.kts`,
  `gradlew`, `gradlew.bat`, `gradle-wrapper.properties`, `gradle-wrapper.jar`
- `composer.json`, `composer.lock`, `bun.lock`, `bun.lockb`,
  `Directory.Build.targets`, `global.json`, `Package.swift`,
  `Package.resolved`, `mix.exs`, `mix.lock`, `vcpkg.json`
- `.npmrc`, `.yarnrc.yml`

Host agent harness directories, matched against every segment of a path at any
depth, ignoring case, each covering itself and everything under it:
`.claude`, `.codex` and `.opencode`. They set agent permissions, tools, hooks,
MCP servers, agent definitions and live-loaded skills in every later worktree.
Only that exact directory name is covered: `.claude-plugin` is not, and
neither is a `skills` or `agents` directory outside one of them.

Root-only paths, each covering itself and everything under it, ignoring case:
`.github/workflows`, `.github/actions`, `LICENSE` (that exact name only;
`LICENSE.md` and `LICENSE.txt` are not covered), and `.orchestrator`.

Only an exact match is covered. Other manifests (`build.sbt`, `Podfile`,
`CMakeLists.txt`), other registry settings (`pip.conf`), other scripts
(`scripts/release.sh`, `release.ps1`), other CI systems (`.gitlab-ci.yml`) and
other license file names are not on the floor.

Before #412 the floor did not cover the harness directories, `.mcp.json`,
`opencode.json`, `opencode.jsonc`, `.github/actions`, or the manifests,
wrappers and registry settings from `go.work` to `.yarnrc.yml` above, and this
section named `Gemfile`, `pom.xml`, `build.gradle` and `go.work` as not on the
floor. Now the floor covers all of them.

Before v0.15.1 this survey ran on a different rule: when in doubt, propose the
path, because a redundant entry is harmless and a missing one is not. It also
told the surveyor to propose every release script and CI configuration the
floor misses, and to keep every scout entry that was not an exact floor match.
In its first run, on Orch's own repository, that rule proposed 62 entries
covering most of the engine, enough to send nearly every grant merge back to
the user. Now a path is proposed only when it meets the test in "What the list
is for", and the other candidates are reported as near misses.

## What the list is for

A grant exists so work merges without the human. A protected path does not
stop a grant's run: a grant merge that touches one waits for the user, and the
run carries on with other issues. So every entry has a cost, because each PR
that touches it goes back to the human, and a list that covers everything
important leaves a grant nothing to merge. Aim for the smallest list that
guards the boundary, not a map of everything that matters.

Every grant merge has already passed the repository's required CI and an
independent review, and a merge can be reverted. Those catch and undo ordinary
bugs; the list is for what they cannot. Propose a path only when a bad change
there, after passing CI and review, would:

- **(a) widen what an unattended agent may do**: change what a grant or gate
  permits, or weaken a check a merge relies on, such as approval binding,
  required-CI status, review verdicts or protected-path matching;
- **(b) silently disable enforcement**: loosen a containment, permission or
  sandbox check, or the hook or configuration that switches one on, in a way
  the repository's tests would not show; or
- **(c) do damage a revert cannot undo**: delete, overwrite or corrupt data,
  leak a secret, or reach users before another human checkpoint.

A change that would only break something (a wrong result, a failing feature, a
broken format that a revert restores) does not qualify, however central the
code.

For (c), find the human checkpoint between a merge and the code's users. When
merged code reaches users only through a human step (a release tag, a version
bump, a manual deploy), protect that step's inputs (version stamps, manifests,
release and deploy configuration) rather than every path behind it. When a
merge deploys or ships on its own, what it ships is in scope.

Prefer a file to its directory when only part of the directory meets the test.
When the smallest path that covers a concern is a large file that most changes
touch, say so and give how many of the last 100 commits touched it, so the user
can weigh what protecting it costs. A path that meets the test belongs on the
list even when it looks redundant.

## Where to look

Survey the repository against each of these eight categories. They are where
qualifying paths usually sit, not a list of things to protect: apply the test
above to every candidate. A repository may have nothing in a category; say so
rather than inventing an entry.

1. **Trust and security boundaries.** Authentication, authorization,
   permission checks, input validation at the edge, sandboxing, path
   containment, anything whose failure lets an untrusted party in.
2. **Persistence and schema.** Database schemas, migrations, on-disk formats,
   state files and their readers and writers, anything where a wrong change
   corrupts or strands existing data.
3. **Cryptography and secrets handling.** Encryption, signing, hashing for
   integrity or passwords, key and token handling, secret loading, checksum
   verification.
4. **Locking and concurrency primitives.** Locks, leases, queues, atomic
   writes, ordering guarantees, anything whose bug is a race that tests rarely
   catch.
5. **The repository's own policy or enforcement code.** Code that decides what
   is allowed: gates, guards, approval and merge logic, lint or CI rules that
   keep other code honest, and the configuration of those rules.
6. **Release and install tooling.** Packaging, release automation, installers,
   update and upgrade paths, version stamping, anything shipped to users
   outside the normal build. The floor covers only the install, build and
   release file names listed above; the rest of this category is usually where
   the human checkpoint in (c) lives.
7. **Public API and wire contracts.** Exported interfaces, CLI surfaces,
   protocol and file formats other programs depend on, schema versions, and the
   tests that pin them. These usually fail the test, because a broken contract
   is a bug a revert fixes; propose one only when a change reaches its
   consumers before a human checkpoint.
8. **Destructive data paths.** Code that deletes, overwrites, truncates,
   migrates in place or force-pushes, and anything that cannot be undone.

## How to run it

Run the survey with `orch-scout` subagents, which are read-only, so the
Architect's own context stays small. Give each scout one or two categories, the
floor entries above, the test in "What the list is for", and these
instructions: find the files or directories in the category that meet the
test, and for each report

- `path`: repository-relative, forward slashes, the smallest path that covers
  the concern (a file, or a directory when the whole subtree is the concern);
  never `.`, never a glob, never a path outside the repository;
- `reason`: one line saying why a change there should never merge without a
  human;
- `test`: which of (a), (b) and (c) it meets, and how;
- `evidence`: the file or files you read that show it, with a line number or a
  short quote.

Also report the near misses: candidates that matter but would only break
something, each with a few words on why they fail the test.

Scouts may run concurrently up to the configured concurrency. Treat their
reports as claims: spot-check the evidence for any entry that looks surprising,
merge duplicates and overlaps (keep the directory when a file sits inside a
directory entry), and drop an entry that is an exact floor match or that meets
none of (a), (b) and (c); move the latter to the near misses.

## What to present

Show the user every proposed entry with its one-line reason, the test it meets
and its file evidence, grouped by category, and name the categories where the
survey found nothing. Then show the near misses, so the user can promote any
of them. Let the user add entries, drop entries, or defer the whole list before
anything is submitted. Submit the answer only after they have settled it:

- the approved entries, as a JSON array of `{"path", "reason"}` objects;
- `[]` when they approve having no entries beyond the floor;
- `defer` when they want to decide later.

The user approves the result again at the interview summary, and a human merges
the PR that commits it.
