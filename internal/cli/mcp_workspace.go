package cli

import (
	"context"
	"fmt"
	"os"

	"github.com/kninetimmy/orch/internal/execx"
	"github.com/kninetimmy/orch/internal/gitops"
	"github.com/kninetimmy/orch/internal/opencode"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const openCodeSessionMeta = "ai.opencode/sessionID"

// requireMCPWorkspace verifies the session location, never using it to select
// a repository. Plan and Activate independently require the bound Git primary
// in the engine; Dispatch verifies it at this transport boundary as well.
func requireMCPWorkspace(ctx context.Context, req *mcp.CallToolRequest, runner execx.Runner, root string) error {
	id, _ := req.Params.Meta[openCodeSessionMeta].(string)
	directory, err := opencode.SessionDirectory(ctx, runner, root, id)
	if err != nil {
		return err
	}
	bound, err := os.Stat(root)
	if err != nil {
		return fmt.Errorf("OpenCode session workspace cannot be verified against the bound checkout")
	}
	current, err := os.Stat(directory)
	if err != nil || !current.IsDir() {
		return fmt.Errorf("OpenCode session workspace cannot be verified as an existing directory")
	}
	if !bound.IsDir() || !os.SameFile(bound, current) {
		return fmt.Errorf("OpenCode session workspace does not match the bound checkout %q; open its primary checkout or fix the project MCP registration", root)
	}
	return nil
}

func requireMCPDispatchPrimary(ctx context.Context, runner execx.Runner, root string) error {
	git, err := gitops.Open(ctx, runner, root)
	if err != nil {
		return err
	}
	return git.RequirePrimary(ctx)
}
