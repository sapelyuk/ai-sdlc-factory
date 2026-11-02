# Architecture Decision Records (ADR) — ai-sdlc-factory

**Version 1.8 · 30.10.2026**

Format: MADR. Statuses: `proposed` | `accepted` | `superseded` | `deprecated` | `requires_review`.

---

## ADR-001. Go as primary backend language

**Status:** accepted.

**Context.** Requires performance, low memory consumption, simple deployment, good concurrency support.

**Decision.** All `ai-sdlc-factory` backend — in Go: Platform API, Registry, Orchestrator wrapper, Agent Runtime, LLM Gateway, Tool Gateway, Sandbox Manager, Semantic Service, Knowledge Service, Diagram Service.

**Consequences.** Temporal Go SDK; for LLM providers without Go SDK — HTTP adapters or LiteLLM.

**References:** FR-TEAM, NFR-3, NFR-6.

---

## ADR-002. Temporal as durable execution engine

**Status:** accepted.

**Context.** Long SDLC processes (hours-days), pauses for human approval, retries on LLM API failure.

**Decision.** Temporal (Go SDK). **Run**, **StageRun**, **TaskRun** — workflows. **AgentStep** and tool calls — activities with idempotency keys.

**Consequences.** Pros: restarts, timers, signals, long waits. Cons: Temporal operational complexity, deterministic workflow code requirement.

**References:** NFR-2, NFR-7.

---

## ADR-003. Semantic layer as property graph in PostgreSQL

**Status:** accepted.

**Context.** Need a knowledge graph for SLP/SLPr/AM with versioning, temporality, ACID, and minimal infrastructure.

**Decision.** Property graph in PostgreSQL: `sem_nodes`, `sem_edges`, `sem_embeddings` (pgvector). GIN on props, btree on kind/project_id.

**Consequences.** Single DB simplifies operations; complex traversals — via recursive CTEs.

**References:** FR-SEM-1…FR-SEM-6, NFR-10.

---

## ADR-003b. Deferred: graph DB as read replica

**Status:** proposed (deferred to v2).

---

## ADR-004. Model capability profiles instead of names

**Status:** accepted.

**Decision.** Roles and agents request a profile: `code-strong`, `code-fast`, `reasoning-strong`, `reasoning-fast`, `long-context`, `vision-capable`, `cheap-bulk`, `local-private`.

**References:** FR-TEAM-3, NFR-9.

---

## ADR-005. MCP as tool protocol

**Status:** accepted.

**Decision.** All agent external capabilities — MCP servers. Role tool schemas — in `packages/contracts/mcp-schemas/`.

**References:** FR-TEAM, NFR-6.

---

## ADR-006. Diagrams as code with KB publication

**Status:** accepted.

**Decision.** Canonical sources — Structurizr DSL (C4, primary), Mermaid, PlantUML. Rendering — in Diagram Service. Publication — in Knowledge Service.

**References:** FR-ARCH-1…FR-ARCH-7.

---

## ADR-007. Project code is not indexed as documents

**Status:** accepted.

**Decision.** Agents read the repo with tools in the sandbox; for large repos — symbol indexing on tree-sitter.

**References:** FR-KB-3.

---

## ADR-008. LLM Gateway in Go

**Status:** accepted.

**Decision.** Own Go service with provider adapters, unified tool calling, fallback, token and cost accounting.

**References:** NFR-9, FR-OPS-2.

---

## ADR-009. Sandboxes on gVisor (v1) → Firecracker/Kata (v2)

**Status:** accepted.

**Decision.** Kernel isolation; no secrets inside; network allowlist via egress-proxy.

**References:** NFR-1, NFR-5.

---

## ADR-010. Separation of control, execution, and data planes

**Status:** accepted.

**Decision.** Strict contracts (API, events) between planes.

---

## ADR-011. Platform ontology (SLP) versioned as API

**Status:** accepted.

**Decision.** SLP — public contract; semver; instance migrations; old runs read their version.

**References:** FR-SEM-1, FR-SEM-6.

---

## ADR-012. ADR as a first-class artifact

**Status:** accepted.

**Decision.** ADR — Markdown (MADR), versioned in Git, indexed in KB, linked to SLPr/AM.

**References:** FR-ARCH-7, FR-KB-4.

---

## ADR-013. Autonomy levels R0–R3

**Status:** accepted.

