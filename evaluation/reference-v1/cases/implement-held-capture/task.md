# implement-held-capture task v1

Fix TotalTokens/rolloutTotal in internal/codexusage/capture.go so unidentified unrelated malformed or empty .jsonl files cannot poison an otherwise exact child capture. Once a rollout identifies the requested child, malformed records, duplicate metadata and unfinished data must still make capture unavailable, even beside a valid matching rollout. Keep exact uniqueness, parent/task attribution and nonnegative resume deltas. Limit changes to capture.go and focused tests; use synthetic local fixtures, never actual session records.

Permitted source paths: `internal/codexusage/capture.go`.
Supplied instructions: ROLE.md and CONTEXT.md. All other history is excluded.
