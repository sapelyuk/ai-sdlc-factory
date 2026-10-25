// Package providers tests the provider adapters against stub HTTP servers.
package providers

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/sapelyuk/ai-sdlc-factory/apps/gateway-llm/internal/domain"
)

// stubServer starts an httptest.Server with the given handler and registers
// cleanup. Adapters are built with the returned URL as their base URL.
func stubServer(t *testing.T, handler http.HandlerFunc) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	return srv
}

// staticHandler answers every request with a fixed status and JSON body.
// The request body is drained so the server can reuse the connection.
func staticHandler(status int, body string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		_ = r.Body.Close()
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = io.WriteString(w, body)
	}
}

// recordedRequest captures the last request seen by the stub so an adapter
// test can assert on the wire format it produced.
type recordedRequest struct {
	path   string
	header http.Header
	body   map[string]any
	raw    []byte
}

// handler records every request and answers with the given status and body.
func (rec *recordedRequest) handler(status int, body string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		data, err := io.ReadAll(r.Body)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		_ = r.Body.Close()
		rec.path = r.URL.Path
		rec.header = r.Header.Clone()
		rec.raw = data
		rec.body = map[string]any{}
		_ = json.Unmarshal(data, &rec.body)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = io.WriteString(w, body)
	}
}

// chatReq is the minimal conversation every adapter test sends.
func chatReq() domain.ChatRequest {
	return domain.ChatRequest{
		Messages: []domain.ChatMessage{
			{Role: "system", Content: "be brief"},
			{Role: "user", Content: "ping"},
		},
	}
}

// chatReqWithTool is chatReq plus one tool definition, used to check that an
// adapter maps the unified tool format onto its own wire representation.
func chatReqWithTool() domain.ChatRequest {
	req := chatReq()
	req.Tools = []domain.ToolDef{{
		Name:        "run_tests",
		Description: "Run the test suite",
		InputSchema: json.RawMessage(`{"type":"object"}`),
	}}
	return req
}

// assertClassify fails the test unless err classifies as want.
func assertClassify(t *testing.T, err error, want ErrorClass) {
	t.Helper()
	if got := Classify(err); got != want {
		t.Errorf("Classify(%v) = %v, want %v", err, got, want)
	}
}

// errorStatus classifies err and verifies the HTTPError envelope fields that
// the routing layer and the audit trail depend on.
func errorStatus(t *testing.T, err error, want ErrorClass, status int, provider, modelID string) {
	t.Helper()
	assertClassify(t, err, want)
	var httpErr *HTTPError
	if !errors.As(err, &httpErr) {
		t.Fatalf("error %v is not an *HTTPError", err)
	}
	if httpErr.StatusCode != status {
		t.Errorf("StatusCode = %d, want %d", httpErr.StatusCode, status)
	}
	if httpErr.Provider != provider {
		t.Errorf("Provider = %q, want %q", httpErr.Provider, provider)
	}
	if httpErr.ModelID != modelID {
		t.Errorf("ModelID = %q, want %q", httpErr.ModelID, modelID)
	}
}

// errorClasses is the status → ErrorClass matrix shared by all adapters.
var errorClasses = []struct {
	status int
	want   ErrorClass
}{
	{http.StatusBadRequest, Err400BadRequest},
	{http.StatusUnauthorized, Err401Unauthorized},
	{http.StatusForbidden, Err403Forbidden},
	{http.StatusNotFound, Err404ModelNotFound},
	{http.StatusTooManyRequests, Err429RateLimit},
	{http.StatusInternalServerError, Err5xxServerError},
	{http.StatusBadGateway, Err5xxServerError},
	{http.StatusServiceUnavailable, Err5xxServerError},
}

// headerOf returns the recorded header value, or the empty string.
func (rec *recordedRequest) headerOf(name string) string {
	if rec.header == nil {
		return ""
	}
	return rec.header.Get(name)
}
