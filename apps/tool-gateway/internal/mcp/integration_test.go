package mcpserver_test

import (
	"context"
	"encoding/json"
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
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	mcpclient "github.com/mark3labs/mcp-go/client"
	"github.com/mark3labs/mcp-go/client/transport"
	mcpgo "github.com/mark3labs/mcp-go/mcp"
	mcpsdk "github.com/mark3labs/mcp-go/server"
	"github.com/sapelyuk/ai-sdlc-factory/apps/tool-gateway/internal/audit"
	"github.com/sapelyuk/ai-sdlc-factory/apps/tool-gateway/internal/catalog"
	"github.com/sapelyuk/ai-sdlc-factory/apps/tool-gateway/internal/credentials"
	"github.com/sapelyuk/ai-sdlc-factory/apps/tool-gateway/internal/identity"
	mcpserver "github.com/sapelyuk/ai-sdlc-factory/apps/tool-gateway/internal/mcp"
	"github.com/sapelyuk/ai-sdlc-factory/apps/tool-gateway/internal/policy"
	httptransport "github.com/sapelyuk/ai-sdlc-factory/apps/tool-gateway/internal/transport/http"
	postgrescontainer "github.com/testcontainers/testcontainers-go/modules/postgres"
)

const (
	schemasDir              = "../../../../packages/contracts/mcp-schemas"
	policyDir               = "../../../../packages/policy"
	migrationPath           = "../../../../migrations/postgres/0011_tool_gateway_audit.up.sql"
	stubResultMigrationPath = "../../../../migrations/postgres/0012_audit_stub_result.up.sql"
)

var (
	workspaceID = uuid.MustParse("11111111-1111-1111-1111-111111111111")
	agentID     = uuid.MustParse("22222222-2222-2222-2222-222222222222")
	authorID    = uuid.MustParse("33333333-3333-3333-3333-333333333333")
)

const secretToken = "ghp_integrationsecret0123456789"

type staticBroker struct{}

func (staticBroker) Fetch(context.Context, string) (map[string]string, error) {
	return map[string]string{"token": secretToken}, nil
}

type testEnv struct {
	url    string
	pool   *pgxpool.Pool
	server *httptest.Server
}

