// Package mcpserver exposes the Tool Gateway as an MCP server.
//
// The MCP protocol itself is implemented by github.com/mark3labs/mcp-go
// (ADR-022). This package only adds the gateway layer: OPA authorization,
// credential brokering, audit and the upstream executor.
package mcpserver

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"

	"github.com/google/uuid"
	mcpgo "github.com/mark3labs/mcp-go/mcp"
	mcpsdk "github.com/mark3labs/mcp-go/server"
	"github.com/sapelyuk/ai-sdlc-factory/apps/tool-gateway/internal/audit"
	"github.com/sapelyuk/ai-sdlc-factory/apps/tool-gateway/internal/catalog"
	"github.com/sapelyuk/ai-sdlc-factory/apps/tool-gateway/internal/credentials"
	"github.com/sapelyuk/ai-sdlc-factory/apps/tool-gateway/internal/identity"
	"github.com/sapelyuk/ai-sdlc-factory/apps/tool-gateway/internal/policy"
)

// ServerName and ServerVersion identify the MCP implementation.
const (
	ServerName    = "ai-sdlc-factory-tool-gateway"
	ServerVersion = "1.0.0"
)

// Call is one tool invocation passed to an Executor.
type Call struct {
	Tool catalog.Tool
	// Arguments are the raw MCP arguments.
	Arguments map[string]any
	// Credentials are injected by the gateway. They must never be returned
	// to the agent or written to logs.
	Credentials map[string]string
}

// Executor runs a tool call.
//
// v1 ships StubExecutor only. The Sandbox Manager integration (issue #13)
// replaces it with an executor that proxies to upstream MCP servers via
// mcp-go ProxyTool.
type Executor interface {
	Execute(ctx context.Context, call Call) (map[string]any, error)
}

// StubExecutor returns a deterministic success response without side effects.
type StubExecutor struct{}

// Execute implements Executor.
func (StubExecutor) Execute(_ context.Context, call Call) (map[string]any, error) {
	result := map[string]any{
		"status":  "success",
		"message": "stub",
		"tool":    call.Tool.Name,
	}
	if len(call.Credentials) > 0 {
		names := make([]string, 0, len(call.Credentials))
		for name := range call.Credentials {
			names = append(names, name)
		}
		result["credentials_injected"] = names
	}
	return result, nil
}

// Options configures the gateway MCP server.
type Options struct {
	Catalog    *catalog.Catalog
	Authorizer policy.Authorizer
	Auditor    *audit.Logger
	Broker     credentials.Broker
	Redactor   *credentials.Redactor
	Executor   Executor
	Logger     *slog.Logger
}

// Server is the Tool Gateway MCP server.
type Server struct {
	catalog    *catalog.Catalog
	authorizer policy.Authorizer
	auditor    *audit.Logger
	broker     credentials.Broker
	redactor   *credentials.Redactor
	executor   Executor
	logger     *slog.Logger
	mcp        *mcpsdk.MCPServer
}

// NewServer registers every catalog tool on a new MCP server.
func NewServer(opts Options) (*Server, error) {
	if opts.Catalog == nil {
		return nil, errors.New("catalog is required")
	}
	if opts.Authorizer == nil {
		return nil, errors.New("authorizer is required")
	}
	if opts.Auditor == nil {
		return nil, errors.New("auditor is required")
	}
	if opts.Executor == nil {
		opts.Executor = StubExecutor{}
	}
	if opts.Redactor == nil {
		opts.Redactor = credentials.NewRedactor()
	}
	if opts.Logger == nil {
		opts.Logger = slog.Default()
	}

	s := &Server{
		catalog:    opts.Catalog,
		authorizer: opts.Authorizer,
		auditor:    opts.Auditor,
		broker:     opts.Broker,
		redactor:   opts.Redactor,
		executor:   opts.Executor,
		logger:     opts.Logger,
	}

	s.mcp = mcpsdk.NewMCPServer(
		ServerName,
		ServerVersion,
		mcpsdk.WithToolCapabilities(true),
		mcpsdk.WithRecovery(),
		mcpsdk.WithInstructions("Tool Gateway: every call is authorized by OPA (R0-R3) and audited."),
	)

	for _, tool := range s.catalog.Tools() {
		t := tool
		toolOpts := []mcpgo.ToolOption{mcpgo.WithDescription(t.Description)}
		if len(t.InputSchema) > 0 {
			toolOpts = append(toolOpts, mcpgo.WithRawInputSchema(t.InputSchema))
		}
		s.mcp.AddTool(mcpgo.NewTool(t.Name, toolOpts...), s.handle(t))
	}

	s.logger.Info("registered MCP tools", slog.Int("count", s.catalog.Len()))
	return s, nil
}

