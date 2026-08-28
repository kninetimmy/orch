package opencode

import (
	"context"
	"fmt"
	"net/url"
	"strings"
	"testing"

	"github.com/kninetimmy/orch/internal/execx"
	"github.com/kninetimmy/orch/internal/paths"
)

func TestReadSkillCatalogRetainsOnlyIDAndContent(t *testing.T) {
	root, err := paths.Canonical(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	wantEndpoint := "/api/skill?" + url.Values{"location[directory]": {root}}.Encode()
	response := fmt.Sprintf(`{"location":{"directory":%q},"data":[{"id":"orch-setup","location":"C:/custom/SKILL.md","content":"compatible custom prose"}]}`, root)
	runner := catalogRunner(func(cmd execx.Cmd) (execx.Result, error) {
		if cmd.Name != Executable || len(cmd.Args) != 3 || cmd.Args[0] != "api" || cmd.Args[1] != "get" || cmd.Args[2] != wantEndpoint {
			t.Fatalf("command = %s %q, want %s api get %q", cmd.Name, cmd.Args, Executable, wantEndpoint)
		}
		if cmd.Dir != root {
			t.Fatalf("command dir = %q, want %q", cmd.Dir, root)
		}
		return execx.Result{Stdout: response}, nil
	})

	catalog, err := ReadSkillCatalog(context.Background(), runner, root)
	if err != nil {
		t.Fatal(err)
	}
	if len(catalog.Skills) != 1 || catalog.Skills[0].ID != "orch-setup" || catalog.Skills[0].Content != "compatible custom prose" {
		t.Fatalf("skills = %+v", catalog.Skills)
	}
	if strings.Contains(fmt.Sprintf("%+v", catalog), "custom/SKILL.md") {
		t.Fatalf("catalog retained installation location: %+v", catalog)
	}
}

func TestReadSkillCatalogRejectsAnotherLocation(t *testing.T) {
	root := t.TempDir()
	other := t.TempDir()
	runner := catalogRunner(func(execx.Cmd) (execx.Result, error) {
		return execx.Result{Stdout: fmt.Sprintf(`{"location":{"directory":%q},"data":[]}`, other)}, nil
	})
	_, err := ReadSkillCatalog(context.Background(), runner, root)
	if err == nil || !strings.Contains(err.Error(), "different directory") {
		t.Fatalf("error = %v, want different-directory rejection", err)
	}
}

func TestReadSkillCatalogDiagnosticsDoNotDiscloseOutput(t *testing.T) {
	const sensitive = "credential-token raw-skill-content copied-skill-location"
	tests := []struct {
		name   string
		result execx.Result
		err    error
	}{
		{name: "cannot start", err: fmt.Errorf("spawn failed: %s", sensitive)},
		{name: "nonzero", result: execx.Result{Stdout: sensitive, Stderr: sensitive, ExitCode: 7}},
		{name: "malformed", result: execx.Result{Stdout: `{"data":[{"id":"orch-setup","content":"raw-skill-content"}]}`}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			runner := catalogRunner(func(execx.Cmd) (execx.Result, error) { return tc.result, tc.err })
			_, err := ReadSkillCatalog(context.Background(), runner, t.TempDir())
			if err == nil {
				t.Fatal("ReadSkillCatalog succeeded")
			}
			for _, forbidden := range strings.Fields(sensitive) {
				if strings.Contains(err.Error(), forbidden) {
					t.Fatalf("diagnostic disclosed %q: %v", forbidden, err)
				}
			}
			if !strings.Contains(err.Error(), "OpenCode skill catalog") || !strings.Contains(err.Error(), "retry") {
				t.Fatalf("diagnostic is not actionable: %v", err)
			}
		})
	}
}
