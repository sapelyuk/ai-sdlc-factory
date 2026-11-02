# AI-SDLC Platform Architecture: ai-sdlc-factory

**Version 1.10 · 27.10.2026**

## 0. How to read this document

Three layers:
1. **What we build** — sections 1-3.
2. **How we build** — sections 4-11.
3. **How to execute** — sections 12-17.

## 1. Context, goals, principles

### 1.1 Problem

Software development is fragmented: Git, CI/CD, tracker, knowledge, and communication live in different systems without a unified context. There is no declarative description of teams (roles, models, rules) and no end-to-end control over agents (observability, budgets, security). The `ai-sdlc-factory` platform fills this gap by unifying tools into a single orchestratable environment with a semantic layer.

### 1.2 Goals

- **G1.** Build a team of AI agents without writing code.
- **G2.** Each role on different models from different providers.
- **G3.** Configurable knowledge bases.
- **G4.** Full SDLC from research to operations.
- **G5.** Pluggable Git, CI/CD, tracker, design.
- **G6.** Managed autonomy with human-approve.
- **G7.** Observability and reproducibility.
- **G8.** Security and cost control.
- **G9.** Semantic layer as a first-class citizen.
- **G10.** Architect as a full-fledged role.

### 1.3 Non-goals v1

The platform does **not** implement in v1:
- Own Git hosting, CI runner, task tracker (only adapters via MCP).
- Model training or fine-tuning — pre-trained models are used.
- Public marketplace of roles/teams — out of scope for v1.
- Full multitenancy with billing: out of scope for v1 (single `workspace_id`).

### 1.4 Principles

- **P1.** Role as declaration (YAML).
- **P2.** Model agnosticism via LLM Gateway.
- **P3.** Artifacts over memory (Git, tracker, semantics).
- **P4.** Durable execution.
- **P5.** Security by default.
- **P6.** Configurable autonomy (policy-as-code).
- **P7.** Observability as a feature.
- **P8.** Tools via MCP.
- **P9.** Verification independent of author.
- **P10.** Semantics over text.
- **P11.** Diagrams as code.
- **P12.** Deployment profiles (local-dev, local-full, prod).

## 2. Glossary

| Term | Meaning |
|---|---|
| Role | Agent template: purpose, instructions, tools, model |
| Agent | Role instance with a model, settings, and budget |
| Team | Agents + topology + process + knowledge |
| Process | Declarative SDLC workflow |
| Task | Unit of work in the tracker and orchestrator |
| Gate | Auto-check / agent review / human approve |
| SLP | Ontology of the ai-sdlc-factory platform |
| SLPr | Product domain model |
| AM | Architecture Model (C4 model) |

## 3. Requirements

### 3.1 Functional

- **FR-TEAM.** Role catalog, create/clone/rollback, team builder.
- **FR-KB.** Scope hierarchy, sources (files, URLs, Git), hybrid search.
- **FR-SEM.** SLP as a public contract; SLPr per project; API.
- **FR-ARCH.** Product AM; C4 diagrams; versioned artifacts; ADR.
- **FR-GIT.** OAuth/GitHub App, PR-flow, protected branches.
- **FR-CI.** Test levels, CI integration, environments, rollout strategies.
- **FR-DES/RES/PM.** Design (Figma), research, bidirectional sync with tracker.
- **FR-OPS.** Run console, cost tracking, audit log.

### 3.2 Non-functional

NFR-1 isolation; NFR-2 reliability; NFR-3 scalability; NFR-4 observability (OTel); NFR-5 security (Vault, RBAC); NFR-6 extensibility; NFR-7 reproducibility; NFR-8 deployment (Compose -> K8s); NFR-9 cost; NFR-10 p95 subgraph < 150 ms.

## 4. Architecture overview

### 4.1 Context

