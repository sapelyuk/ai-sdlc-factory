package httptransport

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/sapelyuk/ai-sdlc-factory/apps/tool-gateway/internal/audit"
	"github.com/sapelyuk/ai-sdlc-factory/apps/tool-gateway/internal/catalog"
	"github.com/sapelyuk/ai-sdlc-factory/apps/tool-gateway/internal/identity"
	"github.com/sapelyuk/ai-sdlc-factory/apps/tool-gateway/internal/policy"
)

type fakeAuthorizer struct {
	decision policy.Decision
	err      error
	readyErr error
	req      policy.Request
	calls    int
}

func (f *fakeAuthorizer) Authorize(_ context.Context, req policy.Request) (policy.Decision, error) {
	f.calls++
	f.req = req
	return f.decision, f.err
}

func (f *fakeAuthorizer) Ready(context.Context) error { return f.readyErr }

type fakePinger struct{ err error }

func (f fakePinger) Ping(context.Context) error { return f.err }

type nextHandler struct {
	called    bool
	decisions map[string]policy.Decision
	body      string
}

func (n *nextHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	n.called = true
	body, _ := io.ReadAll(r.Body)
	n.body = string(body)
	if ds, ok := policy.DecisionsFrom(r.Context()); ok {
		n.decisions = ds
	}
	writeJSON(w, http.StatusOK, map[string]string{"jsonrpc": "2.0", "result": "ok"})
}

func testCatalog(t *testing.T) *catalog.Catalog {
	t.Helper()
	dir := t.TempDir()
	content := `{
		"role": "software-engineer",
		"schema_version": "ai-sdlc-factory/v1",
		"tools": [
			{"name":"git.push","description":"push","input_schema":{"type":"object"},"risk_level":"R1"},
			{"name":"fs.read","description":"read","input_schema":{"type":"object"},"risk_level":"R0"}
		],
		"deny": ["deploy.prod"]
	}`
	if err := os.WriteFile(filepath.Join(dir, "developer.json"), []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	cat, err := catalog.Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	return cat
}

func newRouter(t *testing.T, auth policy.Authorizer, next *nextHandler, store *audit.MemoryStore, db Pinger) http.Handler {
	t.Helper()
	if store == nil {
		store = &audit.MemoryStore{}
	}
	return NewRouter(Options{
		MCP:        next,
		Catalog:    testCatalog(t),
		Authorizer: auth,
		Auditor:    audit.NewLogger(store),
		DB:         db,
	}).Handler()
}

func rpcRequest(t *testing.T, method, tool string) *http.Request {
	t.Helper()
	body := `{"jsonrpc":"2.0","id":1,"method":"` + method + `","params":{"name":"` + tool + `","arguments":{}}}`
	req := httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader(body))
	setIdentity(req, "developer", "R1", "")
	return req
}

func setIdentity(req *http.Request, role, autonomy, approver string) {
	req.Header.Set(identity.HeaderWorkspaceID, "11111111-1111-1111-1111-111111111111")
	req.Header.Set(identity.HeaderAgentID, "22222222-2222-2222-2222-222222222222")
	req.Header.Set(identity.HeaderAuthorID, "33333333-3333-3333-3333-333333333333")
	req.Header.Set(identity.HeaderAgentRole, role)
	req.Header.Set(identity.HeaderAutonomyLevel, autonomy)
	if approver != "" {
		req.Header.Set(identity.HeaderApproverID, approver)
	}
}

func TestHealthz(t *testing.T) {
	router := newRouter(t, &fakeAuthorizer{}, &nextHandler{}, nil, fakePinger{})
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/healthz", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "ok") {
		t.Fatalf("body = %s", rec.Body.String())
	}
}

func TestReadyz(t *testing.T) {
	t.Run("ready", func(t *testing.T) {
		router := newRouter(t, &fakeAuthorizer{}, &nextHandler{}, nil, fakePinger{})
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/readyz", nil))
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d", rec.Code)
		}
	})

	t.Run("database down", func(t *testing.T) {
		router := newRouter(t, &fakeAuthorizer{}, &nextHandler{}, nil, fakePinger{err: errors.New("down")})
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/readyz", nil))
		if rec.Code != http.StatusServiceUnavailable {
			t.Fatalf("status = %d", rec.Code)
		}
	})

	t.Run("policy down", func(t *testing.T) {
		router := newRouter(t, &fakeAuthorizer{readyErr: errors.New("down")}, &nextHandler{}, nil, fakePinger{})
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/readyz", nil))
		if rec.Code != http.StatusServiceUnavailable {
			t.Fatalf("status = %d", rec.Code)
		}
	})
}