**Decision.** R0 (read) — auto; R1 (local write) — auto; R2 (significant) — per profile; R3 (irreversible) — always human approval.

**References:** FR-OPS-1, NFR-5.

---

## ADR-014. Semantic slice in agent context

**Status:** accepted.

**Decision.** Agent Runtime requests a subgraph of depth 1–2 around task entities before the loop and includes it in context as JSON-LD.

**References:** FR-SEM-4, FR-SEM-5.

---

## ADR-015. Unified service name `ai-sdlc-factory`

**Status:** accepted.

**Decision.** Name `ai-sdlc-factory` used uniformly: platform name, `apiVersion`, ontology namespace, root directory, container prefix.

---

## ADR-016. AST code indexing and drift detection

**Status:** accepted.

**Decision.** Worker `semantic-indexer` parses code via tree-sitter on every commit. Worker `drift_detector` compares the actual dependency graph with the approved AM.

**References:** FR-ARCH-6, FR-SEM-2, NFR-10.

---

## ADR-017. Self-correction loop for architectural diagrams

**Status:** accepted.

**Decision.** Before saving a diagram, the Go validator performs 4 steps: DSL syntax, node existence in AM, contract conflicts, forbidden dependencies. Maximum 3 iterations.

**References:** FR-ARCH-2, FR-ARCH-3, FR-ARCH-4, ADR-006.

---

## ADR-018. Switching observability via deployment profiles

**Status:** accepted.

**Decision.** Langfuse remains part of the architecture under the `observability` profile. `local-dev` does not activate observability. OTel Collector routes: always to Jaeger, in `local-full` — additionally to Langfuse.

**References:** FR-OPS-2, NFR-4, NFR-7, NFR-8.

---

## ADR-019. Migration to PostgreSQL 18

**Status:** accepted.

**Context.** PostgreSQL 17 lacks `uuidv7()` function (not part of the core). M0 migrations use `uuidv7()` as a native function, which contradicts actual PG17 behavior (tests failed with `ERROR: function uuidv7() does not exist`).

**Decision.** Upgrade target PostgreSQL version to 18. In PG 18, `uuidv7()` is a native core function, aligning with property graph architecture and ADR-003.

**Consequences.**
- ✅ Native `uuidv7()` without extensions.
- ✅ +33% B-tree index performance on UUIDv7 (timestamp-based ordering).
- ✅ Compatibility with `pgvector/pgvector:pg18`.
- ⚠️ Requires updating images in Compose, Dockerfiles, CI.
- ⚠️ ADR-003, ADR-003b unchanged (property graph remains).

**References:** SYSTEM_DESIGN.md §12, ONTOLOGY_SPEC.md §6.1.

---

## ADR-020. Replacing MinIO with Silo

**Status:** accepted.

**Context.** Official MinIO Docker images (`minio/minio`, `quay.io/minio/minio`) removed from public registries. The project lost its S3-compatible storage. A replacement compatible with existing S3 API, `MINIO_*` variables, and data is required.

**Decision.** Use **Silo** (`docker.io/pgsty/silo`) — a community fork of MinIO, maintained by Pigsty.

- S3 API, SigV4, SDK, presigned URL — unchanged.
- `MINIO_*` env vars, `/minio/*` routes, `x-minio-*` headers, `minio_*` metrics — unchanged.
- Data format (`.minio.sys`, erasure coding, object layouts) — compatible.
- Client `mcli` (instead of `mc`).
- Healthcheck: `silo healthcheck ready`.

**Consequences.**
- Applications (`api`, `worker`, `knowledge`) require no code changes.
- Compose files updated: `minio` → `silo`, `minio-init` → `silo-init`.
- Documentation updated.
- Volume `minio-data` preserved (data compatible, no migration required).
- If Silo becomes unavailable in the future — can revert to MinIO (if image available) or switch to another S3-compatible service (e.g., RustFS) with minimal changes.

**References:** Issue #3, ADR-018.

---

## ADR-021. LLM Gateway: routing, fallback per (provider, model), cost attribution

**Status:** accepted.

**Context.**

LLM Gateway must route requests by capability profiles (`code-strong`, `reasoning-strong`, etc.), provide fallback on failures, and account for cost for NFR-9. Naive design leads to three problems:

