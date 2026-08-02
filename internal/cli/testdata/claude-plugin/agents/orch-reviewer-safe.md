---
name: orch-reviewer-safe
description: Fixture standing in for the installed orch-reviewer-safe definition (downgraded pull request review) in internal/cli's doctor tests.
tools: Read, Grep, Glob
model: claude-sonnet-5
---

# Fixture

Not a real agent definition: only the frontmatter model above is read,
by the installed-definition check `orch doctor` runs for the Claude
host. The models here match what cli_test.go's validTOML gives
`hosts.claude.roles`, so the default fake plugin listing reports a
healthy install.
