// Package nativehost holds the host-neutral task, evaluation-binding and
// evidence types plus the workspace-layout and instruction-file checks that
// every native host bridge and the evaluation controller share. It launches no
// host, adds no host-specific protected path and grants no approval.
package nativehost

import (
	"fmt"

	"github.com/kninetimmy/orch/internal/manifest"
	"github.com/kninetimmy/orch/internal/metrics"
)

// IsolationPaths must name the complete approved layout. Protected directories
// may contain one another, but none may overlap the workspace or scratch.
// Existing Delivery worktrees nested under the main checkout are unsuitable.
type IsolationPaths struct {
	Workspace         string
	Scratch           string
	MainCheckout      string
	ControllerState   string
	SiblingWorkspaces []string
	CredentialPaths   []string
}

// Task is one caller-approved dispatch, not a request for a host bridge to approve
// or route work. The engine remains responsible for eligibility and approval.
// ID and Prompt identify the exact task; Selection is its routed native profile.
type Task struct {
	ID           string
	RunID        string
	IssueNumber  int
	Role         string
	Attempt      string
	ReviewCycle  int
	Selection    manifest.Selection
	Prompt       string
	Layout       IsolationPaths
	Evaluation   *EvaluationBinding
	Instructions string // evaluation-only, supplied from the declared public ROLE.md
}

// EvaluationBinding is explicit caller authority, never a synthetic Delivery run.
// Hashes bind both public task text and separately approved global instructions.
type EvaluationBinding struct {
	Identity           metrics.EvaluationIdentity `json:"identity"`
	OrchRevision       string                     `json:"orch_revision"`
	ProfileSHA256      string                     `json:"profile_sha256"`
	Selection          manifest.Selection         `json:"selection"`
	Workspace          string                     `json:"workspace"`
	Scratch            string                     `json:"scratch"`
	PromptSHA256       string                     `json:"prompt_sha256"`
	InstructionsSHA256 string                     `json:"instructions_sha256"`
	InstructionSources []InstructionSource        `json:"instruction_sources"`
}

type InstructionSource struct {
	Path   string `json:"path"`
	SHA256 string `json:"sha256"`
}

type InstructionEvidence struct {
	SchemaVersion int                 `json:"schema_version"`
	Status        string              `json:"status"`
	Sources       []InstructionSource `json:"sources"`
	Detail        string              `json:"detail"`
}

func (b EvaluationBinding) TaskID() string {
	return fmt.Sprintf("%s/unit-%06d/attempt-%06d", b.Identity.ID, b.Identity.Unit, b.Identity.Attempt)
}

type SessionCleanup struct {
	InterruptionAsked     bool   `json:"interruption_asked"`
	InterruptAcknowledged *bool  `json:"interrupt_acknowledged,omitempty"`
	ShutdownObserved      *bool  `json:"shutdown_observed,omitempty"`
	Detail                string `json:"detail,omitempty"`
}
