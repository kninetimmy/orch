// Package opencode reads the project-scoped OpenCode V2 model catalog.
package opencode

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"slices"

	"github.com/kninetimmy/orch/internal/execx"
	"github.com/kninetimmy/orch/internal/paths"
)

const (
	Executable           = "opencode2"
	RuntimeVersionPrefix = Executable + " v0.0.0-beta-"
	MinimumBetaBuild     = 18314
	MinimumVersion       = RuntimeVersionPrefix + "18314"
)

// Model is the safe subset of an available OpenCode model. Provider settings,
// headers, credentials, account identifiers, and request bodies are never
// retained.
type Model struct {
	ID       string
	Variants []string
}

// Catalog is the currently enabled, agent-capable model catalog for one
// repository.
type Catalog struct {
	Models []Model
}

// ReadCatalog returns enabled models that accept text, emit text, and support
// tools. It verifies that OpenCode answered for directory and never includes
// raw command output in an error.
func ReadCatalog(ctx context.Context, runner execx.Runner, directory string) (Catalog, error) {
	if runner == nil {
		return Catalog{}, fmt.Errorf("read OpenCode model catalog: command runner is unavailable; retry through an initialized Orch command")
	}
	root, err := paths.Canonical(directory)
	if err != nil {
		return Catalog{}, fmt.Errorf("read OpenCode model catalog: repository location cannot be verified")
	}
	query := url.Values{"location[directory]": {root}}
	res, err := runner.Run(ctx, execx.Cmd{
		Name: Executable,
		Args: []string{"api", "get", "/api/model?" + query.Encode()},
		Dir:  directory,
	})
	if err != nil {
		return Catalog{}, fmt.Errorf("read OpenCode model catalog: `%s api get /api/model` could not start; check `%s service status` and retry", Executable, Executable)
	}
	if res.ExitCode != 0 {
		return Catalog{}, fmt.Errorf("read OpenCode model catalog: `%s api get /api/model` exited %d; check `%s service status`, restart the service if needed, and retry", Executable, res.ExitCode, Executable)
	}

	var response catalogResponse
	if err := json.Unmarshal([]byte(res.Stdout), &response); err != nil {
		return Catalog{}, malformed("response is not valid JSON")
	}
	if response.Location.Directory == "" {
		return Catalog{}, malformed("location.directory is missing")
	}
	same, err := sameDirectory(root, response.Location.Directory)
	if err != nil {
		return Catalog{}, malformed("location.directory cannot be verified")
	}
	if !same {
		return Catalog{}, fmt.Errorf("read OpenCode model catalog: response is scoped to a different directory; restart the OpenCode V2 service and retry")
	}
	if response.Data == nil {
		return Catalog{}, malformed("data is not an array")
	}

	catalog := Catalog{Models: make([]Model, 0, len(response.Data))}
	for i, model := range response.Data {
		if model.ID == "" || model.ProviderID == "" {
			return Catalog{}, malformed(fmt.Sprintf("data[%d] has no provider/model identifier", i))
		}
		if model.Enabled == nil {
			return Catalog{}, malformed(fmt.Sprintf("data[%d].enabled is missing", i))
		}
		if model.Capabilities.Tools == nil || model.Capabilities.Input == nil || model.Capabilities.Output == nil {
			return Catalog{}, malformed(fmt.Sprintf("data[%d].capabilities is incomplete", i))
		}
		if model.Variants == nil {
			return Catalog{}, malformed(fmt.Sprintf("data[%d].variants is not an array", i))
		}
		variants := make([]string, len(model.Variants))
		for j, variant := range model.Variants {
			if variant.ID == "" {
				return Catalog{}, malformed(fmt.Sprintf("data[%d].variants[%d].id is missing", i, j))
			}
			variants[j] = variant.ID
		}
		if *model.Enabled && *model.Capabilities.Tools && slices.Contains(model.Capabilities.Input, "text") && slices.Contains(model.Capabilities.Output, "text") {
			catalog.Models = append(catalog.Models, Model{ID: model.ProviderID + "/" + model.ID, Variants: variants})
		}
	}
	return catalog, nil
}

type catalogResponse struct {
	Location struct {
		Directory string `json:"directory"`
	} `json:"location"`
	Data []struct {
		ID           string `json:"id"`
		ProviderID   string `json:"providerID"`
		Enabled      *bool  `json:"enabled"`
		Capabilities struct {
			Tools  *bool    `json:"tools"`
			Input  []string `json:"input"`
			Output []string `json:"output"`
		} `json:"capabilities"`
		Variants []struct {
			ID string `json:"id"`
		} `json:"variants"`
	} `json:"data"`
}

func sameDirectory(want, got string) (bool, error) {
	wantInfo, err := os.Stat(want)
	if err != nil {
		return false, err
	}
	gotInfo, err := os.Stat(got)
	if err != nil {
		return false, err
	}
	if !wantInfo.IsDir() || !gotInfo.IsDir() {
		return false, fmt.Errorf("catalog locations must be directories")
	}
	return os.SameFile(wantInfo, gotInfo), nil
}

func malformed(detail string) error {
	return fmt.Errorf("read OpenCode model catalog: malformed `%s api get /api/model` response (%s); %s or newer must preserve this catalog contract, so update Orch or OpenCode and retry", Executable, detail, MinimumVersion)
}
