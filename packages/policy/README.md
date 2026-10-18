# AI-SDLC Factory — Rego Policies

OPA (Open Policy Agent) policies для управления доступом и рисками AI-SDLC platform.

## Структура

| Файл | Назначение |
|---|---|
| `autonomy.rego` | Уровни автономности R0–R3 (ADR-013), проверка разрешённости действий |
| `risk_levels.rego` | Оценка риска на основе категории и контекста задачи |

## Использование

```bash
# Проверка автономности
curl -X POST http://localhost:8181/v1/data/ai_sdlc_factory/autonomy/allow \
  -d '{"input":{"action":"deploy.prod","autonomy_level":"R3"}}'

# Оценка риска
curl -X POST http://localhost:8181/v1/data/ai_sdlc_factory/risk_levels/risk_score \
  -d '{"input":{"category":"deployment","target":"production"}}'
```

## Интеграция

OPA запускается как сервис в Compose-стэке:
- Порт: `8181`
- Policies монтируются из `packages/policy/` в `/policies`
- Decision logs выводятся в консоль (`decision_logs.console=true`)

## Ссылки

- ADR-013: Уровни автономности R0–R3
- SYSTEM_DESIGN.md §10: Безопасность
- OPA Docs: https://www.openpolicyagent.org/docs/
