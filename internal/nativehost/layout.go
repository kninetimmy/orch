package nativehost

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"unicode/utf8"

	"github.com/kninetimmy/orch/internal/paths"
)

// Errors from this file carry only the refusal detail. Each host bridge wraps
// them with its own sentinel so callers keep matching that bridge's errors.

// ErrHoldUnsupported reports a platform without an instruction-file hold.
var ErrHoldUnsupported = errors.New("instruction file hold unsupported on this platform")

// Boundary is a checked IsolationPaths layout: canonical workspace and scratch
// plus every protected path, case-insensitively de-duplicated on Windows/macOS
// in declaration order.
type Boundary struct {
	Workspace string
	Scratch   string
	Protected []string
}

// CanonicalPath returns the canonical form of an absolute literal path. On
// Windows it also refuses device/UNC namespaces, alternate streams and
// trailing-dot/space aliases, and resolves junction/8.3 aliases. Directory
// requires an existing directory.
func CanonicalPath(path string, directory bool) (string, error) {
	if path == "" || !filepath.IsAbs(path) || !utf8.ValidString(path) || strings.ContainsAny(path, "\x00\r\n*?[]{}") {
		return "", errors.New("isolation requires absolute literal paths")
	}
	if runtime.GOOS == "windows" {
		// Device/UNC namespaces, alternate streams and trailing-dot/space aliases
		// are not supported by this local-drive profile contract.
		if len(path) < 3 || path[1] != ':' || strings.ContainsAny(path[2:], ":") {
			return "", errors.New("isolation requires native local-drive paths")
		}
		for _, part := range strings.Split(filepath.ToSlash(path[2:]), "/") {
			if part != "." && part != ".." && (strings.HasSuffix(part, ".") || strings.HasSuffix(part, " ")) {
				return "", errors.New("unsafe Windows path alias")
			}
		}
	}
	canonical, err := paths.Canonical(path)
	if err != nil {
		return "", errors.New("cannot canonicalize isolation path")
	}
	canonical, err = finalIsolationPath(canonical)
	if err != nil {
		return "", errors.New("cannot resolve native isolation path aliases")
	}
	if filepath.Dir(canonical) == canonical {
		return "", errors.New("volume roots cannot be isolation locations")
	}
	if directory {
		info, err := os.Stat(canonical)
		if err != nil || !info.IsDir() {
			return "", errors.New("isolation directory is missing or inaccessible")
		}
	}
	return canonical, nil
}

func overlap(a, b string) (bool, error) {
	in, err := paths.Inside(a, b)
	if err != nil || in {
		return in, err
	}
	return paths.Inside(b, a)
}

// ValidateLayout checks that workspace and scratch are existing, canonical,
// non-overlapping directories, that credential paths are declared, and that no
// protected path overlaps either of them. Protected paths are, in order: the
// main checkout, controller state, siblings, credentials, hostProtected, then
// shared Git metadata outside the workspace. This package adds no host home;
// each bridge passes its own in hostProtected. On error the returned Boundary
// holds only what was checked before the refusal.
func ValidateLayout(layout IsolationPaths, hostProtected []string) (Boundary, error) {
	var b Boundary
	var err error
	b.Workspace, err = CanonicalPath(layout.Workspace, true)
	if err != nil {
		return b, err
	}
	b.Scratch, err = CanonicalPath(layout.Scratch, true)
	if err != nil {
		return b, err
	}
	if overlaps, err := overlap(b.Workspace, b.Scratch); err != nil || overlaps {
		return b, errors.New("workspace and scratch overlap or cannot be compared")
	}
	if len(layout.CredentialPaths) == 0 {
		return b, errors.New("credential locations must be protected")
	}
	protected := append([]string{layout.MainCheckout, layout.ControllerState}, layout.SiblingWorkspaces...)
	protected = append(protected, layout.CredentialPaths...)
	protected = append(protected, hostProtected...)
	gitPaths, err := sharedGitPaths(b.Workspace)
	if err != nil {
		return b, err
	}
	protected = append(protected, gitPaths...)
	seen := map[string]bool{}
	for _, path := range protected {
		canonical, err := CanonicalPath(path, false)
		if err != nil {
			return b, err
		}
		for _, allowed := range []string{b.Workspace, b.Scratch} {
			if overlaps, err := overlap(allowed, canonical); err != nil || overlaps {
				return b, errors.New("protected path overlaps workspace/scratch or cannot be compared")
			}
		}
		key := canonical
		if runtime.GOOS == "windows" || runtime.GOOS == "darwin" {
			key = strings.ToLower(key)
		}
		if !seen[key] {
			seen[key] = true
			b.Protected = append(b.Protected, canonical)
		}
	}
	return b, nil
}

