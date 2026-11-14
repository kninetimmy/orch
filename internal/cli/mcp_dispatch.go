package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"

	"github.com/kninetimmy/orch/internal/grant"
	"github.com/kninetimmy/orch/internal/run"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func addMCPDispatch(server *mcp.Server, env Env, root string) {
	mcp.AddTool(server, &mcp.Tool{
		Name:        "orch_run_dispatch",
		Description: "Dispatch one approved worktree-ready issue through the bound repository's shared engine (orch run dispatch), returning its complete schema-4 execution brief, not an agent launch or issue-context inspection. Non-idempotent, mutating and externally effectful: may fetch/fast-forward Git, set GitHub in-progress, save phase and record existing sessions/metrics. Partial failures or cancellation can leave effects; inspect fresh state and use normal engine recovery, never automatically retry. Does not spawn an agent, choose a model, grant approval or perform PR/review/merge operations.",
		InputSchema: json.RawMessage(`{"type":"object","properties":{"schema_version":{"type":"integer","enum":[4]},"issue_number":{"type":"integer","minimum":1}},"required":["schema_version","issue_number"],"additionalProperties":false}`),
		Annotations: &mcp.ToolAnnotations{DestructiveHint: new(true), OpenWorldHint: new(true)},
	}, func(ctx context.Context, req *mcp.CallToolRequest, _ any) (*mcp.CallToolResult, *run.DispatchResult, error) {
		args := bytes.TrimSpace(req.Params.Arguments)
		if len(args) == 0 || args[0] != '{' {
			return nil, nil, fmt.Errorf("orch_run_dispatch: arguments must be an explicit object")
		}
		// The SDK validates required/unknown fields. Check original number
		// literals too, before serialization or the engine's session writes.
		var input run.DispatchRequest
		if err := json.Unmarshal(args, &input); err != nil {
			return nil, nil, fmt.Errorf("orch_run_dispatch: %w", err)
		}
		if input.SchemaVersion != run.DispatchSchemaVersion {
			return nil, nil, fmt.Errorf("orch_run_dispatch: schema_version %d is unsupported (this build supports %d)", input.SchemaVersion, run.DispatchSchemaVersion)
		}
		if input.IssueNumber <= 0 {
			return nil, nil, fmt.Errorf("orch_run_dispatch: issue_number must be a positive integer")
		}
		var doc *run.DispatchResult
		err := withDeliveryMutation(root, func() error {
			var err error
			doc, err = run.Dispatch(ctx, run.Env{
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
