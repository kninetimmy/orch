package interview

import (
	"fmt"
	"slices"

	"github.com/kninetimmy/orch/internal/question"
)

const idOpenCodeDeepSeekPreset = "opencode.preset.deepseek"

// This optional starting point does not replace the shipped default profiles.
var openCodeDeepSeekProfiles = map[string]profile{
	"architect":        {"opencode-go/deepseek-v4.1-flash", "max"},
	"scout":            {"opencode-go/glm-5.3-flash", "high"},
	"implementer":      {"openai/gpt-6.1-sol", "high"},
	"specialist":       {"openai/gpt-6.1-sol", "xhigh"},
	"reviewer":         {"openai/gpt-6.1-sol", "xhigh"},
	"review_downgrade": {"opencode-go/deepseek-v4.1-flash", "max"},
}

func requireOpenCodeDeepSeekPreset(facts Facts) error {
	if err := requireOpenCodeCatalog(facts); err != nil {
		return err
	}
	for _, rs := range roleSpecs {
		p := openCodeDeepSeekProfiles[rs.key]
		model, ok := openCodeCatalogModel(facts.OpenCodeCatalog, p.model)
		if !ok {
			return fmt.Errorf("DeepSeek-led mixed-provider preset unavailable: %s requires model %s in the current project catalog; refresh catalog discovery or decline the preset and customize roles manually", rs.key, p.model)
		}
		if !slices.Contains(model.Variants, p.execution) {
			return fmt.Errorf("DeepSeek-led mixed-provider preset unavailable: %s requires variant %s for %s in the current project catalog; refresh catalog discovery or decline the preset and customize roles manually", rs.key, p.execution, p.model)
		}
	}
	return nil
}

// Rebuilt on every step, summary and application, before role-default fallback.
// An incomplete preset is explained on the first model question, leaving the
// ordinary manual path usable. Previously submitted opt-ins instead fail closed.
func openCodePresetStart(facts Facts, answers map[string]string) ([]docSpec, string, error) {
	if !facts.OpenCodeCLI {
		return nil, "", nil
	}
	hint := ""
	if err := requireOpenCodeDeepSeekPreset(facts); err != nil {
		if answers[idOpenCodeDeepSeekPreset] == "yes" {
			return nil, "", err
		}
		if _, answered := answers[idOpenCodeDeepSeekPreset]; !answered {
			return nil, err.Error(), nil
		}
		// Keep an explicit decline applicable if the catalog later changes.
		hint = err.Error()
	}
	return []docSpec{{questions: []question.Question{{
		ID: idOpenCodeDeepSeekPreset, Header: "Preset",
		Prompt:   "Use the optional DeepSeek-led mixed-provider OpenCode V2 preset?",
		Preamble: "Opt in to seed all six role defaults; you can customize every role before approval. No session model is switched automatically. Decline to keep the existing manual/default path.",
		Hint:     hint, Kind: question.KindSelect, Default: "no", Options: yesNoOptions("no"),
	}}}}, hint, nil
}
