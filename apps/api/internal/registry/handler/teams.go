package handler

import (
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/sapelyuk/ai-sdlc-factory/apps/api/internal/identity"
	"github.com/sapelyuk/ai-sdlc-factory/apps/api/internal/registry/domain"
)

type teamRequestMetadata struct {
	Name    string `json:"name"`
	Version string `json:"version,omitempty"`
}

type createTeamRequest struct {
	APIVersion string              `json:"apiVersion,omitempty"`
	Kind       string              `json:"kind,omitempty"`
	Metadata   teamRequestMetadata `json:"metadata"`
	Spec       domain.TeamSpec     `json:"spec"`
}

type updateTeamRequest struct {
	Spec domain.TeamSpec `json:"spec"`
}

func (h *RegistryHandler) CreateTeam(w http.ResponseWriter, r *http.Request) {
	var request createTeamRequest
	if err := requestBody(r.Body, &request); err != nil {
		writeBodyError(w, err)
		return
	}
	if err := checkEnvelope(request.APIVersion, request.Kind, "Team"); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if !validName(request.Metadata.Name) {
		writeError(w, http.StatusBadRequest, "metadata.name must contain between 1 and 128 characters")
		return
	}
	if request.Metadata.Version != "" && request.Metadata.Version != "1.0.0" {
		writeError(w, http.StatusBadRequest, "new teams must start at version 1.0.0")
		return
	}
	if err := validateSpec(h.teamSchema, request.Spec); err != nil {
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
	team := domain.NewTeam(workspaceID, authorID, request.Metadata.Name, request.Spec)
	if err := h.teams.Create(r.Context(), team); err != nil {
		writeRepositoryError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, team)
}

func (h *RegistryHandler) ListTeams(w http.ResponseWriter, r *http.Request) {
	teams, err := h.teams.List(r.Context(), r.URL.Query().Get("status"))
	if err != nil {
		writeRepositoryError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, teams)
}

func (h *RegistryHandler) GetTeamByID(w http.ResponseWriter, r *http.Request) {
	id, ok := parseID(w, r)
	if !ok {
		return
	}
	team, err := h.teams.GetByID(r.Context(), id)
	if err != nil {
		writeRepositoryError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, team)
}

func (h *RegistryHandler) GetTeamByNameAndVersion(w http.ResponseWriter, r *http.Request) {
	team, err := h.teams.GetByNameAndVersion(r.Context(), chi.URLParam(r, "name"), chi.URLParam(r, "version"))
	if err != nil {
		writeRepositoryError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, team)
}

func (h *RegistryHandler) UpdateTeamSpec(w http.ResponseWriter, r *http.Request) {
	id, ok := parseID(w, r)
	if !ok {
		return
	}
	var request updateTeamRequest
	if err := requestBody(r.Body, &request); err != nil {
		writeBodyError(w, err)
		return
	}
	if err := validateSpec(h.teamSchema, request.Spec); err != nil {
		writeError(w, http.StatusUnprocessableEntity, validationMessage(err))
		return
	}
	if err := h.teams.Update(r.Context(), id, request.Spec); err != nil {
		writeRepositoryError(w, err)
		return
	}
	team, err := h.teams.GetByID(r.Context(), id)
	if err != nil {
		writeRepositoryError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, team)
}

func (h *RegistryHandler) UpdateTeamStatus(w http.ResponseWriter, r *http.Request) {
	id, ok := parseID(w, r)
	if !ok {
		return
	}
	var request updateStatusRequest
	if err := requestBody(r.Body, &request); err != nil {
		writeBodyError(w, err)
		return
	}
	approverID, err := uuid.Parse(r.Header.Get("X-Approver-ID"))
	if err != nil || approverID == uuid.Nil {
		writeError(w, http.StatusBadRequest, "X-Approver-ID must be a valid UUID")
		return
	}
	if err := h.teams.UpdateStatus(r.Context(), id, request.Status, approverID); err != nil {
		writeRepositoryError(w, err)
		return
	}
	team, err := h.teams.GetByID(r.Context(), id)
	if err != nil {
		writeRepositoryError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, team)
}

func (h *RegistryHandler) CloneTeam(w http.ResponseWriter, r *http.Request) {
	id, ok := parseID(w, r)
	if !ok {
		return
	}
	team, err := h.teams.Clone(r.Context(), id)
	if err != nil {
		writeRepositoryError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, team)
}

func (h *RegistryHandler) RevertTeam(w http.ResponseWriter, r *http.Request) {
	id, ok := parseID(w, r)
	if !ok {
		return
	}
	team, err := h.teams.Revert(r.Context(), id)
	if err != nil {
		writeRepositoryError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, team)
}
