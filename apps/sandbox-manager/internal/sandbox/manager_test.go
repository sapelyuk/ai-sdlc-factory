package sandbox

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"net"
	"path/filepath"
	"testing"
	"time"

	"github.com/docker/docker/api/types"
	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/network"
	"github.com/google/uuid"
	"github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/sapelyuk/ai-sdlc-factory/apps/sandbox-manager/internal/config"
	sdkaudit "github.com/sapelyuk/ai-sdlc-factory/packages/sdk-go/audit"
)

type fakeDocker struct {
	createConfig  *container.Config
	hostConfig    *container.HostConfig
	startErr      error
	stopErr       error
	removeErr     error
	stdout        []byte
	stderr        []byte
	exitCode      int
	removed       bool
	stopped       bool
	pingErr       error
	createErr     error
	execCreateErr error
	attachErr     error
	inspectErr    error
}

type auditRecorder struct {
	events []sdkaudit.Event
	err    error
}

func (a *auditRecorder) Log(_ context.Context, event sdkaudit.Event) error {
	a.events = append(a.events, event)
	return a.err
}

func (d *fakeDocker) Ping(context.Context) (types.Ping, error) {
	return types.Ping{}, d.pingErr
}

func (d *fakeDocker) ContainerCreate(_ context.Context, cfg *container.Config, host *container.HostConfig, _ *network.NetworkingConfig, _ *v1.Platform, _ string) (container.CreateResponse, error) {
	d.createConfig = cfg
	d.hostConfig = host
	if d.createErr != nil {
		return container.CreateResponse{}, d.createErr
	}
	return container.CreateResponse{ID: "container-123"}, nil
}

func (d *fakeDocker) ContainerStart(context.Context, string, container.StartOptions) error {
	return d.startErr
}

func (d *fakeDocker) ContainerExecCreate(context.Context, string, container.ExecOptions) (container.ExecCreateResponse, error) {
	if d.execCreateErr != nil {
		return container.ExecCreateResponse{}, d.execCreateErr
	}
	return container.ExecCreateResponse{ID: "exec-123"}, nil
}

func (d *fakeDocker) ContainerExecAttach(context.Context, string, container.ExecAttachOptions) (types.HijackedResponse, error) {
	if d.attachErr != nil {
		return types.HijackedResponse{}, d.attachErr
	}
	client, server := net.Pipe()
	go func() {
		_, _ = server.Write(multiplexedOutput(1, d.stdout))
		_, _ = server.Write(multiplexedOutput(2, d.stderr))
		_ = server.Close()
	}()
	return types.NewHijackedResponse(client, "application/vnd.docker.raw-stream"), nil
}

func (d *fakeDocker) ContainerExecInspect(context.Context, string) (container.ExecInspect, error) {
	return container.ExecInspect{ExitCode: d.exitCode}, d.inspectErr
}

func (d *fakeDocker) ContainerStop(context.Context, string, container.StopOptions) error {
	d.stopped = true
	return d.stopErr
}

func (d *fakeDocker) ContainerRemove(_ context.Context, _ string, options container.RemoveOptions) error {
	d.removed = options.RemoveVolumes
	return d.removeErr
}

func multiplexedOutput(stream byte, content []byte) []byte {
	result := make([]byte, 8+len(content))
	result[0] = stream
	binary.BigEndian.PutUint32(result[4:8], uint32(len(content)))
	copy(result[8:], content)
	return result
}

func newTestManager(docker *fakeDocker, runtime string) *Manager {
	return newTestManagerWithAuditor(docker, runtime, nil)
}

func newTestManagerWithAuditor(docker *fakeDocker, runtime string, auditor sdkaudit.Auditor) *Manager {
	return NewManager(docker, &config.Config{
		Runtime:        runtime,
		SandboxImage:   "ai-sdlc-factory/sandbox-go-node:latest",
		SandboxNetwork: "sandbox-net",
		DiskLimitBytes: 2 * 1024 * 1024 * 1024,
	}, nil, auditor)
}

