// Package httptransport exposes the Tool Gateway HTTP API.
package httptransport

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"time"

	"github.com/google/uuid"
	"github.com/sapelyuk/ai-sdlc-factory/apps/tool-gateway/internal/audit"
	"github.com/sapelyuk/ai-sdlc-factory/apps/tool-gateway/internal/catalog"
	"github.com/sapelyuk/ai-sdlc-factory/apps/tool-gateway/internal/identity"
	"github.com/sapelyuk/ai-sdlc-factory/apps/tool-gateway/internal/policy"
)

// maxRequestBodyBytes bounds the JSON-RPC body accepted by the gateway.
const maxRequestBodyBytes = 8 << 20

// JSON-RPC error code for a policy denial.
const codePolicyDenied = -32001

// Pinger is the readiness dependency of GET /readyz.
type Pinger interface {
	Ping(ctx context.Context) error
}

// Options configures the router.
type Options struct {
	MCP        http.Handler
	Catalog    *catalog.Catalog
	Authorizer policy.Authorizer
	Auditor    *audit.Logger
	DB         Pinger
	Logger     *slog.Logger
}

// Router wraps the HTTP routes of the service.
type Router struct {
	handler http.Handler
}

// NewRouter creates the Tool Gateway router.
func NewRouter(opts Options) *Router {
	if opts.Logger == nil {
		opts.Logger = slog.Default()
	}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	})
	mux.HandleFunc("GET /readyz", func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
		defer cancel()

		if opts.DB != nil {
			if err := opts.DB.Ping(ctx); err != nil {
				writeJSON(w, http.StatusServiceUnavailable, map[string]string{"status": "database unavailable"})
				return
			}
		}
		if ready, ok := opts.Authorizer.(policy.Readier); ok {
			if err := ready.Ready(ctx); err != nil {
				writeJSON(w, http.StatusServiceUnavailable, map[string]string{"status": "policy engine unavailable"})
				return
			}
		}
		writeJSON(w, http.StatusOK, map[string]string{"status": "ready"})
	})

	mux.Handle("POST /mcp", identityMiddleware(policyMiddleware(opts, opts.MCP)))
	mux.Handle("GET /mcp", identityMiddleware(opts.MCP))
	mux.Handle("DELETE /mcp", identityMiddleware(opts.MCP))

	mux.HandleFunc("GET /admin/audit/verify", verifyAuditHandler(opts))

	return &Router{handler: mux}
}

// Handler returns the http.Handler.
func (r *Router) Handler() http.Handler { return r.handler }

// identityMiddleware validates the identity headers and stores the identity
// in the request context.
func identityMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id, err := identity.FromHeaders(r.Header)
		if err != nil {
			w.Header().Set("WWW-Authenticate", `ToolGateway realm="tool-gateway"`)
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": err.Error()})
			return
		}
		next.ServeHTTP(w, r.WithContext(identity.With(r.Context(), id)))
	})
}

// policyMiddleware authorizes every tools/call request before it reaches the
// MCP server, so a denied call is answered with HTTP 403.
func policyMiddleware(opts Options, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := readBody(r)
		if err != nil {
			writeJSON(w, http.StatusRequestEntityTooLarge, map[string]string{"error": err.Error()})
			return
		}
		r.Body = io.NopCloser(bytes.NewReader(body))

		calls := toolCalls(body)
		if len(calls) == 0 {
			next.ServeHTTP(w, r)
			return
		}

		id, err := identity.From(r.Context())
		if err != nil {
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": err.Error()})
			return
		}

		decisions := make(map[string]policy.Decision, len(calls))
		for _, call := range calls {
			decision, err := authorize(r.Context(), opts, id, call.Name)
			if err != nil {
				opts.Logger.Error("policy engine error", "tool", call.Name, "error", err)
				writeRPCError(w, call.ID, http.StatusServiceUnavailable,
					"policy engine unavailable", nil)
				return
			}
			if !decision.Allow {
				if err := recordDenied(r.Context(), opts, id, call.Name, decision); err != nil {
					opts.Logger.Error("write audit record", "tool", call.Name, "error", err)
				}
				writeRPCError(w, call.ID, http.StatusForbidden, "policy denied: "+decision.Reason,
					map[string]string{"risk_level": decision.RiskLevel, "tool": call.Name})
				return
			}
			decisions[call.Name] = decision
		}

		next.ServeHTTP(w, r.WithContext(policy.WithDecisions(r.Context(), decisions)))
	})
}

