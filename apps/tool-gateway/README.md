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

Every call (allowed, denied or failed) is written to
`tool_call_audit_log` with a SHA-256 hash chain:

```
hash_n = SHA256(hash_{n-1} || id || agent_id || tool_name || result || timestamp)
```

- The first record chains from an empty string.
- Appends take a PostgreSQL advisory lock, so the chain stays linear across
  gateway replicas.
- Each workspace has its own chain; RLS scopes both the chain-head read and
  the insert.
- `audit.Verify` recomputes the chain and detects modified or deleted rows.

Migration `0011_tool_gateway_audit` creates the table. It is separate from the
generic `audit_log` from migration 0007 because the Tool Gateway needs a
different shape (`tool_name`, `agent_id`, `hash_chain`) and a `result` CHECK
that allows `denied` and `error`; released migrations are not rewritten.

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
```

The integration test applies migration 0011, starts the real HTTP router with
the MCP Streamable HTTP server, calls `git.push` as `developer`, verifies that
the Vault token is absent from the response, checks that `deploy.prod` is
denied with HTTP 403, and recomputes the audit hash chain from PostgreSQL.

## Current limitations (v1)

- Tool execution is a stub (`{"status": "success", "message": "stub"}`).
  Upstream MCP federation via `mcp-go` `ProxyTool` and the Sandbox Manager
  integration land in issue #13.
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
