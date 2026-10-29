# Sandbox Manager

Sandbox Manager provisions short-lived, resource-limited containers for task
run commands. It listens on port **8060** and reaches the Docker daemon only
through `docker-socket-proxy`.

## Contract

- `POST /v1/sandboxes/exec` requires `task_run_id`. The workspace path is
  resolved from `task_runs.workspace_path` (must be set by Agent Runtime). The
  request is rejected with 400 if `workspace_path` is NULL, and with 403 if
  `task_runs.workspace_id` does not match `X-Workspace-ID`.
- Disk limit: 2 GB per sandbox (configurable via
  `SANDBOX_DISK_LIMIT_BYTES`). Docker applies this limit to the container's
  writable layer; the host bind-mounted workspace is persistent and is not
  counted against the writable-layer quota.
- Runtime: `runsc` (gVisor) in production/CI; `runc` fallback only in
  `local-dev` with a WARN log.
- No `/var/run/docker.sock` mounted; all Docker API calls go through
  `docker-socket-proxy` (ADR-023).

## Execution API

`POST /v1/sandboxes/exec` requires `X-Workspace-ID` and `X-Author-ID` UUID
headers. Its JSON body is:

```json
{
  "task_run_id": "018f0000-0000-7000-8000-000000000001",
  "command": ["go", "test", "./..."],
  "timeout_seconds": 300,
  "stdout_limit_bytes": 1048576
}
```

The service rejects a missing or invalid TaskRun ID, a cross-workspace TaskRun,
and an empty `workspace_path`. Clients cannot provide a workspace path. Every
request creates a sandbox, executes one command, then stops and removes the
container, including when execution fails.

Response fields are `status`, `stdout`, `stderr`, `exit_code`,
`stdout_truncated`, and `stderr_truncated`. Each output stream is independently
capped by `stdout_limit_bytes` (maximum 16 MiB).

## Isolation

Sandboxes run as UID/GID `1000:1000`, with a read-only root filesystem, a
512 MiB memory limit, 0.5 CPU, a 100-process limit, a 2 GB writable-layer quota,
and a tmpfs `/tmp`. The workspace bind mount is writable to retain TaskRun
changes, and `HOME=/workspace` keeps Go, Node, and Python caches writable.
Docker's writable-layer quota does not include that host-mounted workspace.
Sandboxes have no secrets and use a dedicated internal Docker network; the only
route to external hosts is the egress proxy.

The Squid allowlist is configured in `deploy/sandboxes/squid.conf`. Requests
are written to the egress proxy's container log. Update the allowlist through
reviewed configuration changes; do not make it configurable from a tool call.

### Disk allocation

The 2 GB platform limit is selected using these typical build estimates:

| Use | Estimate |
|---|---:|
| Go modules and build cache | 500 MB |
| Node `node_modules` | 1 GB |
| Python virtual environments and packages | 500 MB |
| Logs and artifacts | 100 MB |
| Headroom | 400 MB |

These are planning estimates, not per-language quotas.

## Configuration

| Variable | Default | Description |
|---|---|---|
| `PORT` | `8060` | HTTP listen port |
| `DOCKER_HOST` | required | Docker API proxy URL, normally `tcp://docker-socket-proxy:2375` |
| `RUNTIME` | `runc` | `runsc` for production/CI; `runc` emits a warning for local development |
| `DATABASE_URL` | required | PostgreSQL DSN used to resolve tenant-scoped TaskRuns and write audit events |
| `SANDBOX_DISK_LIMIT_BYTES` | `2147483648` | Container writable-layer size |
| `SANDBOX_NETWORK` | `sandbox-net` | Dedicated internal network attached to sandbox containers |
| `SANDBOX_IMAGE` | `ai-sdlc-factory/sandbox-go-node:latest` | Prebuilt sandbox base image |

The `sandbox-manager` service bind-mounts `SANDBOX_WORKSPACE_ROOT` at the same
absolute path. Set it to a path visible to both the Docker daemon and manager;
Agent Runtime must store workspace paths using this daemon-visible path.

## Local development

The root Compose file builds the base image before starting Sandbox Manager.
Set `SANDBOX_RUNTIME=runc` for local engines without gVisor. Do not use `runc`
with untrusted code.

Apply database migrations before using the endpoint:

```bash
make migrate-up
```

Migration `0013_task_runs_workspace_path` adds the nullable workspace path.
Until Agent Runtime creates and records a workspace, requests for that TaskRun
fail closed with HTTP 400.

## Testing

### Unit tests

```bash
cd apps/sandbox-manager
go test -coverprofile=coverage.out ./...
go tool cover -func=coverage.out
```

### Integration test (real Docker)

`TestSandboxDockerLifecycleIntegration` (build tag `integration`) exercises
create, `echo hello`, and destroy through the configured Docker API endpoint.
Per ADR-023 it requires a running `docker-socket-proxy` on `localhost:2375`
(the only component allowed to see `/var/run/docker.sock`), the internal
`sandbox-net` network, and the `ai-sdlc-factory/sandbox-go-node:latest` image:

```bash
# One-time setup (same pinned image digest as deploy/compose/docker-compose.yml;
# tag 0.2 does not proxy the exec-attach connection hijack)
docker run -d --name docker-socket-proxy \
  -p 127.0.0.1:2375:2375 \
  -v /var/run/docker.sock:/var/run/docker.sock:ro \
  -e CONTAINERS=1 -e POST=1 -e EXEC=1 \
  -e IMAGES=0 -e VOLUMES=0 -e NETWORKS=0 -e BUILD=0 -e SYSTEM=0 \
  'tecnativa/docker-socket-proxy:master@sha256:e0afed6a302dafe6fc5ef2a93358660502795177136accfd42cfdab08e7ca6c0'
docker network create --internal sandbox-net

# Run (from the repository root)
make test-sandbox-integration
```

The target builds the sandbox image, then runs the test with
`SANDBOX_TEST_RUNTIME=runc` because GitHub runners and local Docker Desktop
usually have no gVisor; the manager logs a WARN when falling back to `runc`.

In CI (`ubuntu-latest`, `Sandbox Integration (Docker)` job) this test runs
automatically and must pass. Without `DOCKER_HOST` it is skipped locally, but
fails loudly when `CI=true` is set — a skipped integration test in CI is
treated as a broken harness, not as green.