func readGitPointer(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	data, err := io.ReadAll(io.LimitReader(f, 4097))
	err = errors.Join(err, f.Close())
	if err != nil || len(data) > 4096 || strings.TrimSpace(string(data)) == "" {
		return "", errors.New("invalid shared Git pointer")
	}
	return strings.TrimSpace(string(data)), nil
}

func sharedGitPaths(workspace string) ([]string, error) {
	gitDir := filepath.Join(workspace, ".git")
	info, err := os.Lstat(gitDir)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil // synthetic/non-Git workspace
	}
	if err != nil {
		return nil, errors.New("cannot inspect workspace Git metadata")
	}
	if info.Mode().IsRegular() {
		pointer, err := readGitPointer(gitDir)
		if err != nil || !strings.HasPrefix(pointer, "gitdir: ") {
			return nil, errors.New("invalid workspace Git pointer")
		}
		gitDir = strings.TrimPrefix(pointer, "gitdir: ")
		if !filepath.IsAbs(gitDir) {
			gitDir = filepath.Join(workspace, gitDir)
		}
	}
	gitDir, err = CanonicalPath(gitDir, true)
	if err != nil {
		return nil, err
	}
	metadata := []string{gitDir}
	commonFile := filepath.Join(gitDir, "commondir")
	if _, err := os.Lstat(commonFile); err == nil {
		common, err := readGitPointer(commonFile)
		if err != nil {
			return nil, errors.New("cannot inspect shared Git common directory")
		}
		if !filepath.IsAbs(common) {
			common = filepath.Join(gitDir, common)
		}
		common, err = CanonicalPath(common, true)
		if err != nil {
			return nil, err
		}
		metadata = append(metadata, common)
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, errors.New("cannot inspect shared Git common directory")
	}
	var protected []string
	for _, path := range metadata {
		inside, err := paths.Inside(workspace, path)
		if err != nil {
			return nil, errors.New("cannot compare shared Git metadata")
		}
		if !inside {
			protected = append(protected, path)
		}
	}
	return protected, nil
}

// CheckInstructionHash verifies that an approved instruction artifact is a
// canonical, non-symlink regular file of at most 64 KiB of UTF-8 whose raw bytes
// match the approved SHA-256.
func CheckInstructionHash(source InstructionSource) error {
	canonical, err := CanonicalPath(source.Path, false)
	if err != nil || canonical != source.Path || len(source.SHA256) != 64 {
		return errors.New("noncanonical approved instruction artifact")
	}
	info, err := os.Lstat(source.Path)
	if err != nil || !info.Mode().IsRegular() {
		return errors.New("approved instruction artifact unavailable")
	}
	f, err := os.Open(source.Path)
	if err != nil {
		return errors.New("approved instruction artifact unreadable")
	}
	defer func() { _ = f.Close() }()
	opened, err := f.Stat()
	if err != nil || !os.SameFile(info, opened) || info.Size() > 64*1024 {
		return errors.New("approved instruction artifact identity/size changed")
	}
	data, err := io.ReadAll(io.LimitReader(f, 64*1024+1))
	if err != nil || len(data) > 64*1024 || !utf8.Valid(data) || fmt.Sprintf("%x", sha256.Sum256(data)) != source.SHA256 {
		return errors.New("approved instruction artifact hash changed")
	}
	return nil
}
