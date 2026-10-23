#!/usr/bin/env bash
#
# tests/infra/test-otel-jaeger.sh
#
# Integration test: OTel Collector → Jaeger.
# Verifies that a trace sent via OTLP appears in the Jaeger UI API.
#
# Usage:
#   bash tests/infra/test-otel-jaeger.sh [local-dev|local-full]
#
# Graceful skip when Docker is unavailable.

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

# ── Docker check ───────────────────────────────────────────────────
if ! command -v docker &>/dev/null || ! docker info &>/dev/null; then
  echo "SKIP: Docker is unavailable (skipping test)"
  exit 0
fi

# ── Cleanup previous run ───────────────────────────────────────────
echo ">>> Cleanup..."
$COMPOSE down --remove-orphans 2>/dev/null || true

# ── Start OTel Collector + Jaeger ──────────────────────────────────
echo ">>> Starting otel-collector and jaeger (profile: $PROFILE)..."
$COMPOSE up -d otel-collector jaeger

# ── Wait for healthy ───────────────────────────────────────────────
echo ">>> Waiting for healthy..."
MAX_RETRIES=30
RETRY=0
while [ $RETRY -lt $MAX_RETRIES ]; do
  OTEL_STATUS=$($COMPOSE ps otel-collector 2>/dev/null | grep -c "healthy" || true)
  JAEGER_STATUS=$($COMPOSE ps jaeger 2>/dev/null | grep -c "healthy" || true)
  if [ "$OTEL_STATUS" -ge 1 ] && [ "$JAEGER_STATUS" -ge 1 ]; then
    echo ">>> Services healthy"
    break
  fi
  RETRY=$((RETRY + 1))
  sleep 2
done

if [ $RETRY -ge $MAX_RETRIES ]; then
  echo "FAIL: Services did not become healthy within $((MAX_RETRIES * 2))s"
  $COMPOSE logs otel-collector jaeger
  $COMPOSE down
  exit 1
fi

# ── Send test trace ────────────────────────────────────────────────
echo ">>> Sending test trace..."
if ! docker run --rm --network host \
    ghcr.io/open-telemetry/opentelemetry-collector-contrib/telemetrygen:latest \
    traces --otlp-endpoint localhost:4317 --otlp-insecure \
           --duration 2s --traces 1 2>/dev/null; then
  echo "WARN: telemetrygen failed to send trace (check port 4317)"
  # Non-fatal — telemetrygen may not be available yet
fi

# ── Wait for trace processing ──────────────────────────────────────
# Jaeger needs time for indexing the trace (batch processor + indexing)
sleep 5

# ── Check Jaeger API ───────────────────────────────────────────────
# 1. Check that the API responds
echo ">>> Checking Jaeger API (/api/services)..."
SERVICES_RESPONSE=$(curl -sf "http://localhost:16686/api/services" 2>/dev/null || echo "")

if [ -z "$SERVICES_RESPONSE" ]; then
  echo "FAIL: Jaeger API does not respond at http://localhost:16686/api/services"
  $COMPOSE logs jaeger
  $COMPOSE down
  exit 1
fi

echo ">>> Jaeger API response:"
echo "$SERVICES_RESPONSE"

# Verify that at least one service is listed
SERVICE_COUNT=$(echo "$SERVICES_RESPONSE" | grep -c '"name"' || true)
if [ "$SERVICE_COUNT" -lt 1 ]; then
  echo "FAIL: No services in Jaeger (trace did not arrive)"
  $COMPOSE down
  exit 1
fi

echo ">>> Found services: $SERVICE_COUNT"

# 2. REAL CHECK: look for the trace via /api/traces
# Search for traces from the "telemetrygen" service (default name for telemetrygen)
echo ">>> Checking Jaeger API (/api/traces) — searching for sent trace..."
TRACES_RESPONSE=$(curl -sf "http://localhost:16686/api/traces?service=telemetrygen&limit=10" 2>/dev/null || echo "")

if [ -z "$TRACES_RESPONSE" ]; then
  echo "WARN: /api/traces did not respond (trace may still be indexing)"
  # Retry after 3 sec
  sleep 3
  TRACES_RESPONSE=$(curl -sf "http://localhost:16686/api/traces?service=telemetrygen&limit=10" 2>/dev/null || echo "")
fi

if [ -z "$TRACES_RESPONSE" ]; then
  echo "FAIL: Jaeger /api/traces not responding — trace not found"
  echo ">>> Jaeger logs:"
  $COMPOSE logs jaeger
  $COMPOSE down
  exit 1
fi

# Verify at least one trace was found
TRACE_COUNT=$(echo "$TRACES_RESPONSE" | grep -c '"traceID"' || true)
if [ "$TRACE_COUNT" -lt 1 ]; then
  echo "FAIL: No telemetrygen traces in Jaeger (OTel → Jaeger pipeline broken)"
  echo ">>> /api/traces response:"
  echo "$TRACES_RESPONSE"
  $COMPOSE down
  exit 1
fi

echo ">>> Found telemetrygen traces: $TRACE_COUNT"
echo ">>> Test passed: trace sent and found in Jaeger"

# ── Cleanup ──────────────────────────────────────────────────────────
echo ">>> Cleanup..."
$COMPOSE down

echo ">>> Done."
