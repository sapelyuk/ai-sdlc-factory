package mcpserver

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/mark3labs/mcp-go/client"
	mcpgo "github.com/mark3labs/mcp-go/mcp"
	"github.com/sapelyuk/ai-sdlc-factory/apps/tool-gateway/internal/audit"
	"github.com/sapelyuk/ai-sdlc-factory/apps/tool-gateway/internal/catalog"
	"github.com/sapelyuk/ai-sdlc-factory/apps/tool-gateway/internal/credentials"
	"github.com/sapelyuk/ai-sdlc-factory/apps/tool-gateway/internal/identity"
	"github.com/sapelyuk/ai-sdlc-factory/apps/tool-gateway/internal/policy"
)

const (
	schemasDir = "../../../../packages/contracts/mcp-schemas"
	policyDir  = "../../../../packages/policy"
)

type fakeBroker struct {
	data  map[string]string
	err   error
	calls int
}

func (f *fakeBroker) Fetch(context.Context, string) (map[string]string, error) {
	f.calls++
	if f.err != nil {
		return nil, f.err
	}
	return f.data, nil
}

type fakeExecutor struct {
	result map[string]any
	err    error
	call   Call
}

func (f *fakeExecutor) Execute(_ context.Context, call Call) (map[string]any, error) {
	f.call = call
	if f.err != nil {
		return nil, f.err
	}
	return f.result, nil
}

func newTestServer(t *testing.T, cat *catalog.Catalog, broker credentials.Broker, exec Executor) (*Server, *audit.MemoryStore) {
	t.Helper()
	engine, err := policy.NewEmbedded(policyDir)
	if err != nil {
		t.Fatal(err)
	}
	store := &audit.MemoryStore{}
	srv, err := NewServer(Options{
		Catalog:    cat,
		Authorizer: engine,
		Auditor:    audit.NewLogger(store),
		Broker:     broker,
		Executor:   exec,
	})
	if err != nil {
		t.Fatal(err)
	}
	return srv, store
}

func testIdentity(role, autonomy string) identity.Identity {
	return identity.Identity{
		WorkspaceID:   uuid.MustParse("11111111-1111-1111-1111-111111111111"),
		AgentID:       uuid.MustParse("22222222-2222-2222-2222-222222222222"),
		AuthorID:      uuid.MustParse("33333333-3333-3333-3333-333333333333"),
		Role:          role,
		AutonomyLevel: autonomy,
	}
}

func callTool(t *testing.T, srv *Server, ctx context.Context, name string, args map[string]any) (*mcpgo.CallToolResult, error) {
	t.Helper()
	cl, err := client.NewInProcessClient(srv.MCP())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = cl.Close() }()
	if err := cl.Start(ctx); err != nil {
		t.Fatal(err)
	}
	initReq := mcpgo.InitializeRequest{}
	initReq.Params.ProtocolVersion = mcpgo.LATEST_PROTOCOL_VERSION
	initReq.Params.ClientInfo = mcpgo.Implementation{Name: "test", Version: "1.0.0"}
	if _, err := cl.Initialize(ctx, initReq); err != nil {
		t.Fatal(err)
	}

	req := mcpgo.CallToolRequest{}
	req.Params.Name = name
	req.Params.Arguments = args
	return cl.CallTool(ctx, req)
}

func resultText(t *testing.T, result *mcpgo.CallToolResult) string {
	t.Helper()
	if len(result.Content) == 0 {
		t.Fatal("result has no content")
	}
	text, ok := result.Content[0].(mcpgo.TextContent)
	if !ok {
		t.Fatalf("unexpected content type %T", result.Content[0])
	}
	return text.Text
}

func TestAllowedCallInjectsCredentialAndAudits(t *testing.T) {
	cat, err := catalog.Load(schemasDir)
	if err != nil {
		t.Fatal(err)
	}
	broker := &fakeBroker{data: map[string]string{"token": "ghp_supersecrettoken"}}
	srv, store := newTestServer(t, cat, broker, StubExecutor{})

	ctx := identity.With(context.Background(), testIdentity("developer", "R1"))
	result, err := callTool(t, srv, ctx, "git.push", map[string]any{"repo": "ai-sdlc-factory"})
	if err != nil {
		t.Fatalf("CallTool: %v", err)
	}
	if result.IsError {
		t.Fatalf("unexpected tool error: %s", resultText(t, result))
	}

	text := resultText(t, result)
	if strings.Contains(text, "ghp_") {
		t.Fatalf("token leaked to the agent: %s", text)
	}
	if !strings.Contains(text, "credentials_injected") {
		t.Fatalf("expected credential metadata, got %s", text)
	}
	if broker.calls != 1 {
		t.Fatalf("broker calls = %d, want 1", broker.calls)
	}

	records := store.Records()
	if len(records) != 1 {
		t.Fatalf("audit records = %d, want 1", len(records))
	}
	if records[0].Result != audit.ResultSuccess || records[0].RiskLevel != "R1" {
		t.Fatalf("unexpected audit record: %+v", records[0])
	}
	if err := audit.Verify(records); err != nil {
		t.Fatalf("Verify: %v", err)
	}
}

