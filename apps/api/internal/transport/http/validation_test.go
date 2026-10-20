package httptransport

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/sapelyuk/ai-sdlc-factory/apps/api/internal/iam"
)

// mockUserRepo is a test-only user repository that always returns an error.
type mockUserRepo struct{}

func (m *mockUserRepo) Create(_ context.Context, _ *iam.User) error {
	return errors.New("not implemented")
}

func (m *mockUserRepo) GetByEmail(_ context.Context, _ string) (*iam.User, error) {
	return nil, errors.New("user not found")
}

func (m *mockUserRepo) GetByID(_ context.Context, _ string) (*iam.User, error) {
	return nil, errors.New("user not found")
}

func TestLoginHandler_RejectsInvalidJSON(t *testing.T) {
	h := NewLoginHandler(&mockUserRepo{}, iam.NewJWTService("secret", 24*time.Hour))

	resp := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", strings.NewReader("not-json"))
	h.Login(resp, req)
	if resp.Code != http.StatusBadRequest {
		t.Fatalf("invalid JSON status = %d, want %d", resp.Code, http.StatusBadRequest)
	}
}

func TestLoginHandler_RejectsInvalidFields(t *testing.T) {
	h := NewLoginHandler(&mockUserRepo{}, iam.NewJWTService("secret", 24*time.Hour))

	// Missing email.
	resp := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", strings.NewReader(`{"password":"test"}`))
	h.Login(resp, req)
	if resp.Code != http.StatusUnprocessableEntity {
		t.Fatalf("missing email status = %d, want %d", resp.Code, http.StatusUnprocessableEntity)
	}

	// Missing password.
	resp2 := httptest.NewRecorder()
	req2 := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", strings.NewReader(`{"email":"valid@test.com"}`))
	h.Login(resp2, req2)
	if resp2.Code != http.StatusUnprocessableEntity {
		t.Fatalf("missing password status = %d, want %d", resp2.Code, http.StatusUnprocessableEntity)
	}

	// Empty password.
	resp3 := httptest.NewRecorder()
	req3 := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", strings.NewReader(`{"email":"valid@test.com","password":""}`))
	h.Login(resp3, req3)
	if resp3.Code != http.StatusUnprocessableEntity {
		t.Fatalf("empty password status = %d, want %d", resp3.Code, http.StatusUnprocessableEntity)
	}
}

func TestLoginHandler_401WhenUserNotFound(t *testing.T) {
	h := NewLoginHandler(&mockUserRepo{}, iam.NewJWTService("secret", 24*time.Hour))

	resp := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", strings.NewReader(`{"email":"nobody@example.com","password":"test"}`))
	req.Header.Set("Content-Type", "application/json")
	h.Login(resp, req)
	if resp.Code != http.StatusUnauthorized {
		t.Fatalf("user not found status = %d, want %d", resp.Code, http.StatusUnauthorized)
	}
}
