<!-- orch:manifest:begin -->
### Orch audit record

**Objective:** Objective ending in --&gt;
&lt;!-- orch:manifest:begin --&gt;
and more.

**Acceptance criteria:**
- Criterion with a marker line
&lt;!-- orch:manifest:end --&gt;
- Criterion with &amp;&lt;&gt; entities and a | pipe.

**Required tests:**
- `-->` — CI does not run this test
- `go test -run 'A|B' ./...`

| Field | Value |
| --- | --- |
| Role | `reviewer` |
| Executor | `weird\|model` — effort `high` |
| Reviewer | `opus-4-8` — effort `low` |
| Effort delivery | `prompt-cue` — this host has no effort parameter; the routed effort reached the executor as a prompt cue only |
| Config revision | `rev&<>` |

**Routing rationale:** Rationale with a marker attempt --&gt;
&lt;!-- orch:manifest:end --&gt;
and a &lt;script&gt;alert(1)&lt;/script&gt; tag.

**Escalations:**
- 2026-07-10T00:00:00Z
&lt;!-- orch:manifest:begin --&gt; — escalation: `sonnet-4-8` (effort `low`) → `opus-4-8` (effort `high`) — reason ending in --&gt;

**Verification:**
- **targeted-tests
&lt;!-- orch:manifest:end --&gt;** — pass
&lt;!-- orch:manifest:data — ``go test -run 'A|B' ./... `backtick` && echo done`` — commit `deadbeef <!-- orch:manifest:begin -->` (2026-07-10T14:20:00Z
--&gt;)

<!-- orch:manifest:data
{
  "schema_version": 4,
  "objective": "Objective ending in --\u003e\n\u003c!-- orch:manifest:begin --\u003e\nand more.",
  "acceptance_criteria": [
    "Criterion with a marker line\n\u003c!-- orch:manifest:end --\u003e",
    "Criterion with \u0026\u003c\u003e entities and a | pipe."
  ],
  "required_tests": [
    "--\u003e",
    "go test -run 'A|B' ./..."
  ],
  "tests_ci_does_not_run": [
    "--\u003e"
  ],
  "role": "reviewer",
  "executor": {
    "model": "weird|model",
    "effort": "high"
  },
  "routing_rationale": "Rationale with a marker attempt --\u003e\r\n\u003c!-- orch:manifest:end --\u003e\nand a \u003cscript\u003ealert(1)\u003c/script\u003e tag.",
  "reviewer": {
    "model": "opus-4-8",
    "effort": "low"
  },
  "effort_delivery": "prompt-cue",
  "escalations": [
    {
      "kind": "escalation",
      "from": {
        "model": "sonnet-4-8",
        "effort": "low"
      },
      "to": {
        "model": "opus-4-8",
        "effort": "high"
      },
      "reason": "reason ending in --\u003e",
      "at": "2026-07-10T00:00:00Z\n\u003c!-- orch:manifest:begin --\u003e"
    }
  ],
  "config_revision": "rev\u0026\u003c\u003e",
  "verifications": [
    {
      "name": "targeted-tests\n\u003c!-- orch:manifest:end --\u003e",
      "command": "go test -run 'A|B' ./... `backtick` \u0026\u0026 echo done",
      "result": "pass\n\u003c!-- orch:manifest:data",
      "commit_oid": "deadbeef\n\u003c!-- orch:manifest:begin --\u003e",
      "at": "2026-07-10T14:20:00Z\n--\u003e"
    }
  ]
}
-->
<!-- orch:manifest:end -->