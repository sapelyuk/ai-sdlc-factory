# AI-SDLC Factory — Instructions for AI Agents

This document provides guidance for AI agents working on the `ai-sdlc-factory` project.

## Repository Structure

- `apps/` — Go microservices (api, worker, semantic, diagram, etc.)
- `packages/` — Shared contracts, ontology, policies, SDKs
- `backend/` — Legacy monolithic backend (deprecated, migrate to apps/)
- `frontend/` — React/TypeScript Web UI
- `deploy/` — Docker Compose and Kubernetes manifests
- `docs/` — Architecture documentation and ADRs
- `migrations/` — Database migrations (PostgreSQL, Graph)
- `catalog/` — Role, team, and process YAML specifications

## Coding Standards

### Go (Backend)

- Use Go 1.23+
- Follow [Uber Go Style Guide](https://github.com/uber-go/guide/blob/master/style.md)
- All public APIs must have godoc comments
- Use `chi` or `echo` for HTTP routing
- Use `go-playground/validator` for request validation
- Use `temporalio/go` SDK for durable workflows
- Use `nats-io/nats.go` for event streaming
- Use `open-telemetry/opentelemetry-go` for observability
- All new dependencies must be reviewed for license, maintenance, and CVEs

### TypeScript/React (Frontend)

- Use React 18+ with strict TypeScript
- Use Vite for build tooling
- Use Tailwind CSS + shadcn/ui for styling
- Use TanStack Query + Zustand for state management
- Use Vitest + Playwright for testing
- All API calls must go through `shared/api/httpClient`

## Commit Convention

Use [Conventional Commits](https://www.conventionalcommits.org/):

```
<type>(<scope>): <description>

<optional body>

Refs #<issue-number>
Closes #<issue-number>
```

Types: `feat`, `fix`, `docs`, `style`, `refactor`, `test`, `chore`, `ci`, `build`

## Pull Request Rules

1. Every PR must reference FR/NFR/ADR identifiers
2. Small PRs preferred — each PR should be reviewable in 15 minutes
3. CI must pass before merge
4. Use squash merge for all PRs
5. After merge: delete branch
6. Documentation must be updated in the same PR

## Security Rules

- Never commit secrets, tokens, or credentials
- Never commit `.env.local-dev` or `.env.local-full`
- Always use `.env.example` as template
- Sandboxes must not have access to secrets or docker socket
- All LLM calls must be logged with cost tracking
- R3 actions (prod deploy, data deletion) require human approval

## Semantic Layer Rules

- All new entities must be added to SLP or SLPr ontology
- Use URI format: `ai-sdlc-factory/product-domain:<path>#<fragment>`
- Changes to SLP major version require human approval (R3)
- Changes to SLPr require review (R2)
- All artifacts must reference semantic entities by URI

## Architecture Model Rules

- C4 diagrams must be in Structurizr DSL (primary), Mermaid, or PlantUML
- Diagrams must pass self-correction loop (max 3 iterations)
- All diagram nodes must exist in Architecture Model
- When AM changes, dependent diagrams become `stale`
- Drift detection may block PRs at critical level

## Deployment Profiles

- `local-dev`: ~10 GB RAM, no Langfuse/Prometheus/Grafana
- `local-full`: ~30 GB RAM, full observability stack
- `prod`: Kubernetes with managed PostgreSQL and S3

Always specify which profile a new service belongs to.

## Issue Creation

Every feature or bug fix starts with a GitHub Issue. Use templates in `.github/ISSUE_TEMPLATE/`.

Issue template fields:
- Context (1-3 sentences)
- References (FR-*, NFR-*, ADR-*)
- What to do (checklist)
- Acceptance criteria (Given/When/Then)
- Test plan (unit, integration, contract, e2e)
- Dependencies
- Size estimate (S/M/L)
- Priority (p0/p1/p2)
- Definition of Done
