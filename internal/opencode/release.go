package opencode

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/kninetimmy/orch/internal/execx"
	"github.com/kninetimmy/orch/internal/paths"
)

type releaseLocation struct {
	Directory string `json:"directory"`
	info      os.FileInfo
}

type releaseParameter struct {
	Name    string `json:"name"`
	In      string `json:"in"`
	Style   string `json:"style"`
	Explode bool   `json:"explode"`
}

// ReleaseLocation is called only after the run engine verifies disposable
// checkout ownership and removal prerequisites. These bounded observations are
// NOT atomic exclusion of host re-entry. No idle/completion hook calls this.
// All commands use the existing authenticated V2 CLI discovery, never a relay,
// service restart, session deletion or process termination.
func ReleaseLocation(ctx context.Context, runner execx.Runner, primary, target string) error {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	runtime, err := Resolve(ctx, runner, primary)
	if err != nil {
		return err
	}
	call := func(method, endpoint string, out any) error {
		res, err := runner.Run(ctx, execx.Cmd{
			Name: runtime.Executable, Args: []string{"api", method, endpoint}, Dir: primary,
		})
		// Never project raw host output, command bodies or subprocess errors.
		if err != nil || res.ExitCode != 0 {
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
	targetInfo, err := os.Stat(target)
	if err != nil || !targetInfo.IsDir() {
		return fmt.Errorf("OpenCode checkout release: target directory cannot be verified")
	}
	if same, err := sameDirectory(primary, target); err != nil || same {
		return fmt.Errorf("OpenCode checkout release: refusing primary or unverifiable directory")
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
		return err
	}
	evict := spec.Paths["/api/debug/location"]["delete"]
	if spec.Paths["/api/debug/location"]["get"].OperationID != "debug.location.list" || evict.OperationID != "debug.location.evict" ||
		!slices.ContainsFunc(evict.Parameters, func(p releaseParameter) bool {
			return p.Name == "location" && p.In == "query" && p.Style == "deepObject" && p.Explode
		}) {
		return fmt.Errorf("OpenCode checkout release: explicit scoped disposal contract is unsupported")
	}
	// Inventory reads do not activate an unloaded location. Every returned path
	// is filesystem-verified, including Windows extended-path aliases.
	inventory := func() ([]releaseLocation, bool, error) {
		var locations []releaseLocation
		if err := call("get", "/api/debug/location", &locations); err != nil {
			return nil, false, err
		}
		if locations == nil || len(locations) > 1024 {
			return nil, false, fmt.Errorf("OpenCode checkout release: malformed or oversized location inventory")
		}
		var seen []os.FileInfo
		loaded := false
		for i, location := range locations {
			info, err := releaseDirectory(location.Directory)
			if err != nil {
				return nil, false, err
			}
			if slices.ContainsFunc(seen, func(previous os.FileInfo) bool { return os.SameFile(previous, info) }) {
				return nil, false, fmt.Errorf("OpenCode checkout release: duplicate target or other location")
			}
			seen = append(seen, info)
			locations[i].info = info
			if os.SameFile(targetInfo, info) {
				loaded = true
				continue
			}
			inside, err := paths.Inside(target, location.Directory)
			if err != nil || inside {
				return nil, false, fmt.Errorf("OpenCode checkout release: unexpected loaded descendant or unverifiable location; refusing subtree eviction")
			}
		}
		return locations, loaded, nil
	}
	before, loaded, err := inventory()
	if err != nil {
		return err
	}
	if err := releaseActivity(call, target); err != nil {
		return err
	}
	query := url.Values{"location[directory]": {target}}.Encode()
	if loaded {
		var location struct {
			Directory string `json:"directory"`
			Project   struct {
				Directory string `json:"directory"`
				Canonical string `json:"canonical"`
			} `json:"project"`
		}
		if err := call("get", "/api/location?"+query, &location); err != nil {
			return err
		}
		for _, pair := range [][2]string{{target, location.Directory}, {target, location.Project.Directory}, {primary, location.Project.Canonical}} {
			if same, err := sameDirectory(pair[0], pair[1]); err != nil || !same {
				return fmt.Errorf("OpenCode checkout release: host location/project roots differ from the verified checkout")
			}
		}
	}
	// Re-read global activity and locations immediately before disposal. This
	// narrows the observation window; it still cannot lock out concurrent entry.
	if err := releaseActiveJobs(call, target); err != nil {
		return err
	}
	currentLocations, stillLoaded, err := inventory()
	if err != nil {
		return err
	}
	if !loaded && stillLoaded {
		return fmt.Errorf("OpenCode checkout release: target loaded during activity checks; preserve it and retry after use finishes")
	}
	if err := releaseOrdinaryProcesses(call, target, currentLocations); err != nil {
		return err
	}
	if stillLoaded {
		if err := call("delete", "/api/debug/location?"+query, nil); err != nil {
			return err
		}
	}
	after, remainsLoaded, err := inventory()
	if err != nil {
		return err
	}
	if remainsLoaded {
		return fmt.Errorf("OpenCode checkout release: scoped disposal did not unload the target; refusing Git removal")
	}
	for _, location := range before {
		if os.SameFile(targetInfo, location.info) {
			continue
		}
		if !slices.ContainsFunc(after, func(other releaseLocation) bool { return os.SameFile(location.info, other.info) }) {
			return fmt.Errorf("OpenCode checkout release: another loaded location disappeared; refusing Git removal")
		}
	}
	current, err := os.Stat(target)
	if err != nil || !os.SameFile(targetInfo, current) {
		return fmt.Errorf("OpenCode checkout release: target identity changed during disposal; refusing Git removal")
	}
	return nil
}

type releaseCall func(method, endpoint string, out any) error

// Both ordinary creation APIs accept cwd independently of location. Inspect
// every loaded owner, not just target, and attribute running activity by both
// owner and cwd. Persistent terminals have a separate session-scoped inventory.
// Only loaded locations are queried; an unloaded target is never activated.
func releaseOrdinaryProcesses(call releaseCall, target string, locations []releaseLocation) error {
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
			if err != nil || !os.SameFile(location.info, info) || response.Data == nil {
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
					belongs, err := releaseBelongs(target, directory)
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

func releaseBelongs(target, directory string) (bool, error) {
	info, err := releaseDirectory(directory)
	if err != nil {
		return false, err
	}
	targetInfo, err := os.Stat(target)
	if err != nil {
		return false, fmt.Errorf("OpenCode checkout release: target identity is unavailable")
	}
	inside, err := paths.Inside(target, directory)
	if err != nil {
		return false, fmt.Errorf("OpenCode checkout release: activity location cannot be verified")
	}
	return inside || os.SameFile(targetInfo, info), nil
}

func releaseActiveJobs(call releaseCall, target string) error {
	session := func(id string) error {
		if !strings.HasPrefix(id, "ses") {
			return fmt.Errorf("OpenCode checkout release: malformed activity session identifier")
		}
		var response struct {
			Data struct {
				ID       string          `json:"id"`
				Location releaseLocation `json:"location"`
			} `json:"data"`
		}
		if err := call("get", "/api/session/"+url.PathEscape(id), &response); err != nil {
			return err
		}
		if response.Data.ID != id {
			return fmt.Errorf("OpenCode checkout release: activity session identity mismatch")
		}
		belongs, err := releaseBelongs(target, response.Data.Location.Directory)
		if err != nil {
			return err
		}
		if belongs {
			return fmt.Errorf("OpenCode checkout release: active session or unfinished background job prevents removal")
		}
		return nil
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
		if err := session(id); err != nil {
			return err
		}
	}
	var jobs struct {
		Data []struct {
			ID             string `json:"id"`
			Kind           string `json:"kind"`
			Status         string `json:"status"`
			SessionID      string `json:"sessionID"`
			ChildSessionID string `json:"childSessionID"`
		} `json:"data"`
	}
	if err := call("get", "/api/experimental/job?status=running", &jobs); err != nil {
		return err
	}
	if jobs.Data == nil || len(jobs.Data) > 1000 {
		return fmt.Errorf("OpenCode checkout release: malformed or oversized background-job inventory")
	}
	for _, job := range jobs.Data {
		if job.ID == "" || job.Status != "running" || !slices.Contains([]string{"shell", "subagent", "image"}, job.Kind) {
			return fmt.Errorf("OpenCode checkout release: unsupported background-job evidence")
		}
		// The owner may be in primary while the actual subagent runs in target.
		// Both count: owner-side completion/report delivery can also re-enter.
		if err := session(job.SessionID); err != nil {
			return err
		}
		if job.Kind == "subagent" {
			if err := session(job.ChildSessionID); err != nil {
				return err
			}
		}
	}
	return nil
}

func releaseActivity(call releaseCall, target string) error {
	if err := releaseActiveJobs(call, target); err != nil {
		return err
	}
	// Persistent terminals can survive an idle session or a session move. Walk
	// the unfiltered, paginated session inventory, not just target's sessions.
	// ponytail: cap at 1000 sessions / two minutes; refuse larger inventories
	// until OpenCode exposes a complete global persistent-terminal inventory.
	seen := make(map[string]bool)
	cursor := ""
	for page := 0; page < 10; page++ {
		var response struct {
			Data []struct {
				ID       string          `json:"id"`
				Location releaseLocation `json:"location"`
			} `json:"data"`
			Cursor *struct {
				Next string `json:"next"`
			} `json:"cursor"`
		}
		query := url.Values{"limit": {"100"}}
		if cursor != "" {
			query.Set("cursor", cursor)
		}
		if err := call("get", "/api/session?"+query.Encode(), &response); err != nil {
			return err
		}
		if response.Data == nil || response.Cursor == nil || len(response.Data) > 100 {
			return fmt.Errorf("OpenCode checkout release: malformed session/terminal inventory")
		}
		for _, session := range response.Data {
			if !strings.HasPrefix(session.ID, "ses") || seen[session.ID] || !filepath.IsAbs(session.Location.Directory) {
				return fmt.Errorf("OpenCode checkout release: malformed or repeated session inventory")
			}
			seen[session.ID] = true
			var terminals struct {
				Data []struct {
					ID        string `json:"id"`
					SessionID string `json:"sessionID"`
					Cwd       string `json:"cwd"`
					Status    string `json:"status"`
				} `json:"data"`
			}
			if err := call("get", "/api/experimental/session/"+url.PathEscape(session.ID)+"/terminal", &terminals); err != nil {
				return err
			}
			if terminals.Data == nil || len(terminals.Data) > 1000 {
				return fmt.Errorf("OpenCode checkout release: malformed persistent-terminal inventory")
			}
			for _, terminal := range terminals.Data {
				if terminal.ID == "" || terminal.SessionID != session.ID || !slices.Contains([]string{"running", "exited"}, terminal.Status) || !filepath.IsAbs(terminal.Cwd) {
					return fmt.Errorf("OpenCode checkout release: unsupported persistent-terminal evidence")
				}
				if terminal.Status == "exited" {
					continue
				}
				for _, directory := range []string{session.Location.Directory, terminal.Cwd} {
					belongs, err := releaseBelongs(target, directory)
					if err != nil {
						return err
					}
					if belongs {
						return fmt.Errorf("OpenCode checkout release: running persistent terminal prevents removal")
					}
				}
			}
		}
		if response.Cursor.Next == "" {
			return nil
		}
		if response.Cursor.Next == cursor || len(response.Data) == 0 {
			return fmt.Errorf("OpenCode checkout release: session pagination did not advance")
		}
		cursor = response.Cursor.Next
	}
	return fmt.Errorf("OpenCode checkout release: session inventory exceeds the bounded activity check")
}
