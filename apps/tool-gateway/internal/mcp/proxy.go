package mcpserver

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"

	mcpclient "github.com/mark3labs/mcp-go/client"
	mcpgo "github.com/mark3labs/mcp-go/mcp"
)

// ErrNoUpstream is returned when a tool has no configured upstream MCP server.
var ErrNoUpstream = errors.New("no upstream MCP server configured for tool")

// DefaultUpstreamKey routes every tool that has no explicit route.
const DefaultUpstreamKey = "*"

// CredentialsMetaKey is the _meta field that carries brokered credentials to
// the upstream server. The agent never sees this field: the gateway adds it
// after authorization and strips secrets from the response.
const CredentialsMetaKey = "ai-sdlc-factory/credentials"

// upstreamClient is the subset of mcp-go client behaviour the proxy needs.
// It is an interface so tests can substitute an in-memory server.
type upstreamClient interface {
	CallTool(ctx context.Context, request mcpgo.CallToolRequest) (*mcpgo.CallToolResult, error)
	Close() error
}

// Dialer opens a client connection to an upstream MCP server.
type Dialer func(ctx context.Context, url string) (upstreamClient, error)

// ProxyExecutor forwards authorized calls to upstream MCP servers over
// Streamable HTTP using the mcp-go client (ADR-022).
type ProxyExecutor struct {
	routes  map[string]string
	dial    Dialer
	mu      sync.Mutex
	clients map[string]upstreamClient
}

// NewProxyExecutor builds a proxy from tool-to-URL routes. The "*" key, when
// present, is the fallback route for tools without an explicit entry.
func NewProxyExecutor(routes map[string]string, dial Dialer) (*ProxyExecutor, error) {
	if len(routes) == 0 {
		return nil, errors.New("at least one upstream route is required")
	}
	if dial == nil {
		dial = DialHTTPUpstream
	}
	clean := make(map[string]string, len(routes))
	for tool, url := range routes {
		tool = strings.TrimSpace(tool)
		url = strings.TrimSpace(url)
		if tool == "" || url == "" {
			return nil, fmt.Errorf("invalid upstream route %q=%q", tool, url)
		}
		clean[tool] = url
	}
	return &ProxyExecutor{routes: clean, dial: dial, clients: make(map[string]upstreamClient)}, nil
}

// ParseUpstreams parses MCP_UPSTREAMS. Accepted format is a comma-separated
// list of tool=url pairs, for example:
//
//	git.clone=http://git-mcp:9000/mcp,git.push=http://git-mcp:9000/mcp,*=http://sandbox:9000/mcp
//
// An empty value yields no routes.
func ParseUpstreams(raw string) (map[string]string, error) {
	routes := make(map[string]string)
	for _, pair := range strings.Split(raw, ",") {
		pair = strings.TrimSpace(pair)
		if pair == "" {
			continue
		}
		tool, url, ok := strings.Cut(pair, "=")
		if !ok {
			return nil, fmt.Errorf("invalid MCP_UPSTREAMS entry %q: expected tool=url", pair)
		}
		tool = strings.TrimSpace(tool)
		url = strings.TrimSpace(url)
		if tool == "" || url == "" {
			return nil, fmt.Errorf("invalid MCP_UPSTREAMS entry %q: tool and url are required", pair)
		}
		routes[tool] = url
	}
	return routes, nil
}

// Routes returns the configured routes sorted by tool name.
func (p *ProxyExecutor) Routes() []string {
	out := make([]string, 0, len(p.routes))
	for tool := range p.routes {
		out = append(out, tool)
	}
	sort.Strings(out)
	return out
}

// Execute implements Executor.
func (p *ProxyExecutor) Execute(ctx context.Context, call Call) (map[string]any, error) {
	url, ok := p.routes[call.Tool.Name]
	if !ok {
		url, ok = p.routes[DefaultUpstreamKey]
	}
	if !ok {
		return nil, fmt.Errorf("%w: %s", ErrNoUpstream, call.Tool.Name)
	}

	client, err := p.client(ctx, url)
	if err != nil {
		return nil, err
	}

	request := mcpgo.CallToolRequest{}
	request.Params.Name = call.Tool.Name
	request.Params.Arguments = call.Arguments
	if len(call.Credentials) > 0 {
		request.Params.Meta = &mcpgo.Meta{
			AdditionalFields: map[string]any{CredentialsMetaKey: call.Credentials},
		}
	}

	result, err := client.CallTool(ctx, request)
	if err != nil {
		return nil, fmt.Errorf("upstream %s: %w", url, err)
	}
	return resultMap(result)
}

// Close releases every upstream connection.
func (p *ProxyExecutor) Close() error {
	p.mu.Lock()
	defer p.mu.Unlock()

	var errs []error
	for url, client := range p.clients {
		if err := client.Close(); err != nil {
			errs = append(errs, fmt.Errorf("close %s: %w", url, err))
		}
		delete(p.clients, url)
	}
	return errors.Join(errs...)
}

func (p *ProxyExecutor) client(ctx context.Context, url string) (upstreamClient, error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	if client, ok := p.clients[url]; ok {
		return client, nil
	}
	client, err := p.dial(ctx, url)
	if err != nil {
		return nil, fmt.Errorf("connect to upstream %s: %w", url, err)
	}
	p.clients[url] = client
	return client, nil
}

// DialHTTPUpstream connects to an upstream MCP server over Streamable HTTP
// and performs the MCP initialization handshake.
func DialHTTPUpstream(ctx context.Context, url string) (upstreamClient, error) {
	client, err := mcpclient.NewStreamableHttpClient(url)
	if err != nil {
		return nil, err
	}
	if err := client.Start(ctx); err != nil {
		_ = client.Close()
		return nil, err
	}

	request := mcpgo.InitializeRequest{}
	request.Params.ProtocolVersion = mcpgo.LATEST_PROTOCOL_VERSION
	request.Params.ClientInfo = mcpgo.Implementation{Name: ServerName, Version: ServerVersion}
	if _, err := client.Initialize(ctx, request); err != nil {
		_ = client.Close()
		return nil, err
	}
	return client, nil
}

// resultMap converts an MCP tool result into a JSON object so that the gateway
// can redact secrets before returning it to the agent.
func resultMap(result *mcpgo.CallToolResult) (map[string]any, error) {
	if result == nil {
		return nil, errors.New("upstream returned an empty result")
	}
	raw, err := json.Marshal(result)
	if err != nil {
		return nil, err
	}
	var out map[string]any
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, err
	}
	if result.IsError {
		return out, fmt.Errorf("upstream tool error: %s", upstreamErrorText(result))
	}
	return out, nil
}

func upstreamErrorText(result *mcpgo.CallToolResult) string {
	var parts []string
	for _, content := range result.Content {
		if text, ok := content.(mcpgo.TextContent); ok {
			parts = append(parts, text.Text)
		}
	}
	if len(parts) == 0 {
		return "unknown error"
	}
	return strings.Join(parts, "; ")
}
