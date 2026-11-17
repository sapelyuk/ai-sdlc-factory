package httptransport

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"
	"github.com/sapelyuk/ai-sdlc-factory/apps/gateway-llm/internal/domain"
	"github.com/sapelyuk/ai-sdlc-factory/apps/gateway-llm/internal/identity"
	"github.com/sapelyuk/ai-sdlc-factory/apps/gateway-llm/internal/routing"
	"github.com/sapelyuk/ai-sdlc-factory/packages/sdk-go/idempotency"
	postgrescontainer "github.com/testcontainers/testcontainers-go/modules/postgres"
	rediscontainer "github.com/testcontainers/testcontainers-go/modules/redis"
)

// ── Stubs ─────────────────────────────────────────────────────

// stubRouter implements ChatRouter without a database or live providers.
type stubRouter struct {
	res     *routing.Result
	err     error
	profile string
	req     domain.ChatRequest
	meta    routing.CallMeta
	calls   int
}

func (s *stubRouter) Chat(ctx context.Context, profileName string, req domain.ChatRequest, meta routing.CallMeta) (*routing.Result, error) {
	s.calls++
	s.profile = profileName
	s.req = req
	s.meta = meta
	return s.res, s.err
}

// stubPinger implements Pinger for the readiness probe.
type stubPinger struct {
	err   error
	calls int
}

func (s *stubPinger) Ping(context.Context) error {
	s.calls++
	return s.err
}

// succeededResult builds a routing result with dual-level usage attached.
func succeededResult() *routing.Result {
	tokensIn, tokensOut := 50, 200
	finalCost, totalCost := 0.0008, 0.0044
	return &routing.Result{
		LogicalCall: &domain.LogicalCall{
			ID:             uuid.New(),
			ProfileName:    "code-strong",
			ModelIDUsed:    "claude-sonnet-4-20250101",
			ProviderUsed:   "anthropic",
			Status:         "succeeded",
			AttemptCount:   2,
			FinalTokensIn:  &tokensIn,
			FinalTokensOut: &tokensOut,
			FinalCostUSD:   &finalCost,
			TotalTokensIn:  150,
			TotalTokensOut: 200,
			TotalCostUSD:   totalCost,
		},
		Content: "function sort(arr) {}",
	}
}

// newChatRequest builds a POST /v1/llm/chat request carrying a tenant context.
func newChatRequest(t *testing.T, body string) *http.Request {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/v1/llm/chat", strings.NewReader(body))
	ctx := identity.WithTenant(req.Context(), uuid.New(), uuid.New())
	return req.WithContext(ctx)
}

// decodeChat parses a chatResponse from the recorder.
func decodeChat(t *testing.T, w *httptest.ResponseRecorder) chatResponse {
	t.Helper()
	var out chatResponse
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode response %q: %v", w.Body.String(), err)
	}
	return out
}

// ── Handler tests ─────────────────────────────────────────────

