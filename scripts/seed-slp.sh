#!/usr/bin/env bash
# Seed the SLP v0 ontology: ensure the seed workspace row, then load the classes.
# Reads COMPOSE_FILES, DB_SERVICE, SEED_WORKSPACE_ID from environment
# (exported by Makefile). POSIX counterpart of scripts/seed-slp.ps1.
set -euo pipefail

COMPOSE_CMD="docker compose ${COMPOSE_FILES:?COMPOSE_FILES must be exported by Makefile}"
DB_SERVICE="${DB_SERVICE:-postgres}"
SEED_WORKSPACE_ID="${SEED_WORKSPACE_ID:?SEED_WORKSPACE_ID must be exported by Makefile}"
SEED_FILE="${SEED_FILE:-deploy/compose/postgres/seed/default-workspace.sql}"

# Credentials are read from the standard POSTGRES_USER/POSTGRES_DB environment
# variables that Compose sets inside the postgres container. The seed file
# lives on the host, so it is piped via stdin.
$COMPOSE_CMD exec -T "$DB_SERVICE" sh -c 'psql -v ON_ERROR_STOP=1 -v workspace_id="'"$SEED_WORKSPACE_ID"'" -U "$POSTGRES_USER" -d "$POSTGRES_DB"' < "$SEED_FILE"

$COMPOSE_CMD exec -T -e SEED_WORKSPACE_ID="$SEED_WORKSPACE_ID" semantic /app/seed-slp
