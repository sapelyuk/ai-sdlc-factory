# AGENT_KICKOFF — превращение документации в GitHub Issues

**Версия 1.1 · 21.10.2026 · для ИИ-агента-исполнителя**

Этот документ описывает, как превратить архитектурную документацию `ai-sdlc-factory` в структурированный план работ в GitHub Issues.

---

## 0. Роль агента

Ты — **Principal Technical Program Manager** для проекта `ai-sdlc-factory`. Ты не пишешь код в рамках этой задачи. Твой результат — набор milestones, labels, issues и связей между ними.

---

## 1. Обязательное чтение

Прочитай в указанном порядке:

| № | Файл | Что из него берётся |
|---|---|---|
| 1 | `docs/README.md` | Навигация по документации |
| 2 | `docs/architecture/SYSTEM_DESIGN.md` | Требования (FR-*, NFR-*), принципы, этапы поставки (§14.1), структура монорепозитория (§15), тактика генерации файлов (§16.6) |
| 3 | `docs/architecture/ADR.md` | 18 архитектурных решений (ADR-001…ADR-018) |
| 4 | `docs/architecture/ONTOLOGY_SPEC.md` | Семантический слой: SLP, SLPr, AM |
| 5 | `docs/architecture/SDLC_STATE_MACHINE.md` | Состояния задач, гейты, условия блокировки |
| 6 | `docs/architecture/MCP_TOOLS.md` | Инструменты агентов по ролям |
| 7 | `docs/architecture/diagrams/README.md` | Набор C4-диаграмм |
| 8 | `deploy/compose/README.md` | Профили развёртывания `local-dev` / `local-full` |

После чтения составь сжатый отчёт (10–15 строк): ключевые цели, стек, ключевые ADR, этапы поставки.

---

## 2. Что нужно произвести

Твой результат — четыре артефакта в GitHub:

1. **Milestones** — по одному на каждый этап поставки из `SYSTEM_DESIGN.md` §14.1 (Этапы 0–6).
2. **Labels** — стандартизированный набор (см. §4).
3. **Issues** — по одной или нескольким задачам на каждый этап.
4. **Связи** — зависимости между issues, привязка к milestones, labels, ссылки на FR/NFR/ADR.

---

## 3. Правила декомпозиции

### 3.1 Источник структуры

Основа — **этапы поставки** из `SYSTEM_DESIGN.md` §14.1. Каждый этап = один **milestone**.

### 3.2 Размер issues

- **1 issue = 0.5–3 дня работы** для одного разработчика.
- Если задача больше — разбить.
- Если задача меньше 2 часов — объединить с соседней.
- **Spike-задачи** — отдельный тип, с явным time-box.

### 3.3 Что обязательно в каждом issue

- Заголовок: `<area>: <краткое действие>`
- Ссылки на FR-* / NFR-* из `SYSTEM_DESIGN.md` §3
- Ссылки на ADR-* из `ADR.md`
- Ссылка на раздел `SYSTEM_DESIGN.md` — **только фактически существующий** (см. §3.4)
- Acceptance criteria в формате Given/When/Then
- Тестовый план
- Оценка сложности (`size:S` / `size:M` / `size:L`)
- Milestone и labels

### 3.4 Проверка ссылок на разделы

Перед созданием issue убедись, что указываемый раздел существует:

```bash
git grep -n "^#\+ <N>\." -- docs/architecture/SYSTEM_DESIGN.md
```

Если нужного подраздела нет — **не выдумывай номер**. Either сошлись на родительский раздел (§12 вместо §12.2), либо создай spike-issue по правилам `AGENTS.md` («Что делать при обнаружении пробела в документации»).

---

## 4. Labels

### 4.1 По типу работы

| Label | Цвет | Значение |
|---|---|---|
| `type:feature` | `#0E8A16` | Новая функциональность |
| `type:infra` | `#5319E7` | Инфраструктура, Compose, миграции |
| `type:contract` | `#1D76DB` | OpenAPI, JSON Schema, MCP-схемы |
| `type:test` | `#FBCA04` | Только тесты |
| `type:docs` | `#0075CA` | Документация |
| `type:spike` | `#D4C5F9` | Исследование, time-box |
| `type:refactor` | `#BFD4F2` | Рефакторинг |
| `type:security` | `#B60205` | Безопасность |

### 4.2 По области

| Label | Область |
|---|---|
| `area:infra` | Compose, миграции |
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

### 4.3 По приоритету

| Label | Значение |
|---|---|
| `p0` | Блокер |
| `p1` | Высокий |
| `p2` | Средний |

### 4.4 По размеру

| Label | Оценка |
|---|---|
| `size:S` | до 0.5 дня |
| `size:M` | 0.5–1.5 дня |
| `size:L` | 1.5–3 дня |

### 4.5 Особые

| Label | Когда ставить |
|---|---|
| `blocked` | Есть незакрытая зависимость |
| `needs-approval` | Требует approve владельца |
| `r3` | Затрагивает необратимые действия |
| `security-critical` | Требует security review |

---

## 5. Шаблон issue

```markdown
## Контекст

<1–3 предложения. Что делаем и зачем.>

## Ссылки

- Требования: `FR-XXX`, `NFR-XXX`
- ADR: `ADR-XXX`
- Раздел: `docs/architecture/SYSTEM_DESIGN.md` §X.Y
  > ⚠️ **Внимание:** Указывай только фактически существующие разделы. Проверь через `git grep -n "^##" -- docs/architecture/SYSTEM_DESIGN.md`. Если подраздела нет — сошлись на родительский раздел (например, §12 вместо §12.2) или создай его в текущей задаче. Не оставляй битых ссылок.

## Что сделать

- [ ] Пункт 1
- [ ] Пункт 2

## Acceptance criteria

- [ ] Given … When … Then …

## Тестовый план

- [ ] Unit-тесты
- [ ] Integration-тесты
- [ ] Contract-тесты
- [ ] E2E
- [ ] Покрытие нового кода ≥ 80%

## Зависимости

- Blocked by: #<issue>
- Blocks: #<issue>

## Оценка

- Size: S | M | L
- Priority: p0 | p1 | p2

## Definition of Done

- [ ] Код в PR, зелёный CI
- [ ] Тесты зелёные, покрытие ≥ 80%
- [ ] Логи без секретов
- [ ] Документация обновлена
```

---

## Изменения документа

| Версия | Дата | Изменения |
|---|---|---|
| 1.1 | 21.10.2026 | Добавлены §3.4 (проверка ссылок на разделы) и предупреждение в шаблон issue (#64) |
| 1.0 | 16.10.2026 | Первичная спецификация |