func TestChatHandler_Success(t *testing.T) {
	stub := &stubRouter{res: succeededResult()}
	handler := chatHandler(stub, nil)

	w := httptest.NewRecorder()
	handler.ServeHTTP(w, newChatRequest(t, `{"profile":"code-strong","messages":[{"role":"user","content":"sort an array"}]}`))

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body %s)", w.Code, w.Body.String())
	}
	if got := w.Header().Get("Content-Type"); got != "application/json" {
		t.Errorf("Content-Type = %q, want application/json", got)
	}

	res := decodeChat(t, w)
	if res.Status != "succeeded" {
		t.Errorf("status = %q, want succeeded", res.Status)
	}
	if res.ModelUsed != "claude-sonnet-4-20250101" || res.ProviderUsed != "anthropic" {
		t.Errorf("model/provider = %q/%q, want claude-sonnet-4-20250101/anthropic", res.ModelUsed, res.ProviderUsed)
	}
	if res.LogicalCallID == "" || res.LogicalCallID != stub.res.LogicalCall.ID.String() {
		t.Errorf("logical_call_id = %q, want %s", res.LogicalCallID, stub.res.LogicalCall.ID)
	}
	if res.Content != "function sort(arr) {}" {
		t.Errorf("content = %q, unexpected", res.Content)
	}

	// Dual-level cost: cost_usd is the successful attempt, total_cost_usd the
	// sum over all attempts (ADR-021, NFR-9).
	if res.Usage.TokensIn != 50 || res.Usage.TokensOut != 200 {
		t.Errorf("usage tokens = %d/%d, want 50/200", res.Usage.TokensIn, res.Usage.TokensOut)
	}
	if res.Usage.CostUSD != 0.0008 {
		t.Errorf("cost_usd = %v, want 0.0008", res.Usage.CostUSD)
	}
	if res.Usage.TotalTokensIn != 150 || res.Usage.TotalTokensOut != 200 {
		t.Errorf("total tokens = %d/%d, want 150/200", res.Usage.TotalTokensIn, res.Usage.TotalTokensOut)
	}
	if res.Usage.TotalCostUSD != 0.0044 {
		t.Errorf("total_cost_usd = %v, want 0.0044", res.Usage.TotalCostUSD)
	}

	// The handler must forward the profile, messages and attribution ids.
	if stub.profile != "code-strong" {
		t.Errorf("router saw profile %q, want code-strong", stub.profile)
	}
	if len(stub.req.Messages) != 1 || stub.req.Messages[0].Content != "sort an array" {
		t.Errorf("router saw messages %+v", stub.req.Messages)
	}
	if stub.calls != 1 {
		t.Errorf("router calls = %d, want 1", stub.calls)
	}
}

// TestChatHandler_AttributionPassthrough checks that run/task/agent ids from
// the body reach the routing layer (FR-ATTRIBUTION: cost roll-up by run).
func TestChatHandler_AttributionPassthrough(t *testing.T) {
	runID, taskID, agentID := uuid.New(), uuid.New(), uuid.New()
	stub := &stubRouter{res: succeededResult()}
	handler := chatHandler(stub, nil)

	body := `{"profile":"code-strong","messages":[{"role":"user","content":"hi"}],"tools":[{"name":"run_tests"}],"run_id":"` +
		runID.String() + `","task_id":"` + taskID.String() + `","agent_id":"` + agentID.String() + `"}`
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, newChatRequest(t, body))

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body %s)", w.Code, w.Body.String())
	}
	if stub.meta.RunID == nil || *stub.meta.RunID != runID {
		t.Errorf("run_id = %v, want %s", stub.meta.RunID, runID)
	}
	if stub.meta.TaskID == nil || *stub.meta.TaskID != taskID {
		t.Errorf("task_id = %v, want %s", stub.meta.TaskID, taskID)
	}
	if stub.meta.AgentID == nil || *stub.meta.AgentID != agentID {
		t.Errorf("agent_id = %v, want %s", stub.meta.AgentID, agentID)
	}
	if len(stub.req.Tools) != 1 || stub.req.Tools[0].Name != "run_tests" {
		t.Errorf("tools = %+v, want the run_tests tool", stub.req.Tools)
	}
}

func TestChatHandler_InvalidBody(t *testing.T) {
	tests := []struct {
		name string
		body string
	}{
		{"not JSON", `{not json}`},
		{"empty body", ``},
		{"wrong type", `[1,2,3]`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			stub := &stubRouter{res: succeededResult()}
			w := httptest.NewRecorder()
			chatHandler(stub, nil).ServeHTTP(w, newChatRequest(t, tt.body))

			if w.Code != http.StatusBadRequest {
				t.Errorf("status = %d, want 400 (body %s)", w.Code, w.Body.String())
			}
			if !strings.Contains(w.Body.String(), "invalid JSON request body") {
				t.Errorf("body = %s, want an invalid-JSON message", w.Body.String())
			}
			if stub.calls != 0 {
				t.Errorf("router calls = %d, want 0 for a rejected body", stub.calls)
			}
		})
	}
}

