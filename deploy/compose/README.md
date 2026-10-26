# AI-SDLC Factory — local deployment

## Two profiles

| Profile | When to use | RAM | Langfuse |
|---|---|---|---|
| **local-dev** | Development and initial tests (16 GB RAM) | ~10 GB | Off |
| **local-full** | Full development (32+ GB RAM) | ~30 GB | On |

## Quick start

```bash
# 1. Fill .env.local-dev (or .env.local-full)
cp deploy/compose/.env.example deploy/compose/.env.local-dev
$EDITOR deploy/compose/.env.local-dev

# 2. Start the platform
make up-local          # or make up-full

# 3. Verify
make ps-local
```

## Service URLs

| Service | local-dev | local-full |
|---|---|---|
| Web UI | http://localhost:3000 | http://localhost:3000 |
| Platform API | http://localhost:8000 | http://localhost:8000 |
| LLM Gateway | http://localhost:8040 | http://localhost:8040 |
| Tool Gateway (MCP) | http://localhost:8030/mcp | http://localhost:8030/mcp |
| Temporal UI | http://localhost:8080 | — |
| Silo Console | http://localhost:9001 | http://localhost:9001 |
| Jaeger UI | http://localhost:16686 | http://localhost:16686 |
| Langfuse | — | http://localhost:3030 |
| Grafana | — | http://localhost:3001 |
| Prometheus | — | http://localhost:9090 |
| Vault | http://localhost:8200 | http://localhost:8200 |
| OPA   | http://localhost:8181 | http://localhost:8181 |

## Infrastructure services

| Service | Port | Purpose | Profile |
|---|---|---|---|
| Silo (S3) | 9000 (API), 9001 (Console) | S3-compatible storage (MinIO fork) | always |
| NATS | 4222 (client), 8222 (monitoring) | Event bus (JetStream) | always |
| Vault | 8200 | Secret management (dev mode) | always |
| OPA | 8181 | Policy engine (Rego) | always |
| Tool Gateway | 8030 | MCP proxy for agents | always |
| Temporal Server | 7233 | Durable execution for local-dev | local-dev |
| Temporal UI | 8080 | Workflow viewer | local-dev |

### Temporal (orchestration)

Temporal uses two datastores (persistence + visibility) that **must** be in separate PostgreSQL databases. Attempting to use a single database leads to migration conflicts and server crash. Therefore in `ai-sdlc-factory` two databases are created: `ai_sdlc_factory` (persistence) and `temporal_visibility` (visibility).

`temporal_visibility` is created by the init script
`deploy/compose/postgres/init/01-create-temporal-visibility-db.sql` on the first
run on an empty PostgreSQL volume. For an existing volume, init scripts
are not run; if `temporal_visibility` is missing, create it manually:

```bash
docker compose exec postgres sh -c 'psql -U "$POSTGRES_USER" -d postgres -c "CREATE DATABASE temporal_visibility;"'
```

Dynamic config is mounted from
`deploy/compose/temporal/dynamicconfig/development-sql.yaml`. Image versions
are pinned in `docker-compose.local-dev.yml`: the
`temporalio/auto-setup:1.26.3` is used, since the planned `1.26.0` tag
is absent from Docker Hub; `1.26.3` is an available stable patch release of the same
minor branch. In the `local-full` profile, Temporal is not started.

After `make up-local`, create the `ai-sdlc-factory` namespace:

```bash
make temporal-create-namespace
make temporal-smoke-test
```

Smoke test verifies namespace and UI, and also restarts Temporal Server and
checks recovery of a running workflow.

Namespace can be checked directly, and the UI is available at
http://localhost:8080.

### Health check

```bash
make check-infra
```

### Silo — buckets

Buckets are created automatically on first run via `silo-init`:
- `ai-sdlc-factory` — persistent storage (artifacts, diagrams, logs).
- `ai-sdlc-factory-temp` — temporary artifacts (public read for debugging).

> **Note:** Official MinIO images removed from Docker Hub. Using
> Silo (`docker.io/pgsty/silo`) — a community fork, compatible with S3 API,
> `MINIO_*` variables and MinIO data formats. MinIO data is compatible with Silo;
> no migration required.

**Environment variables:** `MINIO_ROOT_USER` / `MINIO_ROOT_PASSWORD` — official
Docker image variable names for MinIO/Silo (Silo fully supports `MINIO_*`).

**Image tag:** Silo uses timestamp tags (`RELEASE.YYYY-MM-DD...`) —
official rolling-release format.

**Volume naming:** Docker volumes use hyphens: `minio-data`, `nats-data`,
`vault-data` — modern Docker Compose standard. The `minio-data` volume is preserved
for compatibility with existing data.

### Vault — dev mode

Vault is running in `server -dev` mode:
- `VAULT_DEV_ROOT_TOKEN` — fixed token (from `.env`, default `root`).
- **Never use in production!** Production config — a separate task.
- Data does not persist between restarts in dev mode.

### OPA — policies

## Observability (basic level)

| Service | Port | Purpose |
|---|---|---|
| OTel Collector (gRPC) | 4317 | OTLP trace ingestion |
| OTel Collector (HTTP) | 4318 | OTLP trace ingestion |
| OTel Collector (metrics) | 8888 | Prometheus metrics |
| OTel Collector (health) | 13133 | Healthcheck |
| Jaeger UI | 16686 | Trace viewer |
| Jaeger admin | 14269 | Healthcheck |
| Jaeger OTLP gRPC | 14317 | Direct OTLP gRPC ingestion |
| Jaeger OTLP HTTP | 14318 | Direct OTLP HTTP ingestion |

### Profiles

- **`local-dev`**: Jaeger in-memory, `MEMORY_MAX_TRACES=10000` (≈500 MB RAM).
- **`local-full`**: Jaeger in-memory, `MEMORY_MAX_TRACES=50000` (≈1 GB RAM), extended OTel config.

