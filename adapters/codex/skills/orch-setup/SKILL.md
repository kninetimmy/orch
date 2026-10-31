---
name: orch-setup
description: >-
  Shared step-loop driver for the three Orch setup interviews (`orch
  init --step`, `orch configure --step`, `orch configure-local --step`).
  Invoke this skill directly to run any of the three interviews.
  Presents each ordinary question and emitted 2-3-option pagination page using
  supported native questions or plain text and drives the loop to terminal form.
---

# Orch Setup

This skill drives any of the three `orch <cmd> --step` interviews. All
three speak the same stateless step-loop protocol
(`internal/question`): you resubmit everything known so far on every
step, and the core tells you what to do next.

## State you hold

Maintain one `AnswerSet` across the whole interview:

```json
{"schema_version": 2, "answers": {}}
```

Question wire `schema_version` is closed at `2` for both `AnswerSet` and
`Document`; reject any other Document before reading `kind`, `questions`, or
`pagination`. A rejected AnswerSet means the setup skill and `orch` binary are
out of date with each other; update the older side before retrying.

Resubmit it **in full** on every step — the core holds no session of
its own. Never send a partial or incremental update.

## The step loop

1. Write the current `AnswerSet` to a scratch file (as
   `orch-delivery` does: OS temp, outside the repo).
2. Run `orch <cmd> --step < <scratch-file>` and parse the
   `question.Document` on stdout.
3. Dispatch on `Document.kind`:

### `kind: "questions"`

`Document.questions` carries 1–4 independent `Question`s. Iterate
`Document.questions` **in order**, asking each question and waiting for
its answer before moving to the next.

Handle `protected.paths` with the guided survey below before applying the
ordinary-question rules.

Use `request_user_input` only when it is available, permitted for the
question's purpose, and able to represent every offered option within
its current contract. Otherwise present the same complete question as
plain text, including every option in its original order, and wait for
the human's answer. If the current contract prohibits approval requests,
use plain text for approvals even when ordinary questions are permitted.
These are checks on `request_user_input`; any other question tool must
satisfy its own current contract and the same requirements for complete
questions and human approval.

Before this update, setup treated tool unavailability as the only reason
to fall back and attributed availability to Plan mode or
`default_mode_request_user_input`. After this update, the current tool
contract controls availability, permitted purposes, and complete
representation; configuration flags do not override those restrictions.

For a question without `pagination`, present its `header` and `prompt`,
and list its `options[]` with each option's `label` for display and
`description` for detail. Include any `preamble`.
Before this update, recommendation hints were confined to descriptions
because no native recommendation affordance was assumed. After this update,
carry `recommended: true` and any `default` as words using the supported
presentation; these hints never supply an answer.

When the human answers that ordinary question, record `answers[question.id] = option.value`
— **the option's `value`, never its `label`**. The label is display
text only; the value is what the core expects back.

When `pagination` is present, require `pagination.hosts` to contain `codex`,
start at `pagination.pages[0]`, and present that page's 2–3 options exactly as
emitted, through a permitted native call or as plain text under the rule above.
Use the parent question's header/prompt and mention the page's `index`/`total`
in the prompt. Never synthesize, split,
merge, reorder, or replace pages with a request to type an identifier. A page
option with `value` is a real answer: record it and finish the question. An
option with `action` is navigation only: `next` and `previous` move one page,
while `cancel` stops the interview without recording an answer. Never submit an
action as `answers[question.id]`.

If a non-paginated `select` question has `free_text: true`, it still carries
real options. Use the host's built-in free-text entry when available instead
of adding a synthetic Other option. When that entry is unavailable, present
the complete question as plain text, accepting a listed choice or a custom
value. A listed option records its `value` as usual; record custom answers
verbatim as `answers[question.id]` — do not transform or re-validate them
yourself; if the core rejects one, its re-ask message says why.

Before this update, free-text selects added a synthetic Other entry and
required a separate typed follow-up because the native tool was described
as selection-only. After this update, use built-in free text where supported,
otherwise one complete plain-text question.