func TestIdentityRequired(t *testing.T) {
	router := newRouter(t, &fakeAuthorizer{decision: policy.Decision{Allow: true}}, &nextHandler{}, nil, fakePinger{})
	req := httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"tools/list"}`))
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d", rec.Code)
	}
}

func TestAllowedCallReachesMCP(t *testing.T) {
	auth := &fakeAuthorizer{decision: policy.Decision{Allow: true, RiskLevel: "R1", Reason: "allowed"}}
	next := &nextHandler{}
	store := &audit.MemoryStore{}
	router := newRouter(t, auth, next, store, fakePinger{})

	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, rpcRequest(t, "tools/call", "git.push"))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", rec.Code, rec.Body.String())
	}
	if !next.called {
		t.Fatal("MCP handler was not called")
	}
	if next.decisions["git.push"].RiskLevel != "R1" {
		t.Fatalf("decision not propagated: %+v", next.decisions)
	}
	if auth.req.Role != "developer" || auth.req.ToolRiskLevel != "R1" {
		t.Fatalf("unexpected policy request: %+v", auth.req)
	}
	if len(auth.req.RoleTools) != 2 {
		t.Fatalf("role tools not passed: %+v", auth.req.RoleTools)
	}
	if len(auth.req.RoleDeny) != 1 || auth.req.RoleDeny[0] != "deploy.prod" {
		t.Fatalf("role deny not passed: %+v", auth.req.RoleDeny)
	}
	if len(store.Records()) != 0 {
		t.Fatal("the HTTP middleware must not audit allowed calls; the handler does")
	}
}

func TestDeniedCallReturns403AndIsAudited(t *testing.T) {
	auth := &fakeAuthorizer{decision: policy.Decision{
		Allow: false, RiskLevel: "R1", Reason: "role is not allowed to call this tool",
	}}
	next := &nextHandler{}
	store := &audit.MemoryStore{}
	router := newRouter(t, auth, next, store, fakePinger{})

	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, rpcRequest(t, "tools/call", "git.push"))
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d: %s", rec.Code, rec.Body.String())
	}
	if next.called {
		t.Fatal("denied call must not reach the MCP handler")
	}
	if !strings.Contains(rec.Body.String(), "policy denied") {
		t.Fatalf("body = %s", rec.Body.String())
	}

	records := store.Records()
	if len(records) != 1 || records[0].Result != audit.ResultDenied {
		t.Fatalf("unexpected audit records: %+v", records)
	}
	if records[0].RiskLevel != "R1" || records[0].ToolName != "git.push" {
		t.Fatalf("unexpected audit record: %+v", records[0])
	}
}

func TestUnknownToolIsDenied(t *testing.T) {
	auth := &fakeAuthorizer{decision: policy.Decision{Allow: true}}
	next := &nextHandler{}
	router := newRouter(t, auth, next, nil, fakePinger{})

	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, rpcRequest(t, "tools/call", "deploy.prod"))
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d", rec.Code)
	}
	if auth.calls != 0 {
		t.Fatal("policy must not be called for an unknown tool")
	}
}

func TestNonToolCallPassesThrough(t *testing.T) {
	auth := &fakeAuthorizer{}
	next := &nextHandler{}
	router := newRouter(t, auth, next, nil, fakePinger{})

	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, rpcRequest(t, "tools/list", ""))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	if !next.called {
		t.Fatal("request was not forwarded")
	}
	if auth.calls != 0 {
		t.Fatal("non-tool requests must not be authorized")
	}
}

func TestBatchCallDenied(t *testing.T) {
	authFn := &sequenceAuthorizer{decisions: []policy.Decision{
		{Allow: true, RiskLevel: "R0"},
		{Allow: false, RiskLevel: "R1", Reason: "denied"},
	}}
	next := &nextHandler{}
	router := newRouter(t, authFn, next, nil, fakePinger{})

	body := `[{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"fs.read"}},` +
		`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"git.push"}}]`
	req := httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader(body))
	setIdentity(req, "developer", "R1", "")
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d", rec.Code)
	}
	if next.called {
		t.Fatal("a denied batch must not reach the MCP handler")
	}
}

func TestPolicyEngineErrorReturns503(t *testing.T) {
	auth := &fakeAuthorizer{err: policy.ErrUnavailable}
	next := &nextHandler{}
	router := newRouter(t, auth, next, nil, fakePinger{})

	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, rpcRequest(t, "tools/call", "git.push"))
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d", rec.Code)
	}
	if next.called {
		t.Fatal("call must not reach the MCP handler")
	}
}

func TestBodyTooLarge(t *testing.T) {
	router := newRouter(t, &fakeAuthorizer{decision: policy.Decision{Allow: true}}, &nextHandler{}, nil, fakePinger{})
	body := strings.Repeat("x", maxRequestBodyBytes+10)
	req := httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader(body))
	setIdentity(req, "developer", "R1", "")
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("status = %d", rec.Code)
	}
}

func TestMalformedJSONPassesToMCP(t *testing.T) {
	auth := &fakeAuthorizer{}
	next := &nextHandler{}
	router := newRouter(t, auth, next, nil, fakePinger{})

	req := httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader(`{not json`))
	setIdentity(req, "developer", "R1", "")
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	if !next.called {
		t.Fatal("malformed body must be left to the MCP server")
	}
	if auth.calls != 0 {
		t.Fatal("malformed body must not be authorized")
	}
}

func TestApproverIsPassedToPolicy(t *testing.T) {
	auth := &fakeAuthorizer{decision: policy.Decision{Allow: true, RiskLevel: "R3"}}
	next := &nextHandler{}
	router := newRouter(t, auth, next, nil, fakePinger{})

	req := rpcRequest(t, "tools/call", "fs.read")
	setIdentity(req, "admin", "R3", "44444444-4444-4444-4444-444444444444")
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	if auth.req.ApproverID != "44444444-4444-4444-4444-444444444444" {
		t.Fatalf("approver = %q", auth.req.ApproverID)
	}
}

func TestGETMCPRequiresIdentity(t *testing.T) {
	next := &nextHandler{}
	router := newRouter(t, &fakeAuthorizer{}, next, nil, fakePinger{})

	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/mcp", nil))
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d", rec.Code)
	}

	req := httptest.NewRequest(http.MethodGet, "/mcp", nil)
	setIdentity(req, "developer", "R1", "")
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || !next.called {
		t.Fatalf("status = %d, called = %v", rec.Code, next.called)
	}
}

func TestAuditFailureOnDenialStillReturns403(t *testing.T) {
	auth := &fakeAuthorizer{decision: policy.Decision{Allow: false, RiskLevel: "R1", Reason: "denied"}}
	router := NewRouter(Options{
		MCP:        &nextHandler{},
		Catalog:    testCatalog(t),
		Authorizer: auth,
		Auditor:    audit.NewLogger(failingStore{}),
		DB:         fakePinger{},
	}).Handler()

	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, rpcRequest(t, "tools/call", "git.push"))
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d", rec.Code)
	}
}

func TestRPCErrorShape(t *testing.T) {
	rec := httptest.NewRecorder()
	writeRPCError(rec, json.RawMessage("7"), http.StatusForbidden, "denied",
		map[string]string{"tool": "git.push"})
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d", rec.Code)
	}
	var payload struct {
		JSONRPC string `json:"jsonrpc"`
		ID      int    `json:"id"`
		Error   struct {
			Code    int    `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if payload.ID != 7 || payload.Error.Code != codePolicyDenied {
		t.Fatalf("unexpected payload: %+v", payload)
	}
}