1. **Floating alias** (`claude-sonnet-4-latest`) can be repinned by the provider, silently changing the profile contract. An agent expecting one behavior will get another without warning.
2. **`404` on one model** does not mean the whole provider is unavailable. Parking the whole provider on `404` — turning a deterministic error (config defect) into a flapping signal.
3. **Retries and mid-stream failover** within a single logical call must not triple `cost_usd` — otherwise NFR-9 overstates spend by the amount of retries.

**Decision.**

1. **Profiles pin the version.** `model_profiles` stores `model_id` with a specific version (`claude-sonnet-4-20250101`), not an alias (`-latest`). Model change is an R2 action (PR to `model_profiles`). The actual table is `llm_model_profiles` (global) to avoid conflict with the workspace-scoped `model_profiles` from migration 0001.
2. **Fallback per `(provider, model)`.** Health tracking is per `(provider, model_id)` pair. Error classification:
   - `404` (model not found) → config defect, no cooldown, alert.
   - `401` / `403` → auth defect, no cooldown, alert.
   - `400` → bad request, no cooldown, log.
   - `429` / `5xx` / `timeout` → availability, cooldown with exponential backoff (60s → 4h max, jitter). After cooldown — a probe request.
3. **Cost at two levels.**
   - `llm_logical_calls` — one row per logical request (attributed by `run_id`/`task_id`).
   - `llm_attempts` — N rows per HTTP attempt (actual spend).
   - `llm_logical_calls` stores `final_cost_usd` (successful attempt = what produced the result) and `total_cost_usd` (sum of all attempts = actual spend).

**Consequences.**

- NFR-9 (cost) is correctly accounted: attribution by logical ID is not doubled by retries.
- Model change via alias cannot silently change profile behavior.
- `404` does not trigger cascading cooldown of the whole provider.
- DB schema is more complex: two tables instead of one (`llm_calls` → `llm_logical_calls` + `llm_attempts`). Historical migration `0007_init_audit_and_cost` is not rewritten; separation is introduced by a new migration.
- Streaming — in v2; on mid-stream failover we will capture partial usage by `usage.completion_tokens` from the provider, not by the client counter.

**References:** FR-OPS-2, NFR-9, ADR-004, ADR-008, ADR-013, `SYSTEM_DESIGN.md` §8.5, §11.4, Issue #73.

---

## ADR-022. Selection of `mark3labs/mcp-go` for Tool Gateway (MCP proxy)

**Status:** accepted.

**Context.**
Tool Gateway must provide agents with a single MCP endpoint, federate multiple upstream MCP servers, apply OPA policies (R0–R3), audit calls, and perform credential brokering via Vault. A "from scratch" Go implementation would take months and require constant maintenance of the evolving MCP spec. Alternatives considered:

- **AgentGateway** (Rust, Apache 2.0) — powerful data plane, but overkill: LLM-routing and CEL-RBAC are not needed for the MCP layer; introduces Rust into a Go monorepo.
- **IBM ContextForge** (Python, Apache 2.0) — full enterprise functionality (OAuth, RBAC, admin UI), but ~150K lines of code; porting to Go is not worthwhile, running a separate Python service complicates operations.
- **Official `modelcontextprotocol/go-sdk`** — Tier 1, but API still evolves; lacks a ready `ProxyTool` mechanism for proxying upstream servers.
- **`github.com/mark3labs/mcp-go`** — community SDK, MIT, ~9.2k stars, active development; supports stdio, SSE, Streamable HTTP; **contains `ProxyTool`** and is used in the reference `mcpproxy-go` for proxying MCP servers. At the time of decision, stable version was `v1.1.1`.

**Decision.**
Use **`github.com/mark3labs/mcp-go`** as the library for MCP server and MCP client in `apps/tool-gateway`. Own Go layer adds:

- loading tools from `packages/contracts/mcp-schemas/*.json`;
- OPA check before every call (R0–R3 levels per ADR-013);
- audit with hash chain;
- credential brokering via Vault/OpenBao (agent does not see tokens);
- proxying calls to upstream MCP servers via `ProxyTool`.

Library version is pinned in `go.mod` (at time of writing — `v1.1.1` or later stable).

**Consequences.**
- **Pros:** Go-native stack, MIT license (compatible with AGPLv3), ready transports (stdio/SSE/Streamable HTTP), `ProxyTool` for federation, active community, easy integration with OPA and Vault.
- **Cons:** dependency on a community project; if migration to official `modelcontextprotocol/go-sdk` is needed, an adapter is required (protocol boundary is small, estimated ~1 day).
- **Limitations:** Tool Gateway does not implement its own LLM-routing (LLM Gateway does that per ADR-021); AgentGateway and ContextForge are not used.

