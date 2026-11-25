package opencode

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"time"

	"github.com/kninetimmy/orch/internal/execx"
	"github.com/kninetimmy/orch/internal/paths"
)

type releaseLocation struct {
	// Directory is the host's cache key, not a canonical filesystem identity.
	// Distinct keys may own independent services for the same physical directory.
	Directory string `json:"directory"`
	info      os.FileInfo
}

type releaseParameter struct {
	Name    string `json:"name"`
	In      string `json:"in"`
	Style   string `json:"style"`
	Explode bool   `json:"explode"`
}

// ReleaseLocation retains the single-checkout handoff for review replacement.
func ReleaseLocation(ctx context.Context, runner execx.Runner, primary, target string) error {
	_, err := ReleaseLocations(ctx, runner, primary, nil, false, target)
	return err
}

// ReleaseLocations is called only after the run engine verifies disposable
// checkout ownership and removal prerequisites. These bounded observations are
// NOT atomic exclusion of host re-entry. No idle/completion hook calls this.
// All commands use the existing authenticated V2 CLI discovery, never a relay,
// service restart, session deletion or process termination.
// On Windows the runner must support concurrent persistent-terminal reads.
// Before #525 sequential handoffs could reload an earlier target through later
// session/terminal probes. Now probes that could load a target precede eviction.
// Bound cleanup supplies verify to recheck engine ownership before disposal
// and to keep retry advice on the bound path.
// Cleanup can use the returned check at later Git boundaries, under the same
// deadline. checkJobs also needs a late check on a ref-only retry, where
// there is no checkout to release or scan. Review replacement needs only this
// release.
func ReleaseLocations(ctx context.Context, runner execx.Runner, primary string, verify func(context.Context) error, checkJobs bool, targets ...string) (ReleaseCheck, error) {
	if len(targets) == 0 && !checkJobs {
		return nil, nil
	}
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	host, err := Resolve(ctx, runner, primary)
	if err != nil {
		return nil, err
	}
	request := func(ctx context.Context, method, endpoint string, out any) error {
		if err := ctx.Err(); err != nil {
			return releaseContextError(err)
		}
		res, err := runner.Run(ctx, execx.Cmd{
			Name: host.Executable, Args: []string{"api", method, endpoint}, Dir: primary,
		})
		// Never project raw host output, command bodies or subprocess errors.
		if runtime.GOOS == "windows" {
			if err := ctx.Err(); err != nil {
				return releaseContextError(err)
			}
			if errors.Is(err, context.DeadlineExceeded) {
				return releaseContextError(context.DeadlineExceeded)
			}
		}
		if err != nil || res.ExitCode != 0 || ctx.Err() != nil {
			return fmt.Errorf("OpenCode checkout release: host %s request unavailable or unsupported; preserve the checkout and inspect the host before retrying", method)
		}
		if out == nil {
			if strings.TrimSpace(res.Stdout) != "" {
				return fmt.Errorf("OpenCode checkout release: scoped disposal returned unexpected content")
			}
		} else if err := json.Unmarshal([]byte(res.Stdout), out); err != nil {
			return fmt.Errorf("OpenCode checkout release: malformed host response")
		}
		return nil
	}
	call := func(method, endpoint string, out any) error {
		return request(ctx, method, endpoint, out)
	}
	deadline, _ := ctx.Deadline()
	if len(targets) == 0 {
		return func(ctx context.Context, _ string) error {
			ctx, cancel := context.WithDeadline(ctx, deadline)
			defer cancel()
			return releaseRunningJobs(func(method, endpoint string, out any) error {
				return request(ctx, method, endpoint, out)
			}, nil, true)
		}, nil
	}
	targetInfos := make([]releaseLocation, 0, len(targets))
	for _, target := range targets {
		info, err := os.Stat(target)
		if err != nil || !info.IsDir() {
			return nil, fmt.Errorf("OpenCode checkout release: target directory cannot be verified")
		}
		if same, err := sameDirectory(primary, target); err != nil || same {
			return nil, fmt.Errorf("OpenCode checkout release: refusing primary or unverifiable directory")
		}
		if slices.ContainsFunc(targetInfos, func(other releaseLocation) bool { return os.SameFile(other.info, info) }) {
			return nil, fmt.Errorf("OpenCode checkout release: duplicate target directory")
		}
		targetInfos = append(targetInfos, releaseLocation{Directory: target, info: info})
	}
	targetIndex := func(info os.FileInfo) int {
		return slices.IndexFunc(targetInfos, func(target releaseLocation) bool { return os.SameFile(target.info, info) })
	}
	// Checking the published operation and explicit deep-object parameter avoids
	// treating a missing/changed debug route as permission for broader teardown.
	var spec struct {
		Paths map[string]map[string]struct {
			OperationID string             `json:"operationId"`
			Parameters  []releaseParameter `json:"parameters"`
		} `json:"paths"`
	}
	if err := call("get", "/openapi.json", &spec); err != nil {
		return nil, err
	}
	evict := spec.Paths["/api/debug/location"]["delete"]
	if spec.Paths["/api/debug/location"]["get"].OperationID != "debug.location.list" || evict.OperationID != "debug.location.evict" ||
		!slices.ContainsFunc(evict.Parameters, func(p releaseParameter) bool {
			return p.Name == "location" && p.In == "query" && p.Style == "deepObject" && p.Explode
		}) {
		return nil, fmt.Errorf("OpenCode checkout release: explicit scoped disposal contract is unsupported")
	}
	// Inventory reads do not activate an unloaded location. Keep every exact
	// host key and independently verify its directory, even for physical aliases.
	inventory := func(call releaseCall) ([]releaseLocation, []string, error) {
		var locations []releaseLocation
		if err := call("get", "/api/debug/location", &locations); err != nil {
			return nil, nil, err
		}
		if locations == nil || len(locations) > 1024 {
			return nil, nil, fmt.Errorf("OpenCode checkout release: malformed or oversized location inventory")
		}
		seen := make(map[string]bool)
		loaded := make([]string, len(targets))
		for i, location := range locations {
			info, err := releaseDirectory(location.Directory)
			if err != nil {
				return nil, nil, err
			}
			if seen[location.Directory] {
				return nil, nil, fmt.Errorf("OpenCode checkout release: duplicate location cache key")
			}
			seen[location.Directory] = true
			locations[i].info = info
			if index := targetIndex(info); index >= 0 {
				if loaded[index] != "" {
					return nil, nil, fmt.Errorf("OpenCode checkout release: ambiguous target cache aliases; refusing disposal")
				}
				loaded[index] = location.Directory
				continue
			}
			for _, target := range targets {
				inside, err := paths.Inside(target, location.Directory)
				if err != nil || inside {
					return nil, nil, fmt.Errorf("OpenCode checkout release: unexpected loaded descendant or unverifiable location; refusing subtree eviction")
				}
			}
		}
		return locations, loaded, nil
	}
	preserve := func(previous, current []releaseLocation, allowMissing bool) (*releaseLocation, error) {
		var missing *releaseLocation
		for i, location := range previous {
			if targetIndex(location.info) >= 0 {
				continue
			}
			index := slices.IndexFunc(current, func(other releaseLocation) bool {
				return location.Directory == other.Directory
			})
			if index >= 0 && os.SameFile(location.info, current[index].info) {
				continue
			}
			if !allowMissing || index >= 0 || missing != nil {
				return nil, fmt.Errorf("OpenCode checkout release: another loaded location disappeared or changed identity; refusing Git removal")
			}
			missing = &previous[i]
		}
		if missing != nil && len(current) != len(previous)-1 {
			return nil, fmt.Errorf("OpenCode checkout release: location inventory changed beyond one unrelated disappearance; refusing Git removal")
		}
		return missing, nil
	}
	before, loaded, err := inventory(call)
	if err != nil {
		return nil, err
	}
	var evicted *releaseLocation
	for recheck := false; ; recheck = true {
		for _, target := range targetInfos {
			if err := checkReleaseIdentity(target); err != nil {
				return nil, err
			}
		}
		if err := releaseActiveJobs(call, targets, verify != nil); err != nil {
			return nil, err
		}
		if err := releaseActivity(ctx, request, targets); err != nil {
			return nil, err
		}
		for i, key := range loaded {
			if key == "" {
				continue
			}
			query := url.Values{"location[directory]": {key}}.Encode()
			var location struct {
				Directory string `json:"directory"`
				Project   struct {
					Directory string `json:"directory"`
					Canonical string `json:"canonical"`
				} `json:"project"`
			}
			if err := call("get", "/api/location?"+query, &location); err != nil {
				return nil, err
			}
			if location.Directory != key {
				return nil, fmt.Errorf("OpenCode checkout release: host location cache key differs from the queried target")
			}
			for _, pair := range [][2]string{{targets[i], location.Directory}, {targets[i], location.Project.Directory}, {primary, location.Project.Canonical}} {
				if same, err := sameDirectory(pair[0], pair[1]); err != nil || !same {
					return nil, fmt.Errorf("OpenCode checkout release: host location/project roots differ from the verified checkout")
				}
			}
		}
		// Re-read global activity and locations immediately before disposal. This
		// narrows the observation window; it still cannot lock out concurrent entry.
		if err := releaseActiveJobs(call, targets, verify != nil); err != nil {
			return nil, err
		}
		currentLocations, stillLoaded, err := inventory(call)
		if err != nil {
			return nil, err
		}
		for i, key := range stillLoaded {
			if key != "" && loaded[i] != key {
				return nil, fmt.Errorf("OpenCode checkout release: target cache key loaded or changed during activity checks; preserve it and retry after use finishes")
			}
		}
		missing, err := preserve(before, currentLocations, !recheck)
		if err != nil {
			return nil, err
		}
		if (missing != nil || recheck) && !slices.Equal(stillLoaded, loaded) || recheck && len(currentLocations) != len(before) {
			return nil, fmt.Errorf("OpenCode checkout release: location inventory changed again or target changed; refusing Git removal")
		}
		if missing != nil {
			// One unrelated idle-cache eviction permits a fresh read-only preflight,
			// not an idle inference or a scoped read that could reload the lost key.
			if err := checkReleaseIdentity(*missing); err != nil {
				return nil, err
			}
			evicted, before = missing, currentLocations
			continue
		}
		if err := releaseOrdinaryProcesses(call, targets, currentLocations); err != nil {
			return nil, err
		}
		for _, target := range targetInfos {
			if err := checkReleaseIdentity(target); err != nil {
				return nil, err
			}
		}
		if evicted != nil {
			if err := checkReleaseIdentity(*evicted); err != nil {
				return nil, err
			}
		}
		before = currentLocations
		loaded = stillLoaded
		break
	}
	if verify != nil {
		if err := verify(ctx); err != nil {
			return nil, err
		}
		checkInventory := func() ([]releaseLocation, error) {
			current, currentKeys, err := inventory(call)
			if err != nil {
				return nil, err
			}
			if !slices.Equal(loaded, currentKeys) || len(current) != len(before) {
				return nil, fmt.Errorf("OpenCode checkout release: inventory changed before disposal; preserve the checkouts")
			}
			_, err = preserve(before, current, false)
			return current, err
		}
		// A primary-owned shell or PTY can start in a target cwd during
		// ownership checks without appearing in session/job inventories.
		// Probe only verified loaded keys and finish with a non-loading read.
		current, err := checkInventory()
		if err != nil {
			return nil, err
		}
		if err := releaseOrdinaryProcesses(call, targets, current); err != nil {
			return nil, err
		}
		if err := releaseActiveJobs(call, targets, true); err != nil {
			return nil, err
		}
		if _, err := checkInventory(); err != nil {
			return nil, err
		}
		for _, target := range targetInfos {
			if err := checkReleaseIdentity(target); err != nil {
				return nil, err
			}
		}
	}
	// Per-session/terminal and target-scoped reads can reload a checkout. After
	// eviction, use only global observations and ordinary reads of surviving keys.
	for _, key := range loaded {
		if key != "" {
			query := url.Values{"location[directory]": {key}}.Encode()
			if err := call("delete", "/api/debug/location?"+query, nil); err != nil {
				return nil, err
			}
		}
	}
	releasedInventory := func(call releaseCall) ([]releaseLocation, error) {
		current, keys, err := inventory(call)
		if err != nil {
			return nil, err
		}
		if slices.ContainsFunc(keys, func(key string) bool { return key != "" }) {
			return nil, fmt.Errorf("OpenCode checkout release: scoped disposal did not unload a target or a target reactivated; refusing Git removal")
		}
		if _, err := preserve(before, current, false); err != nil {
			return nil, err
		}
		return current, nil
	}
	if _, err := releasedInventory(call); err != nil {
		return nil, err
	}
	if err := checkReleaseDeleteAccess(ctx, targetInfos); err != nil {
		return nil, err
	}
	return func(ctx context.Context, removed string) error {
		ctx, cancel := context.WithDeadline(ctx, deadline)
		defer cancel()
		return recheckReleasedLocations(ctx, func(method, endpoint string, out any) error {
			return request(ctx, method, endpoint, out)
		}, targetInfos, removed, releasedInventory, checkJobs)
	}, nil
}