If a non-paginated question has `kind: "text"`, it carries no options at all: fall
back to a plain text prompt — put the
question's `prompt` (and `preamble`, if present) to the human as free
text, and mention any `default` in words. Whatever the human types is
recorded verbatim as `answers[question.id]` — do not transform or
re-validate it yourself.

Once every question in this step's batch is answered, return to step 1
with the updated `AnswerSet`.

#### The protected-paths question

`protected.paths` (asked by `orch init` always, and by `orch configure` when
`pick.protected` is answered `yes`) is a guided survey, not an ordinary
free-text question. Read `PROTECTED_PATHS_SURVEY.md` in this skill's directory
and:

1. If the user's delegation policy already authorizes named `orch-scout`
   dispatch, use that agent per the methodology. Otherwise perform the same
   survey as the Architect. Neither this skill nor a setup question authorizes
   delegation. The Architect runs tracking checks and Git commit counts in
   either case.
2. Present each proposed entry with its one-line reason, the test it meets,
   its file evidence and tracking status, grouped by category. Name empty
   categories, near misses the user may promote, every destructive entry point
   with its ruling, and unresolved items. Give large-file per-path costs and
   the combined cost with the actual denominator, including the whole-history
   fallback and merge-commit caveat from the methodology.
3. Preserve the complete emitted question: its header, prompt, preamble and
   every offered option in original order. Apply the current tool-contract
   rules above to this approval and any edits, including built-in free text
   where available and complete plain text otherwise; use plain text for
   approval when the tool contract prohibits approval requests. Let the user
   add entries, drop entries, explicitly approve an empty list beyond the
   floor, or defer before you submit anything. A list, including an empty
   list, requires an actual affirmative human answer; defaults, preselection,
   silence and timeout never approve it.
4. Record `answers["protected.paths"]` as a string containing JSON array text
   of the approved `{"path", "reason"}` objects, `"[]"` for an approved empty
   list, or `"defer"` when the user defers. Never put a JSON array directly in
   `AnswerSet.answers`. The core rejects a malformed answer; fix it and
   resubmit, do not guess.

Before this update, `protected.paths` followed the ordinary option/free-text
path. After this update, only `protected.paths` uses the guided survey and
serialization above; custom answers to other ordinary questions remain
verbatim. Every question tool still follows its own current contract and the
complete-question and affirmative-approval rules above.

`pick.protected` defaults to `yes` when the committed config has no protected
list or one approved under a different survey methodology version, and to `no`
otherwise; mention that default without answering for the user. A `defer` in
`orch init` writes no protected section; in `orch configure` it leaves the
committed list unchanged. Older lists remain readable but grant-ineligible
until freshly surveyed and approved; never migrate or re-approve a list merely
to match the binary's methodology version.

### `kind: "summary"`

Show, in full:

- `summary.config_toml` — the resulting configuration.
- `summary.config_diff` — only present for `orch configure`; the
  unified diff between the committed `config.toml` and this proposal.
- Every entry in `summary.files[]`: `path`, whether it `existed`, and
  its `diff` (or, if no diff was supplied, its `new_content`).
- When the configuration has a `[protected]` section, list every final protected
  path and reason for summary approval; explicitly show an empty list beyond
  the built-in floor when the section has no entries.
- `summary.gitignore_lines`, if any.
- `summary.conflicts`, if any.

The approval question for this summary rides inside `Document.questions`
(handled the same way as above, one question at a time) **unless**
`summary.blockers` is non-empty.

Setup approval requires an actual affirmative human answer to the presented
summary. Defaults, preselection, silence, timeout, unavailable tools, or an
unanswered question never count as approval. Do not record an approval value
or run a terminal form without that answer.

### Non-empty `summary.blockers`

Report every blocker to the human and **stop** — do not attempt to
resolve a blocker yourself, and do not proceed to the terminal form
while any blocker remains.

### `kind: "complete"`

Only after the human has affirmatively approved the summary, run the terminal
form for this command (see table below) with the final `AnswerSet` on stdin,
and report its result.

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
