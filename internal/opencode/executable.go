package opencode

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/kninetimmy/orch/internal/paths"
)

// windowsExecutable never runs or generally parses a launcher. Only npm's known
// native @opencode/cli shim is associated with its adjacent verified package.
// Pinning is a bounded filesystem observation, not protection against replacement
// of executable/package bytes between verification and a later process start.
func windowsExecutable(selected, name string) (string, error) {
	refuse := func() (string, error) {
		return "", fmt.Errorf("cannot verify a native Windows OpenCode executable; use a direct opencode2.exe/opencode.exe or an intact npm @opencode/cli native .cmd layout on PATH (custom, linked or escaping launchers are unsupported)")
	}
	if !filepath.IsAbs(selected) {
		return refuse()
	}
	command, err := paths.Canonical(selected)
	if err != nil {
		return refuse()
	}
	info, err := os.Stat(command)
	if err != nil || !info.Mode().IsRegular() {
		return refuse()
	}
	if strings.EqualFold(filepath.Ext(selected), ".exe") && strings.EqualFold(filepath.Ext(command), ".exe") {
		return command, nil
	}
	if !strings.EqualFold(filepath.Base(selected), name+".cmd") || !strings.EqualFold(filepath.Ext(command), ".cmd") {
		return refuse()
	}
	prefix, err := paths.Canonical(filepath.Dir(selected))
	sameParent, parentErr := sameDirectory(prefix, filepath.Dir(command))
	if err != nil || parentErr != nil || !sameParent || !strings.EqualFold(filepath.Base(command), name+".cmd") {
		return refuse()
	}
	// Exact native cmd-shim template, not a shell grammar or guessed executable
	// next to an arbitrary wrapper. Never project these bytes into diagnostics.
	shim, err := os.ReadFile(command)
	const nativeShim = "@ECHO off\nGOTO start\n:find_dp0\nSET dp0=%~dp0\nEXIT /b\n:start\nSETLOCAL\nCALL :find_dp0\n\"%dp0%\\node_modules\\@opencode\\cli\\bin\\opencode.exe\"   %*\n"
	if err != nil || strings.ReplaceAll(string(shim), "\r\n", "\n") != nativeShim {
		return refuse()
	}
	pkg, err := paths.Canonical(filepath.Join(prefix, "node_modules", "@opencode", "cli"))
	if err != nil || !containedFile(prefix, pkg) {
		return refuse()
	}
	manifest, err := paths.Canonical(filepath.Join(pkg, "package.json"))
	if err != nil || !containedFile(pkg, manifest) {
		return refuse()
	}
	data, err := os.ReadFile(manifest)
	var metadata struct {
		Name string            `json:"name"`
		Bin  map[string]string `json:"bin"`
	}
	if err != nil || json.Unmarshal(data, &metadata) != nil || metadata.Name != "@opencode/cli" ||
		(metadata.Bin[name] != "./bin/opencode.exe" && metadata.Bin[name] != "bin/opencode.exe") {
		return refuse()
	}
	native, err := paths.Canonical(filepath.Join(pkg, "bin", "opencode.exe"))
	if err != nil || !strings.EqualFold(filepath.Ext(native), ".exe") || !containedFile(pkg, native) {
		return refuse()
	}
	info, err = os.Stat(native)
	if err != nil || !info.Mode().IsRegular() {
		return refuse()
	}
	return native, nil
}

// Containment needs both canonical segment containment and the ancestor's actual
// filesystem identity: Windows case folding alone is not sufficient on a
// case-sensitive directory. Unresolved redirecting reparse paths already refuse
// in Canonical. Missing files are never accepted as executable evidence.
func containedFile(root, target string) bool {
	inside, err := paths.Inside(root, target)
	if err != nil || !inside {
		return false
	}
	relative, err := filepath.Rel(root, target)
	if err != nil || relative == "." {
		return false
	}
	ancestor := target
	for range strings.Split(relative, string(filepath.Separator)) {
		ancestor = filepath.Dir(ancestor)
	}
	a, err := os.Stat(root)
	if err != nil || !a.IsDir() {
		return false
	}
	b, err := os.Stat(ancestor)
	return err == nil && b.IsDir() && os.SameFile(a, b)
}
