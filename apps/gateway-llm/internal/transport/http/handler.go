package httptransport

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"

	"github.com/google/uuid"
	"github.com/sapelyuk/ai-sdlc-factory/apps/gateway-llm/internal/domain"
	"github.com/sapelyuk/ai-sdlc-factory/apps/gateway-llm/internal/identity"
	"github.com/sapelyuk/ai-sdlc-factory/apps/gateway-llm/internal/routing"
)

// chatRequest is the POST /v1/llm/chat payload.
type chatRequest struct {
	Profile  string               `json:"profile"`
	Messages []domain.ChatMessage `json:"messages"`
	Tools    []domain.ToolDef     `json:"tools,omitempty"`
	RunID    *uuid.UUID           `json:"run_id,omitempty"`
	TaskID   *uuid.UUID           `json:"task_id,omitempty"`
	AgentID  *uuid.UUID           `json:"agent_id,omitempty"`
}

// usageResponse carries dual-level cost (ADR-021):
// cost_usd is the cost of the successful attempt (final_cost_usd),
// total_cost_usd is the sum over all attempts (actual spend).
type usageResponse struct {
	TokensIn       int     `json:"tokens_in"`
	TokensOut      int     `json:"tokens_out"`
	CostUSD        float64 `json:"cost_usd"`
	TotalTokensIn  int     `json:"total_tokens_in"`
	TotalTokensOut int     `json:"total_tokens_out"`
	TotalCostUSD   float64 `json:"total_cost_usd"`
}

// chatResponse is the POST /v1/llm/chat response.
type chatResponse struct {
	LogicalCallID string        `json:"logical_call_id"`
	Status        string        `json:"status"`
	ModelUsed     string        `json:"model_used"`
	ProviderUsed  string        `json:"provider_used"`
	Content       string        `json:"content"`
	Usage         usageResponse `json:"usage"`
}

// chatHandler handles POST /v1/llm/chat.
func chatHandler(router *routing.Router) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		workspaceID, err := identity.WorkspaceID(r.Context())
		if err != nil {
			writeError(w, http.StatusUnauthorized, err.Error())
			return
		}
		_ = workspaceID // tenant scope is enforced via RLS set_config in the repository.

		var req chatRequest
		if err := readBody(r.Body, &req); err != nil {
			writeBodyError(w, err)
			return
		}
		if req.Profile == "" {
			writeError(w, http.StatusUnprocessableEntity, "profile is required")
			return
		}
		if len(req.Messages) == 0 {
			writeError(w, http.StatusUnprocessableEntity, "messages must not be empty")
			return
		}

		chatReq := domain.ChatRequest{Messages: req.Messages, Tools: req.Tools}
		meta := routing.CallMeta{RunID: req.RunID, TaskID: req.TaskID, AgentID: req.AgentID}

		res, chatErr := router.Chat(r.Context(), req.Profile, chatReq, meta)

		if chatErr == nil {
			writeJSON(w, http.StatusOK, toChatResponse(res))
			return
		}

		// Routing reached providers but every attempt failed → 502.
		if errors.Is(chatErr, domain.ErrAllFailed) && res != nil && res.LogicalCall != nil {
			writeJSON(w, http.StatusBadGateway, toChatResponse(res))
			return
		}
		switch {
		case errors.Is(chatErr, domain.ErrUnknownProfile):
			writeError(w, http.StatusBadRequest, chatErr.Error())
		case errors.Is(chatErr, domain.ErrNoAvailable):
			// All catalog models of the profile are in cooldown.
			writeError(w, http.StatusServiceUnavailable, chatErr.Error())
		case errors.Is(chatErr, identity.ErrMissingTenantContext):
			writeError(w, http.StatusUnauthorized, chatErr.Error())
		default:
			writeError(w, http.StatusInternalServerError, "internal error")
		}
	}
}

// toChatResponse maps a routing result to the wire format.
func toChatResponse(res *routing.Result) chatResponse {
	call := res.LogicalCall
	out := chatResponse{
		LogicalCallID: call.ID.String(),
		Status:        call.Status,
		ModelUsed:     call.ModelIDUsed,
		ProviderUsed:  call.ProviderUsed,
		Content:       res.Content,
		Usage: usageResponse{
			TotalTokensIn:  call.TotalTokensIn,
			TotalTokensOut: call.TotalTokensOut,
			TotalCostUSD:   call.TotalCostUSD,
		},
	}
	if call.FinalTokensIn != nil {
		out.Usage.TokensIn = *call.FinalTokensIn
	}
	if call.FinalTokensOut != nil {
		out.Usage.TokensOut = *call.FinalTokensOut
	}
	if call.FinalCostUSD != nil {
		out.Usage.CostUSD = *call.FinalCostUSD
	}
	return out
}

// ── Response helpers ──────────────────────────────────────────

// writeJSON writes a JSON response with the given status code.
func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

// writeError writes a JSON error response.
func writeError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]string{"error": message})
}

// writeBodyError writes a 400 error for invalid JSON bodies.
func writeBodyError(w http.ResponseWriter, err error) {
	writeError(w, http.StatusBadRequest, "invalid JSON request body: "+err.Error())
}

// readBody reads and decodes the request body (1 MB limit).
func readBody(r io.Reader, target any) error {
	data, err := io.ReadAll(io.LimitReader(r, 1<<20))
	if err != nil {
		return err
	}
	return json.Unmarshal(data, target)
}
