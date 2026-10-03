# review-dev-ci-defective task v1

Review the newly introduced RequiredCI adapter against this contract: a nonempty status rollup with no required checks may yield exit 0 and empty/whitespace stdout from gh pr checks --required, and must return no-checks with the total preserved. Malformed nonempty JSON and unexpected exits must remain errors; bucket precedence must be preserved. Report verdict and distinct blocking findings with trigger, impact, severity and source/reproduction evidence. Do not call GitHub. Review only the selected CI component.

Permitted source paths: `internal/ghops/ghops.go`, `internal/ghops/checks.go`, `internal/ghops/errors.go`, `internal/paths/paths.go`, `internal/execx/execx.go`.
Supplied instructions: ROLE.md and CONTEXT.md. All other history is excluded.