func TestChatHandler_MissingFields(t *testing.T) {
	tests := []struct {
		name string
		body string
		want string
	}{
		{"no profile", `{"messages":[{"role":"user","content":"hi"}]}`, "profile is required"},
		{"no messages", `{"profile":"code-strong"}`, "messages must not be empty"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			stub := &stubRouter{res: succeededResult()}
			w := httptest.NewRecorder()
			chatHandler(stub, nil).ServeHTTP(w, newChatRequest(t, tt.body))

			if w.Code != http.StatusUnprocessableEntity {
				t.Errorf("status = %d, want 422 (body %s)", w.Code, w.Body.String())
			}
			if !strings.Contains(w.Body.String(), tt.want) {
				t.Errorf("body = %s, want %q", w.Body.String(), tt.want)
			}
		})
	}
}

// TestChatHandler_UnknownProfile pins the reviewer's blocker: an unknown
// profile is a client mistake (400), not a capacity outage (503).
func TestChatHandler_UnknownProfile(t *testing.T) {
	stub := &stubRouter{err: domain.ErrUnknownProfile}
	w := httptest.NewRecorder()
	chatHandler(stub, nil).ServeHTTP(w, newChatRequest(t, `{"profile":"does-not-exist","messages":[{"role":"user","content":"hi"}]}`))

	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 (body %s)", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), domain.ErrUnknownProfile.Error()) {
		t.Errorf("body = %s, want the unknown-profile message", w.Body.String())
	}
}

func TestChatHandler_NoAvailableModels(t *testing.T) {
	stub := &stubRouter{err: domain.ErrNoAvailable}
	w := httptest.NewRecorder()
	chatHandler(stub, nil).ServeHTTP(w, newChatRequest(t, `{"profile":"code-strong","messages":[{"role":"user","content":"hi"}]}`))

	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503 (body %s)", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), domain.ErrNoAvailable.Error()) {
		t.Errorf("body = %s, want the no-available message", w.Body.String())
	}
}

func TestChatHandler_MissingTenantContext(t *testing.T) {
	stub := &stubRouter{res: succeededResult()}
	req := httptest.NewRequest(http.MethodPost, "/v1/llm/chat",
		strings.NewReader(`{"profile":"code-strong","messages":[{"role":"user","content":"hi"}]}`))
	w := httptest.NewRecorder()
	chatHandler(stub, nil).ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401 (body %s)", w.Code, w.Body.String())
	}
	if stub.calls != 0 {
		t.Errorf("router calls = %d, want 0 without a tenant", stub.calls)
	}
}

// TestChatHandler_AllFailed checks that a completed routing loop with every
// attempt failed is reported as 502 and still carries the logical call id,
// so the spend recorded by the gateway stays auditable.
func TestChatHandler_AllFailed(t *testing.T) {
	res := succeededResult()
	res.LogicalCall.Status = "failed"
	res.LogicalCall.ModelIDUsed = ""
	res.LogicalCall.ProviderUsed = ""
	res.Content = ""

	stub := &stubRouter{res: res, err: domain.ErrAllFailed}
	w := httptest.NewRecorder()
	chatHandler(stub, nil).ServeHTTP(w, newChatRequest(t, `{"profile":"code-strong","messages":[{"role":"user","content":"hi"}]}`))

	if w.Code != http.StatusBadGateway {
		t.Fatalf("status = %d, want 502 (body %s)", w.Code, w.Body.String())
	}
	out := decodeChat(t, w)
	if out.LogicalCallID != res.LogicalCall.ID.String() {
		t.Errorf("logical_call_id = %q, want %s", out.LogicalCallID, res.LogicalCall.ID)
	}
	if out.Status != "failed" {
		t.Errorf("status = %q, want failed", out.Status)
	}
	if out.Usage.TotalCostUSD != res.LogicalCall.TotalCostUSD {
		t.Errorf("total_cost_usd = %v, want %v", out.Usage.TotalCostUSD, res.LogicalCall.TotalCostUSD)
	}
}

