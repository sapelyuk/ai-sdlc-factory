# Архитектура платформы AI-SDLC: ai-sdlc-factory

**Версия 1.4 · 21.10.2026**

## 0. Как читать документ

Три слоя:
1. **Что строим** — разделы 1-3.
2. **Как строим** — разделы 4-11.
3. **Как исполнять** — разделы 12-17.

## 1. Контекст, цели, принципы

### 1.2 Цели

- **G1.** Сборка команды из ИИ-агентов без написания кода.
- **G2.** Каждая роль на разных моделях разных провайдеров.
- **G3.** Настраиваемые базы знаний.
- **G4.** Полный SDLC от исследования до эксплуатации.
- **G5.** Подключаемые Git, CI/CD, трекер, дизайн.
- **G6.** Управляемая автономность с human-approve.
- **G7.** Наблюдаемость и воспроизводимость.
- **G8.** Безопасность и контроль затрат.
- **G9.** Семантический слой как первоклассный элемент.
- **G10.** Архитектор — полноценная роль.

### 1.4 Принципы

- **P1.** Роль как декларация (YAML).
- **P2.** Модельная агностичность через LLM Gateway.
- **P3.** Артефакты вместо памяти (Git, трекер, семантика).
- **P4.** Надёжное исполнение (durable execution).
- **P5.** Безопасность по умолчанию.
- **P6.** Настраиваемая автономность (policy-as-code).
- **P7.** Наблюдаемость как функция.
- **P8.** Инструменты через MCP.
- **P9.** Верификация независима от автора.
- **P10.** Семантика прежде текста.
- **P11.** Диаграммы как код.
- **P12.** Профили развёртывания (local-dev, local-full, prod).

## 2. Глоссарий

| Термин | Значение |
|---|---|
| Роль (Role) | Шаблон агента: назначение, инструкции, инструменты, модель |
| Агент (Agent) | Экземпляр роли с моделью, настройками и бюджетом |
| Команда (Team) | Агенты + топология + процесс + знания |
| Процесс (Process) | Декларативный workflow SDLC |
| Задача (Task) | Единица работы в трекере и оркестраторе |
| Гейт (Gate) | Автопроверка / агентное ревью / approve человека |
| SLP | Онтология платформы ai-sdlc-factory |
| SLPr | Доменная модель продукта |
| AM | Architecture Model (C4-модель) |

## 3. Требования

### 3.1 Функциональные

- **FR-TEAM.** Каталог ролей, создание/клонирование/откат, конструктор команды.
- **FR-KB.** Иерархия скоупов, источники (файлы, URL, Git), гибридный поиск.
- **FR-SEM.** SLP как публичный контракт; SLPr для каждого проекта; API.
- **FR-ARCH.** AM продукта; C4-диаграммы; версионируемые артефакты; ADR.
- **FR-GIT.** OAuth/GitHub App, PR-flow, защищённые ветки.
- **FR-CI.** Уровни тестирования, CI-интеграция, окружения, стратегии выката.
- **FR-DES/RES/PM.** Дизайн (Figma), исследования, двусторонняя синхронизация с трекером.
- **FR-OPS.** Консоль прогонов, учёт стоимости, журнал аудита.

### 3.2 Нефункциональные

NFR-1 изоляция; NFR-2 надёжность; NFR-3 масштабируемость; NFR-4 наблюдаемость (OTel); NFR-5 безопасность (Vault, RBAC); NFR-6 расширяемость; NFR-7 воспроизводимость; NFR-8 развёртывание (Compose -> K8s); NFR-9 стоимость; NFR-10 p95 подграфа < 150 мс.

## 4. Архитектурный обзор

### 4.1 Контекст

```
Owner --> Platform
Reviewers --> Platform
Platform --> LLM (Providers)
Platform --> Git (GitHub/GitLab/Gitea)
Platform --> CI/CD
Platform --> Trackers (Linear/Jira/GitHub Issues)
Platform --> Design (Figma)
Platform --> Research (web, API)
KBSRC --> Platform
```

### 4.2 Контейнеры

**Control Plane:** Web UI, Platform API, Registry, Policy & Approval, IAM, Semantic Service

**Execution Plane:** Orchestrator (Temporal), Agent Runtime, LLM Gateway, Tool Gateway (MCP), Sandbox Manager, Diagram Service

**Data Plane:** PostgreSQL 18 + pgvector, Silo (S3), NATS JetStream, Knowledge Service

**Observability:** OTel Collector + Jaeger (always), Langfuse + Prometheus + Grafana (observability profile)

### 4.3 Web UI

**Роль:** единственный пользовательский интерфейс Control Plane; контейнер `ai-sdlc-factory-web`, раздаётся Nginx под `http://localhost:3000` (NFR-8).

