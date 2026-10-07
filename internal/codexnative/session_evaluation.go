package codexnative

import (
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"

	"github.com/kninetimmy/orch/internal/nativehost"
)

// The evaluation binding and evidence types are shared with every host bridge.
type (
	EvaluationBinding   = nativehost.EvaluationBinding
	InstructionSource   = nativehost.InstructionSource
	InstructionEvidence = nativehost.InstructionEvidence
)

var evaluationRestrictedFeatures = []string{"memories", "external_agent_memory_import", "chronicle"}

func textSHA256(text string) string { return fmt.Sprintf("%x", sha256.Sum256([]byte(text))) }

func validateEvaluationTask(task Task) error {
	b := task.Evaluation
	if err := b.Identity.Validate(); err != nil {
		return err
	}
	if task.ID != b.TaskID() || task.RunID != "" || task.IssueNumber != 0 || task.Attempt != "" || task.ReviewCycle != 0 ||
		task.Role != b.Identity.Role || task.Selection != b.Selection || len(b.OrchRevision) != 40 ||
		strings.Trim(b.OrchRevision, "0123456789abcdef") != "" || len(b.ProfileSHA256) != 64 || strings.Trim(b.ProfileSHA256, "0123456789abcdef") != "" ||
		textSHA256(task.Prompt) != b.PromptSHA256 || textSHA256(task.Instructions) != b.InstructionsSHA256 ||
		!utf8.ValidString(task.Prompt) || !utf8.ValidString(task.Instructions) || strings.TrimSpace(task.Instructions) == "" || len(b.InstructionSources) > 1 || b.InstructionSources == nil {
		return fmt.Errorf("%w: evaluation identity/profile/public text differs", ErrTaskBoundary)
	}
	return checkInstructionFiles(b.InstructionSources)
}

func nativeInstructionHome() (string, error) {
	home := os.Getenv("CODEX_HOME")
	if home == "" {
		user, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		home = filepath.Join(user, ".codex")
	}
	return isolationPath(home, false)
}

func instructionHash(source InstructionSource) error {
	if err := nativehost.CheckInstructionHash(source); err != nil {
		return fmt.Errorf("%w: %w", ErrTaskBoundary, err)
	}
	return nil
}

// holdInstructionFile reports an unsupported platform as ErrIsolationUnavailable.
func holdInstructionFile(path string) (*os.File, error) {
	f, err := nativehost.HoldInstructionFile(path)
	if errors.Is(err, nativehost.ErrHoldUnsupported) {
		return nil, ErrIsolationUnavailable
	}
	return f, err
}

func checkInstructionFiles(sources []InstructionSource) error {
	home, err := nativeInstructionHome()
	if err != nil {
		return fmt.Errorf("%w: native instruction home unknown", ErrTaskBoundary)
	}
	var effective string
	for _, name := range []string{"AGENTS.override.md", "AGENTS.md"} {
		candidate := filepath.Join(home, name)
		info, err := os.Lstat(candidate)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil || !info.Mode().IsRegular() {
			return fmt.Errorf("%w: native instruction source unverifiable", ErrTaskBoundary)
		}
		if info.Size() == 0 {
			continue
		}
		effective = candidate
		break
	}
	if len(sources) == 0 && effective != "" || len(sources) != 0 && (len(sources) != 1 || sources[0].Path != effective) {
		return fmt.Errorf("%w: undeclared global/override instruction source", ErrTaskBoundary)
	}
	for _, source := range sources {
		if err := instructionHash(source); err != nil {
			return err
		}
	}
	return nil
}

func (s *Session) checkInstructions() error {
	if s.task.Evaluation == nil {
		return nil
	}
	if s.result.Instructions == nil {
		s.result.Instructions = &InstructionEvidence{SchemaVersion: 1, Status: "unknown", Sources: []InstructionSource{}, Detail: "Native loaded instruction sources not yet reported."}
	}
	if err := checkInstructionFiles(s.task.Evaluation.InstructionSources); err != nil {
		s.result.Instructions.Status, s.result.Instructions.Detail = "mismatch", err.Error()
		return err
	}
	return nil
}

