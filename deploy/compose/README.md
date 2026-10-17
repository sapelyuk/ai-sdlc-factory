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
| Temporal UI | http://localhost:8080 | http://localhost:8080 |
| MinIO Console | http://localhost:9001 | http://localhost:9001 |
| Jaeger UI | http://localhost:16686 | http://localhost:16686 |
| Langfuse | — | http://localhost:3030 |
| Grafana | — | http://localhost:3001 |
| Prometheus | — | http://localhost:9090 |
| Vault | http://localhost:8200 | http://localhost:8200 |

## Остановка

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

Тесты используют Testcontainers (образ `pgvector/pgvector:pg17`).
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
