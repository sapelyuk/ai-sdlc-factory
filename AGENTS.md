# Instructions for AI agent: ai-sdlc-factory

This file is read by AI agents (Codex, Cline, Continue, Aider, Claude Code,
Copilot, etc.) when working with the repository. It defines basic rules
of behavior and references the canonical project documentation.

## Main

You are working in the `ai-sdlc-factory` repository.
Before starting any task, read `CONTRIBUTING.md`.

## Basic rules

1. Any work follows the path: `issue → branch → PR → main`.
2. Never commit directly to `main`.
3. Never create a branch without an issue.
4. Never merge a PR without explicit owner approval.
5. Never weaken tests to get a green CI.
6. Never deviate from ADR without a new ADR.
7. Never invent requirements — ask the owner.

Direct pushes to `main` are blocked by GitHub branch protection. All changes go through PR.

## Language policy

**All content in this repository MUST be in English.** This is enforced by CI.
All PR descriptions, commit messages, and issue descriptions MUST be in English.

## Branch naming

`<type>/<issue-number>-<short-name>`

Types: `feat`, `fix`, `chore`, `docs`, `refactor`, `test`, `spike`.

## Commit messages

Conventional Commits with issue reference.

```
<type>(<scope>): <short description> (#<issue>)
```

## Where to find what

| Question | File |
|---|---|
| How to contribute | `CONTRIBUTING.md` |
| Architecture | `docs/architecture/SYSTEM_DESIGN.md` |
| ADR | `docs/architecture/ADR.md` |
| How to plan work | `docs/AGENT_KICKOFF.md` |
| Semantic layer | `docs/architecture/ONTOLOGY_SPEC.md` |
| States and gates | `docs/architecture/SDLC_STATE_MACHINE.md` |
| MCP tools | `docs/architecture/MCP_TOOLS.md` |
| Local startup | `deploy/compose/README.md` |
| Diagrams | `docs/architecture/diagrams/README.md` |

## Reading order before a task

1. Open the issue the task is linked to.
2. Read the entire issue body, including FR / NFR / ADR references.
3. Open the referenced section in `SYSTEM_DESIGN.md` — read it.
4. Open related ADRs — read them.
5. If anything is unclear — do not start, ask the owner.

## Stack

- Backend: **Go** 1.26+.
- Frontend: **TypeScript + React**.
- Orchestration: **Temporal** (Go SDK).
- Database: **PostgreSQL 18 + pgvector**.
- Event bus: **NATS JetStream**.
- Object storage: **Silo (S3)** (MinIO fork).
- Policies: **OPA (Rego)**.
- Secrets: **Vault / OpenBao**.
- Observability: **OpenTelemetry + Jaeger**; optionally **Langfuse**, **Prometheus**, **Grafana** under the `observability` profile.

## Naming

- Service name: `ai-sdlc-factory`.
- YAML `apiVersion`: `ai-sdlc-factory/v1`.
- Ontology namespace: `ai-sdlc-factory/product-domain@^1`.
- Root directory: `ai-sdlc-factory/`.
- Container and Helm release prefix: `ai-sdlc-factory-*`.

## Code rules

1. Contracts first. OpenAPI, JSON Schema, MCP schemas, events first; then implementation.
2. Tests — with code, not after. New code coverage ≥ 80%.
3. Do not delete or weaken tests to get a green CI.
4. Small PRs: ≤ 400 lines of diff where possible.
5. References to FR / NFR / ADR in PR description are mandatory.
6. Do not add dependencies without checking license, support, and vulnerabilities.
7. Versions are pinned. Do not use versions older than two weeks.
8. Security is not deferred: sandboxes without secrets and network, credential brokering, editing secrets in logs, audit, budgets, R3 gates.
9. Deployment profiles. When adding a new service, specify which profile it belongs to (`always` or `observability`), and update both Compose override files.
10. Naming. Any new name must be derived from `ai-sdlc-factory`.

**Prohibition on weakening checks:**
The agent has no authority to modify (weaken) CI configurations, linter rules, test coverage thresholds (coverage), `CODEOWNERS` files, or security policies (OPA/Rego) to make the build green. Changing these files is classified as risk level **R3** and always requires human confirmation (Human-in-the-loop). If a test fails, fix the code, not the test.

## What to do with ambiguity

Do not invent. Log the question in the issue comment, set the `needs-clarification` label, and continue with the safest option, explicitly stating the assumption. The owner will respond — remove the label.

## What to do when finding a documentation gap

1. Do not decide on your own.
2. Create a `spike` issue with a 2–4 hour time-box.
3. Describe: what is unclear, where in the documentation, what options are possible, what needs to be resolved.
4. Set labels `type:spike` and `needs-approval`.
5. Include the spike in the plan for the owner.

## What to do when deviating from ADR

1. Do not apply the deviation on your own.
2. Propose a new ADR with alternatives and consequences.
3. Wait for owner approval.
4. Only then apply it.

## Completed task report format

```
Task: #<N> <issue title>
Branch: <branch-name>
PR: <URL>
Commits: <N>
CI: <status>
New code coverage: <N>%
Documentation updated: yes / no / not required
Open questions: <list or "none">
```