func (s *Session) holdInstructions() (func(), error) {
	if s.task.Evaluation == nil {
		return func() {}, nil
	}
	if err := s.checkInstructions(); err != nil {
		return nil, err
	}
	var files []*os.File
	release := func() {
		for _, f := range files {
			_ = f.Close()
		}
	}
	for _, source := range s.task.Evaluation.InstructionSources {
		f, err := holdInstructionFile(source.Path)
		if err != nil {
			release()
			if errors.Is(err, ErrIsolationUnavailable) {
				return nil, err // unsupported platform is refusal, never a weaker hold
			}
			return nil, fmt.Errorf("%w: approved instruction file hold unavailable", ErrTaskBoundary)
		}
		files = append(files, f)
	}
	if err := s.checkInstructions(); err != nil {
		release()
		return nil, err
	}
	return release, nil
}

func (s *Session) acceptInstructions(paths []string) error {
	if err := s.checkInstructions(); err != nil {
		return err
	}
	want := s.task.Evaluation.InstructionSources
	if paths == nil || len(paths) != len(want) {
		s.result.Instructions.Status = "mismatch"
		return fmt.Errorf("%w: loaded native instruction sources missing or undeclared", ErrTaskBoundary)
	}
	for i, path := range paths {
		canonical, err := isolationPath(path, false)
		if err != nil || canonical != want[i].Path {
			s.result.Instructions.Status = "mismatch"
			return fmt.Errorf("%w: loaded native instruction source differs", ErrTaskBoundary)
		}
	}
	s.result.Instructions.Status = "verified-sources-and-hashes"
	s.result.Instructions.Sources = append([]InstructionSource{}, want...)
	s.result.Instructions.Detail = "Native source paths match approved artifacts; hashes checked around execution. Existing files held against ordinary Windows writes/replacement. No provider-disable or privileged-change guarantee."
	return nil
}

func verifyEvaluationConfig(c *connection, b isolationBoundary) error {
	config, err := readIsolationConfig(c, b.workspace)
	if err != nil {
		return err
	}
	if err := config.verifyFeatures(evaluationRestrictedFeatures); err != nil {
		return err
	}
	if err := verifyRestrictedFeatures(c, evaluationRestrictedFeatures...); err != nil {
		return err
	}
	var response struct {
		Config map[string]json.RawMessage `json:"config"`
	}
	if err := c.call("config/read", map[string]any{"cwd": b.workspace, "includeLayers": false}, &response); err != nil {
		return err
	}
	var zero *int
	if json.Unmarshal(response.Config["project_doc_max_bytes"], &zero) != nil || zero == nil || *zero != 0 {
		return restrictionError("evaluation project instruction discovery not disabled")
	}
	var skills struct {
		Include *bool `json:"include_instructions"`
	}
	if json.Unmarshal(response.Config["skills"], &skills) != nil || skills.Include == nil || *skills.Include {
		return restrictionError("evaluation inherited skill instructions not disabled")
	}
	for _, name := range []string{"include_apps_instructions", "include_collaboration_mode_instructions", "include_environment_context"} {
		var enabled *bool
		if json.Unmarshal(response.Config[name], &enabled) != nil || enabled == nil || *enabled {
			return restrictionError("evaluation inherited context control missing: " + name)
		}
	}
	for _, name := range []string{"instructions", "developer_instructions", "model_instructions_file", "compact_prompt"} {
		data := response.Config[name]
		if len(data) == 0 || string(data) == "null" {
			continue
		}
		var text string
		if json.Unmarshal(data, &text) != nil || text != "" {
			return restrictionError("evaluation undeclared configured instruction source: " + name)
		}
	}
	var policy struct {
		Requirements json.RawMessage `json:"requirements"`
	}
	if err := c.call("configRequirements/read", struct{}{}, &policy); err != nil {
		return err
	}
	if policy.Requirements == nil {
		return restrictionError("evaluation managed instruction requirements unknown")
	}
	if string(policy.Requirements) != "null" {
		var managed struct {
			Instructions *string `json:"additionalDeveloperInstructions"`
		}
		if json.Unmarshal(policy.Requirements, &managed) != nil || managed.Instructions != nil && *managed.Instructions != "" {
			return restrictionError("evaluation undeclared managed developer instructions")
		}
	}
	// Closed checks do not confer approval; only the caller's exact task does.
	if !b.evaluation {
		return ErrTaskBoundary
	}
	return nil
}