```
Owner --> Platform
Reviewers --> Platform
Platform --> LLM (Providers)
Platform --> Git (GitHub/GitLab/Gitea)
Platform --> CI/CD
Platform --> Trackers (Linear/Jira/GitHub Issues)
Platform --> Design (Figma)
Platform --> Research (web, API)
KBSRC --> Platform
```

### 4.2 Containers

**Control Plane:** Web UI, Platform API, Registry, Policy & Approval, IAM, Semantic Service

**Execution Plane:** Orchestrator (Temporal), Agent Runtime, LLM Gateway, Tool Gateway (MCP, `mark3labs/mcp-go`, ADR-022), Sandbox Manager, Diagram Service

**Data Plane:** PostgreSQL 18 + pgvector, Silo (S3), NATS JetStream, Knowledge Service

**Observability:** OTel Collector + Jaeger (always), Langfuse + Prometheus + Grafana (observability profile)

### 4.3 Web UI

**Role:** the only user-facing interface of the Control Plane; `ai-sdlc-factory-web` container served by Nginx at `http://localhost:3000` (NFR-8).

**Responsibilities:**
- Authentication and session management (JWT, see §12.2).
- Browse role, team, and process catalogs (Registry) — FR-TEAM.
- Audit log and run costs (FR-OPS) — future.
- Team builder (FR-TEAM) — future.
- Run console and observability dashboards (FR-OPS, NFR-4) — future.

**Boundaries:** UI does not access the database, Temporal, or storage directly — only through Platform API (`/api/v1/...`). Technical decisions — §12.2.

## 5. Semantic layer

### 5.1 Purpose

Formal machine-readable model: ontology + instances + rules + API.

### 5.2 Three loops

- **SLP** — classes: Role, Team, Agent, Process, Stage, Gate, ArtifactType, Tool, ModelProfile
- **SLPr** — product domain entities + business rules
- **AM** — C4 classes: System, Container, Component, DataStore, Interface, Dependency

### 5.3 Storage

Property graph in PostgreSQL:
- `sem_nodes(id UUIDv7, workspace_id, project_id NULL, layer, kind, label, props JSONB, version, status)`
- `sem_edges(id UUIDv7, src_id, dst_id, kind, props JSONB, valid_from, valid_to)`
- Indexes: GIN on props, btree on kind/project_id, pgvector in sem_embeddings

## 6. Architect role and diagrams

### 6.1 Role specification

```yaml
apiVersion: ai-sdlc-factory/v1
kind: Role
metadata: { name: software-architect, version: 2.0.0 }
spec:
  tools:
    allow: [sem.*, diag.render, diag.publish, kb.search, fs.*, git.read]
    deny: [deploy.prod, secrets.read, git.push_protected]
  model:
    capability: reasoning-strong
    min_context: 200000
  limits: { max_steps: 80, max_cost_usd: 6.0 }
  autonomy: supervised
```

### 6.2 Self-correction loop

The Go backend validator checks:
1. DSL syntax (Mermaid/Structurizr/PlantUML)
2. Semantic links — all nodes must exist in the AM
3. Contract conflicts
4. Absence of forbidden dependencies

On error — a structured stacktrace to the agent. Maximum 3 iterations.

## 7. Domain model

### 7.1 ER

```
WORKSPACE ||--o{ TEAM : contains
WORKSPACE ||--o{ PROJECT : contains
ROLE ||--o{ ROLE_VERSION : has
TEAM_VERSION ||--o{ AGENT : consists_of
AGENT }o--|| ROLE_VERSION : instance_of
PROJECT ||--o{ RUN : executes
RUN ||--o{ STAGE_RUN : contains
STAGE_RUN ||--o{ TASK_RUN : contains
TASK_RUN ||--o{ ARTIFACT : produces
ARTIFACT ||--o| DIAGRAM : may_be
ARTIFACT ||--o| ADR : may_be
```

## 8. Orchestration

### 8.1 Execution hierarchy

