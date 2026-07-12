---
description: Show this repository's committed Orch configuration and, if desired, run the interactive interview to change it.
---

# /orch:configure

Run `orch configure` (bare, no flags) first and show its committed-
configuration report to the user verbatim. Never pipe anything into
this bare form — it never reads stdin.

If the user wants to change the configuration, load the `orch-setup`
skill and follow its step loop using `orch configure --step` for every
step, and `orch configure --deliver` as the terminal form once the
interview reaches `kind: "complete"`. That terminal form opens a PR — a
human still has to merge it on GitHub for the change to take effect.

If a Delivery run is active, the bare report and the step loop will
both refuse; report that refusal to the user rather than working
around it.

No protocol is duplicated here — `orch-setup` owns every detail of the
step loop, the question presentation, and the terminal-form handoff.
