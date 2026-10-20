package httptransport

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/sapelyuk/ai-sdlc-factory/apps/api/internal/config"
	"github.com/sapelyuk/ai-sdlc-factory/apps/api/internal/iam"
	"github.com/sapelyuk/ai-sdlc-factory/apps/api/internal/registry/domain"
	registryhandler "github.com/sapelyuk/ai-sdlc-factory/apps/api/internal/registry/handler"
	registryrepo "github.com/sapelyuk/ai-sdlc-factory/apps/api/internal/registry/repository"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"
	"golang.org/x/crypto/bcrypt"
)

func dockerAvailable() bool {
	out, err := exec.Command("docker", "info").CombinedOutput()
	return err == nil && len(out) > 0
}

func setupTestDB(t *testing.T) (*pgxpool.Pool, func()) {
	t.Helper()
	if !dockerAvailable() {
		t.Skip("Docker not available, skipping integration test")
	}
	ctx := context.Background()

	pgContainer, err := postgres.Run(ctx,
		"postgres:18-alpine",
		postgres.WithDatabase("test_iam"),
		postgres.WithUsername("test"),
		postgres.WithPassword("test"),
		testcontainers.WithWaitStrategy(
			wait.ForLog("database system is ready to accept connections").
				WithOccurrence(2).
				WithStartupTimeout(30*time.Second),
		),
	)
	if err != nil {
		t.Fatalf("start postgres: %v", err)
	}

	dsn, err := pgContainer.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		t.Fatalf("get connection string: %v", err)
	}

	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("create pool: %v", err)
	}

	// Locate repository root from test source.
	_, sourceFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("could not locate test source")
	}
	repoRoot := filepath.Join(filepath.Dir(sourceFile), "..", "..", "..", "..", "..")
	migrationsDir := filepath.Join(repoRoot, "migrations", "postgres")

	// Apply migration 0001 (workspaces).
	sql0001, err := os.ReadFile(filepath.Join(migrationsDir, "0001_init_squads_and_agents.up.sql"))
	if err != nil {
		t.Fatalf("read migration 0001: %v", err)
	}
	if _, err := pool.Exec(ctx, string(sql0001)); err != nil {
		t.Fatalf("apply migration 0001: %v", err)
	}

	// Apply migration 0009 (users).
	sql0009, err := os.ReadFile(filepath.Join(migrationsDir, "0009_init_users.up.sql"))
	if err != nil {
		t.Fatalf("read migration 0009: %v", err)
	}
	if _, err := pool.Exec(ctx, string(sql0009)); err != nil {
		t.Fatalf("apply migration 0009: %v", err)
	}

	cleanup := func() {
		pool.Close()
		_ = pgContainer.Terminate(ctx)
	}

	return pool, cleanup
}

func TestIntegration_Login(t *testing.T) {
	pool, cleanup := setupTestDB(t)
	defer cleanup()

	ctx := context.Background()
	wsID := uuid.New().String()

	// Disable RLS for test setup by setting the workspace context.
	if _, err := pool.Exec(ctx, `SELECT set_config('app.current_workspace_id', $1, false)`, wsID); err != nil {
		t.Fatalf("set workspace context: %v", err)
	}

	// Insert a test user with bcrypt hash.
	hash, _ := bcrypt.GenerateFromPassword([]byte("testpassword"), bcrypt.DefaultCost)
	_, err := pool.Exec(ctx,
		`INSERT INTO users (workspace_id, email, password_hash, role) VALUES ($1, $2, $3, $4)`,
		wsID, "test@example.com", string(hash), "admin")
	if err != nil {
		t.Fatalf("insert test user: %v", err)
	}

	jwtService := iam.NewJWTService("test-secret", 24*time.Hour)
	userRepo := iam.NewPostgresUserRepository(pool)
	loginHandler := NewLoginHandler(userRepo, jwtService)

	// Successful login.
	body := iam.LoginRequest{Email: "test@example.com", Password: "testpassword"}
	jsonBody, _ := json.Marshal(body)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", bytes.NewReader(jsonBody))
	req.Header.Set("Content-Type", "application/json")
	resp := httptest.NewRecorder()
	loginHandler.Login(resp, req)

	if resp.Code != http.StatusOK {
		t.Fatalf("login status = %d, body = %s", resp.Code, resp.Body.String())
	}

	var loginResp iam.LoginResponse
	if err := json.Unmarshal(resp.Body.Bytes(), &loginResp); err != nil {
		t.Fatalf("unmarshal login response: %v", err)
	}
	if loginResp.Token == "" {
		t.Fatal("login response token is empty")
	}
	if loginResp.User.Email != "test@example.com" {
		t.Errorf("user email = %q, want %q", loginResp.User.Email, "test@example.com")
	}
	if loginResp.User.Role != "admin" {
		t.Errorf("user role = %q, want %q", loginResp.User.Role, "admin")
	}
}

