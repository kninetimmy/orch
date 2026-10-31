# Protected-paths survey

Survey methodology version: 2

Read this when `orch-setup` reaches the `protected.paths` question. The
version above is the one this `orch` binary approves lists under; a repository
whose committed `[protected]` section records another version has no current
list, and autonomy grants refuse there until it is re-surveyed and approved.

Methodology 1 left admission of agent instruction text unsettled and kept its
version after the checkpoint rule changed. Lists approved under that text can
therefore omit qualifying paths. Methodology 2 settles those rules below and
requires a fresh survey and approval. Existing lists stay readable and are
never rewritten or re-approved merely because the binary's version changed.

## What the list is

Repository-relative paths a grant can never merge a change to, each with a
one-line reason. A directory entry covers everything under it. The list sits
on top of a built-in floor every repository already gets. The floor is a fixed
set, not a category, so it covers only these exact entries and nothing similar
to them.

File names, matched against a path's file name at any depth in the repository,
ignoring case (a `*` matches any run of characters):

- `CLAUDE.md`, `AGENTS.md`, `AGENTS.override.md`, `.gitignore`, `.gitattributes`
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

Host agent harness directory paths, matched at any depth, ignoring case, each
covering itself and everything under it: `.claude`, `.codex`, `.opencode` and
`.agents/skills`. They set agent permissions, tools, hooks, MCP servers, agent
definitions and live-loaded skills in every later worktree. Only those exact
directory paths are covered: `.claude-plugin` is not, nor is a `skills` or
`agents` directory outside one of them. `.agents` itself and its other
subdirectories, such as `.agents/hooks`, are not covered.

Before #418 the floor omitted `.agents/skills` and `AGENTS.override.md`, so a
grant could merge changes to these Codex instructions. Now the floor covers
both at any depth, without protecting the rest of `.agents`.

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

Judge a path by what a bad change to it could do, not by what the file holds
today. A configuration file that today only launches a server can, after one
change, carry permission rules or load skills or plugins, and that change
meets (a) or (b) even though the file's current content meets nothing.

Loaded agent operating instructions meet (a) when changing them can widen what
an unattended agent is permitted to do. This includes skills, command
instructions, source templates and program text assembled into those
instructions. Descriptive documentation alone does not qualify: identify the
instruction-loading path and the permission or approval a bad change could
widen. Methodology 1 did not settle this instruction-text case; methodology 2
admits qualifying text under the same test.

A boundary counts only when an unattended agent cannot route around it. A
restricted tool surface, such as an MCP server that only proposes changes, is
not a boundary for an agent that also has a shell and can run the same
program's command-line interface to do what the tool surface withholds. Judge
the paths behind such a surface as if the surface were not there.

This no-bypass rule concerns alternative executable mutation routes. It does
not erase the instruction boundary above by treating an agent's ability to
ignore an instruction as such a route. Before methodology 2 the rule did not
distinguish those readings; now a qualifying loaded instruction remains an
(a) entry.

For (c), find the human checkpoint between a merge and the code's users. A
human step counts as a checkpoint only when the human sees what ships at that
step, for example a release PR showing the changes or release notes identifying
them. A bare release tag, version bump or manual deploy does not show what
ships and is not a checkpoint by itself; it counts only when the step also
shows the shipped changes. A step that installs or ships whatever is on the
main branch without showing it, such as
an upgrade command run against main, is not a checkpoint. When merged code
reaches users only through a checkpoint, protect that checkpoint's inputs
(version stamps, manifests, release and deploy configuration) rather than
every path behind it. When a merge deploys or ships on its own, or reaches
users only through steps that are not checkpoints, what it ships is in scope.

The checkpoint rule applies to (c) only. A path that meets (a) or (b)
qualifies whatever human step follows the merge. These tests concern the
agent's permissions and enforcement; a later checkpoint does not remove that
boundary. Before methodology 2 this paragraph called every such change
silent, although (a) does not require silence; now it makes no such claim.

Through v0.15.1 this section named a human step (a release tag, a version
bump, a manual deploy) as the checkpoint without saying what the human had to
see, and did not say which tests the checkpoint rule applied to. Survey runs
on the same repository then split on whether a step that installs whatever is
on main counted, and some used that rule to drop (a) and (b) entries. Neither
reading holds now.

Prefer a file to its directory when only part of the directory meets the test.
When the smallest path that covers a concern is a large file that most changes
touch, say so. The Architect reports its change rate using the Git checks
below so the user can weigh what protecting it costs. A path that meets the
test belongs on the list even when it looks redundant.

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
   release file names listed above. Find the human checkpoint for (c), and
   judge the tooling itself under all three tests, including damage an
   installer does before that checkpoint. Previously this category said the
   remaining tooling was usually the checkpoint, which did not excuse it
   from those tests.
7. **Public API and wire contracts.** Exported interfaces, CLI surfaces,
   protocol and file formats other programs depend on, schema versions, and the
   tests that pin them. An ordinary broken contract is a bug a revert fixes;
   propose one only when it also meets (a), (b) or (c). Previously this category
   named only reaching consumers before a checkpoint; now permission-widening
   and enforcement changes are explicitly assessed here too.