// TestChatHandler_WrappedRoutingError checks that the handler unwraps the
// profile-wrapped catalog errors produced by the router (fmt.Errorf %w),
// instead of collapsing them into 500.
func TestChatHandler_WrappedRoutingError(t *testing.T) {
	tests := []struct {
		name     string
		err      error
		wantCode int
	}{
		{"wrapped unknown profile", errors.New("ctx: " + domain.ErrUnknownProfile.Error()), http.StatusInternalServerError},
		{"joined unknown profile", errors.Join(errors.New("noise"), domain.ErrUnknownProfile), http.StatusBadRequest},
		{"joined no available", errors.Join(errors.New("noise"), domain.ErrNoAvailable), http.StatusServiceUnavailable},
		{"joined tenant context", errors.Join(errors.New("noise"), identity.ErrMissingTenantContext), http.StatusUnauthorized},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			stub := &stubRouter{err: tt.err}
			w := httptest.NewRecorder()
			chatHandler(stub, nil).ServeHTTP(w, newChatRequest(t, `{"profile":"code-strong","messages":[{"role":"user","content":"hi"}]}`))
			if w.Code != tt.wantCode {
				t.Errorf("status = %d, want %d (body %s)", w.Code, tt.wantCode, w.Body.String())
			}
		})
	}
}

// ── Health endpoints ──────────────────────────────────────────

func TestHealthz(t *testing.T) {
	stub := &stubPinger{err: errors.New("database down")}
	handler := NewRouter(&stubRouter{res: succeededResult()}, stub, nil).Handler()

	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	if !strings.Contains(w.Body.String(), `"status":"ok"`) {
		t.Errorf("body = %s, want status ok", w.Body.String())
	}
	// Liveness must not touch the database.
	if stub.calls != 0 {
		t.Errorf("database pings = %d, want 0", stub.calls)
	}
}

func TestReadyz(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want int
	}{
		{"database reachable", nil, http.StatusOK},
		{"database down", errors.New("connection refused"), http.StatusServiceUnavailable},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			handler := NewRouter(&stubRouter{res: succeededResult()}, &stubPinger{err: tt.err}, nil).Handler()

			req := httptest.NewRequest(http.MethodGet, "/readyz", nil)
			w := httptest.NewRecorder()
			handler.ServeHTTP(w, req)

			if w.Code != tt.want {
				t.Fatalf("status = %d, want %d (body %s)", w.Code, tt.want, w.Body.String())
			}
			if tt.err == nil && !strings.Contains(w.Body.String(), `"status":"ready"`) {
				t.Errorf("body = %s, want status ready", w.Body.String())
			}
			if tt.err != nil && !strings.Contains(w.Body.String(), "database unavailable") {
				t.Errorf("body = %s, want a database-unavailable message", w.Body.String())
			}
		})
	}
}

// TestReadyzDeadline checks the probe cannot outlive its 2 second budget when
// the database stops answering.
func TestReadyzDeadline(t *testing.T) {
	handler := NewRouter(&stubRouter{res: succeededResult()}, blockingPinger{}, nil).Handler()

	start := time.Now()
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/readyz", nil))
	elapsed := time.Since(start)

	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", w.Code)
	}
	if elapsed > 5*time.Second {
		t.Errorf("readyz took %v, want it to stop at the probe deadline", elapsed)
	}
}

// blockingPinger never answers, only honours context cancellation.
type blockingPinger struct{}

func (blockingPinger) Ping(ctx context.Context) error {
	<-ctx.Done()
	return ctx.Err()
}

// ── Full route wiring ─────────────────────────────────────────

