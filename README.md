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
| MinIO Console | http://localhost:9001 |
| Jaeger UI | http://localhost:16686 |

## Архитектура

- **Control Plane:** Web UI, Platform API, Registry, Policy & Approval, IAM, Semantic Service
- **Execution Plane:** Orchestrator (Temporal), Agent Runtime, LLM Gateway, Tool Gateway (MCP), Sandbox Manager, Diagram Service
- **Data Plane:** PostgreSQL 18 + pgvector (property graph), MinIO (S3), NATS JetStream, Knowledge Service
- **Observability:** OTel Collector + Jaeger (всегда), Langfuse + Prometheus + Grafana (профиль observability)

## Стек

- **Бэкенд:** Go 1.26+ (chi/echo, Temporal SDK, NATS, OTel)
- **Фронтенд:** TypeScript + React 18+, Vite, Tailwind + shadcn/ui
- **Данные:** PostgreSQL 18 + pgvector, MinIO, NATS JetStream
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