**Ответственность:**
- Аутентификация и управление сессией (JWT, см. §12.2).
- Просмотр каталога ролей, команд и процессов (Registry) — FR-TEAM.
- Журнал аудита и стоимость прогонов (FR-OPS) — в перспективе.
- Конструктор команд (FR-TEAM) — в перспективе.
- Консоль прогонов и дашборды наблюдаемости (FR-OPS, NFR-4) — в перспективе.

**Границы:** UI не обращается к БД, Temporal и хранилищам напрямую — только через Platform API (`/api/v1/...`). Технические решения — §12.2.

## 5. Семантический слой

Формальная машиночитаемая модель: онтология + экземпляры + правила + API.

### 5.2 Три контура

- **SLP** — классы: Role, Team, Agent, Process, Stage, Gate, ArtifactType, Tool, ModelProfile
- **SLPr** — доменные сущности продукта + бизнес-правила
- **AM** — C4-классы: System, Container, Component, DataStore, Interface, Dependency

### 5.3 Хранение

Property graph в PostgreSQL:
- `sem_nodes(id UUIDv7, workspace_id, project_id NULL, layer, kind, label, props JSONB, version, status)`
- `sem_edges(id UUIDv7, src_id, dst_id, kind, props JSONB, valid_from, valid_to)`
- Индексы: GIN по props, btree по kind/project_id, pgvector в sem_embeddings

## 6. Роль Архитектора и диаграммы

### 6.1 Спецификация роли

```yaml
apiVersion: ai-sdlc-factory/v1
kind: Role
metadata: { name: software-architect, version: 2.0.0 }
spec:
  tools:
    allow: [sem.*, diag.render, diag.publish, kb.search, fs.*, git.read]
    deny: [deploy.prod, secrets.read, git.push_protected]
  model:
    capability: reasoning-strong
    min_context: 200000
  limits: { max_steps: 80, max_cost_usd: 6.0 }
  autonomy: supervised
```

### 6.2 Self-correction loop

Валидатор на Go-бекенде проверяет:
1. синтаксис DSL (Mermaid/Structurizr/PlantUML)
2. семантические ссылки — все узлы должны существовать в AM
3. конфликты контрактов
4. отсутствие запрещённых зависимостей

При ошибке — структурированный стектрейс агенту. Максимум 3 итерации.

## 7. Доменная модель

### 7.1 ER

```
WORKSPACE ||--o{ TEAM : contains
WORKSPACE ||--o{ PROJECT : contains
ROLE ||--o{ ROLE_VERSION : has
TEAM_VERSION ||--o{ AGENT : consists_of
AGENT }o--|| ROLE_VERSION : instance_of
PROJECT ||--o{ RUN : executes
RUN ||--o{ STAGE_RUN : contains
STAGE_RUN ||--o{ TASK_RUN : contains
TASK_RUN ||--o{ ARTIFACT : produces
ARTIFACT ||--o| DIAGRAM : may_be
ARTIFACT ||--o| ADR : may_be
```

## 8. Оркестрация

### 8.1 Иерархия исполнения

Run (workflow) -> StageRun -> TaskRun -> AgentStep (activity). Каждый AgentStep идемпотентен.

### 8.2 Цикл агента

Orchestrator -> Agent Runtime -> Semantic Service (подграф) -> Knowledge Service (поиск) -> LLM Gateway (запрос) -> Tool Gateway (вызов) -> Policy Engine (проверка) -> Result

### 8.3 Передача между агентами

Агенты не обмениваются «сырой» перепиской. Все данные проходят через семантический слой (SLPr) и базы знаний (KB). Между агентами передаётся строго структурированный handoff-пакет.

### 8.4 Handoff-пакет

Между агентами передается не сырая переписка, а строго структурированный JSON-пакет (handoff). Получатель MUST валидировать пакет по JSON-схеме. Если пакет неполон или противоречив, он возвращается отправителю с перечнем ошибок. Это останавливает «эффект испорченного телефона».

Обязательные поля handoff-пакета:
- `goal`: четкая цель задачи.
- `context_refs`: URI или ссылки на артефакты и сущности семантического слоя (не копии текста).
- `acceptance_criteria`: критерии приемки (Given/When/Then).
- `constraints`: ограничения (стек, правила, запреты).
- `decisions_made`: принятые ранее решения (ADR), которые нельзя нарушать.
- `open_questions`: вопросы, требующие решения.
- `expected_output_schema`: JSON-схема ожидаемого результата.

## 9. Базы знаний

### 9.2 Индексация

Источники -> Коннекторы -> Нормализация -> Разбиение -> Эмбеддинги (pgvector) + FTS

### 9.3 Поиск

Гибридный с RRF, фильтр по sem_refs.

## 10. Безопасность

