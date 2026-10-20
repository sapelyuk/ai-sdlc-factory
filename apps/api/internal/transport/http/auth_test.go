package httptransport

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/sapelyuk/ai-sdlc-factory/apps/api/internal/iam"
	"github.com/sapelyuk/ai-sdlc-factory/apps/api/internal/identity"
)

func TestJWTAuthMiddleware_HandlesWhitelistedPaths(t *testing.T) {
	svc := iam.NewJWTService("secret", 24*time.Hour)
	mw := JWTAuthMiddleware(svc, false)

	// /healthz should pass without auth.
	health := httptest.NewRecorder()
	mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})).ServeHTTP(health, httptest.NewRequest(http.MethodGet, "/healthz", nil))
	if health.Code != http.StatusOK {
		t.Fatalf("healthz status = %d, want %d", health.Code, http.StatusOK)
	}

	// /readyz should pass without auth.
	ready := httptest.NewRecorder()
	mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})).ServeHTTP(ready, httptest.NewRequest(http.MethodGet, "/readyz", nil))
	if ready.Code != http.StatusOK {
		t.Fatalf("readyz status = %d, want %d", ready.Code, http.StatusOK)
	}

	// /metrics should pass without auth.
	metrics := httptest.NewRecorder()
	mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})).ServeHTTP(metrics, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	if metrics.Code != http.StatusOK {
		t.Fatalf("metrics status = %d, want %d", metrics.Code, http.StatusOK)
	}
}

func TestJWTAuthMiddleware_LoginEndpoint(t *testing.T) {
	svc := iam.NewJWTService("secret", 24*time.Hour)
	mw := JWTAuthMiddleware(svc, false)

	// POST /api/v1/auth/login should pass without auth.
	login := httptest.NewRecorder()
	mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})).ServeHTTP(login, httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", nil))
	if login.Code != http.StatusOK {
		t.Fatalf("login status = %d, want %d", login.Code, http.StatusOK)
	}
}

func TestJWTAuthMiddleware_RequiresValidToken(t *testing.T) {
	svc := iam.NewJWTService("secret", 24*time.Hour)
	mw := JWTAuthMiddleware(svc, false)

	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	// No Authorization header → 401.
	noAuth := httptest.NewRecorder()
	mw(next).ServeHTTP(noAuth, httptest.NewRequest(http.MethodGet, "/api/v1/registry/roles", nil))
	if noAuth.Code != http.StatusUnauthorized {
		t.Fatalf("no auth status = %d, want %d", noAuth.Code, http.StatusUnauthorized)
	}

	// Invalid format → 401.
	badFormat := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/registry/roles", nil)
	req.Header.Set("Authorization", "InvalidFormat")
	mw(next).ServeHTTP(badFormat, req)
	if badFormat.Code != http.StatusUnauthorized {
		t.Fatalf("bad format status = %d, want %d", badFormat.Code, http.StatusUnauthorized)
	}

	// Invalid token → 401.
	badToken := httptest.NewRecorder()
	req2 := httptest.NewRequest(http.MethodGet, "/api/v1/registry/roles", nil)
	req2.Header.Set("Authorization", "Bearer not-a-jwt")
	mw(next).ServeHTTP(badToken, req2)
	if badToken.Code != http.StatusUnauthorized {
		t.Fatalf("bad token status = %d, want %d", badToken.Code, http.StatusUnauthorized)
	}
}

func TestJWTAuthMiddleware_PassesValidToken(t *testing.T) {
	svc := iam.NewJWTService("secret", 24*time.Hour)
	mw := JWTAuthMiddleware(svc, false)

	userID := uuid.New()
	wsID := uuid.New()
	token, err := svc.GenerateToken(userID.String(), wsID.String(), "admin")
	if err != nil {
		t.Fatalf("GenerateToken() error = %v", err)
	}

	called := false
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		// Verify context has JWT identity.
		role := identity.UserRole(r.Context())
		if role != "admin" {
			t.Errorf("context role = %q, want %q", role, "admin")
		}
		w.WriteHeader(http.StatusOK)
	})

	valid := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/registry/roles", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	mw(next).ServeHTTP(valid, req)
	if valid.Code != http.StatusOK {
		t.Fatalf("valid token status = %d, want %d", valid.Code, http.StatusOK)
	}
	if !called {
		t.Fatal("next handler was not called")
	}
}