func authorize(ctx context.Context, opts Options, id identity.Identity, toolName string) (policy.Decision, error) {
	tool, ok := opts.Catalog.Tool(toolName)
	if !ok {
		return policy.Decision{
			Allow:     false,
			RiskLevel: "R0",
			Reason:    "unknown tool " + toolName,
		}, nil
	}

	req := policy.Request{
		Role:          id.Role,
		Tool:          tool.Name,
		ToolRiskLevel: tool.RiskLevel,
		AutonomyLevel: id.AutonomyLevel,
		AuthorID:      id.AuthorID.String(),
		RoleTools:     opts.Catalog.ToolNamesForRole(id.Role),
		RoleDeny:      opts.Catalog.DenyForRole(id.Role),
	}
	if id.ApproverID != uuid.Nil {
		req.ApproverID = id.ApproverID.String()
	}
	return opts.Authorizer.Authorize(ctx, req)
}

func recordDenied(ctx context.Context, opts Options, id identity.Identity, toolName string, decision policy.Decision) error {
	risk := decision.RiskLevel
	if risk == "" {
		if tool, ok := opts.Catalog.Tool(toolName); ok {
			risk = tool.RiskLevel
		} else {
			risk = "R0"
		}
	}
	_, err := opts.Auditor.Record(ctx, audit.Entry{
		WorkspaceID:  id.WorkspaceID,
		AgentID:      id.AgentID,
		ToolName:     toolName,
		RiskLevel:    risk,
		Result:       audit.ResultDenied,
		ErrorMessage: decision.Reason,
	})
	return err
}

type rpcCall struct {
	ID   json.RawMessage `json:"id"`
	Name string          `json:"name"`
}

type rpcEnvelope struct {
	ID     json.RawMessage `json:"id"`
	Method string          `json:"method"`
	Params struct {
		Name string `json:"name"`
	} `json:"params"`
}

// toolCalls extracts tools/call requests from a single or batched JSON-RPC
// body. Malformed bodies are left to the MCP server, which owns protocol
// error reporting.
func toolCalls(body []byte) []rpcCall {
	trimmed := bytes.TrimSpace(body)
	if len(trimmed) == 0 {
		return nil
	}

	var raw []json.RawMessage
	if trimmed[0] == '[' {
		if err := json.Unmarshal(trimmed, &raw); err != nil {
			return nil
		}
	} else {
		raw = []json.RawMessage{trimmed}
	}

	out := make([]rpcCall, 0, len(raw))
	for _, item := range raw {
		var env rpcEnvelope
		if err := json.Unmarshal(item, &env); err != nil {
			continue
		}
		if env.Method != "tools/call" {
			continue
		}
		out = append(out, rpcCall{ID: env.ID, Name: env.Params.Name})
	}
	return out
}

func readBody(r *http.Request) ([]byte, error) {
	defer func() { _ = r.Body.Close() }()
	body, err := io.ReadAll(io.LimitReader(r.Body, maxRequestBodyBytes+1))
	if err != nil {
		return nil, fmt.Errorf("read request body: %w", err)
	}
	if len(body) > maxRequestBodyBytes {
		return nil, errors.New("request body is too large")
	}
	return body, nil
}

func writeRPCError(w http.ResponseWriter, id json.RawMessage, status int, message string, data any) {
	if len(id) == 0 {
		id = json.RawMessage("null")
	}
	payload := map[string]any{
		"jsonrpc": "2.0",
		"id":      id,
		"error": map[string]any{
			"code":    codePolicyDenied,
			"message": message,
		},
	}
	if data != nil {
		payload["error"].(map[string]any)["data"] = data
	}
	writeJSON(w, status, payload)
}

func writeJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}

// verifyAuditHandler verifies the hash chain for a given workspace.
// Only accessible by admin role.
func verifyAuditHandler(opts Options) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		workspaceID := r.URL.Query().Get("workspace_id")
		if workspaceID == "" {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "workspace_id is required"})
			return
		}

		wsUUID, err := uuid.Parse(workspaceID)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid workspace_id"})
			return
		}

		// Check admin role from identity headers
		id, err := identity.FromHeaders(r.Header)
		if err != nil {
			w.Header().Set("WWW-Authenticate", `ToolGateway realm="tool-gateway"`)
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": err.Error()})
			return
		}
		if id.Role != "admin" {
			writeJSON(w, http.StatusForbidden, map[string]string{"error": "admin role required"})
			return
		}

		if err := opts.Auditor.Verify(r.Context(), wsUUID); err != nil {
			writeJSON(w, http.StatusOK, map[string]any{
				"status": "broken",
				"error":  err.Error(),
			})
			return
		}
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	}
}
