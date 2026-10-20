package httptransport

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/sapelyuk/ai-sdlc-factory/apps/api/internal/config"
	"github.com/sapelyuk/ai-sdlc-factory/apps/api/internal/iam"
)

func TestHealthReadinessAndTenantBoundaries(t *testing.T) {
	// Create a test JWT service and config.
	jwtService := iam.NewJWTService("test-secret", 24*time.Hour)
	cfg := &config.Config{AllowLegacyAuth: true}

	// Create a stub login handler (won't be called in this test).
	loginHandler := NewLoginHandler(nil, jwtService)

	router := NewRouter(nil, func(context.Context) error {
		return errors.New("database unavailable")
	}, cfg, jwtService, loginHandler)

	health := httptest.NewRecorder()
	router.Handler().ServeHTTP(health, httptest.NewRequest(http.MethodGet, "/healthz", nil))
	if health.Code != http.StatusOK {
		t.Fatalf("healthz status = %d, want %d", health.Code, http.StatusOK)
	}

	ready := httptest.NewRecorder()
	router.Handler().ServeHTTP(ready, httptest.NewRequest(http.MethodGet, "/readyz", nil))
	if ready.Code != http.StatusServiceUnavailable {
		t.Fatalf("readyz status = %d, want %d", ready.Code, http.StatusServiceUnavailable)
	}

	// Registry request without auth → 401 (JWT required).
	registry := httptest.NewRecorder()
	router.Handler().ServeHTTP(registry, httptest.NewRequest(http.MethodGet, "/api/v1/registry/roles", nil))
	if registry.Code != http.StatusUnauthorized {
		t.Fatalf("Registry request without auth status = %d, want %d", registry.Code, http.StatusUnauthorized)
	}
}
