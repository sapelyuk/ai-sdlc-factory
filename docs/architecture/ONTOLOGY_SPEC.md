# Semantic Layer Specification (SLP / SLPr / AM) — ai-sdlc-factory

**Version 1.1 · 16.10.2026**

## 1. Overview

The semantic layer is a formal machine-readable knowledge model. Consists of three loops:

| Loop | Purpose | Scope |
|---|---|---|
| **SLP** | Ontology of the `ai-sdlc-factory` platform | Global, workspace |
| **SLPr** | Domain model of a specific product | One per project |
| **AM** | C4 model of the product | One per project (versioned) |

## 2. Formats

### 2.1 Notations

Ontologies: **Turtle/OWL-compatible YAML** + **JSON-LD** (export).
Instances: **property graph in PostgreSQL** + export to **JSON-LD / RDF / GraphML**.

### 2.2 Namespace

- SLP: `ai-sdlc-factory/slp@<semver>`
- SLPr: `ai-sdlc-factory/product-domain@<semver>`
- AM: `ai-sdlc-factory/am@<semver>`

### 2.3 Versioning

Semver. Major changes — R3 (human approval).

## 3. SLP — platform ontology

### 3.1 Classes

| Class | Description |
|---|---|
| `Role` | Agent template |
| `Team` | Agent set + topology + process |
| `Agent` | Role instance |
| `Process` | SDLC workflow |
| `Stage` | Process stage |
| `Gate` | Checkpoint |
| `ArtifactType` | Artifact type |
| `Tool` | MCP tool |
| `ModelProfile` | Capability profile (pins model version, not alias — ADR-021) |
| `Capability` | Role capability |
| `KnowledgeBase` | Knowledge base |
| `Scope` | Knowledge scope |
| `Policy` | Policy |
| `AutonomyLevel` | Autonomy level |

## 4. SLPr — product domain

### 4.2 Typical classes

Example for an online store: `Customer`, `Order`, `Product`, `Payment`, `Shipment`, `LineItem`.

### 4.4 Business rules

Documented as SHACL-like rules in the project ontology; validated by Semantic Service.

## 5. AM — architecture model

### 5.1 Classes (C4)

`System`, `Container`, `Component`, `DataStore`, `Interface`, `Dependency`, `DeploymentNode`, `Environment`.

### 5.3 Source of truth

AM is stored in Semantic Service. Exported to **Structurizr DSL** (primary format).

## 6. Storage

### 6.1 Schema (PostgreSQL)

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

Row Level Security by `workspace_id`.

> **Note:** In Issue and UI terminology, the status "approved" corresponds to the value `active` in the database (CHECK in migration `0005_init_semantic_graph`). Mapping: `approved` ↔ `active`. A node in `active` status is immutable: `PATCH /v1/nodes/{id}` returns 403.

> **Note:** `sem_edges` and `sem_embeddings` contain denormalized `workspace_id` (reference to `workspaces`) for RLS policy optimization. This allows applying RLS directly by `workspace_id` without a subquery through `sem_nodes`, which is critical for performance (details: ADR-019).

> **Note:** The `model_name` column in `sem_embeddings` and `kb_embeddings` stores the name of the model that generated the embedding. This is needed for reproducibility (NFR-7) and the ability to change the model without recalculating all vectors (ADR-004).

## 7. Inference rules

1. An AM Component with a dependency on `DataStore` MUST be linked to an SLPr `Entity` via `reads`/`writes`.
2. A `Deployment` stage in prod MUST have a gate `approval: human`.
3. An ADR referencing a `deprecated` AM component receives `requires_review`.

## 8. Semantic Service API

- `POST /v1/nodes`, `GET /v1/nodes/{id}`, `PATCH /v1/nodes/{id}` — CRUD
- `POST /v1/edges`, `DELETE /v1/edges/{id}` — edge management
- `GET /v1/graph/subgraph?root={id}&depth={n}` — subgraph
- `POST /v1/search` — hybrid search
- `POST /v1/validate/node` — validation
- `GET /v1/export?format=jsonld|turtle|graphml` — export

## 9. Agent usage

1. Agent Runtime receives a TaskRun.
2. Determines relevant entities.
3. Requests a subgraph of depth 1–2.
4. Puts the subgraph into context as JSON-LD.
5. References entities by URI in artifacts.

## Document changes

| Version | Date | Changes |
|---|---|---|
| 1.3 | 22.10.2026 | §6.1: status mapping `approved` ↔ `active` (DB) fixed |
| 1.2 | 17.10.2026 | Denormalized `workspace_id` in `sem_edges`/`sem_embeddings` for RLS; added `model_name` column to `sem_embeddings` and `kb_embeddings`; updated §6.1 schema |
| 1.1 | 16.10.2026 | Added ADR-016, ADR-017 |
| 1.0 | 16.10.2026 | Initial specification |