**References:** ADR-005 (MCP as tool protocol), ADR-008 (LLM Gateway in Go), ADR-013 (R0–R3 levels), ADR-021 (LLM Gateway routing/fallback/cost), NFR-5 (security), NFR-6 (extensibility), FR-TEAM.

---

## ADR-023. Sandbox isolation boundary: docker-socket-proxy, egress allowlist, and gVisor

**Status:** accepted.

**Context.**
Implementing the Sandbox Manager (Issue #13) requires a secure boundary between the service and the host's container runtime. If the Sandbox Manager mounts `/var/run/docker.sock` directly, a compromise of the service (e.g., via a malicious payload in `sandbox.exec`) grants the attacker full control over the host's Docker daemon, violating NFR-1 (isolation) and NFR-5 (security).

Additionally, the sandbox containers must be able to fetch dependencies (Go, npm, pip, git) while remaining protected from exfiltration. Using `network_mode: none` breaks normal build workflows, while leaving them on an unrestricted network creates a prompt-injection exfiltration risk.

**Decision.**

1. **Docker Socket Boundary:**
   - The Sandbox Manager **MUST NOT** mount `/var/run/docker.sock`.
   - The Sandbox Manager interacts with the Docker daemon exclusively via `docker-socket-proxy` (e.g., `tecnativa/docker-socket-proxy`).
   - The proxy operates with a **default-deny** policy. Only specific endpoints are allowed:
     - `CONTAINERS=1` (create, start, stop, exec, remove)
     - `POST=1` (required for container creation)
     - `EXEC=1`
     - `VERSION=1` (required by Docker API version negotiation)
     - All other endpoints (`IMAGES`, `VOLUMES`, `NETWORKS`, `BUILD`, `SYSTEM`, `SWARM`, `AUTH`) are **denied** (`0`).

2. **Network Boundary (Egress):**
   - Sandbox containers are attached to a dedicated bridge network, not `none`.
   - Containers are injected with `HTTP_PROXY` and `HTTPS_PROXY` environment variables pointing to an `egress-proxy` (Squid or Envoy).
   - The egress proxy enforces an allowlist of destinations (e.g., `proxy.golang.org`, `registry.npmjs.org`, `pypi.org`, `github.com`).
   - All requests through the egress proxy are logged for audit.
   - *Note:* In `local-dev`, if the egress proxy is not deployed, `network_mode: none` is permitted for strict isolation, but this must be explicitly documented as a dev-only limitation.

3. **Runtime Isolation:**
   - In production (and CI), sandbox containers **MUST** use the `runsc` (gVisor) runtime to prevent kernel escapes.
   - In `local-dev` (especially on Windows/macOS where `runsc` is unavailable), the system MAY fall back to `runc`, but **MUST** log a loud warning: `WARN: gVisor (runsc) not available. Falling back to runc. Do not use with untrusted code.`

4. **Container Constraints:**
   - Non-root user (`1000:1000`).
   - `ReadonlyRootfs: true` (with tmpfs for `/tmp` and `/workspace`).
   - Resource limits: CPU, Memory, Pids (to prevent fork bombs).
   - No secrets inside the container; credential brokering is handled by Tool Gateway upstream.

**Implementation defaults (v1, subject to future revision):**
- Disk limit per sandbox: 2 GB (`SANDBOX_DISK_LIMIT_BYTES` env override).
- `sandbox.exec` requires `task_run_id` (uuid). Workspace path is resolved server-side from `task_runs.workspace_path` and validated against `X-Workspace-ID`. The agent never passes a host path.

**Consequences.**
- **Pros:** Defense in depth. A compromise of Sandbox Manager does not lead to host root access. Egress is auditable and restricted. gVisor provides a strong barrier against kernel exploits.
- **Cons:** Additional infrastructure (`docker-socket-proxy`, `egress-proxy`). Slightly more complex `docker-compose.yml`. Local development on Windows/macOS runs with weaker isolation (`runc`).
- **Rejected alternatives:**
  - *Direct `/var/run/docker.sock` mount:* Too risky (root compromise).
  - *`network_mode: none`:* Breaks package installation and git clone.
  - *Podman rootless:* Incompatible with the existing `docker compose` stack.
  - *Runner on host (systemd):* Over-engineering for v1; breaks Docker Compose portability.

**References:** Issue #13, NFR-1, NFR-5, ADR-009 (Sandbox gVisor), ADR-010 (Plane isolation).

---

## ADR-024. Data-only migrations require a reversible schema change

**Status:** accepted.

**Context.**
Migration 0015 (seed providers) is the first data-only migration in the repository: it inserts rows into `llm_model_profiles` without changing schema. The existing test policy (`TestDownSQLDropTableIfExists`) requires every `.down.sql` to contain a reversible schema change (`DROP TABLE IF EXISTS` or `DROP INDEX IF EXISTS`). A pure `DELETE FROM` cannot satisfy this policy without weakening the test, and weakening tests is R3 (`AGENTS.md` rule 4).

**Decision.**
Every data-only migration MUST contain a reversible schema change that is used by production code. For migration 0015, this is a partial index used by `catalog.Load`:

```sql
CREATE INDEX IF NOT EXISTS idx_llm_model_profiles_enabled
  ON llm_model_profiles (profile_name, priority)
  WHERE enabled = true;
```

This index is:
- directly useful for the actual query (`WHERE enabled = true ORDER BY profile_name, priority`);
- reversible (`DROP INDEX IF EXISTS` in `.down.sql`);
- a genuine schema change that satisfies `TestDownSQLDropTableIfExists` without weakening it.

If a future data-only migration cannot justify a useful schema change, its design is questioned before implementation. Either:
- the migration is folded into a feature migration (with a real schema change), or
- the test policy is amended via a new ADR.

**Consequences.**
- No weakening of the test policy.
- Data-only seeds are reversible and have a performance rationale.
- Future data migrations must follow this pattern.

**References:** ADR-002, `AGENTS.md` rule 4 (no weakening tests), Issue #113, Issue #110.

---

## ADR-025. Model pinning exceptions for providers without version pinning

**Status:** accepted.

**Context.**
ADR-021 requires `model_id` in `llm_model_profiles` to be a pinned version (e.g., `claude-sonnet-4-20250101`), not a floating alias (`-latest`). This protects the capability contract from silent changes by the provider.

However, DeepSeek and Groq do not expose pinned model versions — only stable aliases (`deepseek-chat`, `deepseek-reasoner`, `llama-3.3-70b-versatile`, `llama-3.1-8b-instant`). These aliases are documented by the providers as stable identifiers, but they can change underlying model weights without notice.

**Decision.**
Pinning is required where the provider supports it. For providers that do not expose pinned versions, the stable alias is used as the `model_id`, and the following applies:
- The alias is treated as an opaque stable identifier.
- The risk of capability drift is documented in `Known Limitations` of the PR that adds the provider.
- Regression evals (per role) MUST run when a provider's alias changes behavior — a follow-up for M2 once evals land.
- If a provider later exposes pinned versions, the `model_id` is updated to the pinned form via a new migration.

Currently affected providers: DeepSeek, Groq. Any future provider that does not support pinning falls under this ADR.

**Consequences.**
- DeepSeek and Groq can be used without violating ADR-021.
- The risk of capability drift is accepted and documented.
- Evals become a mandatory control once they exist (M2).

**References:** ADR-004, ADR-021, Issue #113.

---

## Document changes

| Version | Date | Changes |
|---|---|---|
| 1.8 | 30.10.2026 | Added ADR-024 (data-only migrations) and ADR-025 (pinning exceptions). |
| 1.7 | 27.10.2026 | Added ADR-023 (Sandbox isolation boundary) (#101) |
| 1.6 | 22.10.2026 | Added ADR-022 (selection of `mark3labs/mcp-go` for Tool Gateway) (#83) |
| 1.5 | 22.10.2026 | Added ADR-021 (LLM Gateway: routing, fallback, cost attribution) (#73) |
| 1.4 | 19.10.2026 | Added ADR-020 (MinIO → Silo) |
| 1.3 | 17.10.2026 | Added ADR-019 |
| 1.2 | 16.10.2026 | Added ADR-018 |
| 1.1 | 16.10.2026 | Added ADR-016, ADR-017 |
| 1.0 | 16.10.2026 | Initial ADR registry ADR-001…ADR-015 |
