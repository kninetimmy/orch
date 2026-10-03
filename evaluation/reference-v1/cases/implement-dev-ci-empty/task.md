# implement-dev-ci-empty task v1

Fix RequiredCI in internal/ghops/checks.go for repositories whose rollup contains checks but gh pr checks --required emits empty or whitespace-only stdout with exit 0. Return CINoChecks, zero required checks and the existing total count. Preserve rejection of nonempty malformed JSON and unexpected exit codes, and preserve existing pending/failing/passing bucket semantics. Make the smallest change to checks.go; add local tests if useful. Do not call GitHub.

Permitted source paths: `internal/ghops/ghops.go`, `internal/ghops/checks.go`, `internal/ghops/errors.go`, `internal/paths/paths.go`, `internal/execx/execx.go`.
Supplied instructions: ROLE.md and CONTEXT.md. All other history is excluded.
