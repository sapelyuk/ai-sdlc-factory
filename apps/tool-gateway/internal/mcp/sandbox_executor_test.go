package mcpserver

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"
	"github.com/sapelyuk/ai-sdlc-factory/apps/tool-gateway/internal/catalog"
	"github.com/sapelyuk/ai-sdlc-factory/apps/tool-gateway/internal/identity"
)

func TestSandboxExecutorForwardsRequestAndTenantHeaders(t *testing.T) {
	taskRunID := uuid.New()
	workspaceID := uuid.New()
	authorID := uuid.New()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/v1/sandboxes/exec" {
			t.Errorf("request = %s %s", r.Method, r.URL.Path)
		}
		if r.Header.Get("X-Workspace-ID") != workspaceID.String() {
			t.Errorf("workspace header = %q", r.Header.Get("X-Workspace-ID"))
		}
		if r.Header.Get("X-Author-ID") != authorID.String() {
			t.Errorf("author header = %q", r.Header.Get("X-Author-ID"))
		}
		var body sandboxExecRequest
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Errorf("decode request: %v", err)
		}
		if body.TaskRunID != taskRunID.String() || body.TimeoutSeconds != 45 || body.StdoutLimitBytes != 128 {
			t.Errorf("request body = %+v", body)
		}
		if len(body.Command) != 2 || body.Command[0] != "go" || body.Command[1] != "test" {
			t.Errorf("command = %v", body.Command)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"status":"success","stdout":"ok","stderr":"","exit_code":0}`))
	}))
	defer server.Close()

	executor, err := NewSandboxExecutor(server.URL, nil)
	if err != nil {
		t.Fatal(err)
	}
	ctx := identity.With(context.Background(), identity.Identity{WorkspaceID: workspaceID, AuthorID: authorID})
	result, err := executor.Execute(ctx, Call{
		Tool: catalog.Tool{Name: "sandbox.exec"},
		Arguments: map[string]any{
			"task_run_id":        taskRunID.String(),
			"command":            []any{"go", "test"},
			"timeout_seconds":    45,
			"stdout_limit_bytes": 128,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if result["stdout"] != "ok" || result["status"] != "success" {
		t.Fatalf("result = %#v", result)
	}
}

func TestSandboxExecutorValidatesRequiredArguments(t *testing.T) {
	executor, err := NewSandboxExecutor("http://sandbox-manager:8060", nil)
	if err != nil {
		t.Fatal(err)
	}
	ctx := identity.With(context.Background(), identity.Identity{WorkspaceID: uuid.New(), AuthorID: uuid.New()})
	tests := []map[string]any{
		{"command": []any{"echo", "hello"}},
		{"task_run_id": "not-a-uuid", "command": []any{"echo"}},
		{"task_run_id": uuid.NewString(), "command": []any{}},
		{"task_run_id": uuid.NewString(), "command": []any{"echo"}, "timeout_seconds": 3601},
		{"task_run_id": uuid.NewString(), "command": []any{"echo"}, "stdout_limit_bytes": 0},
	}
	for _, args := range tests {
		if _, err := executor.Execute(ctx, Call{Tool: catalog.Tool{Name: "sandbox.exec"}, Arguments: args}); err == nil {
			t.Errorf("expected validation error for %#v", args)
		}
	}
}

func TestSandboxExecutorDelegatesOtherTools(t *testing.T) {
	fallback := &fakeExecutor{result: map[string]any{"status": "fallback"}}
	executor, err := NewSandboxExecutor("http://sandbox-manager:8060", fallback)
	if err != nil {
		t.Fatal(err)
	}
	result, err := executor.Execute(context.Background(), Call{Tool: catalog.Tool{Name: "git.diff"}})
	if err != nil {
		t.Fatal(err)
	}
	if result["status"] != "fallback" || fallback.call.Tool.Name != "git.diff" {
		t.Fatalf("fallback result/call = %#v / %#v", result, fallback.call)
	}
}

func TestSandboxExecutorRejectsBadEndpointAndHTTPError(t *testing.T) {
	if _, err := NewSandboxExecutor("file:///tmp/sandbox", nil); err == nil {
		t.Fatal("expected invalid endpoint error")
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, `{"error":"workspace mismatch"}`, http.StatusForbidden)
	}))
	defer server.Close()
	executor, err := NewSandboxExecutor(server.URL, nil)
	if err != nil {
		t.Fatal(err)
	}
	ctx := identity.With(context.Background(), identity.Identity{WorkspaceID: uuid.New(), AuthorID: uuid.New()})
	_, err = executor.Execute(ctx, Call{
		Tool: catalog.Tool{Name: "sandbox.exec"},
		Arguments: map[string]any{
			"task_run_id": uuid.NewString(),
			"command":     []any{"echo"},
		},
	})
	if err == nil {
		t.Fatal("expected HTTP error")
	}
}
