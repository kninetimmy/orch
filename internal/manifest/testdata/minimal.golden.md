<!-- orch:manifest:begin -->
### Orch audit record

**Objective:** Ship the manifest round trip.

**Acceptance criteria:**
- Render and Parse agree on every field.

**Required tests:**
- `go test ./internal/manifest/...`

| Field | Value |
| --- | --- |
| Role | `implementer` |
| Executor | `opus-4-8` — effort `high` |
| Reviewer | `gpt-5.6-sol` — effort `medium` |
| Effort delivery | `parameter` — the host applied the routed effort as a real model parameter |
| Config revision | `cfg-2026-07-10` |

**Routing rationale:** Selected implementer for a bounded single-file change.

**Escalations:** _none_

**Verification:** _none_

<!-- orch:manifest:data
{
  "schema_version": 3,
  "objective": "Ship the manifest round trip.",
  "acceptance_criteria": [
    "Render and Parse agree on every field."
  ],
  "required_tests": [
    "go test ./internal/manifest/..."
  ],
  "role": "implementer",
  "executor": {
    "model": "opus-4-8",
    "effort": "high"
  },
  "routing_rationale": "Selected implementer for a bounded single-file change.",
  "reviewer": {
    "model": "gpt-5.6-sol",
    "effort": "medium"
  },
  "effort_delivery": "parameter",
  "config_revision": "cfg-2026-07-10"
}
-->
<!-- orch:manifest:end -->