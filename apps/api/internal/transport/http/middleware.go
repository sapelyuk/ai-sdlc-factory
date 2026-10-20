package httptransport

import (
	"encoding/json"
	"net/http"

	"github.com/google/uuid"
	"github.com/sapelyuk/ai-sdlc-factory/apps/api/internal/identity"
)

// TenantContextMiddleware validates tenant headers.
// If the context already has workspace/author (e.g. from JWT), it passes through.
func TenantContextMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// If JWT already set workspace/author in context, skip header validation.
		if _, err := identity.WorkspaceID(r.Context()); err == nil {
			if _, err := identity.AuthorID(r.Context()); err == nil {
				next.ServeHTTP(w, r)
				return
			}
		}
		workspaceID, err := uuid.Parse(r.Header.Get("X-Workspace-ID"))
		if err != nil || workspaceID == uuid.Nil {
			jsonError(w, http.StatusBadRequest, "X-Workspace-ID must be a valid UUID")
			return
		}
		authorID, err := uuid.Parse(r.Header.Get("X-Author-ID"))
		if err != nil || authorID == uuid.Nil {
			jsonError(w, http.StatusBadRequest, "X-Author-ID must be a valid UUID")
			return
		}
		next.ServeHTTP(w, r.WithContext(identity.WithTenant(r.Context(), workspaceID, authorID)))
	})
}

// jsonResponse writes a JSON response with the given status code.
func jsonResponse(w http.ResponseWriter, status int, data any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(data)
}

// jsonError writes a JSON error response.
func jsonError(w http.ResponseWriter, status int, message string) {
	jsonResponse(w, status, map[string]string{"error": message})
}
