# scout-dev-scan task v1

Trace Scan and its target-name/root-pair helpers in internal/instructions. Determine (1) whether all dot-prefixed directories or only .git are skipped, (2) whether the root AGENTS.md/CLAUDE.md pair counts as nested conflicts, and (3) which case variants of those names are recognized in visible nested directories. Support each claim with source or a reproduction. Distinguish reporting from editing. Do not modify source.

Permitted source paths: `internal/instructions/instructions.go`, `internal/instructions/block.go`, `internal/instructions/marker.go`, `internal/instructions/errors.go`, `internal/instructions/scan.go`.
Supplied instructions: ROLE.md and CONTEXT.md. All other history is excluded.
