# Спецификация семантического слоя (SLP / SLPr / AM) — ai-sdlc-factory

**Версия 1.1 · 16.10.2026**

## 1. Обзор

Семантический слой — формальная машиночитаемая модель знаний. Состоит из трёх контуров:

| Контур | Назначение | Скоуп |
|---|---|---|
| **SLP** | Онтология платформы `ai-sdlc-factory` | Глобальный, workspace |
| **SLPr** | Доменная модель конкретного продукта | Один на проект |
| **AM** | C4-модель продукта | Один на проект (версионируется) |

## 2. Форматы

### 2.1 Нотации

Онтологии: **Turtle/OWL-совместимый YAML** + **JSON-LD** (экспорт).
Экземпляры: **property graph в PostgreSQL** + экспорт в **JSON-LD / RDF / GraphML**.

### 2.2 Namespace

- SLP: `ai-sdlc-factory/slp@<semver>`
- SLPr: `ai-sdlc-factory/product-domain@<semver>`
- AM: `ai-sdlc-factory/am@<semver>`

### 2.3 Версионирование

Semver. Мажорные изменения — R3 (approval человека).

## 3. SLP — онтология платформы

### 3.1 Классы

| Класс | Описание |
|---|---|
| `Role` | Шаблон агента |
| `Team` | Набор агентов + топология + процесс |
| `Agent` | Экземпляр роли |
| `Process` | Workflow SDLC |
| `Stage` | Стадия процесса |
| `Gate` | Контрольная точка |
| `ArtifactType` | Тип артефакта |
| `Tool` | MCP-инструмент |
| `ModelProfile` | Профиль возможностей |
| `Capability` | Возможность роли |
| `KnowledgeBase` | База знаний |
| `Scope` | Скоуп знаний |
| `Policy` | Политика |
| `AutonomyLevel` | Уровень автономности |

## 4. SLPr — домен продукта

### 4.2 Типовые классы

Пример для интернет-магазина: `Customer`, `Order`, `Product`, `Payment`, `Shipment`, `LineItem`.

### 4.4 Бизнес-правила

Оформляются как SHACL-подобные правила в онтологии проекта; валидируются Semantic Service.

## 5. AM — архитектурная модель

### 5.1 Классы (C4)

`System`, `Container`, `Component`, `DataStore`, `Interface`, `Dependency`, `DeploymentNode`, `Environment`.

### 5.3 Источник истины

AM хранится в Semantic Service. Экспортируется в **Structurizr DSL** (основной формат).

## 6. Хранение

### 6.1 Схема (PostgreSQL)

```sql
CREATE TABLE sem_nodes (
  id UUID PRIMARY KEY, workspace_id UUID NOT NULL, project_id UUID NULL,
  layer TEXT NOT NULL CHECK (layer IN ('SLP','SLPr','AM')),
  kind TEXT NOT NULL, label TEXT NOT NULL, props JSONB NOT NULL DEFAULT '{}',
  version INT NOT NULL DEFAULT 1, status TEXT NOT NULL DEFAULT 'draft',
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(), updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE sem_edges (
  id UUID PRIMARY KEY,
  workspace_id UUID NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
  src_id UUID NOT NULL REFERENCES sem_nodes(id) ON DELETE CASCADE,
  dst_id UUID NOT NULL REFERENCES sem_nodes(id) ON DELETE CASCADE,
  kind TEXT NOT NULL,
  props JSONB NOT NULL DEFAULT '{}',
  valid_from TIMESTAMPTZ NOT NULL DEFAULT now(),
  valid_to TIMESTAMPTZ NULL
);

CREATE TABLE sem_embeddings (
  node_id UUID PRIMARY KEY REFERENCES sem_nodes(id) ON DELETE CASCADE,
  workspace_id UUID NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
  embedding VECTOR(1536) NOT NULL,
  model_name TEXT NOT NULL DEFAULT 'text-embedding-3-small',
  created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
```

Row Level Security по `workspace_id`.

> **Примечание:** В терминах Issue и UI статус «approved» соответствует значению `active` в БД (CHECK в миграции `0005_init_semantic_graph`). Маппинг: `approved` ↔ `active`. Узел в статусе `active` неизменяем: `PATCH /v1/nodes/{id}` возвращает 403.


> **Примечание:** `sem_edges` и `sem_embeddings` содержат денормализованный `workspace_id` (ссылка на `workspaces`) для оптимизации RLS-политик. Это позволяет применять RLS напрямую по `workspace_id` без подзапроса через `sem_nodes`, что критично для производительности (подробнее: ADR-019).

> **Примечание:** Колонка `model_name` в `sem_embeddings` и `kb_embeddings` хранит название модели, сгенерировавшей эмбеддинг. Это необходимо для воспроизводимости (NFR-7) и возможности смены модели без пересчёта всех векторов (ADR-004).

## 7. Правила вывода

1. Компонент AM с зависимостью на `DataStore` MUST быть связан с `Entity` SLPr через `reads`/`writes`.
2. Стадия `Deployment` в prod MUST иметь гейт `approval: human`.
3. ADR, ссылающийся на `deprecated`-компонент AM, получает `requires_review`.

## 8. API Semantic Service

- `POST /v1/nodes`, `GET /v1/nodes/{id}`, `PATCH /v1/nodes/{id}` — CRUD
- `POST /v1/edges`, `DELETE /v1/edges/{id}` — управление связями
- `GET /v1/graph/subgraph?root={id}&depth={n}` — подграф
- `POST /v1/search` — гибридный поиск
- `POST /v1/validate/node` — валидация
- `GET /v1/export?format=jsonld|turtle|graphml` — экспорт

## 9. Использование агентом

1. Agent Runtime получает TaskRun.
2. Определяет релевантные сущности.
3. Запрашивает подграф глубины 1–2.
4. Складывает подграф в контекст как JSON-LD.
5. В артефактах ссылается на сущности по URI.

## Изменения документа

| Версия | Дата | Изменения |
|---|---|---|
| 1.3 | 22.10.2026 | §6.1: зафиксирован маппинг статуса `approved` ↔ `active` (БД) |
| 1.2 | 17.10.2026 | Денормализованный `workspace_id` в `sem_edges`/`sem_embeddings` для RLS; добавлена колонка `model_name` в `sem_embeddings` и `kb_embeddings`; обновлена схема §6.1 |
| 1.1 | 16.10.2026 | Добавлены ADR-016, ADR-017 |
| 1.0 | 16.10.2026 | Первичная спецификация |