### How to send a trace

Any application sends traces to `otel-collector:4317` (OTLP gRPC).
In `local-dev` traces are available in Jaeger UI (`http://localhost:16686`).

### Verification

```bash
# Send a test trace
docker run --rm --network host \
  ghcr.io/open-telemetry/opentelemetry-collector-contrib/telemetrygen:latest \
  traces --otlp-endpoint localhost:4317 --otlp-insecure --traces 1

# Open Jaeger UI
open http://localhost:16686
```

### Langfuse exporter (M6)

Langfuse exporter in OTel Collector remains commented out — will be added in M6.
Currently traces go only to Jaeger.
Langfuse infrastructure (ClickHouse, PostgreSQL, Redis, Web, Worker) is already available
in the `local-full` profile, but LLM tracing, evals, and quality dashboards —
a separate task.

### Legacy jaeger exporter and ports 14317/14318

OTel Collector uses **legacy jaeger exporter** (`otlp/jaeger` on port `4317`
inside Docker network) to send traces to Jaeger. This is a proven working approach.

Ports `14317` (OTLP gRPC) and `14318` (OTLP HTTP) on Jaeger are exposed to the host
**for debugging only** — external clients can send traces directly to Jaeger,
bypassing OTel Collector. In the main architecture, all traces should go through
OTel Collector (batch processor, memory_limiter, single routing point).

Rego policies are in `packages/policy/`:
- `autonomy.rego` — autonomy levels R0–R3 (ADR-013).
- `risk_levels.rego` — risk assessment by task category.

Example query:
```bash
curl -X POST http://localhost:8181/v1/data/ai_sdlc_factory/autonomy/risk_level \
  -d '{"input":{"action":"deploy.prod"}}'
# → {"result":"R3"}
```

### Shutdown

```bash
make down-local        # or make down-full
```

## Full data reset

```bash
make clean             # containers and networks
make volumes           # ALL volumes (caution!)
```

## If out of memory (OOM)

1. Check `docker stats` — which container uses the most.
2. Reduce limits in `docker-compose.local-dev.yml`.
3. Increase swap on the host.
4. Disable unnecessary services in the override file.

## Database migrations

PostgreSQL is used with the `pgvector/pgvector:pg18` image (built-in pgvector).

### Applying migrations

```bash
# Apply all .up.sql in order
make migrate-up

# Rollback all .down.sql in reverse order
make migrate-down
```

Cross-platform targets work the same on Linux/macOS and on Windows
(cmd.exe and PowerShell). For POSIX shells, `scripts/migrate.sh` is used,
for Windows — `scripts/migrate.ps1`; the choice is made by `make` via `ifeq ($(OS),Windows_NT)`.
Only GNU make and Docker Compose are required — `psql` on the host is not needed, it
runs inside the `postgres` container.

Migrations are **not** idempotent: a repeated `make migrate-up` on an already updated
database will fail with `relation "..." already exists`. For a clean reinstall,
run `make migrate-down`, then `make migrate-up`.

> `make migrate-down` removes only tables created by migrations. Temporal
> system tables in the same database (`namespaces`, `shards`, `history_node`, ...)
> remain untouched.

### SLP v0 seed

```bash
# Create seed-workspace and load 14 SLP v0 classes
make seed-slp
```

The target is also cross-platform (`scripts/seed-slp.sh` / `scripts/seed-slp.ps1`) and
consists of two steps:

1. `deploy/compose/postgres/seed/default-workspace.sql` creates a workspace if it
   does not already exist (`ON CONFLICT DO NOTHING`, so the seed can be run repeatedly).
2. The `semantic` container runs `/app/seed-slp`, which loads SLP v0 classes.

The workspace identifier is taken from the `SEED_WORKSPACE_ID` variable. The default
value `00000000-0000-0000-0000-000000000001` is substituted automatically from
`Makefile`, no need to set it separately. To seed a different workspace,
override the variable:

```bash
# Linux / macOS
make seed-slp SEED_WORKSPACE_ID=11111111-1111-1111-1111-111111111111
```

```powershell
# Windows (PowerShell)
make seed-slp SEED_WORKSPACE_ID=11111111-1111-1111-1111-111111111111
```

Before `make seed-slp`, the `postgres` and `semantic` services must be running
(`make up-local`).

### Migration testing

```bash
cd migrations/tests
go test -v ./...
```

Tests use Testcontainers (`pgvector/pgvector:pg18` image).
If Docker is not running, container tests are skipped (`t.Skip`).

Migrations are in `migrations/postgres/`:

| # | File | Description |
|---|---|---|
| 1 | `0001_init_squads_and_agents` | workspaces, squads, agents, model_profiles |
| 2 | `0002_init_tasks_and_sdlc` | runs, stage_runs, task_runs, approvals |
| 3 | `0003_init_knowledge_base` | kb_documents, kb_sources |
| 4 | `0004_init_pgvector` | pgvector extension, kb_embeddings, HNSW |
| 5 | `0005_init_semantic_graph` | sem_nodes, sem_edges, sem_embeddings |
| 6 | `0006_init_architecture_and_adr` | diagrams, architecture_models, adrs |
| 7 | `0007_init_audit_and_cost` | audit_log, llm_calls |
| 8 | `0008_init_registry` | roles, teams, processes (semver) |
| 9 | `0009_init_users` | users, JWT auth, RBAC |
| 10 | `0010_llm_gateway` | llm_model_profiles, model_health, llm_logical_calls, llm_attempts |
| 11 | `0011_tool_gateway_audit` | tool_call_audit_log (hash-chained audit) |

Each migration has a paired `.up.sql` / `.down.sql` file for applying and rolling back.
