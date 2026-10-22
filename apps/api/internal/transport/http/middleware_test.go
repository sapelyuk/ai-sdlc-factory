package httptransport

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"
	"github.com/sapelyuk/ai-sdlc-factory/apps/api/internal/identity"
)

// Missing tenant headers return 401 Unauthorized (Issue #71).
func TestTenantContextMiddleware_MissingHeaders_Returns401(t *testing.T) {
	tests := []struct {
		name        string
		workspaceID string
		authorID    string
	}{
		{name: "missing workspace"},
		{name: "missing author", workspaceID: uuid.NewString()},
		{name: "zero workspace", workspaceID: uuid.Nil.String(), authorID: uuid.NewString()},
		{name: "zero author", workspaceID: uuid.NewString(), authorID: uuid.Nil.String()},
		{name: "zero workspace 32-hex", workspaceID: "00000000000000000000000000000000", authorID: uuid.NewString()},
		{name: "zero workspace urn", workspaceID: "urn:uuid:" + uuid.Nil.String(), authorID: uuid.NewString()},
		{name: "zero author 32-hex", workspaceID: uuid.NewString(), authorID: "00000000000000000000000000000000"},
		{name: "zero author urn", workspaceID: uuid.NewString(), authorID: "urn:uuid:" + uuid.Nil.String()},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodGet, "/", nil)
			request.Header.Set("X-Workspace-ID", test.workspaceID)
			request.Header.Set("X-Author-ID", test.authorID)
			response := httptest.NewRecorder()
			TenantContextMiddleware(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
				t.Fatal("next handler called with missing tenant headers")
			})).ServeHTTP(response, request)
			if response.Code != http.StatusUnauthorized {
				t.Fatalf("status = %d, want %d; body = %s", response.Code, http.StatusUnauthorized, response.Body.String())
			}
			if got, want := response.Header().Get("WWW-Authenticate"), `Bearer realm="api"`; got != want {
				t.Fatalf("WWW-Authenticate = %q, want %q", got, want)
			}
		})
	}
}

// Invalid UUID format returns 400 Bad Request (Issue #71).
func TestTenantContextMiddleware_InvalidUUID_Returns400(t *testing.T) {
	tests := []struct {
		name        string
		workspaceID string
		authorID    string
	}{
		{name: "invalid workspace", workspaceID: "not-a-uuid", authorID: uuid.NewString()},
		{name: "invalid author", workspaceID: uuid.NewString(), authorID: "not-a-uuid"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodGet, "/", nil)
			request.Header.Set("X-Workspace-ID", test.workspaceID)
			request.Header.Set("X-Author-ID", test.authorID)
			response := httptest.NewRecorder()
			TenantContextMiddleware(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
				t.Fatal("next handler called with invalid tenant headers")
			})).ServeHTTP(response, request)
			if response.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want %d; body = %s", response.Code, http.StatusBadRequest, response.Body.String())
			}
		})
	}
}

func TestTenantContextMiddlewareAddsIdentityToContext(t *testing.T) {
	workspaceID, authorID := uuid.New(), uuid.New()
	request := httptest.NewRequest(http.MethodGet, "/", nil)
	request.Header.Set("X-Workspace-ID", workspaceID.String())
	request.Header.Set("X-Author-ID", authorID.String())
	response := httptest.NewRecorder()
	TenantContextMiddleware(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		if got, err := identity.WorkspaceID(r.Context()); err != nil || got != workspaceID {
			t.Errorf("workspace context = %s, %v", got, err)
		}
		if got, err := identity.AuthorID(r.Context()); err != nil || got != authorID {
			t.Errorf("author context = %s, %v", got, err)
		}
	})).ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusOK)
	}
}
