// Package handler exposes the approval queue over HTTP (FR-OPS-1).
package handler

import (
	"errors"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/sapelyuk/ai-sdlc-factory/apps/api/internal/approvals/domain"
	"github.com/sapelyuk/ai-sdlc-factory/apps/api/internal/approvals/repository"
	"github.com/sapelyuk/ai-sdlc-factory/apps/api/internal/identity"
)

// ApprovalHandler serves the /api/v1/approvals endpoints.
type ApprovalHandler struct {
	repo repository.ApprovalRepository
}

// NewApprovalHandler creates the handler.
func NewApprovalHandler(repo repository.ApprovalRepository) *ApprovalHandler {
	return &ApprovalHandler{repo: repo}
}

// List returns the pending approval requests of the active workspace.
// GET /api/v1/approvals?status=pending
func (h *ApprovalHandler) List(w http.ResponseWriter, r *http.Request) {
	var filter *domain.Status
	if raw := r.URL.Query().Get("status"); raw != "" {
		status := domain.Status(raw)
		if !validStatus(status) {
			writeError(w, http.StatusBadRequest,
				"status must be one of pending, approved, rejected, expired")
			return
		}
		filter = &status
	}
	approvals, err := h.repo.List(r.Context(), filter)
	if err != nil {
		writeRepositoryError(w, err)
		return
	}
	if approvals == nil {
		approvals = []*domain.ApprovalRequest{}
	}
	writeJSON(w, http.StatusOK, approvals)
}

// Get returns one approval request with its transition context.
// GET /api/v1/approvals/{id}
func (h *ApprovalHandler) Get(w http.ResponseWriter, r *http.Request) {
	id, ok := parseID(w, r)
	if !ok {
		return
	}
	req, err := h.repo.GetByID(r.Context(), id)
	if err != nil {
		writeRepositoryError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, req)
}

// Approve records the owner's positive decision.
// POST /api/v1/approvals/{id}/approve
func (h *ApprovalHandler) Approve(w http.ResponseWriter, r *http.Request) {
	h.decide(w, r, domain.StatusApproved)
}

// Reject records the owner's negative decision.
// POST /api/v1/approvals/{id}/reject
func (h *ApprovalHandler) Reject(w http.ResponseWriter, r *http.Request) {
	h.decide(w, r, domain.StatusRejected)
}

// decide applies a terminal decision by the authenticated user.
func (h *ApprovalHandler) decide(w http.ResponseWriter, r *http.Request, decision domain.Status) {
	id, ok := parseID(w, r)
	if !ok {
		return
	}
	// The approver is the authenticated principal, never a value from the
	// request body: an R3 approval must be attributable to a real user
	// (ADR-013, NFR-5).
	decidedBy, err := identity.AuthorID(r.Context())
	if err != nil {
		writeError(w, http.StatusBadRequest, "caller identity is required to decide an approval")
		return
	}
	var body domain.ApproveDecision
	if err := decodeBody(r, &body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body: "+err.Error())
		return
	}

	switch decision {
	case domain.StatusApproved:
		err = h.repo.Approve(r.Context(), id, decidedBy, body.Comment)
	case domain.StatusRejected:
		err = h.repo.Reject(r.Context(), id, decidedBy, body.Comment)
	}
	if errors.Is(err, repository.ErrNotPending) {
		// Distinguish "someone already answered" from "it does not exist".
		existing, getErr := h.repo.GetByID(r.Context(), id)
		if getErr != nil {
			writeRepositoryError(w, getErr)
			return
		}
		writeError(w, http.StatusConflict,
			"approval request is already decided with status "+string(existing.Status))
		return
	}
	if errors.Is(err, repository.ErrSelfApproval) {
		writeError(w, http.StatusForbidden, "approver cannot be the author")
		return
	}
	if err != nil {
		writeRepositoryError(w, err)
		return
	}

	updated, err := h.repo.GetByID(r.Context(), id)
	if err != nil {
		writeRepositoryError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, updated)
}

func validStatus(status domain.Status) bool {
	switch status {
	case domain.StatusPending, domain.StatusApproved,
		domain.StatusRejected, domain.StatusExpired:
		return true
	}
	return false
}

func parseID(w http.ResponseWriter, r *http.Request) (uuid.UUID, bool) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "id must be a valid UUID")
		return uuid.Nil, false
	}
	return id, true
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := encoder(w).Encode(value); err != nil {
		logError("failed to encode approval response", err)
	}
}

func writeError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]string{"error": message})
}

// writeRepositoryError maps repository failures onto the platform status
// codes documented in SYSTEM_DESIGN.md section 10.3.
func writeRepositoryError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, domain.ErrNotFound):
		writeError(w, http.StatusNotFound, "approval request not found")
	case errors.Is(err, identity.ErrMissingTenantContext):
		writeError(w, http.StatusBadRequest, err.Error())
	default:
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23514" {
			writeError(w, http.StatusUnprocessableEntity, "approval data violates a database constraint")
			return
		}
		writeError(w, http.StatusInternalServerError, "internal approval error")
	}
}
