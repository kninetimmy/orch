package gitops

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// CheckWorktreeRemoval refuses observed Windows traversal hazards before
// canonicalization or deletion. Absence is allowed, not proof of registration
// or ownership. Other platforms retain generic Git/bootstrap symlink behavior.
func CheckWorktreeRemoval(ctx context.Context, target string) error {
	if runtime.GOOS != "windows" {
		return nil
	}
	return inspectWorktree(ctx, target, true)
}

// InspectWorktree is the stronger all-platform OpenCode pre-disposal check,
// shared with Windows teardown rather than a second remover. It reads only
// metadata, including ignored entries, and never descends through an observed
// link/reparse entry. Handle/path identity checks are observations, not an
// atomic snapshot or exclusion of later filesystem/host changes.
func InspectWorktree(ctx context.Context, target string) error {
	return inspectWorktree(ctx, target, false)
}

func inspectWorktree(ctx context.Context, target string, allowAbsent bool) (resultErr error) {
	refuse := func(path string) error {
		return fmt.Errorf("linked or unverifiable checkout entry %s; preserve and inspect it before retrying", path)
	}
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("inspect checkout %s: %w", target, err)
	}
	// Check the requested spelling, not a canonical destination that could hide
	// a redirected root/parent. Remember parent identities for reinspection.
	type ancestor struct {
		path string
		info os.FileInfo
	}
	var parents []ancestor
	parentsComplete := true
	if runtime.GOOS == "windows" {
		for _, part := range strings.FieldsFunc(target, func(r rune) bool { return r == '/' || r == '\\' }) {
			if part == ".." {
				return refuse(target)
			}
		}
		abs, err := filepath.Abs(target)
		if err != nil {
			return fmt.Errorf("inspect checkout %s: %w", target, err)
		}
		var chain []string
		for parent := filepath.Dir(abs); ; parent = filepath.Dir(parent) {
			chain = append(chain, parent)
			if len(chain) > 64 {
				return fmt.Errorf("checkout %s exceeds parent inspection bounds; preserve and inspect it", target)
			}
			if filepath.Dir(parent) == parent {
				break
			}
		}
		for i := len(chain) - 1; i >= 0; i-- {
			if err := ctx.Err(); err != nil {
				return fmt.Errorf("inspect checkout %s: %w", target, err)
			}
			info, err := os.Lstat(chain[i])
			if os.IsNotExist(err) {
				parentsComplete = false
				break // The requested path cannot exist below a missing parent.
			}
			if err != nil || !ordinaryWorktreeEntry(info) || !info.IsDir() {
				return refuse(chain[i])
			}
			parents = append(parents, ancestor{chain[i], info})
		}
	}
	checkParents := func() error {
		for _, parent := range parents {
			if err := ctx.Err(); err != nil {
				return fmt.Errorf("inspect checkout %s: %w", target, err)
			}
			current, err := os.Lstat(parent.path)
			if err != nil || !ordinaryWorktreeEntry(current) || !current.IsDir() || !os.SameFile(parent.info, current) {
				return fmt.Errorf("checkout %s parent identity changed at %s; preserve and inspect it", target, parent.path)
			}
		}
		return nil
	}
	defer func() { resultErr = errors.Join(resultErr, checkParents()) }()
	original, err := os.Lstat(target)
	if allowAbsent && os.IsNotExist(err) {
		return nil
	}
	if err != nil || !parentsComplete || !ordinaryWorktreeEntry(original) || !original.IsDir() {
		return refuse(target)
	}
	root, err := os.OpenRoot(target)
	if err != nil {
		return fmt.Errorf("cannot inspect checkout interior %s; preserve and inspect it: %w", target, err)
	}
	defer func() { resultErr = errors.Join(resultErr, root.Close()) }()
	checkRoot := func() error {
		opened, openErr := root.Lstat(".")
		current, currentErr := os.Lstat(target)
		if openErr != nil || currentErr != nil || !ordinaryWorktreeEntry(current) || !current.IsDir() ||
			!ordinaryWorktreeEntry(opened) || !os.SameFile(original, opened) || !os.SameFile(original, current) {
			return fmt.Errorf("checkout %s identity changed during inspection; preserve and inspect it", target)
		}
		return checkParents()
	}
	if err := checkRoot(); err != nil {
		return err
	}
	defer func() { resultErr = errors.Join(resultErr, checkRoot()) }()
	// ponytail: bound metadata work, refuse larger/deeper trees rather than
	// silently skipping ignored paths or treating incomplete evidence as safe.
	remaining := 100_000
	var walk func(*os.Root, int) error
	walk = func(dir *os.Root, depth int) error {
		if err := ctx.Err(); err != nil {
			return fmt.Errorf("inspect checkout %s: %w", target, err)
		}
		if depth > 64 {
			return fmt.Errorf("checkout %s exceeds interior inspection bounds; preserve and inspect it", target)
		}
		f, err := dir.Open(".")
		if err != nil {
			return fmt.Errorf("cannot inspect checkout interior %s: %w", target, err)
		}
		entries, readErr := f.ReadDir(remaining + 1)
		closeErr := f.Close()
		if readErr != nil && !errors.Is(readErr, io.EOF) {
			return errors.Join(readErr, closeErr)
		}
		if closeErr != nil {
			return closeErr
		}
		if len(entries) > remaining {
			return fmt.Errorf("checkout %s exceeds interior inspection bounds; preserve and inspect it", target)
		}
		remaining -= len(entries)
		for _, entry := range entries {
			if err := ctx.Err(); err != nil {
				return fmt.Errorf("inspect checkout %s: %w", target, err)
			}
			info, err := dir.Lstat(entry.Name())
			if err != nil || !ordinaryWorktreeEntry(info) {
				return refuse(filepath.Join(dir.Name(), entry.Name()))
			}
			if !info.IsDir() {
				continue
			}
			child, err := dir.OpenRoot(entry.Name())
			if err != nil {
				return fmt.Errorf("cannot inspect checkout interior %s: %w", target, err)
			}
			checkChild := func() error {
				opened, openErr := child.Lstat(".")
				current, currentErr := dir.Lstat(entry.Name())
				if openErr != nil || currentErr != nil || !ordinaryWorktreeEntry(current) || !current.IsDir() ||
					!ordinaryWorktreeEntry(opened) || !os.SameFile(info, current) || !os.SameFile(info, opened) {
					return fmt.Errorf("checkout %s interior identity changed; preserve and inspect it", target)
				}
				return nil
			}
			if err := checkChild(); err != nil {
				return errors.Join(err, child.Close())
			}
			walkErr := walk(child, depth+1)
			if err := errors.Join(walkErr, checkChild(), child.Close()); err != nil {
				return err
			}
		}
		return ctx.Err()
	}
	return walk(root, 0)
}
