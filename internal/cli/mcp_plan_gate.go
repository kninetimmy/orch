package cli

import (
	"context"
	"encoding/json"

	"github.com/kninetimmy/orch/internal/run"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func addPlanGateTool(server *mcp.Server, env Env, root string) {
	mcp.AddTool(server, &mcp.Tool{
		Name:        "orch_plan_gate",
		Description: "Produce the bound repository's full human decision gate for a submitted plan (same GateDoc and effective-contract digest as orch run plan). Requires Assist with no Delivery lock. Does not approve, activate, record a session or create Git/GitHub artifacts. Uses the existing mode-dependent memhub status/recall probes, whose own storage/provider side effects are not suppressed.",
		InputSchema: json.RawMessage(`{"type":"object","properties":{"plan":{"type":"object"}},"required":["plan"],"additionalProperties":false}`),
		// Do not promise read-only/idempotent external memhub behavior. Orch's
		// gate itself remains non-activating; off mode skips both probes.
		Annotations: &mcp.ToolAnnotations{OpenWorldHint: new(true)},
	}, func(ctx context.Context, _ *mcp.CallToolRequest, input struct {
		Plan json.RawMessage `json:"plan"`
	}) (*mcp.CallToolResult, *run.GateDoc, error) {
		// Preserve the submitted bytes for the engine's strict nested decoder;
		// decoding a PlanDoc here would silently discard unknown fields.
		doc, err := run.Plan(ctx, run.Env{RepoRoot: root, Runner: env.Runner}, input.Plan)
		if err != nil {
			return nil, nil, err
		}
		return nil, doc, nil
	})
}
