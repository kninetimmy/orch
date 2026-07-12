// Package codex anchors the adapters/codex directory as a Go package
// so its validation tests (plugin_test.go) build and run under the
// module's ordinary `go test ./...`. The Codex CLI plugin itself is
// non-Go: the manifest, hooks, skills, and agent TOMLs under this
// directory are read directly by Codex CLI, never compiled. This file
// carries no runtime code.
package codex