Run (workflow) -> StageRun -> TaskRun -> AgentStep (activity). Each AgentStep is idempotent.

### 8.2 Agent loop

Orchestrator -> Agent Runtime -> Semantic Service (subgraph) -> Knowledge Service (search) -> LLM Gateway (request) -> Tool Gateway (call) -> Policy Engine (check) -> Result

### 8.3 Agent-to-agent handoff

Agents do not exchange raw correspondence. All data passes through the semantic layer (SLPr) and knowledge bases (KB). Agents exchange only a strictly structured handoff package.

### 8.4 Handoff package

Agents exchange not raw correspondence, but a strictly structured JSON package (handoff). The receiver MUST validate the package against the JSON schema. If the package is incomplete or contradictory, it is returned to the sender with an error list. This stops the "broken telephone" effect.

Required handoff package fields:
- `goal`: clear task objective.
- `context_refs`: URIs or links to artifacts and semantic layer entities (not text copies).
- `acceptance_criteria`: acceptance criteria (Given/When/Then).
- `constraints`: limitations (stack, rules, prohibitions).
- `decisions_made`: prior decisions (ADR) that must not be violated.
- `open_questions`: questions requiring resolution.
- `expected_output_schema`: JSON schema for the expected result.

### 8.5 LLM Gateway and model profiles

**Capability profiles pin the model version.**

A profile (`code-strong`, `reasoning-strong`, `code-fast`, `cheap-bulk`, `local-private`) is a capability contract. `model_profiles` stores the **specific pinned version** of the model (`claude-sonnet-4-20250101`), not an alias (`-latest`). This protects against silent profile contract changes when the provider repins the alias. Model change is an R2 action (see ADR-021). The actual table is `llm_model_profiles` (global) to avoid conflict with the workspace-scoped `model_profiles` from migration 0001.

**Fallback per `(provider, model)`.**

Health tracking is per `(provider, model_id)` pair, not per provider. Error classification:

- `404` / `401` / `403` / `400` → not availability events. Logged, no cooldown triggered.
- `429` / `5xx` / `timeout` → availability. Cooldown with exponential backoff (60s → 4h, jitter). After cooldown — a probe request; on success — reset, on repeat — extend cooldown.

This prevents flapping on a config error in one model (the whole provider remains available for other models).

**Cost accounting at two levels.**

- `llm_logical_calls` — one row per logical request, tied to `run_id`/`task_id`/`agent_id`. Stores `final_cost_usd` (successful attempt) and `total_cost_usd` (sum of all attempts).
- `llm_attempts` — N rows per HTTP attempt, with status classification and `cost_usd` per attempt.

This correctly accounts for NFR-9 (cost) on retries and mid-stream failover. See ADR-021.

## 9. Knowledge bases

### 9.1 Model and scopes

Hierarchy: `KnowledgeBase` (SLP) → `Source` → `Document` → `Chunk` (`chunk_index` column in `kb_documents`, migration `0003`).

Scopes: `platform`, `team`, `project`, `role`, `task` (FR-KB "Scope hierarchy").

### 9.2 Indexing

Sources -> Connectors -> Normalization -> Chunking -> Embeddings (pgvector) + FTS

### 9.3 Search

Hybrid with RRF, filtered by sem_refs.

## 10. Security

### 10.1 Execution isolation

Each agent runs in an isolated sandbox using gVisor (`runsc`) in production and CI; `runc` is permitted only in `local-dev` with an explicit warning. The Sandbox Manager MUST NOT mount `/var/run/docker.sock` and interacts with the Docker daemon exclusively via a restricted `docker-socket-proxy`. Sandboxes use an egress proxy for network access; no secrets are placed inside the sandbox, and credential brokering is handled by the Tool Gateway. See ADR-023.

### 10.2 Untrusted content and prompt injection