func newTestEnv(t *testing.T) *testEnv {
	t.Helper()

	if output, err := exec.CommandContext(t.Context(), "docker", "info").CombinedOutput(); err != nil {
		if ci := os.Getenv("CI"); ci != "" && ci != "false" {
			t.Fatalf("Docker is required for integration tests in CI: %s", strings.TrimSpace(string(output)))
		}
		t.Skipf("Docker is unavailable; skipping integration test: %s", strings.TrimSpace(string(output)))
	}

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	t.Cleanup(cancel)

	container, err := postgrescontainer.Run(ctx, "pgvector/pgvector:pg18",
		postgrescontainer.WithDatabase("tool_gateway"),
		postgrescontainer.WithUsername("tool_gateway"),
		postgrescontainer.WithPassword("tool_gateway"),
		postgrescontainer.BasicWaitStrategies(),
	)
	if err != nil {
		t.Fatalf("start PostgreSQL: %v", err)
	}
	t.Cleanup(func() {
		if err := container.Terminate(context.Background()); err != nil {
			t.Errorf("terminate PostgreSQL: %v", err)
		}
	})

	dsn, err := container.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		t.Fatal(err)
	}
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)

	migration, err := os.ReadFile(filepath.Clean(migrationPath))
	if err != nil {
		t.Fatalf("read migration: %v", err)
	}
	conn, err := pool.Acquire(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := conn.Conn().PgConn().Exec(ctx, string(migration)).ReadAll(); err != nil {
		conn.Release()
		t.Fatalf("apply migration: %v", err)
	}
	conn.Release()

	// Apply migration 0012 for 'stub' result support.
	stubMigration, err := os.ReadFile(filepath.Clean(stubResultMigrationPath))
	if err != nil {
		t.Fatalf("read stub migration: %v", err)
	}
	conn, err = pool.Acquire(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := conn.Conn().PgConn().Exec(ctx, string(stubMigration)).ReadAll(); err != nil {
		conn.Release()
		t.Fatalf("apply stub migration: %v", err)
	}
	conn.Release()

	// The container owner bypasses RLS. The gateway connects through a
	// dedicated NOBYPASSRLS role, so the workspace policy is exercised.
	const appRole = "tool_gateway_app"
	if _, err := pool.Exec(ctx, "CREATE ROLE "+appRole+" NOLOGIN NOBYPASSRLS"); err != nil {
		t.Fatalf("create app role: %v", err)
	}
	if _, err := pool.Exec(ctx, "GRANT SELECT, INSERT ON tool_call_audit_log TO "+appRole); err != nil {
		t.Fatalf("grant app role: %v", err)
	}

	poolCfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		t.Fatal(err)
	}
	poolCfg.AfterConnect = func(ctx context.Context, c *pgx.Conn) error {
		if _, err := c.Exec(ctx, "SET ROLE "+appRole); err != nil {
			return err
		}
		_, err := c.Exec(ctx, "SELECT set_config('app.current_workspace_id', $1, false)", workspaceID.String())
		return err
	}
	appPool, err := pgxpool.NewWithConfig(ctx, poolCfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(appPool.Close)

	cat, err := catalog.Load(schemasDir)
	if err != nil {
		t.Fatal(err)
	}
	engine, err := policy.NewEmbedded(policyDir)
	if err != nil {
		t.Fatal(err)
	}
	auditor := audit.NewLogger(audit.NewPostgresStore(appPool))
	redactor := credentials.NewRedactor()
	gateway, err := mcpserver.NewServer(mcpserver.Options{
		Catalog:    cat,
		Authorizer: engine,
		Auditor:    auditor,
		Broker:     staticBroker{},
		Redactor:   redactor,
	})
	if err != nil {
		t.Fatal(err)
	}

	streamable := mcpsdk.NewStreamableHTTPServer(
		gateway.MCP(),
		mcpsdk.WithEndpointPath("/mcp"),
		mcpsdk.WithStateLess(true),
		mcpsdk.WithHTTPContextFunc(func(ctx context.Context, r *http.Request) context.Context {
			id, err := identity.FromHeaders(r.Header)
			if err != nil {
				return ctx
			}
			return identity.With(ctx, id)
		}),
	)

	router := httptransport.NewRouter(httptransport.Options{
		MCP:        streamable,
		Catalog:    cat,
		Authorizer: engine,
		Auditor:    auditor,
		DB:         pool,
	})
	server := httptest.NewServer(router.Handler())
	t.Cleanup(server.Close)

	return &testEnv{url: server.URL, pool: pool, server: server}
}

func headers(role, autonomy string) map[string]string {
	return map[string]string{
		identity.HeaderWorkspaceID:   workspaceID.String(),
		identity.HeaderAgentID:       agentID.String(),
		identity.HeaderAuthorID:      authorID.String(),
		identity.HeaderAgentRole:     role,
		identity.HeaderAutonomyLevel: autonomy,
	}
}

func callWithClient(t *testing.T, env *testEnv, role, autonomy, tool string, args map[string]any) (*mcpgo.CallToolResult, error) {
	t.Helper()
	cl, err := mcpclient.NewStreamableHttpClient(env.url+"/mcp",
		transport.WithHTTPHeaders(headers(role, autonomy)))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = cl.Close() }()

	ctx := t.Context()
	if err := cl.Start(ctx); err != nil {
		t.Fatal(err)
	}
	initReq := mcpgo.InitializeRequest{}
	initReq.Params.ProtocolVersion = mcpgo.LATEST_PROTOCOL_VERSION
	initReq.Params.ClientInfo = mcpgo.Implementation{Name: "integration-test", Version: "1.0.0"}
	if _, err := cl.Initialize(ctx, initReq); err != nil {
		t.Fatalf("initialize: %v", err)
	}

	req := mcpgo.CallToolRequest{}
	req.Params.Name = tool
	req.Params.Arguments = args
	return cl.CallTool(ctx, req)
}

