# SDLC State Machine — ai-sdlc-factory

**Version 1.2 · 26.11.2026**

## 1. Overview

```
[*] --> Idea --> Research --> Requirements --> RequirementsReview
RequirementsReview --> Design --> Architecture --> ArchitectureReview
ArchitectureReview --> Planning --> InDev --> CodeReview
CodeReview --> QA --> Build --> Release --> Deploy --> Monitoring
Monitoring --> [*]
Monitoring --> Research (feedback loop)
```

## 2. States

| State | Responsible | Input artifacts | Output artifacts |
|---|---|---|---|
| `Idea` | PM | owner goal | problem description |
| `Research` | Researcher | hypothesis | research report |
| `Requirements` | PM + Analyst | research report | PRD, SLPr v1, user stories |
| `RequirementsReview` | Reviewer + human | PRD, SLPr v1 | approve / reject |
| `Design` | Designer | PRD | mockups, prototypes |
| `Architecture` | Architect | PRD, SLPr | AM v1, C4 diagrams, ADR |
| `ArchitectureReview` | Reviewer + Tech Lead | AM, ADR, diagrams | approve / reject |
| `Planning` | PM + Architect | AM, PRD | epics, tasks, sprint |
| `InDev` | Developer | tickets | branches, PRs, commits |
| `CodeReview` | Reviewer + Architect | PR | approve / changes_requested |
| `QA` | QA / SDET | PR + acceptance criteria | autotests, coverage report |
| `Build` | DevOps | merged PR | image, RC, SBOM |
| `Release` | Release Manager + human | RC | approve / reject |
| `Deploy` | DevOps | approve | deployed environment |
| `Monitoring` | DevOps + PM | metrics | reports, feedback tasks |

### Role I/O contract (v1)

Each role declares `spec.inputs` and `spec.outputs` as an array of
`{name, artifact_type, required, schema_ref?}` (see `packages/contracts/schemas/role.schema.json`).
Each role also declares `spec.tools_policy` (`read_only` | `write_capable`) to classify
its outbound capability for anti-injection enforcement (MCP_TOOLS.md §1, SYSTEM_DESIGN.md §10.2).
For write-capable roles, an incoming handoff is sanitized before the act phase is allowed to use
its full tool set.

- `artifact_type` refers to artifacts listed in the table above. In v1 it is a free string
  validated by pattern `^[a-z][a-z0-9_-]*$`; in v2 it will become an enum sourced from
  SLP `ArtifactType` (ONTOLOGY_SPEC.md §3.1, FR-SEM).
