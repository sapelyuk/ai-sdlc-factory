package handler

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/sapelyuk/ai-sdlc-factory/apps/api/internal/identity"
	"github.com/sapelyuk/ai-sdlc-factory/apps/api/internal/registry/domain"
	"github.com/sapelyuk/ai-sdlc-factory/apps/api/internal/registry/repository"
)

type roleRepositoryStub struct {
	repository.RoleRepository
	created   *domain.Role
	createErr error
	role      *domain.Role
	roles     []*domain.Role
	listErr   error
	cloned    *domain.Role
	reverted  *domain.Role
	getErr    error
	updateErr error
	statusErr error
}

func (s *roleRepositoryStub) Create(_ context.Context, role *domain.Role) error {
	if s.createErr != nil {
		return s.createErr
	}
	s.created = role
	return nil
}

func (s *roleRepositoryStub) GetByID(context.Context, uuid.UUID) (*domain.Role, error) {
	return s.role, s.getErr
}

func (s *roleRepositoryStub) GetByNameAndVersion(context.Context, string, string) (*domain.Role, error) {
	return s.role, s.getErr
}

func (s *roleRepositoryStub) List(context.Context, string, string) ([]*domain.Role, error) {
	return s.roles, s.listErr
}

func (s *roleRepositoryStub) Update(context.Context, uuid.UUID, domain.RoleSpec) error {
	return s.updateErr
}

func (s *roleRepositoryStub) UpdateStatus(_ context.Context, _ uuid.UUID, _ domain.Status, approverID uuid.UUID) error {
	if s.statusErr != nil {
		return s.statusErr
	}
	return nil
}

func (s *roleRepositoryStub) Clone(context.Context, uuid.UUID) (*domain.Role, error) {
	return s.cloned, s.getErr
}

func (s *roleRepositoryStub) Revert(context.Context, uuid.UUID) (*domain.Role, error) {
	return s.reverted, s.getErr
}

type teamRepositoryStub struct {
	repository.TeamRepository
	created   *domain.Team
	createErr error
	team      *domain.Team
	teams     []*domain.Team
	listErr   error
	cloned    *domain.Team
	reverted  *domain.Team
	getErr    error
	updateErr error
	statusErr error
}

func (s *teamRepositoryStub) Create(_ context.Context, team *domain.Team) error {
	if s.createErr != nil {
		return s.createErr
	}
	s.created = team
	return nil
}

func (s *teamRepositoryStub) GetByID(context.Context, uuid.UUID) (*domain.Team, error) {
	return s.team, s.getErr
}

func (s *teamRepositoryStub) GetByNameAndVersion(context.Context, string, string) (*domain.Team, error) {
	return s.team, s.getErr
}

func (s *teamRepositoryStub) List(context.Context, string) ([]*domain.Team, error) {
	return s.teams, s.listErr
}

func (s *teamRepositoryStub) Update(context.Context, uuid.UUID, domain.TeamSpec) error {
	return s.updateErr
}

func (s *teamRepositoryStub) UpdateStatus(context.Context, uuid.UUID, domain.Status, uuid.UUID) error {
	return s.statusErr
}

func (s *teamRepositoryStub) Clone(context.Context, uuid.UUID) (*domain.Team, error) {
	return s.cloned, s.getErr
}

func (s *teamRepositoryStub) Revert(context.Context, uuid.UUID) (*domain.Team, error) {
	return s.reverted, s.getErr
}

type processRepositoryStub struct {
	repository.ProcessRepository
	created   *domain.Process
	createErr error
	process   *domain.Process
	processes []*domain.Process
	listErr   error
	cloned    *domain.Process
	reverted  *domain.Process
	getErr    error
	updateErr error
	statusErr error
}

func (s *processRepositoryStub) Create(_ context.Context, process *domain.Process) error {
	if s.createErr != nil {
		return s.createErr
	}
	s.created = process
	return nil
}

func (s *processRepositoryStub) GetByID(context.Context, uuid.UUID) (*domain.Process, error) {
	return s.process, s.getErr
}

