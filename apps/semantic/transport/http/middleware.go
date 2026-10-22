package httptransport

import (
	"net/http"

	"github.com/google/uuid"
	"github.com/sapelyuk/ai-sdlc-factory/apps/semantic/internal/identity"
)

// TenantContextMiddleware validates tenant headers.
func TenantContextMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		workspaceID, err := uuid.Parse(r.Header.Get("X-Workspace-ID"))
		if err != nil || workspaceID == uuid.Nil {
			writeError(w, http.StatusBadRequest, "X-Workspace-ID must be a valid UUID")
			return
		}
		authorID, err := uuid.Parse(r.Header.Get("X-Author-ID"))
		if err != nil || authorID == uuid.Nil {
			writeError(w, http.StatusBadRequest, "X-Author-ID must be a valid UUID")
			return
		}
		next.ServeHTTP(w, r.WithContext(identity.WithTenant(r.Context(), workspaceID, authorID)))
	})
}