**Separation of Capabilities rule:**
One agent in one step must not simultaneously have access to three things:
1. Private data (database, secrets, source code).
2. Untrusted content (web pages, tickets, README files from other repos).
3. Outbound channel (Git writes, internet requests, tracker comments).

*Example:* A research agent (`product-researcher`) has web access (item 2), but no Git write permissions (item 3). Its output is a structured report (JSON) passed to a developer agent. The developer agent has code access (item 1) and Git access (item 3), but no web access (item 2) within the same task. This prevents execution of malicious instructions received from external content.

### 10.3 Authentication response codes

Error bodies below are actual middleware messages (`apps/api/internal/transport/http`, `apps/semantic/transport/http`), body format `{"error":"<message>"}`.

| Situation | HTTP code | Headers / body |
|---|---|---|
| Missing `Authorization` (JWT-middleware) | `401 Unauthorized` | `WWW-Authenticate: Bearer realm="api"`, body `{"error":"missing Authorization header"}` |
| Invalid `Authorization` format (not `Bearer <token>`) | `401 Unauthorized` | `WWW-Authenticate: Bearer realm="api"`, body `{"error":"invalid Authorization header format"}` |
| Invalid or expired token | `401 Unauthorized` | `WWW-Authenticate: Bearer realm="api"`, body `{"error":"invalid or expired token"}` |
| Missing or `uuid.Nil` `X-Workspace-ID` (tenant-middleware, legacy mode) | `401 Unauthorized` | `WWW-Authenticate: Bearer realm="api"` (semantic: `realm="semantic"`), body `{"error":"missing X-Workspace-ID header"}` |
| Missing or `uuid.Nil` `X-Author-ID` (tenant-middleware, legacy mode) | `401 Unauthorized` | `WWW-Authenticate: Bearer realm="api"` (semantic: `realm="semantic"`), body `{"error":"missing X-Author-ID header"}` |
| Invalid UUID format in `X-Workspace-ID` | `400 Bad Request` | body `{"error":"X-Workspace-ID must be a valid UUID"}` |
| Invalid UUID format in `X-Author-ID` | `400 Bad Request` | body `{"error":"X-Author-ID must be a valid UUID"}` |
| Valid creds but `approver = author` | `403 Forbidden` | body `{"error":"approver cannot be the author"}` |
| Valid creds but role below required (RBAC) | `403 Forbidden` | body `{"error":"insufficient role: requires <role>"}` |
| Successful authentication and authorization | `2xx` | — |

**Note:** `uuid.Nil` is determined after UUID parsing, so a zero identifier in any valid form (`00000000-0000-0000-0000-000000000000`, 32 hex chars, `urn:uuid:…`, `{…}`) is treated as a missing header → `401`. `GET /healthz`, `GET /readyz`, and `GET /metrics` do not require authentication. `POST /api/v1/auth/login` is also available without a token.

### 10.4 Risk levels

- R0 (read) — auto
- R1 (local write) — auto
- R2 (significant) — per profile
- R3 (irreversible) — always human approve

## 11. Integrations, Observability, Cost, Deployment

### 11.1 Integrations

External systems are not duplicated inside the platform — they are sources and sinks of artifacts (§1.4 P3). All access to them goes through the Tool Gateway over MCP (ADR-005, ADR-022): the Tool Gateway loads tool schemas, filters them by `Role.tools.allow` / `Role.tools.deny`, checks policy, injects secrets, executes the call, and writes it to the audit log (`MCP_TOOLS.md` §5). Implementation: `mark3labs/mcp-go` (see ADR-022). Tool registry by role — `docs/architecture/MCP_TOOLS.md`.

