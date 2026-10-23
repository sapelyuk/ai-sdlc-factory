# AI-SDLC Factory — Rego Policies

OPA (Open Policy Agent) policies for access control and risk management in the AI-SDLC platform.

## Structure

| File | Purpose |
|---|---|
| `autonomy.rego` | Autonomy levels R0–R3 (ADR-013), action allowance checks |
| `risk_levels.rego` | Risk assessment based on task category and context |

## Usage

```bash
# Check autonomy
curl -X POST http://localhost:8181/v1/data/ai_sdlc_factory/autonomy/allow \
  -d '{"input":{"action":"deploy.prod","autonomy_level":"R3"}}'

# Check risk
curl -X POST http://localhost:8181/v1/data/ai_sdlc_factory/risk_levels/risk_score \
  -d '{"input":{"category":"deployment","target":"production"}}'
```

## Integration

OPA runs as a service in the Compose stack:
- Port: `8181`
- Policies are mounted from `packages/policy/` into `/policies`
- Decision logs are output to console (`decision_logs.console=true`)

## References

- ADR-013: Autonomy Levels R0–R3
- SYSTEM_DESIGN.md §10: Security
- OPA Docs: https://www.openpolicyagent.org/docs/
