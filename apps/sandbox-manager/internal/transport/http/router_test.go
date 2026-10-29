package http

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/docker/docker/api/types"
	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/network"
	"github.com/google/uuid"
	"github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/sapelyuk/ai-sdlc-factory/apps/sandbox-manager/internal/config"
	"github.com/sapelyuk/ai-sdlc-factory/apps/sandbox-manager/internal/sandbox"
	sdkaudit "github.com/sapelyuk/ai-sdlc-factory/packages/sdk-go/audit"
)

type routerDocker struct {
	createConfig *container.Config
	hostConfig   *container.HostConfig
	stdout       []byte
	stderr       []byte
	exitCode     int
	removed      bool
	pingErr      error
	createErr    error
	startErr     error
	inspectErr   error
	stopErr      error
	removeErr    error
}

func (d *routerDocker) Ping(context.Context) (types.Ping, error) { return types.Ping{}, d.pingErr }

func (d *routerDocker) ContainerCreate(_ context.Context, cfg *container.Config, host *container.HostConfig, _ *network.NetworkingConfig, _ *v1.Platform, _ string) (container.CreateResponse, error) {
	d.createConfig, d.hostConfig = cfg, host
	if d.createErr != nil {
		return container.CreateResponse{}, d.createErr
	}
	return container.CreateResponse{ID: "container-1"}, nil
}

func (d *routerDocker) ContainerStart(context.Context, string, container.StartOptions) error {
	return d.startErr
}

func (d *routerDocker) ContainerExecCreate(context.Context, string, container.ExecOptions) (container.ExecCreateResponse, error) {
	return container.ExecCreateResponse{ID: "exec-1"}, nil
}

func (d *routerDocker) ContainerExecAttach(context.Context, string, container.ExecAttachOptions) (types.HijackedResponse, error) {
	client, server := net.Pipe()
	go func() {
		_, _ = server.Write(muxOutput(1, d.stdout))
		_, _ = server.Write(muxOutput(2, d.stderr))
		_ = server.Close()
	}()
	return types.NewHijackedResponse(client, "application/vnd.docker.raw-stream"), nil
}

func (d *routerDocker) ContainerExecInspect(context.Context, string) (container.ExecInspect, error) {
	return container.ExecInspect{ExitCode: d.exitCode}, d.inspectErr
}

func (d *routerDocker) ContainerStop(context.Context, string, container.StopOptions) error {
	return d.stopErr
}

func (d *routerDocker) ContainerRemove(_ context.Context, _ string, options container.RemoveOptions) error {
	d.removed = options.RemoveVolumes
	return d.removeErr
}

func muxOutput(stream byte, output []byte) []byte {
	data := make([]byte, 8+len(output))
	data[0] = stream
	binary.BigEndian.PutUint32(data[4:8], uint32(len(output)))
	copy(data[8:], output)
	return data
}

type routerTasks struct {
	path     string
	err      error
	readyErr error
}

type routerAuditRecorder struct {
	events []sdkaudit.Event
}

func (a *routerAuditRecorder) Log(_ context.Context, event sdkaudit.Event) error {
	a.events = append(a.events, event)
	return nil
}

func (s routerTasks) WorkspacePath(context.Context, uuid.UUID, uuid.UUID) (string, error) {
	return s.path, s.err
}

func (s routerTasks) Ready(context.Context) error { return s.readyErr }

func newRouterTestHandler(tasks sandbox.TaskRunStore, docker *routerDocker) http.Handler {
	return newRouterTestHandlerWithAuditor(tasks, docker, nil)
}