| Integration | What it gives the platform | Requirement | Tools (MCP) |
|---|---|---|---|
| VCS (GitHub / GitLab / Gitea) | Branches, commits, PRs, diffs | FR-GIT | `git.clone`, `git.branch`, `git.commit`, `git.push`, `git.diff` |
| CI/CD | Run checks, status, build artifacts | FR-CI | `ci.trigger`, `ci.status` |
| Deploy | Rollout to staging/prod, rollback | FR-CI | `deploy.staging` (R2), `deploy.prod` (R3), `deploy.rollback` (R2) — §11.2 |
| Tracker (Linear / Jira / GitHub Issues) | Bidirectional task and comment sync | FR-DES/RES/PM | `tracker.comment`, `pm.update_backlog`, `bug.open` |
| Design (Figma) | Mocks, frame edits, design tokens | FR-DES/RES/PM | `figma.read` (R0), `figma.write` (R2), `design.export_tokens` |
| Research (web, API) | External sources, summaries, KB drafts | FR-DES/RES/PM | `web.search`, `web.fetch`, `research.summarize` |
| Diagrams | DSL → SVG/PNG, validation, publish to KB | FR-ARCH | `diag.render`, `diag.validate`, `diag.publish` (ADR-006) |
| Security | SAST, dependency and secret scanning | NFR-5 | `sast.run`, `dep.scan`, `secret.scan` |

Integration rules:

1. **Secrets do not enter the sandbox.** Access to external systems — only via credential brokering in the Tool Gateway (§10.1, ADR-009). `secrets.read` and `deploy.prod` permissions are explicitly denied to developer and architect roles (`MCP_TOOLS.md` §3.5, §3.6).
2. **Permissions are part of the role declaration.** The tool set is defined in `Role.spec.tools` (§6.1) and checked by OPA policy at R0–R3 levels (§10.4, ADR-013).
3. **Separation of capabilities.** An agent reading untrusted content (web, other tickets) does not get a write channel (Git, tracker comments) in the same task (§10.2).
4. **Every call is in the audit log.** Audit log and run costs — FR-OPS; cost accounting — §11.4.
5. **VCS contract is indistinguishable from human work.** The agent opens a PR, not a push to a protected branch: `git.push_protected` is forbidden, merge is protected (FR-GIT, `CONTRIBUTING.md`).

The audit log is implemented in `packages/sdk-go/audit` with a SHA-256 hash
chain and is used by Tool Gateway, LLM Gateway, Agent Runtime, and Sandbox
Manager.

### 11.2 Testing, build, deploy

**CI is the quality standard.** Checks in `.github/workflows/ci.yml` (lint → test → build per `apps/*` modules, plus frontend lint and build) are required before review: the `ci_green` gate moves `InDev → CodeReview` (`SDLC_STATE_MACHINE.md` §3.9, §4). New code coverage ≥ 80% is a condition of the same gate (`SDLC_STATE_MACHINE.md` §3.9, `CONTRIBUTING.md`). Weakening tests, coverage thresholds, or CI config for a green build is forbidden (AGENTS.md, R3).

**Sandbox for fast feedback.** Build and test commands are executed by the agent in an isolated gVisor sandbox without network and secrets (§10.1, ADR-009) via `sandbox.exec`; test runs and reports — `test.run`, `test.report` (`MCP_TOOLS.md` §3.7).

**Test levels.** The level and scope of checks is defined by `FR-CI` (§3.1); progression — via the `qa_passed` gate: green tests at all levels, met coverage thresholds, closed critical/high defects (`SDLC_STATE_MACHINE.md` §3.12). Build produces an image, RC, and SBOM under the `sbom_signed` gate (§4).

**Flaky tests** are handled per project policy; repeated failures are escalated to a human along with other circuit breakers (`SDLC_STATE_MACHINE.md` §6, §7). Policy setting names are recorded in the project contract when the project is formalized in `packages/contracts` — in v1 they are not yet defined.

**Environments and rollout strategies.** Environment composition and strategy are defined at the project level (see `FR-CI` §3.1: "test levels, CI integration, environments, rollout strategies"; rollout to staging/prod — Stage 5 §14.1). The specific strategy (rolling, canary, blue/green) is a project config choice, not a platform choice. The platform guarantees only gates: `prod_approved` — human approve for R3, `deploy_healthy` — healthcheck and metrics normal (`SDLC_STATE_MACHINE.md` §4, §8).

