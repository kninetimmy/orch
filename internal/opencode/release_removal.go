package opencode

import (
	"context"
	"fmt"
	"os"
	"slices"

	"github.com/kninetimmy/orch/internal/paths"
)

// ReleaseCheck rechecks observable activity before Git mutation after release.
// If removed is nonempty, the caller has already verified deletion of that
// checkout. Only remaining targets need delete access and cwd attribution.
// Bound merged cleanup also requests the non-loading global job probe.
type ReleaseCheck func(ctx context.Context, removed string) error

func recheckReleasedLocations(ctx context.Context, call releaseCall, targets []releaseLocation, removed string,
	inventory func(releaseCall) ([]releaseLocation, error), checkJobs bool,
) error {
	remaining := targets
	if removed != "" {
		if _, err := os.Lstat(removed); !os.IsNotExist(err) {
			return fmt.Errorf("OpenCode checkout release: earlier checkout is no longer verifiably absent; refusing further removal")
		}
		remaining = nil
		for _, target := range targets {
			removedTarget, err := paths.Inside(removed, target.Directory)
			if err != nil {
				return err
			}
			if removedTarget {
				removedTarget, err = paths.Inside(target.Directory, removed)
				if err != nil {
					return err
				}
			}
			if !removedTarget {
				remaining = append(remaining, target)
			}
		}
	}
	if len(remaining) == 0 {
		if checkJobs {
			return releaseRunningJobs(call, nil)
		}
		return nil
	}
	cwds := make([]string, 0, len(remaining))
	for _, target := range remaining {
		cwds = append(cwds, target.Directory)
	}
	before, err := inventory(call)
	if err != nil {
		return err
	}
	// Ordinary work can use a target cwd from another cache key. Never
	// scope a read to an evicted target or repeat the history/terminal walk.
	if err := releaseOrdinaryProcesses(call, cwds, before); err != nil {
		return err
	}
	if checkJobs {
		if err := releaseRunningJobs(call, nil); err != nil {
			return err
		}
	}
	after, err := inventory(call)
	if err != nil {
		return err
	}
	seen := make(map[string]os.FileInfo, len(before))
	for _, location := range before {
		seen[location.Directory] = location.info
	}
	if len(before) != len(after) || slices.ContainsFunc(after, func(location releaseLocation) bool {
		return !os.SameFile(seen[location.Directory], location.info)
	}) {
		if checkJobs {
			return fmt.Errorf("OpenCode checkout release: location inventory changed during the final activity check; refusing Git removal")
		}
		return fmt.Errorf("OpenCode checkout release: location inventory changed during the final shell/PTY check; refusing Git removal")
	}
	return checkReleaseDeleteAccess(ctx, remaining)
}

func checkReleaseIdentity(location releaseLocation) error {
	info, err := releaseDirectory(location.Directory)
	if err != nil || !os.SameFile(location.info, info) {
		return fmt.Errorf("OpenCode checkout release: directory disappeared or changed identity; refusing Git removal")
	}
	return nil
}

func checkReleaseDeleteAccess(ctx context.Context, targets []releaseLocation) error {
	for _, target := range targets {
		if err := checkReleaseIdentity(target); err != nil {
			return err
		}
		if err := ctx.Err(); err != nil {
			return releaseContextError(err)
		}
		if err := releaseDeleteAccess(target.Directory, target.info); err != nil {
			return err
		}
	}
	if err := ctx.Err(); err != nil {
		return releaseContextError(err)
	}
	return nil
}
