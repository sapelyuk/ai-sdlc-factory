package httptransport

import (
	"context"
	"log/slog"
	"net/http"
	"strings"

	"github.com/google/uuid"
	"github.com/sapelyuk/ai-sdlc-factory/apps/api/internal/iam"
	"github.com/sapelyuk/ai-sdlc-factory/apps/api/internal/identity"
)

// authContextKey is a private key to store JWT claims in the request context.
type authContextKey struct{}

type authContextValue struct {
	UserID      string
	WorkspaceID string
	Role        string
}

// JWTAuthMiddleware validates the Bearer token and injects identity into the context.
// If the token is absent and legacy auth is allowed, it delegates to TenantContextMiddleware.
func JWTAuthMiddleware(jwtService *iam.JWTService, allowLegacy bool) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// Whitelisted paths — no auth required.
			switch r.URL.Path {
			case "/healthz", "/readyz", "/metrics":
				next.ServeHTTP(w, r)
				return
			}

			// POST /auth/login is also whitelisted.
			if r.URL.Path == "/api/v1/auth/login" && r.Method == http.MethodPost {
				next.ServeHTTP(w, r)
				return
			}

			authHeader := r.Header.Get("Authorization")
			if authHeader == "" {
				// Legacy auth fallback for local-dev only.
				if allowLegacy && r.Header.Get("X-Workspace-ID") != "" {
					TenantContextMiddleware(next).ServeHTTP(w, r)
					return
				}
				w.Header().Set("WWW-Authenticate", `Bearer realm="api"`)
				jsonError(w, http.StatusUnauthorized, "missing Authorization header")
				return
			}

			parts := strings.SplitN(authHeader, " ", 2)
			if len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") {
				w.Header().Set("WWW-Authenticate", `Bearer realm="api"`)
				jsonError(w, http.StatusUnauthorized, "invalid Authorization header format")
				return
			}

			claims, err := jwtService.ValidateToken(parts[1])
			if err != nil {
				slog.Debug("JWT validation failed", "error", err)
				w.Header().Set("WWW-Authenticate", `Bearer realm="api"`)
				jsonError(w, http.StatusUnauthorized, "invalid or expired token")
				return
			}

			// Inject identity into context for downstream handlers.
			wsID, wsErr := uuid.Parse(claims.WorkspaceID)
			if wsErr != nil || wsID == uuid.Nil {
				jsonError(w, http.StatusUnauthorized, "invalid workspace_id in token")
				return
			}
			userID, userErr := uuid.Parse(claims.UserID)
			if userErr != nil || userID == uuid.Nil {
				jsonError(w, http.StatusUnauthorized, "invalid user_id in token")
				return
			}

			ctx := identity.WithJWT(r.Context(), wsID, userID, claims.Role)
			ctx = context.WithValue(ctx, authContextKey{}, authContextValue{
				UserID:      claims.UserID,
				WorkspaceID: claims.WorkspaceID,
				Role:        claims.Role,
			})
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// GetAuthFromContext returns the JWT auth data from the request context.
func GetAuthFromContext(ctx context.Context) (authContextValue, bool) {
	v, ok := ctx.Value(authContextKey{}).(authContextValue)
	return v, ok
}