func TestCreateUsesRequiredIsolationAndLimits(t *testing.T) {
	docker := &fakeDocker{}
	manager := newTestManager(docker, "runsc")
	taskRunID := uuid.New()
	path := filepath.Join(t.TempDir(), taskRunID.String())

	id, err := manager.Create(context.Background(), uuid.New(), taskRunID, path)
	if err != nil {
		t.Fatal(err)
	}
	if id != "container-123" {
		t.Fatalf("container ID = %q", id)
	}
	cfg, host := docker.createConfig, docker.hostConfig
	if cfg.User != "1000:1000" {
		t.Fatalf("user = %q", cfg.User)
	}
	if host.Privileged || !host.ReadonlyRootfs {
		t.Fatalf("security flags: privileged=%v readonly=%v", host.Privileged, host.ReadonlyRootfs)
	}
	if host.Runtime != "runsc" || host.NetworkMode != "sandbox-net" {
		t.Fatalf("runtime/network = %q/%q", host.Runtime, host.NetworkMode)
	}
	if host.Memory != 512*1024*1024 || host.NanoCPUs != 500_000_000 {
		t.Fatalf("resource limits = memory=%d nanoCPUs=%d", host.Memory, host.NanoCPUs)
	}
	if host.PidsLimit == nil || *host.PidsLimit != 100 {
		t.Fatalf("pids limit = %v", host.PidsLimit)
	}
	if host.StorageOpt["size"] != "2048M" {
		t.Fatalf("storage limit = %q", host.StorageOpt["size"])
	}
	if cfg.Labels[labelService] != "task-run-"+taskRunID.String() {
		t.Fatalf("service label = %q", cfg.Labels[labelService])
	}
	if cfg.Env[0] != "HTTP_PROXY=http://egress-proxy:3128" || cfg.Env[1] != "HTTPS_PROXY=http://egress-proxy:3128" {
		t.Fatalf("proxy environment = %v", cfg.Env)
	}
	if cfg.Env[3] != "HOME=/workspace" {
		t.Fatalf("home environment = %q", cfg.Env[3])
	}
	if len(host.Mounts) != 1 || host.Mounts[0].Source != path || host.Mounts[0].Target != "/workspace" {
		t.Fatalf("mounts = %+v", host.Mounts)
	}
}

func TestCreateWritesTenantScopedAuditEvent(t *testing.T) {
	docker := &fakeDocker{}
	auditor := &auditRecorder{}
	manager := newTestManagerWithAuditor(docker, "runsc", auditor)
	workspaceID := uuid.New()
	path := filepath.Join(t.TempDir(), "workspace")

	containerID, err := manager.Create(context.Background(), workspaceID, uuid.New(), path)
	if err != nil {
		t.Fatal(err)
	}
	if containerID != "container-123" || len(auditor.events) != 1 {
		t.Fatalf("container/events = %q/%d", containerID, len(auditor.events))
	}
	ev := auditor.events[0]
	if ev.WorkspaceID != workspaceID || ev.Action != sdkaudit.ActionSandboxCreate ||
		ev.IdempotencyKey != "sandbox_create:container-123" || ev.ResourceID != containerID {
		t.Fatalf("audit event = %+v", ev)
	}
	if ev.Details["container_id"] != containerID ||
		ev.Details["image"] != "ai-sdlc-factory/sandbox-go-node:latest" ||
		ev.Details["workspace_path"] != path {
		t.Fatalf("audit details = %+v", ev.Details)
	}
	limits, ok := ev.Details["limits"].(map[string]any)
	if !ok || limits["disk_bytes"] != int64(2*1024*1024*1024) ||
		limits["memory_bytes"] != int64(512*1024*1024) || limits["nano_cpus"] != int64(500_000_000) {
		t.Fatalf("audit limits = %#v", ev.Details["limits"])
	}
}

