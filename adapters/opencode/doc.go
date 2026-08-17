// Package opencode embeds the native OpenCode V2 adapter assets used by Orch.
package opencode

import "embed"

// PackageJSON is the shipped adapter manifest and version source.
//
//go:embed package.json
var PackageJSON string

// AgentDefinitions contains the canonical native V2 role definitions.
//
//go:embed agents/*.md
var AgentDefinitions embed.FS

// Skills contains the native V2 Orch workflows.
//
//go:embed skills/*/SKILL.md
var Skills embed.FS
