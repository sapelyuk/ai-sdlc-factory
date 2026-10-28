# ai-sdlc-factory — Tool Gateway

MCP proxy that gives agents a single tool endpoint. It loads MCP tool schemas,
enforces OPA policies (R0–R3), writes a hash-chained audit log and brokers
credentials from Vault/OpenBao. The MCP protocol itself is implemented by
[`github.com/mark3labs/mcp-go`](https://github.com/mark3labs/mcp-go)
(ADR-022); this service adds the gateway layer on top.

## Endpoints

| Method | Path | Auth | Description |
|---|---|---|---|
| GET | `/healthz` | — | Liveness probe |
| GET | `/readyz` | — | Readiness probe (PostgreSQL + policy engine) |
| POST | `/mcp` | Identity headers | MCP Streamable HTTP endpoint |
| GET | `/mcp` | Identity headers | MCP server-initiated stream |
| DELETE | `/mcp` | Identity headers | MCP session termination |
| GET | `/audit?run_id=<id>` | Admin identity headers | Audit records for the caller's workspace and run |
| GET | `/audit/verify` | Admin identity headers | Verify the caller's workspace hash chain |

The service listens on port **8030** by default.

## Identity headers

Every `/mcp` request must carry the caller identity:

| Header | Required | Description |
|---|---|---|
| `X-Workspace-ID` | yes | Workspace UUID (tenant scope, RLS) |
| `X-Agent-ID` | yes | Agent UUID (audit actor) |
| `X-Agent-Role` | yes | Role name, e.g. `developer`, `qa`, `reviewer`, `devops`, `admin`, `viewer` |
| `X-Autonomy-Level` | no | `R0`–`R3`, default `R0` (least privilege) |
| `X-Author-ID` | no | Human/agent author of the action, defaults to `X-Agent-ID` |
| `X-Approver-ID` | no | Human approver; required for every R3 action |

Short role names (`developer`, `qa`, `reviewer`, `devops`) are aliases for the
schema roles `software-engineer`, `qa-engineer`, `code-reviewer` and
`devops-engineer`.

## Trust model

Tool Gateway trusts the headers `X-Agent-Role`, `X-Agent-ID`, `X-Workspace-ID`,
`X-Approver-ID` **only when the service is deployed behind a trusted reverse
proxy (mTLS or JWT-based)**. The proxy is responsible for authenticating agents
and stripping any client-supplied identity headers.

In `local-dev`, the port `8030` is exposed for debugging; do not use this
configuration in production.

## Policy enforcement (R0–R3)

Before a `tools/call` reaches the MCP server, the gateway evaluates
`data.ai_sdlc_factory.autonomy.decision` in OPA. The decision contains
`allow`, `risk_level` and `reason`.

- **Role × tool matrix** — a tool not listed for the role is denied.
- **Deny wins over allow** — `deny` from the role schema and the static
  deny-list always win.
- **Risk level** — the higher of the schema-declared level and the
  classification in `packages/policy/risk_levels.rego`.
- **R0/R1** — automatic.
- **R2** — allowed only when `X-Autonomy-Level` is `R2` or `R3`.
- **R3** — always requires `X-Approver-ID`, and the approver must differ from
  the author. Otherwise the call is rejected with **403 Forbidden**.

A denied HTTP call is answered with HTTP 403 and a JSON-RPC error
(`-32001`) before the MCP layer runs. The denial is written to the audit log.
If OPA is unreachable the gateway fails closed with HTTP 503.

## Audit log

Every call (allowed, denied or failed) is recorded as a `tool_call` event in the
global, workspace-scoped audit chain. The shared implementation, including
PostgreSQL locking, RLS scoping, secret redaction, and chain verification, lives
in [`packages/sdk-go/audit`](../../packages/sdk-go/audit). Tool Gateway maps its
tool-call fields onto that event contract.

The audit endpoints require an authenticated `admin` identity. Both endpoints
use `X-Workspace-ID` as the tenant scope; the run query matches `run_id`,
`task_run_id`, or the record's `resource_id`. The legacy
`GET /admin/audit/verify?workspace_id=<id>` endpoint remains available.

If the audit write fails, the tool call fails: an unaudited call is not
allowed.

## Credential brokering

Agents never receive tokens. For tools that declare a secret requirement
(`git.clone`, `git.push`, `git.open_pr`), the gateway reads
`secret/data/github` from Vault/OpenBao, injects the token into the upstream
call and returns only the injected environment variable names.

- KV v2 (`data.data`) and KV v1 (`data`) responses are supported.
- Secrets are cached in memory for `VAULT_CACHE_TTL` (default `60s`).
- Known secret values and token-shaped strings (`ghp_…`, `github_pat_…`,
  `glpat-…`, bearer tokens) are redacted from responses, errors and logs.
- Without `VAULT_TOKEN` the gateway starts, but tools that need a secret fail
  closed.

## Upstream proxy

Authorized calls are forwarded to upstream MCP servers over Streamable HTTP
using the `mcp-go` client. Routes are configured with `MCP_UPSTREAMS`, a
comma-separated list of `tool=url` pairs; `*` is the fallback route:

```bash
MCP_UPSTREAMS="git.clone=http://git-mcp:9000/mcp,git.push=http://git-mcp:9000/mcp,*=http://sandbox:9000/mcp"
```

- Connections are established lazily and cached per URL; the MCP handshake
  runs on first use.
- Brokered credentials travel in the `_meta` field
  `ai-sdlc-factory/credentials` and never reach the agent.
- Upstream errors (`isError`) and transport failures are audited as `error`.
- `sandbox.exec` is always sent to Sandbox Manager at `SANDBOX_MANAGER_URL`;
  it is never routed through `MCP_UPSTREAMS` or the stub executor.
- With `MCP_UPSTREAMS` empty, non-sandbox tools use the stub executor.

## Configuration

| Variable | Default | Description |
|---|---|---|
| `PORT` | `8030` | HTTP listen port |
| `DATABASE_URL` | `postgres://postgres:postgres@localhost:5432/ai_sdlc_factory?sslmode=disable` | PostgreSQL DSN for the audit log |
| `OPA_URL` | `http://localhost:8181` | OPA base URL; set to `embedded` to evaluate Rego in-process |
| `POLICY_DIR` | `packages/policy` | Rego directory for the in-process engine |
| `SCHEMA_DIR` | `packages/contracts/mcp-schemas` | MCP schema directory |
| `POLICY_TIMEOUT` | `5s` | OPA request timeout |
| `VAULT_ADDR` | `http://localhost:8200` | Vault/OpenBao address |
| `VAULT_TOKEN` | — | Vault token; empty disables credential brokering |
| `VAULT_SECRET_MOUNT` | `secret` | KV mount name |
| `VAULT_CACHE_TTL` | `60s` | Secret cache TTL |
| `MCP_UPSTREAMS` | — | Comma-separated `tool=url` upstream routes; empty uses the stub executor |
| `SANDBOX_MANAGER_URL` | `http://localhost:8060` | Sandbox Manager API base URL |

Schema loading is fail-fast: a malformed or missing schema file prevents the
service from starting.

## Local run

```bash
# From the repository root (OPA, Vault and PostgreSQL from Compose)
make up-local

# Or run the binary against local infrastructure
cd apps/tool-gateway
OPA_URL=embedded POLICY_DIR=../../packages/policy \
SCHEMA_DIR=../../packages/contracts/mcp-schemas \
DATABASE_URL=postgres://ai_sdlc_factory:ai_sdlc_dev_password@localhost:5432/ai_sdlc_factory?sslmode=disable \
go run ./cmd/tool-gateway
```

Health check:

```bash
curl -s http://localhost:8030/healthz
curl -s http://localhost:8030/readyz
```

Example tool call:

```bash
curl -s -X POST http://localhost:8030/mcp \
  -H 'Content-Type: application/json' \
  -H 'Accept: application/json, text/event-stream' \
  -H 'X-Workspace-ID: 11111111-1111-1111-1111-111111111111' \
  -H 'X-Agent-ID: 22222222-2222-2222-2222-222222222222' \
  -H 'X-Agent-Role: developer' \
  -H 'X-Autonomy-Level: R1' \
  -d '{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"git.push","arguments":{"repo":"ai-sdlc-factory"}}}'
```

## Testing

```bash
# Unit tests (no Docker required)
go test ./...

# Coverage
go test -coverprofile=coverage.out ./...
go tool cover -func=coverage.out

# Integration test (requires Docker; starts PostgreSQL 18 via testcontainers)
go test -run TestToolGatewayEndToEnd -v ./internal/mcp/

# Upstream proxy integration test (no Docker; in-process test MCP server)
go test -run TestProxyExecutorAgainstUpstreamServer -v ./internal/mcp/
```

The integration test applies migration 0011, starts the real HTTP router with
the MCP Streamable HTTP server, calls `git.push` as `developer`, verifies that
the Vault token is absent from the response, checks that `deploy.prod` is
denied with HTTP 403, and recomputes the audit hash chain from PostgreSQL.

## Current limitations (v1)

- Tool execution is a stub unless `MCP_UPSTREAMS` is configured. The
  sandbox-backed executor for untrusted code execution lands in issue #13.
- Only the schema files currently present in
  `packages/contracts/mcp-schemas/` are loaded. Issue #16 delivered three role
  schemas (`developer`, `qa`, `reviewer`); the other seven role files from
  `MCP_TOOLS.md` are not in the repository yet. The loader is generic and
  fails fast, so adding them requires no code change.
- `deploy.prod` is enforced by the policy matrix, but it is not an exposed MCP
  tool until the DevOps schema is added.

## References

- ADR-022 — `mark3labs/mcp-go` for the Tool Gateway
- ADR-005 — MCP as the tool protocol
- ADR-013 — autonomy levels R0–R3
- `docs/architecture/MCP_TOOLS.md` — tool registry by role
- NFR-5 (security), NFR-6 (extensibility)
