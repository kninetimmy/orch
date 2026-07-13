package codex

import "embed"

// AgentTOMLs embeds the five shipped Codex agent definitions under
// agents/ verbatim (LF-only, byte-identical to what this plugin ships,
// since go:embed reads the file at build time). internal/agents reads
// from this embed.FS to render `.codex/agents/*.toml` from the
// effective configuration, so the shipped adapter files and `orch
// render-agents`'s output share exactly one canonical source and
// cannot silently diverge: any edit to a file under agents/ changes
// what both the plugin ships and the render command produces, in the
// same build.
//
//go:embed agents/*.toml
var AgentTOMLs embed.FS
