package opencode

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"

	"github.com/kninetimmy/orch/internal/execx"
	"github.com/kninetimmy/orch/internal/paths"
)

// CheckPlugin requires a live server component in the intended project scope.
func (runtime Runtime) CheckPlugin(ctx context.Context, runner execx.Runner, directory, id string) error {
	if runner == nil {
		return fmt.Errorf("read OpenCode plugin catalog: command runner is unavailable; retry through an initialized Orch command")
	}
	root, err := paths.Canonical(directory)
	if err != nil {
		return fmt.Errorf("read OpenCode plugin catalog: repository location cannot be verified")
	}
	query := url.Values{"location[directory]": {root}}
	res, err := runner.Run(ctx, execx.Cmd{Name: runtime.Executable, Args: []string{"api", "get", "/api/plugin?" + query.Encode()}, Dir: directory})
	if err != nil {
		return fmt.Errorf("read OpenCode plugin catalog: `%s api get /api/plugin` could not start; check the V2 service and retry", runtime.Executable)
	}
	if res.ExitCode != 0 {
		return fmt.Errorf("read OpenCode plugin catalog: `%s api get /api/plugin` exited %d; check the V2 service and retry", runtime.Executable, res.ExitCode)
	}
	malformed := func(detail string) error {
		return fmt.Errorf("malformed `%s api get /api/plugin` output: %s", runtime.Executable, detail)
	}
	var response struct {
		Location struct {
			Directory string `json:"directory"`
		} `json:"location"`
		Data []struct {
			ID       string `json:"id"`
			Features struct {
				Server *bool `json:"server"`
			} `json:"features"`
			State struct {
				Status string `json:"status"`
			} `json:"state"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(res.Stdout), &response); err != nil || response.Data == nil {
		return malformed("expected an object with a data array")
	}
	if response.Location.Directory == "" {
		return malformed("location.directory is missing")
	}
	same, err := sameDirectory(root, response.Location.Directory)
	if err != nil {
		return malformed("location.directory cannot be verified")
	}
	if !same {
		return fmt.Errorf("OpenCode plugin catalog is scoped to a different directory; restart the V2 service and retry")
	}
	var matches []int
	for i, plugin := range response.Data {
		if plugin.ID == id {
			matches = append(matches, i)
		}
	}
	if len(matches) == 0 {
		if len(response.Data) == 0 {
			return fmt.Errorf("OpenCode plugin catalog is not ready or is empty; wait for startup, verify the configured plugin directory, then retry")
		}
		return fmt.Errorf("%s is absent; verify the configured plugin directory and retry", id)
	}
	if len(matches) != 1 {
		return fmt.Errorf("%s appears %d times; exactly one active plugin is required", id, len(matches))
	}
	plugin := response.Data[matches[0]]
	switch plugin.State.Status {
	case "active":
	case "failed":
		return fmt.Errorf("%s failed to load; check the V2 service plugin diagnostics and retry", id)
	case "":
		return malformed(id + " has no state.status")
	default:
		return fmt.Errorf("%s is not ready (state is not active); wait for startup and retry", id)
	}
	if plugin.Features.Server == nil || !*plugin.Features.Server {
		return fmt.Errorf("%s has no active server component", id)
	}
	return nil
}
