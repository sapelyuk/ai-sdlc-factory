package handler

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/santhosh-tekuri/jsonschema/v5"
	"github.com/sapelyuk/ai-sdlc-factory/apps/api/internal/identity"
	"github.com/sapelyuk/ai-sdlc-factory/apps/api/internal/registry/domain"
)

func TestDecodeJSONRejectsInvalidBodies(t *testing.T) {
	for _, test := range []struct {
		name string
		body string
	}{
		{name: "unknown field", body: `{"unknown":true}`},
		{name: "multiple values", body: `{} {}`},
		{name: "malformed JSON", body: `{`},
	} {
		t.Run(test.name, func(t *testing.T) {
			var value struct {
				Valid bool `json:"valid"`
			}
			if err := decodeJSON(strings.NewReader(test.body), &value); err == nil {
				t.Fatal("decodeJSON unexpectedly succeeded")
			}
		})
	}

	var value struct {
		Valid bool `json:"valid"`
	}
	if err := decodeJSON(strings.NewReader(`{"valid":true}`), &value); err != nil {
		t.Fatalf("decodeJSON valid body: %v", err)
	}
}

func TestWriteBodyErrorMapsInvalidAndOversizedBodies(t *testing.T) {
	for _, test := range []struct {
		err        error
		wantStatus int
	}{
		{err: errors.New("malformed"), wantStatus: http.StatusBadRequest},
		{err: errRequestTooLarge, wantStatus: http.StatusRequestEntityTooLarge},
	} {
		response := httptest.NewRecorder()
		writeBodyError(response, test.err)
		if response.Code != test.wantStatus {
			t.Errorf("status = %d, want %d", response.Code, test.wantStatus)
		}
	}
}

