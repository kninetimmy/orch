---
name: orch-setup
description: >-
  Shared step-loop driver for the three Orch setup interviews (`orch
  init --step`, `orch configure --step`, `orch configure-local --step`).
  Invoke this skill directly to run any of the three interviews.
  Presents each interview's Document one question at a time via Codex's
  `ask` primitive and drives the loop to its terminal form.
---

# Orch Setup

This skill drives any of the three `orch <cmd> --step` interviews. All
three speak the same stateless step-loop protocol
(`internal/question`): you resubmit everything known so far on every
step, and the core tells you what to do next.

## State you hold

Maintain one `AnswerSet` across the whole interview:

```json
{"schema_version": 1, "answers": {}}
```

Resubmit it **in full** on every step — the core holds no session of
its own. Never send a partial or incremental update.

## The step loop

1. Write the current `AnswerSet` to a scratch file (as
   `orch-delivery` does: OS temp, outside the repo).
2. Run `orch <cmd> --step < <scratch-file>` and parse the
   `question.Document` on stdout.
3. Dispatch on `Document.kind`:

### `kind: "questions"`

`Document.questions` carries 1–4 independent `Question`s. Codex's `ask`
primitive presents one question at a time, so iterate
`Document.questions` **in order**, asking each with its own `ask` call
before moving to the next — there is no batched multi-question call to
fall back on here.

For each question, use its `header` and `prompt` as the `ask` call's
header/prompt, and list its `options[]` with each option's `label` for
display and `description` for detail. There is no separate
"recommended" UI affordance on this host either: if an option has
`recommended: true`, say so in words in the description text. If the
question has a `default`, likewise mention it in words in the
description of the matching option.

When the human answers, record `answers[question.id] = option.value`
— **the option's `value`, never its `label`**. The label is display
text only; the value is what the core expects back.

If a question has `kind: "text"`, or `free_text: true` on a `select`
question, it never carries meaningful options for that path: fall back
to a plain text prompt instead of an `ask` call with options — put the
question's `prompt` (and `preamble`, if present) to the human as free
text, and mention any `default` in words. Whatever the human types is
recorded verbatim as `answers[question.id]` — do not transform or
re-validate it yourself.

Once every question in this step's batch is answered, return to step 1
with the updated `AnswerSet`.

### `kind: "summary"`

Show, in full:

- `summary.config_toml` — the resulting configuration.
- `summary.config_diff` — only present for `orch configure`; the
  unified diff between the committed `config.toml` and this proposal.
- Every entry in `summary.files[]`: `path`, whether it `existed`, and
  its `diff` (or, if no diff was supplied, its `new_content`).
- `summary.gitignore_lines`, if any.
- `summary.conflicts`, if any.

The approval question for this summary rides inside `Document.questions`
(handled the same way as above, one question at a time) **unless**
`summary.blockers` is non-empty.

### Non-empty `summary.blockers`

Report every blocker to the human and **stop** — do not attempt to
resolve a blocker yourself, and do not proceed to the terminal form
while any blocker remains.

### `kind: "complete"`

The interview is answered and approved. Run the terminal form for this
command (see table below) with the final `AnswerSet` on stdin, and
report its result.

### `kind: "aborted"`

The human chose not to proceed. Report that and stop; nothing is
written.

## Terminal forms

| Command | Terminal form | Where it lands |
|---|---|---|
| `orch init` | `orch init --bootstrap` | Opens a PR a human merges on GitHub. |
| `orch configure` | `orch configure --deliver` | Opens a PR a human merges on GitHub. |
| `orch configure-local` | `orch configure-local --apply` | Writes `config.local.toml` locally — no PR, nothing to merge. |

Say plainly, when reaching a terminal form for `init` or `configure`,
that the change lands as a PR the human still has to merge on GitHub —
running the terminal form is not the same as the change taking effect.

## The bare form

`orch init`, `orch configure`, and `orch configure-local`, run with no
flags, are each a **human report** — a plain-text detection/status
summary. Run this bare form first, before starting the step loop, so
the human sees the current state before answering anything. It never
reads stdin; do not pipe an `AnswerSet` into it.
