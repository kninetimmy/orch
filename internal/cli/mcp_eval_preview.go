package cli

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/kninetimmy/orch/internal/evalplan"
	"github.com/kninetimmy/orch/internal/execx"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func addEvalPreviewTool(server *mcp.Server, repo, storageRoot string, runner execx.Runner) {
	mcp.AddTool(server, &mcp.Tool{
		Name:        "orch_eval_preview",
		Description: "Prepare a bounded evaluation from a repository-relative plan_file and retain its immutable public preparation record in the launch-bound --eval-storage-root. May write a digest-named record and interrupted-publication .pending-* artifacts; uses existing read-only Git probes. Grants no approval and creates no evaluation execution, worker or Delivery run.",
		InputSchema: json.RawMessage(`{"type":"object","properties":{"plan_file":{"type":"string","minLength":1}},"required":["plan_file"],"additionalProperties":false}`),
		Annotations: &mcp.ToolAnnotations{ReadOnlyHint: false, DestructiveHint: new(false), IdempotentHint: true, OpenWorldHint: new(false)},
	}, func(ctx context.Context, _ *mcp.CallToolRequest, input struct {
		PlanFile string `json:"plan_file"`
	}) (*mcp.CallToolResult, any, error) {
		if storageRoot == "" {
			return nil, nil, fmt.Errorf("orch_eval_preview requires launch-time --eval-storage-root <absolute-local-path> binding")
		}
		doc, err := evalplan.PreviewRepository(ctx, repo, storageRoot, input.PlanFile, runner)
		if err != nil {
			return nil, nil, err
		}
		return nil, doc, nil
	})
}
