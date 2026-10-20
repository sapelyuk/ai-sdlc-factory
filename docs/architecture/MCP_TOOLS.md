# MCP-инструменты агентов — ai-sdlc-factory

**Версия 1.2 · 20.10.2026**

## 1. Общие правила

- Все инструменты доступны через **Tool Gateway**.
- Каждый вызов проходит **Policy Engine**; для R2/R3 может требовать approve.
- Секреты подставляет Tool Gateway (credential brokering).
- Правила доступа: `Role.tools.allow` — белый список; `Role.tools.deny` — чёрный список, приоритетнее `allow`.

**Ограничения по ролям (Anti-Injection):**
При назначении инструментов роли (см. `spec.tools` в YAML) MUST соблюдаться правило разделения возможностей. Ни одна роль не должна иметь одновременно `web.*` (недоверенный контент), `secrets.read` / `fs.*` (приватные данные) и `git.push` / `deploy.*` / `tracker.write` (исходящий канал). Если роль требует пересечения этих доменов, задача должна быть декомпозирована на два шага (например, «Исследование» → Handoff → «Реализация»).

## 2. Формат файла схемы

```json
{
  "role": "<role-name>",
  "schema_version": "ai-sdlc-factory/v1",
  "tools": [{
    "name": "<tool.name>",
    "description": "<описание>",
    "input_schema": { "type": "object", "properties": { } },
    "output_schema": { "type": "object", "properties": { } },
    "risk_level": "R0|R1|R2|R3"
  }]
}
```

## 3. Инструменты по ролям

### 3.1 `pm_tools.json`

| Инструмент | Описание | Risk |
|---|---|---|
| `pm.create_prd` | Создать PRD | R1 |
| `pm.update_backlog` | Обновить бэклог | R1 |
| `tracker.comment` | Комментарий в тикете | R1 |
| `kb.search` | Поиск в KB | R0 |

### 3.2 `researcher_tools.json`

| Инструмент | Описание | Risk |
|---|---|---|
| `web.search` | Поиск в интернете | R0 |
| `web.fetch` | Загрузка страницы | R0 |
| `research.summarize` | Сводка по источникам | R0 |
| `kb.write_draft` | Черновик в KB | R1 |

### 3.3 `analyst_tools.json`

| Инструмент | Описание | Risk |
|---|---|---|
| `slpr.propose_node` | Предложить сущность SLPr | R2 |
| `slpr.propose_edge` | Предложить связь | R2 |
| `slpr.search` | Поиск по SLPr | R0 |

### 3.4 `designer_tools.json`

| Инструмент | Описание | Risk |
|---|---|---|
| `figma.read` | Чтение файла Figma | R0 |
| `figma.write` | Правка фреймов | R2 |
| `design.export_tokens` | Экспорт токенов | R1 |

### 3.5 `architect_tools.json`

| Инструмент | Описание | Risk |
|---|---|---|
| `sem.am_read` | Чтение AM | R0 |
| `sem.am_write` | Правка AM | R2 |
| `diag.render` | Рендер DSL в SVG/PNG | R1 |
| `diag.validate` | Валидация DSL | R1 |
| `diag.publish` | Публикация в KB | R2 |
| `adr.propose` | Предложить ADR | R2 |

**Запрещено:** `deploy.prod`, `secrets.read`, `git.push_protected`.

### 3.6 `developer_tools.json`

| Инструмент | Описание | Risk |
|---|---|---|
| `git.clone` | Клонирование репо | R0 |
| `git.branch` | Создание ветки | R1 |
| `git.commit` | Коммит | R1 |
| `git.push` | Push ветки | R1 |
| `sandbox.exec` | Команда в песочнице | R1 |
| `fs.read` | Чтение файла | R0 |
| `fs.write` | Запись файла | R1 |

**Запрещено:** `deploy.prod`, `secrets.read`, `git.push_protected`.

### 3.7 `qa_tools.json`

| Инструмент | Описание | Risk |
|---|---|---|
| `sandbox.exec` | Прогон тестов | R1 |
| `test.run` | Unit/integration/e2e | R1 |
| `test.report` | Отчёт покрытия | R0 |
| `bug.open` | Открыть баг-репорт | R1 |

### 3.8 `devops_tools.json`

| Инструмент | Описание | Risk |
|---|---|---|
| `ci.trigger` | Запуск CI | R1 |
| `ci.status` | Статус CI | R0 |
| `deploy.staging` | Деплой в staging | R2 |
| `deploy.prod` | Деплой в prod | R3 |
| `deploy.rollback` | Откат | R2 |

### 3.9 `reviewer_tools.json`

| Инструмент | Описание | Risk |
|---|---|---|
| `git.diff` | Diff PR | R0 |
| `pr.review` | Ревью PR | R1 |
| `pr.approve` | Approve | R1 |

### 3.10 `security_tools.json`

| Инструмент | Описание | Risk |
|---|---|---|
| `sast.run` | Статический анализ | R1 |
| `dep.scan` | Сканирование зависимостей | R1 |
| `secret.scan` | Сканирование на секреты | R1 |

## 4. Универсальные инструменты

| Инструмент | Описание | Risk |
|---|---|---|
| `kb.search` | Поиск в KB | R0 |
| `sem.query` | Запрос по семантическому графу | R0 |
| `tracker.comment` | Комментарий | R1 |
| `agent.handoff` | Handoff-пакет | R1 |

## 5. Маппинг на Tool Gateway

1. Загружает схемы из `packages/contracts/mcp-schemas/`.
2. Фильтрует инструменты по `Role.tools.allow` и `Role.tools.deny`.
3. Для каждого вызова: проверяет права, применяет политику, подставляет секреты, выполняет вызов, логирует.

## Изменения документа

| Версия | Дата | Изменения |
|---|---|---|
| 1.2 | 20.10.2026 | Добавлены ограничения по ролям (Anti-Injection) |
| 1.1 | 16.10.2026 | Добавлены sem.*, diag.*, adr.*, slpr.* |
| 1.0 | 16.10.2026 | Первичный реестр |
