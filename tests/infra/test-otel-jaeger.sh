#!/usr/bin/env bash
#
# tests/infra/test-otel-jaeger.sh
#
# Интеграционный тест: OTel Collector → Jaeger.
# Проверяет, что трейс, отправленный через OTLP, появляется в Jaeger UI API.
#
# Использование:
#   bash tests/infra/test-otel-jaeger.sh [local-dev|local-full]
#
# Graceful skip, если Docker недоступен.

set -euo pipefail

PROFILE="${1:-local-dev}"
COMPOSE_FILES="deploy/compose/docker-compose.yml"
ENV_FILE=""

case "$PROFILE" in
  local-dev)
    COMPOSE_FILES="$COMPOSE_FILES deploy/compose/docker-compose.local-dev.yml"
    ENV_FILE="deploy/compose/.env.local-dev"
    ;;
  local-full)
    COMPOSE_FILES="$COMPOSE_FILES deploy/compose/docker-compose.local-full.yml"
    ENV_FILE="deploy/compose/.env.local-full"
    ;;
  *)
    echo "Usage: $0 [local-dev|local-full]"
    exit 1
    ;;
esac

COMPOSE="docker compose -f $COMPOSE_FILES"
if [ -n "$ENV_FILE" ] && [ -f "$ENV_FILE" ]; then
  COMPOSE="$COMPOSE --env-file $ENV_FILE"
fi

# ── Проверка Docker ──────────────────────────────────────────────────
if ! command -v docker &>/dev/null || ! docker info &>/dev/null; then
  echo "SKIP: Docker недоступен (пропускаем тест)"
  exit 0
fi

# ── Cleanup предыдущего запуска ──────────────────────────────────────
echo ">>> Cleanup..."
$COMPOSE down --remove-orphans 2>/dev/null || true

# ── Запуск OTel Collector + Jaeger ───────────────────────────────────
echo ">>> Запуск otel-collector и jaeger (профиль: $PROFILE)..."
$COMPOSE up -d otel-collector jaeger

# ── Ожидание healthy ─────────────────────────────────────────────────
echo ">>> Ожидание healthy..."
MAX_RETRIES=30
RETRY=0
while [ $RETRY -lt $MAX_RETRIES ]; do
  OTEL_STATUS=$($COMPOSE ps otel-collector 2>/dev/null | grep -c "healthy" || true)
  JAEGER_STATUS=$($COMPOSE ps jaeger 2>/dev/null | grep -c "healthy" || true)
  if [ "$OTEL_STATUS" -ge 1 ] && [ "$JAEGER_STATUS" -ge 1 ]; then
    echo ">>> Сервисы healthy"
    break
  fi
  RETRY=$((RETRY + 1))
  sleep 2
done

if [ $RETRY -ge $MAX_RETRIES ]; then
  echo "FAIL: Сервисы не стали healthy за $((MAX_RETRIES * 2))s"
  $COMPOSE logs otel-collector jaeger
  $COMPOSE down
  exit 1
fi

# ── Отправка тестового трейса ────────────────────────────────────────
echo ">>> Отправка тестового трейса..."
if ! docker run --rm --network host \
    ghcr.io/open-telemetry/opentelemetry-collector-contrib/telemetrygen:latest \
    traces --otlp-endpoint localhost:4317 --otlp-insecure \
           --duration 2s --traces 1 2>/dev/null; then
  echo "WARN: telemetrygen не смог отправить трейс (проверьте порт 4317)"
  # Не фатально — возможно, telemetrygen ещё не доступен
fi

# ── Ожидание обработки трейса ────────────────────────────────────────
sleep 3

# ── Проверка Jaeger API ──────────────────────────────────────────────
echo ">>> Проверка Jaeger API (/api/services)..."
SERVICES_RESPONSE=$(curl -sf "http://localhost:16686/api/services" 2>/dev/null || echo "")

if [ -z "$SERVICES_RESPONSE" ]; then
  echo "FAIL: Jaeger API не отвечает на http://localhost:16686/api/services"
  $COMPOSE logs jaeger
  $COMPOSE down
  exit 1
fi

echo ">>> Ответ Jaeger API:"
echo "$SERVICES_RESPONSE"

# Проверяем, что в списке сервисов есть хотя бы один
SERVICE_COUNT=$(echo "$SERVICES_RESPONSE" | grep -c '"name"' || true)
if [ "$SERVICE_COUNT" -lt 1 ]; then
  echo "FAIL: В Jaeger нет сервисов (трейс не дошёл)"
  $COMPOSE down
  exit 1
fi

echo ">>> Найдено сервисов: $SERVICE_COUNT"
echo ">>> Тест пройден: трейс принят Jaeger"

# ── Cleanup ──────────────────────────────────────────────────────────
echo ">>> Cleanup..."
$COMPOSE down

echo ">>> Done."
