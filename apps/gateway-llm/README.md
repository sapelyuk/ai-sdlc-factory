# ai-sdlc-factory — LLM Gateway

Service that routes LLM chat requests by capability profile (`code-strong`, `reasoning-strong`, etc.), provides fallback per `(provider, model)`, and accounts for cost at two levels (ADR-021).

## Endpoints

| Method | Path | Auth | Description |
|---|---|---|---|
| GET | `/healthz` | — | Liveness probe |
| GET | `/readyz` | — | Readiness probe (pings PostgreSQL) |
| POST | `/v1/llm/chat` | X-Workspace-ID + X-Author-ID | Chat completion with routing |

### POST /v1/llm/chat

```http
POST /v1/llm/chat
X-Workspace-ID: <uuid>
X-Author-ID: <uuid>
Content-Type: application/json
```

```json
{
  "profile": "code-strong",
  "messages": [
    {"role": "user", "content": "Write a function to sort an array."},
    {"role": "assistant", "content": "Here is the function:"}
  ],
  "tools": [],
  "run_id": "<uuid>",
  "task_id": "<uuid>",
  "agent_id": "<uuid>"
}
```

Response:

```json
{
  "logical_call_id": "<uuid>",
  "status": "succeeded",
  "model_used": "claude-sonnet-4-20250101",
  "provider_used": "anthropic",
  "content": "function sort(arr) { ... }",
  "usage": {
    "tokens_in": 50,
    "tokens_out": 200,
    "cost_usd": 0.0008,
    "total_tokens_in": 150,
    "total_tokens_out": 200,
    "total_cost_usd": 0.0044
  }
}
```

- `cost_usd` = cost of the successful attempt (`final_cost_usd` per ADR-021).
- `total_cost_usd` = sum of all attempt costs including retries (actual spend, NFR-9).

## Configuration

| Variable | Default | Description |
|---|---|---|
| `DATABASE_URL` | `postgres://postgres:postgres@localhost:5432/ai_sdlc_factory?sslmode=disable` | PostgreSQL DSN |
| `PORT` | `8040` | HTTP listen port |
| `AI_ANTHROPIC_API_KEY` | — | Anthropic API key |
| `AI_OPENAI_API_KEY` | — | OpenAI API key |
| `AI_OLLAMA_BASE_URL` | `http://localhost:11434` | Local Ollama URL |

## Architecture

- **Model Catalog** (`llm_model_profiles`): global catalog of pinned model versions, loaded with 5-min in-memory cache. `-latest` aliases are rejected at startup (ADR-021).
- **Routing**: for each request, loads the profile's models ordered by priority, filters out cooldown entries, and tries them in order.
- **Error classification**:
  - `404 / 401 / 403 / 400` → config/auth defect, no cooldown, logged as alert.
  - `429 / 5xx / timeout` → availability, cooldown with exponential backoff (60s → 4h, jitter).
- **Dual-level cost**: `final_cost_usd` (successful attempt) ≠ `total_cost_usd` (sum of all attempts). Partial usage from retries is captured via `UsageError`.
- **RLS**: logical calls are scoped to `workspace_id` via PostgreSQL Row Level Security; attempts are linked to logical calls via `logical_call_id` and do not use RLS.

## Testing

```bash
# Unit tests (no Docker required)
go test ./internal/domain/ ./internal/providers/ ./internal/routing/ ./internal/transport/http/

# Integration tests (requires Docker)
go test -run TestFallbackOn404_NoCooldown ./internal/routing/
go test -run TestDualCost ./internal/routing/
go test -run TestRLS_Isolation ./internal/routing/
```

## References

- ADR-021 (routing, fallback, cost attribution)
- SYSTEM_DESIGN.md §8.5, §11.4
- NFR-9 (cost accounting), FR-OPS-2