### 10.1 Изоляция исполнения

Каждый агент работает в изолированной песочнице (gVisor) без доступа к сети и секретам. Секреты подставляет Tool Gateway (credential brokering).

### 10.2 Недоверенный контент и prompt injection

**Правило разделения возможностей (Separation of Capabilities):**
Один агент в одном шаге не должен одновременно иметь доступ к трем вещам:
1. Приватным данным (БД, секреты, исходный код).
2. Недоверенному контенту (веб-страницы, тикеты, README чужих репозиториев).
3. Исходящему каналу (запись в Git, отправка запросов в интернет, комментарии в трекере).

*Пример:* Агент-исследователь (`product-researcher`) имеет доступ к вебу (п.2), но не имеет прав на запись в Git (п.3). Его результат — структурированный отчёт (JSON), который передается агенту-разработчику. Агент-разработчик имеет доступ к коду (п.1) и Git (п.3), но не имеет доступа к вебу (п.2) в рамках этой же задачи. Это предотвращает выполнение вредоносных инструкций, полученных из внешнего контента.

### 10.4 Уровни риска

- R0 (чтение) — авто
- R1 (локальная запись) — авто
- R2 (значимое) — по профилю
- R3 (необратимое) — всегда approve человека

## 11. Интеграции, наблюдаемость, развёртывание

### 11.5 Профили развёртывания

| Профиль | Назначение | RAM | Состав |
|---|---|---|---|
| local-dev | Разработка на 16 ГБ | ~10 ГБ | Ядро + OTel + Jaeger |
| local-full | Разработка на 32+ ГБ | ~30 ГБ | Ядро + OTel + Jaeger + Langfuse + Prometheus + Grafana |
| prod | Kubernetes | — | Полный стек + управляемые БД |

### 11.3 Наблюдаемость

OTel Collector + Jaeger (always). Langfuse + Prometheus + Grafana (profile observability).

## 12. Технологический стек

### 12.1. Бэкенд

- **Язык:** Go 1.26+ (ADR-001).
- **Фреймворки:** chi/echo, OpenAPI 3.1.
- **Оркестрация:** Temporal Go SDK (ADR-002), NATS JetStream.
- **Наблюдаемость:** OpenTelemetry (NFR-4).

### 12.2. Фронтенд (apps/web)

- **Ядро:** Vite 6, React 18, TypeScript (strict, noUnusedLocals, noUnusedParameters).
- **Стилизация:** Tailwind CSS v4 (CSS-first конфигурация, `@theme inline`), shadcn/ui (компоненты на Radix UI: Button, Input, Table, Card, Dialog, Dropdown, Tabs, Tooltip, Toast).
- **Состояние и данные:** TanStack Query (серверное состояние, retry: 1, staleTime: 5 мин), Zustand v5 (глобальное состояние, auth store).
- **Роутинг:** React Router DOM v7.18+, классический API (`BrowserRouter`, `Routes`, `Route`, `Navigate`).
- **Тесты:** Vitest + Testing Library (jsdom), ESLint + typescript-eslint (`--max-warnings 0`).
- **Сборка и раздача:** multi-stage Dockerfile (Node.js 20 Alpine — builder → Nginx Alpine — продакшн, порт 3000, SPA fallback, `/api/` proxy → `api:8000`, security-заголовки).

**Архитектурные решения:**

- **JWT-аутентификация:** токен хранится **в памяти** (Zustand store), не в `localStorage` (защита от XSS). При перезагрузке страницы сессия сбрасывается. `ProtectedRoute` редиректит на `/login`.
- **API-клиент:** `src/lib/api.ts` — функции `login` и `fetchRoles` с явным `Authorization: Bearer <token>`; при `401` — `clearAuth()`.
- **SSE (Server-Sent Events):** `src/lib/sse.ts` — обёртка над `EventSource` с `AbortController` для будущего стриминга логов агентов и обновлений. Пока заглушка.
- **Страницы:** `/login` (аутентификация), `/` (Dashboard), `/registry/roles` (реальный API), `/registry/teams` и `/registry/processes` (stub).

**Связанные требования:** NFR-8 (развёртывание через Compose → K8s), ADR-015 (единое имя `ai-sdlc-factory`).

### 12.3. Данные

- **СУБД:** PostgreSQL 18 + pgvector (property graph, ADR-003, ADR-019).
- **Объектное хранилище:** Silo (S3-совместимый форк MinIO, ADR-020).
- **Шина событий:** NATS JetStream.

### 12.4. Инфраструктура

- **Контейнеризация:** Docker Compose (профили `local-dev`, `local-full`, ADR-018).
- **Оркестрация:** Kubernetes (Helm) — в перспективе.

## 13. ADR

Полный реестр — `docs/architecture/ADR.md` (ADR-001...ADR-018).

