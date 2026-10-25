// Package httptransport exposes the LLM Gateway HTTP API.
package httptransport

import (
	"context"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/go-chi/cors"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/sapelyuk/ai-sdlc-factory/apps/gateway-llm/internal/identity"
	"github.com/sapelyuk/ai-sdlc-factory/apps/gateway-llm/internal/routing"
)

// Router wraps chi.Mux with common middleware and routes.
type Router struct {
	r *chi.Mux
}

// NewRouter creates a new router with all routes.
func NewRouter(chat *routing.Router, pool *pgxpool.Pool) *Router {
	r := chi.NewMux()

	r.Use(middleware.RequestID)
	r.Use(middleware.Recoverer)
	r.Use(middleware.Timeout(5 * time.Minute))
	r.Use(cors.Handler(cors.Options{
		AllowedOrigins:   []string{"https://*", "http://*"},
		AllowedMethods:   []string{"GET", "POST", "OPTIONS"},
		AllowedHeaders:   []string{"Accept", "Content-Type", "X-Workspace-ID", "X-Author-ID"},
		AllowCredentials: false,
		MaxAge:           300,
	}))

	// Health endpoints (no auth).
	r.Get("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"status":"ok"}`))
	})
	r.Get("/readyz", func(w http.ResponseWriter, req *http.Request) {
		ctx, cancel := context.WithTimeout(req.Context(), 2*time.Second)
		defer cancel()
		if err := pool.Ping(ctx); err != nil {
			writeError(w, http.StatusServiceUnavailable, "database unavailable")
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"status":"ready"}`))
	})

	// Tenant-context-protected LLM routes.
	r.Group(func(r chi.Router) {
		r.Use(TenantContextMiddleware)
		r.Post("/v1/llm/chat", chatHandler(chat))
	})

	return &Router{r: r}
}

// Handler returns the http.Handler.
func (r *Router) Handler() http.Handler {
	return r.r
}

// TenantContextMiddleware validates tenant headers.
//
// Status codes follow the platform convention (SYSTEM_DESIGN.md §10.3):
//   - Missing X-Workspace-ID / X-Author-ID → 401 Unauthorized.
//   - Invalid UUID format → 400 Bad Request.
func TenantContextMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		workspaceHeader := r.Header.Get("X-Workspace-ID")
		if workspaceHeader == "" {
			w.Header().Set("WWW-Authenticate", `Bearer realm="llm-gateway"`)
			writeError(w, http.StatusUnauthorized, "missing X-Workspace-ID header")
			return
		}
		authorHeader := r.Header.Get("X-Author-ID")
		if authorHeader == "" {
			w.Header().Set("WWW-Authenticate", `Bearer realm="llm-gateway"`)
			writeError(w, http.StatusUnauthorized, "missing X-Author-ID header")
			return
		}
		workspaceID, err := uuid.Parse(workspaceHeader)
		if err != nil {
			writeError(w, http.StatusBadRequest, "X-Workspace-ID must be a valid UUID")
			return
		}
		if workspaceID == uuid.Nil {
			w.Header().Set("WWW-Authenticate", `Bearer realm="llm-gateway"`)
			writeError(w, http.StatusUnauthorized, "missing X-Workspace-ID header")
			return
		}
		authorID, err := uuid.Parse(authorHeader)
		if err != nil {
			writeError(w, http.StatusBadRequest, "X-Author-ID must be a valid UUID")
			return
		}
		if authorID == uuid.Nil {
			w.Header().Set("WWW-Authenticate", `Bearer realm="llm-gateway"`)
			writeError(w, http.StatusUnauthorized, "missing X-Author-ID header")
			return
		}
		next.ServeHTTP(w, r.WithContext(identity.WithTenant(r.Context(), workspaceID, authorID)))
	})
}