func newRouterTestHandlerWithAuditor(tasks sandbox.TaskRunStore, docker *routerDocker, auditor sdkaudit.Auditor) http.Handler {
	manager := sandbox.NewManager(docker, &config.Config{
		Runtime:        "runsc",
		SandboxImage:   "ai-sdlc-factory/sandbox-go-node:latest",
		SandboxNetwork: "sandbox-net",
		DiskLimitBytes: 2 * 1024 * 1024 * 1024,
	}, slog.New(slog.NewTextHandler(io.Discard, nil)), auditor)
	return NewRouter(Options{
		Manager: manager,
		Tasks:   tasks,
		Logger:  slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
}

func makeRequest(handler http.Handler, body string) *httptest.ResponseRecorder {
	request := httptest.NewRequest(http.MethodPost, "/v1/sandboxes/exec", strings.NewReader(body))
	request.Header.Set("X-Workspace-ID", "11111111-1111-4111-8111-111111111111")
	request.Header.Set("X-Author-ID", "22222222-2222-4222-8222-222222222222")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	return response
}

func TestExecResolvesTenantWorkspaceAndDestroysContainer(t *testing.T) {
	docker := &routerDocker{stdout: []byte("hello")}
	auditor := &routerAuditRecorder{}
	workspacePath := filepath.Join(t.TempDir(), "task-1")
	handler := newRouterTestHandlerWithAuditor(routerTasks{path: workspacePath}, docker, auditor)
	taskRunID := uuid.NewString()
	response := makeRequest(handler, `{"task_run_id":"`+taskRunID+`","command":["echo","hello"]}`)
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", response.Code, response.Body)
	}
	var result execResponse
	if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if result.Status != "success" || result.Stdout != "hello" {
		t.Fatalf("result = %+v", result)
	}
	if docker.hostConfig.Mounts[0].Source != workspacePath || !docker.removed {
		t.Fatalf("workspace mount/cleanup = %+v / %v", docker.hostConfig.Mounts, docker.removed)
	}
	if len(auditor.events) != 2 {
		t.Fatalf("audit events = %d, want create and destroy", len(auditor.events))
	}
	workspaceID := uuid.MustParse("11111111-1111-4111-8111-111111111111")
	if auditor.events[0].WorkspaceID != workspaceID || auditor.events[1].WorkspaceID != workspaceID {
		t.Fatalf("audit tenant IDs = %s/%s, want %s", auditor.events[0].WorkspaceID, auditor.events[1].WorkspaceID, workspaceID)
	}
	if auditor.events[1].Details["reason"] != "exec_completed" {
		t.Fatalf("destroy event details = %+v", auditor.events[1].Details)
	}
}

func TestExecRejectsMissingTenantHeaders(t *testing.T) {
	handler := newRouterTestHandler(routerTasks{path: "/workspace"}, &routerDocker{})
	request := httptest.NewRequest(http.MethodPost, "/v1/sandboxes/exec", strings.NewReader(`{"task_run_id":"`+uuid.NewString()+`","command":["echo"]}`))
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusBadRequest || !strings.Contains(response.Body.String(), "X-Workspace-ID") {
		t.Fatalf("status/body = %d/%s", response.Code, response.Body)
	}
}

func TestExecValidatesRequestFields(t *testing.T) {
	handler := newRouterTestHandler(routerTasks{path: filepath.Join(t.TempDir(), "workspace")}, &routerDocker{})
	bodies := []string{
		`{`,
		`{"task_run_id":"` + uuid.NewString() + `","command":["echo"],"extra":true}`,
		`{"task_run_id":"` + uuid.NewString() + `","command":["echo"]} {}`,
		`{"task_run_id":"not-a-uuid","command":["echo"]}`,
		`{"task_run_id":"` + uuid.NewString() + `","command":[]}`,
		`{"task_run_id":"` + uuid.NewString() + `","command":[""]}`,
		"{\"task_run_id\":\"" + uuid.NewString() + "\",\"command\":[\"echo\\u0000bad\"]}",
		`{"task_run_id":"` + uuid.NewString() + `","command":["echo"],"timeout_seconds":3601}`,
		`{"task_run_id":"` + uuid.NewString() + `","command":["echo"],"stdout_limit_bytes":16777217}`,
	}
	for _, body := range bodies {
		if response := makeRequest(handler, body); response.Code != http.StatusBadRequest {
			t.Errorf("body %q returned status %d, want 400 (%s)", body, response.Code, response.Body)
		}
	}
}

func TestExecValidatesAuthorHeader(t *testing.T) {
	handler := newRouterTestHandler(routerTasks{path: "/workspace"}, &routerDocker{})
	request := httptest.NewRequest(http.MethodPost, "/v1/sandboxes/exec", strings.NewReader(`{"task_run_id":"`+uuid.NewString()+`","command":["echo"]}`))
	request.Header.Set("X-Workspace-ID", "11111111-1111-4111-8111-111111111111")
	request.Header.Set("X-Author-ID", "bad-id")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusBadRequest || !strings.Contains(response.Body.String(), "X-Author-ID") {
		t.Fatalf("status/body = %d/%s", response.Code, response.Body)
	}
}

func TestExecRejectsInvalidTaskRunAndTenantMismatch(t *testing.T) {
	handler := newRouterTestHandler(routerTasks{path: "/workspace"}, &routerDocker{})
	invalid := makeRequest(handler, `{"task_run_id":"not-a-uuid","command":["echo"]}`)
	if invalid.Code != http.StatusBadRequest {
		t.Fatalf("invalid task_run status = %d", invalid.Code)
	}
	mismatch := newRouterTestHandler(routerTasks{err: sandbox.ErrTaskRunNotFound}, &routerDocker{})
	response := makeRequest(mismatch, `{"task_run_id":"`+uuid.NewString()+`","command":["echo"]}`)
	if response.Code != http.StatusForbidden {
		t.Fatalf("tenant mismatch status = %d, body = %s", response.Code, response.Body)
	}
}

func TestExecRejectsEmptyWorkspaceAndCleansUpAfterExecFailure(t *testing.T) {
	noPath := newRouterTestHandler(routerTasks{path: ""}, &routerDocker{})
	response := makeRequest(noPath, `{"task_run_id":"`+uuid.NewString()+`","command":["echo"]}`)
	if response.Code != http.StatusBadRequest || !strings.Contains(response.Body.String(), "migrate-up") {
		t.Fatalf("empty workspace status/body = %d/%s", response.Code, response.Body)
	}

	failedDocker := &routerDocker{inspectErr: errors.New("exec inspect failed")}
	failedHandler := newRouterTestHandler(routerTasks{path: filepath.Join(t.TempDir(), "workspace")}, failedDocker)
	execFailure := makeRequest(failedHandler, `{"task_run_id":"`+uuid.NewString()+`","command":["echo"]}`)
	if execFailure.Code != http.StatusInternalServerError || !failedDocker.removed {
		t.Fatalf("exec failure status/cleanup = %d/%v; body=%s", execFailure.Code, failedDocker.removed, execFailure.Body)
	}
}

func TestExecReturnsDatabaseCreateAndCleanupErrors(t *testing.T) {
	body := `{"task_run_id":"` + uuid.NewString() + `","command":["echo"]}`
	storeFailure := newRouterTestHandler(routerTasks{err: errors.New("database unavailable")}, &routerDocker{})
	if response := makeRequest(storeFailure, body); response.Code != http.StatusInternalServerError {
		t.Fatalf("store error status = %d", response.Code)
	}

	createFailure := newRouterTestHandler(routerTasks{path: filepath.Join(t.TempDir(), "workspace")}, &routerDocker{createErr: errors.New("create failed")})
	if response := makeRequest(createFailure, body); response.Code != http.StatusInternalServerError {
		t.Fatalf("create error status = %d", response.Code)
	}

	removeFailure := &routerDocker{removeErr: errors.New("remove failed")}
	cleanupFailure := newRouterTestHandler(routerTasks{path: filepath.Join(t.TempDir(), "workspace")}, removeFailure)
	if response := makeRequest(cleanupFailure, body); response.Code != http.StatusInternalServerError {
		t.Fatalf("cleanup error status = %d", response.Code)
	}
}

func TestExecReportsNonzeroExitAndTruncatedOutput(t *testing.T) {
	docker := &routerDocker{
		stdout:   []byte("hello world"),
		exitCode: 9,
	}
	handler := newRouterTestHandler(routerTasks{path: filepath.Join(t.TempDir(), "workspace")}, docker)
	body := `{"task_run_id":"` + uuid.NewString() + `","command":["echo"],"stdout_limit_bytes":5}`
	response := makeRequest(handler, body)
	if response.Code != http.StatusOK {
		t.Fatalf("status/body = %d/%s", response.Code, response.Body)
	}
	var result execResponse
	if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if result.Status != "error" || result.ExitCode != 9 || result.Stdout != "hello" || !result.StdoutTruncated {
		t.Fatalf("result = %+v", result)
	}
}

func TestReadinessAndServerConfiguration(t *testing.T) {
	ready := newRouterTestHandler(routerTasks{}, &routerDocker{})
	for _, path := range []string{"/healthz", "/readyz"} {
		request := httptest.NewRequest(http.MethodGet, path, nil)
		response := httptest.NewRecorder()
		ready.ServeHTTP(response, request)
		if response.Code != http.StatusOK {
			t.Errorf("%s status = %d", path, response.Code)
		}
	}

	databaseDown := newRouterTestHandler(routerTasks{readyErr: errors.New("database down")}, &routerDocker{})
	response := httptest.NewRecorder()
	databaseDown.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/readyz", nil))
	if response.Code != http.StatusServiceUnavailable {
		t.Fatalf("database readiness status = %d", response.Code)
	}

	dockerDown := newRouterTestHandler(routerTasks{}, &routerDocker{pingErr: errors.New("proxy down")})
	response = httptest.NewRecorder()
	dockerDown.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/readyz", nil))
	if response.Code != http.StatusServiceUnavailable {
		t.Fatalf("Docker readiness status = %d", response.Code)
	}

	server := NewServer(8060, ready)
	if server.Addr != ":8060" || server.Handler != ready {
		t.Fatalf("server = %+v", server)
	}
}
