# AI-SDLC Factory — локальное развёртывание

## Два профиля

| Профиль | Когда использовать | RAM | Langfuse |
|---|---|---|---|
| **local-dev** | Разработка и первые тесты (16 ГБ RAM) | ~10 ГБ | Выключен |
| **local-full** | Полноценная разработка (32+ ГБ RAM) | ~30 ГБ | Включён |

## Быстрый старт

```bash
# 1. Заполнить .env.local-dev (или .env.local-full)
cp deploy/compose/.env.example deploy/compose/.env.local-dev
$EDITOR deploy/compose/.env.local-dev

# 2. Поднять платформу
make up-local          # или make up-full

# 3. Проверить
make ps-local
```

## URL-ы сервисов

| Сервис | local-dev | local-full |
|---|---|---|
| Web UI | http://localhost:3000 | http://localhost:3000 |
| Platform API | http://localhost:8000 | http://localhost:8000 |
| Temporal UI | http://localhost:8080 | — |
| Silo Console | http://localhost:9001 | http://localhost:9001 |
| Jaeger UI | http://localhost:16686 | http://localhost:16686 |
| Langfuse | — | http://localhost:3030 |
| Grafana | — | http://localhost:3001 |
| Prometheus | — | http://localhost:9090 |
| Vault | http://localhost:8200 | http://localhost:8200 |
| OPA   | http://localhost:8181 | http://localhost:8181 |

## Инфраструктурные сервисы

| Сервис | Порт | Назначение | Профиль |
|---|---|---|---|
| Silo (S3) | 9000 (API), 9001 (Console) | S3-совместимое хранилище (форк MinIO) | всегда |
| NATS | 4222 (клиент), 8222 (мониторинг) | Шина событий (JetStream) | всегда |
| Vault | 8200 | Управление секретами (dev-режим) | всегда |
| OPA | 8181 | Policy engine (Rego) | всегда |
| Temporal Server | 7233 | Durable execution для local-dev | local-dev |
| Temporal UI | 8080 | Просмотр workflow | local-dev |

### Temporal (оркестрация)

Temporal использует два datastore (persistence + visibility), которые
**должны** находиться в разных БД PostgreSQL. Попытка использовать одну БД
приводит к конфликту миграций и падению сервера. Поэтому в `ai-sdlc-factory`
создаются: `ai_sdlc_factory` (persistence) и `temporal_visibility` (visibility).

`temporal_visibility` создаётся init-скриптом
`deploy/compose/postgres/init/01-create-temporal-visibility-db.sql` при первом
запуске на пустом PostgreSQL volume. Для уже существующего volume init-скрипты
не запускаются; если `temporal_visibility` отсутствует, создайте её вручную:

```bash
docker compose exec postgres sh -c 'psql -U "$POSTGRES_USER" -d postgres -c "CREATE DATABASE temporal_visibility;"'
```

Dynamic config монтируется из
`deploy/compose/temporal/dynamicconfig/development-sql.yaml`. Версии образов
зафиксированы в `docker-compose.local-dev.yml`: используется
`temporalio/auto-setup:1.26.3`, поскольку запланированный тег `1.26.0`
отсутствует в Docker Hub; `1.26.3` — доступный стабильный patch-релиз той же
minor-ветки. В профиле `local-full` Temporal не запускается.

После `make up-local` создайте namespace `ai-sdlc-factory`:

```bash
make temporal-create-namespace
make temporal-smoke-test
```

Smoke-тест проверяет namespace и UI, а также перезапускает Temporal Server и
проверяет восстановление запущенного workflow.

Namespace можно проверить напрямую, а UI доступен по адресу
http://localhost:8080.

### Проверка здоровья

```bash
make check-infra
```

### Silo — бакеты

Бакеты создаются автоматически при первом запуске через `silo-init`:
- `ai-sdlc-factory` — постоянное хранение (артефакты, диаграммы, логи).
- `ai-sdlc-factory-temp` — временные артефакты (публичное чтение для отладки).

> **Примечание:** Официальные образы MinIO удалены из Docker Hub. Используется
> Silo (`docker.io/pgsty/silo`) — community-форк, совместимый с S3 API,
> `MINIO_*` переменными и форматами данных MinIO. Данные MinIO совместимы с Silo;
> миграция не требуется.

**Переменные окружения:** `MINIO_ROOT_USER` / `MINIO_ROOT_PASSWORD` — официальные
имена переменных Docker-образа MinIO/Silo (Silo полностью поддерживает `MINIO_*`).

**Тег образа:** Silo использует timestamp-теги (`RELEASE.YYYY-MM-DD...`) —
официальный формат rolling-release.

**Именование томов:** Docker-тома используют дефисы: `minio-data`, `nats-data`,
`vault-data` — современный стандарт Docker Compose. Том `minio-data` сохраняется
для совместимости с существующими данными.

### Vault — dev-режим

