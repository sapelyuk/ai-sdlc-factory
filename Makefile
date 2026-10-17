SHELL = powershell.exe

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

.PHONY: up-local up-full down-local down-full ps-local ps-full logs-local logs-full rebuild-local rebuild-full clean volumes

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

# ── Database ───────────────────────────────────────────────

migrate-up:
	docker compose $(COMPOSE_FILES) run --rm api migrate up

migrate-down:
	docker compose $(COMPOSE_FILES) run --rm api migrate down

# ── Development ────────────────────────────────────────────

dev-api:
	cd apps/api && go run cmd/api-server/main.go

dev-worker:
	cd apps/worker && go run cmd/orchestrator-worker/main.go

dev-web:
	cd apps/web && pnpm dev
