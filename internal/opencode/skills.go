package opencode

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"

	"github.com/kninetimmy/orch/internal/execx"
	"github.com/kninetimmy/orch/internal/paths"
)

// LoadedSkill is the safe subset of one live OpenCode skill. Its source
// location is deliberately not retained: compatibility depends on content, not
// where OpenCode loaded it from.
type LoadedSkill struct {
	ID      string
	Content string
}

// SkillCatalog is OpenCode's winning live skill set for one repository.
type SkillCatalog struct {
	Skills []LoadedSkill
}

// ReadSkillCatalog returns the repository-scoped live OpenCode skill catalog.
// It verifies that OpenCode answered for directory and never includes command
// output or loaded skill content in an error.
func ReadSkillCatalog(ctx context.Context, runner execx.Runner, directory string) (SkillCatalog, error) {
	if runner == nil {
		return SkillCatalog{}, fmt.Errorf("read OpenCode skill catalog: command runner is unavailable; retry through an initialized Orch command")
	}
	root, err := paths.Canonical(directory)
	if err != nil {
		return SkillCatalog{}, fmt.Errorf("read OpenCode skill catalog: repository location cannot be verified")
	}
	query := url.Values{"location[directory]": {root}}
	res, err := runner.Run(ctx, execx.Cmd{
		Name: Executable,
		Args: []string{"api", "get", "/api/skill?" + query.Encode()},
		Dir:  directory,
	})
	if err != nil {
		return SkillCatalog{}, fmt.Errorf("read OpenCode skill catalog: `%s api get /api/skill` could not start; check `%s service status` and retry", Executable, Executable)
	}
	if res.ExitCode != 0 {
		return SkillCatalog{}, fmt.Errorf("read OpenCode skill catalog: `%s api get /api/skill` exited %d; check `%s service status`, restart the service if needed, and retry", Executable, res.ExitCode, Executable)
	}

	var response skillCatalogResponse
	if err := json.Unmarshal([]byte(res.Stdout), &response); err != nil {
		return SkillCatalog{}, malformedSkills("response is not valid JSON")
	}
	if response.Location.Directory == "" {
		return SkillCatalog{}, malformedSkills("location.directory is missing")
	}
	same, err := sameDirectory(root, response.Location.Directory)
	if err != nil {
		return SkillCatalog{}, malformedSkills("location.directory cannot be verified")
	}
	if !same {
		return SkillCatalog{}, fmt.Errorf("read OpenCode skill catalog: response is scoped to a different directory; restart the OpenCode V2 service and retry")
	}
	if response.Data == nil {
		return SkillCatalog{}, malformedSkills("data is not an array")
	}

	catalog := SkillCatalog{Skills: make([]LoadedSkill, len(response.Data))}
	seen := make(map[string]struct{}, len(response.Data))
	for i, skill := range response.Data {
		if skill.ID == "" {
			return SkillCatalog{}, malformedSkills(fmt.Sprintf("data[%d].id is missing", i))
		}
		if skill.Content == nil {
			return SkillCatalog{}, malformedSkills(fmt.Sprintf("data[%d].content is missing", i))
		}
		if _, ok := seen[skill.ID]; ok {
			return SkillCatalog{}, malformedSkills(fmt.Sprintf("data[%d].id is duplicated", i))
		}
		seen[skill.ID] = struct{}{}
		catalog.Skills[i] = LoadedSkill{ID: skill.ID, Content: *skill.Content}
	}
	return catalog, nil
}

type skillCatalogResponse struct {
	Location struct {
		Directory string `json:"directory"`
	} `json:"location"`
	Data []struct {
		ID      string  `json:"id"`
		Content *string `json:"content"`
	} `json:"data"`
}

func malformedSkills(detail string) error {
	return fmt.Errorf("read OpenCode skill catalog: malformed `%s api get /api/skill` response (%s); %s or newer must preserve this catalog contract, so update Orch or OpenCode and retry", Executable, detail, MinimumVersion)
}