func TestIntegration_LoginWrongPassword(t *testing.T) {
	pool, cleanup := setupTestDB(t)
	defer cleanup()

	ctx := context.Background()
	wsID := uuid.New().String()

	if _, err := pool.Exec(ctx, `SELECT set_config('app.current_workspace_id', $1, false)`, wsID); err != nil {
		t.Fatalf("set workspace context: %v", err)
	}

	hash, _ := bcrypt.GenerateFromPassword([]byte("correctpassword"), bcrypt.DefaultCost)
	_, err := pool.Exec(ctx,
		`INSERT INTO users (workspace_id, email, password_hash, role) VALUES ($1, $2, $3, $4)`,
		wsID, "wrong@example.com", string(hash), "viewer")
	if err != nil {
		t.Fatalf("insert test user: %v", err)
	}

	jwtService := iam.NewJWTService("test-secret", 24*time.Hour)
	userRepo := iam.NewPostgresUserRepository(pool)
	loginHandler := NewLoginHandler(userRepo, jwtService)

	body := iam.LoginRequest{Email: "wrong@example.com", Password: "wrongpassword"}
	jsonBody, _ := json.Marshal(body)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", bytes.NewReader(jsonBody))
	req.Header.Set("Content-Type", "application/json")
	resp := httptest.NewRecorder()
	loginHandler.Login(resp, req)

	if resp.Code != http.StatusUnauthorized {
		t.Fatalf("wrong password status = %d, want %d", resp.Code, http.StatusUnauthorized)
	}
}