// MCP returns the underlying mcp-go server.
func (s *Server) MCP() *mcpsdk.MCPServer { return s.mcp }

func (s *Server) handle(tool catalog.Tool) mcpsdk.ToolHandlerFunc {
	return func(ctx context.Context, request mcpgo.CallToolRequest) (*mcpgo.CallToolResult, error) {
		id, err := identity.From(ctx)
		if err != nil {
			return mcpgo.NewToolResultError("missing caller identity"), nil
		}

		decision, ok := policy.DecisionFrom(ctx)
		if !ok {
			if batch, hasBatch := policy.DecisionsFrom(ctx); hasBatch {
				decision, ok = batch[tool.Name]
			}
		}
		if !ok {
			decision, err = s.authorize(ctx, id, tool)
			if err != nil {
				_ = s.record(ctx, id, tool, tool.RiskLevel, audit.ResultError, err.Error())
				return mcpgo.NewToolResultError("policy engine unavailable"), nil
			}
		}

		if !decision.Allow {
			if err := s.record(ctx, id, tool, decision.RiskLevel, audit.ResultDenied, decision.Reason); err != nil {
				return nil, fmt.Errorf("write audit record: %w", err)
			}
			return mcpgo.NewToolResultError("policy denied: " + decision.Reason), nil
		}

		injected, err := s.injectCredentials(ctx, tool)
		if err != nil {
			_ = s.record(ctx, id, tool, decision.RiskLevel, audit.ResultError, err.Error())
			return mcpgo.NewToolResultError("credential brokering failed: " + s.redactor.Redact(err.Error())), nil
		}

		result, err := s.executor.Execute(ctx, Call{
			Tool:        tool,
			Arguments:   request.GetArguments(),
			Credentials: injected,
		})
		if err != nil {
			_ = s.record(ctx, id, tool, decision.RiskLevel, audit.ResultError, err.Error())
			return mcpgo.NewToolResultError(s.redactor.Redact(err.Error())), nil
		}

		clean, err := s.sanitize(result)
		if err != nil {
			_ = s.record(ctx, id, tool, decision.RiskLevel, audit.ResultError, err.Error())
			return nil, fmt.Errorf("sanitize tool result: %w", err)
		}

		if err := s.record(ctx, id, tool, decision.RiskLevel, audit.ResultSuccess, ""); err != nil {
			return nil, fmt.Errorf("write audit record: %w", err)
		}
		return mcpgo.NewToolResultJSON(clean)
	}
}

func (s *Server) authorize(ctx context.Context, id identity.Identity, tool catalog.Tool) (policy.Decision, error) {
	req := policy.Request{
		Role:          id.Role,
		Tool:          tool.Name,
		ToolRiskLevel: tool.RiskLevel,
		AutonomyLevel: id.AutonomyLevel,
		AuthorID:      id.AuthorID.String(),
		RoleTools:     s.catalog.ToolNamesForRole(id.Role),
		RoleDeny:      s.catalog.DenyForRole(id.Role),
	}
	if id.ApproverID != uuid.Nil {
		req.ApproverID = id.ApproverID.String()
	}
	return s.authorizer.Authorize(ctx, req)
}

func (s *Server) injectCredentials(ctx context.Context, tool catalog.Tool) (map[string]string, error) {
	reqs := credentials.RequirementsFor(tool.Name)
	if len(reqs) == 0 {
		return nil, nil
	}
	if s.broker == nil {
		return nil, errors.New("credential brokering is not configured")
	}

	out := make(map[string]string, len(reqs))
	for _, req := range reqs {
		data, err := s.broker.Fetch(ctx, req.Path)
		if err != nil {
			return nil, err
		}
		value := data[req.Key]
		if value == "" {
			return nil, fmt.Errorf("%w: %s#%s", credentials.ErrSecretNotFound, req.Path, req.Key)
		}
		s.redactor.Add(value)
		out[req.Env] = value
	}
	return out, nil
}

func (s *Server) sanitize(result map[string]any) (map[string]any, error) {
	raw, err := json.Marshal(result)
	if err != nil {
		return nil, err
	}
	var clean map[string]any
	if err := json.Unmarshal([]byte(s.redactor.Redact(string(raw))), &clean); err != nil {
		return nil, err
	}
	return clean, nil
}

func (s *Server) record(ctx context.Context, id identity.Identity, tool catalog.Tool, riskLevel, result, errMsg string) error {
	_, err := s.auditor.Record(ctx, audit.Entry{
		WorkspaceID:  id.WorkspaceID,
		AgentID:      id.AgentID,
		ToolName:     tool.Name,
		RiskLevel:    riskLevel,
		Result:       result,
		ErrorMessage: s.redactor.Redact(errMsg),
	})
	return err
}
