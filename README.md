# AI-SDLC Factory

**Мультиагентная платформа для полного SDLC под контролем владельца.**

`ai-sdlc-factory` — это платформа, где команды ИИ-агентов реализуют полный цикл разработки ПО: от исследования и проектирования до тестирования, деплоя и мониторинга. Каждая роль и агент работает на своей модели, с настраиваемыми правами, бюджетами и уровнями автономности.

## Быстрый старт

```bash
# 1. Настроить окружение
cp deploy/compose/.env.example deploy/compose/.env.local-dev

# 2. Поднять платформу
make up-local          # local-dev (~10 ГБ RAM)
make up-full           # local-full (~30 ГБ RAM, с Langfuse)

# 3. Проверить
make ps-local
```

| Сервис | URL |
|---|---|
| Web UI | http://localhost:3000 |
| Platform API | http://localhost:8000 |
| Temporal UI | http://localhost:8080 |
| Silo Console | http://localhost:9001 |
| Jaeger UI | http://localhost:16686 |

## Архитектура

- **Control Plane:** Web UI, Platform API, Registry, Policy & Approval, IAM, Semantic Service
- **Execution Plane:** Orchestrator (Temporal), Agent Runtime, LLM Gateway, Tool Gateway (MCP), Sandbox Manager, Diagram Service
- **Data Plane:** PostgreSQL 18 + pgvector (property graph), Silo (S3), NATS JetStream, Knowledge Service
- **Observability:** OTel Collector + Jaeger (всегда), Langfuse + Prometheus + Grafana (профиль observability)

## Registry API

После запуска PostgreSQL примените миграции командой `make migrate-up`. Для Registry-запросов обязательны заголовки `X-Workspace-ID` и `X-Author-ID` с ненулевыми UUID; для смены статуса дополнительно требуется `X-Approver-ID`, отличный от автора.

Пример создания роли (спецификация проверяется по JSON-Schema):

```bash
curl -X POST http://localhost:8000/api/v1/registry/roles \
  -H "Content-Type: application/json" \
  -H "X-Workspace-ID: <workspace-uuid>" \
  -H "X-Author-ID: <author-uuid>" \
  -d '{
    "apiVersion": "ai-sdlc-factory/v1",
    "kind": "Role",
    "metadata": {"name": "backend-developer", "category": "engineering"},
    "spec": {
      "description": "Implements backend features.",
      "system_prompt_ref": "packages/ontology/prompts/backend-developer.yaml",
      "inputs": [{"name": "requirements", "type": "document"}],
      "outputs": [{"name": "implementation", "type": "code"}],
      "tools": {"allow": ["fs.read"]},
      "model": {"capability": "code-strong"},
      "limits": {"max_steps": 60, "max_cost_usd": 3.0},
      "autonomy": "balanced"
    }
  }'
```

Для публикации используйте `PATCH /api/v1/registry/roles/{id}/status` с телом `{"status":"approved"}` и заголовком `X-Approver-ID`. Опубликованные спецификации неизменяемы; создайте новую версию через `POST /api/v1/registry/roles/{id}/clone` или восстановите предыдущую через `/revert`.

Для локальной проверки после применения миграции и запуска API задайте `WORKSPACE_ID`, `AUTHOR_ID` и отличный от автора `APPROVER_ID`, затем запустите `pwsh -File tests/smoke/registry-smoke.ps1`.

SLP v0 загружается командой `cd apps/api && go run ./cmd/seed`; задайте `SEED_WORKSPACE_ID` и `SEED_AUTHOR_ID` для повторяемой локальной загрузки. Если они отсутствуют, seed создаёт UUIDv7 и предупреждает, что значения подходят только для локальной разработки.

## Стек

- **Бэкенд:** Go 1.26+ (chi/echo, Temporal SDK, NATS, OTel)
- **Фронтенд:** TypeScript + React 18+, Vite, Tailwind + shadcn/ui
- **Данные:** PostgreSQL 18 + pgvector, Silo (S3), NATS JetStream
- **Инфраструктура:** Docker Compose → Kubernetes (Helm)

## Документация

| Документ | Описание |
|---|---|
| [SYSTEM_DESIGN.md](docs/architecture/SYSTEM_DESIGN.md) | Полная архитектура платформы |
| [ADR.md](docs/architecture/ADR.md) | 19 архитектурных решений |
| [ONTOLOGY_SPEC.md](docs/architecture/ONTOLOGY_SPEC.md) | Семантический слой (SLP/SLPr/AM) |
| [SDLC_STATE_MACHINE.md](docs/architecture/SDLC_STATE_MACHINE.md) | Состояния задач и гейты |
| [MCP_TOOLS.md](docs/architecture/MCP_TOOLS.md) | Инструменты агентов по ролям |
| [AGENT_KICKOFF.md](docs/AGENT_KICKOFF.md) | Создание задач через GitHub Issues |

## Лицензия

AGPLv3