func TestJWTAuthMiddleware_LegacyFallback(t *testing.T) {
	svc := iam.NewJWTService("secret", 24*time.Hour)
	mw := JWTAuthMiddleware(svc, true) // legacy allowed

	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	// No Authorization but valid X-Workspace-ID → passes through TenantContextMiddleware.
	wsID := uuid.New()
	authorID := uuid.New()
	legacy := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/registry/roles", nil)
	req.Header.Set("X-Workspace-ID", wsID.String())
	req.Header.Set("X-Author-ID", authorID.String())
	mw(next).ServeHTTP(legacy, req)
	if legacy.Code != http.StatusOK {
		t.Fatalf("legacy auth status = %d, want %d", legacy.Code, http.StatusOK)
	}
}

func TestJWTAuthMiddleware_LegacyNotAvailableInStrictMode(t *testing.T) {
	svc := iam.NewJWTService("secret", 24*time.Hour)
	mw := JWTAuthMiddleware(svc, false) // legacy NOT allowed

	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	// No Authorization, no JWT — even with X-Workspace-ID → 401.
	wsID := uuid.New()
	strict := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/registry/roles", nil)
	req.Header.Set("X-Workspace-ID", wsID.String())
	req.Header.Set("X-Author-ID", uuid.New().String())
	mw(next).ServeHTTP(strict, req)
	if strict.Code != http.StatusUnauthorized {
		t.Fatalf("strict mode status = %d, want %d", strict.Code, http.StatusUnauthorized)
	}
}

func TestGetAuthFromContext(t *testing.T) {
	ctx := contextWithAuth("user123", "ws456", "owner")
	auth, ok := GetAuthFromContext(ctx)
	if !ok {
		t.Fatal("GetAuthFromContext() returned not ok")
	}
	if auth.UserID != "user123" {
		t.Errorf("auth.UserID = %q, want %q", auth.UserID, "user123")
	}
	if auth.Role != "owner" {
		t.Errorf("auth.Role = %q, want %q", auth.Role, "owner")
	}
}

func contextWithAuth(userID, workspaceID, role string) context.Context {
	return context.WithValue(context.Background(), authContextKey{}, authContextValue{
		UserID:      userID,
		WorkspaceID: workspaceID,
		Role:        role,
	})
}

func TestRBACMiddleware_AllowsHigherRoles(t *testing.T) {
	mw := RBACMiddleware("viewer")
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	for _, role := range []string{"viewer", "operator", "admin", "owner"} {
		t.Run(role, func(t *testing.T) {
			resp := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodGet, "/", nil)
			ctx := identity.WithJWT(req.Context(), uuid.New(), uuid.New(), role)
			mw(next).ServeHTTP(resp, req.WithContext(ctx))
			if resp.Code != http.StatusOK {
				t.Errorf("role %s status = %d, want %d", role, resp.Code, http.StatusOK)
			}
		})
	}
}

func TestRBACMiddleware_RejectsLowerRoles(t *testing.T) {
	mw := RBACMiddleware("admin")
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	resp := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	ctx := identity.WithJWT(req.Context(), uuid.New(), uuid.New(), "viewer")
	mw(next).ServeHTTP(resp, req.WithContext(ctx))
	if resp.Code != http.StatusForbidden {
		t.Errorf("viewer on admin route status = %d, want %d", resp.Code, http.StatusForbidden)
	}
}

func TestRBACMiddleware_AllowsMissingRole(t *testing.T) {
	// In legacy mode, role is not set — should allow through.
	mw := RBACMiddleware("admin")
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	resp := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	// No JWT context — role is empty.
	mw(next).ServeHTTP(resp, req)
	if resp.Code != http.StatusOK {
		t.Errorf("missing role status = %d, want %d (legacy fallback)", resp.Code, http.StatusOK)
	}
}