type sequenceAuthorizer struct {
	decisions []policy.Decision
	idx       int
}

func (s *sequenceAuthorizer) Authorize(context.Context, policy.Request) (policy.Decision, error) {
	d := s.decisions[s.idx]
	s.idx++
	return d, nil
}

func TestVerifyAuditEndpoint(t *testing.T) {
	t.Run("missing workspace_id", func(t *testing.T) {
		router := newRouter(t, &fakeAuthorizer{}, &nextHandler{}, nil, fakePinger{})
		req := httptest.NewRequest(http.MethodGet, "/admin/audit/verify", nil)
		setIdentity(req, "admin", "R0", "")
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("status = %d, want 400", rec.Code)
		}
	})

	t.Run("non-admin denied", func(t *testing.T) {
		router := newRouter(t, &fakeAuthorizer{}, &nextHandler{}, nil, fakePinger{})
		req := httptest.NewRequest(http.MethodGet, "/admin/audit/verify?workspace_id=11111111-1111-1111-1111-111111111111", nil)
		setIdentity(req, "developer", "R0", "")
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)
		if rec.Code != http.StatusForbidden {
			t.Fatalf("status = %d, want 403", rec.Code)
		}
	})

	t.Run("admin gets ok", func(t *testing.T) {
		router := newRouter(t, &fakeAuthorizer{}, &nextHandler{}, nil, fakePinger{})
		req := httptest.NewRequest(http.MethodGet, "/admin/audit/verify?workspace_id=11111111-1111-1111-1111-111111111111", nil)
		setIdentity(req, "admin", "R0", "")
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200", rec.Code)
		}
		if !strings.Contains(rec.Body.String(), "ok") {
			t.Fatalf("body = %s", rec.Body.String())
		}
	})
}