func TestIntegration_RBACWithJWT(t *testing.T) {
	pool, cleanup := setupTestDB(t)
	defer cleanup()

	ctx := context.Background()
	wsID := uuid.New().String()

	// Create registry tables.
	_, sourceFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("could not locate test source")
	}
	repoRoot := filepath.Join(filepath.Dir(sourceFile), "..", "..", "..", "..", "..")
	migrationsDir := filepath.Join(repoRoot, "migrations", "postgres")
	sql0008, _ := os.ReadFile(filepath.Join(migrationsDir, "0008_init_registry.up.sql"))
	if _, err := pool.Exec(ctx, string(sql0008)); err != nil {
		t.Fatalf("apply migration 0008: %v", err)
	}

	// Create a workspace.
	if _, err := pool.Exec(ctx, `SELECT set_config('app.current_workspace_id', $1, false)`, wsID); err != nil {
		t.Fatalf("set workspace context: %v", err)
	}
	_, err := pool.Exec(ctx,
		`INSERT INTO workspaces (id, name, owner_id) VALUES ($1, 'test-ws', $2)`,
		wsID, uuid.New().String())
	if err != nil {
		t.Fatalf("create workspace: %v", err)
	}

	// Create test users.
	viewerID := uuid.New().String()
	adminID := uuid.New().String()
	hash, _ := bcrypt.GenerateFromPassword([]byte("viewerpass"), bcrypt.DefaultCost)
	_, err = pool.Exec(ctx,
		`INSERT INTO users (id, workspace_id, email, password_hash, role) VALUES ($1, $2, $3, $4, $5)`,
		viewerID, wsID, "viewer@test.com", string(hash), "viewer")
	if err != nil {
		t.Fatalf("create viewer user: %v", err)
	}

	hash2, _ := bcrypt.GenerateFromPassword([]byte("adminpass"), bcrypt.DefaultCost)
	_, err = pool.Exec(ctx,
		`INSERT INTO users (id, workspace_id, email, password_hash, role) VALUES ($1, $2, $3, $4, $5)`,
		adminID, wsID, "admin@test.com", string(hash2), "admin")
	if err != nil {
		t.Fatalf("create admin user: %v", err)
	}

	// Create registry handler and router.
	jwtService := iam.NewJWTService("test-secret", 24*time.Hour)
	userRepo := iam.NewPostgresUserRepository(pool)
	loginHandler := NewLoginHandler(userRepo, jwtService)

	roleRepo := registryrepo.NewPostgresRoleRepository(pool)
	teamRepo := registryrepo.NewPostgresTeamRepository(pool)
	processRepo := registryrepo.NewPostgresProcessRepository(pool)
	registryHandler, err := registryhandler.NewRegistryHandler(roleRepo, teamRepo, processRepo)
	if err != nil {
		t.Fatalf("create registry handler: %v", err)
	}

	cfg := &config.Config{AllowLegacyAuth: false}
	router := NewRouter(registryHandler, nil, cfg, jwtService, loginHandler)

	// Login as viewer.
	viewerToken := loginAsViewer(t, router)

	// GET /roles as viewer → 200.
	req := httptest.NewRequest(http.MethodGet, "/api/v1/registry/roles", nil)
	req.Header.Set("Authorization", "Bearer "+viewerToken)
	req.Header.Set("X-Workspace-ID", wsID)
	resp := httptest.NewRecorder()
	router.Handler().ServeHTTP(resp, req)
	if resp.Code != http.StatusOK {
		t.Fatalf("viewer GET roles status = %d, want %d", resp.Code, http.StatusOK)
	}

	// POST /roles as viewer → 403.
	roleBody := `{"apiVersion":"ai-sdlc-factory/v1","kind":"Role","metadata":{"name":"test-role","category":"engineering"},"spec":{"description":"Test.","system_prompt_ref":"packages/ontology/prompts/test.yaml","inputs":[{"name":"req","type":"document"}],"outputs":[{"name":"res","type":"document"}],"tools":{"allow":["fs.read"]},"model":{"capability":"code-strong"},"limits":{"max_steps":10,"max_cost_usd":1},"autonomy":"balanced"}}`
	req2 := httptest.NewRequest(http.MethodPost, "/api/v1/registry/roles", bytes.NewReader([]byte(roleBody)))
	req2.Header.Set("Authorization", "Bearer "+viewerToken)
	req2.Header.Set("Content-Type", "application/json")
	req2.Header.Set("X-Workspace-ID", wsID)
	req2.Header.Set("X-Author-ID", viewerID)
	resp2 := httptest.NewRecorder()
	router.Handler().ServeHTTP(resp2, req2)
	if resp2.Code != http.StatusForbidden {
		t.Fatalf("viewer POST roles status = %d, want %d", resp2.Code, http.StatusForbidden)
	}

	// Login as admin and POST → 201.
	adminToken := loginAsAdmin(t, router)
	req3 := httptest.NewRequest(http.MethodPost, "/api/v1/registry/roles", bytes.NewReader([]byte(roleBody)))
	req3.Header.Set("Authorization", "Bearer "+adminToken)
	req3.Header.Set("Content-Type", "application/json")
	req3.Header.Set("X-Workspace-ID", wsID)
	req3.Header.Set("X-Author-ID", adminID)
	resp3 := httptest.NewRecorder()
	router.Handler().ServeHTTP(resp3, req3)
	if resp3.Code != http.StatusCreated {
		t.Fatalf("admin POST roles status = %d, body = %s, want %d", resp3.Code, resp3.Body.String(), http.StatusCreated)
	}
}

