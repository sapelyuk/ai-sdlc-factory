# Worker — Agent Runtime

Temporal-based agent runtime for `ai-sdlc-factory`.

## What it does

- **TaskRunWorkflow** — full ReAct loop for one agent on one task.
- **AgentStep** — one iteration: LLM → (optional) tool → observe.
- **Context Builder** — semantic subgraph + KB search.
- **Tool Executor** — via Tool Gateway (MCP).
- **Budget Guard** — hard stop on cost/steps.
- **Handoff Builder** — validates LLM output against JSON schema.
- **SDLCWorkflow** — minimal: single `TaskRunWorkflow` child call.

## Architecture

```
SDLCWorkflow (child)
  └─ TaskRunWorkflow
       └─ AgentStepActivity (loop)
            ├─ LLM Gateway (chat)
            ├─ Tool Gateway (MCP)
            ├─ Semantic Service (subgraph)
            ├─ Knowledge Service (search)
            └─ BudgetGuard (hard stop)
```

## Configuration

| Env | Default | Description |
|---|---|---|
| `TEMPORAL_HOST` | `temporal:7233` | Temporal gRPC address |
| `AI_TEMPORAL_TASK_QUEUE` | `ai-sdlc-factory` | Task queue name |
| `LLM_GATEWAY_URL` | `http://gateway-llm:8040` | LLM Gateway base URL |
| `TOOL_GATEWAY_URL` | `http://tool-gateway:8030` | Tool Gateway base URL |
| `SEMANTIC_URL` | `http://semantic:8010` | Semantic Service URL |
| `KNOWLEDGE_URL` | `http://knowledge:8020` | Knowledge Service URL |
| `DEFAULT_MAX_COST_USD` | `10.0` | Default cost budget |
| `DEFAULT_MAX_STEPS` | `50` | Default step budget |

## Structure

```
cmd/worker/main.go          — entry point, Temporal worker setup
internal/config/config.go   — environment configuration
internal/client/            — thin HTTP wrappers for downstream services
internal/agent/loop.go      — ReAct executor
internal/agent/budget.go    — hard-stop budget guard
internal/context/builder.go — semantic + knowledge context assembly
internal/handoff/builder.go — handoff package builder
internal/handoff/validate.go — JSON schema validation
internal/activity/agent_step.go — Temporal activity
internal/workflow/          — TaskRunWorkflow, SDLCWorkflow
```

## Testing

```bash
go test ./... -v -cover
```

Requires Go 1.26+.

## References

- ADR-002 (Temporal), ADR-005 (MCP), ADR-014 (Orchestration), ADR-021 (Cost attribution)
- SYSTEM_DESIGN.md §8 (Orchestration)