func releaseContextError(err error) error {
	if runtime.GOOS == "windows" {
		return fmt.Errorf("OpenCode checkout release: preserve the checkout: %w", err)
	}
	return fmt.Errorf("OpenCode checkout release: release deadline exhausted or canceled; preserve the checkout")
}

type releaseCall func(method, endpoint string, out any) error

// Both ordinary creation APIs accept cwd independently of location. Inspect
// every loaded cache key, not just target or one key per physical directory,
// and attribute running activity by both owner and cwd. Persistent terminals
// have a separate session-scoped inventory.
// Only loaded locations are queried; an unloaded target is never activated.
func releaseOrdinaryProcesses(call releaseCall, targets []string, locations []releaseLocation) error {
	for _, location := range locations {
		query := url.Values{"location[directory]": {location.Directory}}.Encode()
		for _, endpoint := range []string{"/api/shell", "/api/pty"} {
			var response struct {
				Location releaseLocation `json:"location"`
				Data     []struct {
					ID     string `json:"id"`
					Status string `json:"status"`
					Cwd    string `json:"cwd"`
				} `json:"data"`
			}
			if err := call("get", endpoint+"?"+query, &response); err != nil {
				return err
			}
			info, err := releaseDirectory(response.Location.Directory)
			if err != nil || response.Location.Directory != location.Directory || !os.SameFile(location.info, info) || response.Data == nil {
				return fmt.Errorf("OpenCode checkout release: malformed or wrong-root shell/PTY inventory")
			}
			if len(response.Data) > 1000 {
				return fmt.Errorf("OpenCode checkout release: oversized shell/PTY inventory")
			}
			for _, process := range response.Data {
				if process.ID == "" || !filepath.IsAbs(process.Cwd) ||
					(process.Status != "running" && process.Status != "exited") || (endpoint == "/api/shell" && process.Status != "running") {
					return fmt.Errorf("OpenCode checkout release: malformed shell/PTY activity")
				}
				if process.Status == "exited" {
					continue
				}
				// Verify cwd even when ownership alone would prohibit removal.
				// An unsupported/unverifiable attribution is never idle evidence.
				for _, directory := range []string{process.Cwd, location.Directory} {
					belongs, err := releaseBelongs(targets, directory)
					if err != nil {
						return err
					}
					if belongs {
						return fmt.Errorf("OpenCode checkout release: running shell or terminal prevents removal")
					}
				}
			}
		}
	}
	return nil
}

