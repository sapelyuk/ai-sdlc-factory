# SDLC State Machine — ai-sdlc-factory

**Версия 1.1 · 16.10.2026**

## 1. Общая схема

```
[*] --> Idea --> Research --> Requirements --> RequirementsReview
RequirementsReview --> Design --> Architecture --> ArchitectureReview
ArchitectureReview --> Planning --> InDev --> CodeReview
CodeReview --> QA --> Build --> Release --> Deploy --> Monitoring
Monitoring --> [*]
Monitoring --> Research (feedback loop)
```

## 2. Состояния

| Состояние | Ответственный | Входные артефакты | Выходные артефакты |
|---|---|---|---|
| `Idea` | PM | цель владельца | описание проблемы |
| `Research` | Researcher | гипотеза | отчёт исследования |
| `Requirements` | PM + Analyst | отчёт исследования | PRD, SLPr v1, user stories |
| `RequirementsReview` | Reviewer + человек | PRD, SLPr v1 | approve / reject |
| `Design` | Designer | PRD | макеты, прототипы |
| `Architecture` | Architect | PRD, SLPr | AM v1, C4-диаграммы, ADR |
| `ArchitectureReview` | Reviewer + Tech Lead | AM, ADR, диаграммы | approve / reject |
| `Planning` | PM + Architect | AM, PRD | эпики, задачи, спринт |
| `InDev` | Developer | тикеты | ветки, PR, коммиты |
| `CodeReview` | Reviewer + Architect | PR | approve / changes_requested |
| `QA` | QA / SDET | PR + критерии приёмки | автотесты, отчёт покрытия |
| `Build` | DevOps | merged PR | образ, RC, SBOM |
| `Release` | Release Manager + человек | RC | approve / reject |
| `Deploy` | DevOps | approve | развёрнутый стенд |
| `Monitoring` | DevOps + PM | метрики | отчёты, задачи обратной связи |

## 3. Переходы и условия

### 3.9 InDev → CodeReview

Условие: PR открыт; CI зелёный; покрытие нового кода >= 80%; Conventional Commits соблюдены.

### 3.10 CodeReview → QA

Условие: approve от ревьюера (не автора PR); max_rounds=3.

### 3.12 QA → Build

Условие: все уровни тестов зелёные; пороги покрытия выполнены; critical/high баги закрыты.

### 3.15 Release → Deploy

Условие: гейт `human_approval` для prod; release notes в KB; миграции БД проверены.

## 4. Гейты

| Гейт | Тип | Где применяется | Критерий |
|---|---|---|---|
| `requirements_approved` | human_approval | RequirementsReview → Design | Approve + закрытые комментарии |
| `architecture_approved` | human_approval | ArchitectureReview → Planning | Approve + валидная AM |
| `ci_green` | auto | InDev → CodeReview | Все CI-проверки зелёные |
| `review_approved` | agent_review | CodeReview → QA | Approve от ревьюера |
| `qa_passed` | auto | QA → Build | Тесты зелёные, пороги покрытия |
| `sbom_signed` | auto | Build → Release | SBOM + подпись образа |
| `prod_approved` | human_approval | Release → Deploy | Approve человека (R3) |
| `deploy_healthy` | auto | Deploy → Monitoring | Healthcheck + метрики в норме |
| `conformance_ok` | auto | На merge в main | Drift <= warning |
| `kb_published` | agent_review | Диаграмма → KB | Валидатор + ревью |
| `semantic_valid` | auto | Все изменения SLPr/AM | Правила вывода §7 |

## 5. Условия блокировки

Задача **блокируется** при:
1. Наличии `critical` дефектов в QA.
2. Провале CI или тестов.
3. Отсутствии approve от необходимого гейта.
4. Превышении бюджета.
5. Обнаружении `drift` уровня `critical`.
6. Наличии `stale` диаграмм.
7. Обнаружении prompt injection.

## 6. Эскалация

Эскалация человеку при:
- превышении `max_rounds` в цикле ревью (по умолчанию 3);
- превышении `max_steps` или `max_cost_usd`;
- детекторе повторяющихся действий;
- отказе 3 итераций self-correction loop.

## 7. Предохранители

| Предохранитель | Значение по умолчанию |
|---|---|
| `max_rounds` в цикле ревью | 3 |
| `max_steps` на TaskRun | 60 |
| `max_cost_usd` на TaskRun | 3.0 |
| `max_wall_minutes` на TaskRun | 45 |
| `deadline` на StageRun | 24 часа |

## 8. Уровни риска и автономность

| Уровень | Действия | Гейт |
|---|---|---|
| R0 | Чтение (KB, репо, веб) | Автоматически |
| R1 | Коммит в рабочую ветку, комментарии, PR | Автоматически |
| R2 | Merge в main, миграции БД, staging deploy | По профилю автономности |
| R3 | Prod deploy, удаление данных, смена прав | Всегда approve человека |

## 9. Снимки версий для воспроизводимости

Каждый Run закрепляет: `role_versions[]`, `team_version`, `process_version`, `model_profiles[]`, `knowledge_snapshot_id`, `slp_version`, `slpr_version`, `am_version`.

## Изменения документа

| Версия | Дата | Изменения |
|---|---|---|
| 1.1 | 16.10.2026 | Добавлены гейты conformance_ok, semantic_valid |
| 1.0 | 16.10.2026 | Первичная спецификация |
