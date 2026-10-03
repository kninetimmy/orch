# review-held-effort-clean task v1

Review the supplied effort-domain extension in internal/config/config.go and validate.go. Codex must accept low/medium/high/xhigh/max/ultra; Claude must accept low/medium/high/xhigh/max and reject ultra. Both must reject none/minimal, wrong case and unknown tokens. All six role entries must be checked. Return a verdict and supported blocking findings; keep advisories separate. Review only these predicates and role validation, not the whole interview or TOML pipeline.

Permitted source paths: `internal/config/config.go`, `internal/config/validate.go`.
Supplied instructions: ROLE.md and CONTEXT.md. All other history is excluded.