func releaseDirectory(directory string) (os.FileInfo, error) {
	if !filepath.IsAbs(directory) {
		return nil, fmt.Errorf("OpenCode checkout release: host directory is not absolute")
	}
	if _, err := paths.Canonical(directory); err != nil {
		return nil, fmt.Errorf("OpenCode checkout release: host directory cannot be canonicalized")
	}
	info, err := os.Stat(directory)
	if err != nil || !info.IsDir() {
		return nil, fmt.Errorf("OpenCode checkout release: host directory identity is unavailable")
	}
	return info, nil
}

func releaseBelongs(targets []string, directory string) (bool, error) {
	info, err := releaseDirectory(directory)
	if err != nil {
		return false, err
	}
	for _, target := range targets {
		targetInfo, err := os.Stat(target)
		if err != nil {
			return false, fmt.Errorf("OpenCode checkout release: target identity is unavailable")
		}
		inside, err := paths.Inside(target, directory)
		if err != nil {
			return false, fmt.Errorf("OpenCode checkout release: activity location cannot be verified")
		}
		if inside || os.SameFile(targetInfo, info) {
			return true, nil
		}
	}
	return false, nil
}

const runningJobsEndpoint = "/api/experimental/job?status=running"

func releaseActiveJobs(call releaseCall, targets []string, bound bool) error {
	sessionInTarget := func(id string) (bool, error) {
		if !strings.HasPrefix(id, "ses") {
			return false, fmt.Errorf("OpenCode checkout release: malformed activity session identifier")
		}
		var response struct {
			Data struct {
				ID       string          `json:"id"`
				Location releaseLocation `json:"location"`
			} `json:"data"`
		}
		if err := call("get", "/api/session/"+url.PathEscape(id), &response); err != nil {
			return false, err
		}
		if response.Data.ID != id {
			return false, fmt.Errorf("OpenCode checkout release: activity session identity mismatch")
		}
		return releaseBelongs(targets, response.Data.Location.Directory)
	}
	var active struct {
		Data map[string]struct {
			Type string `json:"type"`
		} `json:"data"`
	}
	if err := call("get", "/api/session/active", &active); err != nil {
		return err
	}
	if active.Data == nil || len(active.Data) > 1000 {
		return fmt.Errorf("OpenCode checkout release: malformed or oversized active-session inventory")
	}
	ids := make([]string, 0, len(active.Data))
	for id, value := range active.Data {
		if value.Type != "running" {
			return fmt.Errorf("OpenCode checkout release: unsupported session activity state")
		}
		ids = append(ids, id)
	}
	slices.Sort(ids)
	for _, id := range ids {
		if err := checkReleaseSession(sessionInTarget, id); err != nil {
			return err
		}
	}
	return releaseRunningJobs(call, sessionInTarget, bound)
}