func (s *processRepositoryStub) GetByNameAndVersion(context.Context, string, string) (*domain.Process, error) {
	return s.process, s.getErr
}

func (s *processRepositoryStub) List(context.Context, string) ([]*domain.Process, error) {
	return s.processes, s.listErr
}

func (s *processRepositoryStub) Update(context.Context, uuid.UUID, domain.ProcessSpec) error {
	return s.updateErr
}

func (s *processRepositoryStub) UpdateStatus(context.Context, uuid.UUID, domain.Status, uuid.UUID) error {
	return s.statusErr
}

func (s *processRepositoryStub) Clone(context.Context, uuid.UUID) (*domain.Process, error) {
	return s.cloned, s.getErr
}

func (s *processRepositoryStub) Revert(context.Context, uuid.UUID) (*domain.Process, error) {
	return s.reverted, s.getErr
}

func newTestRegistryHandler(t *testing.T, roles repository.RoleRepository, teams repository.TeamRepository, processes repository.ProcessRepository) *RegistryHandler {
	t.Helper()
	handler, err := NewRegistryHandler(roles, teams, processes)
	if err != nil {
		t.Fatalf("NewRegistryHandler() error = %v", err)
	}
	return handler
}

func tenantRequest(method, path, body string, workspaceID, authorID uuid.UUID) *http.Request {
	request := httptest.NewRequest(method, path, strings.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("X-Workspace-ID", workspaceID.String())
	request.Header.Set("X-Author-ID", authorID.String())
	return request.WithContext(identity.WithTenant(request.Context(), workspaceID, authorID))
}

func TestCreateRoleUsesTenantContextAndReturnsInitialVersion(t *testing.T) {
	roles := &roleRepositoryStub{}
	h := newTestRegistryHandler(t, roles, &teamRepositoryStub{}, &processRepositoryStub{})
	workspaceID, authorID := uuid.New(), uuid.New()
	body := `{"apiVersion":"ai-sdlc-factory/v1","kind":"Role","metadata":{"name":"backend-developer","category":"engineering"},"spec":{"description":"Builds backend services.","system_prompt_ref":"packages/ontology/prompts/backend-developer.yaml","inputs":[{"name":"requirements","type":"document"}],"outputs":[{"name":"implementation","type":"code"}],"tools":{"allow":["fs.read"]},"model":{"capability":"code-strong"},"limits":{"max_steps":60,"max_cost_usd":3},"autonomy":"balanced"}}`
	request := tenantRequest(http.MethodPost, "/roles", body, workspaceID, authorID)
	response := httptest.NewRecorder()
	h.CreateRole(response, request)

	if response.Code != http.StatusCreated {
		t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
	}
	if roles.created == nil {
		t.Fatal("repository did not receive the new role")
	}
	if roles.created.WorkspaceID != workspaceID || roles.created.AuthorID != authorID {
		t.Fatalf("tenant context was not used: workspace=%s author=%s", roles.created.WorkspaceID, roles.created.AuthorID)
	}
	if got := roles.created.VersionString(); got != "1.0.0" {
		t.Fatalf("initial version = %s, want 1.0.0", got)
	}
	if roles.created.Status != domain.StatusDraft {
		t.Fatalf("initial status = %s, want draft", roles.created.Status)
	}
}

func TestCreateRoleRejectsInvalidSpecWithoutRepositoryWrite(t *testing.T) {
	roles := &roleRepositoryStub{}
	h := newTestRegistryHandler(t, roles, &teamRepositoryStub{}, &processRepositoryStub{})
	request := tenantRequest(http.MethodPost, "/roles",
		`{"metadata":{"name":"bad-role","category":"engineering"},"spec":{"description":"bad","autonomy":"unknown"}}`,
		uuid.New(), uuid.New())
	response := httptest.NewRecorder()
	h.CreateRole(response, request)
	if response.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
	}
	if roles.created != nil {
		t.Fatal("repository was called for an invalid role spec")
	}
}