**Database migrations** — R2 action (§10.4, `SDLC_STATE_MACHINE.md` §8): applied via `make migrate-up`, rolled back via `make migrate-down`, each `.up.sql` / `.down.sql` pair lives in `migrations/postgres/` (0001–0009, `deploy/compose/README.md`). Verified migrations are a condition for `Release → Deploy` transition (§3.15).

### 11.3 Observability

OTel Collector + Jaeger (always). Langfuse + Prometheus + Grafana (observability profile).

### 11.4 Cost

Budgets: step, task, run, day, month. Estimation before run. 80% — warning; hard — stop.

**Attribution at two levels (ADR-021):**

- `llm_logical_calls.final_cost_usd` — cost that produced a result (what the agent sees).
- `llm_logical_calls.total_cost_usd` — actual spend (including retries and failover).
- `llm_attempts` — per HTTP call detail for diagnostics.

When aggregating budgets, use `total_cost_usd` (real money). When displaying "task cost" — `final_cost_usd` (result).

### 11.5 Deployment profiles

| Profile | Purpose | RAM | Composition |
|---|---|---|---|
| local-dev | Development on 16 GB | ~10 GB | Core + OTel + Jaeger |
| local-full | Development on 32+ GB | ~30 GB | Core + OTel + Jaeger + Langfuse + Prometheus + Grafana |
| prod | Kubernetes | — | Full stack + managed DBs |

## 12. Technology stack

### 12.1. Backend

- **Language:** Go 1.26+ (ADR-001).
- **Frameworks:** chi/echo, OpenAPI 3.1.
- **MCP:** `github.com/mark3labs/mcp-go` (ADR-022) — Tool Gateway (MCP server + client + ProxyTool).
- **Orchestration:** Temporal Go SDK (ADR-002), NATS JetStream.
- **Observability:** OpenTelemetry (NFR-4).

### 12.2. Frontend (apps/web)

- **Core:** Vite 6, React 18, TypeScript (strict, noUnusedLocals, noUnusedParameters).
- **Styling:** Tailwind CSS v4 (CSS-first config, `@theme inline`), shadcn/ui (components on Radix UI: Button, Input, Table, Card, Dialog, Dropdown, Tabs, Tooltip, Toast).
- **State and data:** TanStack Query (server state, retry: 1, staleTime: 5 min), Zustand v5 (global state, auth store).
- **Routing:** React Router DOM v7.18+, classic API (`BrowserRouter`, `Routes`, `Route`, `Navigate`).
- **Tests:** Vitest + Testing Library (jsdom), ESLint + typescript-eslint (`--max-warnings 0`).
- **Build and serve:** multi-stage Dockerfile (Node.js 20 Alpine — builder → Nginx Alpine — production, port 3000, SPA fallback, `/api/` proxy → `api:8000`, security headers).

**Architectural decisions:**

- **JWT authentication:** token is stored **in memory** (Zustand store), not in `localStorage` (XSS protection). On page reload, the session is cleared. `ProtectedRoute` redirects to `/login`.
- **API client:** `src/lib/api.ts` — `login` and `fetchRoles` functions with explicit `Authorization: Bearer <token>`; on `401` — `clearAuth()`.
- **SSE (Server-Sent Events):** `src/lib/sse.ts` — wrapper around `EventSource` with `AbortController` for future agent log streaming and updates. Currently a stub.
- **Pages:** `/login` (authentication), `/` (Dashboard), `/registry/roles` (real API), `/registry/teams` and `/registry/processes` (stub).

**Related requirements:** NFR-8 (deployment via Compose → K8s), ADR-015 (unified name `ai-sdlc-factory`).

### 12.3. Data

