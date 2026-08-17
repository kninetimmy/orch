---
name: orch-setup
description: Drive the orch init, configure, and configure-local stateless interviews with OpenCode's native question tool.
---

# Orch Setup

Run the requested bare command first so the human sees current detection or
status. Then maintain one complete answer document:

```json
{"schema_version":1,"answers":{}}
```

For each step, write that JSON to an OS temporary file outside the repository,
pipe it to `orch <command> --step`, and parse the returned document. Resubmit
the full answer map every time.

For `kind: "questions"`, ask each question in order using OpenCode's native
`question` tool. Use the document's header, prompt, preamble, option labels,
descriptions, recommended marker, and default. Record the selected option's
`value`, never its display label. When `free_text` is true, OpenCode's automatic
custom-answer choice is the free-text path; record the typed value verbatim.
Ask `kind: "text"` as plain text and also record it verbatim.

For `kind: "summary"`, show the complete configuration, configuration diff,
every file path/existence/diff or new content, gitignore additions, conflicts,
and blockers. Ask its approval question with `question`. If blockers are
non-empty, report all of them and stop.

For `kind: "aborted"`, stop without writing. For `kind: "complete"`, pass the
final answer document to the matching terminal form:

| Interview | Terminal form | Result |
| --- | --- | --- |
| `orch init` | `orch init --bootstrap` | Opens a PR for human merge. |
| `orch configure` | `orch configure --deliver` | Opens a PR for human merge. |
| `orch configure-local` | `orch configure-local --apply` | Writes only the local override. |

An init or configure terminal result is not active until its PR is merged.