func TestCreateTeamAndProcessReturnCreated(t *testing.T) {
	teams := &teamRepositoryStub{}
	processes := &processRepositoryStub{}
	h := newTestRegistryHandler(t, &roleRepositoryStub{}, teams, processes)
	workspaceID, authorID := uuid.New(), uuid.New()

	teamBody := `{"apiVersion":"ai-sdlc-factory/v1","kind":"Team","metadata":{"name":"mvp-startup"},"spec":{"description":"A team.","topology":"orchestrated","members":[{"role_name":"qa-engineer","role_version":"1.0.0","count":1}],"budget":{"max_cost_usd":5,"max_steps":60},"review_matrix":{"required_reviewers":1}}}`
	teamResponse := httptest.NewRecorder()
	h.CreateTeam(teamResponse, tenantRequest(http.MethodPost, "/teams", teamBody, workspaceID, authorID))
	if teamResponse.Code != http.StatusCreated || teams.created == nil {
		t.Fatalf("team status = %d, body = %s", teamResponse.Code, teamResponse.Body.String())
	}

	processBody := `{"apiVersion":"ai-sdlc-factory/v1","kind":"Process","metadata":{"name":"default-sdlc"},"spec":{"description":"A process.","stages":[{"name":"Idea","order":0,"gate":"requirements_approved"}]}}`
	processResponse := httptest.NewRecorder()
	h.CreateProcess(processResponse, tenantRequest(http.MethodPost, "/processes", processBody, workspaceID, authorID))
	if processResponse.Code != http.StatusCreated || processes.created == nil {
		t.Fatalf("process status = %d, body = %s", processResponse.Code, processResponse.Body.String())
	}
}

func TestApprovalConflictMapsToForbidden(t *testing.T) {
	roles := &roleRepositoryStub{statusErr: domain.ErrApproverIsAuthor}
	h := newTestRegistryHandler(t, roles, &teamRepositoryStub{}, &processRepositoryStub{})
	id := uuid.NewString()
	request := tenantRequest(http.MethodPatch, "/roles/"+id+"/status", `{"status":"approved"}`, uuid.New(), uuid.New())
	request.Header.Set("X-Approver-ID", uuid.NewString())
	response := httptest.NewRecorder()
	router := chi.NewRouter()
	router.Patch("/roles/{id}/status", h.UpdateRoleStatus)
	router.ServeHTTP(response, request)
	if response.Code != http.StatusForbidden {
		t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
	}
}

func TestUpdateRoleSpecMapsImmutableToMethodNotAllowed(t *testing.T) {
	roles := &roleRepositoryStub{updateErr: domain.ErrImmutable}
	h := newTestRegistryHandler(t, roles, &teamRepositoryStub{}, &processRepositoryStub{})
	id := uuid.NewString()
	body := `{"spec":{"description":"Builds backend services.","system_prompt_ref":"packages/ontology/prompts/backend-developer.yaml","inputs":[{"name":"requirements","type":"document"}],"outputs":[{"name":"implementation","type":"code"}],"tools":{"allow":["fs.read"]},"model":{"capability":"code-strong"},"limits":{"max_steps":60,"max_cost_usd":3},"autonomy":"balanced"}}`
	request := tenantRequest(http.MethodPatch, "/roles/"+id, body, uuid.New(), uuid.New())
	response := httptest.NewRecorder()
	router := chi.NewRouter()
	router.Patch("/roles/{id}", h.UpdateRoleSpec)
	router.ServeHTTP(response, request)
	if response.Code != http.StatusMethodNotAllowed {
		t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
	}
}

