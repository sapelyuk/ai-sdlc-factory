# Profiles
PROFILE ?= local-dev

# Default env file per profile. Must be assigned before the ifneq block below,
# otherwise ENV_FILE is still empty when --env-file is evaluated and the flag
# is never added to COMPOSE_FILES.
ifeq ($(PROFILE),local-dev)
  ENV_FILE ?= deploy/compose/.env.local-dev
else ifeq ($(PROFILE),local-full)
  ENV_FILE ?= deploy/compose/.env.local-full
endif

# Compose files
COMPOSE_FILES = -f deploy/compose/docker-compose.yml
ifneq ($(strip $(ENV_FILE)),)
  COMPOSE_FILES += --env-file $(ENV_FILE)
endif
ifeq ($(PROFILE),local-dev)
  COMPOSE_FILES += -f deploy/compose/docker-compose.local-dev.yml
else ifeq ($(PROFILE),local-full)
  COMPOSE_FILES += -f deploy/compose/docker-compose.local-full.yml
endif

COMPOSE = docker compose $(COMPOSE_FILES)

# Services
API_SERVICE = api
WORKER_SERVICE = worker
WEB_SERVICE = web

.PHONY: up-local up-full down-local down-full ps-local ps-full logs-local logs-full rebuild-local rebuild-full clean volumes migrate-up migrate-down health dev-api dev-worker dev-web temporal-create-namespace temporal-smoke-test

TEMPORAL_NAMESPACE ?= ai-sdlc-factory
TEMPORAL_COMPOSE = docker compose $(COMPOSE_FILES)

# ── Local Dev ──────────────────────────────────────────────

up-local:
	$(COMPOSE) up -d --wait

up-full:
	$(COMPOSE) up -d --wait

ifeq ($(OS),Windows_NT)
temporal-create-namespace:
	@$(TEMPORAL_COMPOSE) exec -T temporal tctl --address temporal:7233 --namespace "$(TEMPORAL_NAMESPACE)" namespace register --retention 24h || $(TEMPORAL_COMPOSE) exec -T temporal tctl --address temporal:7233 --namespace "$(TEMPORAL_NAMESPACE)" namespace describe >NUL
else
temporal-create-namespace:
	@if $(TEMPORAL_COMPOSE) exec -T temporal tctl --address temporal:7233 namespace list | grep -Fq "$(TEMPORAL_NAMESPACE)"; then \
	  echo "Temporal namespace '$(TEMPORAL_NAMESPACE)' already exists"; \
	else \
	  $(TEMPORAL_COMPOSE) exec -T temporal tctl --address temporal:7233 --namespace "$(TEMPORAL_NAMESPACE)" namespace register --retention 24h; \
	fi
endif

temporal-smoke-test:
	bash tests/temporal/smoke_test.sh

down-local:
	$(COMPOSE) down

down-full:
	$(COMPOSE) down

ps-local:
	$(COMPOSE) ps

ps-full:
	$(COMPOSE) ps

logs-local:
	$(COMPOSE) logs -f

logs-full:
	$(COMPOSE) logs -f

rebuild-local:
	$(COMPOSE) up -d --build

rebuild-full:
	$(COMPOSE) up -d --build

# ── Full Stack ─────────────────────────────────────────────

clean:
	$(COMPOSE) down -v --remove-orphans

volumes:
	docker volume prune -f
	docker volume rm $$(docker volume ls -q --filter name=ai-sdlc-factory) 2>/dev/null || true

# ── Health Checks ──────────────────────────────────────────

health:
	@echo "Checking services..."
	@docker inspect --format='{{.Name}}: {{.State.Status}}' $$(docker ps -q) 2>/dev/null || echo "Some services are not running"

# ── Infrastructure Health ──────────────────────────────────

check-infra:
	@echo "Checking Silo..."
	@$(COMPOSE) exec -T silo silo healthcheck ready || exit 1
	@echo "Checking NATS..."
	@curl -sf http://localhost:8222/healthz || exit 1
	@echo "Checking Vault..."
	@$(COMPOSE) exec -T vault vault status || exit 1
	@echo "Checking OPA..."
	@curl -sf http://localhost:8181/health || exit 1
	@echo "All infra services healthy"

# ── Tracing ────────────────────────────────────────────────

check-tracing:
	@echo "Sending test trace..."
	@docker run --rm --network host \
	  ghcr.io/open-telemetry/opentelemetry-collector-contrib/telemetrygen:latest \
	  traces --otlp-endpoint localhost:4317 --otlp-insecure --traces 1
	@echo "Open http://localhost:16686 to see traces"

# ── Database ───────────────────────────────────────────────

MIGRATIONS_DIR ?= migrations/postgres
DB_SERVICE ?= postgres

# The migrate recipes delegate to per-OS scripts so that they work in both
# POSIX shells and Windows cmd.exe/PowerShell. GNU make hands recipes to
# cmd.exe on Windows, where POSIX `for f in ...; do ... done` loops and
# single-quoted `sh -c '...'` bodies are not valid syntax. The scripts
# receive the Compose configuration through the environment.
export COMPOSE_FILES
export MIGRATIONS_DIR
export DB_SERVICE

ifeq ($(OS),Windows_NT)
migrate-up:
	@powershell -NoProfile -ExecutionPolicy Bypass -File scripts/migrate.ps1 up
migrate-down:
	@powershell -NoProfile -ExecutionPolicy Bypass -File scripts/migrate.ps1 down
else
# Apply all .up.sql in ascending order. SQL is piped via stdin because the
# migration files live on the host, not inside the postgres container.
# Credentials are read from the standard POSTGRES_USER/POSTGRES_DB environment
# variables that Compose sets inside the postgres container.
migrate-up:
	@bash scripts/migrate.sh up
# Roll back all .down.sql in descending order (reverse of migrate-up).
migrate-down:
	@bash scripts/migrate.sh down
endif

# ── Development ────────────────────────────────────────────

dev-api:
	cd apps/api && go run cmd/api-server/main.go

dev-worker:
	cd apps/worker && go run cmd/main.go

dev-web:
	cd apps/web && pnpm dev

# ── Semantic Service ─────────────────────────────────────────

# Workspace used by the SLP v0 seed. Override to seed another workspace.
SEED_WORKSPACE_ID ?= 00000000-0000-0000-0000-000000000001
export SEED_WORKSPACE_ID

# Seed SLP v0 ontology into the database.
# Creates the seed workspace if it is missing, then loads the SLP classes.
ifeq ($(OS),Windows_NT)
seed-slp:
	@powershell -NoProfile -ExecutionPolicy Bypass -File scripts/seed-slp.ps1
else
seed-slp:
	@bash scripts/seed-slp.sh
endif