func TestCreateRemovesSandboxWhenAuditFails(t *testing.T) {
	docker := &fakeDocker{}
	auditor := &auditRecorder{err: errors.New("audit unavailable")}
	manager := newTestManagerWithAuditor(docker, "runsc", auditor)
	if _, err := manager.Create(context.Background(), uuid.New(), uuid.New(), filepath.Join(t.TempDir(), "workspace")); err == nil {
		t.Fatal("expected audit failure")
	}
	if !docker.removed {
		t.Fatal("unaudited container should be removed")
	}
}

func TestCreateRejectsUnsafeWorkspacePath(t *testing.T) {
	manager := newTestManager(&fakeDocker{}, "runsc")
	if _, err := manager.Create(context.Background(), uuid.New(), uuid.New(), "relative/path"); err == nil {
		t.Fatal("expected relative workspace path to be rejected")
	}
}

func TestCreateRemovesContainerWhenStartFails(t *testing.T) {
	docker := &fakeDocker{startErr: errors.New("start failed")}
	manager := newTestManager(docker, "runc")
	_, err := manager.Create(context.Background(), uuid.New(), uuid.New(), filepath.Join(t.TempDir(), "workspace"))
	if err == nil {
		t.Fatal("expected start error")
	}
	if !docker.removed {
		t.Fatal("container should be removed after failed start")
	}
}

func TestReadyPropagatesDockerError(t *testing.T) {
	manager := newTestManager(&fakeDocker{pingErr: errors.New("proxy unavailable")}, "runsc")
	if err := manager.Ready(context.Background()); err == nil {
		t.Fatal("expected Docker ping error")
	}
}

func TestCreatePropagatesDockerCreateError(t *testing.T) {
	manager := newTestManager(&fakeDocker{createErr: errors.New("create failed")}, "runsc")
	if _, err := manager.Create(context.Background(), uuid.New(), uuid.New(), filepath.Join(t.TempDir(), "workspace")); err == nil {
		t.Fatal("expected container create error")
	}
}

func TestCreateReportsCleanupErrorAfterStartFailure(t *testing.T) {
	docker := &fakeDocker{startErr: errors.New("start failed"), removeErr: errors.New("remove failed")}
	manager := newTestManager(docker, "runc")
	_, err := manager.Create(context.Background(), uuid.New(), uuid.New(), filepath.Join(t.TempDir(), "workspace"))
	if err == nil || !bytes.Contains([]byte(err.Error()), []byte("remove failed")) {
		t.Fatalf("error = %v, want cleanup failure", err)
	}
}

func TestExecCapturesOutputAndExitCode(t *testing.T) {
	docker := &fakeDocker{stdout: []byte("hello"), stderr: []byte("warning"), exitCode: 7}
	manager := newTestManager(docker, "runsc")
	stdout, stderr, exitCode, stdoutTruncated, stderrTruncated, err := manager.Exec(
		context.Background(), "container-123", []string{"echo", "hello"}, time.Second, 64,
	)
	if err != nil {
		t.Fatal(err)
	}
	if stdout != "hello" || stderr != "warning" || exitCode != 7 {
		t.Fatalf("result = %q, %q, %d", stdout, stderr, exitCode)
	}
	if stdoutTruncated || stderrTruncated {
		t.Fatal("small output should not be truncated")
	}
}

func TestExecEnforcesOutputLimits(t *testing.T) {
	docker := &fakeDocker{stdout: bytes.Repeat([]byte("x"), 16), stderr: bytes.Repeat([]byte("y"), 16)}
	manager := newTestManager(docker, "runsc")
	stdout, stderr, _, stdoutTruncated, stderrTruncated, err := manager.Exec(
		context.Background(), "container-123", []string{"large-output"}, time.Second, 4,
	)
	if err != nil {
		t.Fatal(err)
	}
	if stdout != "xxxx" || stderr != "yyyy" || !stdoutTruncated || !stderrTruncated {
		t.Fatalf("limited output = %q/%q, truncated=%v/%v", stdout, stderr, stdoutTruncated, stderrTruncated)
	}
}

