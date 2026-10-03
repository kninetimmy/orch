# review-dev-risk-clean task v1

Review the newly introduced risk-domain API in internal/routing/risk.go against this contract: exactly the nine declared domains are valid, Domains returns them in declared canonical order, and callers may mutate a returned slice without changing later results. Return approve or request-changes with concrete trigger, impact, severity and evidence for each blocking finding. Advisory suggestions are separate. Review only this API, not the whole historical PR.

Permitted source paths: `internal/routing/risk.go`.
Supplied instructions: ROLE.md and CONTEXT.md. All other history is excluded.
