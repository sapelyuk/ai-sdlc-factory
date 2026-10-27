package sandbox

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"path/filepath"
	"time"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/mount"
	"github.com/docker/docker/pkg/stdcopy"
	"github.com/google/uuid"
	"github.com/sapelyuk/ai-sdlc-factory/apps/sandbox-manager/internal/config"
)

const (
	labelService  = "ai-sdlc-factory"
	labelTaskRun  = "task_run_id"
	labelSandbox  = "sandbox"
	maxExecOutput = 16 * 1024 * 1024
)

// Manager creates and manages isolated task-run containers.
type Manager struct {
	docker DockerClient
	config *config.Config
	logger *slog.Logger
}

// NewManager constructs a sandbox manager.
func NewManager(docker DockerClient, cfg *config.Config, logger *slog.Logger) *Manager {
	if logger == nil {
		logger = slog.Default()
	}
	return &Manager{docker: docker, config: cfg, logger: logger}
}

// Ready checks the Docker API connection through the configured proxy.
func (m *Manager) Ready(ctx context.Context) error {
	_, err := m.docker.Ping(ctx)
	return err
}

// Create creates a non-root sandbox with a read-only root filesystem.
func (m *Manager) Create(ctx context.Context, taskRunID uuid.UUID, workspacePath string) (string, error) {
	if taskRunID == uuid.Nil {
		return "", errors.New("task_run_id must not be nil")
	}
	if !filepath.IsAbs(workspacePath) || filepath.Clean(workspacePath) != workspacePath {
		return "", errors.New("workspace_path must be a clean absolute path")
	}

	runtime := m.config.Runtime
	if runtime == "runc" {
		m.logger.Warn("gVisor (runsc) not available. Falling back to runc. Do not use with untrusted code.")
	}
	diskLimitMB := m.config.DiskLimitBytes / (1024 * 1024)
	if diskLimitMB < 1 {
		diskLimitMB = 1
	}
	pidsLimit := int64(100)
	containerConfig := &container.Config{
		Image: m.config.SandboxImage,
		User:  "1000:1000",
		Env: []string{
			"HTTP_PROXY=http://egress-proxy:3128",
			"HTTPS_PROXY=http://egress-proxy:3128",
			"NO_PROXY=localhost,127.0.0.1",
			"HOME=/workspace",
		},
		Labels: map[string]string{
			labelService: "task-run-" + taskRunID.String(),
			labelTaskRun: taskRunID.String(),
			labelSandbox: "true",
		},
	}
	hostConfig := &container.HostConfig{
		Privileged:     false,
		ReadonlyRootfs: true,
		Runtime:        runtime,
		NetworkMode:    container.NetworkMode(m.config.SandboxNetwork),
		StorageOpt:     map[string]string{"size": fmt.Sprintf("%dM", diskLimitMB)},
		Resources: container.Resources{
			Memory:    512 * 1024 * 1024,
			NanoCPUs:  500_000_000,
			PidsLimit: &pidsLimit,
		},
		CapDrop:     []string{"ALL"},
		SecurityOpt: []string{"no-new-privileges:true"},
		Mounts: []mount.Mount{{
			Type:        mount.TypeBind,
			Source:      workspacePath,
			Target:      "/workspace",
			ReadOnly:    false,
			BindOptions: &mount.BindOptions{Propagation: mount.PropagationRPrivate},
		}},
		Tmpfs: map[string]string{"/tmp": "rw,noexec,nosuid,size=64m"},
	}
	created, err := m.docker.ContainerCreate(ctx, containerConfig, hostConfig, nil, nil, "")
	if err != nil {
		return "", fmt.Errorf("create sandbox container: %w", err)
	}
	if err := m.docker.ContainerStart(ctx, created.ID, container.StartOptions{}); err != nil {
		cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
		defer cancel()
		removeErr := m.docker.ContainerRemove(cleanupCtx, created.ID, container.RemoveOptions{Force: true, RemoveVolumes: true})
		if removeErr != nil {
			return "", errors.Join(fmt.Errorf("start sandbox container: %w", err), fmt.Errorf("remove failed container: %w", removeErr))
		}
		return "", fmt.Errorf("start sandbox container: %w", err)
	}
	return created.ID, nil
}

