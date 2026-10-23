# AI-SDLC Factory

**Multi-agent platform for full SDLC with owner control.**

`ai-sdlc-factory` is a platform where AI agent teams implement the full software development lifecycle: from research and design to testing, deployment, and monitoring. Each role and agent runs on its own model, with configurable permissions, budgets, and autonomy levels.

## Quick Start

```bash
# 1. Set up the environment
cp deploy/compose/.env.example deploy/compose/.env.local-dev

# 2. Start the platform
make up-local          # local-dev (~10 GB RAM)
make up-full           # local-full (~30 GB RAM, with Langfuse)

# 3. Verify
make ps-local
```

| Service | URL |
|---|---|
| Web UI | http://localhost:3000 |
| Platform API | http://localhost:8000 |
| Temporal UI | http://localhost:8080 |
| Silo Console | http://localhost:9001 |
| Jaeger UI | http://localhost:16686 |

## Architecture

- **Control Plane:** Web UI, Platform API, Registry, Policy & Approval, IAM, Semantic Service
- **Execution Plane:** Orchestrator (Temporal), Agent Runtime, LLM Gateway, Tool Gateway (MCP), Sandbox Manager, Diagram Service
- **Data Plane:** PostgreSQL 18 + pgvector (property graph), Silo (S3), NATS JetStream, Knowledge Service
- **Observability:** OTel Collector + Jaeger (always), Langfuse + Prometheus + Grafana (observability profile)

## Registry API

After starting PostgreSQL, apply migrations with `make migrate-up`. Registry requests require `X-Workspace-ID` and `X-Author-ID` headers with non-zero UUIDs; changing status additionally requires `X-Approver-ID` different from the author.

Example role creation (spec validated against JSON Schema):

```bash
curl -X POST http://localhost:8000/api/v1/registry/roles \
  -H "Content-Type: application/json" \
  -H "X-Workspace-ID: <workspace-uuid>" \
  -H "X-Author-ID: <author-uuid>" \
  -d '{
    "apiVersion": "ai-sdlc-factory/v1",
    "kind": "Role",
    "metadata": {"name": "backend-developer", "category": "engineering"},
    "spec": {
      "description": "Implements backend features.",
      "system_prompt_ref": "packages/ontology/prompts/backend-developer.yaml",
      "inputs": [{"name": "requirements", "type": "document"}],
      "outputs": [{"name": "implementation", "type": "code"}],
      "tools": {"allow": ["fs.read"]},
      "model": {"capability": "code-strong"},
      "limits": {"max_steps": 60, "max_cost_usd": 3.0},
      "autonomy": "balanced"
    }
  }'
```

For publishing, use `PATCH /api/v1/registry/roles/{id}/status` with body `{"status":"approved"}` and header `X-Approver-ID`. Published specs are immutable; create a new version via `POST /api/v1/registry/roles/{id}/clone` or restore the previous one via `/revert`.

For local verification after applying migrations and starting the API, set `WORKSPACE_ID`, `AUTHOR_ID`, and an `APPROVER_ID` different from the author, then run `pwsh -File tests/smoke/registry-smoke.ps1`.

SLP v0 is loaded with `cd apps/api && go run ./cmd/seed`; set `SEED_WORKSPACE_ID` and `SEED_AUTHOR_ID` for reproducible local loading. If absent, the seed generates UUIDv7 and warns that values are suitable only for local development.

## Stack

- **Backend:** Go 1.26+ (chi/echo, Temporal SDK, NATS, OTel)
- **Frontend:** TypeScript + React 18+, Vite, Tailwind + shadcn/ui
- **Data:** PostgreSQL 18 + pgvector, Silo (S3), NATS JetStream
- **Infrastructure:** Docker Compose → Kubernetes (Helm)

## Documentation

| Document | Description |
|---|---|
| [SYSTEM_DESIGN.md](docs/architecture/SYSTEM_DESIGN.md) | Full platform architecture |
| [ADR.md](docs/architecture/ADR.md) | 23 architecture decisions |
| [ONTOLOGY_SPEC.md](docs/architecture/ONTOLOGY_SPEC.md) | Semantic layer (SLP/SLPr/AM) |
| [SDLC_STATE_MACHINE.md](docs/architecture/SDLC_STATE_MACHINE.md) | Task states and gates |
| [MCP_TOOLS.md](docs/architecture/MCP_TOOLS.md) | Agent tools by role |
| [AGENT_KICKOFF.md](docs/AGENT_KICKOFF.md) | Task creation via GitHub Issues |

## License

AGPLv3
