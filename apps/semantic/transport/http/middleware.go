package httptransport

import (
	"net/http"

	"github.com/google/uuid"
	"github.com/sapelyuk/ai-sdlc-factory/apps/semantic/internal/identity"
)

// TenantContextMiddleware validates tenant headers.
//
// Status codes (Issue #71):
//   - Missing X-Workspace-ID / X-Author-ID → 401 Unauthorized
//     with WWW-Authenticate: Bearer.
//   - Invalid UUID format → 400 Bad Request.
//   - Valid credentials but insufficient permissions → 403 Forbidden.
func TenantContextMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Check for missing or zero headers first → 401 Unauthorized (Issue #71).
		workspaceHeader := r.Header.Get("X-Workspace-ID")
		if workspaceHeader == "" || workspaceHeader == uuid.Nil.String() {
			w.Header().Set("WWW-Authenticate", `Bearer realm="semantic"`)
			writeError(w, http.StatusUnauthorized, "missing X-Workspace-ID header")
			return
		}
		authorHeader := r.Header.Get("X-Author-ID")
		if authorHeader == "" || authorHeader == uuid.Nil.String() {
			w.Header().Set("WWW-Authenticate", `Bearer realm="semantic"`)
			writeError(w, http.StatusUnauthorized, "missing X-Author-ID header")
			return
		}
		// Validate UUID format → 400 Bad Request.
		workspaceID, err := uuid.Parse(workspaceHeader)
		if err != nil {
			writeError(w, http.StatusBadRequest, "X-Workspace-ID must be a valid UUID")
			return
		}
		authorID, err := uuid.Parse(authorHeader)
		if err != nil {
			writeError(w, http.StatusBadRequest, "X-Author-ID must be a valid UUID")
			return
		}
		next.ServeHTTP(w, r.WithContext(identity.WithTenant(r.Context(), workspaceID, authorID)))
	})
}
