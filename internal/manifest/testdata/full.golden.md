<!-- orch:manifest:begin -->
### Orch audit record

**Objective:** Ship the manifest round trip.

**Acceptance criteria:**
- Render and Parse agree on every field.
- A hand-edited region fails closed.

**Required tests:**
- `go test ./internal/manifest/...`
- `go vet ./...`

| Field | Value |
| --- | --- |
| Role | `implementer` |
| Executor | `opus-4-8` — effort `high` |
| Reviewer | `gpt-5.6-sol` — effort `medium` |
| Effort delivery | `prompt-cue` — this host has no effort parameter; the routed effort reached the executor as a prompt cue only |
| Config revision | `cfg-2026-07-10` |

**Routing rationale:** Selected implementer for a bounded single-file change.

**Escalations:**
- 2026-07-10T14:03:00Z — escalation (implementer): `sonnet-4-8` (effort `medium`) → `opus-4-8` (effort `high`) — Repeated test failures on the concurrency path.
- substitution: `opus-4-8` (effort `high`) → `opus-4-8` (effort `medium`) — Downgraded effort after the fix landed.

**Verification:**
- **targeted-tests** — pass — `go test ./internal/manifest/...` (2026-07-10T14:20:00Z)
- **vet** — pass — `go vet ./...` — no findings
- **ci** — CLEAN — required checks green (2026-07-10T14:45:00Z)

<!-- orch:manifest:data
{
  "schema_version": 2,
  "objective": "Ship the manifest round trip.",
  "acceptance_criteria": [
    "Render and Parse agree on every field.",
    "A hand-edited region fails closed."
  ],
  "required_tests": [
    "go test ./internal/manifest/...",
    "go vet ./..."
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
  "effort_delivery": "prompt-cue",
  "escalations": [
    {
      "kind": "escalation",
      "role": "implementer",
      "from": {
        "model": "sonnet-4-8",
        "effort": "medium"
      },
      "to": {
        "model": "opus-4-8",
        "effort": "high"
      },
      "reason": "Repeated test failures on the concurrency path.",
      "at": "2026-07-10T14:03:00Z"
    },
    {
      "kind": "substitution",
      "from": {
        "model": "opus-4-8",
        "effort": "high"
      },
      "to": {
        "model": "opus-4-8",
        "effort": "medium"
      },
      "reason": "Downgraded effort after the fix landed."
    }
  ],
  "config_revision": "cfg-2026-07-10",
  "verifications": [
    {
      "name": "targeted-tests",
      "command": "go test ./internal/manifest/...",
      "result": "pass",
      "at": "2026-07-10T14:20:00Z"
    },
    {
      "name": "vet",
      "command": "go vet ./...",
      "result": "pass",
      "detail": "no findings"
    },
    {
      "name": "ci",
      "result": "CLEAN",
      "detail": "required checks green",
      "at": "2026-07-10T14:45:00Z"
    }
  ]
}
-->
<!-- orch:manifest:end -->