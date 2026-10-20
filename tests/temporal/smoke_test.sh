#!/usr/bin/env bash
set -euo pipefail

compose=(
  docker compose
  -f deploy/compose/docker-compose.yml
  -f deploy/compose/docker-compose.local-dev.yml
  --env-file deploy/compose/.env.local-dev
)

if ! "${compose[@]}" exec -T temporal tctl --address temporal:7233 namespace list |
  grep -Fq "ai-sdlc-factory"; then
  echo "FAIL: namespace ai-sdlc-factory not found" >&2
  exit 1
fi
echo "PASS: namespace exists"

workflow_id="temporal-persistence-smoke-$(date +%s)-$$"
"${compose[@]}" exec -T temporal tctl --address temporal:7233 \
  --namespace ai-sdlc-factory workflow start \
  --taskqueue temporal-smoke \
  --workflow_id "$workflow_id" \
  --workflow_type temporal-persistence-smoke \
  --execution_timeout 120 >/dev/null
echo "Started persistence-check workflow"

"${compose[@]}" restart temporal >/dev/null
healthy=false
for _ in {1..60}; do
  if "${compose[@]}" exec -T temporal tctl --address temporal:7233 cluster health >/dev/null 2>&1; then
    healthy=true
    break
  fi
  sleep 2
done
if [[ "$healthy" != true ]]; then
  echo "FAIL: Temporal did not recover after restart" >&2
  exit 1
fi

description=$("${compose[@]}" exec -T temporal tctl --address temporal:7233 \
  --namespace ai-sdlc-factory workflow describe --workflow_id "$workflow_id")
if ! grep -Fq '"status": "Running"' <<<"$description"; then
  echo "FAIL: workflow was not recovered after restart" >&2
  exit 1
fi
echo "PASS: workflow recovered after restart"

ui_address=$("${compose[@]}" port temporal-ui 8080)
ui_port=${ui_address##*:}
if ! curl -fsS "http://localhost:${ui_port}/" >/dev/null; then
  echo "FAIL: Temporal UI is not responding" >&2
  exit 1
fi
echo "PASS: Temporal UI responds"
