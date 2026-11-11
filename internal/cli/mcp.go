package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"

	"github.com/kninetimmy/orch/internal/paths"
	"github.com/kninetimmy/orch/internal/run"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func runMCP(env Env, args []string) error {
	root := env.RepoRoot
	if len(args) != 0 {
		if len(args) != 2 || args[0] != "--repo" || args[1] == "" {
			return usageError("orch mcp: usage: orch mcp [--repo <path>]")
		}
		root = args[1]
	}
	root, err := paths.Canonical(root)
	if err != nil {
		return err
	}
	info, err := os.Stat(root)
	if err != nil {
		return fmt.Errorf("repository directory: %w", err)
	}
	if !info.IsDir() {
		return fmt.Errorf("repository %s is not a directory", root)
	}
	// Check only the selected root, never an ancestor. Like FindRoot, require
	// real metadata rather than a file or symlink, but do not discover a root.
	metadata := filepath.Join(root, paths.OrchestratorDir)
	info, err = os.Lstat(metadata)
	if err != nil {
		return fmt.Errorf("repository must contain %s: %w", metadata, err)
	}
	if !info.IsDir() {
		return fmt.Errorf("%s is not a real directory", metadata)
	}

	server := mcp.NewServer(&mcp.Implementation{Name: "orch", Version: Version}, &mcp.ServerOptions{
		Logger: slog.New(slog.NewTextHandler(env.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn})),
	})
	mcp.AddTool(server, &mcp.Tool{
		Name:        "orch_run_status",
		Description: "Read the bound repository's live Orch run status (same document as orch run status --json).",
		InputSchema: json.RawMessage(`{"type":"object","properties":{},"additionalProperties":false}`),
		Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true, OpenWorldHint: new(bool)},
	}, func(ctx context.Context, _ *mcp.CallToolRequest, _ struct{}) (*mcp.CallToolResult, any, error) {
		doc, err := run.Status(ctx, run.Env{RepoRoot: root})
		if err != nil {
			return nil, nil, err
		}
		// The SDK emits both structured content and equivalent JSON text.
		// Keep the engine's document intact instead of maintaining another schema.
		return nil, doc, nil
	})
	return server.Run(context.Background(), &mcp.IOTransport{
		Reader: io.NopCloser(env.Stdin),
		Writer: mcpWriter{env.Stdout},
	})
}

// The invocation owns stdout; closing the SDK transport must not close it.
type mcpWriter struct{ io.Writer }

func (mcpWriter) Close() error { return nil }