func TestDeniedCallIsAudited(t *testing.T) {
	// A malicious viewer schema grants git.push. The static role matrix
	// must still deny it.
	cat := tempCatalog(t, map[string]string{
		"viewer.json": `{"role":"viewer","schema_version":"ai-sdlc-factory/v1","tools":[
			{"name":"git.push","description":"d","input_schema":{"type":"object"},"risk_level":"R1"}]}`,
	})
	srv, store := newTestServer(t, cat, &fakeBroker{}, StubExecutor{})

	ctx := identity.With(context.Background(), testIdentity("viewer", "R3"))
	result, err := callTool(t, srv, ctx, "git.push", nil)
	if err != nil {
		t.Fatalf("CallTool: %v", err)
	}
	if !result.IsError {
		t.Fatal("expected a tool error")
	}
	if !strings.Contains(resultText(t, result), "policy denied") {
		t.Fatalf("unexpected text: %s", resultText(t, result))
	}

	records := store.Records()
	if len(records) != 1 || records[0].Result != audit.ResultDenied {
		t.Fatalf("unexpected audit records: %+v", records)
	}
}

func TestR3RequiresApprover(t *testing.T) {
	cat := tempCatalog(t, map[string]string{
		"admin.json": `{"role":"admin","schema_version":"ai-sdlc-factory/v1","tools":[
			{"name":"deploy.prod","description":"d","input_schema":{"type":"object"},"risk_level":"R3"}]}`,
	})
	srv, store := newTestServer(t, cat, nil, StubExecutor{})

	ctx := identity.With(context.Background(), testIdentity("admin", "R3"))
	result, err := callTool(t, srv, ctx, "deploy.prod", nil)
	if err != nil {
		t.Fatal(err)
	}
	if !result.IsError {
		t.Fatal("R3 without an approver must be denied")
	}

	approved := testIdentity("admin", "R3")
	approved.ApproverID = uuid.MustParse("44444444-4444-4444-4444-444444444444")
	ctx = identity.With(context.Background(), approved)
	result, err = callTool(t, srv, ctx, "deploy.prod", nil)
	if err != nil {
		t.Fatal(err)
	}
	if result.IsError {
		t.Fatalf("approved R3 call failed: %s", resultText(t, result))
	}

	records := store.Records()
	if len(records) != 2 {
		t.Fatalf("audit records = %d, want 2", len(records))
	}
	if records[0].Result != audit.ResultDenied || records[1].Result != audit.ResultSuccess {
		t.Fatalf("unexpected results: %+v", records)
	}
}

func TestCredentialFailureIsAudited(t *testing.T) {
	cat, err := catalog.Load(schemasDir)
	if err != nil {
		t.Fatal(err)
	}
	broker := &fakeBroker{err: errors.New("vault is down")}
	srv, store := newTestServer(t, cat, broker, StubExecutor{})

	ctx := identity.With(context.Background(), testIdentity("developer", "R1"))
	result, err := callTool(t, srv, ctx, "git.push", nil)
	if err != nil {
		t.Fatal(err)
	}
	if !result.IsError {
		t.Fatal("expected a credential error")
	}

	records := store.Records()
	if len(records) != 1 || records[0].Result != audit.ResultError {
		t.Fatalf("unexpected audit records: %+v", records)
	}
}

func TestMissingCredentialBroker(t *testing.T) {
	cat, err := catalog.Load(schemasDir)
	if err != nil {
		t.Fatal(err)
	}
	srv, _ := newTestServer(t, cat, nil, StubExecutor{})

	ctx := identity.With(context.Background(), testIdentity("developer", "R1"))
	result, err := callTool(t, srv, ctx, "git.push", nil)
	if err != nil {
		t.Fatal(err)
	}
	if !result.IsError || !strings.Contains(resultText(t, result), "not configured") {
		t.Fatalf("unexpected result: %+v", result)
	}
}

func TestMissingIdentity(t *testing.T) {
	cat, err := catalog.Load(schemasDir)
	if err != nil {
		t.Fatal(err)
	}
	srv, store := newTestServer(t, cat, &fakeBroker{}, StubExecutor{})

	result, err := callTool(t, srv, context.Background(), "fs.read", map[string]any{"path": "x"})
	if err != nil {
		t.Fatal(err)
	}
	if !result.IsError || !strings.Contains(resultText(t, result), "identity") {
		t.Fatalf("unexpected result: %+v", result)
	}
	if len(store.Records()) != 0 {
		t.Fatal("a call without identity must not be audited")
	}
}

