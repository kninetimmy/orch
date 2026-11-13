package cli

import (
	"context"
	"encoding/json"

	"github.com/kninetimmy/orch/internal/interview"
	"github.com/kninetimmy/orch/internal/question"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func addMCPInterviewTools(server *mcp.Server, env Env, root string) {
	for _, tool := range []struct {
		name, description string
		next              func(interview.Facts, map[string]string, string) (question.Document, error)
	}{
		{"orch_configure_step", "Derive the next committed-configuration interview document (orch configure --step). Submit the full schema-versioned AnswerSet each time. Uses fresh read probes; summary approval and completion do not deliver configuration or approve a run.", interview.NextConfigure},
		{"orch_configure_local_step", "Derive the next machine-local configuration interview document (orch configure-local --step). Submit the full schema-versioned AnswerSet each time. Uses fresh read probes; summary approval and completion do not apply overrides or approve a run.", interview.NextConfigureLocalWithFacts},
	} {
		mcp.AddTool(server, &mcp.Tool{
			Name:        tool.name,
			Description: tool.description,
			// DecodeAnswers also accepts omitted/null answers as an empty map.
			InputSchema: json.RawMessage(`{"type":"object","properties":{"schema_version":{"type":"integer"},"answers":{"type":["object","null"],"additionalProperties":{"type":"string"}}},"required":["schema_version"],"additionalProperties":false}`),
			Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true, OpenWorldHint: new(true)},
		}, func(ctx context.Context, req *mcp.CallToolRequest, _ any) (*mcp.CallToolResult, any, error) {
			if err := requireNoActiveDelivery(root); err != nil {
				return nil, nil, err
			}
			answers, err := question.DecodeAnswers(req.Params.Arguments)
			if err != nil {
				return nil, nil, err
			}
			facts, err := interview.DetectBound(ctx, interview.Deps{RepoRoot: root, LookPath: env.LookPath, Runner: env.Runner})
			if err != nil {
				return nil, nil, err
			}
			doc, err := tool.next(facts, answers.Answers, root)
			if err != nil {
				return nil, nil, err
			}
			return nil, doc, nil
		})
	}
}
