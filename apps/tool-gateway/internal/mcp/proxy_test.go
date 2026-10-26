package mcpserver

import (
	"context"
	"encoding/json"
	"errors"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	mcpgo "github.com/mark3labs/mcp-go/mcp"
	mcpsdk "github.com/mark3labs/mcp-go/server"
	"github.com/sapelyuk/ai-sdlc-factory/apps/tool-gateway/internal/catalog"
)

func mustJSON(t *testing.T, v any) []byte {
	t.Helper()
	raw, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

type fakeUpstream struct {
	result  *mcpgo.CallToolResult
	err     error
	request mcpgo.CallToolRequest
	closed  bool
}

func (f *fakeUpstream) CallTool(_ context.Context, request mcpgo.CallToolRequest) (*mcpgo.CallToolResult, error) {
	f.request = request
	return f.result, f.err
}

func (f *fakeUpstream) Close() error {
	f.closed = true
	return nil
}

func textResult(text string) *mcpgo.CallToolResult {
	return &mcpgo.CallToolResult{Content: []mcpgo.Content{mcpgo.NewTextContent(text)}}
}

func TestParseUpstreams(t *testing.T) {
	routes, err := ParseUpstreams(" git.push=http://git-mcp:9000/mcp , *=http://sandbox:9000/mcp,")
	if err != nil {
		t.Fatal(err)
	}
	if len(routes) != 2 {
		t.Fatalf("routes = %v", routes)
	}
	if routes["git.push"] != "http://git-mcp:9000/mcp" {
		t.Fatalf("git.push route = %q", routes["git.push"])
	}
	if routes[DefaultUpstreamKey] != "http://sandbox:9000/mcp" {
		t.Fatalf("default route = %q", routes[DefaultUpstreamKey])
	}

	if routes, err := ParseUpstreams(""); err != nil || len(routes) != 0 {
		t.Fatalf("empty input: routes=%v err=%v", routes, err)
	}
	for _, bad := range []string{"git.push", "=http://x", "git.push="} {
		if _, err := ParseUpstreams(bad); err == nil {
			t.Fatalf("expected error for %q", bad)
		}
	}
}

func TestNewProxyExecutorValidation(t *testing.T) {
	if _, err := NewProxyExecutor(nil, nil); err == nil {
		t.Fatal("expected error for empty routes")
	}
	if _, err := NewProxyExecutor(map[string]string{" ": "http://x"}, nil); err == nil {
		t.Fatal("expected error for blank tool name")
	}
	if _, err := NewProxyExecutor(map[string]string{"git.push": " "}, nil); err == nil {
		t.Fatal("expected error for blank url")
	}
}

func TestProxyExecutorRoutesAndExecute(t *testing.T) {
	upstream := &fakeUpstream{result: textResult("pushed")}
	exec, err := NewProxyExecutor(map[string]string{"git.push": "http://git-mcp/mcp"}, func(context.Context, string) (upstreamClient, error) {
		return upstream, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := exec.Routes(); len(got) != 1 || got[0] != "git.push" {
		t.Fatalf("routes = %v", got)
	}

	tool := catalog.Tool{Name: "git.push"}
	out, err := exec.Execute(context.Background(), Call{
		Tool:        tool,
		Arguments:   map[string]any{"repo": "ai-sdlc-factory"},
		Credentials: map[string]string{"GITHUB_TOKEN": "ghp_secret"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if out["content"] == nil {
		t.Fatalf("result = %v", out)
	}
	if upstream.request.Params.Name != "git.push" {
		t.Fatalf("upstream tool = %q", upstream.request.Params.Name)
	}
	args, ok := upstream.request.Params.Arguments.(map[string]any)
	if !ok || args["repo"] != "ai-sdlc-factory" {
		t.Fatalf("upstream arguments = %v", upstream.request.Params.Arguments)
	}
	if upstream.request.Params.Meta == nil {
		t.Fatal("credentials were not forwarded to the upstream server")
	}
	if _, ok := upstream.request.Params.Meta.AdditionalFields[CredentialsMetaKey]; !ok {
		t.Fatalf("meta = %v", upstream.request.Params.Meta.AdditionalFields)
	}

	// The client is cached per URL, so a second call reuses the connection.
	if _, err := exec.Execute(context.Background(), Call{Tool: tool}); err != nil {
		t.Fatal(err)
	}
	if err := exec.Close(); err != nil {
		t.Fatal(err)
	}
	if !upstream.closed {
		t.Fatal("upstream client was not closed")
	}
}

func TestProxyExecutorDefaultRoute(t *testing.T) {
	upstream := &fakeUpstream{result: textResult("ok")}
	exec, err := NewProxyExecutor(map[string]string{DefaultUpstreamKey: "http://sandbox/mcp"}, func(context.Context, string) (upstreamClient, error) {
		return upstream, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := exec.Execute(context.Background(), Call{Tool: catalog.Tool{Name: "fs.read"}}); err != nil {
		t.Fatal(err)
	}
	if upstream.request.Params.Name != "fs.read" {
		t.Fatalf("upstream tool = %q", upstream.request.Params.Name)
	}
}

func TestProxyExecutorErrors(t *testing.T) {
	upstream := &fakeUpstream{err: errors.New("boom")}
	exec, err := NewProxyExecutor(map[string]string{"git.push": "http://git-mcp/mcp"}, func(context.Context, string) (upstreamClient, error) {
		return upstream, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := exec.Execute(context.Background(), Call{Tool: catalog.Tool{Name: "git.clone"}}); !errors.Is(err, ErrNoUpstream) {
		t.Fatalf("err = %v", err)
	}
	if _, err := exec.Execute(context.Background(), Call{Tool: catalog.Tool{Name: "git.push"}}); err == nil || !strings.Contains(err.Error(), "boom") {
		t.Fatalf("err = %v", err)
	}

	erroring := &fakeUpstream{result: &mcpgo.CallToolResult{
		Content: []mcpgo.Content{mcpgo.NewTextContent("remote rejected")},
		IsError: true,
	}}
	exec, err = NewProxyExecutor(map[string]string{"git.push": "http://git-mcp/mcp"}, func(context.Context, string) (upstreamClient, error) {
		return erroring, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	out, err := exec.Execute(context.Background(), Call{Tool: catalog.Tool{Name: "git.push"}})
	if err == nil || !strings.Contains(err.Error(), "remote rejected") {
		t.Fatalf("err = %v", err)
	}
	if out["isError"] != true {
		t.Fatalf("result = %v", out)
	}

	dialErr := errors.New("dial failed")
	exec, err = NewProxyExecutor(map[string]string{"git.push": "http://git-mcp/mcp"}, func(context.Context, string) (upstreamClient, error) {
		return nil, dialErr
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := exec.Execute(context.Background(), Call{Tool: catalog.Tool{Name: "git.push"}}); !errors.Is(err, dialErr) {
		t.Fatalf("err = %v", err)
	}
}

func TestResultMapEmpty(t *testing.T) {
	if _, err := resultMap(nil); err == nil {
		t.Fatal("expected error for nil result")
	}
	if _, err := resultMap(&mcpgo.CallToolResult{IsError: true}); err == nil {
		t.Fatal("expected error for error result without content")
	}
}

// TestProxyExecutorAgainstUpstreamServer is the integration test required by
// issue #10: the gateway proxies a call to a real MCP server over Streamable
// HTTP and returns its result.
func TestProxyExecutorAgainstUpstreamServer(t *testing.T) {
	var mu sync.Mutex
	var received map[string]any

	upstream := mcpsdk.NewMCPServer("test-upstream", "1.0.0", mcpsdk.WithToolCapabilities(false))
	upstream.AddTool(
		mcpgo.NewTool("git.push", mcpgo.WithDescription("push a branch")),
		func(_ context.Context, request mcpgo.CallToolRequest) (*mcpgo.CallToolResult, error) {
			mu.Lock()
			received = request.GetArguments()
			mu.Unlock()
			return mcpgo.NewToolResultText("pushed"), nil
		},
	)

	server := mcpsdk.NewStreamableHTTPServer(upstream, mcpsdk.WithStateLess(true))
	ts := httptest.NewServer(server)
	defer ts.Close()

	exec, err := NewProxyExecutor(map[string]string{"git.push": ts.URL}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = exec.Close() }()

	out, err := exec.Execute(context.Background(), Call{
		Tool:      catalog.Tool{Name: "git.push"},
		Arguments: map[string]any{"branch": "feat/10-tool-gateway"},
	})
	if err != nil {
		t.Fatal(err)
	}
	content, ok := out["content"].([]any)
	if !ok || len(content) == 0 {
		t.Fatalf("result = %v", out)
	}
	if !strings.Contains(string(mustJSON(t, content)), "pushed") {
		t.Fatalf("result = %v", out)
	}

	mu.Lock()
	defer mu.Unlock()
	if received["branch"] != "feat/10-tool-gateway" {
		t.Fatalf("upstream arguments = %v", received)
	}
}