func TestRoleReadUpdateCloneAndRevertHandlers(t *testing.T) {
	role := domain.NewRole(uuid.New(), uuid.New(), "backend-developer", domain.CategoryEngineering, domain.RoleSpec{})
	roles := &roleRepositoryStub{
		role: role, roles: []*domain.Role{role}, cloned: role.Clone(), reverted: role.Clone(),
	}
	h := newTestRegistryHandler(t, roles, &teamRepositoryStub{}, &processRepositoryStub{})
	if response := invoke(h.ListRoles, tenantRequest(http.MethodGet, "/roles?category=engineering&status=draft", "", uuid.New(), uuid.New())); response.Code != http.StatusOK {
		t.Fatalf("ListRoles status = %d", response.Code)
	}

	router := chi.NewRouter()
	router.Get("/roles/{id}", h.GetRoleByID)
	router.Get("/roles/{name}/{version}", h.GetRoleByNameAndVersion)
	router.Patch("/roles/{id}", h.UpdateRoleSpec)
	router.Patch("/roles/{id}/status", h.UpdateRoleStatus)
	router.Post("/roles/{id}/clone", h.CloneRole)
	router.Post("/roles/{id}/revert", h.RevertRole)

	for _, path := range []string{"/roles/" + role.ID.String(), "/roles/backend-developer/1.0.0"} {
		response := httptest.NewRecorder()
		router.ServeHTTP(response, tenantRequest(http.MethodGet, path, "", role.WorkspaceID, role.AuthorID))
		if response.Code != http.StatusOK {
			t.Fatalf("GET %s status = %d, body = %s", path, response.Code, response.Body.String())
		}
	}

	roleSpec := `{"spec":{"description":"Builds backend services.","system_prompt_ref":"packages/ontology/prompts/backend-developer.yaml","inputs":[{"name":"requirements","type":"document"}],"outputs":[{"name":"implementation","type":"code"}],"tools":{"allow":["fs.read"]},"model":{"capability":"code-strong"},"limits":{"max_steps":60,"max_cost_usd":3},"autonomy":"balanced"}}`
	for _, test := range []struct {
		method, path, body string
		status             int
	}{
		{http.MethodPatch, "/roles/" + role.ID.String(), roleSpec, http.StatusOK},
		{http.MethodPatch, "/roles/" + role.ID.String() + "/status", `{"status":"approved"}`, http.StatusOK},
		{http.MethodPost, "/roles/" + role.ID.String() + "/clone", "", http.StatusCreated},
		{http.MethodPost, "/roles/" + role.ID.String() + "/revert", "", http.StatusCreated},
	} {
		request := tenantRequest(test.method, test.path, test.body, role.WorkspaceID, role.AuthorID)
		request.Header.Set("X-Approver-ID", uuid.NewString())
		response := httptest.NewRecorder()
		router.ServeHTTP(response, request)
		if response.Code != test.status {
			t.Errorf("%s %s status = %d, body = %s; want %d", test.method, test.path, response.Code, response.Body.String(), test.status)
		}
	}
}

