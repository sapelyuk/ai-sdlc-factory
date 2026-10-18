# Profiles
PROFILE ?= local-dev

# Compose files
COMPOSE_FILES = -f deploy/compose/docker-compose.yml
ifneq ($(strip $(ENV_FILE)),)
  COMPOSE_FILES += --env-file $(ENV_FILE)
endif
ifeq ($(PROFILE),local-dev)
  COMPOSE_FILES += -f deploy/compose/docker-compose.local-dev.yml
  ENV_FILE ?= deploy/compose/.env.local-dev
else ifeq ($(PROFILE),local-full)
  COMPOSE_FILES += -f deploy/compose/docker-compose.local-full.yml
  ENV_FILE ?= deploy/compose/.env.local-full
endif

COMPOSE = docker compose $(COMPOSE_FILES)

# Services
API_SERVICE = api
WORKER_SERVICE = worker
WEB_SERVICE = web

.PHONY: up-local up-full down-local down-full ps-local ps-full logs-local logs-full rebuild-local rebuild-full clean volumes migrate-up migrate-down health dev-api dev-worker dev-web

# ── Local Dev ──────────────────────────────────────────────

up-local:
	$(COMPOSE) up -d --wait

up-full:
	$(COMPOSE) up -d --wait

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
	@echo "Checking MinIO..."
	@curl -sf http://localhost:9000/minio/health/live || exit 1
	@echo "Checking NATS..."
	@curl -sf http://localhost:8222/healthz || exit 1
	@echo "Checking Vault..."
	@curl -sf http://localhost:8200/v1/sys/health || exit 1
	@echo "Checking OPA..."
	@curl -sf http://localhost:8181/health || exit 1
	@echo "All infra services healthy"

# ── Database ───────────────────────────────────────────────

MIGRATIONS_DIR ?= migrations/postgres
DB_SERVICE ?= postgres

# Apply all .up.sql in ascending order. SQL is piped via stdin because the
# migration files live on the host, not inside the postgres container.
# Credentials are read from the standard POSTGRES_USER/POSTGRES_DB environment
# variables that Compose sets inside the postgres container.
migrate-up:
	@for f in $$(ls $(MIGRATIONS_DIR)/*.up.sql 2>/dev/null | sort); do \
		echo "==> Applying $$f"; \
		$(COMPOSE) exec -T $(DB_SERVICE) sh -c 'psql -v ON_ERROR_STOP=1 -U "$$POSTGRES_USER" -d "$$POSTGRES_DB"' < "$$f" || exit 1; \
	done
	@echo "==> All migrations applied"

# Roll back all .down.sql in descending order (reverse of migrate-up).
migrate-down:
	@for f in $$(ls $(MIGRATIONS_DIR)/*.down.sql 2>/dev/null | sort -r); do \
		echo "==> Rolling back $$f"; \
		$(COMPOSE) exec -T $(DB_SERVICE) sh -c 'psql -v ON_ERROR_STOP=1 -U "$$POSTGRES_USER" -d "$$POSTGRES_DB"' < "$$f" || exit 1; \
	done
	@echo "==> All migrations rolled back"

# ── Development ────────────────────────────────────────────

dev-api:
	cd apps/api && go run cmd/api-server/main.go

dev-worker:
	cd apps/worker && go run cmd/orchestrator-worker/main.go

dev-web:
	cd apps/web && pnpm dev