// TestChatRoute_RequiresTenantHeaders covers the middleware + handler chain:
// the tenant headers are what makes the route reachable.
func TestChatRoute_RequiresTenantHeaders(t *testing.T) {
	stub := &stubRouter{res: succeededResult()}
	handler := NewRouter(stub, &stubPinger{}, nil).Handler()

	body := `{"profile":"code-strong","messages":[{"role":"user","content":"hi"}]}`

	// No headers → 401 from the middleware, router never called.
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/llm/chat", strings.NewReader(body))
	handler.ServeHTTP(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Errorf("no headers: status = %d, want 401", w.Code)
	}
	if stub.calls != 0 {
		t.Errorf("no headers: router calls = %d, want 0", stub.calls)
	}

	// Valid headers → 200 and the tenant reaches the routing layer.
	workspaceID, authorID := uuid.New(), uuid.New()
	w = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "/v1/llm/chat", strings.NewReader(body))
	req.Header.Set("X-Workspace-ID", workspaceID.String())
	req.Header.Set("X-Author-ID", authorID.String())
	handler.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("valid headers: status = %d, want 200 (body %s)", w.Code, w.Body.String())
	}
	if stub.calls != 1 {
		t.Errorf("valid headers: router calls = %d, want 1", stub.calls)
	}
}

// TestChatRoute_MethodNotAllowed guards the 405 path of the chi mux.
func TestChatRoute_MethodNotAllowed(t *testing.T) {
	handler := NewRouter(&stubRouter{res: succeededResult()}, &stubPinger{}, nil).Handler()

	w := httptest.NewRecorder()
	handler.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/v1/llm/chat", nil))
	if w.Code == http.StatusOK {
		t.Fatalf("GET /v1/llm/chat returned 200, want a method mismatch")
	}
}

// TestWriteJSONErrorOnUnencodableValue covers the encoder failure branch.
func TestWriteJSONErrorOnUnencodableValue(t *testing.T) {
	w := httptest.NewRecorder()
	writeJSON(w, http.StatusOK, func() {})

	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", w.Code)
	}
	// Headers were already sent, so the encoder error cannot change the
	// status; it must not panic either.
	if _, err := io.ReadAll(w.Body); err != nil {
		t.Errorf("read body: %v", err)
	}
}

// ── Idempotency tests (issue #106) ─────────────────────────────

// redisImage pins the same digest as deploy/compose/docker-compose.yml so the
// integration tests run against the exact image the stack ships (ADR-026).
const redisImage = "docker.io/library/redis:7-alpine@sha256:7f7d30b7fdd09467bac58198a1f166dd69789d348d4fe859cccb20e0cf287fbe"

// startTestRedis starts a Redis container for integration tests.
func startTestRedis(t *testing.T) *idempotency.Cache {
	t.Helper()
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}
	if output, err := exec.CommandContext(t.Context(), "docker", "info").CombinedOutput(); err != nil {
		if ci := os.Getenv("CI"); ci != "" && ci != "false" {
			t.Fatalf("Docker required for integration test: %s", strings.TrimSpace(string(output)))
		}
		t.Skipf("Docker unavailable; skipping: %s", strings.TrimSpace(string(output)))
	}

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	t.Cleanup(cancel)

	container, err := rediscontainer.Run(ctx, redisImage)
	if err != nil {
		t.Fatalf("failed to start Redis container: %v", err)
	}
	t.Cleanup(func() { _ = container.Terminate(context.Background()) })

	connStr, err := container.ConnectionString(ctx)
	if err != nil {
		t.Fatalf("connection string: %v", err)
	}

	redisOpts, err := redis.ParseURL(connStr)
	if err != nil {
		t.Fatalf("parse URL %q: %v", connStr, err)
	}
	client := redis.NewClient(redisOpts)
	t.Cleanup(func() { _ = client.Close() })

	if err := client.FlushAll(ctx).Err(); err != nil {
		t.Fatalf("flush: %v", err)
	}
	return idempotency.New(client, nil, nil)
}

type logicalCallRouter struct {
	pool        *pgxpool.Pool
	workspaceID uuid.UUID
	calls       int
}

