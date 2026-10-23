# AGENT_KICKOFF — converting documentation into GitHub Issues

**Version 1.1 · 21.10.2026 · for AI agent executor**

This document describes how to convert the `ai-sdlc-factory` architectural documentation into a structured plan of work in GitHub Issues.

---

## 0. Agent role

You are the **Principal Technical Program Manager** for the `ai-sdlc-factory` project. You do not write code within this task. Your output is a set of milestones, labels, issues, and their relationships.

---

## 1. Mandatory reading

Read in the specified order:

| # | File | What to extract |
|---|---|---|
| 1 | `docs/README.md` | Documentation navigation |
| 2 | `docs/architecture/SYSTEM_DESIGN.md` | Requirements (FR-*, NFR-*), principles, delivery stages (§14.1), monorepo structure (§15), file generation tactics (§16.6) |
| 3 | `docs/architecture/ADR.md` | 18 architecture decisions (ADR-001…ADR-018) |
| 4 | `docs/architecture/ONTOLOGY_SPEC.md` | Semantic layer: SLP, SLPr, AM |
| 5 | `docs/architecture/SDLC_STATE_MACHINE.md` | Task states, gates, blocking conditions |
| 6 | `docs/architecture/MCP_TOOLS.md` | Agent tools by role |
| 7 | `docs/architecture/diagrams/README.md` | C4 diagram set |
| 8 | `deploy/compose/README.md` | Deployment profiles `local-dev` / `local-full` |

After reading, produce a concise report (10–15 lines): key goals, stack, key ADRs, delivery stages.

---

## 2. What to produce

Your output is four artifacts in GitHub:

1. **Milestones** — one per each delivery stage from `SYSTEM_DESIGN.md` §14.1 (Stages 0–6).
2. **Labels** — standardized set (see §4).
3. **Issues** — one or more tasks per stage.
4. **Relationships** — dependencies between issues, milestone binding, labels, links to FR/NFR/ADR.

---

## 3. Decomposition rules

### 3.1 Source of structure

The basis is **delivery stages** from `SYSTEM_DESIGN.md` §14.1. Each stage = one **milestone**.

### 3.2 Issue size

- **1 issue = 0.5–3 days of work** for one developer.
- If the task is larger — split it.
- If the task is less than 2 hours — merge with a neighbor.
- **Spike tasks** — a separate type, with explicit time-box.

### 3.3 What is mandatory in each issue

- Title: `<area>: <short action>`
- Links to FR-* / NFR-* from `SYSTEM_DESIGN.md` §3
- Links to ADR-* from `ADR.md`
- Link to `SYSTEM_DESIGN.md` section — **only actually existing** (see §3.4)
- Acceptance criteria in Given/When/Then format
- Test plan
- Complexity estimate (`size:S` / `size:M` / `size:L`)
- Milestone and labels

### 3.4 Section reference validation

Before creating an issue, verify the referenced section exists:

```bash
git grep -n "^#+ <N>\." -- docs/architecture/SYSTEM_DESIGN.md
```

If the subsection does not exist — **do not invent a number**. Either reference the parent section (§12 instead of §12.2), or create a spike-issue per `AGENTS.md` rules ("What to do when finding a documentation gap").

---

## 4. Labels

### 4.1 By work type

| Label | Color | Meaning |
|---|---|---|
| `type:feature` | `#0E8A16` | New feature |
| `type:infra` | `#5319E7` | Infrastructure, Compose, migrations |
| `type:contract` | `#1D76DB` | OpenAPI, JSON Schema, MCP schemas |
| `type:test` | `#FBCA04` | Tests only |
| `type:docs` | `#0075CA` | Documentation |
| `type:spike` | `#D4C5F9` | Investigation, time-box |
| `type:refactor` | `#BFD4F2` | Refactoring |
| `type:security` | `#B60205` | Security |

### 4.2 By area

| Label | Area |
|---|---|
| `area:infra` | Compose, migrations |
| `area:semantic` | Semantic Service, SLP, SLPr, AM |
| `area:architecture` | Architect engine, Diagram Service, ADR |
| `area:orchestration` | Temporal, workflow, Agent Runtime |
| `area:llm-gateway` | LLM Gateway, profiles, providers |
| `area:tool-gateway` | MCP, tool registry |
| `area:sandbox` | Sandbox Manager, gVisor |
| `area:knowledge` | Knowledge Service, indexer, search |
| `area:frontend` | Web UI |
| `area:observability` | OTel, Jaeger, Langfuse |
| `area:registry` | Registry, roles, teams |

### 4.3 By priority

| Label | Meaning |
|---|---|
| `p0` | Blocker |
| `p1` | High |
| `p2` | Medium |

### 4.4 By size

| Label | Estimate |
|---|---|
| `size:S` | up to 0.5 day |
| `size:M` | 0.5–1.5 days |
| `size:L` | 1.5–3 days |

### 4.5 Special

| Label | When to set |
|---|---|
| `blocked` | Has an open dependency |
| `needs-approval` | Requires owner approval |
| `r3` | Involves irreversible actions |
| `security-critical` | Requires security review |

---

## 5. Issue template

```markdown
## Context

<1–3 sentences. What we are doing and why.>

## References

- Requirements: `FR-XXX`, `NFR-XXX`
- ADR: `ADR-XXX`
- Section: `docs/architecture/SYSTEM_DESIGN.md` §X.Y
  > ⚠️ **Warning:** Only reference actually existing sections. Verify with `git grep -n "^##" -- docs/architecture/SYSTEM_DESIGN.md`. If a subsection does not exist — reference the parent section (e.g., §12 instead of §12.2) or create it in the current task. Do not leave broken links.

## What to do

- [ ] Item 1
- [ ] Item 2

## Acceptance criteria

- [ ] Given … When … Then …

## Test plan

- [ ] Unit tests
- [ ] Integration tests
- [ ] Contract tests
- [ ] E2E
- [ ] New code coverage ≥ 80%

## Dependencies

- Blocked by: #<issue>
- Blocks: #<issue>

## Estimate

- Size: S | M | L
- Priority: p0 | p1 | p2

## Definition of Done

- [ ] Code in PR, green CI
- [ ] Tests green, coverage ≥ 80%
- [ ] Logs without secrets
- [ ] Documentation updated
```

---

## Document changes

| Version | Date | Changes |
|---|---|---|
| 1.1 | 21.10.2026 | Added §3.4 (section reference validation) and warning in issue template (#64) |
| 1.0 | 16.10.2026 | Initial specification |
