package opencode

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestPackagePinsOpenCodeV2Contract(t *testing.T) {
	var manifest struct {
		Name         string            `json:"name"`
		Version      string            `json:"version"`
		Dependencies map[string]string `json:"dependencies"`
	}
	if err := json.Unmarshal([]byte(PackageJSON), &manifest); err != nil {
		t.Fatal(err)
	}
	if manifest.Name != "@kninetimmy/orch-opencode" || manifest.Version == "" {
		t.Fatalf("manifest = %#v", manifest)
	}
	if got := manifest.Dependencies["@opencode-ai/plugin"]; got != "0.0.0-next-17444" {
		t.Fatalf("@opencode-ai/plugin = %q, want pinned next-17444", got)
	}
	if len(manifest.Dependencies) != 1 {
		t.Fatalf("dependencies = %v, want only the V2 plugin API", manifest.Dependencies)
	}
}

func TestNativeAgents(t *testing.T) {
	models := map[string]string{
		"orch-scout": "openai/gpt-5.6-luna#max", "orch-implementer": "openai/gpt-5.6-terra#max",
		"orch-specialist": "openai/gpt-5.6-sol#max", "orch-reviewer": "openai/gpt-5.6-sol#xhigh",
		"orch-reviewer-safe": "openai/gpt-5.6-sol#high",
	}
	for name, model := range models {
		data, err := AgentDefinitions.ReadFile("agents/" + name + ".md")
		if err != nil {
			t.Fatal(err)
		}
		text := string(data)
		for _, want := range []string{"mode: subagent", "model: " + model, `action: subagent, resource: "*", effect: deny`} {
			if !strings.Contains(text, want) {
				t.Errorf("%s missing %q", name, want)
			}
		}
		readOnly := name == "orch-scout" || strings.HasPrefix(name, "orch-reviewer")
		for _, action := range []string{"edit", "shell"} {
			hasDeny := strings.Contains(text, "action: "+action+`, resource: "*", effect: deny`)
			if hasDeny != readOnly {
				t.Errorf("%s %s deny = %v, want %v", name, action, hasDeny, readOnly)
			}
		}
	}
}

func TestSkillsUseOnlyOpenCodeV2Surfaces(t *testing.T) {
	forbidden := []string{"request_user_input", "spawn_agent", "wait_agent", "followup_task", "rollout", "marketplace", "hook-manifest", "agent-TOML"}
	for _, name := range []string{"orch-architect", "orch-delivery", "orch-setup"} {
		data, err := Skills.ReadFile("skills/" + name + "/SKILL.md")
		if err != nil {
			t.Fatal(err)
		}
		text := string(data)
		for _, word := range forbidden {
			if strings.Contains(text, word) {
				t.Errorf("%s refers to Codex-only surface %q", name, word)
			}
		}
	}
}
