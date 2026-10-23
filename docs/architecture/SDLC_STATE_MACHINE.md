# SDLC State Machine — ai-sdlc-factory

**Version 1.1 · 16.10.2026**

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

## 3. Transitions and conditions

### 3.9 InDev → CodeReview

Condition: PR opened; CI green; new code coverage >= 80%; Conventional Commits observed.

### 3.10 CodeReview → QA

Condition: approval from reviewer (not PR author); max_rounds=3.

### 3.12 QA → Build

Condition: all test levels green; coverage thresholds met; critical/high bugs closed.

### 3.15 Release → Deploy

Condition: `human_approval` gate for prod; release notes in KB; DB migrations verified.

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
| 1.1 | 16.10.2026 | Added conformance_ok, semantic_valid gates |
| 1.0 | 16.10.2026 | Initial specification |
