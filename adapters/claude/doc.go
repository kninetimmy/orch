// Package claude anchors the adapters/claude directory as a Go package
// so its validation tests (plugin_test.go) build and run under the
// module's ordinary `go test ./...`. The Claude Code plugin itself is
// non-Go: the manifest, hooks, skills, commands, and agents under this
// directory are read directly by Claude Code, never compiled.
package claude

import _ "embed"

// PluginManifestJSON is the shipped plugin manifest and therefore the
// canonical adapter version for this build.
//
//go:embed .claude-plugin/plugin.json
var PluginManifestJSON string
