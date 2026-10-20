package handler

import (
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/sapelyuk/ai-sdlc-factory/apps/api/internal/identity"
	"github.com/sapelyuk/ai-sdlc-factory/apps/api/internal/registry/domain"
)

type processRequestMetadata struct {
	Name    string `json:"name"`
	Version string `json:"version,omitempty"`
}

type createProcessRequest struct {
	APIVersion string                 `json:"apiVersion,omitempty"`
	Kind       string                 `json:"kind,omitempty"`
	Metadata   processRequestMetadata `json:"metadata"`
	Spec       domain.ProcessSpec     `json:"spec"`
}

type updateProcessRequest struct {
	Spec domain.ProcessSpec `json:"spec"`
}

func (h *RegistryHandler) CreateProcess(w http.ResponseWriter, r *http.Request) {
	var request createProcessRequest
	if err := requestBody(r.Body, &request); err != nil {
		writeBodyError(w, err)
		return
	}
	if err := checkEnvelope(request.APIVersion, request.Kind, "Process"); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if !validName(request.Metadata.Name) {
		writeError(w, http.StatusBadRequest, "metadata.name must contain between 1 and 128 characters")
		return
	}
	if request.Metadata.Version != "" && request.Metadata.Version != "1.0.0" {
		writeError(w, http.StatusBadRequest, "new processes must start at version 1.0.0")
		return
	}
	if err := validateSpec(h.processSchema, request.Spec); err != nil {
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
	process := domain.NewProcess(workspaceID, authorID, request.Metadata.Name, request.Spec)
	if err := h.processes.Create(r.Context(), process); err != nil {
		writeRepositoryError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, process)
}

func (h *RegistryHandler) ListProcesses(w http.ResponseWriter, r *http.Request) {
	processes, err := h.processes.List(r.Context(), r.URL.Query().Get("status"))
	if err != nil {
		writeRepositoryError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, processes)
}

func (h *RegistryHandler) GetProcessByID(w http.ResponseWriter, r *http.Request) {
	id, ok := parseID(w, r)
	if !ok {
		return
	}
	process, err := h.processes.GetByID(r.Context(), id)
	if err != nil {
		writeRepositoryError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, process)
}

func (h *RegistryHandler) GetProcessByNameAndVersion(w http.ResponseWriter, r *http.Request) {
	process, err := h.processes.GetByNameAndVersion(r.Context(), chi.URLParam(r, "name"), chi.URLParam(r, "version"))
	if err != nil {
		writeRepositoryError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, process)
}

func (h *RegistryHandler) UpdateProcessSpec(w http.ResponseWriter, r *http.Request) {
	id, ok := parseID(w, r)
	if !ok {
		return
	}
	var request updateProcessRequest
	if err := requestBody(r.Body, &request); err != nil {
		writeBodyError(w, err)
		return
	}
	if err := validateSpec(h.processSchema, request.Spec); err != nil {
		writeError(w, http.StatusUnprocessableEntity, validationMessage(err))
		return
	}
	if err := h.processes.Update(r.Context(), id, request.Spec); err != nil {
		writeRepositoryError(w, err)
		return
	}
	process, err := h.processes.GetByID(r.Context(), id)
	if err != nil {
		writeRepositoryError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, process)
}

func (h *RegistryHandler) UpdateProcessStatus(w http.ResponseWriter, r *http.Request) {
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
	if err := h.processes.UpdateStatus(r.Context(), id, request.Status, approverID); err != nil {
		writeRepositoryError(w, err)
		return
	}
	process, err := h.processes.GetByID(r.Context(), id)
	if err != nil {
		writeRepositoryError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, process)
}

func (h *RegistryHandler) CloneProcess(w http.ResponseWriter, r *http.Request) {
	id, ok := parseID(w, r)
	if !ok {
		return
	}
	process, err := h.processes.Clone(r.Context(), id)
	if err != nil {
		writeRepositoryError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, process)
}

func (h *RegistryHandler) RevertProcess(w http.ResponseWriter, r *http.Request) {
	id, ok := parseID(w, r)
	if !ok {
		return
	}
	process, err := h.processes.Revert(r.Context(), id)
	if err != nil {
		writeRepositoryError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, process)
}
