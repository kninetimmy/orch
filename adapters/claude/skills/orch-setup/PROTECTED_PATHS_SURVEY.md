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

Root-only paths, each covering itself and everything under it, ignoring case:
`.github/workflows`, `LICENSE` (that exact name only; `LICENSE.md` and
`LICENSE.txt` are not covered), and `.orchestrator`.

Only an exact match is covered. Other manifests (`Gemfile`, `pom.xml`,
`build.gradle`, `go.work`), other scripts (`scripts/release.sh`,
`release.ps1`), other CI systems and other license file names are not on the
floor. When in doubt, propose the path: a redundant entry is harmless and a
missing one is not.

## What to survey for

Survey the repository against each of these eight categories. A repository
may have nothing in a category; say so rather than inventing an entry.

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
   release file names listed above, so propose every other script and
   CI configuration in this category.
7. **Public API and wire contracts.** Exported interfaces, CLI surfaces,
   protocol and file formats other programs depend on, schema versions, and the
   tests that pin them.
8. **Destructive data paths.** Code that deletes, overwrites, truncates,
   migrates in place or force-pushes, and anything that cannot be undone.

## How to run it

Run the survey with `orch-scout` subagents, which are read-only, so the
Architect's own context stays small. Give each scout one or two categories, the
floor entries above, and these instructions: find the files or directories that belong
in the category, and for each report

- `path`: repository-relative, forward slashes, the smallest path that covers
  the concern (a file, or a directory when the whole subtree is the concern);
  never `.`, never a glob, never a path outside the repository;
- `reason`: one line saying why a change there should never merge without a
  human;
- `evidence`: the file or files you read that show it, with a line number or a
  short quote.

Scouts may run concurrently up to the configured concurrency. Treat their
reports as claims: spot-check the evidence for any entry that looks surprising,
merge duplicates and overlaps (keep the directory when a file sits inside a
directory entry), and drop an entry only when it is an exact floor match as
listed above; otherwise keep it.

## What to present

Show the user every proposed entry with its one-line reason and its file
evidence, grouped by category, and name the categories where the survey found
nothing. Let the user add entries, drop entries, or defer the whole list before
anything is submitted. Submit the answer only after they have settled it:

- the approved entries, as a JSON array of `{"path", "reason"}` objects;
- `[]` when they approve having no entries beyond the floor;
- `defer` when they want to decide later.

The user approves the result again at the interview summary, and a human merges
the PR that commits it.