func checkReleaseSession(sessionInTarget func(string) (bool, error), id string) error {
	belongs, err := sessionInTarget(id)
	if err != nil {
		return err
	}
	if belongs {
		return fmt.Errorf("OpenCode checkout release: active session or unfinished background job prevents removal")
	}
	return nil
}

// Late and ref-only checks have no attribution function: job data is global,
// but resolving a session can load its checkout. Do not issue those reads then.
func releaseRunningJobs(call releaseCall, sessionInTarget func(string) (bool, error), bound bool) error {
	var jobs struct {
		Data []struct {
			ID             string `json:"id"`
			Kind           string `json:"kind"`
			Status         string `json:"status"`
			SessionID      string `json:"sessionID"`
			ChildSessionID string `json:"childSessionID"`
		} `json:"data"`
	}
	if err := call("get", runningJobsEndpoint, &jobs); err != nil {
		return err
	}
	if jobs.Data == nil || len(jobs.Data) > 1000 {
		return fmt.Errorf("OpenCode checkout release: malformed or oversized background-job inventory")
	}
	for _, job := range jobs.Data {
		if job.ID == "" || job.Status != "running" || job.Kind == "" {
			return fmt.Errorf("OpenCode checkout release: malformed background-job evidence")
		}
		switch job.Kind {
		case "codemode":
			if sessionInTarget == nil && !strings.HasPrefix(job.SessionID, "ses") {
				return fmt.Errorf("OpenCode checkout release: malformed activity session identifier")
			}
			// The inventory has no nested tool destinations. Verifying an
			// outside owner does not establish where the job's tools work.
			reason := "its working directories cannot be verified"
			if sessionInTarget != nil {
				belongs, err := sessionInTarget(job.SessionID)
				if err != nil {
					return err
				}
				if belongs {
					reason = "its owning session is in a target checkout"
				}
			}
			if bound {
				return fmt.Errorf("OpenCode checkout release: running Code Mode job prevents removal: %s; "+
					"let the job finish, re-confirm the run and issue, then retry `orch_cleanup` directly outside Code Mode from the verified primary checkout", reason)
			}
			return fmt.Errorf("OpenCode checkout release: running Code Mode job prevents removal: %s; "+
				"let the job finish, then rerun the normal command (for cleanup, `orch run cleanup`) "+
				"from a standalone foreground terminal at the verified primary checkout when OpenCode is idle", reason)
		case "shell", "image", "subagent":
			if sessionInTarget == nil {
				// This late check adds the global Code Mode hold, not a second
				// owner attribution pass for other known job kinds.
				if !strings.HasPrefix(job.SessionID, "ses") || job.Kind == "subagent" && !strings.HasPrefix(job.ChildSessionID, "ses") {
					return fmt.Errorf("OpenCode checkout release: malformed activity session identifier")
				}
				continue
			}
			// The owner may be in primary while the actual subagent runs in target.
			// Both count: owner-side completion/report delivery can also re-enter.
			if err := checkReleaseSession(sessionInTarget, job.SessionID); err != nil {
				return err
			}
			if job.Kind == "subagent" {
				if err := checkReleaseSession(sessionInTarget, job.ChildSessionID); err != nil {
					return err
				}
			}
		default:
			return fmt.Errorf("OpenCode checkout release: unsupported background-job evidence")
		}
	}
	return nil
}
