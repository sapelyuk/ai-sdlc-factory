//go:build integration

package sandbox

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/docker/docker/client"
	"github.com/google/uuid"
	"github.com/sapelyuk/ai-sdlc-factory/apps/sandbox-manager/internal/config"
)

func TestSandboxDockerLifecycleIntegration(t *testing.T) {
	dockerHost := os.Getenv("DOCKER_HOST")
	if dockerHost == "" {
		t.Skip("DOCKER_HOST must point to docker-socket-proxy")
	}
	dockerClient, err := client.NewClientWithOpts(client.WithHost(dockerHost), client.WithAPIVersionNegotiation())
	if err != nil {
		t.Fatal(err)
	}
	defer dockerClient.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	if _, err := dockerClient.Ping(ctx); err != nil {
		t.Fatalf("Docker proxy is unavailable: %v", err)
	}

	workspacePath, err := os.MkdirTemp("", "sandbox-manager-integration-*")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(workspacePath) })
	workspacePath, err = filepath.Abs(workspacePath)
	if err != nil {
		t.Fatal(err)
	}

	runtime := os.Getenv("SANDBOX_TEST_RUNTIME")
	if runtime == "" {
		runtime = "runsc"
	}
	manager := NewManager(dockerClient, &config.Config{
		Runtime:        runtime,
		SandboxImage:   "ai-sdlc-factory/sandbox-go-node:latest",
		SandboxNetwork: "sandbox-net",
		DiskLimitBytes: 2 * 1024 * 1024 * 1024,
	}, slog.New(slog.NewTextHandler(os.Stderr, nil)))
	containerID, err := manager.Create(ctx, uuid.New(), workspacePath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cleanupCancel()
		if err := manager.Destroy(cleanupCtx, containerID); err != nil {
			t.Errorf("destroy sandbox: %v", err)
		}
	})
	stdout, stderr, exitCode, _, _, err := manager.Exec(ctx, containerID, []string{"/bin/echo", "hello"}, 10*time.Second, 1024)
	if err != nil {
		t.Fatal(err)
	}
	if exitCode != 0 || strings.TrimSpace(stdout) != "hello" || stderr != "" {
		t.Fatalf("exec result: stdout=%q stderr=%q exit_code=%d", stdout, stderr, exitCode)
	}
}