8. **Destructive data paths.** Code that deletes, overwrites, truncates,
   migrates in place or force-pushes, and anything that cannot be undone.

## How to run it

The Architect owns the survey. Use read-only `orch-scout` subagents only when
the user's delegation policy already authorizes them; a setup question or
this document does not authorize delegation. Otherwise the Architect performs
the same survey across every category and destructive entry point, without
subagents. Previously the survey required scouts unconditionally; now that
choice follows the user's policy.

When delegation is authorized, give each scout one or two categories, the floor
entries above and the test in "What the list is for". The Architect or scouts
find the files or directories that meet the test and report for each:

- `path`: repository-relative, forward slashes, the smallest path that covers
  the concern (a file, or a directory when the whole subtree is the concern);
  never `.`, never a glob, never a path outside the repository;
- `reason`: one line saying why a change there should never merge without a
  human;
- `test`: which of (a), (b) and (c) it meets, and how;
- `evidence`: the file or files you read that show it, with a line number or a
  short quote.

Also report the near misses: candidates that matter but would only break
something, each with a few words on why they fail the test, and anything you
did not check.

The Architect also ensures that every command or entry point in the
repository that deletes, overwrites, truncates or migrates user data is listed
and ruled an entry or a near miss, whichever category it sits in. When
delegating, assign that complete inventory to one scout. A command that wipes
user data with no backup must not fall between two categories.

Authorized scouts may run concurrently up to the configured concurrency. Treat
their reports as claims: spot-check the evidence for any entry that looks
surprising, merge duplicates and overlaps (keep the directory when a file sits
inside a directory entry), and drop every entry already covered by the floor, including
a file inside a floored directory. Drop entries meeting none of (a), (b) and
(c) to the near misses. Previously only an exact floor match was dropped; now
coverage by a floor directory also removes a redundant entry. Resolve every
not-checked item yourself or through an already-authorized scout; name any
item you cannot resolve to the user as unresolved.

### Architect Git checks

The Architect runs the Git checks after combining the reports. Previously
scouts were asked to run tracking checks even when their tools could not, and
the report offered only tracked or ignored. Now the Architect supplies
`tracked` from these commands:

- `git ls-files -- <path>`: any listed file means the path is tracked;
- otherwise `git check-ignore -v -- <path>`: a matching rule means ignored;
- if neither command identifies it, report untracked and not ignored.

The Architect also computes both per-path and combined commit costs. First run
`git rev-list --first-parent --count HEAD`. `HEAD~100` requires at least 101
first-parent commits, not the 100 commits the old instructions named.

- With at least 101 first-parent commits, use
  `git rev-list --count HEAD~100..HEAD -- <path>` for an entry, and
  `git rev-list --count HEAD~100..HEAD -- <path> <path> ...` for all proposed
  paths together. The denominator is `git rev-list --count HEAD~100..HEAD`.
- With fewer, explicitly report the whole-history fallback: use
  `git rev-list --count HEAD -- <path>` for an entry and
  `git rev-list --count HEAD -- <path> <path> ...` for the combined list. The
  denominator is `git rev-list --count HEAD`.

On a history with merges, the selected range can contain more than 100
commits. Report the actual denominator for every per-path and combined cost,
not an assumed 100. Do not use `git log -100 -- <path>`: it counts the path's
own last 100 commits, however far back they reach. If no paths are proposed,
report a combined numerator of 0 rather than running a path count without path
arguments. If a Git check cannot complete, report that evidence as unresolved,
never as an estimated zero.

## What to present

Show the user every proposed entry with its one-line reason, the test it meets,
its file evidence and tracking status, grouped by category, and name the
categories where the survey found nothing. Then show the near misses, so the
user can promote any of them. Show the list of commands and entry points that
delete, overwrite, truncate or migrate user data, each with its ruling, and
name every item left unresolved. Include the Architect's per-path costs for
large files and the whole proposed list's combined cost, using the Git checks
above. State the actual denominator, the whole-history fallback when used,
and that merges can make the selected range larger than 100 commits for the
combined count as well as per-path counts. Let
the user add entries, drop entries, or defer the whole list before anything is
submitted. Submit the answer only after they have settled it:

- the approved entries, as JSON array text of `{"path", "reason"}` objects;
- `[]` when they approve having no entries beyond the floor;
- `defer` when they want to decide later.

Each of those answers is a string value in `AnswerSet.answers`, not a JSON
array value. Before methodology 2 the wording called the answer an array
without specifying that outer string encoding. For example:

```json
{"schema_version":2,"answers":{"protected.paths":"[{\"path\":\"internal/auth\",\"reason\":\"session and token handling\"}]"}}
```

An approved empty list uses `"protected.paths":"[]"`; deferral uses
`"protected.paths":"defer"`.

The user approves the result again at the interview summary, and a human merges
the PR that commits it.