func TestRunAuditEndpointsUseWorkspaceIdentity(t *testing.T) {
	const workspaceID = "11111111-1111-1111-1111-111111111111"
	const agentID = "22222222-2222-2222-2222-222222222222"
	store := audit.NewMemoryStore()
	logger := audit.NewLogger(store)
	_, err := logger.Record(context.Background(), audit.Entry{
		WorkspaceID: uuid.MustParse(workspaceID),
		AgentID:     uuid.MustParse(agentID),
		ToolName:    "run-123",
		RiskLevel:   "R1",
		Result:      audit.ResultSuccess,
	})
	if err != nil {
		t.Fatal(err)
	}
	router := NewRouter(Options{
		MCP:        &nextHandler{},
		Catalog:    testCatalog(t),
		Authorizer: &fakeAuthorizer{},
		Auditor:    logger,
		DB:         fakePinger{},
	}).Handler()

	t.Run("query run records", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/audit?run_id=run-123", nil)
		setIdentity(req, "admin", "R0", "")
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"resource_id":"run-123"`) {
			t.Fatalf("status/body = %d/%s", rec.Code, rec.Body)
		}
	})

	t.Run("verify current workspace", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/audit/verify", nil)
		setIdentity(req, "admin", "R0", "")
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"status":"ok"`) {
			t.Fatalf("status/body = %d/%s", rec.Code, rec.Body)
		}
	})

	t.Run("reject non-admin", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/audit?run_id=run-123", nil)
		setIdentity(req, "developer", "R0", "")
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)
		if rec.Code != http.StatusForbidden {
			t.Fatalf("status = %d, want 403", rec.Code)
		}
	})
}

func TestRunAuditHandlerMatchesRunIDInGlobalDetails(t *testing.T) {
	const workspaceID = "11111111-1111-1111-1111-111111111111"
	store := fixedAuditRecordStore{records: []audit.Record{{
		ID:          uuid.New(),
		WorkspaceID: uuid.MustParse(workspaceID),
		ActorID:     "system:worker",
		Action:      "run_started",
		ResourceID:  "task-run-1",
		Details:     json.RawMessage(`{"run_id":"run-123"}`),
	}}}
	router := NewRouter(Options{
		MCP:        &nextHandler{},
		Catalog:    testCatalog(t),
		Authorizer: &fakeAuthorizer{},
		Auditor:    audit.NewLogger(store),
		DB:         fakePinger{},
	}).Handler()
	req := httptest.NewRequest(http.MethodGet, "/audit?run_id=run-123", nil)
	setIdentity(req, "admin", "R0", "")
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"action":"run_started"`) {
		t.Fatalf("status/body = %d/%s", rec.Code, rec.Body)
	}
}

type fixedAuditRecordStore struct {
	records []audit.Record
}

func (s fixedAuditRecordStore) Append(context.Context, uuid.UUID, func(string) (audit.Record, error)) (audit.Record, error) {
	return audit.Record{}, errors.New("append not supported")
}

func (s fixedAuditRecordStore) Query(_ context.Context, workspaceID uuid.UUID) ([]audit.Record, error) {
	records := make([]audit.Record, 0, len(s.records))
	for _, record := range s.records {
		if record.WorkspaceID == workspaceID {
			records = append(records, record)
		}
	}
	return records, nil
}

type failingStore struct{}

func (failingStore) Append(context.Context, uuid.UUID, func(string) (audit.Record, error)) (audit.Record, error) {
	return audit.Record{}, errors.New("store down")
}

func (failingStore) Query(context.Context, uuid.UUID) ([]audit.Record, error) {
	return nil, errors.New("store down")
}
