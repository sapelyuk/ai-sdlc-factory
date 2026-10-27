package mcpserver

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/sapelyuk/ai-sdlc-factory/apps/tool-gateway/internal/identity"
)

const (
	defaultSandboxTimeoutSeconds = 300
	defaultSandboxOutputLimit    = 1024 * 1024
	maxSandboxOutputLimit        = 16 * 1024 * 1024
)

// SandboxExecutor sends sandbox.exec calls to Sandbox Manager. Other tools
// continue to use the configured MCP upstream or the local stub executor.
type SandboxExecutor struct {
	endpoint string
	client   *http.Client
	fallback Executor
}

// NewSandboxExecutor creates an executor for the Sandbox Manager API.
func NewSandboxExecutor(endpoint string, fallback Executor) (*SandboxExecutor, error) {
	parsed, err := url.Parse(strings.TrimSpace(endpoint))
	if err != nil || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") {
		return nil, fmt.Errorf("invalid Sandbox Manager URL %q", endpoint)
	}
	if fallback == nil {
		fallback = StubExecutor{}
	}
	return &SandboxExecutor{
		endpoint: strings.TrimRight(parsed.String(), "/"),
		client:   &http.Client{},
		fallback: fallback,
	}, nil
}

// Execute implements Executor.
func (e *SandboxExecutor) Execute(ctx context.Context, call Call) (map[string]any, error) {
	if call.Tool.Name != "sandbox.exec" {
		return e.fallback.Execute(ctx, call)
	}
	id, err := identity.From(ctx)
	if err != nil {
		return nil, fmt.Errorf("sandbox.exec requires caller identity: %w", err)
	}
	request, timeout, err := parseSandboxRequest(call.Arguments)
	if err != nil {
		return nil, err
	}
	body, err := json.Marshal(request)
	if err != nil {
		return nil, fmt.Errorf("encode sandbox request: %w", err)
	}
	requestCtx, cancel := context.WithTimeout(ctx, time.Duration(timeout+30)*time.Second)
	defer cancel()
	httpRequest, err := http.NewRequestWithContext(requestCtx, http.MethodPost, e.endpoint+"/v1/sandboxes/exec", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("build Sandbox Manager request: %w", err)
	}
	httpRequest.Header.Set("Content-Type", "application/json")
	httpRequest.Header.Set("X-Workspace-ID", id.WorkspaceID.String())
	httpRequest.Header.Set("X-Author-ID", id.AuthorID.String())

	response, err := e.client.Do(httpRequest)
	if err != nil {
		return nil, fmt.Errorf("call Sandbox Manager: %w", err)
	}
	responseBody, err := io.ReadAll(io.LimitReader(response.Body, int64(maxSandboxOutputLimit*4+64*1024)))
	closeErr := response.Body.Close()
	if err != nil {
		return nil, fmt.Errorf("read Sandbox Manager response: %w", err)
	}
	if closeErr != nil {
		return nil, fmt.Errorf("close Sandbox Manager response: %w", closeErr)
	}
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		return nil, fmt.Errorf("sandbox manager returned HTTP %d: %s", response.StatusCode, strings.TrimSpace(string(responseBody)))
	}
	var result map[string]any
	if err := json.Unmarshal(responseBody, &result); err != nil {
		return nil, fmt.Errorf("decode Sandbox Manager response: %w", err)
	}
	return result, nil
}

// Close closes any upstream connections owned by the fallback executor.
func (e *SandboxExecutor) Close() error {
	if closer, ok := e.fallback.(interface{ Close() error }); ok {
		return closer.Close()
	}
	return nil
}

type sandboxExecRequest struct {
	TaskRunID        string   `json:"task_run_id"`
	Command          []string `json:"command"`
	TimeoutSeconds   int      `json:"timeout_seconds"`
	StdoutLimitBytes int      `json:"stdout_limit_bytes"`
}

func parseSandboxRequest(arguments map[string]any) (sandboxExecRequest, int, error) {
	taskRunID, ok := arguments["task_run_id"].(string)
	if !ok {
		return sandboxExecRequest{}, 0, errors.New("sandbox.exec requires task_run_id")
	}
	id, err := uuid.Parse(taskRunID)
	if err != nil || id == uuid.Nil {
		return sandboxExecRequest{}, 0, errors.New("sandbox.exec task_run_id must be a valid UUID")
	}
	rawCommand, ok := arguments["command"].([]any)
	if !ok {
		if stringsCommand, stringsOK := arguments["command"].([]string); stringsOK {
			rawCommand = make([]any, len(stringsCommand))
			for i, arg := range stringsCommand {
				rawCommand[i] = arg
			}
		} else {
			return sandboxExecRequest{}, 0, errors.New("sandbox.exec command must be a non-empty array of strings")
		}
	}
	if len(rawCommand) == 0 {
		return sandboxExecRequest{}, 0, errors.New("sandbox.exec command must be a non-empty array of strings")
	}
	command := make([]string, len(rawCommand))
	for i, value := range rawCommand {
		arg, ok := value.(string)
		if !ok || (i == 0 && strings.TrimSpace(arg) == "") {
			return sandboxExecRequest{}, 0, errors.New("sandbox.exec command must be a non-empty array of strings")
		}
		command[i] = arg
	}

	timeoutSeconds := defaultSandboxTimeoutSeconds
	if value, present := arguments["timeout_seconds"]; present {
		timeoutSeconds, err = integer(value)
		if err != nil || timeoutSeconds < 1 || timeoutSeconds > 3600 {
			return sandboxExecRequest{}, 0, errors.New("sandbox.exec timeout_seconds must be between 1 and 3600")
		}
	}
	outputLimit := defaultSandboxOutputLimit
	if value, present := arguments["stdout_limit_bytes"]; present {
		outputLimit, err = integer(value)
		if err != nil || outputLimit < 1 || outputLimit > maxSandboxOutputLimit {
			return sandboxExecRequest{}, 0, fmt.Errorf("sandbox.exec stdout_limit_bytes must be between 1 and %d", maxSandboxOutputLimit)
		}
	}
	return sandboxExecRequest{
		TaskRunID:        id.String(),
		Command:          command,
		TimeoutSeconds:   timeoutSeconds,
		StdoutLimitBytes: outputLimit,
	}, timeoutSeconds, nil
}

func integer(value any) (int, error) {
	switch n := value.(type) {
	case int:
		return n, nil
	case int32:
		return int(n), nil
	case int64:
		return int(n), nil
	case float64:
		if n != float64(int(n)) {
			return 0, errors.New("not an integer")
		}
		return int(n), nil
	case json.Number:
		parsed, err := strconv.Atoi(n.String())
		return parsed, err
	default:
		return 0, errors.New("not an integer")
	}
}
