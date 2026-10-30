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
| `AI_GOOGLE_API_KEY` | — | Google (Gemini) API key |
| `AI_GOOGLE_BASE_URL` | `https://generativelanguage.googleapis.com/v1beta/openai` | Gemini OpenAI-compat endpoint |
| `AI_DEEPSEEK_API_KEY` | — | DeepSeek API key |
| `AI_DEEPSEEK_BASE_URL` | `https://api.deepseek.com` | DeepSeek OpenAI-compat endpoint |
| `AI_GROQ_API_KEY` | — | Groq API key |
| `AI_GROQ_BASE_URL` | `https://api.groq.com/openai/v1` | Groq OpenAI-compat endpoint |

## Providers

| Provider (`llm_model_profiles.provider`) | Env var(s) | Adapter | API style | Version pinning |
|---|---|---|---|---|
| `anthropic` | `AI_ANTHROPIC_API_KEY` | `providers/anthropic.go` | Native Messages API | Pinned (`claude-*-20250101`) |
| `openai` | `AI_OPENAI_API_KEY` | `providers/openai.go` | Native Chat Completions | Pinned |
| `google` | `AI_GOOGLE_API_KEY`, `AI_GOOGLE_BASE_URL` | `providers/openai.go` (reused) | OpenAI-compat layer | Stable model names |
| `deepseek` | `AI_DEEPSEEK_API_KEY`, `AI_DEEPSEEK_BASE_URL` | `providers/openai.go` (reused) | OpenAI-compat | Alias only (see below) |
| `groq` | `AI_GROQ_API_KEY`, `AI_GROQ_BASE_URL` | `providers/openai.go` (reused) | OpenAI-compat | Alias only (see below) |
| `ollama` | `AI_OLLAMA_BASE_URL` | `providers/ollama.go` | Native Ollama `/api/chat` | Local tags |

Gemini, DeepSeek and Groq speak the OpenAI Chat Completions wire format, so they
reuse the OpenAI adapter through `providers.NewOpenAICompat(name, key, baseURL)`
— one implementation, one error-classification path, no per-provider duplication
(ADR-008). The name argument makes errors, `llm_attempts` and audit rows
attribute to the real provider instead of `openai`.

**Credential filtering.** `catalog.Load` drops every `llm_model_profiles` row
whose provider has no credentials in this deployment (for Ollama the credential
is `AI_OLLAMA_BASE_URL`, not a key). Startup does not fail and the router never
tries an unreachable provider; each skipped provider is logged once:
`skipping provider: credentials not configured`.

**Activating a new provider.** Migration `0015_seed_llm_providers` seeds Gemini,
DeepSeek and Groq with `enabled = false`. After provisioning the key:

```sql
UPDATE llm_model_profiles SET enabled = true WHERE provider IN ('google', 'deepseek', 'groq');
```

Catalog rows refresh within 5 minutes without a restart.

## Known Limitations

- **DeepSeek and Groq expose no pinned model versions.** Both publish only
  floating aliases (`deepseek-flash`, `deepseek-v4-pro`, `openai/gpt-oss-20b`,
  `openai/gpt-oss-120b`). ADR-021 requires pinned `model_id` values; the
  provider-documented alias is used as the stable identifier instead, and
  `catalog.Load` still rejects the `-latest` suffix. Re-pinning is the
  provider's action, not ours, so a profile change may need an R2 review when a
  vendor renames an alias.
- **Google stable names are not timestamped.** `gemini-2.5-pro` and
  `gemini-2.5-flash` are version-stable identifiers per the Gemini model
  lifecycle (only `-latest` aliases hot-swap), so they satisfy ADR-021 without a
  `-YYYYMMDD` suffix.
- **DeepSeek price is time-dependent.** The catalog stores the off-peak rate;
  peak hours (01:00–04:00 and 06:00–10:00 UTC, Mon–Fri, excluding Chinese public
  holidays) cost 2x, so `total_cost_usd` may understate spend during peak.
- **Gemini 2.5 Pro price is tiered.** The stored rate ($1.25 / $10.00 per MTok)
  applies to prompts ≤ 200k tokens; larger prompts are billed $2.50 / $15.00.
- **Prices drift.** All seeded prices were verified against provider docs on
  2026-10-29 and are an accounting input, not a billing contract (NFR-9).


## Architecture

- **Model Catalog** (`llm_model_profiles`): global catalog of pinned model versions, loaded with 5-min in-memory cache. `-latest` aliases are rejected at startup (ADR-021). Rows whose provider has no credentials are skipped at load time (issue #112).
- **Routing**: for each request, loads the profile's models ordered by priority, filters out cooldown entries, and tries them in order.
- **Error classification**:
  - `404 / 401 / 403 / 400` → config/auth defect, no cooldown, logged as alert.
  - `429 / 5xx / timeout` → availability, cooldown with exponential backoff (60s → 4h, jitter).
- **Dual-level cost**: `final_cost_usd` (successful attempt) ≠ `total_cost_usd` (sum of all attempts). Partial usage from retries is captured via `UsageError`.
- **RLS**: logical calls are scoped to `workspace_id` via PostgreSQL Row Level Security; attempts are linked to logical calls via `logical_call_id` and do not use RLS.

## Testing

```bash
# Unit tests (no Docker required)
go test ./internal/domain/ ./internal/providers/ ./internal/config/ ./internal/routing/ ./internal/transport/http/

# Integration tests (requires Docker)
go test -run TestFallbackOn404_NoCooldown ./internal/routing/
go test -run TestDualCost ./internal/routing/
go test -run TestRLS_Isolation ./internal/routing/
go test -run TestLoad ./internal/catalog/
```

`go test ./internal/catalog/` runs the credential-filter tests against
PostgreSQL 18 seeded by migrations `0010` + `0015`; they skip when Docker is
unavailable outside CI.

## References

- ADR-021 (routing, fallback, cost attribution)
- SYSTEM_DESIGN.md §8.5, §11.4
- NFR-9 (cost accounting), FR-OPS-2