func (r *logicalCallRouter) Chat(ctx context.Context, profileName string, req domain.ChatRequest, meta routing.CallMeta) (*routing.Result, error) {
	r.calls++
	var logicalCallID uuid.UUID
	if err := r.pool.QueryRow(ctx, `
		INSERT INTO llm_logical_calls
			(workspace_id, run_id, task_id, agent_id, profile_name,
			 model_id_used, provider_used, status, started_at)
		VALUES ($1, $2, $3, $4, $5, 'claude-sonnet-4-20250101', 'anthropic', 'succeeded', $6)
		RETURNING id`,
		r.workspaceID, meta.RunID, meta.TaskID, meta.AgentID, profileName, time.Now().UTC()).Scan(&logicalCallID); err != nil {
		return nil, err
	}
	return &routing.Result{
		LogicalCall: &domain.LogicalCall{
			ID:             logicalCallID,
			WorkspaceID:    r.workspaceID,
			ProfileName:    profileName,
			ModelIDUsed:    "claude-sonnet-4-20250101",
			ProviderUsed:   "anthropic",
			Status:         "succeeded",
			FinalTokensIn:  ptrInt(50),
			FinalTokensOut: ptrInt(200),
			FinalCostUSD:   ptrFloat(0.0008),
			TotalTokensIn:  150,
			TotalTokensOut: 200,
			TotalCostUSD:   0.0044,
		},
		Content: "function sort(arr) {}",
	}, nil
}

func startTestPostgres(t *testing.T) *pgxpool.Pool {
	t.Helper()
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}
	if output, err := exec.CommandContext(t.Context(), "docker", "info").CombinedOutput(); err != nil {
		if ci := os.Getenv("CI"); ci != "" && ci != "false" {
			t.Fatalf("Docker required for integration test: %s", strings.TrimSpace(string(output)))
		}
		t.Skipf("Docker unavailable; skipping: %s", strings.TrimSpace(string(output)))
	}

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	t.Cleanup(cancel)

	container, err := postgrescontainer.Run(ctx, "pgvector/pgvector:pg18",
		postgrescontainer.WithDatabase("llm_gateway_test"),
		postgrescontainer.WithUsername("llm_gateway_test"),
		postgrescontainer.WithPassword("llm_gateway_test"),
		postgrescontainer.BasicWaitStrategies(),
	)
	if err != nil {
		t.Fatalf("start PostgreSQL container: %v", err)
	}
	t.Cleanup(func() { _ = container.Terminate(context.Background()) })

	connStr, err := container.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		t.Fatalf("get PostgreSQL connection string: %v", err)
	}
	pool, err := pgxpool.New(ctx, connStr)
	if err != nil {
		t.Fatalf("create PostgreSQL pool: %v", err)
	}
	t.Cleanup(pool.Close)
	if err := pool.Ping(ctx); err != nil {
		t.Fatalf("connect to PostgreSQL container: %v", err)
	}

	migrationPath := filepath.Join("..", "..", "..", "..", "..", "migrations", "postgres", "0010_llm_gateway.up.sql")
	contents, err := os.ReadFile(migrationPath)
	if err != nil {
		t.Fatalf("read migration file %s: %v", migrationPath, err)
	}
	if _, err := pool.Exec(ctx, string(contents)); err != nil {
		t.Fatalf("apply migration %s: %v", migrationPath, err)
	}
	return pool
}

func ptrInt(v int) *int           { return &v }
func ptrFloat(v float64) *float64 { return &v }

// newIdempotencyRequest builds a POST /v1/llm/chat with tenant + idempotency headers.
func newIdempotencyRequest(t *testing.T, body, workspaceID, idemKey string) *http.Request {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/v1/llm/chat", strings.NewReader(body))
	ctx := identity.WithTenant(req.Context(), uuid.MustParse(workspaceID), uuid.New())
	req = req.WithContext(ctx)
	if idemKey != "" {
		req.Header.Set("X-Idempotency-Key", idemKey)
	}
	return req
}