func loginAsViewer(t *testing.T, router *Router) string {
	t.Helper()
	body := iam.LoginRequest{Email: "viewer@test.com", Password: "viewerpass"}
	jsonBody, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", bytes.NewReader(jsonBody))
	req.Header.Set("Content-Type", "application/json")
	resp := httptest.NewRecorder()
	router.Handler().ServeHTTP(resp, req)
	if resp.Code != http.StatusOK {
		t.Fatalf("viewer login status = %d, body = %s", resp.Code, resp.Body.String())
	}
	var lr iam.LoginResponse
	json.Unmarshal(resp.Body.Bytes(), &lr)
	return lr.Token
}

func loginAsAdmin(t *testing.T, router *Router) string {
	t.Helper()
	body := iam.LoginRequest{Email: "admin@test.com", Password: "adminpass"}
	jsonBody, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", bytes.NewReader(jsonBody))
	req.Header.Set("Content-Type", "application/json")
	resp := httptest.NewRecorder()
	router.Handler().ServeHTTP(resp, req)
	if resp.Code != http.StatusOK {
		t.Fatalf("admin login status = %d, body = %s", resp.Code, resp.Body.String())
	}
	var lr iam.LoginResponse
	json.Unmarshal(resp.Body.Bytes(), &lr)
	return lr.Token
}

func TestIntegration_MetricsEndpoint(t *testing.T) {
	pool, cleanup := setupTestDB(t)
	defer cleanup()

	jwtService := iam.NewJWTService("test-secret", 24*time.Hour)
	userRepo := iam.NewPostgresUserRepository(pool)
	loginHandler := NewLoginHandler(userRepo, jwtService)
	cfg := &config.Config{AllowLegacyAuth: false}

	router := NewRouter(nil, nil, cfg, jwtService, loginHandler)

	// Make a request to generate metrics.
	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	resp := httptest.NewRecorder()
	router.Handler().ServeHTTP(resp, req)

	// Now check /metrics.
	metricsReq := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	metricsResp := httptest.NewRecorder()
	router.Handler().ServeHTTP(metricsResp, metricsReq)

	if metricsResp.Code != http.StatusOK {
		t.Fatalf("metrics status = %d, want %d", metricsResp.Code, http.StatusOK)
	}
	body := metricsResp.Body.String()
	if !bytes.Contains([]byte(body), []byte("http_requests_total")) {
		t.Error("metrics body does not contain http_requests_total")
	}
}

func TestIntegration_HealthWithoutToken(t *testing.T) {
	jwtService := iam.NewJWTService("test-secret", 24*time.Hour)
	loginHandler := NewLoginHandler(nil, jwtService)
	cfg := &config.Config{AllowLegacyAuth: false}

	router := NewRouter(nil, nil, cfg, jwtService, loginHandler)

	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	resp := httptest.NewRecorder()
	router.Handler().ServeHTTP(resp, req)
	if resp.Code != http.StatusOK {
		t.Fatalf("healthz status = %d, want %d", resp.Code, http.StatusOK)
	}
}

func TestIntegration_RedactSensitiveFields(t *testing.T) {
	input := `{"email":"test@example.com","password":"secret123","token":"abc.def.ghi"}`
	result := RedactSensitiveFields(input)

	if !bytes.Contains([]byte(result), []byte(`"password":"[REDACTED]"`)) {
		t.Errorf("password not redacted: %s", result)
	}
	if !bytes.Contains([]byte(result), []byte(`"token":"[REDACTED]"`)) {
		t.Errorf("token not redacted: %s", result)
	}
	// email should not be redacted.
	if !bytes.Contains([]byte(result), []byte(`"email":"test@example.com"`)) {
		t.Errorf("email was redacted: %s", result)
	}
}

