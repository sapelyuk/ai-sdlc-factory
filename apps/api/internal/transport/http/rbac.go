package httptransport

import (
	"net/http"

	"github.com/sapelyuk/ai-sdlc-factory/apps/api/internal/iam"
	"github.com/sapelyuk/ai-sdlc-factory/apps/api/internal/identity"
)

// RBACMiddleware returns a middleware that requires at least the specified role.
func RBACMiddleware(requiredRole string) func(http.Handler) http.Handler {
	requiredLevel := iam.RoleLevel(requiredRole)
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			role := identity.UserRole(r.Context())
			if role == "" {
				// Legacy auth — role not set; allow (legacy mode is dev-only).
				next.ServeHTTP(w, r)
				return
			}
			if iam.RoleLevel(role) < requiredLevel {
				jsonError(w, http.StatusForbidden, "insufficient role: requires "+requiredRole)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}