func TestExecPropagatesDockerFailures(t *testing.T) {
	tests := []struct {
		name   string
		docker *fakeDocker
	}{
		{name: "exec create", docker: &fakeDocker{execCreateErr: errors.New("exec create failed")}},
		{name: "exec attach", docker: &fakeDocker{attachErr: errors.New("attach failed")}},
		{name: "exec inspect", docker: &fakeDocker{inspectErr: errors.New("inspect failed")}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			manager := newTestManager(test.docker, "runsc")
			if _, _, _, _, _, err := manager.Exec(context.Background(), "container-123", []string{"echo"}, time.Second, 64); err == nil {
				t.Fatal("expected Docker error")
			}
		})
	}
}

func TestExecRejectsInvalidArguments(t *testing.T) {
	manager := newTestManager(&fakeDocker{}, "runsc")
	tests := []struct {
		containerID string
		command     []string
		timeout     time.Duration
		limit       int
	}{
		{"", []string{"echo"}, time.Second, 64},
		{"container", nil, time.Second, 64},
		{"container", []string{""}, time.Second, 64},
		{"container", []string{"echo"}, 0, 64},
		{"container", []string{"echo"}, time.Second, 0},
		{"container", []string{"echo"}, time.Second, maxExecOutput + 1},
	}
	for _, test := range tests {
		if _, _, _, _, _, err := manager.Exec(context.Background(), test.containerID, test.command, test.timeout, test.limit); err == nil {
			t.Errorf("Exec(%q, %v, %v, %d) should fail", test.containerID, test.command, test.timeout, test.limit)
		}
	}
}

func TestDestroyStopsAndRemovesVolumes(t *testing.T) {
	docker := &fakeDocker{}
	manager := newTestManager(docker, "runsc")
	if err := manager.Destroy(context.Background(), uuid.New(), "container-123", "test"); err != nil {
		t.Fatal(err)
	}
	if !docker.stopped || !docker.removed {
		t.Fatalf("stop/remove = %v/%v", docker.stopped, docker.removed)
	}
}

func TestDestroyWritesTenantScopedAuditEvent(t *testing.T) {
	docker := &fakeDocker{}
	auditor := &auditRecorder{}
	manager := newTestManagerWithAuditor(docker, "runsc", auditor)
	workspaceID := uuid.New()

	if err := manager.Destroy(context.Background(), workspaceID, "container-123", "exec_completed"); err != nil {
		t.Fatal(err)
	}
	if len(auditor.events) != 1 {
		t.Fatalf("audit events = %d, want 1", len(auditor.events))
	}
	ev := auditor.events[0]
	if ev.WorkspaceID != workspaceID || ev.Action != sdkaudit.ActionSandboxDestroy ||
		ev.IdempotencyKey != "sandbox_destroy:container-123" || ev.ResourceID != "container-123" ||
		ev.Details["reason"] != "exec_completed" {
		t.Fatalf("audit event = %+v", ev)
	}
}

func TestDestroyReturnsRemoveError(t *testing.T) {
	docker := &fakeDocker{stopErr: errors.New("already stopped"), removeErr: errors.New("remove failed")}
	manager := newTestManager(docker, "runsc")
	if err := manager.Destroy(context.Background(), uuid.New(), "container-123", "test"); err == nil {
		t.Fatal("expected remove error")
	}
}

func TestDestroyValidatesIDAndAllowsAlreadyStoppedContainer(t *testing.T) {
	manager := newTestManager(&fakeDocker{stopErr: errors.New("already stopped")}, "runsc")
	if err := manager.Destroy(context.Background(), uuid.New(), "", "test"); err == nil {
		t.Fatal("expected empty container ID to be rejected")
	}
	if err := manager.Destroy(context.Background(), uuid.New(), "container", "test"); err != nil {
		t.Fatalf("remove should succeed even if stop reports an error: %v", err)
	}
}