// TestIntegration_ApproverFromJWT checks P9 rule (approver ≠ author) with JWT.
func TestIntegration_ApproverFromJWT(t *testing.T) {
	pool, cleanup := setupTestDB(t)
	defer cleanup()

	ctx := context.Background()
	wsID := uuid.New().String()

	// Create registry tables.
	_, sourceFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("could not locate test source")
	}
	repoRoot := filepath.Join(filepath.Dir(sourceFile), "..", "..", "..", "..", "..")
	migrationsDir := filepath.Join(repoRoot, "migrations", "postgres")
	sql0008, _ := os.ReadFile(filepath.Join(migrationsDir, "0008_init_registry.up.sql"))
	pool.Exec(ctx, string(sql0008))

	// Create workspace.
	pool.Exec(ctx, `SELECT set_config('app.current_workspace_id', $1, false)`, wsID)
	pool.Exec(ctx,
		`INSERT INTO workspaces (id, name, owner_id) VALUES ($1, 'test-ws', $2)`,
		wsID, uuid.New().String())

	// Create admin user.
	adminID := uuid.New().String()
	hash, _ := bcrypt.GenerateFromPassword([]byte("adminpass"), bcrypt.DefaultCost)
	pool.Exec(ctx,
		`INSERT INTO users (id, workspace_id, email, password_hash, role) VALUES ($1, $2, $3, $4, $5)`,
		adminID, wsID, "admin@test.com", string(hash), "admin")

	// Create a role owned by another user.
	otherAuthor := uuid.New().String()
	role := domain.NewRole(uuid.MustParse(wsID), uuid.MustParse(otherAuthor), "test-role", domain.CategoryEngineering, domain.RoleSpec{
		Description:     "Test role.",
		SystemPromptRef: "packages/ontology/prompts/test.yaml",
		Inputs:          []domain.ArtifactSpec{{Name: "req", Type: "document"}},
		Outputs:         []domain.ArtifactSpec{{Name: "res", Type: "document"}},
		Tools:           domain.ToolsSpec{Allow: []string{"fs.read"}},
		Model:           domain.ModelSpec{Capability: "code-strong"},
		Limits:          domain.LimitsSpec{MaxSteps: 10, MaxCostUSD: 1},
		Autonomy:        domain.AutonomyBalanced,
	})
	pool.Exec(ctx, `SELECT set_config('app.current_workspace_id', $1, false)`, wsID)
	pool.Exec(ctx,
		`INSERT INTO roles (id, workspace_id, name, version, category, spec, status, author_id)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)`,
		role.ID, wsID, role.Name, "1.0.0", "engineering",
		fmt.Sprintf(`{"description":"Test role.","system_prompt_ref":"packages/ontology/prompts/test.yaml","inputs":[{"name":"req","type":"document"}],"outputs":[{"name":"res","type":"document"}],"tools":{"allow":["fs.read"]},"model":{"capability":"code-strong"},"limits":{"max_steps":10,"max_cost_usd":1},"autonomy":"balanced"}`),
		"draft", otherAuthor)

	jwtService := iam.NewJWTService("test-secret", 24*time.Hour)
	userRepo := iam.NewPostgresUserRepository(pool)
	loginHandler := NewLoginHandler(userRepo, jwtService)

	roleRepo := registryrepo.NewPostgresRoleRepository(pool)
	teamRepo := registryrepo.NewPostgresTeamRepository(pool)
	processRepo := registryrepo.NewPostgresProcessRepository(pool)
	registryHandler, err := registryhandler.NewRegistryHandler(roleRepo, teamRepo, processRepo)
	if err != nil {
		t.Fatalf("create registry handler: %v", err)
	}

	cfg := &config.Config{AllowLegacyAuth: false}
	router := NewRouter(registryHandler, nil, cfg, jwtService, loginHandler)

	// Login as admin (different from author).
	adminToken := loginAsAdmin(t, router)

	// PATCH /status as admin → should succeed (approver ≠ author).
	statusBody := `{"status":"approved"}`
	req := httptest.NewRequest(http.MethodPatch, "/api/v1/registry/roles/"+role.ID.String()+"/status", bytes.NewReader([]byte(statusBody)))
	req.Header.Set("Authorization", "Bearer "+adminToken)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Workspace-ID", wsID)
	resp := httptest.NewRecorder()
	router.Handler().ServeHTTP(resp, req)

	if resp.Code != http.StatusOK {
		t.Fatalf("admin PATCH status as non-author = %d, want %d; body = %s", resp.Code, http.StatusOK, resp.Body.String())
	}
}