func TestCheckEnvelopeAndValidationMessage(t *testing.T) {
	for _, test := range []struct {
		apiVersion string
		kind       string
		wantError  bool
	}{
		{apiVersion: "ai-sdlc-factory/v1", kind: "Role"},
		{kind: "Role"},
		{apiVersion: "other/v1", kind: "Role", wantError: true},
		{apiVersion: "ai-sdlc-factory/v1", kind: "Team", wantError: true},
	} {
		if err := checkEnvelope(test.apiVersion, test.kind, "Role"); (err != nil) != test.wantError {
			t.Errorf("checkEnvelope(%q, %q) error = %v, wantError=%t", test.apiVersion, test.kind, err, test.wantError)
		}
	}
	if got := validationMessage(errors.New("invalid")); got != "invalid" {
		t.Fatalf("validationMessage() = %q", got)
	}

	schemaDirectory, err := findSchemaDirectory()
	if err != nil {
		t.Fatal(err)
	}
	schema, err := jsonschema.Compile(filepath.Join(schemaDirectory, "role.schema.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := schema.Validate(map[string]any{}); err == nil {
		t.Fatal("empty role spec unexpectedly passed validation")
	} else if got := validationMessage(err); got == "" {
		t.Fatal("validationMessage() returned an empty message")
	}
}

func TestFindSchemaDirectoryUsesConfiguredPath(t *testing.T) {
	directory, err := findSchemaDirectory()
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("SCHEMA_DIR", directory)
	got, err := findSchemaDirectory()
	if err != nil {
		t.Fatal(err)
	}
	if got != directory {
		t.Fatalf("findSchemaDirectory() = %q, want %q", got, directory)
	}
}

func TestParseIDAndRepositoryErrorMappings(t *testing.T) {
	for _, test := range []struct {
		err        error
		wantStatus int
	}{
		{err: domain.ErrNotFound, wantStatus: http.StatusNotFound},
		{err: domain.ErrImmutable, wantStatus: http.StatusMethodNotAllowed},
		{err: domain.ErrInvalidStatusTransition, wantStatus: http.StatusConflict},
		{err: domain.ErrApproverIsAuthor, wantStatus: http.StatusForbidden},
		{err: identity.ErrMissingTenantContext, wantStatus: http.StatusBadRequest},
		{err: errors.New("internal"), wantStatus: http.StatusInternalServerError},
		{err: &pgconn.PgError{Code: "23505"}, wantStatus: http.StatusConflict},
		{err: &pgconn.PgError{Code: "23514"}, wantStatus: http.StatusUnprocessableEntity},
	} {
		response := httptest.NewRecorder()
		writeRepositoryError(response, test.err)
		if response.Code != test.wantStatus {
			t.Errorf("writeRepositoryError(%v) status = %d, want %d", test.err, response.Code, test.wantStatus)
		}
	}

	for _, test := range []struct {
		value    string
		wantOK   bool
		wantCode int
	}{
		{value: uuid.NewString(), wantOK: true, wantCode: http.StatusOK},
		{value: "not-a-uuid", wantOK: false, wantCode: http.StatusBadRequest},
	} {
		response := httptest.NewRecorder()
		request := httptest.NewRequest(http.MethodGet, "/", nil)
		routeContext := chi.NewRouteContext()
		routeContext.URLParams.Add("id", test.value)
		request = request.WithContext(context.WithValue(request.Context(), chi.RouteCtxKey, routeContext))
		_, ok := parseID(response, request)
		if ok != test.wantOK || response.Code != test.wantCode {
			t.Errorf("parseID(%q) = ok:%t status:%d, want ok:%t status:%d", test.value, ok, response.Code, test.wantOK, test.wantCode)
		}
	}

	response := httptest.NewRecorder()
	writeJSON(response, http.StatusCreated, map[string]string{"status": "ok"})
	if response.Header().Get("Content-Type") != "application/json" {
		t.Fatalf("content type = %q", response.Header().Get("Content-Type"))
	}
}

func TestDecodeJSONEnforcesRequestSizeLimit(t *testing.T) {
	body := strings.NewReader(`{"value":"` + strings.Repeat("x", maxRequestBytes) + `"}`)
	var value map[string]any
	if err := decodeJSON(body, &value); !errors.Is(err, errRequestTooLarge) {
		t.Fatalf("decodeJSON oversized request error = %v, want errRequestTooLarge", err)
	}
}

func TestRoleTeamAndProcessHandlersMapRepositoryFailures(t *testing.T) {
	workspaceID, authorID := uuid.New(), uuid.New()
	approverID := uuid.New()
	roleBody := `{"metadata":{"name":"backend-developer","category":"engineering"},"spec":{"description":"Builds backend services.","system_prompt_ref":"packages/ontology/prompts/backend-developer.yaml","inputs":[{"name":"requirements","type":"document"}],"outputs":[{"name":"implementation","type":"code"}],"tools":{"allow":["fs.read"]},"model":{"capability":"code-strong"},"limits":{"max_steps":60,"max_cost_usd":3},"autonomy":"balanced"}}`
	teamBody := `{"metadata":{"name":"mvp-startup"},"spec":{"description":"A team.","topology":"orchestrated","members":[{"role_name":"qa-engineer","role_version":"1.0.0","count":1}],"budget":{"max_cost_usd":5,"max_steps":60},"review_matrix":{"required_reviewers":1}}}`
	processBody := `{"metadata":{"name":"default-sdlc"},"spec":{"description":"A process.","stages":[{"name":"Idea","order":0,"gate":"requirements_approved"}]}}`

	roleStub := &roleRepositoryStub{
		createErr: errors.New("create failed"),
		listErr:   errors.New("list failed"),
		getErr:    domain.ErrNotFound,
		updateErr: domain.ErrImmutable,
		statusErr: domain.ErrInvalidStatusTransition,
	}
	teamStub := &teamRepositoryStub{
		createErr: errors.New("create failed"),
		listErr:   errors.New("list failed"),
		getErr:    domain.ErrNotFound,
		updateErr: domain.ErrImmutable,
		statusErr: domain.ErrInvalidStatusTransition,
	}
	processStub := &processRepositoryStub{
		createErr: errors.New("create failed"),
		listErr:   errors.New("list failed"),
		getErr:    domain.ErrNotFound,
		updateErr: domain.ErrImmutable,
		statusErr: domain.ErrInvalidStatusTransition,
	}
	handler := newTestRegistryHandler(t, roleStub, teamStub, processStub)

	for _, test := range []struct {
		name       string
		call       http.HandlerFunc
		request    *http.Request
		wantStatus int
	}{
		{"create role", handler.CreateRole, tenantRequest(http.MethodPost, "/roles", roleBody, workspaceID, authorID), http.StatusInternalServerError},
		{"create team", handler.CreateTeam, tenantRequest(http.MethodPost, "/teams", teamBody, workspaceID, authorID), http.StatusInternalServerError},
		{"create process", handler.CreateProcess, tenantRequest(http.MethodPost, "/processes", processBody, workspaceID, authorID), http.StatusInternalServerError},
		{"list roles", handler.ListRoles, tenantRequest(http.MethodGet, "/roles", "", workspaceID, authorID), http.StatusInternalServerError},
		{"list teams", handler.ListTeams, tenantRequest(http.MethodGet, "/teams", "", workspaceID, authorID), http.StatusInternalServerError},
		{"list processes", handler.ListProcesses, tenantRequest(http.MethodGet, "/processes", "", workspaceID, authorID), http.StatusInternalServerError},
		{"get role", handler.GetRoleByID, requestWithID(http.MethodGet, "/roles", uuid.NewString(), "", workspaceID, authorID), http.StatusNotFound},
		{"get team", handler.GetTeamByID, requestWithID(http.MethodGet, "/teams", uuid.NewString(), "", workspaceID, authorID), http.StatusNotFound},
		{"get process", handler.GetProcessByID, requestWithID(http.MethodGet, "/processes", uuid.NewString(), "", workspaceID, authorID), http.StatusNotFound},
		{"update role", handler.UpdateRoleSpec, requestWithID(http.MethodPatch, "/roles", uuid.NewString(), `{"spec":{"description":"Builds backend services.","system_prompt_ref":"packages/ontology/prompts/backend-developer.yaml","inputs":[{"name":"requirements","type":"document"}],"outputs":[{"name":"implementation","type":"code"}],"tools":{"allow":["fs.read"]},"model":{"capability":"code-strong"},"limits":{"max_steps":60,"max_cost_usd":3},"autonomy":"balanced"}}`, workspaceID, authorID), http.StatusMethodNotAllowed},
		{"update team", handler.UpdateTeamSpec, requestWithID(http.MethodPatch, "/teams", uuid.NewString(), `{"spec":{"description":"A team.","topology":"orchestrated","members":[{"role_name":"qa-engineer","role_version":"1.0.0","count":1}],"budget":{"max_cost_usd":5,"max_steps":60},"review_matrix":{"required_reviewers":1}}}`, workspaceID, authorID), http.StatusMethodNotAllowed},
		{"update process", handler.UpdateProcessSpec, requestWithID(http.MethodPatch, "/processes", uuid.NewString(), `{"spec":{"description":"A process.","stages":[{"name":"Idea","order":0,"gate":"approved"}]}}`, workspaceID, authorID), http.StatusMethodNotAllowed},
		{"status role", handler.UpdateRoleStatus, requestWithID(http.MethodPatch, "/roles", uuid.NewString(), `{"status":"approved"}`, workspaceID, authorID), http.StatusConflict},
		{"status team", handler.UpdateTeamStatus, requestWithID(http.MethodPatch, "/teams", uuid.NewString(), `{"status":"approved"}`, workspaceID, authorID), http.StatusConflict},
		{"status process", handler.UpdateProcessStatus, requestWithID(http.MethodPatch, "/processes", uuid.NewString(), `{"status":"approved"}`, workspaceID, authorID), http.StatusConflict},
		{"clone role", handler.CloneRole, requestWithID(http.MethodPost, "/roles", uuid.NewString(), "", workspaceID, authorID), http.StatusNotFound},
		{"clone team", handler.CloneTeam, requestWithID(http.MethodPost, "/teams", uuid.NewString(), "", workspaceID, authorID), http.StatusNotFound},
		{"clone process", handler.CloneProcess, requestWithID(http.MethodPost, "/processes", uuid.NewString(), "", workspaceID, authorID), http.StatusNotFound},
		{"revert role", handler.RevertRole, requestWithID(http.MethodPost, "/roles", uuid.NewString(), "", workspaceID, authorID), http.StatusNotFound},
		{"revert team", handler.RevertTeam, requestWithID(http.MethodPost, "/teams", uuid.NewString(), "", workspaceID, authorID), http.StatusNotFound},
		{"revert process", handler.RevertProcess, requestWithID(http.MethodPost, "/processes", uuid.NewString(), "", workspaceID, authorID), http.StatusNotFound},
	} {
		t.Run(test.name, func(t *testing.T) {
			if strings.Contains(test.name, "status") {
				test.request.Header.Set("X-Approver-ID", approverID.String())
			}
			response := invoke(test.call, test.request)
			if response.Code != test.wantStatus {
				t.Fatalf("status = %d, body = %s; want %d", response.Code, response.Body.String(), test.wantStatus)
			}
		})
	}

	invalidIDRequests := []struct {
		name string
		call http.HandlerFunc
	}{
		{"role update", handler.UpdateRoleSpec},
		{"role status", handler.UpdateRoleStatus},
		{"role clone", handler.CloneRole},
		{"role revert", handler.RevertRole},
		{"team update", handler.UpdateTeamSpec},
		{"team status", handler.UpdateTeamStatus},
		{"team clone", handler.CloneTeam},
		{"team revert", handler.RevertTeam},
		{"process update", handler.UpdateProcessSpec},
		{"process status", handler.UpdateProcessStatus},
		{"process clone", handler.CloneProcess},
		{"process revert", handler.RevertProcess},
	}
	for _, test := range invalidIDRequests {
		response := invoke(test.call, requestWithID(http.MethodPatch, "/", "invalid", `{}`, workspaceID, authorID))
		if response.Code != http.StatusBadRequest {
			t.Errorf("%s invalid ID status = %d; want %d", test.name, response.Code, http.StatusBadRequest)
		}
	}
}

func TestCreateHandlersRejectInvalidRequestsAndMissingTenant(t *testing.T) {
	handler := newTestRegistryHandler(t, &roleRepositoryStub{}, &teamRepositoryStub{}, &processRepositoryStub{})
	workspaceID, authorID := uuid.New(), uuid.New()
	validRole := `{"metadata":{"name":"backend-developer","category":"engineering"},"spec":{"description":"Builds backend services.","system_prompt_ref":"packages/ontology/prompts/backend-developer.yaml","inputs":[{"name":"requirements","type":"document"}],"outputs":[{"name":"implementation","type":"code"}],"tools":{"allow":["fs.read"]},"model":{"capability":"code-strong"},"limits":{"max_steps":60,"max_cost_usd":3},"autonomy":"balanced"}}`
	validTeam := `{"metadata":{"name":"mvp-startup"},"spec":{"description":"A team.","topology":"orchestrated","members":[{"role_name":"qa-engineer","role_version":"1.0.0","count":1}],"budget":{"max_cost_usd":5,"max_steps":60},"review_matrix":{"required_reviewers":1}}}`
	validProcess := `{"metadata":{"name":"default-sdlc"},"spec":{"description":"A process.","stages":[{"name":"Idea","order":0,"gate":"requirements_approved"}]}}`

	for _, test := range []struct {
		name       string
		call       http.HandlerFunc
		body       string
		context    string
		wantStatus int
	}{
		{"role malformed body", handler.CreateRole, `{`, "tenant", http.StatusBadRequest},
		{"role wrong kind", handler.CreateRole, strings.Replace(validRole, `"metadata"`, `"kind":"Team","metadata"`, 1), "tenant", http.StatusBadRequest},
		{"role empty name", handler.CreateRole, strings.Replace(validRole, `"backend-developer"`, `""`, 1), "tenant", http.StatusBadRequest},
		{"role invalid category", handler.CreateRole, strings.Replace(validRole, `"engineering"`, `"invalid"`, 1), "tenant", http.StatusBadRequest},
		{"role unsupported initial version", handler.CreateRole, strings.Replace(validRole, `"name":"backend-developer"`, `"name":"backend-developer","version":"2.0.0"`, 1), "tenant", http.StatusBadRequest},
		{"role missing workspace", handler.CreateRole, validRole, "none", http.StatusBadRequest},
		{"role missing author", handler.CreateRole, validRole, "author-missing", http.StatusBadRequest},
		{"team malformed body", handler.CreateTeam, `{`, "tenant", http.StatusBadRequest},
		{"team wrong API version", handler.CreateTeam, strings.Replace(validTeam, `"metadata"`, `"apiVersion":"other/v1","metadata"`, 1), "tenant", http.StatusBadRequest},
		{"team empty name", handler.CreateTeam, strings.Replace(validTeam, `"mvp-startup"`, `""`, 1), "tenant", http.StatusBadRequest},
		{"team unsupported initial version", handler.CreateTeam, strings.Replace(validTeam, `"mvp-startup"`, `"mvp-startup","version":"2.0.0"`, 1), "tenant", http.StatusBadRequest},
		{"team invalid spec", handler.CreateTeam, `{"metadata":{"name":"team"},"spec":{}}`, "tenant", http.StatusUnprocessableEntity},
		{"team missing tenant", handler.CreateTeam, validTeam, "none", http.StatusBadRequest},
		{"process malformed body", handler.CreateProcess, `{`, "tenant", http.StatusBadRequest},
		{"process wrong kind", handler.CreateProcess, strings.Replace(validProcess, `"metadata"`, `"kind":"Role","metadata"`, 1), "tenant", http.StatusBadRequest},
		{"process empty name", handler.CreateProcess, strings.Replace(validProcess, `"default-sdlc"`, `""`, 1), "tenant", http.StatusBadRequest},
		{"process unsupported initial version", handler.CreateProcess, strings.Replace(validProcess, `"default-sdlc"`, `"default-sdlc","version":"2.0.0"`, 1), "tenant", http.StatusBadRequest},
		{"process invalid spec", handler.CreateProcess, `{"metadata":{"name":"process"},"spec":{}}`, "tenant", http.StatusUnprocessableEntity},
		{"process missing tenant", handler.CreateProcess, validProcess, "none", http.StatusBadRequest},
	} {
		t.Run(test.name, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(test.body))
			switch test.context {
			case "tenant":
				request = tenantRequest(http.MethodPost, "/", test.body, workspaceID, authorID)
			case "author-missing":
				request = request.WithContext(identity.WithTenant(request.Context(), workspaceID, uuid.Nil))
			}
			response := invoke(test.call, request)
			if response.Code != test.wantStatus {
				t.Fatalf("status = %d, body = %s; want %d", response.Code, response.Body.String(), test.wantStatus)
			}
		})
	}
}

func requestWithID(method, path, id, body string, workspaceID, authorID uuid.UUID) *http.Request {
	request := tenantRequest(method, path, body, workspaceID, authorID)
	routeContext := chi.NewRouteContext()
	routeContext.URLParams.Add("id", id)
	return request.WithContext(context.WithValue(request.Context(), chi.RouteCtxKey, routeContext))
}
