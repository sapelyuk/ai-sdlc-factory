package handler

import (
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/sapelyuk/ai-sdlc-factory/apps/api/internal/identity"
	"github.com/sapelyuk/ai-sdlc-factory/apps/api/internal/registry/domain"
)

type roleRequestMetadata struct {
	Name     string          `json:"name"`
	Version  string          `json:"version,omitempty"`
	Category domain.Category `json:"category"`
}

type createRoleRequest struct {
	APIVersion string              `json:"apiVersion,omitempty"`
	Kind       string              `json:"kind,omitempty"`
	Metadata   roleRequestMetadata `json:"metadata"`
	Spec       domain.RoleSpec     `json:"spec"`
}

type updateRoleRequest struct {
	Spec domain.RoleSpec `json:"spec"`
}

type updateStatusRequest struct {
	Status domain.Status `json:"status"`
}

func (h *RegistryHandler) CreateRole(w http.ResponseWriter, r *http.Request) {
	var request createRoleRequest
	if err := requestBody(r.Body, &request); err != nil {
		writeBodyError(w, err)
		return
	}
	if err := checkEnvelope(request.APIVersion, request.Kind, "Role"); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if !validName(request.Metadata.Name) {
		writeError(w, http.StatusBadRequest, "metadata.name must contain between 1 and 128 characters")
		return
	}
	if !request.Metadata.Category.IsValid() {
		writeError(w, http.StatusBadRequest, "metadata.category must be one of product, design, engineering, qa, devops, research, security")
		return
	}
	if request.Metadata.Version != "" && request.Metadata.Version != "1.0.0" {
		writeError(w, http.StatusBadRequest, "new roles must start at version 1.0.0")
		return
	}
	if err := validateSpec(h.roleSchema, request.Spec); err != nil {
		writeError(w, http.StatusUnprocessableEntity, validationMessage(err))
		return
	}
	workspaceID, err := identity.WorkspaceID(r.Context())
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	authorID, err := identity.AuthorID(r.Context())
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	role := domain.NewRole(workspaceID, authorID, request.Metadata.Name, request.Metadata.Category, request.Spec)
	if err := h.roles.Create(r.Context(), role); err != nil {
		writeRepositoryError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, role)
}

func (h *RegistryHandler) ListRoles(w http.ResponseWriter, r *http.Request) {
	roles, err := h.roles.List(r.Context(), r.URL.Query().Get("category"), r.URL.Query().Get("status"))
	if err != nil {
		writeRepositoryError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, roles)
}

func (h *RegistryHandler) GetRoleByID(w http.ResponseWriter, r *http.Request) {
	id, ok := parseID(w, r)
	if !ok {
		return
	}
	role, err := h.roles.GetByID(r.Context(), id)
	if err != nil {
		writeRepositoryError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, role)
}

func (h *RegistryHandler) GetRoleByNameAndVersion(w http.ResponseWriter, r *http.Request) {
	role, err := h.roles.GetByNameAndVersion(r.Context(), chi.URLParam(r, "name"), chi.URLParam(r, "version"))
	if err != nil {
		writeRepositoryError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, role)
}

func (h *RegistryHandler) UpdateRoleSpec(w http.ResponseWriter, r *http.Request) {
	id, ok := parseID(w, r)
	if !ok {
		return
	}
	var request updateRoleRequest
	if err := requestBody(r.Body, &request); err != nil {
		writeBodyError(w, err)
		return
	}
	if err := validateSpec(h.roleSchema, request.Spec); err != nil {
		writeError(w, http.StatusUnprocessableEntity, validationMessage(err))
		return
	}
	if err := h.roles.Update(r.Context(), id, request.Spec); err != nil {
		writeRepositoryError(w, err)
		return
	}
	role, err := h.roles.GetByID(r.Context(), id)
	if err != nil {
		writeRepositoryError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, role)
}

func (h *RegistryHandler) UpdateRoleStatus(w http.ResponseWriter, r *http.Request) {
	id, ok := parseID(w, r)
	if !ok {
		return
	}
	var request updateStatusRequest
	if err := requestBody(r.Body, &request); err != nil {
		writeBodyError(w, err)
		return
	}
	approverID, err := resolveApproverID(r.Context(), r)
	if err != nil || approverID == uuid.Nil {
		writeError(w, http.StatusBadRequest, "approver ID is required (JWT sub or X-Approver-ID)")
		return
	}
	if err := h.roles.UpdateStatus(r.Context(), id, request.Status, approverID); err != nil {
		writeRepositoryError(w, err)
		return
	}
	role, err := h.roles.GetByID(r.Context(), id)
	if err != nil {
		writeRepositoryError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, role)
}

func (h *RegistryHandler) CloneRole(w http.ResponseWriter, r *http.Request) {
	id, ok := parseID(w, r)
	if !ok {
		return
	}
	role, err := h.roles.Clone(r.Context(), id)
	if err != nil {
		writeRepositoryError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, role)
}

func (h *RegistryHandler) RevertRole(w http.ResponseWriter, r *http.Request) {
	id, ok := parseID(w, r)
	if !ok {
		return
	}
	role, err := h.roles.Revert(r.Context(), id)
	if err != nil {
		writeRepositoryError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, role)
}
