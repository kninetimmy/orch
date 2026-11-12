package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"

	"github.com/kninetimmy/orch/internal/config"
	"github.com/kninetimmy/orch/internal/evalplan"
	"github.com/kninetimmy/orch/internal/paths"
	"github.com/kninetimmy/orch/internal/run"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func runMCP(env Env, args []string) error {
	const usage = "orch mcp: usage: orch mcp [--repo <path>] [--eval-storage-root <absolute-local-path>]"
	flags := map[string]string{}
	for i := 0; i < len(args); i += 2 {
		flag := args[i]
		if flag != "--repo" && flag != "--eval-storage-root" || flags[flag] != "" || i+1 == len(args) || args[i+1] == "" {
			return usageError(usage)
		}
		flags[flag] = args[i+1]
	}
	root := env.RepoRoot
	if flags["--repo"] != "" {
		root = flags["--repo"]
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
	evalStorageRoot := flags["--eval-storage-root"]
	if evalStorageRoot != "" {
		if err := evalplan.CheckStorageRoot(evalStorageRoot); err != nil {
			return fmt.Errorf("evaluation storage binding: %w", err)
		}
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
	mcp.AddTool(server, &mcp.Tool{
		Name:        "orch_run_next",
		Description: "Inspect engine-derived next actions, prerequisites, blockers and approval requirements without advancing the bound repository's run (same document as orch run next --json).",
		InputSchema: json.RawMessage(`{"type":"object","properties":{},"additionalProperties":false}`),
		Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true, OpenWorldHint: new(bool)},
	}, func(ctx context.Context, _ *mcp.CallToolRequest, _ struct{}) (*mcp.CallToolResult, any, error) {
		doc, err := run.Next(ctx, run.Env{RepoRoot: root})
		if err != nil {
			return nil, nil, err
		}
		return nil, doc, nil
	})
	mcp.AddTool(server, &mcp.Tool{
		Name:        "orch_run_readiness",
		Description: "Inspect one active-run issue's review commit, live PR/required CI and merge requirements without recording a merge gate or authorizing a merge (same document as orch run readiness --issue <number> --json). Uses read-only GitHub probes.",
		InputSchema: json.RawMessage(`{"type":"object","properties":{"issue_number":{"type":"integer","minimum":1}},"required":["issue_number"],"additionalProperties":false}`),
		Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true, OpenWorldHint: new(true)},
	}, func(ctx context.Context, _ *mcp.CallToolRequest, input struct {
		IssueNumber int `json:"issue_number"`
	}) (*mcp.CallToolResult, any, error) {
		doc, err := run.Readiness(ctx, run.Env{RepoRoot: root, Runner: env.Runner}, input.IssueNumber)
		if err != nil {
			return nil, nil, err
		}
		return nil, doc, nil
	})
	mcp.AddTool(server, &mcp.Tool{
		Name:        "orch_doctor",
		Description: "Inspect environment and configuration health without repairs (same report as orch doctor --json [--host <claude|codex|opencode>]). Uses read-only tool, authentication, repository and configured-host probes. Failed checks return an unhealthy report, not a tool error.",
		InputSchema: json.RawMessage(`{"type":"object","properties":{"host":{"type":"string","enum":["claude","codex","opencode"]}},"additionalProperties":false}`),
		Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true, OpenWorldHint: new(true)},
	}, func(ctx context.Context, _ *mcp.CallToolRequest, input struct {
		Host string `json:"host,omitempty"`
	}) (*mcp.CallToolResult, any, error) {
		doctorEnv := env
		doctorEnv.RepoRoot = root
		doc, err := doctorReport(doctorEnv, input.Host)
		if err != nil {
			return nil, nil, err
		}
		return nil, doc, nil
	})
	mcp.AddTool(server, &mcp.Tool{
		Name:        "orch_config_effective",
		Description: "Read the bound repository's freshly loaded effective Orch configuration, including defaults and permitted local overrides. Reports current files, not Git HEAD or an active run's approved configuration; performs no health probes.",
		InputSchema: json.RawMessage(`{"type":"object","properties":{},"additionalProperties":false}`),
		Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true, OpenWorldHint: new(bool)},
	}, func(_ context.Context, req *mcp.CallToolRequest, _ struct{}) (*mcp.CallToolResult, any, error) {
		// The SDK treats null/omitted arguments as {}; this tool requires an object.
		if args := bytes.TrimSpace(req.Params.Arguments); len(args) == 0 || args[0] != '{' {
			return nil, nil, fmt.Errorf("orch_config_effective: arguments must be an empty object")
		}
		cfg, err := config.Load(root)
		if err != nil {
			return nil, nil, err
		}
		return nil, struct {
			SchemaVersion int            `json:"schema_version"`
			Configuration *config.Config `json:"configuration"`
		}{1, cfg}, nil
	})
	mcp.AddTool(server, &mcp.Tool{
		Name:        "orch_metrics",
		Description: "Read whether metrics are enabled and per-run summaries of recorded metrics in the bound repository. Recorded metrics are not a complete archive of all runs; no commands or writes occur.",
		InputSchema: json.RawMessage(`{"type":"object","properties":{},"additionalProperties":false}`),
		Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true, OpenWorldHint: new(bool)},
	}, func(_ context.Context, req *mcp.CallToolRequest, _ struct{}) (*mcp.CallToolResult, any, error) {
		if args := bytes.TrimSpace(req.Params.Arguments); len(args) == 0 || args[0] != '{' {
			return nil, nil, fmt.Errorf("orch_metrics: arguments must be an empty object")
		}
		doc, err := inspectMetrics(root)
		if err != nil {
			return nil, nil, err
		}
		return nil, doc, nil
	})
	mcp.AddTool(server, &mcp.Tool{
		Name:        "orch_metrics_report",
		Description: "Read the existing cross-run rollup of recorded metrics in the bound repository, including outcomes, separate usage groups, elapsed times and escaped-defect attribution. Not a complete run archive; no commands or writes occur.",
		InputSchema: json.RawMessage(`{"type":"object","properties":{},"additionalProperties":false}`),
		Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true, OpenWorldHint: new(bool)},
	}, func(_ context.Context, req *mcp.CallToolRequest, _ struct{}) (*mcp.CallToolResult, any, error) {
		if args := bytes.TrimSpace(req.Params.Arguments); len(args) == 0 || args[0] != '{' {
			return nil, nil, fmt.Errorf("orch_metrics_report: arguments must be an empty object")
		}
		doc, err := inspectMetricsReport(root)
		if err != nil {
			return nil, nil, err
		}
		return nil, doc, nil
	})
	mcp.AddTool(server, &mcp.Tool{
		Name:        "orch_eval_status",
		Description: "Inspect one retained evaluation in the launch-bound local storage root, verifying its repository identity. Returns the same read-only report as orch eval status --json, not raw worker/private source contents. Does not publish reports, grade, execute, stop or infer process liveness; requires --eval-storage-root at server launch.",
		InputSchema: json.RawMessage(`{"type":"object","properties":{"run_id":{"type":"string","minLength":1}},"required":["run_id"],"additionalProperties":false}`),
		Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true, OpenWorldHint: new(bool)},
	}, func(_ context.Context, _ *mcp.CallToolRequest, input struct {
		RunID string `json:"run_id"`
	}) (*mcp.CallToolResult, any, error) {
		if evalStorageRoot == "" {
			return nil, nil, fmt.Errorf("orch_eval_status requires launch-time --eval-storage-root <absolute-local-path> binding")
		}
		doc, err := evalplan.InspectRepository(root, evalStorageRoot, input.RunID)
		if err != nil {
			return nil, nil, err
		}
		return nil, doc, nil
	})
	addPlanGateTool(server, env, root)
	return server.Run(context.Background(), &mcp.IOTransport{
		Reader: io.NopCloser(env.Stdin),
		Writer: mcpWriter{env.Stdout},
	})
}

// The invocation owns stdout; closing the SDK transport must not close it.
type mcpWriter struct{ io.Writer }

func (mcpWriter) Close() error { return nil }