const chatBody = `{"profile":"code-strong","messages":[{"role":"user","content":"hi"}]}`

func TestChatHandler_IdempotencyKey_RetryDoesNotCallProviderTwice(t *testing.T) {
	cache := startTestRedis(t)
	stub := &stubRouter{res: succeededResult()}
	handler := chatHandler(stub, cache)

	wsID := uuid.New().String()
	idemKey := "idem-" + uuid.New().String()

	// First request: miss → provider called.
	w1 := httptest.NewRecorder()
	handler.ServeHTTP(w1, newIdempotencyRequest(t, chatBody, wsID, idemKey))
	if w1.Code != http.StatusOK {
		t.Fatalf("first request: status = %d (body %s)", w1.Code, w1.Body.String())
	}
	if stub.calls != 1 {
		t.Fatalf("provider calls after first = %d, want 1", stub.calls)
	}
	if got := w1.Header().Get("X-Idempotency-Cache"); got != "miss" {
		t.Errorf("first X-Idempotency-Cache = %q, want miss", got)
	}

	// Second request: same key → cache hit → provider NOT called.
	w2 := httptest.NewRecorder()
	handler.ServeHTTP(w2, newIdempotencyRequest(t, chatBody, wsID, idemKey))
	if w2.Code != http.StatusOK {
		t.Fatalf("second request: status = %d (body %s)", w2.Code, w2.Body.String())
	}
	if stub.calls != 1 {
		t.Fatalf("provider calls after second = %d, want 1 (must not be called twice)", stub.calls)
	}
	if got := w2.Header().Get("X-Idempotency-Cache"); got != "hit" {
		t.Errorf("second X-Idempotency-Cache = %q, want hit", got)
	}
	// Responses must be identical.
	if w1.Body.String() != w2.Body.String() {
		t.Errorf("responses differ:\n  first:  %s\n  second: %s", w1.Body.String(), w2.Body.String())
	}
}

func TestChatHandler_IdempotencyKey_CachedRetryDoesNotCreateLogicalCall(t *testing.T) {
	pool := startTestPostgres(t)
	cache := startTestRedis(t)
	wsID := uuid.New()
	router := &logicalCallRouter{pool: pool, workspaceID: wsID}
	handler := chatHandler(router, cache)
	idemKey := "idem-" + uuid.New().String()

	w1 := httptest.NewRecorder()
	handler.ServeHTTP(w1, newIdempotencyRequest(t, chatBody, wsID.String(), idemKey))
	if w1.Code != http.StatusOK {
		t.Fatalf("first request: status = %d (body %s)", w1.Code, w1.Body.String())
	}

	w2 := httptest.NewRecorder()
	handler.ServeHTTP(w2, newIdempotencyRequest(t, chatBody, wsID.String(), idemKey))
	if w2.Code != http.StatusOK {
		t.Fatalf("second request: status = %d (body %s)", w2.Code, w2.Body.String())
	}
	if got := w2.Header().Get("X-Idempotency-Cache"); got != "hit" {
		t.Fatalf("second X-Idempotency-Cache = %q, want hit", got)
	}

	var count int
	if err := pool.QueryRow(context.Background(), "SELECT COUNT(*) FROM llm_logical_calls").Scan(&count); err != nil {
		t.Fatalf("COUNT(*) FROM llm_logical_calls: %v", err)
	}
	if count != 1 {
		t.Fatalf("llm_logical_calls count = %d, want 1 (cached retry must not create a new logical call)", count)
	}
}

func TestChatHandler_IdempotencyKey_NoKey_NoCache(t *testing.T) {
	cache := startTestRedis(t)
	stub := &stubRouter{res: succeededResult()}
	handler := chatHandler(stub, cache)

	wsID := uuid.New().String()

	// Two requests without idempotency key → 2 provider calls.
	for i := 0; i < 2; i++ {
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, newIdempotencyRequest(t, chatBody, wsID, ""))
		if w.Code != http.StatusOK {
			t.Fatalf("request %d: status = %d", i, w.Code)
		}
	}
	if stub.calls != 2 {
		t.Errorf("provider calls = %d, want 2 (no caching without key)", stub.calls)
	}
}

