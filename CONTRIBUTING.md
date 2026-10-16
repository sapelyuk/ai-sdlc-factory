# Contributing to ai-sdlc-factory

Thank you for your interest in contributing to `ai-sdlc-factory`! This document outlines the process for contributing to the project.

## Table of Contents

- [Code of Conduct](#code-of-conduct)
- [Getting Started](#getting-started)
- [Development Workflow](#development-workflow)
- [Submitting Changes](#submitting-changes)
- [Coding Standards](#coding-standards)
- [Documentation](#documentation)
- [Testing](#testing)
- [Security](#security)

## Code of Conduct

This project follows the [Contributor Covenant Code of Conduct](https://www.contributor-covenant.org/). By participating, you agree to uphold a welcoming and inclusive environment for everyone.

## Getting Started

### Prerequisites

- Go 1.23+
- Node.js 20+ (for frontend)
- Docker and Docker Compose
- PostgreSQL 16 (or use Docker Compose)
- `gh` CLI for GitHub operations

### Local Setup

```bash
# 1. Clone the repository
git clone https://github.com/sapelyuk/ai-sdlc-factory.git
cd ai-sdlc-factory

# 2. Start the development stack
cp deploy/compose/.env.example deploy/compose/.env.local-dev
make up-local

# 3. Verify services are running
make ps-local

# 4. Run migrations (if needed)
make migrate-up
```

### Available Commands

| Command | Description |
|---|---|
| `make up-local` | Start local-dev profile (~10 GB RAM) |
| `make up-full` | Start local-full profile (~30 GB RAM, with observability) |
| `make down-local` | Stop local-dev profile |
| `make down-full` | Stop local-full profile |
| `make ps-local` | Show running services |
| `make logs-local` | Show service logs |
| `make rebuild-local` | Rebuild and restart services |
| `make clean` | Remove all containers and volumes |
| `make migrate-up` | Run database migrations |
| `make migrate-down` | Rollback database migrations |

## Development Workflow

### Branch Naming

- `feat/<issue-number>-<description>` — new features
- `fix/<issue-number>-<description>` — bug fixes
- `docs/<issue-number>-<description>` — documentation updates
- `refactor/<issue-number>-<description>` — code refactoring
- `chore/<issue-number>-<description>` — maintenance tasks

### Commit Convention

Use [Conventional Commits](https://www.conventionalcommits.org/):

```
<type>(<scope>): <description>

<optional body>

Refs #<issue-number>
```

Types: `feat`, `fix`, `docs`, `style`, `refactor`, `test`, `chore`, `ci`, `build`

Examples:
```
feat(semantic): implement traversal API for semantic subgraph
fix(api): resolve JWT token expiration handling
docs: update ADR-003 with property graph schema
```

### Pull Request Process

1. **Create an issue** describing the change
2. **Create a branch** from `main`
3. **Implement changes** following coding standards
4. **Write tests** — minimum 80% code coverage
5. **Run CI locally** — `docker compose up --build`
6. **Push and open PR** with proper template
7. **Address review feedback**
8. **Squash merge** when approved

### PR Template

Every PR must include:
- Context (2-3 sentences)
- References to FR/NFR/ADR identifiers
- List of changes
- Testing instructions
- Type of change (feature, fix, refactor, docs, infra, contract, test, security)
- Definition of Done checklist

## Coding Standards

### Go

- Follow [Uber Go Style Guide](https://github.com/uber-go/guide/blob/master/style.md)
- All public APIs must have godoc comments
- Use `go-playground/validator` for request validation
- Use dependency injection for service initialization
- Write table-driven tests

### TypeScript/React

- Use strict TypeScript mode
- Prefer functional components with hooks
- Use TanStack Query for server state
- Use Zustand for client state
- Write component tests with Vitest

### YAML Specifications

All YAML specs must use:
- `apiVersion: ai-sdlc-factory/v1`
- `kind: <ResourceKind>`
- `metadata: { name, version }`
- `spec: <specification>`

## Documentation

### Architecture Documentation

- All architecture changes require an ADR update
- Diagrams must be in draw.io format (`.drawio`)
- Export diagrams to SVG for GitHub embedding
- Update `docs/architecture/diagrams/README.md` when adding new diagrams

### Semantic Layer

- New entities must be added to SLP or SLPr ontology
- Use URI format: `ai-sdlc-factory/product-domain:<path>#<fragment>`
- Changes to SLP major version require human approval (R3)
- Changes to SLPr require review (R2)

## Testing

### Test Levels

1. **Unit tests** — test individual functions and methods
2. **Integration tests** — test service interactions using testcontainers
3. **Contract tests** — validate OpenAPI/JSON Schema/MCP schemas
4. **E2E tests** — test complete user workflows

### Running Tests

```bash
# Go tests
cd apps/api && go test -v -cover ./...

# Frontend tests
cd apps/web && pnpm test

# Integration tests
docker compose up --build && make test-integration
```

### Coverage Requirements

- Minimum 80% code coverage for all new code
- Coverage reports uploaded to Codecov
- Coverage gaps must be justified in PR description

## Security

### Secrets Management

- Never commit secrets, tokens, or credentials
- Use Vault/OpenBao for secret storage
- Use credential brokering in Tool Gateway
- Edit secret changes must be logged (never in plaintext)

### Sandbox Security

- Sandboxes must run under gVisor (runsc)
- No access to docker socket or cloud metadata
- Network egress must be allowlisted
- No secrets mounted in sandbox containers

### LLM Security

- Wrap untrusted data in explicit delimiters
- Implement prompt injection detection
- Log all LLM calls with cost tracking
- Enforce model profiles (local-private for restricted data)

### Risk Levels

| Level | Actions | Approval |
|---|---|---|
| R0 | Read operations (KB, repo, web) | Automatic |
| R1 | Local write (commit, comment, PR) | Automatic |
| R2 | Significant change (merge, migration, staging deploy) | Profile-based |
| R3 | Irreversible action (prod deploy, data deletion) | Human approval always |

## License

This project is licensed under AGPLv3. All contributions must be compatible with this license.

## Questions?

If you have questions about contributing, please open a GitHub Discussion or reach out to the maintainers.