func TestToolGatewayEndToEnd(t *testing.T) {
	env := newTestEnv(t)
	ctx := t.Context()

	// Health and readiness.
	for _, path := range []string{"/healthz", "/readyz"} {
		resp, err := http.Get(env.url + path) // #nosec G107 -- test server URL.
		if err != nil {
			t.Fatal(err)
		}
		_ = resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("%s status = %d", path, resp.StatusCode)
		}
	}

	// developer -> git.push is allowed and the token never reaches the agent.
	result, err := callWithClient(t, env, "developer", "R1", "git.push", map[string]any{"repo": "ai-sdlc-factory"})
	if err != nil {
		t.Fatalf("git.push: %v", err)
	}
	if result.IsError {
		t.Fatalf("git.push returned a tool error: %v", result.Content)
	}
	body, _ := json.Marshal(result)
	if strings.Contains(string(body), secretToken) || strings.Contains(string(body), "ghp_") {
		t.Fatalf("token leaked in the response: %s", body)
	}

	// developer -> deploy.prod is denied before the MCP layer (403).
	denied := rawToolCall(t, env.url, "developer", "R3", "deploy.prod")
	if denied != http.StatusForbidden {
		t.Fatalf("deploy.prod status = %d, want 403", denied)
	}

	// admin -> git.push is allowed.
	result, err = callWithClient(t, env, "admin", "R1", "git.push", nil)
	if err != nil {
		t.Fatalf("admin git.push: %v", err)
	}
	if result.IsError {
		t.Fatalf("admin git.push returned a tool error: %v", result.Content)
	}

	// admin -> deploy.prod without an approver is denied (403).
	denied = rawToolCall(t, env.url, "admin", "R3", "deploy.prod")
	if denied != http.StatusForbidden {
		t.Fatalf("unapproved deploy.prod status = %d, want 403", denied)
	}

	// The audit log is persisted and the chain verifies.
	records := readAudit(t, env.pool, ctx)
	if len(records) < 4 {
		t.Fatalf("audit records = %d, want at least 4", len(records))
	}
	if err := audit.Verify(records); err != nil {
		t.Fatalf("verify hash chain: %v", err)
	}

	var stub, deniedCount int
	for _, rec := range records {
		switch rec.Result {
		case audit.ResultStub:
			stub++
		case audit.ResultDenied:
			deniedCount++
		}
	}
	if stub != 2 {
		t.Fatalf("stub records = %d, want 2", stub)
	}
	if deniedCount != 2 {
		t.Fatalf("denied records = %d, want 2", deniedCount)
	}
	for _, rec := range records {
		if len(rec.HashChain) != 64 {
			t.Fatalf("hash_chain length = %d, want 64", len(rec.HashChain))
		}
		if strings.Contains(rec.ErrorMessage, secretToken) {
			t.Fatal("token leaked into the audit log")
		}
	}
}

func rawToolCall(t *testing.T, baseURL, role, autonomy, tool string) int {
	t.Helper()
	payload := `{"jsonrpc":"2.0","id":99,"method":"tools/call","params":{"name":"` + tool + `","arguments":{}}}`
	req, err := http.NewRequest(http.MethodPost, baseURL+"/mcp", strings.NewReader(payload))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	for k, v := range headers(role, autonomy) {
		req.Header.Set(k, v)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	_, _ = io.Copy(io.Discard, resp.Body)
	return resp.StatusCode
}

func readAudit(t *testing.T, pool *pgxpool.Pool, ctx context.Context) []audit.Record {
	t.Helper()
	rows, err := pool.Query(ctx, `
		SELECT id, workspace_id, agent_id, tool_name, risk_level, result,
		       COALESCE(error_message, ''), hash_chain, created_at
		FROM tool_call_audit_log
		ORDER BY created_at, id`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()

	var out []audit.Record
	for rows.Next() {
		var rec audit.Record
		if err := rows.Scan(&rec.ID, &rec.WorkspaceID, &rec.AgentID, &rec.ToolName,
			&rec.RiskLevel, &rec.Result, &rec.ErrorMessage, &rec.HashChain, &rec.CreatedAt); err != nil {
			t.Fatal(err)
		}
		out = append(out, rec)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return out
}