func TestExecutorOutputIsRedacted(t *testing.T) {
	cat, err := catalog.Load(schemasDir)
	if err != nil {
		t.Fatal(err)
	}
	exec := &fakeExecutor{result: map[string]any{
		"status": "success",
		"token":  "ghp_abcdefghijklmnopqrstuvwxyz0123456789",
	}}
	srv, _ := newTestServer(t, cat, &fakeBroker{}, exec)

	ctx := identity.With(context.Background(), testIdentity("developer", "R0"))
	result, err := callTool(t, srv, ctx, "fs.read", map[string]any{"path": "x"})
	if err != nil {
		t.Fatal(err)
	}
	text := resultText(t, result)
	if strings.Contains(text, "ghp_") {
		t.Fatalf("executor output leaked a token: %s", text)
	}
}

func TestExecutorErrorIsAudited(t *testing.T) {
	cat, err := catalog.Load(schemasDir)
	if err != nil {
		t.Fatal(err)
	}
	srv, store := newTestServer(t, cat, &fakeBroker{}, &fakeExecutor{err: errors.New("boom")})

	ctx := identity.With(context.Background(), testIdentity("developer", "R0"))
	result, err := callTool(t, srv, ctx, "fs.read", nil)
	if err != nil {
		t.Fatal(err)
	}
	if !result.IsError {
		t.Fatal("expected a tool error")
	}
	records := store.Records()
	if len(records) != 1 || records[0].Result != audit.ResultError {
		t.Fatalf("unexpected audit records: %+v", records)
	}
}

func TestAuditFailureFailsTheCall(t *testing.T) {
	cat, err := catalog.Load(schemasDir)
	if err != nil {
		t.Fatal(err)
	}
	engine, err := policy.NewEmbedded(policyDir)
	if err != nil {
		t.Fatal(err)
	}
	srv, err := NewServer(Options{
		Catalog:    cat,
		Authorizer: engine,
		Auditor:    audit.NewLogger(failingStore{}),
		Executor:   StubExecutor{},
	})
	if err != nil {
		t.Fatal(err)
	}

	ctx := identity.With(context.Background(), testIdentity("developer", "R0"))
	if _, err := callTool(t, srv, ctx, "fs.read", nil); err == nil {
		t.Fatal("expected the call to fail when the audit write fails")
	}
}

func TestListTools(t *testing.T) {
	cat, err := catalog.Load(schemasDir)
	if err != nil {
		t.Fatal(err)
	}
	srv, _ := newTestServer(t, cat, &fakeBroker{}, StubExecutor{})

	cl, err := client.NewInProcessClient(srv.MCP())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = cl.Close() }()
	if err := cl.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	initReq := mcpgo.InitializeRequest{}
	initReq.Params.ProtocolVersion = mcpgo.LATEST_PROTOCOL_VERSION
	initReq.Params.ClientInfo = mcpgo.Implementation{Name: "test", Version: "1.0.0"}
	if _, err := cl.Initialize(context.Background(), initReq); err != nil {
		t.Fatal(err)
	}

	res, err := cl.ListTools(context.Background(), mcpgo.ListToolsRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Tools) != cat.Len() {
		t.Fatalf("tools = %d, want %d", len(res.Tools), cat.Len())
	}
	for _, tool := range res.Tools {
		if tool.Name == "git.push" && tool.Description == "" {
			t.Fatal("git.push has no description")
		}
	}
}

func TestNewServerValidation(t *testing.T) {
	cat, err := catalog.Load(schemasDir)
	if err != nil {
		t.Fatal(err)
	}
	engine, err := policy.NewEmbedded(policyDir)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := NewServer(Options{}); err == nil {
		t.Fatal("expected an error for a missing catalog")
	}
	if _, err := NewServer(Options{Catalog: cat}); err == nil {
		t.Fatal("expected an error for a missing authorizer")
	}
	if _, err := NewServer(Options{Catalog: cat, Authorizer: engine}); err == nil {
		t.Fatal("expected an error for a missing auditor")
	}
}

type failingStore struct{}

func (failingStore) Append(context.Context, uuid.UUID, func(string) (audit.Record, error)) (audit.Record, error) {
	return audit.Record{}, errors.New("audit store is down")
}

func tempCatalog(t *testing.T, files map[string]string) *catalog.Catalog {
	t.Helper()
	dir := t.TempDir()
	for name, content := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	cat, err := catalog.Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	return cat
}
