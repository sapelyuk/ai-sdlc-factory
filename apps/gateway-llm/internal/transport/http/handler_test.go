// Package httptransport tests HTTP middleware.
package httptransport

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestTenantContextMiddleware_MissingHeaders(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	mw := TenantContextMiddleware(handler)

	// No headers → 401
	req := httptest.NewRequest(http.MethodPost, "/v1/llm/chat", nil)
	w := httptest.NewRecorder()
	mw.ServeHTTP(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Errorf("no headers: status = %d, want %d", w.Code, http.StatusUnauthorized)
	}
	if w.Body.String() == "" {
		t.Error("no error body for missing headers")
	}

	// Only workspace → 401
	req = httptest.NewRequest(http.MethodPost, "/v1/llm/chat", nil)
	req.Header.Set("X-Workspace-ID", "00000000-0000-0000-0000-000000000001")
	w = httptest.NewRecorder()
	mw.ServeHTTP(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Errorf("missing author: status = %d, want %d", w.Code, http.StatusUnauthorized)
	}
}

func TestTenantContextMiddleware_InvalidUUID(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	mw := TenantContextMiddleware(handler)

	req := httptest.NewRequest(http.MethodPost, "/v1/llm/chat", nil)
	req.Header.Set("X-Workspace-ID", "not-a-uuid")
	req.Header.Set("X-Author-ID", "00000000-0000-0000-0000-000000000001")
	w := httptest.NewRecorder()
	mw.ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Errorf("invalid workspace: status = %d, want %d", w.Code, http.StatusBadRequest)
	}

	// Invalid author → 400
	req = httptest.NewRequest(http.MethodPost, "/v1/llm/chat", nil)
	req.Header.Set("X-Workspace-ID", "00000000-0000-0000-0000-000000000001")
	req.Header.Set("X-Author-ID", "not-a-uuid")
	w = httptest.NewRecorder()
	mw.ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Errorf("invalid author: status = %d, want %d", w.Code, http.StatusBadRequest)
	}
}

func TestTenantContextMiddleware_ZeroUUID(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	mw := TenantContextMiddleware(handler)

	// Zero workspace → 401
	req := httptest.NewRequest(http.MethodPost, "/v1/llm/chat", nil)
	req.Header.Set("X-Workspace-ID", "00000000-0000-0000-0000-000000000000")
	req.Header.Set("X-Author-ID", "00000000-0000-0000-0000-000000000001")
	w := httptest.NewRecorder()
	mw.ServeHTTP(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Errorf("zero workspace: status = %d, want %d", w.Code, http.StatusUnauthorized)
	}

	// Zero author → 401
	req = httptest.NewRequest(http.MethodPost, "/v1/llm/chat", nil)
	req.Header.Set("X-Workspace-ID", "00000000-0000-0000-0000-000000000001")
	req.Header.Set("X-Author-ID", "00000000-0000-0000-0000-000000000000")
	w = httptest.NewRecorder()
	mw.ServeHTTP(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Errorf("zero author: status = %d, want %d", w.Code, http.StatusUnauthorized)
	}
}

func TestReadBody(t *testing.T) {
	// Valid JSON via strings reader
	data := []byte(`{"profile":"code-strong","messages":[{"role":"user","content":"hi"}]}`)
	var out map[string]any
	err := readBody(strings.NewReader(string(data)), &out)
	if err != nil {
		t.Errorf("readBody valid JSON: %v", err)
	}
	if out["profile"] != "code-strong" {
		t.Errorf("profile = %v, want code-strong", out["profile"])
	}

	// Invalid JSON
	var out2 map[string]any
	err = readBody(strings.NewReader(`{not json}`), &out2)
	if err == nil {
		t.Error("expected error for invalid JSON")
	}
}
