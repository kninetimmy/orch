---
description: Detect this repository's Orch readiness and, if not yet initialized, run the interactive bootstrap interview.
---

# /orch:init

Run `orch init` (bare, no flags) first and show its detection report to
the user verbatim. Never pipe anything into this bare form — it never
reads stdin.

If the report shows the repository is not yet initialized, load the
`orch-setup` skill and follow its step loop using `orch init --step`
for every step, and `orch init --bootstrap` as the terminal form once
the interview reaches `kind: "complete"`.

If the repository is already initialized, the bare report says so
(pointing at `orch configure` instead); do not start the step loop.

No protocol is duplicated here — `orch-setup` owns every detail of the
step loop, the question presentation, and the terminal-form handoff.
