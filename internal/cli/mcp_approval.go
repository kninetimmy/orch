package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"

	"github.com/kninetimmy/orch/internal/run"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func addApprovalStatusTool(server *mcp.Server, env Env, root string) {
	mcp.AddTool(server, &mcp.Tool{
		Name:        "orch_approval_status",
		Description: "Observe recorded run plan-approval metadata, merge-report pins and the clone's active autonomy grant (or explicit absence). Unlocked, non-atomic evidence only, never authorization for a future action; does not approve or spend a grant. Uses only read-only Git top-level/common-directory probes.",
		InputSchema: json.RawMessage(`{"type":"object","properties":{},"additionalProperties":false}`),
		Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true, OpenWorldHint: new(true)},
	}, func(ctx context.Context, req *mcp.CallToolRequest, _ struct{}) (*mcp.CallToolResult, any, error) {
		// The SDK substitutes {} for omitted/null arguments during validation.
		if args := bytes.TrimSpace(req.Params.Arguments); len(args) == 0 || args[0] != '{' {
			return nil, nil, fmt.Errorf("orch_approval_status: arguments must be an empty object")
		}
		doc, err := run.ApprovalStatus(ctx, run.Env{RepoRoot: root, Runner: env.Runner})
		if err != nil {
			return nil, nil, err
		}
		return nil, doc, nil
	})
}
