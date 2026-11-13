package cli

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"
	"strings"

	"github.com/kninetimmy/orch/internal/config"
	"github.com/kninetimmy/orch/internal/run"
	"github.com/modelcontextprotocol/go-sdk/jsonrpc"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type effectiveConfigDoc struct {
	SchemaVersion int            `json:"schema_version"`
	Configuration *config.Config `json:"configuration"`
}

func inspectEffectiveConfig(root string) (*effectiveConfigDoc, error) {
	cfg, err := config.Load(root)
	if err != nil {
		return nil, err
	}
	return &effectiveConfigDoc{1, cfg}, nil
}

func addMCPResources(server *mcp.Server, root string) {
	server.AddReceivingMiddleware(func(next mcp.MethodHandler) mcp.MethodHandler {
		return func(ctx context.Context, method string, req mcp.Request) (mcp.Result, error) {
			result, err := next(ctx, method, req)
			var rpcErr *jsonrpc.Error
			if method == "resources/read" && errors.As(err, &rpcErr) && rpcErr.Code == jsonrpc.CodeInvalidParams &&
				rpcErr.Message == "Resource not found" && !json.Valid(rpcErr.Data) {
				// SDK v1.8.0 Go-quotes this URI into RawMessage. Control
				// bytes can produce invalid JSON (\x00); keep its error
				// semantics but encode the original URI as actual JSON.
				data, err := json.Marshal(map[string]string{"uri": req.(*mcp.ReadResourceRequest).Params.URI})
				if err != nil {
					return nil, err
				}
				return result, &jsonrpc.Error{Code: rpcErr.Code, Message: rpcErr.Message, Data: data}
			}
			return result, err
		}
	})
	// Registration is static: discovery must not load reports or probe tools.
	handler := func(ctx context.Context, req *mcp.ReadResourceRequest) (*mcp.ReadResourceResult, error) {
		uri := req.Params.URI
		var doc any
		var err error
		switch uri {
		case "orch://run/status":
			doc, err = run.Status(ctx, run.Env{RepoRoot: root})
		case "orch://run/next":
			doc, err = run.Next(ctx, run.Env{RepoRoot: root})
		case "orch://config/effective":
			doc, err = inspectEffectiveConfig(root)
		case "orch://metrics/history":
			doc, err = inspectMetrics(root)
		default:
			// Validate the original URI, not parsed/decoded template variables.
			// A canonical positive decimal int forbids aliases and extra paths.
			number, ok := strings.CutPrefix(uri, "orch://run/issues/")
			issue, parseErr := strconv.Atoi(number)
			if !ok || parseErr != nil || issue <= 0 || strconv.Itoa(issue) != number {
				return nil, mcp.ResourceNotFoundError(uri)
			}
			doc, err = run.IssueContext(ctx, run.Env{RepoRoot: root}, issue)
		}
		if err != nil {
			return nil, err
		}
		data, err := json.Marshal(doc)
		if err != nil {
			return nil, err
		}
		return &mcp.ReadResourceResult{
			Cacheable: mcp.Cacheable{CacheScope: "private"}, // Zero TTL: inspect again on every read.
			Contents: []*mcp.ResourceContents{{
				URI: uri, MIMEType: "application/json", Text: string(data),
			}},
		}, nil
	}
	for _, resource := range []*mcp.Resource{
		{URI: "orch://run/status", Name: "orch_run_status", MIMEType: "application/json", Description: "Current local run status, including recorded approvals and consistency warnings; not authorization."},
		{URI: "orch://run/next", Name: "orch_run_next", MIMEType: "application/json", Description: "Current engine guidance and outstanding prerequisites; no live readiness probes or lifecycle actions."},
		{URI: "orch://config/effective", Name: "orch_config_effective", MIMEType: "application/json", Description: "Current effective configuration, not Git HEAD or an approved execution configuration."},
		{URI: "orch://metrics/history", Name: "orch_metrics", MIMEType: "application/json", Description: "Per-run summaries of recorded metrics only, not a complete run archive."},
	} {
		server.AddResource(resource, handler)
	}
	server.AddResourceTemplate(&mcp.ResourceTemplate{
		URITemplate: "orch://run/issues/{issue_number}", Name: "orch_issue_context", MIMEType: "application/json",
		Description: "Recorded issue context and engine guidance for a canonical positive decimal issue number; not a dispatch receipt or authorization.",
	}, handler)
}