func TestTeamReadUpdateStatusCloneAndRevertHandlers(t *testing.T) {
	team := domain.NewTeam(uuid.New(), uuid.New(), "mvp-startup", domain.TeamSpec{})
	repositoryStub := &teamRepositoryStub{team: team, teams: []*domain.Team{team}, cloned: team.Clone(), reverted: team.Clone()}
	h := newTestRegistryHandler(t, &roleRepositoryStub{}, repositoryStub, &processRepositoryStub{})
	if response := invoke(h.ListTeams, tenantRequest(http.MethodGet, "/teams?status=draft", "", uuid.New(), uuid.New())); response.Code != http.StatusOK {
		t.Fatalf("ListTeams status = %d", response.Code)
	}
	router := chi.NewRouter()
	router.Get("/teams/{id}", h.GetTeamByID)
	router.Get("/teams/{name}/{version}", h.GetTeamByNameAndVersion)
	router.Patch("/teams/{id}", h.UpdateTeamSpec)
	router.Patch("/teams/{id}/status", h.UpdateTeamStatus)
	router.Post("/teams/{id}/clone", h.CloneTeam)
	router.Post("/teams/{id}/revert", h.RevertTeam)
	for _, test := range []struct {
		method, path, body string
		status             int
	}{
		{http.MethodGet, "/teams/" + team.ID.String(), "", http.StatusOK},
		{http.MethodGet, "/teams/mvp-startup/1.0.0", "", http.StatusOK},
		{http.MethodPatch, "/teams/" + team.ID.String(), `{"spec":{"description":"Team","topology":"orchestrated","members":[{"role_name":"qa","role_version":"1.0.0","count":1}],"budget":{"max_cost_usd":1,"max_steps":1},"review_matrix":{"required_reviewers":1}}}`, http.StatusOK},
		{http.MethodPatch, "/teams/" + team.ID.String() + "/status", `{"status":"approved"}`, http.StatusOK},
		{http.MethodPost, "/teams/" + team.ID.String() + "/clone", "", http.StatusCreated},
		{http.MethodPost, "/teams/" + team.ID.String() + "/revert", "", http.StatusCreated},
	} {
		request := tenantRequest(test.method, test.path, test.body, team.WorkspaceID, team.AuthorID)
		request.Header.Set("X-Approver-ID", uuid.NewString())
		response := httptest.NewRecorder()
		router.ServeHTTP(response, request)
		if response.Code != test.status {
			t.Errorf("%s %s status = %d, body = %s; want %d", test.method, test.path, response.Code, response.Body.String(), test.status)
		}
	}
}

func TestProcessReadUpdateStatusCloneAndRevertHandlers(t *testing.T) {
	process := domain.NewProcess(uuid.New(), uuid.New(), "default-sdlc", domain.ProcessSpec{})
	repositoryStub := &processRepositoryStub{process: process, processes: []*domain.Process{process}, cloned: process.Clone(), reverted: process.Clone()}
	h := newTestRegistryHandler(t, &roleRepositoryStub{}, &teamRepositoryStub{}, repositoryStub)
	if response := invoke(h.ListProcesses, tenantRequest(http.MethodGet, "/processes?status=draft", "", uuid.New(), uuid.New())); response.Code != http.StatusOK {
		t.Fatalf("ListProcesses status = %d", response.Code)
	}
	router := chi.NewRouter()
	router.Get("/processes/{id}", h.GetProcessByID)
	router.Get("/processes/{name}/{version}", h.GetProcessByNameAndVersion)
	router.Patch("/processes/{id}", h.UpdateProcessSpec)
	router.Patch("/processes/{id}/status", h.UpdateProcessStatus)
	router.Post("/processes/{id}/clone", h.CloneProcess)
	router.Post("/processes/{id}/revert", h.RevertProcess)
	for _, test := range []struct {
		method, path, body string
		status             int
	}{
		{http.MethodGet, "/processes/" + process.ID.String(), "", http.StatusOK},
		{http.MethodGet, "/processes/default-sdlc/1.0.0", "", http.StatusOK},
		{http.MethodPatch, "/processes/" + process.ID.String(), `{"spec":{"description":"Process","stages":[{"name":"Idea","order":0,"gate":"approved"}]}}`, http.StatusOK},
		{http.MethodPatch, "/processes/" + process.ID.String() + "/status", `{"status":"approved"}`, http.StatusOK},
		{http.MethodPost, "/processes/" + process.ID.String() + "/clone", "", http.StatusCreated},
		{http.MethodPost, "/processes/" + process.ID.String() + "/revert", "", http.StatusCreated},
	} {
		request := tenantRequest(test.method, test.path, test.body, process.WorkspaceID, process.AuthorID)
		request.Header.Set("X-Approver-ID", uuid.NewString())
		response := httptest.NewRecorder()
		router.ServeHTTP(response, request)
		if response.Code != test.status {
			t.Errorf("%s %s status = %d, body = %s; want %d", test.method, test.path, response.Code, response.Body.String(), test.status)
		}
	}
}

func invoke(handler http.HandlerFunc, request *http.Request) *httptest.ResponseRecorder {
	response := httptest.NewRecorder()
	handler(response, request)
	return response
}