- `required` defaults to `true`. Set `false` for optional artifacts.
- `schema_ref` is optional. When present, handoff validation (SYSTEM_DESIGN.md §8.4, #21)
  uses it as `expected_output_schema` for the recipient.

## 3. Transitions and conditions

The authoritative machine-readable table is
`packages/contracts/state-machine/sdlc_transitions.json`. The table below
mirrors it; the migration contract test fails when the two drift. Section IDs
are stable references: the four pre-existing IDs (3.9, 3.10, 3.12, 3.15) are
preserved and the remaining IDs fill the gaps, so the numeric order is not the
reading order. The reading order follows the state order of section 2.

Every transition is executed by the run workflow (`RunWorkflow`), which is the
only writer of `runs.status` (ADR-027). A transition that does not satisfy its
condition or gate keeps the run in its current state, writes an audit event
and never moves the run. Every transition is additionally bounded by the
StageRun deadline and the circuit breakers of section 7.

| ID | Transition | Initiator | Condition | Gate | On error |
|---|---|---|---|---|---|
| 3.1 | Idea → Research | run workflow | Run started with the owner goal; the problem description is captured (§2). | — | Stay in Idea; audit; escalate on `max_steps`/`max_cost_usd` (§6). |
| 3.2 | Research → Requirements | run workflow | Research report produced from the hypothesis; findings recorded (§2). | — | Stay in Research; audit; escalate on `max_steps`/`max_cost_usd` (§6). |
| 3.3 | Requirements → RequirementsReview | run workflow | PRD, SLPr v1 and user stories produced (§2). | — | Stay in Requirements; audit; escalate on `max_steps`/`max_cost_usd` (§6). |
| 3.4 | RequirementsReview → Design | human approval | `requirements_approved` passes: approve plus resolved comments (§4). A rejection returns the notes to the PM. | `requirements_approved` | Stay in RequirementsReview; audit; timeout blocks; no escalation. |
| 3.5 | Design → Architecture | run workflow | Mockups and prototypes produced (§2). | — | Stay in Design; audit; escalate on `max_steps`/`max_cost_usd` (§6). |
| 3.6 | Architecture → ArchitectureReview | run workflow | AM v1, C4 diagrams and ADR produced (§2); the AM change passes the `semantic_valid` inference rules (§4). | `semantic_valid` | Stay in Architecture; audit; escalate on `max_steps`/`max_cost_usd` (§6). |
| 3.7 | ArchitectureReview → Planning | human approval | `architecture_approved` passes: approve plus a valid AM (§4). A rejection returns the notes to the architect. | `architecture_approved` | Stay in ArchitectureReview; audit; timeout blocks; no escalation. |
| 3.8 | Planning → InDev | run workflow | Epics, tasks and the sprint plan produced (§2). | — | Stay in Planning; audit; escalate on `max_steps`/`max_cost_usd` (§6). |
| 3.9 | InDev → CodeReview | external evidence | PR opened; `ci_green` passes: all CI checks green (§4); new code coverage >= 80%; Conventional Commits observed. | `ci_green` | Stay in InDev; audit; CI failure blocks (§5); escalate on `max_steps`/`max_cost_usd` (§6). |
| 3.10 | CodeReview → QA | run workflow (reviewer agent) | `review_approved` passes: approval from a reviewer that is not the PR author (§4); `max_rounds=3` (§7). Changes requested return the notes to the author. | `review_approved` | Stay in CodeReview; audit; escalate after `max_rounds` (§6). |
| 3.12 | QA → Build | run workflow | `qa_passed` passes: all test levels green, coverage thresholds met, critical/high bugs closed (§4). Critical defects or test failures block the run (§5). | `qa_passed` | Stay in QA; audit; critical defects or test failure block (§5); escalate on `max_steps`/`max_cost_usd` (§6). |
| 3.11 | Build → Release | external evidence | Image, RC and SBOM produced (§2); `sbom_signed` passes: SBOM present and image signed (§4). | `sbom_signed` | Stay in Build; audit; escalate on `max_steps`/`max_cost_usd` (§6). |
| 3.15 | Release → Deploy | human approval | `prod_approved` passes: human approve (R3); release notes in KB; DB migrations verified (§4). A rejection returns the notes to the release manager. | `prod_approved` | Stay in Release; audit; timeout blocks; no escalation. |
| 3.13 | Deploy → Monitoring | external evidence | `deploy_healthy` passes: healthcheck plus metrics normal (§4). | `deploy_healthy` | Stay in Deploy; audit; escalate on `max_steps`/`max_cost_usd` (§6). |
| 3.14 | Monitoring → Research | scheduled | Monitoring reports produced feedback tasks; the scheduled feedback review starts the next Research cycle (feedback loop of §1). | — | Stay in Monitoring; audit; no escalation. |

Error semantics in the table are the three dimensions of the contract:
`stay in state` (the run never moves on a rejected transition), `audit` (the
rejection is written to `audit_log`) and `escalate` (the condition that hands
the run to a human, per section 6). The machine-readable form encodes them as
`error.status`, `error.audit` and `error.escalation`.

## 4. Gates

| Gate | Type | Where applied | Criterion |
|---|---|---|---|
| `requirements_approved` | human_approval | RequirementsReview → Design | Approve + resolved comments |
| `architecture_approved` | human_approval | ArchitectureReview → Planning | Approve + valid AM |
| `ci_green` | auto | InDev → CodeReview | All CI checks green |
| `review_approved` | agent_review | CodeReview → QA | Approve from reviewer |
| `qa_passed` | auto | QA → Build | Tests green, coverage thresholds |
| `sbom_signed` | auto | Build → Release | SBOM + image signature |
| `prod_approved` | human_approval | Release → Deploy | Human approve (R3) |
| `deploy_healthy` | auto | Deploy → Monitoring | Healthcheck + metrics normal |
| `conformance_ok` | auto | On merge to main | Drift <= warning |
| `kb_published` | agent_review | Diagram → KB | Validator + review |
| `semantic_valid` | auto | All SLPr/AM changes | Inference rules §7 |

Implementation: `SDLCWorkflow` → `ChainWorkflow` → `WaitGate` (issue #126, #20; `SYSTEM_DESIGN.md §8.1`).

## 5. Blocking conditions

A task is **blocked** when:
1. Presence of `critical` defects in QA.
2. CI or test failure.
3. Missing approval from required gate.
4. Budget exceeded.
5. `drift` of `critical` level detected.
6. Presence of `stale` diagrams.
7. Prompt injection detected.

## 6. Escalation

Escalation to human when:
- `max_rounds` exceeded in review loop (default 3);
- `max_steps` or `max_cost_usd` exceeded;
- repeated actions detector triggered;
- 3 iterations of self-correction loop rejected.

## 7. Circuit breakers

| Circuit breaker | Default value |
|---|---|
| `max_rounds` in review loop | 3 |
| `max_steps` per TaskRun | 60 |
| `max_cost_usd` per TaskRun | 3.0 |
| `max_wall_minutes` per TaskRun | 45 |
| `deadline` per StageRun | 24 hours |

The idempotency cache (ADR-026) supports circuit-breaker recovery: when a transient provider failure triggers a retry, the cache ensures the provider is not double-billed (LLM) or the side effect not duplicated (Tool). See `SYSTEM_DESIGN.md` §8.5, §11.1.

## 8. Risk levels and autonomy

| Level | Actions | Gate |
|---|---|---|
| R0 | Read (KB, repo, web) | Automatic |
| R1 | Commit to working branch, comments, PR | Automatic |
| R2 | Merge to main, DB migrations, staging deploy | Per autonomy profile |
| R3 | Prod deploy, data deletion, permission changes | Always human approve |

## 9. Version snapshots for reproducibility

Each Run pins: `role_versions[]`, `team_version`, `process_version`, `model_profiles[]`, `knowledge_snapshot_id`, `slp_version`, `slpr_version`, `am_version`.

## Document changes

| Version | Date | Changes |
|---|---|---|
| 1.2 | 26.11.2026 | Completed section 3 with all 15 transitions and linked it to the machine-readable contract |
| 1.1 | 16.10.2026 | Added conformance_ok, semantic_valid gates |
| 1.0 | 16.10.2026 | Initial specification |
