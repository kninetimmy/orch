package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/kninetimmy/orch/internal/grant"
	"github.com/kninetimmy/orch/internal/run"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func addMCPActivation(server *mcp.Server, env Env, root string) {
	mcp.AddTool(server, &mcp.Tool{
		Name:        "orch_run_activate",
		Description: "Submit an explicit human-approved activation request to the bound repository's shared engine (orch run activate). Requires the human's approval statement, approver, timestamp and effective-contract plan digest; a tool call, validation or gate inspection is not approval. Non-idempotent: may create GitHub labels/issues, branches/worktrees, Delivery state/locks and existing audit/metrics records. Partial failures can preserve artifacts and require explicit recovery; never automatically retry. Does not dispatch, merge, apply configuration, execute evaluations or support grant-based activation.",
		InputSchema: json.RawMessage(`{"type":"object","properties":{"schema_version":{"type":"integer"},"plan":{"type":"object"},"approval":{"type":"object","properties":{"plan_digest":{"type":"string","minLength":1},"approved_by":{"type":"string","minLength":1},"approved_at":{"type":"string","minLength":1},"statement":{"type":"string","minLength":1}},"required":["plan_digest","approved_by","approved_at","statement"],"additionalProperties":false}},"required":["schema_version","plan","approval"],"additionalProperties":false}`),
		Annotations: &mcp.ToolAnnotations{DestructiveHint: new(true), OpenWorldHint: new(true)},
	}, func(ctx context.Context, req *mcp.CallToolRequest, _ any) (*mcp.CallToolResult, *run.ActivationResult, error) {
		// Keep the original JSON (including nested fields and number literals).
		// Reuse the strict decoder before any effects, then narrow this MCP
		// increment to explicit human evidence without changing CLI/grant policy.
		input, err := run.DecodeActivation(req.Params.Arguments)
		if err != nil {
			return nil, nil, err
		}
		if input.Approval.Statement != run.ApprovalStatement {
			return nil, nil, fmt.Errorf("%w: orch_run_activate requires human approval statement %q; grant-based activation is not supported", run.ErrBadApproval, run.ApprovalStatement)
		}
		if strings.TrimSpace(input.Approval.ApprovedBy) == "" {
			return nil, nil, fmt.Errorf("%w: orch_run_activate requires a nonempty approved_by", run.ErrBadApproval)
		}
		if input.Approval.ApprovedAt.IsZero() {
			return nil, nil, fmt.Errorf("%w: orch_run_activate requires a nonzero approved_at timestamp", run.ErrBadApproval)
		}
		var doc *run.ActivationResult
		err = withDeliveryMutation(root, func() error {
			doc, err = run.Activate(ctx, run.Env{
				RepoRoot: root, Runner: env.Runner, ClaudeSession: os.Getenv(grant.SessionEnv),
			}, req.Params.Arguments)
			return err
		})
		if err != nil {
			return nil, nil, err
		}
		return nil, doc, nil
	})
}
