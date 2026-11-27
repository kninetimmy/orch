package opencode

import (
	"context"
	"fmt"
	"os"
	"slices"

	"github.com/kninetimmy/orch/internal/paths"
)

// ReleaseCheck rechecks observable activity before Git mutation after release.
// A cleanup invocation uses it serially so the same inventory allowance
// applies at every boundary.
// The caller has already verified deletion of each path in removed. Only
// remaining targets need delete access and cwd attribution.
// Bound merged cleanup also requests the non-loading global job probe.
type ReleaseCheck func(ctx context.Context, removed ...string) error

// One subset change is allowed for the whole bound merged handoff, not for
// each bracket or Git deletion. Retain the original identities on both sides
// of that change, including the keys that will never be queried again.
type releasedLocationSet struct {
	loaded  map[string]releaseLocation
	missing []releaseLocation
}

func (set *releasedLocationSet) observe(current []releaseLocation) (bool, error) {
	for _, location := range current {
		original, ok := set.loaded[location.Directory]
		if !ok {
			return false, fmt.Errorf("OpenCode checkout release: new or reappearing location cache key; refusing Git removal")
		}
		if !os.SameFile(original.info, location.info) {
			return false, fmt.Errorf("OpenCode checkout release: another loaded location changed identity; refusing Git removal")
		}
	}
	for _, location := range set.missing {
		if err := checkReleaseIdentity(location); err != nil {
			return false, err
		}
	}
	if len(current) == len(set.loaded) {
		return false, nil
	}
	if len(set.missing) != 0 {
		return false, fmt.Errorf("OpenCode checkout release: location inventory changed again after an unrelated disappearance; refusing Git removal")
	}
	kept := make(map[string]releaseLocation, len(current))
	for _, location := range current {
		kept[location.Directory] = set.loaded[location.Directory]
	}
	var missing []releaseLocation
	for key, location := range set.loaded {
		if _, ok := kept[key]; !ok {
			if err := checkReleaseIdentity(location); err != nil {
				return false, err
			}
			missing = append(missing, location)
		}
	}
	set.loaded, set.missing = kept, missing
	return true, nil
}

func recheckReleasedLocations(ctx context.Context, call releaseCall, targets []releaseLocation, removed []string,
	inventory func(releaseCall) ([]releaseLocation, bool, error), boundMerged bool,
) error {
	remaining := targets
	for _, path := range removed {
		if _, err := os.Lstat(path); !os.IsNotExist(err) {
			return fmt.Errorf("OpenCode checkout release: earlier checkout is no longer verifiably absent; refusing further removal")
		}
		var next []releaseLocation
		for _, target := range remaining {
			removedTarget, err := paths.Inside(path, target.Directory)
			if err != nil {
				return err
			}
			if removedTarget {
				removedTarget, err = paths.Inside(target.Directory, path)
				if err != nil {
					return err
				}
			}
			if !removedTarget {
				next = append(next, target)
			}
		}
		remaining = next
	}
	if len(remaining) == 0 && !boundMerged {
		return nil
	}
	cwds := make([]string, 0, len(remaining))
	for _, target := range remaining {
		cwds = append(cwds, target.Directory)
	}
	for {
		before, retry, err := inventory(call)
		if err != nil {
			return err
		}
		if retry {
			continue
		}
		// Ordinary work can use a target cwd from another cache key. Never
		// scope a read to an evicted target or repeat the history/terminal walk.
		if err := releaseOrdinaryProcesses(call, cwds, before); err != nil {
			return err
		}
		if boundMerged {
			if err := releaseRunningJobs(call, nil, true); err != nil {
				return err
			}
		}
		after, retry, err := inventory(call)
		if err != nil {
			return err
		}
		if retry {
			// The allowance was consumed by this observation. Start a new
			// bracket; no partial activity sample counts for the retry.
			continue
		}
		if !boundMerged {
			// Other callers retain the original, strict bracket comparison.
			seen := make(map[string]os.FileInfo, len(before))
			for _, location := range before {
				seen[location.Directory] = location.info
			}
			if len(before) != len(after) || slices.ContainsFunc(after, func(location releaseLocation) bool {
				return !os.SameFile(seen[location.Directory], location.info)
			}) {
				return fmt.Errorf("OpenCode checkout release: location inventory changed during the final shell/PTY check; refusing Git removal")
			}
		}
		return checkReleaseDeleteAccess(ctx, remaining)
	}
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