func TestChatHandler_IdempotencyKey_DifferentKey_DifferentCache(t *testing.T) {
	cache := startTestRedis(t)
	stub := &stubRouter{res: succeededResult()}
	handler := chatHandler(stub, cache)

	wsID := uuid.New().String()

	w1 := httptest.NewRecorder()
	handler.ServeHTTP(w1, newIdempotencyRequest(t, chatBody, wsID, "key-A"))

	w2 := httptest.NewRecorder()
	handler.ServeHTTP(w2, newIdempotencyRequest(t, chatBody, wsID, "key-B"))

	if stub.calls != 2 {
		t.Errorf("provider calls = %d, want 2 (different keys)", stub.calls)
	}
}

func TestChatHandler_IdempotencyKey_WorkspaceIsolation(t *testing.T) {
	cache := startTestRedis(t)
	stub := &stubRouter{res: succeededResult()}
	handler := chatHandler(stub, cache)

	idemKey := "shared-key"
	wsA := uuid.New().String()
	wsB := uuid.New().String()

	w1 := httptest.NewRecorder()
	handler.ServeHTTP(w1, newIdempotencyRequest(t, chatBody, wsA, idemKey))

	w2 := httptest.NewRecorder()
	handler.ServeHTTP(w2, newIdempotencyRequest(t, chatBody, wsB, idemKey))

	if stub.calls != 2 {
		t.Errorf("provider calls = %d, want 2 (workspace isolation)", stub.calls)
	}
}

func TestChatHandler_IdempotencyKey_ProviderError_MarksFailed(t *testing.T) {
	cache := startTestRedis(t)
	stub := &stubRouter{err: domain.ErrNoAvailable}
	handler := chatHandler(stub, cache)

	wsID := uuid.New().String()
	idemKey := "fail-" + uuid.New().String()

	// First request: provider error.
	w1 := httptest.NewRecorder()
	handler.ServeHTTP(w1, newIdempotencyRequest(t, chatBody, wsID, idemKey))
	if w1.Code != http.StatusServiceUnavailable {
		t.Fatalf("first request: status = %d, want 503", w1.Code)
	}
	if stub.calls != 1 {
		t.Fatalf("provider calls = %d, want 1", stub.calls)
	}

	// Retry after error: cache was cleared, provider called again.
	stub.err = nil
	stub.res = succeededResult()
	w2 := httptest.NewRecorder()
	handler.ServeHTTP(w2, newIdempotencyRequest(t, chatBody, wsID, idemKey))
	if w2.Code != http.StatusOK {
		t.Fatalf("retry: status = %d (body %s)", w2.Code, w2.Body.String())
	}
	if stub.calls != 2 {
		t.Errorf("provider calls after retry = %d, want 2 (MarkFailed cleared cache)", stub.calls)
	}
}

func TestChatHandler_RedisDown_FailOpen(t *testing.T) {
	// Use an invalid Redis URL — the client will fail on first command.
	// The handler must still work (fail-open).
	client := redis.NewClient(&redis.Options{Addr: "127.0.0.1:1"}) // port 1 = unreachable
	cache := idempotency.New(client, nil, nil)

	stub := &stubRouter{res: succeededResult()}
	handler := chatHandler(stub, cache)

	wsID := uuid.New().String()
	idemKey := "failopen-" + uuid.New().String()

	w := httptest.NewRecorder()
	handler.ServeHTTP(w, newIdempotencyRequest(t, chatBody, wsID, idemKey))

	if w.Code != http.StatusOK {
		t.Fatalf("fail-open: status = %d (body %s), want 200", w.Code, w.Body.String())
	}
	if stub.calls != 1 {
		t.Errorf("provider calls = %d, want 1 (cache skipped, provider called)", stub.calls)
	}
	_ = client.Close()
}