- **Database:** PostgreSQL 18 + pgvector (property graph, ADR-003, ADR-019).
- **Object storage:** Silo (S3-compatible MinIO fork, ADR-020).
- **Event bus:** NATS JetStream.

### 12.4. Infrastructure

- **Containerization:** Docker Compose (profiles `local-dev`, `local-full`, ADR-018).
- **Orchestration:** Kubernetes (Helm) — future.

## 13. ADR

Full registry — `docs/architecture/ADR.md` (ADR-001…ADR-023).

## 14. Delivery plan

### 14.1 Stages

| Stage | Content | Acceptance criterion |
|---|---|---|
| 0. Foundation | Monorepo, CI, Compose, DB, auth, Registry, UI, OTel+Jaeger, SLP v0 | Role created in UI; SLP v0 read via API; `make up-local` works |
| 1. One agent | Temporal, Agent Runtime, LLM Gateway, Tool Gateway, Sandbox | Agent creates branch and opens PR; CI green |
| 2. Team | PM/analyst/architect/developer/reviewer/QA roles; handoff; gates | 6 agents implement a feature from ticket to merged PR |
| 3. Knowledge | Knowledge Service, connectors, hybrid search; SLPr v1 + AST indexing | Agent cites a fragment; uses SLPr subgraph |
| 4. Architecture | Architect role, AM v1, Diagram Service, self-correction, ADR, conformance | Architect builds C4, publishes to KB, ADR linked to AM |
| 5. Full SDLC | Research, design, tracker, CI/CD, GitOps, staging/prod | Demo product from idea to staging, then prod with approve |
| 6. Operations | Evals, replay, dashboards, hardening, K8s, backups | Model swap -> evals; security review; `make up-full` works |

## 15. Monorepo structure

```
ai-sdlc-factory/
  .cursorrules, .env.example, Makefile, docker-compose.yml, go.work, README.md
  apps/ (web, api, worker, semantic, semantic-indexer, diagram, knowledge, gateway-llm, tool-gateway, sandbox-manager)
  packages/ (contracts, ontology, policy, sdk-go, sdk-ts)
  backend/ (go.mod, cmd/, internal/)
  frontend/ (package.json, vite.config.ts, src/)
  catalog/ (roles, teams, processes)
  deploy/ (compose, helm, sandboxes, k8s)
  migrations/ (postgres, graph)
  docs/ (README.md, architecture/)
  evals/ (roles, models)
```

## 16. Instructions for AI agent

### 16.1 Work rules

1. Before each stage — a plan, wait for confirmation.
2. Stages — strictly in order.
3. Contracts first (OpenAPI, JSON Schema, MCP schemas), then implementation.
4. Deviation from ADR — only via a new ADR.
5. Security is not deferred.
6. Tests — with code; do not weaken.
7. Small PRs with Conventional Commits; links to FR/NFR/ADR.
8. Naming: `ai-sdlc-factory` everywhere.
9. Deployment profiles: specify the profile for each new service.

### 16.2 Definition of Done for a stage

Before completing a stage (see §14.1), verify:

- [ ] CI green (`ci.yml` — lint → test → build).
- [ ] New code coverage ≥ 80% (`AGENTS.md`).
- [ ] Logs without secrets.
- [ ] Documentation updated in the same PR.
- [ ] Stage acceptance criterion from §14.1 met.

### 16.3 Semantic layer checklist

- [ ] SLP change (major) — R3, human approval (`ONTOLOGY_SPEC.md` §2.3).
- [ ] SLPr/AM change — R2 with review.
- [ ] All artifacts reference entity URIs (`ONTOLOGY_SPEC.md` §9, item 5).
- [ ] SLPr version snapshot is pinned to the run (NFR-7).

### 16.4 Architect and diagrams checklist

