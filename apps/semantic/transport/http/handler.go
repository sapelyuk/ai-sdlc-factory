package httptransport

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/sapelyuk/ai-sdlc-factory/apps/semantic/domain"
	"github.com/sapelyuk/ai-sdlc-factory/apps/semantic/internal/identity"
)

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

// parseID parses the "id" URL parameter as a UUID.
func parseID(w http.ResponseWriter, r *http.Request) (string, bool) {
	id := chi.URLParam(r, "id")
	if id == "" {
		writeError(w, http.StatusBadRequest, "id is required")
		return "", false
	}
	return id, true
}

// readBody reads and decodes the request body.
func readBody(r io.Reader, target any) error {
	data, err := io.ReadAll(io.LimitReader(r, 1<<20))
	if err != nil {
		return err
	}
	if err := json.Unmarshal(data, target); err != nil {
		return err
	}
	return nil
}

// writeRepositoryError maps repository errors to HTTP status codes.
func writeRepositoryError(w http.ResponseWriter, err error) {
	status, message := repoErrorStatus(err)
	writeError(w, status, message)
}

func repoErrorStatus(err error) (int, string) {
	switch {
	case errors.Is(err, domain.ErrNotFound) || errors.Is(err, domain.ErrEdgeNotFound):
		return http.StatusNotFound, "item not found"
	case errors.Is(err, domain.ErrForbidden):
		return http.StatusForbidden, "operation not allowed: node is approved"
	case errors.Is(err, domain.ErrConflict):
		return http.StatusConflict, "item already exists"
	case errors.Is(err, domain.ErrInvalidInput):
		return http.StatusUnprocessableEntity, err.Error()
	default:
		if errors.Is(err, identity.ErrMissingTenantContext) {
			return http.StatusBadRequest, err.Error()
		}
		return http.StatusInternalServerError, "internal error"
	}
}