## 14. План поставки

### 14.1 Этапы

| Этап | Содержание | Критерий приёмки |
|---|---|---|
| 0. Фундамент | Монорепозиторий, CI, Compose, БД, auth, Registry, UI, OTel+Jaeger, SLP v0 | Роль создаётся в UI; SLP v0 читается через API; `make up-local` работает |
| 1. Один агент | Temporal, Agent Runtime, LLM Gateway, Tool Gateway, Sandbox | Агент создаёт ветку и открывает PR; CI зелёный |
| 2. Команда | Роли PM/аналитик/архитектор/разработчик/ревьюер/QA; handoff; гейты | 6 агентов реализуют фичу от тикета до слитого PR |
| 3. Знания | Knowledge Service, коннекторы, гибридный поиск; SLPr v1 + AST-индексация | Агент цитирует фрагмент; использует подграф SLPr |
| 4. Архитектура | Architect role, AM v1, Diagram Service, self-correction, ADR, conformance | Архитектор строит C4, публикует в KB, ADR связаны с AM |
| 5. Полный SDLC | Исследование, дизайн, трекер, CI/CD, GitOps, staging/prod | Демо-продукт от идеи до staging, затем prod с approve |
| 6. Эксплуатация | Evals, replay, дашборды, hardening, K8s, бэкапы | Смена модели -> evals; security review; `make up-full` работает |

## 15. Структура монорепозитория

```
ai-sdlc-factory/
  .cursorrules, .env.example, Makefile, docker-compose.yml, go.work, README.md
  apps/ (web, api, worker, semantic, semantic-indexer, diagram, knowledge, gateway-llm, tool-gateway, sandbox-manager)
  packages/ (contracts, ontology, policy, sdk-go, sdk-ts)
  backend/ (go.mod, cmd/, internal/)
  frontend/ (package.json, vite.config.ts, src/)
  catalog/ (roles, teams, processes)
  deploy/ (compose, helm, sandboxes, k8s)
  migrations/ (postgres, graph)
  docs/ (README.md, architecture/)
  evals/ (roles, models)
```

## 16. Инструкция для ИИ-агента

### 16.1 Правила работы

1. Перед каждым этапом — план, дождись подтверждения.
2. Этапы — строго по порядку.
3. Контракты первыми (OpenAPI, JSON Schema, MCP-схемы), затем реализация.
4. Отклонение от ADR — только через новый ADR.
5. Безопасность не откладывается.
6. Тесты — вместе с кодом; не ослабляй.
7. Малые PR с Conventional Commits; ссылки на FR/NFR/ADR.
8. Именование: `ai-sdlc-factory` везде.
9. Профили развёртывания: указывай профиль для каждого нового сервиса.

### 16.6 Тактика генерации файлов

| Итерация | Задача | Файлы |
|---|---|---|
| 1. Инфраструктура и БД | Compose, миграции | Makefile, deploy/compose/**, migrations/** |
| 2. Контракты и домен | OpenAPI, JSON Schema, доменные модели | packages/contracts/**, packages/ontology/**, backend/internal/domain/** |
| 3. Семантика и архитектор | Semantic Service, AST-индексатор, Diagram Service | backend/internal/semantic/**, architect_engine/** |
| 4. Песочницы и MCP | Tool Gateway, Sandbox Manager | backend/internal/sandbox/**, mcp/** |
| 5. Оркестратор и LLM | Temporal workflow, Agent Runtime, LLM Gateway | backend/internal/llm/**, orchestra/**, transport/**, cmd/** |
| 6. Фронтенд | Web UI | frontend/** |

## 17. Открытые вопросы

1. Structurizr DSL как основной формат C4 — согласовано?
2. Глубина подграфа по умолчанию.
3. Нужен ли RDF/OWL-экспорт в v1?
4. Политика хранения снимков SLPr/AM/KB.
5. Мультиязычность UI.
6. Бюджеты по умолчанию.
7. LiteLLM как прокси — допустим?
8. Air-gapped режим в v1?
9. Приоритетные провайдеры LLM.
10. Политика по персональным данным.
11. Парсить ли монорепо Bazel/Nx?
12. Блокировка merge при critical drift — по умолчанию или нет?

## Изменения документа

| Версия | Дата | Изменения |
|---|---|---|
| 1.4 | 21.10.2026 | Добавлены §4.3 (Web UI), §12 структурирован на §12.1–§12.4, добавлен §12.2 (Фронтенд) (#64) |
| 1.3 | 20.10.2026 | Добавлены §8.3, §8.4 (Handoff-пакет), §10.1, §10.2 (Anti-Prompt Injection) |
| 1.2 | 16.10.2026 | Первичная архитектура |

**Конец документа.**
