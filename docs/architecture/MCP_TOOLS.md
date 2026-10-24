# Agent MCP tools — ai-sdlc-factory

**Version 1.2 · 20.10.2026**

## 1. General rules

- All tools are available through **Tool Gateway**.
- Every call passes through **Policy Engine**; for R2/R3 may require approval.
- Secrets are injected by Tool Gateway (credential brokering).
- Access rules: `Role.tools.allow` — whitelist; `Role.tools.deny` — blacklist, higher priority than `allow`.

**Role constraints (Anti-Injection):**
When assigning tools to a role (see `spec.tools` in YAML), the separation of capabilities rule MUST be observed. No role must simultaneously have `web.*` (untrusted content), `secrets.read` / `fs.*` (private data), and `git.push` / `deploy.*` / `tracker.write` (outbound channel). If a role requires crossing these domains, the task must be decomposed into two steps (e.g., "Research" → Handoff → "Implementation").

## 2. Schema file format

```json
{
  "role": "<role-name>",
  "schema_version": "ai-sdlc-factory/v1",
  "tools": [{
    "name": "<tool.name>",
    "description": "<description>",
    "input_schema": { "type": "object", "properties": { } },
    "output_schema": { "type": "object", "properties": { } },
    "risk_level": "R0|R1|R2|R3"
  }]
}
```

## 3. Tools by role

### 3.1 `pm_tools.json`

| Tool | Description | Risk |
|---|---|---|
| `pm.create_prd` | Create PRD | R1 |
| `pm.update_backlog` | Update backlog | R1 |
| `tracker.comment` | Comment on ticket | R1 |
| `kb.search` | Search in KB | R0 |

### 3.2 `researcher_tools.json`

| Tool | Description | Risk |
|---|---|---|
| `web.search` | Internet search | R0 |
| `web.fetch` | Page fetch | R0 |
| `research.summarize` | Source summary | R0 |
| `kb.write_draft` | Draft in KB | R1 |

### 3.3 `analyst_tools.json`

| Tool | Description | Risk |
|---|---|---|
| `slpr.propose_node` | Propose SLPr entity | R2 |
| `slpr.propose_edge` | Propose edge | R2 |
| `slpr.search` | Search SLPr | R0 |

### 3.4 `designer_tools.json`

| Tool | Description | Risk |
|---|---|---|
| `figma.read` | Read Figma file | R0 |
| `figma.write` | Edit frames | R2 |
| `design.export_tokens` | Export tokens | R1 |

### 3.5 `architect_tools.json`

| Tool | Description | Risk |
|---|---|---|
| `sem.am_read` | Read AM | R0 |
| `sem.am_write` | Edit AM | R2 |
| `diag.render` | Render DSL to SVG/PNG | R1 |
| `diag.validate` | Validate DSL | R1 |
| `diag.publish` | Publish to KB | R2 |
| `adr.propose` | Propose ADR | R2 |

**Forbidden:** `deploy.prod`, `secrets.read`, `git.push_protected`.

### 3.6 `developer_tools.json`

| Tool | Description | Risk |
|---|---|---|
| `git.clone` | Clone repo | R0 |
| `git.branch` | Create branch | R1 |
| `git.commit` | Commit | R1 |
| `git.push` | Push branch | R1 |
| `sandbox.exec` | Command in sandbox | R1 |
| `fs.read` | Read file | R0 |
| `fs.write` | Write file | R1 |

**Forbidden:** `deploy.prod`, `secrets.read`, `git.push_protected`.

### 3.7 `qa_tools.json`

| Tool | Description | Risk |
|---|---|---|
| `sandbox.exec` | Run tests | R1 |
| `test.run` | Unit/integration/e2e | R1 |
| `test.report` | Coverage report | R0 |
| `bug.open` | Open bug report | R1 |

### 3.8 `devops_tools.json`

| Tool | Description | Risk |
|---|---|---|
| `ci.trigger` | Trigger CI | R1 |
| `ci.status` | CI status | R0 |
| `deploy.staging` | Deploy to staging | R2 |
| `deploy.prod` | Deploy to prod | R3 |
| `deploy.rollback` | Rollback | R2 |

### 3.9 `reviewer_tools.json`

| Tool | Description | Risk |
|---|---|---|
| `git.diff` | PR diff | R0 |
| `git.read` | Reads file contents from the repository | R0 |
| `pr.review` | Review PR | R1 |
| `pr.approve` | Approve | R1 |

### 3.10 `security_tools.json`

| Tool | Description | Risk |
|---|---|---|
| `sast.run` | Static analysis | R1 |
| `dep.scan` | Dependency scan | R1 |
| `secret.scan` | Secret scan | R1 |

## 4. Universal tools

| Tool | Description | Risk |
|---|---|---|
| `kb.search` | Search in KB | R0 |
| `sem.query` | Query semantic graph | R0 |
| `tracker.comment` | Comment | R1 |
| `agent.handoff` | Handoff package | R1 |

## 5. Tool Gateway mapping

1. Load schemas from `packages/contracts/mcp-schemas/`.
2. Filter tools by `Role.tools.allow` and `Role.tools.deny`.
3. For each call: check permissions, apply policy, inject secrets, execute call, log.

## Document changes

| Version | Date | Changes |
|---|---|---|
| 1.2 | 20.10.2026 | Added role constraints (Anti-Injection) |
| 1.1 | 16.10.2026 | Added sem.*, diag.*, adr.*, slpr.* |
| 1.0 | 16.10.2026 | Initial registry |
