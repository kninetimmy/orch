---
description: Show this repository's machine-local Orch overrides and, if desired, run the interactive interview to change them.
---

# /orch:configure-local

Run `orch configure-local` (bare, no flags) first and show its
machine-local override report to the user verbatim. Never pipe
anything into this bare form — it never reads stdin.

If the user wants to change the local overrides, load the `orch-setup`
skill and follow its step loop using `orch configure-local --step` for
every step, and `orch configure-local --apply` as the terminal form
once the interview reaches `kind: "complete"`. That terminal form
writes `config.local.toml` directly on this machine — there is no PR
and nothing to merge.

If a Delivery run is active, the bare report and the step loop will
both refuse; report that refusal to the user rather than working
around it.

No protocol is duplicated here — `orch-setup` owns every detail of the
step loop, the question presentation, and the terminal-form handoff.
