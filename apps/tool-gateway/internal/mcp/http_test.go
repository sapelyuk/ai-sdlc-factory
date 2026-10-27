package mcpserver

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	mcpsdk "github.com/mark3labs/mcp-go/server"
	"github.com/sapelyuk/ai-sdlc-factory/apps/tool-gateway/internal/audit"
	"github.com/sapelyuk/ai-sdlc-factory/apps/tool-gateway/internal/catalog"
	"github.com/sapelyuk/ai-sdlc-factory/apps/tool-gateway/internal/credentials"
	"github.com/sapelyuk/ai-sdlc-factory/apps/tool-gateway/internal/identity"
	"github.com/sapelyuk/ai-sdlc-factory/apps/tool-gateway/internal/policy"
	httptransport "github.com/sapelyuk/ai-sdlc-factory/apps/tool-gateway/internal/transport/http"
)

func TestHTTPStreamableEndpoint(t *testing.T) {
	cat, err := catalog.Load(schemasDir)
	if err != nil {
		t.Fatal(err)
	}
	engine, err := policy.NewEmbedded(policyDir)
	if err != nil {
		t.Fatal(err)
	}
	store := &audit.MemoryStore{}
	gateway, err := NewServer(Options{
		Catalog:    cat,
		Authorizer: engine,
		Auditor:    audit.NewLogger(store),
		Broker:     &fakeBroker{data: map[string]string{"token": "ghp_httpsecret0123456789"}},
		Redactor:   credentials.NewRedactor(),
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
		Auditor:    audit.NewLogger(store),
	})
	ts := httptest.NewServer(router.Handler())
	defer ts.Close()

	callBody := `{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"git.push","arguments":{"repo":"ai-sdlc-factory"}}}`

	// Initialize the MCP session.
	initBody := `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"http-test","version":"1.0.0"}}}`
	status, body := postMCP(t, ts.URL, initBody)
	if status != http.StatusOK {
		t.Fatalf("initialize status = %d: %s", status, body)
	}

	status, body = postMCP(t, ts.URL, callBody)
	if status != http.StatusOK {
		t.Fatalf("tools/call status = %d: %s", status, body)
	}
	if strings.Contains(body, "ghp_") {
		t.Fatalf("token leaked over HTTP: %s", body)
	}
	if !strings.Contains(body, "stub") {
		t.Fatalf("unexpected tool result: %s", body)
	}

	records := store.Records()
	if len(records) != 1 || records[0].Result != audit.ResultStub {
		t.Fatalf("unexpected audit records: %+v", records)
	}
}

func TestHTTPPolicyDenialBeforeMCP(t *testing.T) {
	cat, err := catalog.Load(schemasDir)
	if err != nil {
		t.Fatal(err)
	}
	engine, err := policy.NewEmbedded(policyDir)
	if err != nil {
		t.Fatal(err)
	}
	store := &audit.MemoryStore{}
	gateway, err := NewServer(Options{
		Catalog:    cat,
		Authorizer: engine,
		Auditor:    audit.NewLogger(store),
		Broker:     &fakeBroker{},
	})
	if err != nil {
		t.Fatal(err)
	}
	streamable := mcpsdk.NewStreamableHTTPServer(gateway.MCP(), mcpsdk.WithStateLess(true))
	router := httptransport.NewRouter(httptransport.Options{
		MCP:        streamable,
		Catalog:    cat,
		Authorizer: engine,
		Auditor:    audit.NewLogger(store),
	})
	ts := httptest.NewServer(router.Handler())
	defer ts.Close()

	status, body := postMCP(t, ts.URL,
		`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"deploy.prod","arguments":{}}}`)
	if status != http.StatusForbidden {
		t.Fatalf("status = %d: %s", status, body)
	}
	if !strings.Contains(body, "policy denied") {
		t.Fatalf("body = %s", body)
	}
}

func postMCP(t *testing.T, baseURL, payload string) (int, string) {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, baseURL+"/mcp", bytes.NewReader([]byte(payload)))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	req.Header.Set(identity.HeaderWorkspaceID, "11111111-1111-1111-1111-111111111111")
	req.Header.Set(identity.HeaderAgentID, "22222222-2222-2222-2222-222222222222")
	req.Header.Set(identity.HeaderAgentRole, "developer")
	req.Header.Set(identity.HeaderAutonomyLevel, "R1")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	raw, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(raw)
}