- [ ] Diagram passed `diag.validate` (ADR-017).
- [ ] Self-correction loop ≤ 3 iterations (§6.2, ADR-017).
- [ ] ADR linked to AM via semantic layer entities.
- [ ] On AM change — dependent diagrams marked as outdated.

### 16.5 Naming checklist

- [ ] `apiVersion: ai-sdlc-factory/v1` in YAML.
- [ ] Ontology namespace: `ai-sdlc-factory/product-domain@^1`.
- [ ] Root directory: `ai-sdlc-factory/`.
- [ ] Containers and Helm releases: prefix `ai-sdlc-factory-*`.
- [ ] Legacy names (if any in history) — absent (ADR-015).

### 16.6 File generation tactics

| Iteration | Task | Files |
|---|---|---|
| 1. Infrastructure and DB | Compose, migrations | Makefile, deploy/compose/**, migrations/** |
| 2. Contracts and domain | OpenAPI, JSON Schema, domain models | packages/contracts/**, packages/ontology/**, backend/internal/domain/** |
| 3. Semantics and architect | Semantic Service, AST indexer, Diagram Service | backend/internal/semantic/**, architect_engine/** |
| 4. Sandboxes and MCP | Tool Gateway, Sandbox Manager | backend/internal/sandbox/**, mcp/** |
| 5. Orchestrator and LLM | Temporal workflow, Agent Runtime, LLM Gateway | backend/internal/llm/**, orchestra/**, transport/**, cmd/** |
| 6. Frontend | Web UI | frontend/** |

## 17. Open questions

1. Structurizr DSL as the primary C4 format — agreed?
2. Default subgraph depth.
3. Is RDF/OWL export needed in v1?
4. Snapshot retention policy for SLPr/AM/KB.
5. UI multilingual support.
6. Default budgets.
7. LiteLLM as proxy — acceptable?
8. Air-gapped mode in v1?
9. Priority LLM providers.
10. Personal data policy.
11. Should we parse monorepo Bazel/Nx?
12. Merge block on critical drift — default on or off?

## Document changes

| Version | Date | Changes |
|---|---|---|
| 1.11 | 2026-10-30 | Added ADR-024 (data-only migrations) and ADR-025 (pinning exceptions). |
| 1.10 | 27.10.2026 | Added ADR-023 (Sandbox isolation boundary: docker-socket-proxy, egress allowlist, gVisor). |
| 1.9 | 22.10.2026 | Added ADR-022 (selection of `mark3labs/mcp-go` for Tool Gateway). §4.2, §11.1, and §12.1 reference ADR-022. (#83) |
| 1.8 | 22.10.2026 | Restored numbering: §1 (§1.1, §1.3), §5 (§5.1), §9 (§9.1), §16 (§16.2–§16.5). References to §1.4, §16.1, §16.6 preserved. Per review: removed ADR-015 reference from §1.3 (not about multitenancy), clarified KB hierarchy sources in §9.1, fixed coverage threshold source in §16.2 (`AGENTS.md`), removed unconfirmed historical names in §16.5 (#78) |

| 1.7 | 22.10.2026 | Restored §11 numbering: added §11.1 "Integrations" and §11.2 "Testing, build, deploy". §11 heading extended with "Cost" (§11.4). §11.x references unchanged (#75) |
| 1.6 | 22.10.2026 | Added §10.3 (Authentication response codes); error bodies aligned with actual middleware messages — Issue #71 |
| 1.5 | 22.10.2026 | Added §8.5 (LLM Gateway and model profiles) and §11.4 (Cost); §11.3 and §11.5 numbering fixed (#73) |
| 1.4 | 21.10.2026 | Added §4.3 (Web UI), §12 restructured into §12.1–§12.4, added §12.2 (Frontend) (#64) |
| 1.3 | 20.10.2026 | Added §8.3, §8.4 (Handoff package), §10.1, §10.2 (Anti-Prompt Injection) |
| 1.2 | 16.10.2026 | Initial architecture |

**End of document.**
