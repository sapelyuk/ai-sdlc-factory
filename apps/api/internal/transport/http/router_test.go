package httptransport

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestHealthReadinessAndTenantBoundaries(t *testing.T) {
	router := NewRouter(nil, func(context.Context) error {
		return errors.New("database unavailable")
	})

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

	registry := httptest.NewRecorder()
	router.Handler().ServeHTTP(registry, httptest.NewRequest(http.MethodGet, "/api/v1/registry/roles", nil))
	if registry.Code != http.StatusBadRequest {
		t.Fatalf("Registry request without tenant headers status = %d, want %d", registry.Code, http.StatusBadRequest)
	}
}
