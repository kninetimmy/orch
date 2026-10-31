package bootstrap

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/kninetimmy/orch/internal/config"
	"github.com/kninetimmy/orch/internal/question"
)

func TestRenderRecordPreservesReviewEvidence(t *testing.T) {
	complete := validComplete(t)
	cfg, err := config.Parse([]byte(complete.Summary.ConfigTOML))
	if err != nil {
		t.Fatal(err)
	}
	cfg.Protected = &config.Protected{
		MethodologyVersion: config.SurveyMethodologyVersion,
		Paths: []config.ProtectedPath{
			{Path: "internal/grant", Reason: "approval rules | holder checks"},
			{Path: "internal/data", Reason: "irreversible writes"},
		},
	}
	cfg.ConfigRevision, err = config.Revision(cfg)
	if err != nil {
		t.Fatal(err)
	}
	rendered, err := config.Render(cfg)
	if err != nil {
		t.Fatal(err)
	}
	complete.Summary.ConfigTOML = string(rendered)
	complete.Summary.ConfigRevision = cfg.ConfigRevision
	complete.Summary.ConfigDiff = "@@ -1 +1 @@\n-before-setting\n+approved-setting\n"
	complete.Summary.Files = []question.FileChange{
		{Path: "CURRENT.md", Existed: true, NewContent: "unchanged-body-must-not-repeat"},
		{Path: "AGENTS.md", Existed: true, Diff: "@@ -1 +1 @@\n-old-convention\n+approved-convention\n", NewContent: "approved-convention\n"},
		{Path: "CLAUDE.md", NewContent: "# Local conventions\n\n```sh\ngo test ./...\n```\n"},
	}
	complete.Summary.Conflicts = []string{"nested/AGENTS.md: inspect the conflicting managed block"}
	before, err := json.Marshal(complete)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name, title, sections string
		answers               map[string]string
	}{
		{"init", bootstrapTitle, "host enablement, role profiles, settings, grant-protected paths", map[string]string{"protected.paths": `[{"path":"internal/grant","reason":"approval rules | holder checks"},{"path":"internal/data","reason":"irreversible writes"}]`}},
		{"configure", configureTitle, "settings, grant-protected paths", map[string]string{"pick.hosts": "no", "pick.roles.claude": "no", "pick.settings": "yes", "protected.paths": `[{"path":"internal/grant","reason":"approval rules | holder checks"},{"path":"internal/data","reason":"irreversible writes"}]`}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			body, err := renderRecord(complete, tc.answers, tc.title, []ValidationEntry{{Name: "config.Load", Result: "pass", Detail: "approved bytes"}}, 42)
			if err != nil {
				t.Fatal(err)
			}
			why := strings.Index(body, "## Why\n")
			if !strings.HasPrefix(body, "## What\n\n"+tc.title+".\n") || why < 0 || why > strings.Index(body, "## Detection\n") {
				t.Fatalf("record does not begin with What and Why:\n%s", body)
			}
			for _, want := range []string{
				"Selected interview sections: " + tc.sections + ".",
				"Apply the choices explicitly approved in the Orch configuration interview.",
				"- gh: yes\n- git: yes",
				"**Config revision:** `" + cfg.ConfigRevision + "`",
				complete.Summary.ConfigTOML,
				complete.Summary.ConfigDiff,
				fmt.Sprintf("**Survey methodology:** %d", cfg.Protected.MethodologyVersion),
				"| internal/grant | approval rules \\| holder checks |",
				"| internal/data | irreversible writes |",
				complete.Summary.Files[1].Diff,
				"````md\n" + complete.Summary.Files[2].NewContent + "````\n",
				"- `.orchestrator/state.json`",
				"## Conflicts\n\n- nested/AGENTS.md: inspect the conflicting managed block",
				"- config.Load: pass (approved bytes)",
				"Closes #42",
			} {
				if !strings.Contains(body, want) {
					t.Errorf("record omitted %q", want)
				}
			}
			for _, unwanted := range []string{"```json", `"bootstrap_ready"`, `"new_content"`, "unchanged-body-must-not-repeat"} {
				if strings.Contains(body, unwanted) {
					t.Errorf("record repeated %q", unwanted)
				}
			}
		})
	}
	after, err := json.Marshal(complete)
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != string(before) {
		t.Fatal("rendering changed the structured Complete document")
	}
}

func TestRenderRecordProtectedEmptyAndDeferred(t *testing.T) {
	complete := validComplete(t)
	cfg, err := config.Parse([]byte(complete.Summary.ConfigTOML))
	if err != nil {
		t.Fatal(err)
	}
	cfg.Protected = &config.Protected{MethodologyVersion: config.SurveyMethodologyVersion}
	cfg.ConfigRevision, err = config.Revision(cfg)
	if err != nil {
		t.Fatal(err)
	}
	rendered, err := config.Render(cfg)
	if err != nil {
		t.Fatal(err)
	}
	complete.Summary.ConfigTOML = string(rendered)
	complete.Summary.ConfigRevision = cfg.ConfigRevision
	complete.Summary.Files = nil
	for _, answer := range []string{"[]", "defer"} {
		body, err := renderRecord(complete, map[string]string{"pick.hosts": "no", "protected.paths": answer}, configureTitle, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(body, "No entries beyond the built-in floor.") {
			t.Error("resulting empty list was not shown")
		}
		if strings.Contains(body, "Selected interview sections: grant-protected paths.") != (answer == "[]") {
			t.Errorf("deferred selection was described as an approved change: %s", answer)
		}
		if strings.Contains(body, "## Validations") || strings.Contains(body, "Closes #") {
			t.Error("issue record invented validation or issue-closing evidence")
		}
	}
}

func TestRenderRecordRejectsInvalidApprovedConfiguration(t *testing.T) {
	complete := validComplete(t)
	complete.Summary.ConfigTOML = "invalid = ["
	if _, err := renderRecord(complete, nil, bootstrapTitle, nil, 0); err == nil || !strings.Contains(err.Error(), "parse approved configuration") {
		t.Fatalf("invalid approved configuration: %v", err)
	}
}
