package cli

import (
	"context"
	"encoding/json"

	"github.com/kninetimmy/orch/internal/config"
	"github.com/kninetimmy/orch/internal/run"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// This receipt identifies the decoded submission, not an effective contract or
// approval digest. Successful validation grants no activation/dispatch authority.
type mcpPlanValidationResult struct {
	SchemaVersion   int          `json:"schema_version"`
	ValidationScope string       `json:"validation_scope"`
	ConfigRevision  string       `json:"config_revision"`
	Plan            *run.PlanDoc `json:"plan"`
}

func addMCPPlanValidation(server *mcp.Server, root string) {
	mcp.AddTool(server, &mcp.Tool{
		Name:        "orch_plan_validate",
		Description: "Validate an in-memory plan's structure against the bound repository's current configuration. Returns the decoded plan and configuration revision, not an effective-contract gate, approval digest, readiness assertion or permission to activate/dispatch. No commands, staging or writes occur.",
		InputSchema: json.RawMessage(`{"type":"object","properties":{"plan":{"type":"object"}},"required":["plan"],"additionalProperties":false}`),
		Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true, OpenWorldHint: new(bool)},
	}, func(_ context.Context, req *mcp.CallToolRequest, _ struct{}) (*mcp.CallToolResult, any, error) {
		// Read the original arguments: the SDK's schema pass can re-marshal
		// numbers. Preserve nested fields and number literals for DecodePlan.
		var input struct {
			Plan json.RawMessage `json:"plan"`
		}
		if err := json.Unmarshal(req.Params.Arguments, &input); err != nil {
			return nil, nil, err
		}
		cfg, err := config.Load(root)
		if err != nil {
			return nil, nil, err
		}
		plan, err := run.DecodePlan(input.Plan)
		if err != nil {
			return nil, nil, err
		}
		if err := plan.Validate(cfg); err != nil {
			return nil, nil, err
		}
		return nil, &mcpPlanValidationResult{
			SchemaVersion: 1, ValidationScope: "structural-configuration-only",
			ConfigRevision: cfg.ConfigRevision, Plan: plan,
		}, nil
	})
}