// Exec runs command in an existing sandbox and caps captured stdout and stderr.
func (m *Manager) Exec(ctx context.Context, containerID string, command []string, timeout time.Duration, outputLimit int) (stdout, stderr string, exitCode int, stdoutTruncated, stderrTruncated bool, err error) {
	if containerID == "" {
		return "", "", 0, false, false, errors.New("container ID is required")
	}
	if len(command) == 0 || command[0] == "" {
		return "", "", 0, false, false, errors.New("command must contain at least one non-empty argument")
	}
	if timeout <= 0 {
		return "", "", 0, false, false, errors.New("timeout must be positive")
	}
	if outputLimit <= 0 || outputLimit > maxExecOutput {
		return "", "", 0, false, false, fmt.Errorf("stdout_limit_bytes must be between 1 and %d", maxExecOutput)
	}
	execCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	created, err := m.docker.ContainerExecCreate(execCtx, containerID, container.ExecOptions{
		Cmd:          command,
		AttachStdout: true,
		AttachStderr: true,
	})
	if err != nil {
		return "", "", 0, false, false, fmt.Errorf("create container exec: %w", err)
	}
	attached, err := m.docker.ContainerExecAttach(execCtx, created.ID, container.ExecAttachOptions{})
	if err != nil {
		return "", "", 0, false, false, fmt.Errorf("attach container exec: %w", err)
	}
	defer attached.Close()

	stdoutBuffer := &limitedBuffer{limit: outputLimit}
	stderrBuffer := &limitedBuffer{limit: outputLimit}
	if _, err := stdcopy.StdCopy(stdoutBuffer, stderrBuffer, attached.Reader); err != nil {
		if execCtx.Err() != nil {
			return stdoutBuffer.String(), stderrBuffer.String(), 0, stdoutBuffer.Truncated(), stderrBuffer.Truncated(), fmt.Errorf("container exec timed out or was canceled: %w", execCtx.Err())
		}
		return stdoutBuffer.String(), stderrBuffer.String(), 0, stdoutBuffer.Truncated(), stderrBuffer.Truncated(), fmt.Errorf("read container exec output: %w", err)
	}
	if err := execCtx.Err(); err != nil {
		return stdoutBuffer.String(), stderrBuffer.String(), 0, stdoutBuffer.Truncated(), stderrBuffer.Truncated(), fmt.Errorf("container exec timed out or was canceled: %w", err)
	}

	inspection, err := m.docker.ContainerExecInspect(execCtx, created.ID)
	if err != nil {
		return stdoutBuffer.String(), stderrBuffer.String(), 0, stdoutBuffer.Truncated(), stderrBuffer.Truncated(), fmt.Errorf("inspect container exec: %w", err)
	}
	return stdoutBuffer.String(), stderrBuffer.String(), inspection.ExitCode, stdoutBuffer.Truncated(), stderrBuffer.Truncated(), nil
}

// Destroy stops and removes a sandbox and any anonymous volumes it owns.
func (m *Manager) Destroy(ctx context.Context, containerID string) error {
	if containerID == "" {
		return errors.New("container ID is required")
	}
	stopErr := m.docker.ContainerStop(ctx, containerID, container.StopOptions{Timeout: ptr(int(1))})
	removeErr := m.docker.ContainerRemove(ctx, containerID, container.RemoveOptions{Force: true, RemoveVolumes: true})
	if stopErr != nil && removeErr != nil {
		return errors.Join(fmt.Errorf("stop sandbox container: %w", stopErr), fmt.Errorf("remove sandbox container: %w", removeErr))
	}
	if removeErr != nil {
		return fmt.Errorf("remove sandbox container: %w", removeErr)
	}
	return nil
}

func ptr[T any](value T) *T { return &value }

type limitedBuffer struct {
	buffer    bytes.Buffer
	limit     int
	truncated bool
}

func (b *limitedBuffer) Write(value []byte) (int, error) {
	if b.buffer.Len() < b.limit {
		remaining := b.limit - b.buffer.Len()
		if len(value) > remaining {
			_, _ = b.buffer.Write(value[:remaining])
			b.truncated = true
		} else {
			_, _ = b.buffer.Write(value)
		}
	} else if len(value) > 0 {
		b.truncated = true
	}
	return len(value), nil
}

func (b *limitedBuffer) String() string { return b.buffer.String() }

func (b *limitedBuffer) Truncated() bool { return b.truncated }

var _ io.Writer = (*limitedBuffer)(nil)
