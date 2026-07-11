<!-- orch:manifest:begin -->
### Orch audit record

| Field | Value |
| --- | --- |
| Role | `reviewer` |
| Executor | `weird\|model` — effort `high` |
| Reviewer | `opus-4-8` — effort `low` |
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
&lt;!-- orch:manifest:data — ``go test -run 'A|B' ./... `backtick` && echo done`` (2026-07-10T14:20:00Z
--&gt;)

<!-- orch:manifest:data
{
  "schema_version": 1,
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
      "at": "2026-07-10T14:20:00Z\n--\u003e"
    }
  ]
}
-->
<!-- orch:manifest:end -->