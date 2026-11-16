package httptransport

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"time"

	"github.com/google/uuid"
	"github.com/sapelyuk/ai-sdlc-factory/apps/gateway-llm/internal/domain"
	"github.com/sapelyuk/ai-sdlc-factory/apps/gateway-llm/internal/identity"
	"github.com/sapelyuk/ai-sdlc-factory/apps/gateway-llm/internal/routing"
	"github.com/sapelyuk/ai-sdlc-factory/packages/sdk-go/idempotency"
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

// ChatRouter is the routing dependency of POST /v1/llm/chat.
// *routing.Router implements it; tests substitute a stub so the HTTP layer is
// covered without a database or live providers.
type ChatRouter interface {
	Chat(ctx context.Context, profileName string, req domain.ChatRequest, meta routing.CallMeta) (*routing.Result, error)
}

// chatHandler handles POST /v1/llm/chat.
// The cache parameter is optional (nil = no caching, fail-open).
func chatHandler(router ChatRouter, cache *idempotency.Cache) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		workspaceID, err := identity.WorkspaceID(r.Context())
		if err != nil {
			writeError(w, http.StatusUnauthorized, err.Error())
			return
		}

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

		// ── Idempotency cache: claim ──────────────────────────────
		idempotencyKey := r.Header.Get("X-Idempotency-Key")
		if idempotencyKey != "" && cache != nil {
			entry, hit, err := cache.Claim(r.Context(), "llm", workspaceID.String(), idempotencyKey, 60*time.Second)
			if errors.Is(err, idempotency.ErrPending) {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusConflict)
				_, _ = w.Write([]byte(`{"error":"concurrent request with same idempotency key in progress"}`))
				return
			}
			if err != nil {
				// fail-open: proceed without cache (metrics tracked by cache pkg)
			} else if hit && entry != nil {
				w.Header().Set("Content-Type", "application/json")
				w.Header().Set("X-Idempotency-Cache", "hit")
				w.WriteHeader(http.StatusOK)
				_, _ = w.Write(entry.ResponseJSON)
				return
			}
		}

		chatReq := domain.ChatRequest{Messages: req.Messages, Tools: req.Tools}
		meta := routing.CallMeta{RunID: req.RunID, TaskID: req.TaskID, AgentID: req.AgentID}

		res, chatErr := router.Chat(r.Context(), req.Profile, chatReq, meta)

		if chatErr == nil {
			resp := toChatResponse(res)
			respJSON, _ := json.Marshal(resp)

			// ── Idempotency cache: complete ────────────────────────
			if idempotencyKey != "" && cache != nil {
				_ = cache.Complete(r.Context(), "llm", workspaceID.String(), idempotencyKey, idempotency.Entry{
					LogicalCallID: res.LogicalCall.ID.String(),
					ResponseJSON:  respJSON,
					ProviderUsed:  res.LogicalCall.ProviderUsed,
					ModelUsed:     res.LogicalCall.ModelIDUsed,
				}, 24*time.Hour)
			}

			w.Header().Set("Content-Type", "application/json")
			if idempotencyKey != "" && cache != nil {
				w.Header().Set("X-Idempotency-Cache", "miss")
			}
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write(respJSON)
			return
		}

		// ── Idempotency cache: mark failed ───────────────────────
		if idempotencyKey != "" && cache != nil {
			_ = cache.MarkFailed(r.Context(), "llm", workspaceID.String(), idempotencyKey)
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