Vault запущен в `server -dev` режиме:
- `VAULT_DEV_ROOT_TOKEN` — фиксированный токен (из `.env`, по умолчанию `root`).
- **Никогда не использовать в production!** Production-конфиг — отдельная задача.
- Данные не персистятся между перезапусками в dev-режиме.

### OPA — политики

## Наблюдаемость (базовый уровень)

| Сервис | Порт | Назначение |
|---|---|---|
| OTel Collector (gRPC) | 4317 | Приём OTLP-трейсов |
| OTel Collector (HTTP) | 4318 | Приём OTLP-трейсов |
| OTel Collector (metrics) | 8888 | Prometheus-метрики |
| OTel Collector (health) | 13133 | Healthcheck |
| Jaeger UI | 16686 | Просмотр трейсов |
| Jaeger admin | 14269 | Healthcheck |
| Jaeger OTLP gRPC | 14317 | Прямой приём OTLP gRPC |
| Jaeger OTLP HTTP | 14318 | Прямой приём OTLP HTTP |

### Профили

- **`local-dev`**: Jaeger in-memory, `MEMORY_MAX_TRACES=10000` (≈500 МБ RAM).
- **`local-full`**: Jaeger in-memory, `MEMORY_MAX_TRACES=50000` (≈1 ГБ RAM), расширенный OTel config.

### Как отправить трейс

Любое приложение шлёт трейсы на `otel-collector:4317` (OTLP gRPC).
В `local-dev` трейсы доступны в Jaeger UI (`http://localhost:16686`).

### Проверка

```bash
# Отправить тестовый трейс
docker run --rm --network host \
  ghcr.io/open-telemetry/opentelemetry-collector-contrib/telemetrygen:latest \
  traces --otlp-endpoint localhost:4317 --otlp-insecure --traces 1

# Открыть Jaeger UI
open http://localhost:16686
```

### Langfuse exporter (M6)

Langfuse exporter в OTel Collector остаётся закомментированным — будет добавлен в M6.
Сейчас трейсы идут только в Jaeger.
Инфраструктура Langfuse (ClickHouse, PostgreSQL, Redis, Web, Worker) уже доступна
в профиле `local-full`, но интеграция LLM-трейсинга, evals и дашбордов качества —
отдельная задача.

### Legacy jaeger exporter и порты 14317/14318

OTel Collector использует **legacy jaeger exporter** (`otlp/jaeger` на порт `4317`
внутри сети Docker) для отправки трейсов в Jaeger. Это проверенный рабочий вариант.

Порты `14317` (OTLP gRPC) и `14318` (OTLP HTTP) на Jaeger проброшены на хост
**только для отладки** — внешние клиенты могут слать трейсы напрямую в Jaeger,
минуя OTel Collector. В основной архитектуре все трейсы должны идти через
OTel Collector (batch processor, memory_limiter, единая точка маршрутизации).

Rego-политики находятся в `packages/policy/`:
- `autonomy.rego` — уровни автономности R0–R3 (ADR-013).
- `risk_levels.rego` — оценка риска по категории задачи.

Пример запроса:
```bash
curl -X POST http://localhost:8181/v1/data/ai_sdlc_factory/autonomy/risk_level \
  -d '{"input":{"action":"deploy.prod"}}'
# → {"result":"R3"}
```

### Остановка

```bash
make down-local        # или make down-full
```

## Полный сброс данных

```bash
make clean             # контейнеры и сети
make volumes           # ВСЕ тома (осторожно!)
```

## Если не хватает памяти (OOM)

1. Проверьте `docker stats` — какой контейнер ест больше всего.
2. Уменьшите лимиты в `docker-compose.local-dev.yml`.
3. Увеличьте swap на хосте.
4. Отключите ненужные сервисы в override-файле.

## Миграции БД

PostgreSQL используется с образом `pgvector/pgvector:pg18` (встроенный pgvector).

### Применение миграций

```bash
# Применить все .up.sql по порядку
make migrate-up

# Откатить все .down.sql в обратном порядке
make migrate-down
```

### Тестирование миграций

```bash
cd migrations/tests
go test -v ./...
```

Тесты используют Testcontainers (образ `pgvector/pgvector:pg18`).
Если Docker не запущен, контейнерные тесты пропускаются (`t.Skip`).

Миграции находятся в `migrations/postgres/`:

| № | Файл | Описание |
|---|---|---|
| 1 | `0001_init_squads_and_agents` | workspaces, squads, agents, model_profiles |
| 2 | `0002_init_tasks_and_sdlc` | runs, stage_runs, task_runs, approvals |
| 3 | `0003_init_knowledge_base` | kb_documents, kb_sources |
| 4 | `0004_init_pgvector` | pgvector extension, kb_embeddings, HNSW |
| 5 | `0005_init_semantic_graph` | sem_nodes, sem_edges, sem_embeddings |
| 6 | `0006_init_architecture_and_adr` | diagrams, architecture_models, adrs |
| 7 | `0007_init_audit_and_cost` | audit_log, llm_calls |

Каждая миграция имеет парный файл `.up.sql` / `.down.sql` для применения и отката.
