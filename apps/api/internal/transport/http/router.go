// Package httptransport provides HTTP routing and middleware.
package httptransport

import (
	"context"
	"log/slog"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/go-chi/cors"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"

	approvalshandler "github.com/sapelyuk/ai-sdlc-factory/apps/api/internal/approvals/handler"
	"github.com/sapelyuk/ai-sdlc-factory/apps/api/internal/config"
	"github.com/sapelyuk/ai-sdlc-factory/apps/api/internal/iam"
	registryhandler "github.com/sapelyuk/ai-sdlc-factory/apps/api/internal/registry/handler"
)

// Router wraps chi.Mux with common middleware.
type Router struct {
	r *chi.Mux
}

// NewRouter creates a new router with JWT auth, RBAC, and all routes.
// approvalsHandler may be nil: then the approval queue routes are not mounted
// (used by tests that only exercise the registry).
func NewRouter(
	h *registryhandler.RegistryHandler,
	approvalsHandler *approvalshandler.ApprovalHandler,
	readinessCheck func(context.Context) error,
	cfg *config.Config,
	jwtService *iam.JWTService,
	loginHandler *LoginHandler,
) *Router {
	r := chi.NewMux()

	// ── Common middleware ──────────────────────────────────────────
	r.Use(middleware.RequestID)
	r.Use(otelhttp.NewMiddleware("api-server"))
	r.Use(MetricsMiddleware)
	r.Use(middleware.Recoverer)
	r.Use(middleware.Timeout(60 * time.Second))
	r.Use(cors.Handler(cors.Options{
		AllowedOrigins:   []string{"https://*", "http://*"},
		AllowedMethods:   []string{"GET", "POST", "PATCH", "DELETE", "OPTIONS"},
		AllowedHeaders:   []string{"Accept", "Authorization", "Content-Type", "X-Workspace-ID", "X-Author-ID", "X-Approver-ID"},
		ExposedHeaders:   []string{"Link"},
		AllowCredentials: false,
		MaxAge:           300,
	}))

	// ── Health endpoints (no auth) ─────────────────────────────────
	r.Get("/healthz", healthHandler)
	r.Get("/readyz", func(w http.ResponseWriter, r *http.Request) {
		if readinessCheck != nil {
			if err := readinessCheck(r.Context()); err != nil {
				slog.Error("readiness check failed", "error", err)
				jsonError(w, http.StatusServiceUnavailable, "database is unavailable")
				return
			}
		}
		readyHandler(w, r)
	})

	// ── Metrics (Prometheus, no auth) ──────────────────────────────
	r.Get("/metrics", promhttp.Handler().ServeHTTP)

	// ── Auth (login endpoint, no auth) ─────────────────────────────
	r.Post("/api/v1/auth/login", loginHandler.Login)

	// ── Protected routes (JWT auth + RBAC) ─────────────────────────
	r.Group(func(r chi.Router) {
		r.Use(JWTAuthMiddleware(jwtService, cfg.AllowLegacyAuth))
		r.Use(TenantContextMiddleware) // legacy fallback: adds workspace/author to context

		// RBAC helper: wraps a handler with a role requirement.
		wrapRBAC := func(requiredRole string, handler http.HandlerFunc) http.HandlerFunc {
			mw := RBACMiddleware(requiredRole)
			return func(w http.ResponseWriter, r *http.Request) {
				mw(handler).ServeHTTP(w, r)
			}
		}

		r.Route("/api/v1/registry/roles", func(r chi.Router) {
			r.Post("/", wrapRBAC("admin", h.CreateRole))
			r.Get("/", wrapRBAC("viewer", h.ListRoles))
			r.Get("/{id}", h.GetRoleByID)
			r.Get("/{name}/{version}", h.GetRoleByNameAndVersion)
			r.Patch("/{id}", wrapRBAC("admin", h.UpdateRoleSpec))
			r.Patch("/{id}/status", wrapRBAC("admin", h.UpdateRoleStatus))
			r.Post("/{id}/clone", wrapRBAC("admin", h.CloneRole))
			r.Post("/{id}/revert", wrapRBAC("admin", h.RevertRole))
		})
		r.Route("/api/v1/registry/teams", func(r chi.Router) {
			r.Post("/", wrapRBAC("admin", h.CreateTeam))
			r.Get("/", wrapRBAC("viewer", h.ListTeams))
			r.Get("/{id}", h.GetTeamByID)
			r.Get("/{name}/{version}", h.GetTeamByNameAndVersion)
			r.Patch("/{id}", wrapRBAC("admin", h.UpdateTeamSpec))
			r.Patch("/{id}/status", wrapRBAC("admin", h.UpdateTeamStatus))
			r.Post("/{id}/clone", wrapRBAC("admin", h.CloneTeam))
			r.Post("/{id}/revert", wrapRBAC("admin", h.RevertTeam))
		})
		r.Route("/api/v1/registry/processes", func(r chi.Router) {
			r.Post("/", wrapRBAC("admin", h.CreateProcess))
			r.Get("/", wrapRBAC("viewer", h.ListProcesses))
			r.Get("/{id}", h.GetProcessByID)
			r.Get("/{name}/{version}", h.GetProcessByNameAndVersion)
			r.Patch("/{id}", wrapRBAC("admin", h.UpdateProcessSpec))
			r.Patch("/{id}/status", wrapRBAC("admin", h.UpdateProcessStatus))
			r.Post("/{id}/clone", wrapRBAC("admin", h.CloneProcess))
			r.Post("/{id}/revert", wrapRBAC("admin", h.RevertProcess))
		})

		// ── Approval queue (human_approval gates, ADR-013) ───────────
		if approvalsHandler != nil {
			r.Route("/api/v1/approvals", func(r chi.Router) {
				r.Get("/", wrapRBAC("viewer", approvalsHandler.List))
				r.Get("/{id}", wrapRBAC("viewer", approvalsHandler.Get))
				r.Post("/{id}/approve", wrapRBAC("admin", approvalsHandler.Approve))
				r.Post("/{id}/reject", wrapRBAC("admin", approvalsHandler.Reject))
			})
		}
	})

	return &Router{r: r}
}

// Handler returns the http.Handler.
func (r *Router) Handler() http.Handler {
	return r.r
}

func healthHandler(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(`{"status":"ok"}`))
}

func readyHandler(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(`{"status":"ready"}`))
}
